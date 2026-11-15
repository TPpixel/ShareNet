package hotspotvpn

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"com.tppixel/sharenetusb/internal/netutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const ProxyPort = 8080

var (
	modWininet             = windows.NewLazySystemDLL("wininet.dll")
	procInternetSetOptionW = modWininet.NewProc("InternetSetOptionW")
)

const (
	internetOptionRefresh        = 37
	internetOptionSettingsChange = 39
)

type GatewayCandidate struct {
	IP          string `json:"ip"`
	AdapterName string `json:"adapterName"`
	LocalIP     string `json:"localIp"`
	IsWireless  bool   `json:"isWireless"`
}

type ProxyBackup struct {
	Active      bool
	HadEnable   bool
	Enable      uint32
	HadServer   bool
	Server      string
	HadOverride bool
	Override    string
}

type Status struct {
	Gateway      string             `json:"gateway"`
	ManualGW     string             `json:"manualGateway"`
	AdapterName  string             `json:"adapterName"`
	LocalIP      string             `json:"localIp"`
	ProxyPort    int                `json:"proxyPort"`
	Reachable    bool               `json:"reachable"`
	Candidates   []GatewayCandidate `json:"candidates"`
	Mode         string             `json:"mode"` // "tun" or "proxy"
	IsConnected  bool               `json:"isConnected"`
	IsBusy       bool               `json:"isBusy"`
	HasSingBox   bool               `json:"hasSingBox"`
	LatencyMs    int64              `json:"latencyMs"`
	CheckStatus  string             `json:"checkStatus"`
}

type Manager struct {
	mu          sync.Mutex
	appRoot     string
	singBoxPath string
	tunConfig   string
	tunLogPath  string
	gateway     string
	manualGW    string
	adapterName string
	localIP     string
	reachable   bool
	candidates  []GatewayCandidate
	mode        string // "tun" or "proxy"
	connected   bool
	connectedAs string
	busy        bool
	latencyMs   int64
	checkStatus string
	proxyBackup ProxyBackup
	logOffset   int64
	logFn       func(string)
}

func NewManager(logFn func(string)) *Manager {
	return NewManagerWithRoot("", logFn)
}

func resolveTunStateDir() string {
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		dir := filepath.Join(localAppData, "ShareNet")
		if err := os.MkdirAll(dir, 0o700); err == nil {
			return dir
		}
	}
	return os.TempDir()
}

func NewManagerWithRoot(customRoot string, logFn func(string)) *Manager {
	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)
	wd, _ := os.Getwd()
	root := strings.TrimSpace(customRoot)
	if root == "" {
		root = exeDir
	}

	sbPath := findSingBox(root, exeDir, wd)
	stateDir := resolveTunStateDir()
	// Default to full TUN when sing-box is available; fall back to proxy otherwise.
	mode := "tun"
	if !fileExists(sbPath) {
		mode = "proxy"
	}

	return &Manager{
		appRoot:     root,
		singBoxPath: sbPath,
		tunConfig:   filepath.Join(stateDir, "sharenet-singbox.json"),
		tunLogPath:  filepath.Join(stateDir, "sharenet-singbox.log"),
		mode:        mode,
		logFn:       logFn,
	}
}

