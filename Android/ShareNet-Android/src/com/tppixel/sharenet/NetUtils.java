package com.tppixel.sharenet;

import android.content.Context;
import android.net.ConnectivityManager;
import android.net.DhcpInfo;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.RouteInfo;
import android.net.wifi.WifiManager;

import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.InterfaceAddress;
import java.net.NetworkInterface;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;

/**
 * Network inspection helpers for Wi-Fi gateway discovery, hotspot IPs, and underlying transport checks.
 */
final class NetUtils {
    private static final int NET_CAPABILITY_LOCAL_NETWORK = 36;

    private NetUtils() {}

    static final class HotspotSubnet {
        final String interfaceName;
        final String ipString;
        final int localIpInt;
        final int mask;
        final int networkInt;

        HotspotSubnet(String interfaceName, String ipString, int localIpInt, int prefixLength) {
            this.interfaceName = interfaceName;
            this.ipString = ipString;
            this.localIpInt = localIpInt;
            int prefix = (prefixLength >= 8 && prefixLength <= 30) ? prefixLength : 24;
            this.mask = 0xFFFFFFFF << (32 - prefix);
            this.networkInt = localIpInt & mask;
        }

        boolean matchesConnection(int localInt, int remoteInt) {
            if (localInt != localIpInt) {
                return false;
            }
            if ((remoteInt & mask) != networkInt) {
                return false;
            }
            int hostPart = remoteInt & ~mask;
            return hostPart != 0 && hostPart != (~mask) && remoteInt != localIpInt;
        }
    }

    static String detectWifiGateway(Context ctx) {
        try {
            ConnectivityManager cm = (ConnectivityManager) ctx.getSystemService(Context.CONNECTIVITY_SERVICE);
            if (cm != null) {
                String activeGw = extractWifiGatewayFromNetwork(cm, cm.getActiveNetwork());
                if (!activeGw.isEmpty()) {
                    return activeGw;
                }
            }
        } catch (Exception ignored) {
        }

        try {
            WifiManager wm = (WifiManager) ctx.getApplicationContext().getSystemService(Context.WIFI_SERVICE);
            if (wm != null && wm.isWifiEnabled()) {
                DhcpInfo dhcp = wm.getDhcpInfo();
                if (dhcp != null) {
                    int ip = (dhcp.gateway != 0) ? dhcp.gateway : dhcp.serverAddress;
                    if (ip != 0) {
                        return (ip & 0xff) + "." + ((ip >> 8) & 0xff) + "."
                                + ((ip >> 16) & 0xff) + "." + ((ip >> 24) & 0xff);
                    }
                }
            }
        } catch (Exception ignored) {
        }

        try {
            ConnectivityManager cm = (ConnectivityManager) ctx.getSystemService(Context.CONNECTIVITY_SERVICE);
            if (cm != null) {
                for (Network net : cm.getAllNetworks()) {
                    String gw = extractWifiGatewayFromNetwork(cm, net);
                    if (!gw.isEmpty()) {
                        return gw;
                    }
                }
            }
        } catch (Exception ignored) {
        }
        return "";
    }

    static Network findClientWifiNetwork(Context ctx) {
        try {
            ConnectivityManager cm = (ConnectivityManager) ctx.getSystemService(Context.CONNECTIVITY_SERVICE);
            if (cm == null) {
                return null;
            }
            Network active = cm.getActiveNetwork();
            if (!extractWifiGatewayFromNetwork(cm, active).isEmpty()) {
                return active;
            }
            for (Network net : cm.getAllNetworks()) {
                if (!extractWifiGatewayFromNetwork(cm, net).isEmpty()) {
                    return net;
                }
            }
        } catch (Exception ignored) {
        }
        return null;
    }

