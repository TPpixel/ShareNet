package adb

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"com.tppixel/sharenetusb/internal/netutil"
)

const (
	TargetHotspotProxyVersionCode = 36
	TargetUsbNetVersionCode       = 12
)

type CommandResult struct {
	ExitCode int
	Output   string
}

type DeviceInfo struct {
	Serial string `json:"serial"`
	State  string `json:"state"`
	Model  string `json:"model"`
}

type PrivateDnsBackup struct {
	WasModified bool
	Mode        string
	Specifier   string
}

type HotspotStatus struct {
	IsActive      bool   `json:"isActive"`
	InterfaceName string `json:"interfaceName"`
	IpAddress     string `json:"ipAddress"`
}

type Helper struct {
	AppRoot                 string
	BundledPlatformToolsDir string
	GnirehtetDir            string
	SingBoxDir              string
	AdbExePath              string
	GnirehtetExePath        string
	GnirehtetApkPath        string
	HotspotProxyApkPath     string
	SingBoxExePath          string
	SingBoxAndroidPath      string
}

var (
	reVersionCode = regexp.MustCompile(`versionCode=(\d+)`)
	reTethered    = regexp.MustCompile(`(?m)^\s*([a-zA-Z0-9_.-]+)\s*-\s*TetheredState`)
	reIfaceHdr    = regexp.MustCompile(`^\d+:\s+([^:@\s]+)`)
	reInet4       = regexp.MustCompile(`\binet\s+(\d+\.\d+\.\d+\.\d+)/\d+`)
	reValidIface  = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,32}$`)
)

// IsValidIfaceName checks whether iface is a safe Android network interface name.
func IsValidIfaceName(iface string) bool {
	return reValidIface.MatchString(strings.TrimSpace(iface))
}

func NewHelper() *Helper {
	return NewHelperWithRoot("")
}

func NewHelperWithRoot(customRoot string) *Helper {
	appRoot := strings.TrimSpace(customRoot)
	if appRoot == "" || !fileExists(filepath.Join(appRoot, "tools", "platform-tools", "adb.exe")) {
		exe, _ := os.Executable()
		appRoot = filepath.Dir(exe)
		if !fileExists(filepath.Join(appRoot, "tools", "platform-tools", "adb.exe")) {
			if wd, err := os.Getwd(); err == nil && fileExists(filepath.Join(wd, "tools", "platform-tools", "adb.exe")) {
				appRoot = wd
			}
		}
	}

	bundledPt := filepath.Join(appRoot, "tools", "platform-tools")
	gnDir := filepath.Join(appRoot, "tools", "gnirehtet")
	adbPath := filepath.Join(bundledPt, "adb.exe")
	if !fileExists(adbPath) {
		localAppData := os.Getenv("LOCALAPPDATA")
		sdkAdb := filepath.Join(localAppData, "Android", "Sdk", "platform-tools", "adb.exe")
		if fileExists(sdkAdb) {
			adbPath = sdkAdb
		} else {
			adbPath = "adb.exe"
		}
	}

	sbDir := filepath.Join(appRoot, "tools", "sing-box")
	return &Helper{
		AppRoot:                 appRoot,
		BundledPlatformToolsDir: bundledPt,
		GnirehtetDir:            gnDir,
		SingBoxDir:              sbDir,
		AdbExePath:              adbPath,
		GnirehtetExePath:        filepath.Join(gnDir, "gnirehtet.exe"),
		GnirehtetApkPath:        filepath.Join(gnDir, "gnirehtet.apk"),
		HotspotProxyApkPath:     filepath.Join(appRoot, "tools", "hotspot-proxy", "hotspot-proxy.apk"),
		SingBoxExePath:          filepath.Join(sbDir, "sing-box.exe"),
		SingBoxAndroidPath:      filepath.Join(sbDir, "sing-box-android"),
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func (h *Helper) EffectiveEnv() []string {
	adbDir := filepath.Dir(h.AdbExePath)
	env := os.Environ()
	if adbDir == "" || adbDir == "." {
		return env
	}
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if len(kv) >= 5 && strings.EqualFold(kv[:5], "PATH=") {
			out = append(out, "PATH="+adbDir+";"+kv[5:])
			found = true
		} else {
			out = append(out, kv)
		}
	}
	if !found {
		out = append(out, "PATH="+adbDir)
	}
	return out
}

func (h *Helper) RunAdb(timeout time.Duration, args ...string) CommandResult {
	return runCommand(h.AdbExePath, "", nil, timeout, args...)
}

func (h *Helper) RunGnirehtet(timeout time.Duration, args ...string) CommandResult {
	return runCommand(h.GnirehtetExePath, h.GnirehtetDir, h.EffectiveEnv(), timeout, args...)
}

func runCommand(bin, dir string, env []string, timeout time.Duration, args ...string) CommandResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = env
	}
	netutil.HideWindow(cmd)

	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return CommandResult{ExitCode: -1, Output: fmt.Sprintf("Превышено время ожидания %s.", filepath.Base(bin))}
	}
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
			outBuf.WriteString("\n" + err.Error())
		}
	}
	return CommandResult{ExitCode: code, Output: strings.TrimSpace(outBuf.String())}
}

func ParseDevicesOutput(raw string) []DeviceInfo {
	var devices []DeviceInfo
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		model := ""
		for _, tok := range fields[2:] {
			if strings.HasPrefix(tok, "model:") {
				model = strings.ReplaceAll(strings.TrimPrefix(tok, "model:"), "_", " ")
				break
			}
		}
		devices = append(devices, DeviceInfo{
			Serial: fields[0],
			State:  fields[1],
			Model:  model,
		})
	}
	return devices
}

func (h *Helper) GetDevices() []DeviceInfo {
	res := h.RunAdb(8*time.Second, "devices", "-l")
	if res.ExitCode != 0 || res.Output == "" {
		return nil
	}
	return ParseDevicesOutput(res.Output)
}

func (h *Helper) GetInstalledVersionCode(serial, pkg string) int {
	args := serialArgs(serial, "shell", "dumpsys", "package", pkg)
	res := h.RunAdb(8*time.Second, args...)
	if res.ExitCode != 0 || res.Output == "" {
		return 0
	}
	if m := reVersionCode.FindStringSubmatch(res.Output); len(m) == 2 {
		v, _ := strconv.Atoi(m[1])
		return v
	}
	return 0
}

func (h *Helper) IsPackageInstalled(serial, pkg string) bool {
	args := serialArgs(serial, "shell", "pm", "list", "packages", pkg)
	res := h.RunAdb(6*time.Second, args...)
	return res.ExitCode == 0 && strings.Contains(strings.ToLower(res.Output), "package:"+strings.ToLower(pkg))
}

func (h *Helper) installOrReplaceApk(serial, apkPath, pkg string) CommandResult {
	res := h.RunAdb(60*time.Second, serialArgs(serial, "install", "-r", apkPath)...)
	if res.ExitCode != 0 && (strings.Contains(res.Output, "INSTALL_FAILED_UPDATE_INCOMPATIBLE") || strings.Contains(res.Output, "INSTALL_FAILED_VERSION_DOWNGRADE")) {
		_ = h.RunAdb(30*time.Second, serialArgs(serial, "uninstall", pkg)...)
		res = h.RunAdb(60*time.Second, serialArgs(serial, "install", apkPath)...)
	}
	return res
}

func (h *Helper) EnsureRequiredApksInstalled(serial string, installHotspotProxy, forceReinstall bool, logFn func(string)) {
	if fileExists(h.GnirehtetApkPath) {
		ver := h.GetInstalledVersionCode(serial, "com.genymobile.gnirehtet")
		if forceReinstall || ver < TargetUsbNetVersionCode {
			logFn("Установка/обновление UsbNet на телефон...")
			res := h.installOrReplaceApk(serial, h.GnirehtetApkPath, "com.genymobile.gnirehtet")
			logFn("UsbNet (gnirehtet.apk): " + res.Output)
		}
	}
	if installHotspotProxy && fileExists(h.HotspotProxyApkPath) {
		ver := h.GetInstalledVersionCode(serial, "com.tppixel.sharenet")
		if forceReinstall || ver < TargetHotspotProxyVersionCode {
			logFn("Установка/обновление ShareNet на телефон...")
			res := h.installOrReplaceApk(serial, h.HotspotProxyApkPath, "com.tppixel.sharenet")
			logFn("hotspot-proxy.apk: " + res.Output)
		}
	}
}

func (h *Helper) StartHotspotProxyApp(serial string, port int) CommandResult {
	return h.RunAdb(8*time.Second, serialArgs(serial,
		"shell", "am", "start",
		"-n", "com.tppixel.sharenet/.MainActivity",
		"--ez", "autostart", "true",
		"--ei", "port", strconv.Itoa(port),
	)...)
}

func (h *Helper) StopHotspotProxyApp(serial string) CommandResult {
	return h.RunAdb(8*time.Second, serialArgs(serial,
		"shell", "am", "start",
		"-n", "com.tppixel.sharenet/.MainActivity",
		"--ez", "autostop", "true",
		"--ez", "stopvpn", "true",
	)...)
}

func (h *Helper) OpenHotspotSettings(serial string) CommandResult {
	return h.RunAdb(8*time.Second, serialArgs(serial,
		"shell", "am", "start", "-a", "android.settings.TETHER_SETTINGS",
	)...)
}

func (h *Helper) ResetGnirehtetState(serial string) {
	_ = runCommand("taskkill.exe", "", nil, 3*time.Second, "/F", "/IM", "gnirehtet.exe")

	if serial != "" {
		_ = h.RunGnirehtet(3*time.Second, "stop", serial)
		_ = h.RunAdb(5*time.Second, "-s", serial, "shell", "am", "force-stop", "com.genymobile.gnirehtet")
	}
}

func ParseHotspotStatus(tetherOutput, ipAddrOutput string) HotspotStatus {
	var st HotspotStatus
	tetheredIface := ""
	if m := reTethered.FindStringSubmatch(tetherOutput); len(m) == 2 {
		tetheredIface = strings.TrimSpace(m[1])
	}

	ifaceIps := make(map[string]string)
	currentIface := ""
	for _, line := range strings.Split(strings.ReplaceAll(ipAddrOutput, "\r\n", "\n"), "\n") {
		if hdr := reIfaceHdr.FindStringSubmatch(line); len(hdr) == 2 {
			currentIface = hdr[1]
			continue
		}
		if currentIface != "" {
			if m := reInet4.FindStringSubmatch(line); len(m) == 2 {
				ip := m[1]
				if ip != "127.0.0.1" && ip != "10.0.0.2" {
					if _, exists := ifaceIps[currentIface]; !exists {
						ifaceIps[currentIface] = ip
					}
				}
			}
		}
	}

	if tetheredIface != "" {
		if ip, ok := ifaceIps[tetheredIface]; ok {
			st.IsActive = true
			st.InterfaceName = tetheredIface
			st.IpAddress = ip
			return st
		}
	}

	for _, pref := range []string{"ap", "swlan", "softap", "wlan2", "wlan1"} {
		for iface, ip := range ifaceIps {
			if strings.HasPrefix(strings.ToLower(iface), pref) {
				st.IsActive = true
				st.InterfaceName = iface
				st.IpAddress = ip
				return st
			}
		}
	}
	return st
}

func (h *Helper) DetectHotspotStatus(serial string) HotspotStatus {
	if serial == "" {
		return HotspotStatus{}
	}
	tRes := h.RunAdb(6*time.Second, "-s", serial, "shell", "dumpsys", "tethering")
	ipRes := h.RunAdb(6*time.Second, "-s", serial, "shell", "ip", "-4", "addr")
	if ipRes.ExitCode != 0 || ipRes.Output == "" {
		return HotspotStatus{}
	}
	return ParseHotspotStatus(tRes.Output, ipRes.Output)
}

func (h *Helper) DisablePrivateDnsTemporarily(serial string) PrivateDnsBackup {
	mode := strings.TrimSpace(h.RunAdb(5*time.Second, serialArgs(serial, "shell", "settings", "get", "global", "private_dns_mode")...).Output)
	spec := strings.TrimSpace(h.RunAdb(5*time.Second, serialArgs(serial, "shell", "settings", "get", "global", "private_dns_specifier")...).Output)
	if strings.EqualFold(mode, "hostname") {
		_ = h.RunAdb(5*time.Second, serialArgs(serial, "shell", "settings", "put", "global", "private_dns_mode", "off")...)
		return PrivateDnsBackup{
			WasModified: true,
			Mode:        mode,
			Specifier:   spec,
		}
	}
	return PrivateDnsBackup{WasModified: false}
}

func (h *Helper) RestorePrivateDns(serial string, backup PrivateDnsBackup) {
	if !backup.WasModified {
		return
	}
	if backup.Specifier != "" && backup.Specifier != "null" {
		_ = h.RunAdb(5*time.Second, serialArgs(serial, "shell", "settings", "put", "global", "private_dns_specifier", backup.Specifier)...)
	}
	_ = h.RunAdb(5*time.Second, serialArgs(serial, "shell", "settings", "put", "global", "private_dns_mode", backup.Mode)...)
}

func (h *Helper) SendFileSmart(serial, localPath string) string {
	ext := strings.ToLower(filepath.Ext(localPath))
	fileName := filepath.Base(localPath)

	if ext == ".apk" {
		res := h.RunAdb(60*time.Second, serialArgs(serial, "install", "-r", localPath)...)
		return fmt.Sprintf("Установка APK (%s): %s", fileName, res.Output)
	}

	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".jfif":
		remoteName := fileName
		if ext == ".jfif" {
			remoteName = strings.TrimSuffix(fileName, filepath.Ext(fileName)) + ".jpg"
		}
		remotePath := "/sdcard/Pictures/" + remoteName
		res := h.RunAdb(60*time.Second, serialArgs(serial, "push", localPath, remotePath)...)
		if res.ExitCode == 0 {
			_ = h.RunAdb(10*time.Second, serialArgs(serial, "shell", "content", "call", "--uri", "content://media", "--method", "scan_volume", "--arg", "external_primary")...)
			return fmt.Sprintf("Изображение отправлено в %s (проиндексировано для галереи).\n%s", remotePath, res.Output)
		}
		return "Ошибка отправки изображения: " + res.Output
	}

	downloadPath := "/sdcard/Download/" + fileName
	res := h.RunAdb(60*time.Second, serialArgs(serial, "push", localPath, downloadPath)...)
	return fmt.Sprintf("Файл отправлен в %s:\n%s", downloadPath, res.Output)
}

func serialArgs(serial string, rest ...string) []string {
	if serial == "" {
		return rest
	}
	args := make([]string, 0, len(rest)+2)
	args = append(args, "-s", serial)
	args = append(args, rest...)
	return args
}

// RootHotspotState holds the active transparent NAT state applied on a rooted Android device.
type RootHotspotState struct {
	Applied   bool   `json:"applied"`
	TunIface  string `json:"tunIface"`
	ApIface   string `json:"apIface"`
	DnsServer string `json:"dnsServer"`
}

// CheckRootStatus returns "rooted" if su grants uid=0, "denied" if su exists without permission, or "none".
func (h *Helper) CheckRootStatus(serial string) string {
	if serial == "" {
		return "none"
	}
	res := h.RunAdb(4*time.Second, serialArgs(serial, "shell", "su", "-c", "id")...)
	if strings.Contains(res.Output, "uid=0(") {
		return "rooted"
	}
	whichRes := h.RunAdb(3*time.Second, serialArgs(serial, "shell", "which su || ls /debug_ramdisk/su /sbin/su /system_ext/bin/su /system/xbin/su 2>/dev/null")...)
	if strings.Contains(whichRes.Output, "/su") {
		return "denied"
	}
	return "none"
}

// CheckRootAccess checks whether ADB shell can obtain uid=0 via su (Magisk / KernelSU / APatch).
func (h *Helper) CheckRootAccess(serial string) bool {
	return h.CheckRootStatus(serial) == "rooted"
}

// PrimaryDnsIP extracts the best IPv4 DNS server for transparent DNAT on the phone.
func PrimaryDnsIP(dnsCsv string) string {
	var first string
	for _, part := range strings.FieldsFunc(dnsCsv, func(r rune) bool {
		return r == ',' || r == ':' || r == ';' || r == ' '
	}) {
		p := strings.TrimSpace(part)
		if !isSimpleIPv4(p) {
			continue
		}
		if first == "" {
			first = p
		}
		// Prefer public DNS over local router LAN IPs for reliable DNAT through tun0.
		if !strings.HasPrefix(p, "192.168.") && !strings.HasPrefix(p, "10.") && !strings.HasPrefix(p, "127.") {
			return p
		}
	}
	if first != "" {
		return first
	}
	return "1.1.1.1"
}

func isSimpleIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || v > 255 {
			return false
		}
	}
	return true
}

const (
	MiniShareRemoteDir = "/data/local/tmp/sharenet-root"
	MiniShareDnsPort   = 1053
)

// GenerateUUIDv4 returns a random RFC 4122 v4 UUID string.
func GenerateUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func isSafePrivateDnsIP(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s)).To4()
	if ip == nil {
		return false
	}
	return ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

var miniShareForbiddenCIDRs = []string{
	"0.0.0.0/8",
	"127.0.0.0/8",
	"100.64.0.0/10",
	"169.254.0.0/16",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::/128",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
}

// BuildMiniShareServerJSON builds the Windows sing-box VLESS server config (bound strictly to 127.0.0.1)
// and rejects loopback, link-local, and RFC1918 LAN targets to prevent SSRF from hotspot clients.
// If an external Windows system HTTP proxy is enabled, traffic is forwarded through it.
func BuildMiniShareServerJSON(uuid string, listenPort int, allowedDns ...string) string {
	dnsArg := ""
	if len(allowedDns) > 0 {
		dnsArg = allowedDns[0]
	}
	pHost, pPort, ok := netutil.DetectWindowsSystemHttpProxy()
	if !ok {
		pHost, pPort = "", 0
	}
	return buildMiniShareServerJSONWithProxy(uuid, listenPort, dnsArg, pHost, pPort)
}

func buildMiniShareServerJSONWithProxy(uuid string, listenPort int, allowedDns, proxyHost string, proxyPort int) string {
	routeRules := []map[string]interface{}{
		{"action": "resolve"},
	}
	if strings.TrimSpace(allowedDns) != "" {
		dnsIP := PrimaryDnsIP(allowedDns)
		if isSafePrivateDnsIP(dnsIP) {
			routeRules = append(routeRules, map[string]interface{}{
				"ip_cidr":  []string{dnsIP + "/32"},
				"network":  []string{"tcp", "udp"},
				"port":     []int{53},
				"outbound": "direct",
			})
		}
	}
	routeRules = append(routeRules,
		map[string]interface{}{
			"ip_cidr": miniShareForbiddenCIDRs,
			"action":  "reject",
		},
		map[string]interface{}{
			"ip_is_private": true,
			"action":        "reject",
		},
	)

	outbounds := []map[string]interface{}{
		{
			"type": "direct",
			"tag":  "direct",
		},
	}
	finalOutbound := "direct"
	if proxyHost != "" && proxyPort > 0 {
		outbounds = append(outbounds, map[string]interface{}{
			"type":        "http",
			"tag":         "upstream-proxy",
			"server":      proxyHost,
			"server_port": proxyPort,
		})
		finalOutbound = "upstream-proxy"
	}

	cfg := map[string]interface{}{
		"log": map[string]interface{}{
			"level":     "warn",
			"timestamp": true,
		},
		"dns": map[string]interface{}{
			"servers": []map[string]interface{}{
				{
					"type": "local",
					"tag":  "dns-guard",
				},
			},
		},
		"inbounds": []map[string]interface{}{
			{
				"type":        "vless",
				"tag":         "vless-in",
				"listen":      "127.0.0.1",
				"listen_port": listenPort,
				"users": []map[string]interface{}{
					{"uuid": uuid},
				},
			},
		},
		"outbounds": outbounds,
		"route": map[string]interface{}{
			"default_domain_resolver": "dns-guard",
			"rules":                   routeRules,
			"final":                   finalOutbound,
		},
	}
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	return string(raw) + "\n"
}

// BuildMiniShareClientJSON builds the Android sing-box client config (tun0 + DNS hijack over VLESS xudp).
func BuildMiniShareClientJSON(uuid string, serverPort, dnsPort int, dnsServer string) string {
	dnsIP := PrimaryDnsIP(dnsServer)
	var dnsEntry map[string]interface{}
	switch {
	case dnsIP == "1.1.1.1" || dnsIP == "1.0.0.1" || dnsIP == "8.8.8.8" || dnsIP == "8.8.4.4":
		dnsEntry = map[string]interface{}{
			"type":        "https",
			"tag":         "remote-dns",
			"server":      dnsIP,
			"server_port": 443,
			"path":        "/dns-query",
			"detour":      "proxy",
		}
	case isSafePrivateDnsIP(dnsIP):
		dnsEntry = map[string]interface{}{
			"type":   "udp",
			"tag":    "remote-dns",
			"server": dnsIP,
			"detour": "proxy",
		}
	default:
		dnsEntry = map[string]interface{}{
			"type":   "tcp",
			"tag":    "remote-dns",
			"server": dnsIP,
			"detour": "proxy",
		}
	}

	cfg := map[string]interface{}{
		"log": map[string]interface{}{
			"level":     "warn",
			"timestamp": true,
		},
		"dns": map[string]interface{}{
			"servers": []map[string]interface{}{dnsEntry},
			"final":   "remote-dns",
		},
		"inbounds": []map[string]interface{}{
			{
				"type":           "tun",
				"tag":            "tun0-in",
				"interface_name": "tun0",
				"stack":          "gvisor",
				"address":        []string{"172.19.0.1/30"},
				"mtu":            1500,
				"auto_route":     false,
			},
			{
				"type":        "direct",
				"tag":         "dns-in",
				"listen":      "0.0.0.0",
				"listen_port": dnsPort,
			},
		},
		"outbounds": []map[string]interface{}{
			{
				"type":            "vless",
				"tag":             "proxy",
				"server":          "127.0.0.1",
				"server_port":     serverPort,
				"uuid":            uuid,
				"packet_encoding": "xudp",
			},
			{
				"type": "direct",
				"tag":  "direct",
			},
		},
		"route": map[string]interface{}{
			"rules": []map[string]interface{}{
				{"action": "sniff"},
				{"inbound": "dns-in", "action": "hijack-dns"},
				{"protocol": "dns", "action": "hijack-dns"},
				{"ip_cidr": miniShareForbiddenCIDRs, "action": "reject"},
				{"ip_is_private": true, "action": "reject"},
			},
			"final": "proxy",
		},
	}
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	return string(raw) + "\n"
}

const miniShareScript = `#!/system/bin/sh
BASE_DIR="/data/local/tmp/sharenet-root"
SINGBOX="$BASE_DIR/sing-box"
CONFIG="$BASE_DIR/client.json"
PIDFILE="$BASE_DIR/singbox.pid"
LOGFILE="$BASE_DIR/singbox.log"
STATEFILE="$BASE_DIR/hotspot.iface"
TUN_IF="tun0"
ROUTE_TABLE="100"
RULE_PREF_HOTSPOT="9000"
DNS_PORT="1053"