func findSingBox(customRoot, exeDir, workDir string) string {
	var candidates []string
	if customRoot != "" {
		candidates = append(candidates, filepath.Join(customRoot, "tools", "sing-box", "sing-box.exe"))
	}
	candidates = append(candidates,
		filepath.Join(exeDir, "tools", "sing-box", "sing-box.exe"),
		filepath.Join(exeDir, "sing-box.exe"),
		filepath.Join(workDir, "tools", "sing-box", "sing-box.exe"),
		filepath.Join(workDir, "sing-box.exe"),
		filepath.Join(filepath.Dir(workDir), "HotspotVPN-Win", "sing-box.exe"),
		filepath.Join(filepath.Dir(exeDir), "HotspotVPN-Win", "sing-box.exe"),
	)
	for _, p := range candidates {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func (m *Manager) emitLog(format string, args ...any) {
	if m.logFn != nil {
		m.logFn(fmt.Sprintf(format, args...))
	}
}

func (m *Manager) Snapshot() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	cands := make([]GatewayCandidate, len(m.candidates))
	copy(cands, m.candidates)
	return Status{
		Gateway:     m.gateway,
		ManualGW:    m.manualGW,
		AdapterName: m.adapterName,
		LocalIP:     m.localIP,
		ProxyPort:   ProxyPort,
		Reachable:   m.reachable,
		Candidates:  cands,
		Mode:        m.mode,
		IsConnected: m.connected,
		IsBusy:      m.busy,
		HasSingBox:  fileExists(m.singBoxPath),
		LatencyMs:   m.latencyMs,
		CheckStatus: m.checkStatus,
	}
}

func (m *Manager) SetMode(mode string) {
	if mode != "tun" && mode != "proxy" {
		return
	}
	m.mu.Lock()
	if !m.connected {
		if mode == "tun" && !fileExists(m.singBoxPath) {
			m.mode = "proxy"
		} else {
			m.mode = mode
		}
	}
	m.mu.Unlock()
}

func (m *Manager) SetManualGateway(gw string) {
	trimmed := strings.TrimSpace(gw)
	m.mu.Lock()
	if trimmed == "" {
		m.manualGW = ""
	} else if net.ParseIP(trimmed) != nil {
		m.manualGW = trimmed
		m.gateway = trimmed
	}
	m.mu.Unlock()
}

func (m *Manager) Refresh() {
	cands := DiscoverGatewayCandidates()

	m.mu.Lock()
	gw := m.manualGW
	adapter := "Вручную"
	localIP := ""
	if gw == "" && len(cands) > 0 {
		gw = cands[0].IP
		adapter = cands[0].AdapterName
		localIP = cands[0].LocalIP
	} else if gw != "" {
		for _, c := range cands {
			if c.IP == gw {
				adapter = c.AdapterName
				localIP = c.LocalIP
				break
			}
		}
	}
	m.candidates = cands
	m.gateway = gw
	m.adapterName = adapter
	m.localIP = localIP
	m.mu.Unlock()

	reach := false
	if gw != "" {
		reach = ProbePortTCP(gw, ProxyPort, 850*time.Millisecond)
	}
	tunAlive := m.IsSingBoxRunning()

	m.mu.Lock()
	m.reachable = reach
	if m.connectedAs == "tun" && !tunAlive && !m.busy {
		m.connected = false
		m.connectedAs = ""
	}
	m.mu.Unlock()
}

func formatSingBoxLog(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}
	low := strings.ToLower(line)
	if strings.Contains(low, "inbound connection") || strings.Contains(low, "outbound connection") || strings.Contains(low, "rejected") {
		return "", false
	}
	if strings.Contains(low, "sing-box started") {
		return "sing-box: туннель запущен.", true
	}
	isImportant := strings.Contains(low, "error") || strings.Contains(low, "fatal") || strings.Contains(low, "warn")
	if !isImportant {
		return "", false
	}
	for _, lvl := range []string{"FATAL", "ERROR", "WARN"} {
		if idx := strings.Index(line, lvl); idx >= 0 {
			rest := strings.TrimSpace(line[idx:])
			if bracket := strings.Index(rest, "] "); bracket >= 0 {
				rest = strings.TrimSpace(rest[bracket+2:])
			} else {
				rest = strings.TrimSpace(strings.TrimPrefix(rest, lvl))
			}
			if rest != "" {
				return "sing-box: " + rest, true
			}
		}
	}
	return "sing-box: " + line, true
}

func (m *Manager) TailTunLog() {
	m.mu.Lock()
	active := m.connectedAs == "tun" || m.busy
	logPath := m.tunLogPath
	offset := m.logOffset
	m.mu.Unlock()

	if !active {
		return
	}
	f, err := os.Open(logPath)
	if err != nil {
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil || st.Size() <= offset {
		return
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, 32*1024))
	if err != nil || len(data) == 0 {
		return
	}

	m.mu.Lock()
	m.logOffset += int64(len(data))
	m.mu.Unlock()

	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if formatted, ok := formatSingBoxLog(line); ok {
			m.emitLog("%s", formatted)
		}
	}
}

