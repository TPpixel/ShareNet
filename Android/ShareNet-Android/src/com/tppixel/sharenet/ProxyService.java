package com.tppixel.sharenet;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Intent;
import android.os.Build;
import android.os.IBinder;
import android.os.PowerManager;
import android.util.Log;

import java.io.ByteArrayOutputStream;
import java.io.EOFException;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.Inet4Address;
import java.net.Inet6Address;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.SocketAddress;
import java.net.SocketTimeoutException;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.Collections;
import java.util.List;
import java.util.Locale;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Future;
import java.util.concurrent.SynchronousQueue;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import java.util.regex.Pattern;

/**
 * Foreground HTTP/HTTPS CONNECT & SOCKS5 proxy server restricted strictly to active hotspot clients.
 */
public class ProxyService extends Service {
    public static final String EXTRA_PORT = "port";
    public static final String ACTION_STOP = "com.tppixel.sharenet.STOP";
    public static final String ACTION_STATE_CHANGED = "com.tppixel.sharenet.STATE_CHANGED";
    private static final String CHANNEL_ID = "hotspot_proxy_channel";
    private static final String TAG = "ShareNetProxy";
    private static final int NOTIFICATION_ID = 101;
    private static final int DEFAULT_PORT = 8080;

    private static final int MAX_CONCURRENT_CONNECTIONS = 192;
    private static final int MAX_CONNECTIONS_PER_IP = 96;
    private static final int MAX_UDP_ASSOCIATIONS = 24;
    private static final int HANDSHAKE_TIMEOUT_MS = 15_000;
    private static final int IDLE_TIMEOUT_MS = 180_000;
    private static final int UDP_IDLE_TIMEOUT_MS = 60_000;
    private static final int MAX_HEADER_BYTES = 16_384;
    private static final long SUBNET_CACHE_MS = 1_000L;
    private static final long SUBNET_REFRESH_MIN_MS = 250L;

    private static final Pattern IPV4_LITERAL = Pattern.compile("^\\d{1,3}(\\.\\d{1,3}){3}$");
    private static final Pattern SAFE_HOST_CHARS = Pattern.compile("^[A-Za-z0-9._:\\[\\]-]{1,255}$");
    private static final Pattern SAFE_METHOD = Pattern.compile("^[A-Z]{2,16}$");

    public static volatile boolean isRunning = false;
    public static volatile int currentPort = DEFAULT_PORT;
    public static volatile String lastError = "";
    public static final AtomicInteger activeConnections = new AtomicInteger(0);
    private static final AtomicInteger activeUdpAssociations = new AtomicInteger(0);
    private static final ConcurrentHashMap<Integer, AtomicInteger> perIpConnections = new ConcurrentHashMap<>();
    private static final Set<Socket> openSockets =
            Collections.newSetFromMap(new ConcurrentHashMap<Socket, Boolean>());
    private static final Set<DatagramSocket> openUdpSockets =
            Collections.newSetFromMap(new ConcurrentHashMap<DatagramSocket, Boolean>());

    private volatile List<NetUtils.HotspotSubnet> cachedSubnets = Collections.emptyList();
    private volatile long lastSubnetRefreshMs = 0L;

    private ServerSocket serverSocket;
    private ExecutorService executor;
    private ExecutorService pipeExecutor;
    private Thread acceptThread;
    private PowerManager.WakeLock wakeLock;
    private UsbDisconnectWatcher usbWatcher;

    @Override
    public void onCreate() {
        super.onCreate();
        usbWatcher = new UsbDisconnectWatcher(this, this::stopProxyNow);
        usbWatcher.register();
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        if (intent != null && ACTION_STOP.equals(intent.getAction())) {
            stopProxyNow();
            return START_NOT_STICKY;
        }

        int port = (intent != null) ? intent.getIntExtra(EXTRA_PORT, DEFAULT_PORT) : DEFAULT_PORT;
        if (port < 1024 || port > 65535) {
            port = DEFAULT_PORT;
        }

        if (isRunning) {
            return START_STICKY;
        }

        currentPort = port;
        lastError = "";
        if (usbWatcher != null) {
            usbWatcher.refreshSnapshot(false);
        }
        startForegroundWithNotification(port);
        acquireWakeLock();
        startServer(port);
        sendStateBroadcast();
        return START_STICKY;
    }