is_valid_iface() {
  case "$1" in
    ""|*[!a-zA-Z0-9_.-]*) return 1 ;;
  esac
  [ "${#1}" -le 32 ]
}

detect_hotspot_iface() {
  for iface in ap0 wlan1 wlan2 swlan0 softap0 rndis0 ncm0 usb0; do
    if ip link show "$iface" >/dev/null 2>&1; then
      if ip -4 addr show dev "$iface" 2>/dev/null | grep -q "inet "; then
        echo "$iface"
        return 0
      fi
    fi
  done
  return 1
}

ipt_fwd_est_add() {
  iptables -w -I FORWARD 1 -i "$1" -o "$2" -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || \
  iptables -w -I FORWARD 1 -i "$1" -o "$2" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null
}

ipt_fwd_est_del() {
  iptables -w -D FORWARD -i "$1" -o "$2" -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null
  iptables -w -D FORWARD -i "$1" -o "$2" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null
}

stop_rules_for_iface() {
  _IF="$1"
  is_valid_iface "$_IF" || return
  iptables -w -t nat -D PREROUTING -i "$_IF" -p udp --dport 53 -j REDIRECT --to-ports "$DNS_PORT" 2>/dev/null
  iptables -w -t nat -D PREROUTING -i "$_IF" -p tcp --dport 53 -j REDIRECT --to-ports "$DNS_PORT" 2>/dev/null
  iptables -w -D INPUT -i "$_IF" -p udp --dport "$DNS_PORT" -j ACCEPT 2>/dev/null
  iptables -w -D INPUT -i "$_IF" -p tcp --dport "$DNS_PORT" -j ACCEPT 2>/dev/null
  iptables -w -D INPUT ! -i "$_IF" -p udp --dport "$DNS_PORT" -j DROP 2>/dev/null
  iptables -w -D INPUT ! -i "$_IF" -p tcp --dport "$DNS_PORT" -j DROP 2>/dev/null
  iptables -w -D FORWARD -i "$_IF" -o "$TUN_IF" -j ACCEPT 2>/dev/null
  ipt_fwd_est_del "$TUN_IF" "$_IF"
}

