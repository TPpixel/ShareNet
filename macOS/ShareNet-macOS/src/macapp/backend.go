package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	proxyPort           = 8080
	tunDNSIP            = "172.19.0.2"
	cfgPath             = "/tmp/hotspot-vpn-singbox.json"
	logPath             = "/tmp/hotspot-vpn-singbox.log"
	pidPath             = "/tmp/hotspot-vpn-singbox.pid"
	tunTokenPath        = "/tmp/hotspot-vpn-tun.token"
	dnsBackupPath       = "/tmp/hotspot-vpn-dns-backup.json"
	singBoxPgrepPattern = "[s]ing-box run -c " + cfgPath
	githubURL           = "https://github.com/TPpixel"
	maxLogRows          = 160
)

var (
	ipv4Re         = regexp.MustCompile(`^((25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(25[0-5]|2[0-4]\d|1?\d?\d)$`)
	ansiRe         = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	sbTimePrefixRe = regexp.MustCompile(`^(?:[+-]\d{4}\s+)?\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\s+`)
	sbConnIDRe     = regexp.MustCompile(`\[\d+(?:\s+\d+m?s)?\]\s*`)
	sbTargetRe     = regexp.MustCompile(`open connection to [0-9a-fA-F.:]+`)
	sbSrcPortRe    = regexp.MustCompile(`(\d+\.\d+\.\d+\.\d+):\d+->`)
)

type StatusResponse struct {
	HasSingBox    bool     `json:"hasSingBox"`
	Connected     bool     `json:"connected"`
	ConnectedMode string   `json:"connectedMode"`
	Service       string   `json:"service"`
	Gateway       string   `json:"gateway"`
	Reachable     bool     `json:"reachable"`
	DNSStuck      bool     `json:"dnsStuck"`
	Logs          []string `json:"logs"`
}

type dnsBackup struct {
	Service string   `json:"service"`
	Servers []string `json:"servers"`
}

type Backend struct {
	actionMu         sync.Mutex
	mu               sync.Mutex
	singBoxPath      string
	stopFlagPath     string
	connected        bool
	connectedMode    string
	connectedService string
	proxyProto       string
	service          string
	gateway          string
	dnsBackup        []string
	logOffset        int64
	lastLogMsg       string
	seenLogKeys      map[string]bool
	logs             []string
}

func resolveStopFlagPath() string {
	if cfgDir, err := os.UserConfigDir(); err == nil && cfgDir != "" {
		appDir := filepath.Join(cfgDir, "ShareNet")
		if err := os.MkdirAll(appDir, 0o700); err == nil {
			return filepath.Join(appDir, "stop.flag")
		}
	}
	return "/tmp/hotspot-vpn-stop.flag"
}

func NewBackend(singBoxPath string) *Backend {
	b := &Backend{
		singBoxPath:  singBoxPath,
		stopFlagPath: resolveStopFlagPath(),
		service:      "Wi-Fi",
		seenLogKeys:  make(map[string]bool),
	}
	_ = os.Remove(b.stopFlagPath)
	b.AddLog("Готово.")
	if !isSingBoxRunning() {
		if stuck := findStuckDNSServices(detectService()); len(stuck) > 0 {
			b.AddLog(fmt.Sprintf("Обнаружен зависший DNS %s (%s). Нажмите «Отключить всё и откатить».", tunDNSIP, strings.Join(stuck, ", ")))
		}
	}
	return b
}

func (b *Backend) resetLogDedupe() {
	b.mu.Lock()
	b.lastLogMsg = ""
	b.seenLogKeys = make(map[string]bool)
	b.mu.Unlock()
}

func (b *Backend) AddLog(msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	b.mu.Lock()
	if msg == b.lastLogMsg {
		b.mu.Unlock()
		return
	}
	b.lastLogMsg = msg
	line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg)
	b.logs = append(b.logs, line)
	if len(b.logs) > maxLogRows {
		b.logs = append([]string(nil), b.logs[len(b.logs)-maxLogRows:]...)
	}
	b.mu.Unlock()
}

func (b *Backend) HasSingBox() bool {
	if b.singBoxPath == "" {
		return false
	}
	st, err := os.Stat(b.singBoxPath)
	return err == nil && !st.IsDir()
}

func isValidIPv4(ip string) bool {
	return ipv4Re.MatchString(strings.TrimSpace(ip))
}

func parseRouteField(out, field string) string {
	prefix := field + ":"
	for _, line := range strings.Split(out, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trim, prefix))
		}
	}
	return ""
}

func parseHardwarePort(out, iface string) string {
	if iface == "" {
		return ""
	}
	var currentPort string
	for _, line := range strings.Split(out, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "Hardware Port:") {
			currentPort = strings.TrimSpace(strings.TrimPrefix(trim, "Hardware Port:"))
		} else if strings.HasPrefix(trim, "Device:") {
			dev := strings.TrimSpace(strings.TrimPrefix(trim, "Device:"))
			if dev == iface && currentPort != "" {
				return currentPort
			}
		}
	}
	return ""
}

