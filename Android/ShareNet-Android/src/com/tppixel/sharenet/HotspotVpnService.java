package com.tppixel.sharenet;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.VpnService;
import android.os.Build;
import android.os.ParcelFileDescriptor;
import android.os.PowerManager;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;

import hev.htproxy.TProxyService;

/**
 * System-wide Android VPN tunnel (tun2socks via hev-socks5-tunnel + mapdns)
 * routing all apps through a Hotspot Proxy or USB ShareNet SOCKS5 endpoint.
 */
public class HotspotVpnService extends VpnService {
    public static final String ACTION_START = "com.tppixel.sharenet.VPN_START";
    public static final String ACTION_STOP = "com.tppixel.sharenet.VPN_STOP";
    public static final String ACTION_REVOKE_OTHER = "com.tppixel.sharenet.VPN_REVOKE_OTHER";
    public static final String EXTRA_PROXY_HOST = "proxy_host";
    public static final String EXTRA_PROXY_PORT = "proxy_port";

    private static final String CHANNEL_ID = "hotspot_vpn_channel";
    private static final int NOTIFICATION_ID = 102;

    public static volatile boolean isRunning = false;
    public static volatile String currentUpstreamHost = "";
    public static volatile int currentUpstreamPort = 8080;
    public static volatile String lastError = "";
    private static volatile HotspotVpnService activeInstance;

    private ParcelFileDescriptor tunFd;
    private PowerManager.WakeLock wakeLock;
    private UsbDisconnectWatcher usbWatcher;
    private volatile boolean boundToWifiNetwork = false;

    @Override
    public void onCreate() {
        super.onCreate();
        activeInstance = this;
        usbWatcher = new UsbDisconnectWatcher(this, () -> {
            if (isRunning && isUsbUpstream(currentUpstreamHost)) {
                stopVpn();
            }
        });
        usbWatcher.register();
    }

    /**
     * Stops the running VPN immediately even when system_server holds an active VpnService binding
     * (Android ignores plain stopService() onDestroy while bound until tunFd is closed).
     */
    public static void requestStop(Context ctx) {
        HotspotVpnService inst = activeInstance;
        if (inst != null) {
            inst.stopVpn();
        }
        if (ctx != null) {
            try {
                if (inst == null && isRunning) {
                    Intent stopVpn = new Intent(ctx, HotspotVpnService.class);
                    stopVpn.setAction(ACTION_STOP);
                    ctx.startService(stopVpn);
                }
            } catch (Exception ignored) {
            }
            try {
                ctx.stopService(new Intent(ctx, HotspotVpnService.class));
            } catch (Exception ignored) {
            }
        }
    }

    private static boolean isUsbUpstream(String host) {
        return "127.0.0.1".equals(host)
                || "localhost".equalsIgnoreCase(host)
                || "::1".equals(host);
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        if (intent != null && ACTION_STOP.equals(intent.getAction())) {
            stopVpn();
            return START_NOT_STICKY;
        }
        if (intent != null && ACTION_REVOKE_OTHER.equals(intent.getAction())) {
            revokeAnyActiveVpn();
            return START_NOT_STICKY;
        }

        String host = (intent != null) ? intent.getStringExtra(EXTRA_PROXY_HOST) : null;
        int port = (intent != null) ? intent.getIntExtra(EXTRA_PROXY_PORT, 8080) : 8080;
        if (host == null || host.trim().isEmpty()) {
            lastError = "Не указан адрес прокси";
            stopSelf();
            return START_NOT_STICKY;
        }
        host = host.trim();
        if (port < 1 || port > 65535) {
            port = 8080;
        }

        if (isRunning && host.equals(currentUpstreamHost) && port == currentUpstreamPort) {
            return START_STICKY;
        }

        if (isRunning) {
            cleanupTunnel();
        }

        try {
            usbWatcher.refreshSnapshot(isUsbUpstream(host));
        } catch (Throwable ignored) {
        }
        startVpn(host, port);
        return START_STICKY;
    }

    private void startVpn(String host, int port) {
        lastError = "";
        try {
            VpnService.Builder builder = new VpnService.Builder();
            builder.setSession("ShareNet VPN (" + host + ":" + port + ")");
            builder.setBlocking(false);
            builder.setMtu(1500);
            builder.addAddress("198.18.0.1", 30);
            builder.addDnsServer("198.18.0.2");
            builder.addRoute("0.0.0.0", 0);
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                builder.setMetered(false);
            }
            try {
                builder.addDisallowedApplication(getPackageName());
            } catch (PackageManager.NameNotFoundException ignored) {
            }

            Network wifiNet = (!isUsbUpstream(host)) ? NetUtils.findClientWifiNetwork(this) : null;
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP_MR1 && wifiNet != null) {
                builder.setUnderlyingNetworks(new Network[]{ wifiNet });
            }