stop_all() {
  if [ -f "$STATEFILE" ]; then
    OLD_IF=$(cat "$STATEFILE" 2>/dev/null)
    stop_rules_for_iface "$OLD_IF"
  fi
  for _f in ap0 wlan1 wlan2 swlan0 softap0 rndis0 ncm0 usb0; do
    stop_rules_for_iface "$_f"
  done
  while ip rule del pref "$RULE_PREF_HOTSPOT" 2>/dev/null; do :; done
  iptables -w -t nat -D POSTROUTING -o "$TUN_IF" -j MASQUERADE 2>/dev/null
  ip route flush table "$ROUTE_TABLE" 2>/dev/null
  if [ -f "$PIDFILE" ]; then
    _PID=$(cat "$PIDFILE" 2>/dev/null)
    case "$_PID" in
      ""|*[!0-9]*) _PID="" ;;
    esac
    if [ -n "$_PID" ] && tr '\0' ' ' < "/proc/$_PID/cmdline" 2>/dev/null | grep -Fq "$SINGBOX"; then
      kill "$_PID" 2>/dev/null
      sleep 0.2 2>/dev/null || true
      kill -9 "$_PID" 2>/dev/null
    fi
    rm -f "$PIDFILE"
  fi
  pkill -f "$SINGBOX" 2>/dev/null || true
  rm -f "$STATEFILE"
}