func parseRouterField(out string) string {
	for _, line := range strings.Split(out, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "Router:") {
			val := strings.TrimSpace(strings.TrimPrefix(trim, "Router:"))
			if val != "" && !strings.EqualFold(val, "none") {
				return val
			}
		}
	}
	return ""
}

func runShell(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runAdminShell(shellCmd string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	script := "on run argv\ndo shell script (item 1 of argv) with administrator privileges\nend run"
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script, shellCmd).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func detectService() string {
	routeOut, err := runShell("/sbin/route", "-n", "get", "default")
	if err == nil {
		iface := parseRouteField(routeOut, "interface")
		if iface != "" && !strings.HasPrefix(iface, "utun") {
			portsOut, err := runShell("/usr/sbin/networksetup", "-listallhardwareports")
			if err == nil {
				if svc := parseHardwarePort(portsOut, iface); svc != "" {
					return svc
				}
			}
		}
	}
	return "Wi-Fi"
}

func detectGateway(service string) string {
	routeOut, err := runShell("/sbin/route", "-n", "get", "default")
	if err == nil {
		gw := parseRouteField(routeOut, "gateway")
		if isValidIPv4(gw) && !strings.HasPrefix(gw, "172.19.0.") {
			return gw
		}
	}
	if service != "" {
		infoOut, err := runShell("/usr/sbin/networksetup", "-getinfo", service)
		if err == nil {
			if router := parseRouterField(infoOut); isValidIPv4(router) {
				return router
			}
		}
	}
	return ""
}

func probePort(ip string, port int) bool {
	if !isValidIPv4(ip) {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 900*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func isSingBoxRunning() bool {
	_, err := runShell("/usr/bin/pgrep", "-f", singBoxPgrepPattern)
	return err == nil
}

func probeHTTPConnect(ip string, port int) (bool, string) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 900*time.Millisecond)
	if err != nil {
		return false, err.Error()
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(1500 * time.Millisecond))
	req := "CONNECT 1.1.1.1:443 HTTP/1.1\r\nHost: 1.1.1.1:443\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return false, err.Error()
	}
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if n == 0 && err != nil {
		return false, err.Error()
	}
	line := strings.TrimSpace(strings.SplitN(string(buf[:n]), "\n", 2)[0])
	if strings.HasPrefix(line, "HTTP/1.") {
		return strings.Contains(line, " 200"), line
	}
	return false, line
}

// detectProxyProtocol probes whether ip:port speaks SOCKS5 or HTTP CONNECT
// and captures upstream reset diagnostics if outbound forwarding fails.
func detectProxyProtocol(ip string, port int) (string, string) {
	if !isValidIPv4(ip) {
		return "socks5", "invalid gateway"
	}
	addr := fmt.Sprintf("%s:%d", ip, port)
	conn, err := net.DialTimeout("tcp", addr, 900*time.Millisecond)
	if err != nil {
		return "socks5", err.Error()
	}
	_ = conn.SetDeadline(time.Now().Add(1500 * time.Millisecond))

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		_ = conn.Close()
		if ok, httpLine := probeHTTPConnect(ip, port); ok {
			return "http", ""
		} else if strings.HasPrefix(httpLine, "HTTP/1.") {
			return "http", httpLine
		}
		return "socks5", err.Error()
	}

	greeting := make([]byte, 64)
	n, err := conn.Read(greeting)
	if n >= 2 && greeting[0] == 0x05 && greeting[1] == 0x00 {
		// Verify SOCKS5 CONNECT to 1.1.1.1:443 so we catch proxies that accept
		// the 2-byte greeting but reset CONNECT (or only allow HTTP CONNECT).
		req := []byte{0x05, 0x01, 0x00, 0x01, 1, 1, 1, 1, 0x01, 0xbb}
		_, werr := conn.Write(req)
		resp := make([]byte, 32)
		var rn int
		var rerr error
		if werr == nil {
			rn, rerr = conn.Read(resp)
		}
		_ = conn.Close()

		if werr == nil && rn >= 2 && resp[0] == 0x05 && resp[1] == 0x00 {
			return "socks5", ""
		}
		socksErr := ""
		if werr != nil {
			socksErr = werr.Error()
		} else if rerr != nil {
			socksErr = rerr.Error()
		} else if rn >= 2 {
			socksErr = fmt.Sprintf("SOCKS5 reply 0x%02x", resp[1])
		}
		if ok, httpLine := probeHTTPConnect(ip, port); ok {
			return "http", ""
		} else if strings.HasPrefix(httpLine, "HTTP/1.") {
			return "http", httpLine
		}
		if socksErr != "" {
			return "socks5", "SOCKS5 тест 1.1.1.1:443: " + socksErr
		}
		return "socks5", ""
	}
	_ = conn.Close()

	if ok, httpLine := probeHTTPConnect(ip, port); ok {
		return "http", ""
	} else if strings.HasPrefix(httpLine, "HTTP/1.") {
		return "http", httpLine
	} else if n > 0 && strings.HasPrefix(string(greeting[:n]), "HTTP/1.") {
		return "http", strings.TrimSpace(strings.SplitN(string(greeting[:n]), "\n", 2)[0])
	}
	if err != nil {
		return "socks5", "SOCKS5 handshake: " + err.Error()
	}
	return "socks5", "unknown proxy response"
}

