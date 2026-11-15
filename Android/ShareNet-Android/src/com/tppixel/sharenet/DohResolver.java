package com.tppixel.sharenet;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.URL;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Lightweight Cloudflare DoH (1.1.1.1) IPv4 resolver with multi-IP caching and system DNS fallback.
 */
final class DohResolver {
    private static final long CACHE_TTL_MS = 60_000L;
    private static final int MAX_CACHED_IPS = 6;
    private static final Pattern IPV4_LITERAL = Pattern.compile("^\\d{1,3}(\\.\\d{1,3}){3}$");
    private static final Pattern DOH_ANSWER_IP = Pattern.compile("\"type\"\\s*:\\s*1\\s*,[^}]*?\"data\"\\s*:\\s*\"(\\d+\\.\\d+\\.\\d+\\.\\d+)\"");

    private static final ConcurrentHashMap<String, CacheEntry> cache = new ConcurrentHashMap<>();

    private static final class CacheEntry {
        final List<String> ips;
        final long expiresAt;

        CacheEntry(List<String> ips, long expiresAt) {
            this.ips = ips;
            this.expiresAt = expiresAt;
        }
    }

    private DohResolver() {}

    static void clearCache() {
        cache.clear();
    }

    static String resolve(String host) {
        List<String> all = resolveAll(host);
        return (all != null && !all.isEmpty()) ? all.get(0) : host;
    }

    static List<String> resolveAll(String host) {
        if (host == null || host.isEmpty()) {
            return Collections.emptyList();
        }
        String clean = host.trim();
        if (IPV4_LITERAL.matcher(clean).matches() || clean.indexOf(':') >= 0) {
            return Collections.singletonList(clean);
        }
        String lower = clean.toLowerCase();
        if ("localhost".equals(lower) || lower.endsWith(".local") || clean.indexOf('.') < 0) {
            return Collections.singletonList(clean);
        }

        long now = System.currentTimeMillis();
        CacheEntry cached = cache.get(lower);
        if (cached != null && cached.expiresAt > now && !cached.ips.isEmpty()) {
            return cached.ips;
        }

        List<String> dohIps = queryCloudflareDoh(clean);
        if (!dohIps.isEmpty()) {
            List<String> immutable = Collections.unmodifiableList(dohIps);
            cache.put(lower, new CacheEntry(immutable, now + CACHE_TTL_MS));
            return immutable;
        }

        try {
            InetAddress[] addrs = InetAddress.getAllByName(clean);
            if (addrs != null && addrs.length > 0) {
                Set<String> uniq = new LinkedHashSet<>();
                for (InetAddress addr : addrs) {
                    String sysIp = addr.getHostAddress();
                    if (sysIp != null && !sysIp.isEmpty()) {
                        uniq.add(sysIp);
                        if (uniq.size() >= MAX_CACHED_IPS) {
                            break;
                        }
                    }
                }
                if (!uniq.isEmpty()) {
                    List<String> list = Collections.unmodifiableList(new ArrayList<>(uniq));
                    cache.put(lower, new CacheEntry(list, now + CACHE_TTL_MS));
                    return list;
                }
            }
        } catch (Exception ignored) {
        }
        return Collections.singletonList(clean);
    }

    private static List<String> queryCloudflareDoh(String domain) {
        HttpURLConnection conn = null;
        try {
            String urlStr = "https://1.1.1.1/dns-query?name="
                    + URLEncoder.encode(domain, "UTF-8") + "&type=A";
            conn = (HttpURLConnection) new URL(urlStr).openConnection();
            conn.setRequestMethod("GET");
            conn.setRequestProperty("Accept", "application/dns-json");
            conn.setConnectTimeout(2000);
            conn.setReadTimeout(2000);
            if (conn.getResponseCode() != 200) {
                return Collections.emptyList();
            }
            StringBuilder sb = new StringBuilder();
            try (BufferedReader br = new BufferedReader(
                    new InputStreamReader(conn.getInputStream(), StandardCharsets.UTF_8))) {
                String line;
                while ((line = br.readLine()) != null) {
                    sb.append(line);
                }
            }
            Set<String> ips = new LinkedHashSet<>();
            Matcher m = DOH_ANSWER_IP.matcher(sb.toString());
            while (m.find()) {
                ips.add(m.group(1));
                if (ips.size() >= MAX_CACHED_IPS) {
                    break;
                }
            }
            return ips.isEmpty() ? Collections.emptyList() : new ArrayList<>(ips);
        } catch (Exception ignored) {
        } finally {
            if (conn != null) {
                conn.disconnect();
            }
        }
        return Collections.emptyList();
    }
}
