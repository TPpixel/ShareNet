using System;
using System.Collections.Concurrent;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;

namespace ShareNetUSB
{
    /// <summary>
    /// Lightweight loopback SOCKS5 TCP server with Cloudflare DoH (1.1.1.1) resolution
    /// exposed over 'adb reverse tcp:10808 tcp:10808' for the phone's HotspotVPN mode.
    /// </summary>
    internal sealed class Socks5Server
    {
        private sealed class DnsCacheEntry
        {
            public IPAddress Ip;
            public DateTime ExpiresAtUtc;
        }

        private static readonly ConcurrentDictionary<string, DnsCacheEntry> DnsCache =
            new ConcurrentDictionary<string, DnsCacheEntry>(StringComparer.OrdinalIgnoreCase);

        private static readonly Regex DohAnswerRegex =
            new Regex("\"type\"\\s*:\\s*1\\s*,[^}]*?\"data\"\\s*:\\s*\"(\\d+\\.\\d+\\.\\d+\\.\\d+)\"", RegexOptions.Compiled);

        private readonly int _port;
        private TcpListener _listener;
        private volatile bool _running;
        private Thread _acceptThread;

        public Socks5Server(int port)
        {
            _port = port;
        }

        public void Start()
        {
            if (_running) return;
            try
            {
                ServicePointManager.SecurityProtocol |= (SecurityProtocolType)3072; // TLS 1.2
            }
            catch { }

            _listener = new TcpListener(IPAddress.Loopback, _port);
            _listener.Start(128);
            _running = true;

            _acceptThread = new Thread(AcceptLoop);
            _acceptThread.IsBackground = true;
            _acceptThread.Name = "ShareNetUsbSocks5";
            _acceptThread.Start();
        }

        public void Stop()
        {
            _running = false;
            try
            {
                if (_listener != null) _listener.Stop();
            }
            catch { }
            _listener = null;
        }

        private void AcceptLoop()
        {
            while (_running && _listener != null)
            {
                try
                {
                    TcpClient client = _listener.AcceptTcpClient();
                    ThreadPool.QueueUserWorkItem(HandleClient, client);
                }
                catch
                {
                    if (!_running) break;
                }
            }
        }

        private static void HandleClient(object state)
        {
            TcpClient client = (TcpClient)state;
            TcpClient remote = null;
            try
            {
                client.NoDelay = true;
                client.ReceiveTimeout = 30000;
                client.SendTimeout = 30000;
                client.ReceiveTimeout = 15000;
                client.SendTimeout = 15000;
                NetworkStream cin = client.GetStream();

                int ver = cin.ReadByte();
                int nMethods = cin.ReadByte();
                if (ver != 0x05 || nMethods <= 0 || nMethods > 64) return;

                byte[] methods = ReadExact(cin, nMethods);
                bool noAuth = false;
                for (int i = 0; i < methods.Length; i++)
                {
                    if (methods[i] == 0x00) { noAuth = true; break; }
                }
                if (!noAuth)
                {
                    cin.Write(new byte[] { 0x05, 0xFF }, 0, 2);
                    return;
                }
                cin.Write(new byte[] { 0x05, 0x00 }, 0, 2);

                byte[] reqHdr = ReadExact(cin, 4);
                if (reqHdr[0] != 0x05 || reqHdr[1] != 0x01)
                {
                    cin.Write(new byte[] { 0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0 }, 0, 10);
                    return;
                }

                string targetHost;
                IPAddress directIp = null;
                int atyp = reqHdr[3];
                if (atyp == 0x01)
                {
                    byte[] ip4 = ReadExact(cin, 4);
                    directIp = new IPAddress(ip4);
                    targetHost = directIp.ToString();
                }
                else if (atyp == 0x03)
                {
                    int len = cin.ReadByte();
                    if (len <= 0) return;
                    byte[] dom = ReadExact(cin, len);
                    targetHost = Encoding.ASCII.GetString(dom);
                }
                else if (atyp == 0x04)
                {
                    byte[] ip6 = ReadExact(cin, 16);
                    directIp = new IPAddress(ip6);
                    targetHost = directIp.ToString();
                }
                else
                {
                    cin.Write(new byte[] { 0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0 }, 0, 10);
                    return;
                }

                byte[] portBytes = ReadExact(cin, 2);
                int targetPort = (portBytes[0] << 8) | portBytes[1];
                if (targetPort < 1 || targetPort > 65535) return;

                IPAddress connectIp = directIp ?? ResolveHost(targetHost);
                if (connectIp == null)
                {
                    cin.Write(new byte[] { 0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0 }, 0, 10);
                    return;
                }
                if (IsForbiddenTarget(connectIp))
                {
                    cin.Write(new byte[] { 0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0 }, 0, 10);
                    return;
                }

                remote = new TcpClient(connectIp.AddressFamily);
                remote.NoDelay = true;
                IAsyncResult ar = remote.BeginConnect(connectIp, targetPort, null, null);
                if (!ar.AsyncWaitHandle.WaitOne(15000))
                {
                    cin.Write(new byte[] { 0x05, 0x06, 0x00, 0x01, 0, 0, 0, 0, 0, 0 }, 0, 10);
                    return;
                }
                remote.EndConnect(ar);

                client.ReceiveTimeout = 120000;
                client.SendTimeout = 30000;
                remote.ReceiveTimeout = 120000;
                remote.SendTimeout = 30000;

                cin.Write(new byte[] { 0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0 }, 0, 10);

                NetworkStream rin = remote.GetStream();
                Thread pump = new Thread(delegate()
                {
                    CopyLoop(cin, rin);
                    try { remote.Client.Shutdown(SocketShutdown.Send); } catch { }
                });
                pump.IsBackground = true;
                pump.Start();
                CopyLoop(rin, cin);
                try { client.Client.Shutdown(SocketShutdown.Send); } catch { }
                pump.Join(1500);
            }
            catch { }
            finally
            {
                try { if (remote != null) remote.Close(); } catch { }
                try { client.Close(); } catch { }
            }
        }