func (m *Manager) Start(gw string, mode string) error {
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return errors.New("операция приёма уже выполняется")
	}
	if gw != "" {
		if net.ParseIP(gw) == nil {
			m.mu.Unlock()
			return errors.New("введите корректный IPv4-адрес телефона")
		}
		m.manualGW = gw
		m.gateway = gw
	}
	targetGW := m.gateway
	if mode == "tun" || mode == "proxy" {
		m.mode = mode
	}
	if m.mode == "tun" && !fileExists(m.singBoxPath) {
		m.mode = "proxy"
	}
	chosenMode := m.mode
	m.busy = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.busy = false
		m.mu.Unlock()
	}()

	if targetGW == "" {
		m.emitLog("Шлюз телефона не найден. Подключите ПК к Wi-Fi точке доступа телефона.")
		return errors.New("шлюз телефона не найден")
	}

	m.emitLog("Приём: проверка %s:%d...", targetGW, ProxyPort)
	if !ProbePortTCP(targetGW, ProxyPort, 1800*time.Millisecond) {
		m.emitLog("Порт %d на %s не отвечает. Убедитесь, что на телефоне включена раздача ShareNet.", ProxyPort, targetGW)
		return fmt.Errorf("нет ответа от %s:%d", targetGW, ProxyPort)
	}

	if chosenMode == "tun" {
		return m.startTunVPN(targetGW)
	}
	return m.startWindowsProxy(targetGW)
}

func (m *Manager) Stop() {
	m.mu.Lock()
	was := m.connectedAs
	hadBackup := m.proxyBackup.Active
	m.busy = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.connected = false
		m.connectedAs = ""
		m.busy = false
		m.latencyMs = 0
		m.checkStatus = ""
		m.mu.Unlock()
	}()

	if was == "tun" || m.IsSingBoxRunning() {
		m.emitLog("Останавливаю TUN-туннель sing-box...")
		if err := m.stopTunProcess(); err != nil {
			m.emitLog("Не удалось остановить sing-box: %v", err)
		} else {
			m.emitLog("VPN-туннель (Приём) остановлен.")
		}
	}
	if hadBackup || was == "proxy" {
		m.restoreWindowsProxy()
		m.emitLog("Системный прокси Windows восстановлен.")
	}
	_ = os.Remove(m.tunConfig)
}

func (m *Manager) CheckConnectivity() {
	start := time.Now()
	client := &http.Client{
		Timeout: 6 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}

	m.mu.Lock()
	connectedAs := m.connectedAs
	gw := m.gateway
	m.mu.Unlock()

	if (connectedAs == "proxy" || (connectedAs == "" && gw != "")) && gw != "" {
		if u, err := url.Parse(fmt.Sprintf("socks5://%s:%d", gw, ProxyPort)); err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
		}
	}

	resp, err := client.Get("https://www.google.com/generate_204")
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		m.mu.Lock()
		m.latencyMs = 0
		m.checkStatus = "Нет ответа"
		m.mu.Unlock()
		m.emitLog("Проверка приёма: нет ответа (%v)", err)
		return
	}
	_ = resp.Body.Close()

	m.mu.Lock()
	m.latencyMs = elapsed
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		m.checkStatus = fmt.Sprintf("HTTP %d · %d мс", resp.StatusCode, elapsed)
	} else {
		m.checkStatus = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	m.mu.Unlock()
	m.emitLog("Проверка сети через телефон: HTTP %d (%d мс)", resp.StatusCode, elapsed)
}