cleanup_sensitive() {
  rm -f "$CONFIG" "$LOGFILE" "$PIDFILE" "$STATEFILE"
}

case "$1" in
  start)
    if [ -n "$2" ]; then
      if ! is_valid_iface "$2"; then
        echo "ERR:BAD_IFACE"
        exit 3
      fi
      HOTSPOT_IF="$2"
    else
      HOTSPOT_IF=$(detect_hotspot_iface)
    fi
    if [ -z "$HOTSPOT_IF" ]; then
      echo "ERR:NO_AP"
      exit 3
    fi
    stop_all
    if [ ! -x "$SINGBOX" ]; then
      echo "ERR:NO_SINGBOX"
      cleanup_sensitive
      exit 4
    fi
    if [ ! -f "$CONFIG" ]; then
      echo "ERR:NO_CONFIG"
      cleanup_sensitive
      exit 5
    fi
    chmod 700 "$BASE_DIR" "$SINGBOX" "$BASE_DIR/share.sh" 2>/dev/null || true
    chmod 600 "$CONFIG" 2>/dev/null || true
    echo "$HOTSPOT_IF" > "$STATEFILE"
    chmod 600 "$STATEFILE" 2>/dev/null || true
    nohup "$SINGBOX" run -c "$CONFIG" >"$LOGFILE" 2>&1 </dev/null &
    SB_PID=$!
    echo "$SB_PID" > "$PIDFILE"
    chmod 600 "$PIDFILE" "$LOGFILE" 2>/dev/null || true

    WAIT_COUNT=0
    TUN_READY=0
    while [ "$WAIT_COUNT" -lt 24 ]; do
      if ! kill -0 "$SB_PID" 2>/dev/null; then
        LAST_ERR=$(tail -n 5 "$LOGFILE" 2>/dev/null | tr '\n' ' ')
        echo "ERR:SINGBOX_EXITED:$LAST_ERR"
        stop_all
        cleanup_sensitive
        exit 6
      fi
      if ip -o link show up 2>/dev/null | grep -q " $TUN_IF[:@]"; then
        TUN_READY=1
        break
      fi
      WAIT_COUNT=$((WAIT_COUNT + 1))
      sleep 0.4 2>/dev/null || sleep 1
    done
    if [ "$TUN_READY" -ne 1 ]; then
      echo "ERR:NO_TUN"
      stop_all
      cleanup_sensitive
      exit 2
    fi

    sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1
    sysctl -w net.ipv4.conf.all.rp_filter=0 >/dev/null 2>&1
    sysctl -w "net.ipv4.conf.$HOTSPOT_IF.rp_filter=0" >/dev/null 2>&1
    sysctl -w "net.ipv4.conf.$TUN_IF.rp_filter=0" >/dev/null 2>&1

    ip route flush table "$ROUTE_TABLE" 2>/dev/null
    ip route replace default dev "$TUN_IF" table "$ROUTE_TABLE" || { echo "ERR:ROUTE_TUN_FAIL"; stop_all; cleanup_sensitive; exit 7; }
    HOTSPOT_SUBNET=$(ip -4 route show dev "$HOTSPOT_IF" proto kernel 2>/dev/null | awk 'NR==1{print $1}')
    if [ -n "$HOTSPOT_SUBNET" ]; then
      ip route replace "$HOTSPOT_SUBNET" dev "$HOTSPOT_IF" table "$ROUTE_TABLE"
    fi

    while ip rule del pref "$RULE_PREF_HOTSPOT" 2>/dev/null; do :; done
    ip rule add pref "$RULE_PREF_HOTSPOT" iif "$HOTSPOT_IF" lookup "$ROUTE_TABLE" || { echo "ERR:RULE_FAIL"; stop_all; cleanup_sensitive; exit 8; }

    iptables -w -I FORWARD 1 -i "$HOTSPOT_IF" -o "$TUN_IF" -j ACCEPT
    ipt_fwd_est_add "$TUN_IF" "$HOTSPOT_IF"
    iptables -w -t nat -I POSTROUTING 1 -o "$TUN_IF" -j MASQUERADE
    iptables -w -I INPUT 1 ! -i "$HOTSPOT_IF" -p udp --dport "$DNS_PORT" -j DROP 2>/dev/null || true
    iptables -w -I INPUT 1 ! -i "$HOTSPOT_IF" -p tcp --dport "$DNS_PORT" -j DROP 2>/dev/null || true
    iptables -w -I INPUT 1 -i "$HOTSPOT_IF" -p udp --dport "$DNS_PORT" -j ACCEPT
    iptables -w -I INPUT 1 -i "$HOTSPOT_IF" -p tcp --dport "$DNS_PORT" -j ACCEPT
    iptables -w -t nat -I PREROUTING 1 -i "$HOTSPOT_IF" -p udp --dport 53 -j REDIRECT --to-ports "$DNS_PORT"
    iptables -w -t nat -I PREROUTING 1 -i "$HOTSPOT_IF" -p tcp --dport 53 -j REDIRECT --to-ports "$DNS_PORT"
    echo "OK:$TUN_IF:$HOTSPOT_IF"
    ;;
  stop)
    stop_all
    cleanup_sensitive
    echo "STOPPED"
    ;;
  status)
    if [ -f "$PIDFILE" ]; then
      _PID=$(cat "$PIDFILE" 2>/dev/null)
      if [ -n "$_PID" ] && kill -0 "$_PID" 2>/dev/null; then
        if ip route show table "$ROUTE_TABLE" 2>/dev/null | grep -q "^default dev $TUN_IF"; then
          echo "HEALTHY"
          exit 0
        fi
      fi
    fi
    echo "DOWN"
    exit 1
    ;;