func parseDNSServers(out string) []string {
	if strings.Contains(out, "There aren't any DNS Servers") {
		return nil
	}
	var servers []string
	for _, line := range strings.Split(out, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "There aren't") || strings.Contains(s, " ") {
			continue
		}
		servers = append(servers, s)
	}
	return servers
}

func sanitizeBackupDNS(servers []string) []string {
	var clean []string
	for _, s := range servers {
		s = strings.TrimSpace(s)
		if s == "" || s == tunDNSIP {
			continue
		}
		if net.ParseIP(s) != nil {
			clean = append(clean, s)
		}
	}
	if len(clean) == 0 {
		return []string{"Empty"}
	}
	return clean
}

func parseNetworkServices(out string) []string {
	var svcs []string
	for _, line := range strings.Split(out, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "An asterisk") || strings.HasPrefix(trim, "*") {
			continue
		}
		svcs = append(svcs, trim)
	}
	return svcs
}

func parseSocksProxyEnabled(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trim), "enabled:") {
			val := strings.TrimSpace(trim[len("enabled:"):])
			return strings.EqualFold(val, "yes")
		}
	}
	return false
}

func getDNSServers(service string) []string {
	if service == "" {
		return nil
	}
	out, err := runShell("/usr/sbin/networksetup", "-getdnsservers", service)
	if err != nil {
		return nil
	}
	return parseDNSServers(out)
}

func isDNSStuckOnService(service string) bool {
	for _, s := range getDNSServers(service) {
		if s == tunDNSIP {
			return true
		}
	}
	return false
}

func findStuckDNSServices(primarySvc string) []string {
	seen := make(map[string]bool)
	var stuck []string
	addIfStuck := func(svc string) {
		svc = strings.TrimSpace(svc)
		if svc == "" || seen[svc] {
			return
		}
		seen[svc] = true
		if isDNSStuckOnService(svc) {
			stuck = append(stuck, svc)
		}
	}
	addIfStuck(primarySvc)
	if out, err := runShell("/usr/sbin/networksetup", "-listallnetworkservices"); err == nil {
		for _, svc := range parseNetworkServices(out) {
			addIfStuck(svc)
		}
	}
	return stuck
}

func isProxyEnabled(service string) bool {
	if service == "" {
		return false
	}
	for _, getter := range []string{"-getsocksfirewallproxy", "-getwebproxy", "-getsecurewebproxy"} {
		out, err := runShell("/usr/sbin/networksetup", getter, service)
		if err == nil && parseSocksProxyEnabled(out) {
			return true
		}
	}
	return false
}

func disableAllSystemProxies(service string) {
	if service == "" {
		return
	}
	_, _ = runShell("/usr/sbin/networksetup", "-setsocksfirewallproxystate", service, "off")
	_, _ = runShell("/usr/sbin/networksetup", "-setwebproxystate", service, "off")
	_, _ = runShell("/usr/sbin/networksetup", "-setsecurewebproxystate", service, "off")
}