            tunFd = builder.establish();
            if (tunFd == null) {
                lastError = "Нет разрешения на VPN";
                stopSelf();
                return;
            }

            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M && wifiNet != null && !ProxyService.isRunning) {
                ConnectivityManager cm = (ConnectivityManager) getSystemService(CONNECTIVITY_SERVICE);
                if (cm != null && cm.bindProcessToNetwork(wifiNet)) {
                    boundToWifiNetwork = true;
                }
            }

            File confFile = new File(getCacheDir(), "hotspotvpn.yml");
            String yaml = "misc:\n"
                    + "  task-stack-size: 86016\n"
                    + "tunnel:\n"
                    + "  mtu: 1500\n"
                    + "  ipv4: 198.18.0.1\n"
                    + "  icmp: 'reply'\n"
                    + "socks5:\n"
                    + "  port: " + port + "\n"
                    + "  address: '" + host.replace("'", "") + "'\n"
                    + "  udp: 'udp'\n"
                    + "mapdns:\n"
                    + "  address: 198.18.0.2\n"
                    + "  port: 53\n"
                    + "  network: 100.64.0.0\n"
                    + "  netmask: 255.192.0.0\n"
                    + "  cache-size: 10000\n";

            try (FileOutputStream fos = new FileOutputStream(confFile, false)) {
                fos.write(yaml.getBytes(StandardCharsets.UTF_8));
            }

            boolean started = TProxyService.TProxyStartService(confFile.getAbsolutePath(), tunFd.getFd());
            if (!started) {
                lastError = "Не удалось запустить TProxy";
                cleanupTunnel();
                stopSelf();
                return;
            }

            currentUpstreamHost = host;
            currentUpstreamPort = port;
            isRunning = true;
            acquireWakeLock();
            startForegroundWithNotification(host, port);
            sendStateBroadcast();
        } catch (Throwable e) {
            lastError = (e.getMessage() != null) ? e.getMessage() : e.getClass().getSimpleName();
            cleanupTunnel();
            stopSelf();
        }
    }

    private void startForegroundWithNotification(String host, int port) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel ch = new NotificationChannel(
                    CHANNEL_ID, "ShareNet VPN", NotificationManager.IMPORTANCE_LOW);
            NotificationManager nm = getSystemService(NotificationManager.class);
            if (nm != null) {
                nm.createNotificationChannel(ch);
            }
        }

        Intent openIntent = new Intent(this, MainActivity.class);
        int piFlags = (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M)
                ? PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT
                : PendingIntent.FLAG_UPDATE_CURRENT;
        PendingIntent contentIntent = PendingIntent.getActivity(this, 1, openIntent, piFlags);

        Notification.Builder builder = (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O)
                ? new Notification.Builder(this, CHANNEL_ID)
                : new Notification.Builder(this);

        Notification notification = builder
                .setContentTitle("ShareNet VPN активен")
                .setContentText("Все приложения через " + host + ":" + port)
                .setSmallIcon(android.R.drawable.stat_sys_download_done)
                .setContentIntent(contentIntent)
                .setOngoing(true)
                .build();

        startForeground(NOTIFICATION_ID, notification);
    }

    private void acquireWakeLock() {
        try {
            if (wakeLock == null) {
                PowerManager pm = (PowerManager) getSystemService(POWER_SERVICE);
                if (pm != null) {
                    wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "HotspotProxy::VpnLock");
                    wakeLock.acquire();
                }
            }
        } catch (Exception ignored) {
        }
    }

    private synchronized void cleanupTunnel() {
        boolean wasRunning = isRunning;
        isRunning = false;
        try {
            if (TProxyService.TProxyIsRunning()) {
                TProxyService.TProxyStopService();
            }
        } catch (Throwable ignored) {
        }
        if (tunFd != null) {
            try {
                tunFd.close();
            } catch (IOException ignored) {
            }
            tunFd = null;
        }
        if (wakeLock != null && wakeLock.isHeld()) {
            try {
                wakeLock.release();
            } catch (Exception ignored) {
            }
        }
        if (boundToWifiNetwork) {
            boundToWifiNetwork = false;
            try {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
                    ConnectivityManager cm = (ConnectivityManager) getSystemService(CONNECTIVITY_SERVICE);
                    if (cm != null) {
                        cm.bindProcessToNetwork(null);
                    }
                }
            } catch (Exception ignored) {
            }
        }
        if (wasRunning) {
            sendStateBroadcast();
        }
    }

    private void sendStateBroadcast() {
        try {
            Intent i = new Intent(ProxyService.ACTION_STATE_CHANGED);
            i.setPackage(getPackageName());
            sendBroadcast(i);
        } catch (Exception ignored) {
        }
    }

    private void stopVpn() {
        cleanupTunnel();
        stopForeground(true);
        stopSelf();
    }

    private void revokeAnyActiveVpn() {
        cleanupTunnel();
        try {
            if (VpnService.prepare(this) == null) {
                VpnService.Builder b = new VpnService.Builder();
                b.setSession("ShareNet-Reset");
                b.addAddress("198.18.0.1", 30);
                ParcelFileDescriptor tmp = b.establish();
                if (tmp != null) {
                    tmp.close();
                }
            }
        } catch (Throwable ignored) {
        }
        stopForeground(true);
        stopSelf();
    }

    public static long[] getTrafficStats() {
        if (!isRunning) {
            return null;
        }
        try {
            return TProxyService.TProxyGetStats();
        } catch (Throwable ignored) {
            return null;
        }
    }

    @Override
    public void onRevoke() {
        stopVpn();
        super.onRevoke();
    }

    @Override
    public void onDestroy() {
        if (activeInstance == this) {
            activeInstance = null;
        }
        if (usbWatcher != null) {
            usbWatcher.unregister();
            usbWatcher = null;
        }
        cleanupTunnel();
        stopForeground(true);
        super.onDestroy();
    }
}