        private static bool IsForbiddenTarget(IPAddress addr)
        {
            if (addr == null || IPAddress.IsLoopback(addr)) return true;
            if (addr.IsIPv4MappedToIPv6) addr = addr.MapToIPv4();
            if (addr.AddressFamily == AddressFamily.InterNetwork)
            {
                byte[] b = addr.GetAddressBytes();
                int b0 = b[0], b1 = b[1], b2 = b[2];
                if (b0 == 0 || b0 == 10 || b0 == 127) return true;
                if (b0 == 100 && b1 >= 64 && b1 <= 127) return true;
                if (b0 == 169 && b1 == 254) return true;
                if (b0 == 172 && b1 >= 16 && b1 <= 31) return true;
                if (b0 == 192 && b1 == 0 && (b2 == 0 || b2 == 2)) return true;
                if (b0 == 192 && b1 == 168) return true;
                if (b0 == 198 && (b1 == 18 || b1 == 19)) return true;
                if (b0 == 198 && b1 == 51 && b2 == 100) return true;
                if (b0 == 203 && b1 == 0 && b2 == 113) return true;
                if (b0 >= 224) return true;
                return false;
            }
            if (addr.AddressFamily == AddressFamily.InterNetworkV6)
            {
                if (addr.Equals(IPAddress.IPv6Any) || addr.Equals(IPAddress.IPv6None)
                    || addr.IsIPv6LinkLocal || addr.IsIPv6Multicast || addr.IsIPv6SiteLocal)
                    return true;
                byte[] b = addr.GetAddressBytes();
                if ((b[0] & 0xFE) == 0xFC) return true;
                return false;
            }
            return true;
        }

        private static IPAddress ResolveHost(string host)
        {
            IPAddress parsed;
            if (IPAddress.TryParse(host, out parsed)) return parsed;

            DnsCacheEntry entry;
            if (DnsCache.TryGetValue(host, out entry) && entry.ExpiresAtUtc > DateTime.UtcNow)
            {
                return entry.Ip;
            }

            IPAddress dohIp = QueryCloudflareDoh(host);
            if (dohIp != null)
            {
                DnsCache[host] = new DnsCacheEntry { Ip = dohIp, ExpiresAtUtc = DateTime.UtcNow.AddSeconds(60) };
                return dohIp;
            }

            try
            {
                IPAddress[] addrs = Dns.GetHostAddresses(host);
                for (int i = 0; i < addrs.Length; i++)
                {
                    if (addrs[i].AddressFamily == AddressFamily.InterNetwork) return addrs[i];
                }
                if (addrs.Length > 0) return addrs[0];
            }
            catch { }
            return null;
        }

        private static IPAddress QueryCloudflareDoh(string host)
        {
            try
            {
                string url = "https://1.1.1.1/dns-query?name=" + Uri.EscapeDataString(host) + "&type=A";
                var req = (HttpWebRequest)WebRequest.Create(url);
                req.Accept = "application/dns-json";
                req.Timeout = 4000;
                req.ReadWriteTimeout = 4000;
                using (var resp = (HttpWebResponse)req.GetResponse())
                using (var reader = new StreamReader(resp.GetResponseStream(), Encoding.UTF8))
                {
                    string json = reader.ReadToEnd();
                    Match m = DohAnswerRegex.Match(json);
                    IPAddress ip;
                    if (m.Success && IPAddress.TryParse(m.Groups[1].Value, out ip))
                    {
                        return ip;
                    }
                }
            }
            catch { }
            return null;
        }

        private static void CopyLoop(Stream src, Stream dst)
        {
            byte[] buf = new byte[16384];
            try
            {
                int n;
                while ((n = src.Read(buf, 0, buf.Length)) > 0)
                {
                    dst.Write(buf, 0, n);
                    dst.Flush();
                }
            }
            catch { }
            try { dst.Flush(); } catch { }
        }

        private static byte[] ReadExact(Stream s, int count)
        {
            byte[] buf = new byte[count];
            int off = 0;
            while (off < count)
            {
                int n = s.Read(buf, off, count - off);
                if (n <= 0) throw new EndOfStreamException();
                off += n;
            }
            return buf;
        }
    }
}