func writeFileSafe(path string, data []byte, perm os.FileMode) error {
	_ = os.Remove(path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

func saveDNSBackup(svc string, servers []string) {
	bkp := dnsBackup{
		Service: svc,
		Servers: sanitizeBackupDNS(servers),
	}
	if data, err := json.Marshal(bkp); err == nil {
		_ = writeFileSafe(dnsBackupPath, data, 0o600)
	}
}

func loadDNSBackup() (string, []string) {
	data, err := os.ReadFile(dnsBackupPath)
	if err != nil {
		return "", nil
	}
	var bkp dnsBackup
	if err := json.Unmarshal(data, &bkp); err != nil {
		return "", nil
	}
	return bkp.Service, sanitizeBackupDNS(bkp.Servers)
}

func buildDNSRestoreArgs(servers []string) string {
	clean := sanitizeBackupDNS(servers)
	parts := make([]string, 0, len(clean))
	for _, s := range clean {
		parts = append(parts, shellQuote(s))
	}
	return strings.Join(parts, " ")
}

func formatSingBoxLogLine(raw string) (msg string, dedupeKey string, ok bool) {
	clean := ansiRe.ReplaceAllString(raw, "")
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return "", "", false
	}
	clean = sbTimePrefixRe.ReplaceAllString(clean, "")
	clean = sbConnIDRe.ReplaceAllString(clean, "")
	clean = strings.TrimSpace(strings.Join(strings.Fields(clean), " "))
	if clean == "" {
		return "", "", false
	}

	// Keep concise key events/errors; drop noisy per-packet INFO chatter
	if strings.HasPrefix(clean, "INFO ") {
		if strings.Contains(clean, "inbound connection") ||
			strings.Contains(clean, "inbound DNS packet") ||
			strings.Contains(clean, "outbound connection") ||
			strings.Contains(clean, "inbound packet") {
			return "", "", false
		}
		if !strings.Contains(clean, "started") && !strings.Contains(clean, "tun") && !strings.Contains(clean, "closed") {
			return "", "", false
		}
	}

	key := sbTargetRe.ReplaceAllString(clean, "open connection")
	key = sbSrcPortRe.ReplaceAllString(key, "$1->")
	return "[sing-box] " + clean, key, true
}

func (b *Backend) tailSingBoxLog() {
	f, err := os.Open(logPath)
	if err != nil {
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return
	}

	b.mu.Lock()
	offset := b.logOffset
	b.mu.Unlock()

	if st.Size() < offset {
		offset = 0
	}
	if st.Size() == offset {
		return
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, 32*1024))
	if err != nil || len(data) == 0 {
		return
	}

	b.mu.Lock()
	b.logOffset = offset + int64(len(data))
	b.mu.Unlock()

	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		msg, key, ok := formatSingBoxLogLine(line)
		if !ok {
			continue
		}
		b.mu.Lock()
		if b.seenLogKeys == nil {
			b.seenLogKeys = make(map[string]bool)
		}
		seen := b.seenLogKeys[key]
		if !seen {
			b.seenLogKeys[key] = true
		}
		b.mu.Unlock()
		if !seen {
			b.AddLog(msg)
		}
	}
}

func (b *Backend) Status(manualGW string) StatusResponse {
	svc := detectService()
	b.mu.Lock()
	if b.connected && b.connectedMode == "tun" && b.connectedService != "" {
		svc = b.connectedService
	}
	b.mu.Unlock()

	gw := strings.TrimSpace(manualGW)
	if gw == "" {
		gw = detectGateway(svc)
	}
	reachable := probePort(gw, proxyPort)
	tunAlive := isSingBoxRunning()

	if tunAlive {
		b.tailSingBoxLog()
	}

	b.mu.Lock()
	dropped := false
	if b.connected && b.connectedMode == "tun" && !tunAlive {
		b.connected = false
		b.connectedMode = ""
		dropped = true
	}
	b.service = svc
	b.gateway = gw
	b.mu.Unlock()

	if dropped {
		b.tailSingBoxLog()
		b.AddLog("TUN-туннель sing-box завершился.")
	}

	dnsStuck := !tunAlive && len(findStuckDNSServices(svc)) > 0

	b.mu.Lock()
	resp := StatusResponse{
		HasSingBox:    b.HasSingBox(),
		Connected:     b.connected,
		ConnectedMode: b.connectedMode,
		Service:       b.service,
		Gateway:       b.gateway,
		Reachable:     reachable,
		DNSStuck:      dnsStuck,
		Logs:          append([]string(nil), b.logs...),
	}
	b.mu.Unlock()
	return resp
}

