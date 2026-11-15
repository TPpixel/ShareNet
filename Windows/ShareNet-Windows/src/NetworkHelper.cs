using System;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;

namespace ShareNetUSB
{
    internal static class NetworkHelper
    {
        // All public IPv4 ranges excluding RFC 1918 private/hotspot subnets
        // (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16) so hotspot replies route via Wi-Fi AP, not tun.
        private static readonly string[] PublicIpv4Routes = new[]
        {
            "0.0.0.0/5",
            "8.0.0.0/7",
            "11.0.0.0/8",
            "12.0.0.0/6",
            "16.0.0.0/4",
            "32.0.0.0/3",
            "64.0.0.0/2",
            "128.0.0.0/3",
            "160.0.0.0/5",
            "168.0.0.0/6",
            "172.0.0.0/12",
            "172.32.0.0/11",
            "172.64.0.0/10",
            "172.128.0.0/9",
            "173.0.0.0/8",
            "174.0.0.0/7",
            "176.0.0.0/4",
            "192.0.0.0/9",
            "192.128.0.0/11",
            "192.160.0.0/13",
            "192.169.0.0/16",
            "192.170.0.0/15",
            "192.172.0.0/14",
            "192.176.0.0/12",
            "192.192.0.0/10",
            "193.0.0.0/8",
            "194.0.0.0/7",
            "196.0.0.0/6",
            "200.0.0.0/5",
            "208.0.0.0/4",
            "224.0.0.0/3"
        };

        /// <summary>
        /// Returns comma-separated active IPv4 DNS servers of this PC, or "8.8.8.8" as fallback.
        /// </summary>
        public static string DetectPrimaryDns()
        {
            var dnsList = new List<string>();
            try
            {
                NetworkInterface[] interfaces = NetworkInterface.GetAllNetworkInterfaces();
                foreach (NetworkInterface ni in interfaces)
                {
                    if (ni.OperationalStatus != OperationalStatus.Up)
                    {
                        continue;
                    }
                    if (ni.NetworkInterfaceType == NetworkInterfaceType.Loopback ||
                        ni.NetworkInterfaceType == NetworkInterfaceType.Tunnel)
                    {
                        continue;
                    }

                    IPInterfaceProperties props = ni.GetIPProperties();
                    if (props.GatewayAddresses == null || props.GatewayAddresses.Count == 0)
                    {
                        continue;
                    }

                    foreach (IPAddress dns in props.DnsAddresses)
                    {
                        if (dns.AddressFamily != AddressFamily.InterNetwork)
                        {
                            continue;
                        }
                        if (IPAddress.IsLoopback(dns))
                        {
                            continue;
                        }
                        string ip = dns.ToString();
                        if (!dnsList.Contains(ip))
                        {
                            dnsList.Add(ip);
                        }
                    }
                }
            }
            catch
            {
                // Ignore enumeration errors and use fallback.
            }

            if (dnsList.Count == 0)
            {
                return "8.8.8.8";
            }

            if (dnsList.Count > 2)
            {
                return dnsList[0] + "," + dnsList[1];
            }
            return string.Join(",", dnsList.ToArray());
        }

        /// <summary>
        /// Builds the gnirehtet -r route list: all public IPv4 ranges + VPN control IPs + any private PC DNS IPs.
        /// </summary>
        public static string BuildGnirehtetRoutes(string dnsCsv)
        {
            var routes = new List<string>(PublicIpv4Routes);
            routes.Add("10.0.0.2/32");
            routes.Add("10.0.2.2/32");

            if (!string.IsNullOrEmpty(dnsCsv))
            {
                string[] items = dnsCsv.Split(new[] { ',', ';', ' ' }, StringSplitOptions.RemoveEmptyEntries);
                foreach (string item in items)
                {
                    IPAddress ip;
                    if (IPAddress.TryParse(item.Trim(), out ip) &&
                        ip.AddressFamily == AddressFamily.InterNetwork &&
                        IsPrivateIpv4(ip))
                    {
                        string cidr = ip.ToString() + "/32";
                        if (!routes.Contains(cidr))
                        {
                            routes.Add(cidr);
                        }
                    }
                }
            }

            return string.Join(",", routes.ToArray());
        }

        private static bool IsPrivateIpv4(IPAddress ip)
        {
            byte[] b = ip.GetAddressBytes();
            if (b.Length != 4) return false;
            if (b[0] == 10) return true;
            if (b[0] == 172 && b[1] >= 16 && b[1] <= 31) return true;
            if (b[0] == 192 && b[1] == 168) return true;
            return false;
        }

        /// <summary>
        /// Installs platform-tools into %LOCALAPPDATA%\Android\Sdk\platform-tools and adds it to User PATH.
        /// </summary>
        public static string InstallAdbToUserProfile(string sourceDir)
        {
            if (string.IsNullOrEmpty(sourceDir) || !Directory.Exists(sourceDir))
            {
                throw new DirectoryNotFoundException("Папка platform-tools не найдена: " + sourceDir);
            }

            string localAppData = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
            string targetDir = Path.Combine(localAppData, @"Android\Sdk\platform-tools");
            Directory.CreateDirectory(targetDir);

            foreach (string file in Directory.GetFiles(sourceDir, "*", SearchOption.AllDirectories))
            {
                string rel = file.Substring(sourceDir.Length).TrimStart('\\', '/');
                string dest = Path.Combine(targetDir, rel);
                string destSubDir = Path.GetDirectoryName(dest);
                if (!string.IsNullOrEmpty(destSubDir))
                {
                    Directory.CreateDirectory(destSubDir);
                }
                File.Copy(file, dest, true);
            }

            string userPath = Environment.GetEnvironmentVariable("Path", EnvironmentVariableTarget.User) ?? string.Empty;
            string[] rawParts = userPath.Split(new[] { ';' }, StringSplitOptions.RemoveEmptyEntries);
            var cleanParts = new List<string>();
            bool exists = false;
            string normalizedTarget = targetDir.TrimEnd('\\');

            foreach (string p in rawParts)
            {
                string trimmed = p.Trim();
                if (trimmed.Length == 0)
                {
                    continue;
                }
                cleanParts.Add(trimmed);
                if (string.Equals(trimmed.TrimEnd('\\'), normalizedTarget, StringComparison.OrdinalIgnoreCase))
                {
                    exists = true;
                }
            }

            if (!exists)
            {
                cleanParts.Add(targetDir);
                Environment.SetEnvironmentVariable("Path", string.Join(";", cleanParts.ToArray()), EnvironmentVariableTarget.User);
                return "ADB установлен в " + targetDir + " и добавлен в пользовательский PATH.";
            }

            return "ADB обновлён в " + targetDir + " (путь уже есть в пользовательском PATH).";
        }
    }
}