    private static String extractWifiGatewayFromNetwork(ConnectivityManager cm, Network net) {
        if (cm == null || net == null) {
            return "";
        }
        NetworkCapabilities nc = cm.getNetworkCapabilities(net);
        if (nc == null || !nc.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)
                || nc.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) {
            return "";
        }
        LinkProperties lp = cm.getLinkProperties(net);
        if (lp == null) {
            return "";
        }
        String iface = lp.getInterfaceName();
        if (iface != null && (iface.startsWith("ap") || iface.startsWith("swlan"))) {
            return "";
        }
        for (RouteInfo route : lp.getRoutes()) {
            if (route.isDefaultRoute() && route.getGateway() instanceof Inet4Address) {
                String gw = route.getGateway().getHostAddress();
                if (gw != null && !"0.0.0.0".equals(gw)) {
                    return gw;
                }
            }
        }
        if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.R) {
            Inet4Address dhcpSrv = (lp.getDhcpServerAddress() instanceof Inet4Address)
                    ? (Inet4Address) lp.getDhcpServerAddress()
                    : null;
            if (dhcpSrv != null && !dhcpSrv.isAnyLocalAddress()) {
                return dhcpSrv.getHostAddress();
            }
        }
        return "";
    }

    static boolean hasPhysicalInternetTransport(Context ctx) {
        try {
            ConnectivityManager cm = (ConnectivityManager) ctx.getSystemService(Context.CONNECTIVITY_SERVICE);
            if (cm == null) return false;
            for (Network net : cm.getAllNetworks()) {
                NetworkCapabilities nc = cm.getNetworkCapabilities(net);
                if (nc == null || nc.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) {
                    continue;
                }
                if (nc.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) {
                    return true;
                }
                if (nc.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) {
                    if (nc.hasCapability(NET_CAPABILITY_LOCAL_NETWORK)) {
                        continue;
                    }
                    LinkProperties lp = cm.getLinkProperties(net);
                    String iface = (lp != null) ? lp.getInterfaceName() : null;
                    boolean hasGateway = hasDefaultGatewayRoute(lp);
                    if (hasGateway && (iface == null || (!iface.startsWith("ap") && !iface.startsWith("swlan")))) {
                        return true;
                    }
                }
            }
        } catch (Exception ignored) {
        }
        return false;
    }

    static List<String> detectHotspotIps() {
        return detectHotspotIps(null);
    }

    static List<String> detectHotspotIps(Context ctx) {
        List<HotspotSubnet> subnets = detectHotspotSubnets(ctx);
        List<String> out = new ArrayList<>(subnets.size());
        for (HotspotSubnet s : subnets) {
            out.add(s.ipString + " (" + s.interfaceName + ")");
        }
        return out;
    }

    static List<HotspotSubnet> detectHotspotSubnets(Context ctx) {
        Set<String> upstreamIfaces = collectUpstreamInterfaces(ctx);
        List<HotspotSubnet> preferred = new ArrayList<>();
        List<HotspotSubnet> fallback = new ArrayList<>();
        try {
            for (NetworkInterface ni : Collections.list(NetworkInterface.getNetworkInterfaces())) {
                if (!ni.isUp() || ni.isLoopback()) {
                    continue;
                }
                String name = ni.getName();
                if (name == null) {
                    continue;
                }
                String lower = name.toLowerCase(Locale.ROOT);
                if (isExcludedSystemInterface(lower) || upstreamIfaces.contains(lower)) {
                    continue;
                }
                for (InterfaceAddress ia : ni.getInterfaceAddresses()) {
                    InetAddress addr = ia.getAddress();
                    if (!(addr instanceof Inet4Address) || addr.isLoopbackAddress() || addr.isLinkLocalAddress()) {
                        continue;
                    }
                    String ip = addr.getHostAddress();
                    if (ip == null || "10.0.0.2".equals(ip) || "198.18.0.1".equals(ip)) {
                        continue;
                    }
                    int ipInt = ipv4ToInt(addr);
                    if (!isPrivateIpv4(ipInt)) {
                        continue;
                    }
                    HotspotSubnet subnet = new HotspotSubnet(lower, ip, ipInt, ia.getNetworkPrefixLength());
                    if (isPreferredHotspotInterface(lower)) {
                        preferred.add(subnet);
                    } else {
                        fallback.add(subnet);
                    }
                }
            }
        } catch (Exception ignored) {
        }
        if (!preferred.isEmpty()) {
            preferred.addAll(fallback);
            return preferred;
        }
        return fallback;
    }

    private static Set<String> collectUpstreamInterfaces(Context ctx) {
        Set<String> upstream = new HashSet<>();
        if (ctx == null) {
            return upstream;
        }
        try {
            ConnectivityManager cm = (ConnectivityManager) ctx.getSystemService(Context.CONNECTIVITY_SERVICE);
            if (cm == null) {
                return upstream;
            }
            for (Network net : cm.getAllNetworks()) {
                NetworkCapabilities nc = cm.getNetworkCapabilities(net);
                if (nc == null || nc.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) {
                    continue;
                }
                if (nc.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)
                        || nc.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)
                        || nc.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)
                        || nc.hasTransport(NetworkCapabilities.TRANSPORT_BLUETOOTH)) {
                    LinkProperties lp = cm.getLinkProperties(net);
                    if (lp != null && lp.getInterfaceName() != null) {
                        String iface = lp.getInterfaceName().toLowerCase(Locale.ROOT);
                        // Pixel/Android 15+ registers Wi-Fi hotspot APs (e.g. wlan2) in ConnectivityManager
                        // with TRANSPORT_WIFI + LOCAL_NETWORK and no INTERNET/default-gateway route.
                        if (!isExplicitTetherInterface(iface) && isUpstreamNetwork(nc, lp)) {
                            upstream.add(iface);
                        }
                    }
                }
            }
        } catch (Exception ignored) {
        }
        return upstream;
    }

    static boolean isUpstreamNetwork(NetworkCapabilities nc, LinkProperties lp) {
        if (nc == null || nc.hasCapability(NET_CAPABILITY_LOCAL_NETWORK)) {
            return false;
        }
        if (nc.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) {
            return true;
        }
        return hasDefaultGatewayRoute(lp);
    }

    private static boolean hasDefaultGatewayRoute(LinkProperties lp) {
        if (lp == null) {
            return false;
        }
        for (RouteInfo r : lp.getRoutes()) {
            if (r.isDefaultRoute() && r.hasGateway()) {
                return true;
            }
        }
        return false;
    }

    private static boolean isExplicitTetherInterface(String name) {
        return name.startsWith("ap")
                || name.startsWith("swlan")
                || name.startsWith("softap")
                || name.startsWith("rndis")
                || name.startsWith("usb")
                || name.startsWith("ncm")
                || name.startsWith("bt-pan");
    }

    private static boolean isPreferredHotspotInterface(String name) {
        return isExplicitTetherInterface(name)
                || name.startsWith("wlan")
                || name.startsWith("p2p");
    }

    private static boolean isExcludedSystemInterface(String name) {
        return name.equals("lo")
                || name.startsWith("tun")
                || name.startsWith("tap")
                || name.startsWith("ppp")
                || name.startsWith("ipsec")
                || name.startsWith("v4-")
                || name.startsWith("rmnet")
                || name.startsWith("ccmni")
                || name.startsWith("dummy")
                || name.startsWith("clat")
                || name.startsWith("sit")
                || name.startsWith("ip6tnl");
    }

    static int ipv4ToInt(InetAddress addr) {
        byte[] b = addr.getAddress();
        return ((b[0] & 0xFF) << 24)
                | ((b[1] & 0xFF) << 16)
                | ((b[2] & 0xFF) << 8)
                | (b[3] & 0xFF);
    }

    static boolean isPrivateIpv4(int ip) {
        int b0 = (ip >>> 24) & 0xFF;
        int b1 = (ip >>> 16) & 0xFF;
        if (b0 == 10) return true; // 10.0.0.0/8
        if (b0 == 172 && b1 >= 16 && b1 <= 31) return true; // 172.16.0.0/12
        return b0 == 192 && b1 == 168; // 192.168.0.0/16
    }
}