    private void startForegroundWithNotification(int port) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel ch = new NotificationChannel(
                    CHANNEL_ID, "ShareNet Прокси", NotificationManager.IMPORTANCE_LOW);
            NotificationManager nm = getSystemService(NotificationManager.class);
            if (nm != null) {
                nm.createNotificationChannel(ch);
            }
        }

        Intent openIntent = new Intent(this, MainActivity.class);
        int piFlags = (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M)
                ? PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT
                : PendingIntent.FLAG_UPDATE_CURRENT;
        PendingIntent contentIntent = PendingIntent.getActivity(this, 0, openIntent, piFlags);

        Notification.Builder builder = (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O)
                ? new Notification.Builder(this, CHANNEL_ID)
                : new Notification.Builder(this);

        Notification notification = builder
                .setContentTitle("ShareNet Прокси активен")
                .setContentText("Порт " + port + " — трафик идёт через телефон")
                .setSmallIcon(android.R.drawable.stat_sys_upload)
                .setContentIntent(contentIntent)
                .setOngoing(true)
                .build();

        startForeground(NOTIFICATION_ID, notification);
    }

    private void acquireWakeLock() {
        try {
            PowerManager pm = (PowerManager) getSystemService(POWER_SERVICE);
            if (pm != null) {
                wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "HotspotProxy::ServerLock");
                wakeLock.acquire();
            }
        } catch (Exception ignored) {
        }
    }

    private void startServer(final int port) {
        executor = new ThreadPoolExecutor(
                4, MAX_CONCURRENT_CONNECTIONS, 30L, TimeUnit.SECONDS,
                new SynchronousQueue<>(), new ThreadPoolExecutor.AbortPolicy());
        pipeExecutor = new ThreadPoolExecutor(
                4, MAX_CONCURRENT_CONNECTIONS, 30L, TimeUnit.SECONDS,
                new SynchronousQueue<>(), new ThreadPoolExecutor.AbortPolicy());
        perIpConnections.clear();
        activeConnections.set(0);
        activeUdpAssociations.set(0);
        isRunning = true;

        acceptThread = new Thread(() -> {
            try {
                serverSocket = new ServerSocket();
                serverSocket.setReuseAddress(true);
                serverSocket.bind(new InetSocketAddress(port), 128);

                while (isRunning && !serverSocket.isClosed()) {
                    final Socket client = serverSocket.accept();
                    final Integer clientIpKey = acquireClientSlotIfHotspot(client);
                    if (clientIpKey == null) {
                        closeQuietly(client);
                        continue;
                    }
                    try {
                        executor.execute(() -> handleClient(client, clientIpKey));
                    } catch (RuntimeException rejected) {
                        releaseClientSlot(clientIpKey);
                        closeQuietly(client);
                    }
                }
            } catch (IOException e) {
                if (isRunning) {
                    lastError = e.getMessage();
                }
            } finally {
                isRunning = false;
                closeQuietly(serverSocket);
            }
        }, "ProxyAcceptThread");
        acceptThread.start();
    }

    private Integer acquireClientSlotIfHotspot(Socket client) {
        InetAddress local = client.getLocalAddress();
        InetAddress remote = client.getInetAddress();
        if (!(local instanceof Inet4Address) || !(remote instanceof Inet4Address)) {
            return null;
        }
        int localInt = NetUtils.ipv4ToInt(local);
        int remoteInt = NetUtils.ipv4ToInt(remote);
        if (!isHotspotClient(localInt, remoteInt)) {
            Log.w(TAG, "Reject non-hotspot client local=" + local.getHostAddress() + " remote=" + remote.getHostAddress());
            return null;
        }

        while (true) {
            int cur = activeConnections.get();
            if (cur >= MAX_CONCURRENT_CONNECTIONS) {
                Log.w(TAG, "Reject max concurrent (" + cur + ") remote=" + remote.getHostAddress());
                return null;
            }
            if (activeConnections.compareAndSet(cur, cur + 1)) {
                break;
            }
        }

        Integer key = remoteInt;
        AtomicInteger ipCounter = perIpConnections.get(key);
        if (ipCounter == null) {
            AtomicInteger created = new AtomicInteger(0);
            AtomicInteger existing = perIpConnections.putIfAbsent(key, created);
            ipCounter = (existing != null) ? existing : created;
        }
        while (true) {
            int curIp = ipCounter.get();
            if (curIp >= MAX_CONNECTIONS_PER_IP) {
                activeConnections.decrementAndGet();
                Log.w(TAG, "Reject max per-ip (" + curIp + ") remote=" + remote.getHostAddress());
                return null;
            }
            if (ipCounter.compareAndSet(curIp, curIp + 1)) {
                break;
            }
        }
        return key;
    }

    private void releaseClientSlot(Integer clientIpKey) {
        activeConnections.decrementAndGet();
        if (clientIpKey != null) {
            AtomicInteger ipCounter = perIpConnections.get(clientIpKey);
            if (ipCounter != null && ipCounter.decrementAndGet() <= 0) {
                perIpConnections.remove(clientIpKey, ipCounter);
            }
        }
    }

    private boolean isHotspotClient(int localInt, int remoteInt) {
        long now = System.currentTimeMillis();
        List<NetUtils.HotspotSubnet> subnets = cachedSubnets;
        if (subnets.isEmpty() || (now - lastSubnetRefreshMs) > SUBNET_CACHE_MS) {
            subnets = refreshHotspotSubnets(now);
        }
        for (NetUtils.HotspotSubnet s : subnets) {
            if (s.matchesConnection(localInt, remoteInt)) {
                return true;
            }
        }
        if ((now - lastSubnetRefreshMs) > SUBNET_REFRESH_MIN_MS) {
            subnets = refreshHotspotSubnets(now);
            for (NetUtils.HotspotSubnet s : subnets) {
                if (s.matchesConnection(localInt, remoteInt)) {
                    return true;
                }
            }
        }
        return false;
    }

    private synchronized List<NetUtils.HotspotSubnet> refreshHotspotSubnets(long now) {
        if ((now - lastSubnetRefreshMs) <= SUBNET_REFRESH_MIN_MS && !cachedSubnets.isEmpty()) {
            return cachedSubnets;
        }
        List<NetUtils.HotspotSubnet> fresh = NetUtils.detectHotspotSubnets(this);
        cachedSubnets = fresh;
        lastSubnetRefreshMs = now;
        return fresh;
    }

    private void handleClient(Socket client, Integer clientIpKey) {
        openSockets.add(client);
        Socket remote = null;
        boolean responseStarted = false;
        try {
            client.setTcpNoDelay(true);
            client.setSoTimeout(HANDSHAKE_TIMEOUT_MS);
            InputStream clientIn = client.getInputStream();
            OutputStream clientOut = client.getOutputStream();

            int firstByte = clientIn.read();
            if (firstByte == -1) {
                return;
            }

            if (firstByte == 0x05) {
                remote = handleSocks5(client, clientIn, clientOut);
                return;
            }

            String headerBlock = readHeaderBlock(firstByte, clientIn);
            if (headerBlock == null || headerBlock.isEmpty()) {
                return;
            }

            String[] lines = headerBlock.split("\r\n");
            if (lines.length == 0) {
                return;
            }

            String[] parts = lines[0].split(" ");
            if (parts.length != 3) {
                writeSimpleError(clientOut, "400 Bad Request");
                return;
            }

            String method = parts[0].toUpperCase(Locale.ROOT);
            String target = parts[1];
            String version = parts[2].toUpperCase(Locale.ROOT);
            if (!SAFE_METHOD.matcher(method).matches()
                    || (!"HTTP/1.1".equals(version) && !"HTTP/1.0".equals(version))) {
                writeSimpleError(clientOut, "400 Bad Request");
                return;
            }

            if ("CONNECT".equals(method)) {
                HostPort hp = parseHostPort(target, 443);
                if (hp == null) {
                    writeSimpleError(clientOut, "400 Bad Request");
                    return;
                }
                remote = openUpstreamSocket(hp.host, hp.port);
                client.setSoTimeout(IDLE_TIMEOUT_MS);
                remote.setSoTimeout(IDLE_TIMEOUT_MS);
                responseStarted = true;
                clientOut.write("HTTP/1.1 200 Connection Established\r\n\r\n".getBytes(StandardCharsets.US_ASCII));
                clientOut.flush();
                tunnelBidirectional(client, remote);
            } else {
                URI uri;
                try {
                    uri = URI.create(target);
                } catch (IllegalArgumentException e) {
                    writeSimpleError(clientOut, "400 Bad Request");
                    return;
                }
                String host = uri.getHost();
                int port = uri.getPort();
                if (host == null) {
                    host = findHeaderValue(lines, "Host");
                    if (host == null) {
                        writeSimpleError(clientOut, "400 Bad Request");
                        return;
                    }
                    HostPort hp = parseHostPort(host, 80);
                    if (hp == null) {
                        writeSimpleError(clientOut, "400 Bad Request");
                        return;
                    }
                    host = hp.host;
                    port = hp.port;
                } else if (port <= 0) {
                    port = 80;
                }

                String path = uri.getRawPath();
                if (path == null || path.isEmpty()) {
                    path = "/";
                }
                if (uri.getRawQuery() != null) {
                    path += "?" + uri.getRawQuery();
                }
                if (path.indexOf('\r') >= 0 || path.indexOf('\n') >= 0) {
                    writeSimpleError(clientOut, "400 Bad Request");
                    return;
                }

                remote = openUpstreamSocket(host, port);
                client.setSoTimeout(IDLE_TIMEOUT_MS);
                remote.setSoTimeout(IDLE_TIMEOUT_MS);

                StringBuilder req = new StringBuilder();
                req.append(method).append(' ').append(path).append(' ').append(version).append("\r\n");
                for (int i = 1; i < lines.length; i++) {
                    String h = lines[i];
                    String lower = h.toLowerCase(Locale.ROOT);
                    if (lower.startsWith("proxy-connection:")
                            || lower.startsWith("connection:")
                            || lower.startsWith("proxy-authorization:")) {
                        continue;
                    }
                    req.append(h).append("\r\n");
                }
                req.append("Connection: close\r\n\r\n");

                OutputStream remoteOut = remote.getOutputStream();
                remoteOut.write(req.toString().getBytes(StandardCharsets.ISO_8859_1));
                remoteOut.flush();
                responseStarted = true;
                tunnelBidirectional(client, remote);
            }
        } catch (BlockedTargetException blocked) {
            Log.w(TAG, "HTTP blocked " + client.getInetAddress().getHostAddress() + ": " + blocked.getMessage());
            if (!responseStarted) {
                try {
                    writeSimpleError(client.getOutputStream(), "403 Forbidden");
                } catch (Exception ignored) {
                }
            }
        } catch (MalformedRequestException bad) {
            Log.w(TAG, "HTTP malformed " + client.getInetAddress().getHostAddress() + ": " + bad.getMessage());
            if (!responseStarted) {
                try {
                    writeSimpleError(client.getOutputStream(), "400 Bad Request");
                } catch (Exception ignored) {
                }
            }
        } catch (Exception e) {
            Log.w(TAG, "HTTP err " + client.getInetAddress().getHostAddress() + " started=" + responseStarted + ": " + e);
            if (!responseStarted) {
                try {
                    writeSimpleError(client.getOutputStream(), "502 Bad Gateway");
                } catch (Exception ignored2) {
                }
            }
        } finally {
            if (remote != null) {
                openSockets.remove(remote);
            }
            openSockets.remove(client);
            closeQuietly(remote);
            closeQuietly(client);
            releaseClientSlot(clientIpKey);
        }
    }

    private Socket handleSocks5(Socket client, InputStream clientIn, OutputStream clientOut) throws IOException {
        int nMethods = clientIn.read();
        if (nMethods <= 0 || nMethods > 64) {
            return null;
        }
        byte[] methods = readExact(clientIn, nMethods);
        boolean supportsNoAuth = false;
        for (byte m : methods) {
            if (m == 0x00) {
                supportsNoAuth = true;
                break;
            }
        }
        if (!supportsNoAuth) {
            clientOut.write(new byte[]{0x05, (byte) 0xFF});
            clientOut.flush();
            return null;
        }
        clientOut.write(new byte[]{0x05, 0x00});
        clientOut.flush();

        byte[] reqHdr = readExact(clientIn, 4);
        if (reqHdr[0] != 0x05) {
            return null;
        }
        int cmd = reqHdr[1] & 0xFF;
        int atyp = reqHdr[3] & 0xFF;
        if (cmd != 0x01 && cmd != 0x03) {
            Log.w(TAG, "SOCKS5 unsupported cmd=" + cmd + " atyp=" + atyp + " from=" + client.getInetAddress().getHostAddress());
            clientOut.write(new byte[]{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0});
            clientOut.flush();
            return null;
        }

        String host;
        if (atyp == 0x01) {
            byte[] ip = readExact(clientIn, 4);
            host = InetAddress.getByAddress(ip).getHostAddress();
        } else if (atyp == 0x03) {
            int len = clientIn.read();
            if (len <= 0) {
                return null;
            }
            byte[] dom = readExact(clientIn, len);
            host = new String(dom, StandardCharsets.US_ASCII);
        } else if (atyp == 0x04) {
            byte[] ip6 = readExact(clientIn, 16);
            host = InetAddress.getByAddress(ip6).getHostAddress();
        } else {
            clientOut.write(new byte[]{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0});
            clientOut.flush();
            return null;
        }

        byte[] portBytes = readExact(clientIn, 2);
        int port = ((portBytes[0] & 0xFF) << 8) | (portBytes[1] & 0xFF);

        if (cmd == 0x03) {
            handleSocks5UdpAssociate(client, clientIn, clientOut);
            return null;
        }

        Socket remote = null;
        try {
            remote = openUpstreamSocket(host, port);
            client.setSoTimeout(IDLE_TIMEOUT_MS);
            remote.setSoTimeout(IDLE_TIMEOUT_MS);
        } catch (BlockedTargetException blocked) {
            Log.w(TAG, "SOCKS5 blocked " + client.getInetAddress().getHostAddress() + " -> " + host + ":" + port + ": " + blocked.getMessage());
            closeQuietly(remote);
            clientOut.write(new byte[]{0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0});
            clientOut.flush();
            return null;
        } catch (IOException e) {
            Log.w(TAG, "SOCKS5 err " + client.getInetAddress().getHostAddress() + " -> " + host + ":" + port + ": " + e);
            closeQuietly(remote);
            clientOut.write(new byte[]{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0});
            clientOut.flush();
            return null;
        }

        clientOut.write(new byte[]{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0});
        clientOut.flush();
        tunnelBidirectional(client, remote);
        return remote;
    }

    private void handleSocks5UdpAssociate(Socket client, InputStream clientIn, OutputStream clientOut) throws IOException {
        if (HotspotVpnService.isRunning && "127.0.0.1".equals(HotspotVpnService.currentUpstreamHost)) {
            clientOut.write(new byte[]{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0});
            clientOut.flush();
            return;
        }
        InetAddress localAddr = client.getLocalAddress();
        if (!(localAddr instanceof Inet4Address)) {
            clientOut.write(new byte[]{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0});
            clientOut.flush();
            return;
        }
        int curUdp = activeUdpAssociations.incrementAndGet();
        if (curUdp > MAX_UDP_ASSOCIATIONS) {
            activeUdpAssociations.decrementAndGet();
            clientOut.write(new byte[]{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0});
            clientOut.flush();
            return;
        }

        DatagramSocket clientUdp = null;
        DatagramSocket upstreamUdp = null;
        Future<?> f1 = null;
        Future<?> f2 = null;
        try {
            clientUdp = new DatagramSocket(0, localAddr);
            upstreamUdp = new DatagramSocket();
            clientUdp.setSoTimeout(UDP_IDLE_TIMEOUT_MS);
            upstreamUdp.setSoTimeout(UDP_IDLE_TIMEOUT_MS);
            openUdpSockets.add(clientUdp);
            openUdpSockets.add(upstreamUdp);

            byte[] ipBytes = localAddr.getAddress();
            int relayPort = clientUdp.getLocalPort();
            clientOut.write(new byte[]{
                    0x05, 0x00, 0x00, 0x01,
                    ipBytes[0], ipBytes[1], ipBytes[2], ipBytes[3],
                    (byte) ((relayPort >> 8) & 0xFF), (byte) (relayPort & 0xFF)
            });
            clientOut.flush();

            client.setSoTimeout(UDP_IDLE_TIMEOUT_MS);
            final InetAddress expectedClientIp = client.getInetAddress();
            final AtomicReference<SocketAddress> clientPeer = new AtomicReference<>();
            final AtomicLong lastUdpActivityMs = new AtomicLong(System.currentTimeMillis());
            final DatagramSocket cSock = clientUdp;
            final DatagramSocket uSock = upstreamUdp;

            f1 = pipeExecutor.submit(() -> relayUdpClientToUpstream(cSock, uSock, expectedClientIp, clientPeer, lastUdpActivityMs));
            f2 = pipeExecutor.submit(() -> relayUdpUpstreamToClient(cSock, uSock, clientPeer, lastUdpActivityMs));

            byte[] drain = new byte[64];
            while (true) {
                try {
                    if (clientIn.read(drain) == -1) {
                        break;
                    }
                } catch (SocketTimeoutException timeout) {
                    if ((System.currentTimeMillis() - lastUdpActivityMs.get()) >= UDP_IDLE_TIMEOUT_MS) {
                        break;
                    }
                } catch (IOException ignored) {
                    break;
                }
            }
        } catch (IOException | RuntimeException e) {
            Log.w(TAG, "SOCKS5 UDP associate err: " + e);
        } finally {
            if (clientUdp != null) {
                openUdpSockets.remove(clientUdp);
                closeQuietly(clientUdp);
            }
            if (upstreamUdp != null) {
                openUdpSockets.remove(upstreamUdp);
                closeQuietly(upstreamUdp);
            }
            if (f1 != null) f1.cancel(true);
            if (f2 != null) f2.cancel(true);
            activeUdpAssociations.decrementAndGet();
        }
    }

    private static void relayUdpClientToUpstream(
            DatagramSocket clientUdp,
            DatagramSocket upstreamUdp,
            InetAddress expectedClientIp,
            AtomicReference<SocketAddress> clientPeer,
            AtomicLong lastUdpActivityMs) {
        byte[] buf = new byte[65535];
        DatagramPacket pkt = new DatagramPacket(buf, buf.length);
        while (!clientUdp.isClosed() && !upstreamUdp.isClosed()) {
            try {
                pkt.setLength(buf.length);
                clientUdp.receive(pkt);
                if (!expectedClientIp.equals(pkt.getAddress())) {
                    continue;
                }
                clientPeer.set(pkt.getSocketAddress());
                lastUdpActivityMs.set(System.currentTimeMillis());
                int len = pkt.getLength();
                if (len < 10) {
                    continue;
                }
                if (buf[2] != 0x00) {
                    continue; // Fragmented SOCKS5 UDP not supported
                }
                int atyp = buf[3] & 0xFF;
                int offset = 4;
                String dstHost;
                if (atyp == 0x01) {
                    if (len < offset + 4 + 2) continue;
                    byte[] ip = new byte[4];
                    System.arraycopy(buf, offset, ip, 0, 4);
                    offset += 4;
                    dstHost = InetAddress.getByAddress(ip).getHostAddress();
                } else if (atyp == 0x03) {
                    if (len < offset + 1) continue;
                    int dlen = buf[offset] & 0xFF;
                    offset++;
                    if (dlen <= 0 || len < offset + dlen + 2) continue;
                    dstHost = new String(buf, offset, dlen, StandardCharsets.US_ASCII);
                    offset += dlen;
                } else if (atyp == 0x04) {
                    if (len < offset + 16 + 2) continue;
                    byte[] ip6 = new byte[16];
                    System.arraycopy(buf, offset, ip6, 0, 16);
                    offset += 16;
                    dstHost = InetAddress.getByAddress(ip6).getHostAddress();
                } else {
                    continue;
                }
                int dstPort = ((buf[offset] & 0xFF) << 8) | (buf[offset + 1] & 0xFF);
                offset += 2;
                int payloadLen = len - offset;
                if (payloadLen <= 0) {
                    continue;
                }
                String cleanHost = validateLiteralTarget(dstHost, dstPort);
                String resolved = DohResolver.resolve(cleanHost);
                InetAddress dstAddr = InetAddress.getByName(resolved);
                if (isForbiddenAddress(dstAddr)) {
                    continue;
                }
                DatagramPacket outPkt = new DatagramPacket(buf, offset, payloadLen, dstAddr, dstPort);
                upstreamUdp.send(outPkt);
            } catch (IOException ignored) {
                if (clientUdp.isClosed() || upstreamUdp.isClosed()) {
                    break;
                }
            }
        }
    }

    private static void relayUdpUpstreamToClient(
            DatagramSocket clientUdp,
            DatagramSocket upstreamUdp,
            AtomicReference<SocketAddress> clientPeer,
            AtomicLong lastUdpActivityMs) {
        byte[] buf = new byte[65535];
        DatagramPacket pkt = new DatagramPacket(buf, buf.length);
        while (!clientUdp.isClosed() && !upstreamUdp.isClosed()) {
            try {
                pkt.setLength(buf.length);
                upstreamUdp.receive(pkt);
                lastUdpActivityMs.set(System.currentTimeMillis());
                SocketAddress peer = clientPeer.get();
                if (peer == null) {
                    continue;
                }
                InetAddress srcAddr = pkt.getAddress();
                byte[] addrBytes = (srcAddr != null) ? srcAddr.getAddress() : null;
                if (addrBytes == null || (addrBytes.length != 4 && addrBytes.length != 16)) {
                    continue;
                }
                int hdrLen = 4 + addrBytes.length + 2;
                int payloadLen = pkt.getLength();
                byte[] out = new byte[hdrLen + payloadLen];
                out[0] = 0x00;
                out[1] = 0x00;
                out[2] = 0x00;
                out[3] = (byte) (addrBytes.length == 4 ? 0x01 : 0x04);
                System.arraycopy(addrBytes, 0, out, 4, addrBytes.length);
                int srcPort = pkt.getPort();
                out[4 + addrBytes.length] = (byte) ((srcPort >> 8) & 0xFF);
                out[5 + addrBytes.length] = (byte) (srcPort & 0xFF);
                System.arraycopy(buf, pkt.getOffset(), out, hdrLen, payloadLen);
                clientUdp.send(new DatagramPacket(out, out.length, peer));
            } catch (IOException ignored) {
                if (clientUdp.isClosed() || upstreamUdp.isClosed()) {
                    break;
                }
            }
        }
    }

    private static Socket openUpstreamSocket(String host, int port) throws IOException {
        String cleanHost = validateLiteralTarget(host, port);
        Socket s;
        if (HotspotVpnService.isRunning && "127.0.0.1".equals(HotspotVpnService.currentUpstreamHost)) {
            s = openSocks5Upstream("127.0.0.1", HotspotVpnService.currentUpstreamPort, cleanHost, port);
        } else {
            List<String> candidates = DohResolver.resolveAll(cleanHost);
            if (candidates.isEmpty()) {
                candidates = Collections.singletonList(cleanHost);
            }
            java.util.ArrayList<InetAddress> allowed = new java.util.ArrayList<>();
            boolean sawForbidden = false;
            IOException lastDnsErr = null;
            for (String cand : candidates) {
                try {
                    for (InetAddress addr : InetAddress.getAllByName(cand)) {
                        if (isForbiddenAddress(addr)) {
                            sawForbidden = true;
                        } else if (!allowed.contains(addr)) {
                            allowed.add(addr);
                        }
                    }
                } catch (IOException e) {
                    lastDnsErr = e;
                }
            }
            if (allowed.isEmpty()) {
                if (sawForbidden) {
                    throw new BlockedTargetException("Target destination is restricted");
                }
                if (lastDnsErr != null) {
                    throw lastDnsErr;
                }
                throw new IOException("Unable to resolve host " + cleanHost);
            }

            int maxAttempts = Math.min(allowed.size(), 4);
            int perAttemptTimeout = (maxAttempts > 1) ? 3500 : 15000;
            Socket connected = null;
            IOException lastConnectErr = null;
            for (int i = 0; i < maxAttempts; i++) {
                Socket attempt = new Socket();
                try {
                    int timeout = (i == maxAttempts - 1 && maxAttempts > 1) ? 6000 : perAttemptTimeout;
                    attempt.connect(new InetSocketAddress(allowed.get(i), port), timeout);
                    attempt.setTcpNoDelay(true);
                    connected = attempt;
                    break;
                } catch (IOException e) {
                    closeQuietly(attempt);
                    lastConnectErr = e;
                }
            }
            if (connected == null) {
                throw (lastConnectErr != null) ? lastConnectErr : new IOException("Connect failed for " + cleanHost);
            }
            s = connected;
        }
        openSockets.add(s);
        return s;
    }

    private static String validateLiteralTarget(String host, int port) throws IOException {
        if (port < 1 || port > 65535) {
            throw new MalformedRequestException("Invalid port");
        }
        if (host == null) {
            throw new MalformedRequestException("Missing host");
        }
        String trimmed = host.trim();
        if (trimmed.isEmpty() || !SAFE_HOST_CHARS.matcher(trimmed).matches()) {
            throw new MalformedRequestException("Invalid host");
        }
        String unbracketed = trimmed;
        boolean bracketed = trimmed.startsWith("[") && trimmed.endsWith("]");
        if (bracketed) {
            unbracketed = trimmed.substring(1, trimmed.length() - 1);
            if (unbracketed.isEmpty() || unbracketed.indexOf(':') < 0) {
                throw new MalformedRequestException("Invalid IPv6 host");
            }
        }
        String lower = unbracketed.toLowerCase(Locale.ROOT);
        if (lower.endsWith(".")) {
            lower = lower.substring(0, lower.length() - 1);
            unbracketed = unbracketed.substring(0, unbracketed.length() - 1);
        }
        if (lower.isEmpty()
                || "localhost".equals(lower)
                || lower.endsWith(".localhost")
                || lower.endsWith(".local")
                || lower.endsWith(".internal")
                || lower.endsWith(".home.arpa")
                || lower.endsWith(".localdomain")) {
            throw new BlockedTargetException("Local destination is restricted");
        }

        if (unbracketed.indexOf(':') >= 0) {
            InetAddress addr = InetAddress.getByName(unbracketed);
            if (isForbiddenAddress(addr)) {
                throw new BlockedTargetException("Target IPv6 address is restricted");
            }
            return addr.getHostAddress();
        }

        if (IPV4_LITERAL.matcher(unbracketed).matches()) {
            String[] octets = unbracketed.split("\\.");
            for (String oct : octets) {
                if (oct.length() > 1 && oct.startsWith("0")) {
                    throw new MalformedRequestException("Non-canonical IPv4");
                }
                int val = Integer.parseInt(oct);
                if (val < 0 || val > 255) {
                    throw new MalformedRequestException("Invalid IPv4 octet");
                }
            }
            InetAddress addr = InetAddress.getByName(unbracketed);
            if (isForbiddenAddress(addr)) {
                throw new BlockedTargetException("Target IPv4 address is restricted");
            }
            return unbracketed;
        }

        int lastDot = lower.lastIndexOf('.');
        if (lastDot <= 0 || lastDot == lower.length() - 1) {
            throw new BlockedTargetException("Unqualified host is restricted");
        }
        char tldFirst = lower.charAt(lastDot + 1);
        if (tldFirst < 'a' || tldFirst > 'z') {
            throw new MalformedRequestException("Malformed hostname");
        }
        return unbracketed;
    }

    static boolean isForbiddenAddress(InetAddress addr) {
        if (addr == null
                || addr.isAnyLocalAddress()
                || addr.isLoopbackAddress()
                || addr.isLinkLocalAddress()
                || addr.isSiteLocalAddress()
                || addr.isMulticastAddress()) {
            return true;
        }
        if (addr instanceof Inet4Address) {
            int ip = NetUtils.ipv4ToInt(addr);
            int b0 = (ip >>> 24) & 0xFF;
            int b1 = (ip >>> 16) & 0xFF;
            int b2 = (ip >>> 8) & 0xFF;
            if (b0 == 0 || b0 == 10 || b0 == 127) return true;
            if (b0 == 100 && b1 >= 64 && b1 <= 127) return true; // CGNAT 100.64.0.0/10
            if (b0 == 169 && b1 == 254) return true; // Link-local 169.254.0.0/16
            if (b0 == 172 && b1 >= 16 && b1 <= 31) return true; // Private 172.16.0.0/12
            if (b0 == 192 && b1 == 0 && (b2 == 0 || b2 == 2)) return true; // IETF / TEST-NET-1
            if (b0 == 192 && b1 == 168) return true; // Private 192.168.0.0/16
            if (b0 == 198 && (b1 == 18 || b1 == 19)) return true; // Benchmarking / VPN tun
            if (b0 == 198 && b1 == 51 && b2 == 100) return true; // TEST-NET-2
            if (b0 == 203 && b1 == 0 && b2 == 113) return true; // TEST-NET-3
            if (b0 >= 224) return true; // Multicast & Reserved
            return false;
        }
        if (addr instanceof Inet6Address) {
            byte[] b = addr.getAddress();
            if (b.length != 16) return true;
            int first = b[0] & 0xFF;
            int second = b[1] & 0xFF;
            if ((first & 0xFE) == 0xFC) return true; // Unique local fc00::/7
            if (first == 0xFE && (second & 0xC0) == 0x80) return true; // Link-local fe80::/10
            if (first == 0xFE && (second & 0xC0) == 0xC0) return true; // Site-local fec0::/10
            if (first == 0xFF) return true; // Multicast ff00::/8
            boolean allZeroPrefix = true;
            for (int i = 0; i < 15; i++) {
                if (b[i] != 0) {
                    allZeroPrefix = false;
                    break;
                }
            }
            if (allZeroPrefix && (b[15] == 0 || b[15] == 1)) return true; // :: / ::1
            return false;
        }
        return true;
    }

    private static Socket openSocks5Upstream(String proxyHost, int proxyPort, String targetHost, int targetPort) throws IOException {
        Socket s = new Socket();
        s.connect(new InetSocketAddress(proxyHost, proxyPort), 10000);
        s.setTcpNoDelay(true);
        s.setSoTimeout(HANDSHAKE_TIMEOUT_MS);
        try {
            InputStream in = s.getInputStream();
            OutputStream out = s.getOutputStream();
            out.write(new byte[]{0x05, 0x01, 0x00});
            out.flush();
            byte[] authResp = readExact(in, 2);
            if (authResp[0] != 0x05 || authResp[1] != 0x00) {
                throw new IOException("Upstream SOCKS5 auth failed");
            }
            byte[] hostBytes = targetHost.getBytes(StandardCharsets.US_ASCII);
            if (hostBytes.length > 255) {
                throw new IOException("Host too long");
            }
            byte[] req = new byte[7 + hostBytes.length];
            req[0] = 0x05;
            req[1] = 0x01;
            req[2] = 0x00;
            req[3] = 0x03;
            req[4] = (byte) hostBytes.length;
            System.arraycopy(hostBytes, 0, req, 5, hostBytes.length);
            req[5 + hostBytes.length] = (byte) ((targetPort >> 8) & 0xFF);
            req[6 + hostBytes.length] = (byte) (targetPort & 0xFF);
            out.write(req);
            out.flush();

            byte[] hdr = readExact(in, 4);
            if (hdr[0] != 0x05 || hdr[1] != 0x00) {
                throw new IOException("Upstream SOCKS5 connect error: " + (hdr[1] & 0xFF));
            }
            int atyp = hdr[3] & 0xFF;
            if (atyp == 0x01) {
                readExact(in, 6);
            } else if (atyp == 0x03) {
                int len = in.read();
                if (len < 0) throw new EOFException("Upstream SOCKS5 EOF");
                readExact(in, len + 2);
            } else if (atyp == 0x04) {
                readExact(in, 18);
            } else {
                throw new IOException("Upstream SOCKS5 unknown atyp: " + atyp);
            }
            return s;
        } catch (IOException e) {
            closeQuietly(s);
            throw e;
        }
    }

    private static byte[] readExact(InputStream in, int count) throws IOException {
        byte[] buf = new byte[count];
        int off = 0;
        while (off < count) {
            int n = in.read(buf, off, count - off);
            if (n == -1) {
                throw new EOFException("Unexpected EOF");
            }
            off += n;
        }
        return buf;
    }

    private void tunnelBidirectional(final Socket a, final Socket b) {
        java.util.concurrent.Future<?> f = null;
        try {
            f = pipeExecutor.submit(() -> pipe(a, b));
            pipe(b, a);
        } catch (RuntimeException ignored) {
        } finally {
            if (f != null) {
                try {
                    f.get(2000, TimeUnit.MILLISECONDS);
                } catch (Exception ignored) {
                    f.cancel(true);
                }
            }
        }
    }

    private void pipe(Socket src, Socket dst) {
        byte[] buf = new byte[16384];
        try {
            InputStream in = src.getInputStream();
            OutputStream out = dst.getOutputStream();
            int n;
            while ((n = in.read(buf)) != -1) {
                out.write(buf, 0, n);
                out.flush();
            }
        } catch (IOException ignored) {
        } finally {
            try { dst.shutdownOutput(); } catch (IOException ignored) {}
        }
    }

    private static String readHeaderBlock(int firstByte, InputStream in) throws IOException {
        ByteArrayOutputStream baos = new ByteArrayOutputStream();
        baos.write(firstByte);
        int prev1 = firstByte, prev2 = -1, prev3 = -1;
        int count = 1;
        boolean foundEnd = false;
        while (count < MAX_HEADER_BYTES) {
            int b = in.read();
            if (b == -1) {
                return null;
            }
            baos.write(b);
            count++;
            if (prev3 == '\r' && prev2 == '\n' && prev1 == '\r' && b == '\n') {
                foundEnd = true;
                break;
            }
            prev3 = prev2;
            prev2 = prev1;
            prev1 = b;
        }
        if (!foundEnd) {
            return null;
        }
        String text = new String(baos.toByteArray(), StandardCharsets.ISO_8859_1);
        int end = text.indexOf("\r\n\r\n");
        return (end >= 0) ? text.substring(0, end) : null;
    }

    private static String findHeaderValue(String[] lines, String name) {
        String prefix = name.toLowerCase(Locale.ROOT) + ":";
        for (int i = 1; i < lines.length; i++) {
            if (lines[i].toLowerCase(Locale.ROOT).startsWith(prefix)) {
                return lines[i].substring(prefix.length()).trim();
            }
        }
        return null;
    }

    private static HostPort parseHostPort(String raw, int defaultPort) {
        if (raw == null || raw.isEmpty()) {
            return null;
        }
        raw = raw.trim();
        try {
            if (raw.startsWith("[")) {
                int close = raw.indexOf(']');
                if (close < 0) return null;
                String host = raw.substring(1, close);
                int port = defaultPort;
                if (close + 1 < raw.length()) {
                    if (raw.charAt(close + 1) != ':') return null;
                    port = Integer.parseInt(raw.substring(close + 2));
                }
                if (port < 1 || port > 65535) return null;
                return new HostPort("[" + host + "]", port);
            }
            int colon = raw.lastIndexOf(':');
            if (colon > 0) {
                String host = raw.substring(0, colon);
                int port = Integer.parseInt(raw.substring(colon + 1));
                if (port < 1 || port > 65535) return null;
                return new HostPort(host, port);
            }
            if (defaultPort < 1 || defaultPort > 65535) return null;
            return new HostPort(raw, defaultPort);
        } catch (NumberFormatException ignored) {
            return null;
        }
    }

    private static void writeSimpleError(OutputStream out, String status) throws IOException {
        out.write(("HTTP/1.1 " + status + "\r\nConnection: close\r\n\r\n").getBytes(StandardCharsets.US_ASCII));
        out.flush();
    }

    private static void closeQuietly(Socket s) {
        if (s != null) {
            try { s.close(); } catch (IOException ignored) {}
        }
    }

    private static void closeQuietly(ServerSocket s) {
        if (s != null) {
            try { s.close(); } catch (IOException ignored) {}
        }
    }

    private static void closeQuietly(DatagramSocket s) {
        if (s != null) {
            try { s.close(); } catch (Exception ignored) {}
        }
    }

    private void cleanupProxyResources() {
        boolean wasRunning = isRunning;
        isRunning = false;
        closeQuietly(serverSocket);
        serverSocket = null;
        for (Socket s : openSockets) {
            closeQuietly(s);
        }
        openSockets.clear();
        for (DatagramSocket ds : openUdpSockets) {
            closeQuietly(ds);
        }
        openUdpSockets.clear();
        perIpConnections.clear();
        activeConnections.set(0);
        activeUdpAssociations.set(0);
        if (executor != null) {
            executor.shutdownNow();
            executor = null;
        }
        if (pipeExecutor != null) {
            pipeExecutor.shutdownNow();
            pipeExecutor = null;
        }
        if (wakeLock != null && wakeLock.isHeld()) {
            try { wakeLock.release(); } catch (Exception ignored) {}
        }
        stopForeground(true);
        if (wasRunning) {
            sendStateBroadcast();
        }
    }

    private void sendStateBroadcast() {
        try {
            Intent i = new Intent(ACTION_STATE_CHANGED);
            i.setPackage(getPackageName());
            sendBroadcast(i);
        } catch (Exception ignored) {
        }
    }

    private void stopProxyNow() {
        cleanupProxyResources();
        stopSelf();
    }

    @Override
    public void onDestroy() {
        if (usbWatcher != null) {
            usbWatcher.unregister();
            usbWatcher = null;
        }
        cleanupProxyResources();
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    private static final class BlockedTargetException extends IOException {
        BlockedTargetException(String message) {
            super(message);
        }
    }

    private static final class MalformedRequestException extends IOException {
        MalformedRequestException(String message) {
            super(message);
        }
    }

    private static final class HostPort {
        final String host;
        final int port;

        HostPort(String host, int port) {
            this.host = host;
            this.port = port;
        }
    }
}