func (m *Manager) startTunVPN(gw string) error {
	if !fileExists(m.singBoxPath) {
		return errors.New("не найден sing-box.exe")
	}

	_ = m.stopTunProcess()
	_ = os.Remove(m.tunLogPath)
	m.mu.Lock()
	m.logOffset = 0
	m.mu.Unlock()

	cfgBytes, err := BuildSingBoxConfig(gw, ProxyPort, m.tunLogPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(m.tunConfig, cfgBytes, 0o600); err != nil {
		return err
	}

	m.emitLog("Запуск системного VPN (TUN) на %s:%d (подтвердите UAC)...", gw, ProxyPort)
	args := fmt.Sprintf(`run -c "%s"`, m.tunConfig)
	if err := runElevated(m.singBoxPath, args, filepath.Dir(m.singBoxPath)); err != nil {
		m.emitLog("Запуск TUN с правами администратора отменён: %v", err)
		return err
	}

	started := false
	for i := 0; i < 12; i++ {
		time.Sleep(350 * time.Millisecond)
		m.TailTunLog()
		if m.IsSingBoxRunning() {
			started = true
			break
		}
	}
	if !started {
		m.emitLog("sing-box.exe не запустился. Проверьте журнал.")
		return errors.New("tun не поднялся")
	}

	m.mu.Lock()
	m.connected = true
	m.connectedAs = "tun"
	m.reachable = true
	m.mu.Unlock()
	m.emitLog("Приём через полный VPN (TUN + DoH 1.1.1.1) активен.")
	go m.CheckConnectivity()
	return nil
}

func (m *Manager) startWindowsProxy(gw string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	m.mu.Lock()
	if !m.proxyBackup.Active {
		var bk ProxyBackup
		bk.Active = true
		if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
			bk.HadEnable = true
			bk.Enable = uint32(v)
		}
		if s, _, err := k.GetStringValue("ProxyServer"); err == nil {
			bk.HadServer = true
			bk.Server = s
		}
		if o, _, err := k.GetStringValue("ProxyOverride"); err == nil {
			bk.HadOverride = true
			bk.Override = o
		}
		m.proxyBackup = bk
	}
	m.mu.Unlock()

	proxyStr := fmt.Sprintf("%s:%d", gw, ProxyPort)
	overrideStr := "localhost;127.*;10.*;172.16.*;192.168.*;<local>"
	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}
	if err := k.SetStringValue("ProxyServer", proxyStr); err != nil {
		return err
	}
	_ = k.SetStringValue("ProxyOverride", overrideStr)
	notifyWinINetSettings()

	m.mu.Lock()
	m.connected = true
	m.connectedAs = "proxy"
	m.reachable = true
	m.mu.Unlock()
	m.emitLog("Системный прокси Windows включён: %s", proxyStr)
	go m.CheckConnectivity()
	return nil
}

func (m *Manager) restoreWindowsProxy() {
	m.mu.Lock()
	bk := m.proxyBackup
	m.proxyBackup = ProxyBackup{}
	m.mu.Unlock()

	if !bk.Active {
		return
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()

	if bk.HadEnable {
		_ = k.SetDWordValue("ProxyEnable", bk.Enable)
	} else {
		_ = k.SetDWordValue("ProxyEnable", 0)
	}
	if bk.HadServer {
		_ = k.SetStringValue("ProxyServer", bk.Server)
	} else {
		_ = k.DeleteValue("ProxyServer")
	}
	if bk.HadOverride {
		_ = k.SetStringValue("ProxyOverride", bk.Override)
	} else {
		_ = k.DeleteValue("ProxyOverride")
	}
	notifyWinINetSettings()
}

func (m *Manager) stopTunProcess() error {
	if !m.IsSingBoxRunning() {
		return nil
	}
	cmd := exec.Command("taskkill.exe", "/F", "/IM", "sing-box.exe")
	netutil.HideWindow(cmd)
	if err := cmd.Run(); err != nil && m.IsSingBoxRunning() {
		if err := runElevated("taskkill.exe", "/F /IM sing-box.exe", m.appRoot); err != nil {
			return err
		}
		for i := 0; i < 8 && m.IsSingBoxRunning(); i++ {
			time.Sleep(250 * time.Millisecond)
		}
	}
	return nil
}

func (m *Manager) IsSingBoxRunning() bool {
	cmd := exec.Command("tasklist.exe", "/FI", "IMAGENAME eq sing-box.exe", "/NH")
	netutil.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "sing-box.exe")
}

