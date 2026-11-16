package adb

import (
	"bytes"
	"context"
	"fmt"
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
	TargetHotspotProxyVersionCode = 33
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
	AdbExePath              string
	GnirehtetExePath        string
	GnirehtetApkPath        string
	HotspotProxyApkPath     string
}

var (
	reVersionCode = regexp.MustCompile(`versionCode=(\d+)`)
	reTethered    = regexp.MustCompile(`(?m)^\s*([a-zA-Z0-9_.-]+)\s*-\s*TetheredState`)
	reIfaceHdr    = regexp.MustCompile(`^\d+:\s+([^:@\s]+)`)
	reInet4       = regexp.MustCompile(`\binet\s+(\d+\.\d+\.\d+\.\d+)/\d+`)
)

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

	return &Helper{
		AppRoot:                 appRoot,
		BundledPlatformToolsDir: bundledPt,
		GnirehtetDir:            gnDir,
		AdbExePath:              adbPath,
		GnirehtetExePath:        filepath.Join(gnDir, "gnirehtet.exe"),
		GnirehtetApkPath:        filepath.Join(gnDir, "gnirehtet.apk"),
		HotspotProxyApkPath:     filepath.Join(appRoot, "tools", "hotspot-proxy", "hotspot-proxy.apk"),
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

// CheckRootAccess checks whether ADB shell can obtain uid=0 via su (Magisk / KernelSU / APatch).
func (h *Helper) CheckRootAccess(serial string) bool {
	if serial == "" {
		return false
	}
	res := h.RunAdb(4*time.Second, serialArgs(serial, "shell", "su", "-c", "id")...)
	return strings.Contains(res.Output, "uid=0(")
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

const rootHotspotScript = `#!/system/bin/sh
find_tun() {
  ip -o -4 addr show 2>/dev/null | while read -r _ iface _ cidr _; do
    case "$iface" in
      tun*) echo "$iface"; return 0 ;;
    esac
  done
}
find_ap() {
  ip -o -4 addr show 2>/dev/null | while read -r _ iface _ cidr _; do
    case "$iface" in
      ap*|swlan*|softap*|wlan1*|wlan2*|rndis*|usb*|ncm*)
        echo "$iface $cidr"; return 0 ;;
      wlan0)
        case "$cidr" in
          192.168.43.*|192.168.137.*) echo "$iface $cidr"; return 0 ;;
        esac ;;
    esac
  done
}
ipt_fwd_est_add() {
  iptables -w -I FORWARD 1 -i "$1" -o "$2" -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || \
  iptables -w -I FORWARD 1 -i "$1" -o "$2" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null
}
ipt_fwd_est_del() {
  iptables -w -D FORWARD -i "$1" -o "$2" -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null
  iptables -w -D FORWARD -i "$1" -o "$2" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null
}
case "$1" in
  start)
    DNS="$2"
    [ -z "$DNS" ] && DNS="1.1.1.1"
    TUN=$(find_tun)
    if [ -z "$TUN" ]; then
      echo "ERR:NO_TUN"
      exit 2
    fi
    AP_INFO=$(find_ap)
    if [ -z "$AP_INFO" ]; then
      echo "ERR:NO_AP"
      exit 3
    fi
    AP=$(echo "$AP_INFO" | awk '{print $1}')
    CIDR=$(echo "$AP_INFO" | awk '{print $2}')
    SUBNET=$(ip -o -4 route show dev "$AP" proto kernel 2>/dev/null | while read -r net _; do echo "$net"; break; done)
    [ -z "$SUBNET" ] && SUBNET="$CIDR"
    sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1
    sysctl -w net.ipv4.conf.all.rp_filter=0 >/dev/null 2>&1
    sysctl -w net.ipv4.conf."$TUN".rp_filter=0 >/dev/null 2>&1
    sysctl -w net.ipv4.conf."$AP".rp_filter=0 >/dev/null 2>&1
    while ip rule del pref 100 2>/dev/null; do :; done
    ip route flush table 999 2>/dev/null
    ip route replace "$SUBNET" dev "$AP" table 999
    ip route replace default dev "$TUN" table 999
    ip rule add from "$SUBNET" lookup 999 pref 100
    ip rule add to "$SUBNET" lookup 999 pref 100
    ip rule add iif "$AP" lookup 999 pref 100
    iptables -w -D FORWARD -i "$AP" -o "$TUN" -j ACCEPT 2>/dev/null
    ipt_fwd_est_del "$TUN" "$AP"
    iptables -w -I FORWARD 1 -i "$AP" -o "$TUN" -j ACCEPT
    ipt_fwd_est_add "$TUN" "$AP"
    iptables -w -t nat -D POSTROUTING -o "$TUN" -j MASQUERADE 2>/dev/null
    iptables -w -t nat -I POSTROUTING 1 -o "$TUN" -j MASQUERADE
    iptables -w -t nat -D PREROUTING -i "$AP" -p udp --dport 53 -j DNAT --to-destination "$DNS":53 2>/dev/null
    iptables -w -t nat -D PREROUTING -i "$AP" -p tcp --dport 53 -j DNAT --to-destination "$DNS":53 2>/dev/null
    iptables -w -t nat -I PREROUTING 1 -i "$AP" -p udp --dport 53 -j DNAT --to-destination "$DNS":53
    iptables -w -t nat -I PREROUTING 1 -i "$AP" -p tcp --dport 53 -j DNAT --to-destination "$DNS":53
    iptables -w -t mangle -D FORWARD -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu 2>/dev/null
    iptables -w -t mangle -I FORWARD 1 -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
    echo "OK:$TUN:$AP"
    ;;
  stop)
    TUN="$2"
    AP="$3"
    DNS="$4"
    [ -z "$DNS" ] && DNS="1.1.1.1"
    while ip rule del pref 100 2>/dev/null; do :; done
    ip route flush table 999 2>/dev/null
    if [ -n "$AP" ] && [ -n "$TUN" ]; then
      iptables -w -D FORWARD -i "$AP" -o "$TUN" -j ACCEPT 2>/dev/null
      ipt_fwd_est_del "$TUN" "$AP"
      iptables -w -t nat -D POSTROUTING -o "$TUN" -j MASQUERADE 2>/dev/null
      iptables -w -t nat -D PREROUTING -i "$AP" -p udp --dport 53 -j DNAT --to-destination "$DNS":53 2>/dev/null
      iptables -w -t nat -D PREROUTING -i "$AP" -p tcp --dport 53 -j DNAT --to-destination "$DNS":53 2>/dev/null
    fi
    iptables -w -t mangle -D FORWARD -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu 2>/dev/null
    ;;
esac
`

func (h *Helper) ensureRootScript(serial string) error {
	tmpFile, err := os.CreateTemp("", "sharenet_root_*.sh")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	_, wErr := tmpFile.WriteString(rootHotspotScript)
	cErr := tmpFile.Close()
	if wErr != nil {
		return wErr
	}
	if cErr != nil {
		return cErr
	}
	res := h.RunAdb(10*time.Second, serialArgs(serial, "push", tmpPath, "/data/local/tmp/sharenet_root.sh")...)
	if res.ExitCode != 0 {
		return fmt.Errorf("adb push root script failed: %s", res.Output)
	}
	_ = h.RunAdb(5*time.Second, serialArgs(serial, "shell", "chmod", "755", "/data/local/tmp/sharenet_root.sh")...)
	return nil
}

// StartRootHotspot configures transparent NAT routing from the Android Wi-Fi hotspot to tun0.
func (h *Helper) StartRootHotspot(serial, dnsCsv string) (RootHotspotState, error) {
	if err := h.ensureRootScript(serial); err != nil {
		return RootHotspotState{}, err
	}
	dnsIP := PrimaryDnsIP(dnsCsv)
	res := h.RunAdb(10*time.Second, serialArgs(serial, "shell", "su", "-c", "sh /data/local/tmp/sharenet_root.sh start "+dnsIP)...)
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
	return RootHotspotState{}, fmt.Errorf("root NAT setup failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Output))
}

// StopRootHotspot rolls back transparent NAT rules on the Android device.
func (h *Helper) StopRootHotspot(serial string, st RootHotspotState) {
	if serial == "" || !st.Applied {
		return
	}
	_ = h.RunAdb(8*time.Second, serialArgs(serial, "shell", "su", "-c",
		fmt.Sprintf("sh /data/local/tmp/sharenet_root.sh stop %s %s %s", st.TunIface, st.ApIface, st.DnsServer))...)
	_ = h.RunAdb(4*time.Second, serialArgs(serial, "shell", "rm", "-f", "/data/local/tmp/sharenet_root.sh")...)
}