func buildSingBoxConfig(gateway string, port int, proto string) ([]byte, error) {
	proxyOutbound := map[string]any{
		"type":        "socks",
		"tag":         "proxy-out",
		"server":      gateway,
		"server_port": port,
		"version":     "5",
		"network":     "tcp",
	}
	if proto == "http" {
		proxyOutbound = map[string]any{
			"type":        "http",
			"tag":         "proxy-out",
			"server":      gateway,
			"server_port": port,
		}
	}
	cfg := map[string]any{
		"log": map[string]any{
			"level":     "warn",
			"timestamp": true,
		},
		"dns": map[string]any{
			"servers": []map[string]any{
				{
					"tag":         "doh_cf",
					"type":        "https",
					"server":      "1.1.1.1",
					"server_port": 443,
					"path":        "/dns-query",
					"detour":      "proxy-out",
				},
			},
			"final":    "doh_cf",
			"strategy": "ipv4_only",
		},
		"inbounds": []map[string]any{
			{
				"type":         "tun",
				"tag":          "tun-in",
				"address":      []string{"172.19.0.1/30"},
				"mtu":          1500,
				"auto_route":   true,
				"strict_route": false,
				"stack":        "gvisor",
				"dns_mode":     "hijack",
				"dns_address":  []string{"172.19.0.2"},
			},
		},
		"outbounds": []map[string]any{
			proxyOutbound,
			{
				"type": "direct",
				"tag":  "direct-out",
			},
		},
		"route": map[string]any{
			"rules": []map[string]any{
				{"action": "sniff"},
				{"port": []int{53}, "action": "hijack-dns"},
				{"protocol": "dns", "action": "hijack-dns"},
				{"ip_is_private": true, "outbound": "direct-out"},
				{"network": "udp", "action": "reject"},
			},
			"final":                 "proxy-out",
			"auto_detect_interface": true,
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func (b *Backend) Connect(mode, manualGW string) StatusResponse {
	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	b.resetLogDedupe()

	st := b.Status(manualGW)
	gw := st.Gateway
	svc := st.Service
	if !isValidIPv4(gw) {
		b.AddLog("Шлюз не обнаружен. Подключите Mac к Wi-Fi / точке доступа телефона.")
		return b.Status(manualGW)
	}
	if !st.Reachable {
		b.AddLog(fmt.Sprintf("Порт %d на %s не отвечает. Проверьте ShareNet на телефоне.", proxyPort, gw))
		return b.Status(manualGW)
	}

	proto, protoDiag := detectProxyProtocol(gw, proxyPort)
	if protoDiag != "" {
		b.AddLog(fmt.Sprintf("Проверка %s:%d (%s): %s", gw, proxyPort, strings.ToUpper(proto), protoDiag))
	} else {
		b.AddLog(fmt.Sprintf("Шлюз %s:%d подтверждён (%s).", gw, proxyPort, strings.ToUpper(proto)))
	}

	if mode == "" || (mode == "tun" && !b.HasSingBox()) {
		mode = "proxy"
	}

	if mode == "proxy" {
		disableAllSystemProxies(svc)
		if proto == "http" {
			portStr := fmt.Sprintf("%d", proxyPort)
			if _, err := runShell("/usr/sbin/networksetup", "-setwebproxy", svc, gw, portStr); err != nil {
				b.AddLog("Ошибка настройки HTTP прокси: " + err.Error())
				return b.Status(manualGW)
			}
			if _, err := runShell("/usr/sbin/networksetup", "-setsecurewebproxy", svc, gw, portStr); err != nil {
				b.AddLog("Ошибка настройки HTTPS прокси: " + err.Error())
				return b.Status(manualGW)
			}
			_, _ = runShell("/usr/sbin/networksetup", "-setwebproxystate", svc, "on")
			if out, err := runShell("/usr/sbin/networksetup", "-setsecurewebproxystate", svc, "on"); err != nil {
				b.AddLog("Ошибка включения HTTPS прокси: " + out)
				return b.Status(manualGW)
			}
			b.mu.Lock()
			b.connected = true
			b.connectedMode = "proxy"
			b.connectedService = svc
			b.proxyProto = "http"
			b.mu.Unlock()
			b.AddLog(fmt.Sprintf("Системный HTTP/HTTPS прокси включён -> %s:%d", gw, proxyPort))
			go b.CheckNetwork(manualGW)
			return b.Status(manualGW)
		}

		if _, err := runShell("/usr/sbin/networksetup", "-setsocksfirewallproxy", svc, gw, fmt.Sprintf("%d", proxyPort)); err != nil {
			b.AddLog("Ошибка настройки SOCKS5 прокси: " + err.Error())
			return b.Status(manualGW)
		}
		if out, err := runShell("/usr/sbin/networksetup", "-setsocksfirewallproxystate", svc, "on"); err != nil {
			b.AddLog("Ошибка включения прокси: " + out)
			return b.Status(manualGW)
		}
		b.mu.Lock()
		b.connected = true
		b.connectedMode = "proxy"
		b.connectedService = svc
		b.proxyProto = "socks5"
		b.mu.Unlock()
		b.AddLog(fmt.Sprintf("Системный SOCKS5 включён -> %s:%d", gw, proxyPort))
		go b.CheckNetwork(manualGW)
		return b.Status(manualGW)
	}

	rawCfg, err := buildSingBoxConfig(gw, proxyPort, proto)
	if err != nil {
		b.AddLog("Ошибка формирования конфига: " + err.Error())
		return b.Status(manualGW)
	}
	if err := writeFileSafe(cfgPath, rawCfg, 0o600); err != nil {
		b.AddLog("Не удалось записать " + cfgPath + ": " + err.Error())
		return b.Status(manualGW)
	}
	_ = writeFileSafe(logPath, nil, 0o600)
	_ = os.Remove(b.stopFlagPath)
	b.mu.Lock()
	b.logOffset = 0
	b.mu.Unlock()

	_ = os.Chmod(b.singBoxPath, 0o755)
	_, _ = runShell("/usr/bin/xattr", "-d", "com.apple.quarantine", b.singBoxPath)

	if chkOut, err := runShell(b.singBoxPath, "check", "-c", cfgPath); err != nil {
		b.AddLog("sing-box не смог запуститься (" + chkOut + "). Переключите режим на «Системный прокси» — он работает на любой macOS без пароля.")
		return b.Status(manualGW)
	}

	disableAllSystemProxies(svc)

	backupDNS := sanitizeBackupDNS(getDNSServers(svc))
	saveDNSBackup(svc, backupDNS)
	backupArgs := buildDNSRestoreArgs(backupDNS)
	runToken := fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())

	// Use Perl setsid() and close FDs >= 3 so AppleScript `do shell script ... with administrator privileges`
	// cannot block waiting on inherited authorization pipe descriptors held by background children.
	perlDaemon := `use POSIX qw(setsid); my ($log,$pidf,@c)=@ARGV; my $p=fork(); die "fork: $!" unless defined $p; if($p==0){ setsid(); $SIG{HUP}="IGNORE"; POSIX::close($_) for 3..64; open(STDIN,"<","/dev/null"); open(STDOUT,">",$log); open(STDERR,">&STDOUT"); exec @c or exit 127; } if($pidf ne ""){ open(my $f,">",$pidf); print $f "$p\n"; close($f); }`

	watchdogScript := `trap "" HUP; SB_PID="$1"; APP_PID="$2"; SVC="$3"; STOP_FLAG="$4"; CFG="$5"; PIDF="$6"; BKP="$7"; TOK_FILE="$8"; MY_TOK="$9"; shift 9; ` +
		`while kill -0 "$SB_PID" 2>/dev/null && kill -0 "$APP_PID" 2>/dev/null && [ ! -f "$STOP_FLAG" ] && [ "$(cat "$TOK_FILE" 2>/dev/null)" = "$MY_TOK" ]; do sleep 1; done; ` +
		`kill -INT "$SB_PID" >/dev/null 2>&1 || true; ` +
		`i=0; while [ "$i" -lt 15 ] && kill -0 "$SB_PID" 2>/dev/null; do sleep 0.2; i=$((i+1)); done; ` +
		`kill -TERM "$SB_PID" >/dev/null 2>&1 || true; ` +
		`if [ "$(cat "$TOK_FILE" 2>/dev/null)" = "$MY_TOK" ]; then ` +
		`networksetup -setdnsservers "$SVC" "$@" >/dev/null 2>&1 || true; ` +
		`dscacheutil -flushcache >/dev/null 2>&1 || true; ` +
		`killall -HUP mDNSResponder >/dev/null 2>&1 || true; ` +
		`rm -f "$CFG" "$PIDF" "$STOP_FLAG" "$BKP" "$TOK_FILE"; ` +
		`fi`

	adminCmd := fmt.Sprintf(
		"rm -f %s; "+
			"printf '%%s\\n' %s > %s; "+
			"OLD_PID=$(cat %s 2>/dev/null || true); "+
			"if [ -n \"$OLD_PID\" ]; then kill -INT \"$OLD_PID\" >/dev/null 2>&1 || true; fi; "+
			"pkill -INT -f %s >/dev/null 2>&1 || true; "+
			"w=0; while [ \"$w\" -lt 20 ] && pgrep -f %s >/dev/null 2>&1; do sleep 0.1; w=$((w+1)); done; "+
			"pkill -TERM -f %s >/dev/null 2>&1 || true; "+
			"/usr/bin/perl -MPOSIX=setsid -e %s %s %s %s run -c %s; "+
			"SB_PID=$(cat %s 2>/dev/null || true); "+
			"if [ -z \"$SB_PID\" ]; then echo 'missing sing-box pid'; exit 1; fi; "+
			"sleep 1.0; "+
			"if ! kill -0 \"$SB_PID\" 2>/dev/null; then echo 'sing-box exited early'; exit 1; fi; "+
			"if ! networksetup -setdnsservers %s %s; then kill -INT \"$SB_PID\" >/dev/null 2>&1 || true; exit 1; fi; "+
			"dscacheutil -flushcache >/dev/null 2>&1 || true; "+
			"killall -HUP mDNSResponder >/dev/null 2>&1 || true; "+
			"/usr/bin/perl -MPOSIX=setsid -e %s /dev/null '' /bin/sh -c %s _ \"$SB_PID\" %d %s %s %s %s %s %s %s %s",
		shellQuote(b.stopFlagPath),
		shellQuote(runToken), shellQuote(tunTokenPath),
		shellQuote(pidPath),
		shellQuote(singBoxPgrepPattern),
		shellQuote(singBoxPgrepPattern),
		shellQuote(singBoxPgrepPattern),
		shellQuote(perlDaemon), shellQuote(logPath), shellQuote(pidPath), shellQuote(b.singBoxPath), shellQuote(cfgPath),
		shellQuote(pidPath),
		shellQuote(svc), shellQuote(tunDNSIP),
		shellQuote(perlDaemon), shellQuote(watchdogScript),
		os.Getpid(),
		shellQuote(svc),
		shellQuote(b.stopFlagPath),
		shellQuote(cfgPath),
		shellQuote(pidPath),
		shellQuote(dnsBackupPath),
		shellQuote(tunTokenPath),
		shellQuote(runToken),
		backupArgs,
	)

	if out, err := runAdminShell(adminCmd); err != nil {
		b.tailSingBoxLog()
		errDesc := err.Error()
		if out != "" {
			errDesc += ": " + out
		}
		b.AddLog("Запуск TUN не выполнен (" + errDesc + ")")
		if isSingBoxRunning() {
			_, _ = runAdminShell(b.buildRestoreDNSShellCmd([]string{svc}, svc, backupDNS))
		} else {
			b.restoreStuckDNSIfNeeded(svc)
		}
		return b.Status(manualGW)
	}

	if !isSingBoxRunning() {
		b.tailSingBoxLog()
		tail, _ := os.ReadFile(logPath)
		b.AddLog("sing-box завершился после запуска: " + strings.TrimSpace(string(tail)))
		b.restoreStuckDNSIfNeeded(svc)
		return b.Status(manualGW)
	}

	b.tailSingBoxLog()
	b.mu.Lock()
	b.connected = true
	b.connectedMode = "tun"
	b.connectedService = svc
	b.proxyProto = proto
	b.dnsBackup = backupDNS
	b.mu.Unlock()
	b.AddLog(fmt.Sprintf("VPN-туннель активен (%s: utun + DNS %s, протокол %s).", svc, tunDNSIP, strings.ToUpper(proto)))
	go b.CheckNetwork(manualGW)
	return b.Status(manualGW)
}

func (b *Backend) buildRestoreDNSShellCmd(services []string, primarySvc string, primaryBackup []string) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("OLD_PID=$(cat %s 2>/dev/null || true); if [ -n \"$OLD_PID\" ]; then kill -INT \"$OLD_PID\" >/dev/null 2>&1 || true; fi", shellQuote(pidPath)))
	parts = append(parts, fmt.Sprintf("pkill -INT -f %s >/dev/null 2>&1 || true", shellQuote(singBoxPgrepPattern)))
	parts = append(parts, fmt.Sprintf("w=0; while [ \"$w\" -lt 15 ] && pgrep -f %s >/dev/null 2>&1; do sleep 0.1; w=$((w+1)); done", shellQuote(singBoxPgrepPattern)))
	parts = append(parts, fmt.Sprintf("pkill -TERM -f %s >/dev/null 2>&1 || true", shellQuote(singBoxPgrepPattern)))
	seen := make(map[string]bool)
	for _, svc := range services {
		svc = strings.TrimSpace(svc)
		if svc == "" || seen[svc] {
			continue
		}
		seen[svc] = true
		restoreArgs := shellQuote("Empty")
		if svc == primarySvc && len(primaryBackup) > 0 {
			restoreArgs = buildDNSRestoreArgs(primaryBackup)
		}
		parts = append(parts, fmt.Sprintf("networksetup -setdnsservers %s %s >/dev/null 2>&1 || true", shellQuote(svc), restoreArgs))
	}
	parts = append(parts,
		"dscacheutil -flushcache >/dev/null 2>&1 || true",
		"killall -HUP mDNSResponder >/dev/null 2>&1 || true",
		fmt.Sprintf("rm -f %s %s %s %s %s", shellQuote(cfgPath), shellQuote(pidPath), shellQuote(b.stopFlagPath), shellQuote(dnsBackupPath), shellQuote(tunTokenPath)),
	)
	return strings.Join(parts, "; ")
}