esac
`

func writeTempLF(prefix, content string) (string, error) {
	f, err := os.CreateTemp("", prefix)
	if err != nil {
		return "", err
	}
	p := f.Name()
	clean := strings.ReplaceAll(content, "\r\n", "\n")
	_, wErr := f.WriteString(clean)
	cErr := f.Close()
	if wErr != nil {
		_ = os.Remove(p)
		return "", wErr
	}
	if cErr != nil {
		_ = os.Remove(p)
		return "", cErr
	}
	return p, nil
}

func fileSHA256Hex(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func parseRemoteSha256(out string) string {
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && len(fields[0]) == 64 {
			if _, err := hex.DecodeString(fields[0]); err == nil {
				return strings.ToLower(fields[0])
			}
		}
	}
	return ""
}

// StartMiniShare deploys sing-box + client.json + share.sh to /data/local/tmp/sharenet-root and starts the root router.
func (h *Helper) StartMiniShare(serial, uuid, hotspotIface, dnsCsv string, port int) (RootHotspotState, error) {
	iface := strings.TrimSpace(hotspotIface)
	if iface != "" && !IsValidIfaceName(iface) {
		return RootHotspotState{}, fmt.Errorf("недопустимое имя интерфейса точки доступа: %q", iface)
	}
	if !fileExists(h.SingBoxAndroidPath) {
		return RootHotspotState{}, fmt.Errorf("не найден Android sing-box: %s", h.SingBoxAndroidPath)
	}
	expectedHash, err := fileSHA256Hex(h.SingBoxAndroidPath)
	if err != nil {
		return RootHotspotState{}, err
	}

	_ = h.RunAdb(5*time.Second, serialArgs(serial, "shell", "mkdir", "-p", MiniShareRemoteDir)...)
	_ = h.RunAdb(5*time.Second, serialArgs(serial, "shell", "chmod", "700", MiniShareRemoteDir)...)

	hashRes := h.RunAdb(6*time.Second, serialArgs(serial, "shell",
		fmt.Sprintf("sha256sum %s/sing-box 2>/dev/null || true", MiniShareRemoteDir))...)
	if parseRemoteSha256(hashRes.Output) != expectedHash {
		pushRes := h.RunAdb(60*time.Second, serialArgs(serial, "push", h.SingBoxAndroidPath, MiniShareRemoteDir+"/sing-box")...)
		if pushRes.ExitCode != 0 {
			return RootHotspotState{}, fmt.Errorf("ошибка отправки sing-box на телефон: %s", pushRes.Output)
		}
		verifyRes := h.RunAdb(6*time.Second, serialArgs(serial, "shell",
			fmt.Sprintf("sha256sum %s/sing-box 2>/dev/null || true", MiniShareRemoteDir))...)
		if remoteHash := parseRemoteSha256(verifyRes.Output); remoteHash != "" && remoteHash != expectedHash {
			return RootHotspotState{}, fmt.Errorf("ошибка проверки контрольной суммы sing-box на телефоне")
		}
	}

	dnsIP := PrimaryDnsIP(dnsCsv)
	clientPath, err := writeTempLF("sharenet_client_*.json", BuildMiniShareClientJSON(uuid, port, MiniShareDnsPort, dnsIP))
	if err != nil {
		return RootHotspotState{}, err
	}
	defer os.Remove(clientPath)
	if res := h.RunAdb(10*time.Second, serialArgs(serial, "push", clientPath, MiniShareRemoteDir+"/client.json")...); res.ExitCode != 0 {
		return RootHotspotState{}, fmt.Errorf("ошибка отправки client.json: %s", res.Output)
	}

	scriptPath, err := writeTempLF("sharenet_share_*.sh", miniShareScript)
	if err != nil {
		return RootHotspotState{}, err
	}
	defer os.Remove(scriptPath)
	if res := h.RunAdb(10*time.Second, serialArgs(serial, "push", scriptPath, MiniShareRemoteDir+"/share.sh")...); res.ExitCode != 0 {
		return RootHotspotState{}, fmt.Errorf("ошибка отправки share.sh: %s", res.Output)
	}

	_ = h.RunAdb(5*time.Second, serialArgs(serial, "shell", "chmod", "700",
		MiniShareRemoteDir+"/sing-box", MiniShareRemoteDir+"/share.sh")...)
	_ = h.RunAdb(5*time.Second, serialArgs(serial, "shell", "chmod", "600",
		MiniShareRemoteDir+"/client.json")...)

	startCmd := fmt.Sprintf("sh %s/share.sh start", MiniShareRemoteDir)
	if iface != "" {
		startCmd = fmt.Sprintf("sh %s/share.sh start %s", MiniShareRemoteDir, iface)
	}
	res := h.RunAdb(20*time.Second, serialArgs(serial, "shell", "su", "-c", startCmd)...)
	for _, line := range strings.Split(strings.ReplaceAll(res.Output, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "OK:") {
			parts := strings.Split(strings.TrimPrefix(line, "OK:"), ":")
			if len(parts) == 2 {
				return RootHotspotState{
					Applied:   true,
					TunIface:  parts[0],
					ApIface:   parts[1],
					DnsServer: dnsIP,
				}, nil
			}
		}
		if strings.HasPrefix(line, "ERR:") {
			return RootHotspotState{}, fmt.Errorf("%s", strings.TrimPrefix(line, "ERR:"))
		}
	}
	return RootHotspotState{}, fmt.Errorf("ошибка запуска MiniShare (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Output))
}

// IsMiniShareHealthy returns true if Android sing-box is alive and table 100 has default dev tun0.
func (h *Helper) IsMiniShareHealthy(serial string) bool {
	if serial == "" {
		return false
	}
	res := h.RunAdb(4*time.Second, serialArgs(serial, "shell", "su", "-c",
		fmt.Sprintf("sh %s/share.sh status", MiniShareRemoteDir))...)
	return res.ExitCode == 0 && strings.Contains(res.Output, "HEALTHY")
}

// StopMiniShare stops sing-box on the phone and cleans up all routing/iptables rules.
func (h *Helper) StopMiniShare(serial string) {
	if serial == "" {
		return
	}
	_ = h.RunAdb(8*time.Second, serialArgs(serial, "shell", "su", "-c",
		fmt.Sprintf("sh %s/share.sh stop", MiniShareRemoteDir))...)
}