func BuildSingBoxConfig(gateway string, port int, logPath string) ([]byte, error) {
	gw := strings.TrimSpace(gateway)
	if net.ParseIP(gw) == nil {
		return nil, fmt.Errorf("некорректный IP шлюза: %q", gateway)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("некорректный порт: %d", port)
	}

	cfg := map[string]any{
		"log": map[string]any{
			"level":     "warn",
			"timestamp": true,
			"output":    filepath.ToSlash(logPath),
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
			{
				"type":        "socks",
				"tag":         "proxy-out",
				"server":      gw,
				"server_port": port,
				"version":     "5",
				"network":     "tcp",
			},
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

func IsPrivateHotspotIPv4(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	if ip4[0] == 172 && ip4[1] == 19 && ip4[2] == 0 {
		return false
	}
	if ip4[0] == 10 || (ip4[0] == 192 && ip4[1] == 168) {
		return true
	}
	return ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31
}

// IsIgnoredGatewayAdapter excludes virtual VPN and hypervisor adapters that cannot be a phone hotspot gateway.
func IsIgnoredGatewayAdapter(friendly, desc string) bool {
	low := strings.ToLower(friendly + " " + desc)
	for _, kw := range []string{
		"sing-box", "wintun", "tun", "tap-", "tap windows",
		"radmin", "hamachi", "zerotier", "tailscale", "wireguard", "openvpn", "nordlynx", "proton", "warp",
		"vmware", "virtualbox", "vbox", "hyper-v", "vethernet",
		"loopback", "teredo", "isatap",
	} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

func DiscoverGatewayCandidates() []GatewayCandidate {
	var size uint32 = 16384
	var buf []byte
	for i := 0; i < 4; i++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(
			windows.AF_INET,
			windows.GAA_FLAG_INCLUDE_GATEWAYS,
			0,
			(*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])),
			&size,
		)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return nil
		}
	}

	var out []GatewayCandidate
	seen := make(map[string]bool)
	aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
	for ; aa != nil; aa = aa.Next {
		if aa.OperStatus != windows.IfOperStatusUp {
			continue
		}
		if aa.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK || aa.IfType == windows.IF_TYPE_TUNNEL {
			continue
		}
		friendly := windows.UTF16PtrToString(aa.FriendlyName)
		desc := windows.UTF16PtrToString(aa.Description)
		low := strings.ToLower(friendly + " " + desc)
		if IsIgnoredGatewayAdapter(friendly, desc) {
			continue
		}

		localIP := ""
		for ua := aa.FirstUnicastAddress; ua != nil; ua = ua.Next {
			sa, err := ua.Address.Sockaddr.Sockaddr()
			if err != nil {
				continue
			}
			if sa4, ok := sa.(*syscall.SockaddrInet4); ok {
				ip := net.IP(sa4.Addr[:])
				if !ip.IsLoopback() && !ip.IsUnspecified() {
					localIP = ip.String()
					break
				}
			}
		}

		isWifi := aa.IfType == windows.IF_TYPE_IEEE80211 ||
			strings.Contains(low, "wi-fi") ||
			strings.Contains(low, "wireless")

		for gw := aa.FirstGatewayAddress; gw != nil; gw = gw.Next {
			sa, err := gw.Address.Sockaddr.Sockaddr()
			if err != nil {
				continue
			}
			sa4, ok := sa.(*syscall.SockaddrInet4)
			if !ok {
				continue
			}
			ip := net.IP(sa4.Addr[:])
			if ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil && ip4[0] == 172 && ip4[1] == 19 && ip4[2] == 0 {
				continue
			}
			ipStr := ip.String()
			if seen[ipStr] {
				continue
			}
			seen[ipStr] = true
			out = append(out, GatewayCandidate{
				IP:          ipStr,
				AdapterName: friendly,
				LocalIP:     localIP,
				IsWireless:  isWifi,
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		score := func(c GatewayCandidate) int {
			s := 0
			if c.IsWireless {
				s += 4
			}
			if IsPrivateHotspotIPv4(net.ParseIP(c.IP)) {
				s += 2
			}
			return s
		}
		return score(out[i]) > score(out[j])
	})
	return out
}

func ProbePortTCP(ip string, port int, timeout time.Duration) bool {
	if net.ParseIP(ip) == nil {
		return false
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, fmt.Sprintf("%d", port)), timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func runElevated(exePath, args, workDir string) error {
	verbPtr, _ := windows.UTF16PtrFromString("runas")
	filePtr, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return err
	}
	argPtr, err := windows.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	dirPtr, _ := windows.UTF16PtrFromString(workDir)
	return windows.ShellExecute(0, verbPtr, filePtr, argPtr, dirPtr, windows.SW_HIDE)
}

func notifyWinINetSettings() {
	_, _, _ = procInternetSetOptionW.Call(0, internetOptionSettingsChange, 0, 0)
	_, _, _ = procInternetSetOptionW.Call(0, internetOptionRefresh, 0, 0)
}