func (b *Backend) restoreStuckDNSIfNeeded(svc string) {
	stuck := findStuckDNSServices(svc)
	if len(stuck) == 0 {
		return
	}
	bkpSvc, bkpServers := loadDNSBackup()
	if bkpSvc == "" {
		bkpSvc = svc
	}
	b.AddLog(fmt.Sprintf("Восстанавливаю DNS (%s)...", strings.Join(stuck, ", ")))
	_, _ = runAdminShell(b.buildRestoreDNSShellCmd(stuck, bkpSvc, bkpServers))
}

func (b *Backend) Disconnect() StatusResponse {
	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	b.resetLogDedupe()

	b.mu.Lock()
	svc := b.connectedService
	if svc == "" {
		svc = b.service
	}
	prevMode := b.connectedMode
	backup := append([]string(nil), b.dnsBackup...)
	b.mu.Unlock()

	bkpSvc, bkpServers := loadDNSBackup()
	if svc == "" {
		svc = detectService()
	}
	if bkpSvc != "" && len(backup) == 0 {
		backup = bkpServers
	}
	if bkpSvc == "" {
		bkpSvc = svc
	}

	tunRunning := isSingBoxRunning()
	stuckServices := findStuckDNSServices(svc)

	if prevMode == "tun" || tunRunning || len(stuckServices) > 0 {
		b.AddLog("Останавливаю TUN-туннель и восстанавливаю DNS...")
		if tunRunning && b.stopFlagPath != "" {
			_ = writeFileSafe(b.stopFlagPath, []byte("stop\n"), 0o600)
			for i := 0; i < 18; i++ {
				if !isSingBoxRunning() && len(findStuckDNSServices(svc)) == 0 {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
		}

		stillRunning := isSingBoxRunning()
		stillStuck := findStuckDNSServices(svc)
		if stillRunning || len(stillStuck) > 0 {
			targets := stillStuck
			if len(targets) == 0 {
				targets = []string{svc}
			}
			_, _ = runAdminShell(b.buildRestoreDNSShellCmd(targets, bkpSvc, backup))
		} else {
			_ = os.Remove(cfgPath)
			_ = os.Remove(pidPath)
			_ = os.Remove(b.stopFlagPath)
			_ = os.Remove(dnsBackupPath)
			_ = os.Remove(tunTokenPath)
		}

		if remaining := findStuckDNSServices(svc); len(remaining) == 0 {
			b.AddLog("VPN остановлен, DNS восстановлен.")
		} else {
			b.AddLog("Внимание: DNS 172.19.0.2 остался на " + strings.Join(remaining, ", ") + " (отменён ввод пароля).")
		}
	}

	if prevMode == "proxy" || isProxyEnabled(svc) {
		disableAllSystemProxies(svc)
		b.AddLog("Системный прокси отключён.")
	}

	b.mu.Lock()
	b.connected = false
	b.connectedMode = ""
	b.connectedService = ""
	b.proxyProto = ""
	b.dnsBackup = nil
	b.mu.Unlock()
	return b.Status("")
}

func parseCurlCheckOutput(out string) (httpCode string, errDetail string) {
	var errLines []string
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if strings.HasPrefix(trim, "CODE:") {
			httpCode = strings.TrimSpace(strings.TrimPrefix(trim, "CODE:"))
			continue
		}
		errLines = append(errLines, trim)
	}
	return httpCode, strings.Join(errLines, "; ")
}

func (b *Backend) CheckNetwork(manualGW string) StatusResponse {
	st := b.Status(manualGW)
	b.mu.Lock()
	activeProto := b.proxyProto
	b.mu.Unlock()

	var args []string
	via := "прямо"
	useProxy := (st.ConnectedMode == "proxy" || (st.ConnectedMode == "" && st.Reachable)) && isValidIPv4(st.Gateway)
	if useProxy {
		proto := activeProto
		if proto == "" {
			proto, _ = detectProxyProtocol(st.Gateway, proxyPort)
		}
		if proto == "http" {
			args = append(args, "--proxy", fmt.Sprintf("http://%s:%d", st.Gateway, proxyPort))
			via = "HTTP прокси"
		} else {
			args = append(args, "--socks5-hostname", fmt.Sprintf("%s:%d", st.Gateway, proxyPort))
			via = "SOCKS5 прокси"
		}
	} else {
		args = append(args, "--noproxy", "*")
		if st.ConnectedMode == "tun" {
			via = "TUN-туннель"
		}
	}
	args = append(args, "-sS", "-m", "6", "-o", "/dev/null", "-w", "\nCODE:%{http_code}", "https://www.google.com/generate_204")
	out, err := runShell("/usr/bin/curl", args...)
	code, detail := parseCurlCheckOutput(out)
	if code == "" {
		code = "000"
	}
	if err != nil || code == "000" {
		if detail == "" && err != nil {
			detail = err.Error()
		}
		if detail != "" {
			b.AddLog(fmt.Sprintf("Проверка сети (%s): HTTP %s — %s", via, code, detail))
		} else {
			b.AddLog(fmt.Sprintf("Проверка сети (%s): HTTP %s", via, code))
		}
	} else {
		b.AddLog(fmt.Sprintf("Проверка сети (%s): HTTP %s", via, code))
	}
	return b.Status(manualGW)
}

func copyToClipboard(text string) error {
	cmd := exec.Command("/usr/bin/pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func (b *Backend) CopyAddress(manualGW string) StatusResponse {
	st := b.Status(manualGW)
	gw := strings.TrimSpace(manualGW)
	if gw == "" {
		gw = st.Gateway
	}
	if gw == "" {
		b.AddLog("IP шлюза пустой.")
		return st
	}
	addr := fmt.Sprintf("%s:%d", gw, proxyPort)
	if err := copyToClipboard(addr); err != nil {
		b.AddLog("Ошибка копирования: " + err.Error())
	} else {
		b.AddLog("Скопировано в буфер: " + addr)
	}
	return st
}

func (b *Backend) CopyLogs(text string) map[string]any {
	clean := strings.TrimSpace(ansiRe.ReplaceAllString(text, ""))
	if clean == "" {
		b.mu.Lock()
		clean = strings.Join(b.logs, "\n")
		b.mu.Unlock()
	}
	if len(clean) > 3900 {
		clean = clean[len(clean)-3900:]
		if idx := strings.IndexByte(clean, '\n'); idx > 0 {
			clean = clean[idx+1:]
		}
	}
	if err := copyToClipboard(clean); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	return map[string]any{"ok": true}
}

func (b *Backend) OpenGitHub() StatusResponse {
	_, _ = runShell("/usr/bin/open", githubURL)
	return b.Status("")
}

func resolveSingBox(resDir, appParent string) string {
	candidates := []string{
		filepath.Join(resDir, "sing-box"),
		filepath.Join(appParent, "sing-box"),
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
