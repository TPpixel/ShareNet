package main

import (
	"bufio"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"com.tppixel/sharenetusb/internal/adb"
	"com.tppixel/sharenetusb/internal/embedded"
	"com.tppixel/sharenetusb/internal/hotspotvpn"
	"com.tppixel/sharenetusb/internal/netutil"
	"com.tppixel/sharenetusb/internal/socks5"
	"com.tppixel/sharenetusb/internal/winui"
)

//go:embed internal/ui/index.html
var uiFS embed.FS

const (
	ProxyPort    = 8080
	UsbSocksPort = 10808
)

type LogEntry struct {
	ID   int64  `json:"id"`
	Time string `json:"time"`
	Text string `json:"text"`
}

type AppState struct {
	DeviceState    string             `json:"deviceState"`
	DeviceName     string             `json:"deviceName"`
	ActiveSerial   string             `json:"activeSerial"`
	Hotspot        adb.HotspotStatus  `json:"hotspot"`
	Dns            string             `json:"dns"`
	IsSharing      bool               `json:"isSharing"`
	IsBusy         bool               `json:"isBusy"`
	DarkTheme      bool               `json:"darkTheme"`
	Theme          winui.ThemePalette `json:"theme"`
	AutoStartUsb   bool               `json:"autoStartUsb"`
	DisablePrivDns bool               `json:"disablePrivDns"`
	StartProxy     bool               `json:"startProxy"`
	ProxyPort      int                `json:"proxyPort"`
	UsbSocksPort   int                `json:"usbSocksPort"`
	GitHubURL      string             `json:"githubUrl"`
	Vpn            hotspotvpn.Status  `json:"vpn"`
	Logs           []LogEntry         `json:"logs"`
}

type ShareApp struct {
	mu                sync.Mutex
	adb               *adb.Helper
	socks             *socks5.Server
	vpn               *hotspotvpn.Manager
	gnirehtetCmd      *exec.Cmd
	isSharing         bool
	isBusy            bool
	darkTheme         bool
	activeSerial      string
	autoStartedSerial string
	dnsValue          string
	deviceState       string
	deviceName        string
	hotspot           adb.HotspotStatus
	dnsBackup         adb.PrivateDnsBackup
	autoStartUsb      bool
	disablePrivDns    bool
	startProxy        bool
	logs              []LogEntry
	nextLogID         int64
	wv                *winui.WebViewWindow
	apiToken          string
}

func newShareApp() *ShareApp {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	runtimeRoot, runtimeErr := embedded.EnsureRuntimeRoot()
	a := &ShareApp{
		adb:            adb.NewHelperWithRoot(runtimeRoot),
		dnsValue:       netutil.DetectPrimaryDns(),
		deviceState:    "none",
		deviceName:     "Поиск устройства Android...",
		darkTheme:      winui.IsWindowsDarkTheme(),
		autoStartUsb:   true,
		disablePrivDns: true,
		startProxy:     true,
		apiToken:       hex.EncodeToString(b),
	}
	a.vpn = hotspotvpn.NewManagerWithRoot(runtimeRoot, a.log)
	a.vpn.Refresh()
	if runtimeErr != nil {
		a.logf("Распаковка встроенных компонентов: %v", runtimeErr)
	}
	a.logf("Готово к работе. Авто-DNS ПК: %s", a.dnsValue)
	go a.ensureAdbAutoInstall()
	return a
}

func (a *ShareApp) ensureAdbAutoInstall() {
	installed, msg, err := netutil.EnsureAdbInUserPath(a.adb.BundledPlatformToolsDir)
	if err != nil {
		a.logf("Автопроверка ADB: %v", err)
		return
	}
	if installed && msg != "" {
		a.logf("Автоустановка ADB (без прав админа): %s", msg)
	}
}

const maxLogRows = 80

func sanitizeLogLine(msg string) string {
	msg = strings.ReplaceAll(msg, "\r", " ")
	for {
		esc := strings.IndexByte(msg, '\x1b')
		if esc < 0 {
			break
		}
		end := esc + 1
		for end < len(msg) && ((msg[end] >= '[' && msg[end] <= '[') || (msg[end] >= '0' && msg[end] <= '?') || msg[end] == ';') {
			end++
		}
		if end < len(msg) {
			end++
		}
		msg = msg[:esc] + msg[end:]
	}
	return strings.Join(strings.Fields(strings.TrimSpace(msg)), " ")
}

func formatGnirehtetLog(line string) (string, bool) {
	clean := sanitizeLogLine(line)
	if clean == "" {
		return "", false
	}
	low := strings.ToLower(clean)
	if strings.Contains(low, " debug ") || strings.Contains(low, " trace ") {
		return "", false
	}
	if strings.Contains(low, "relay server started") {
		return "USB-туннель gnirehtet запущен.", true
	}
	if strings.Contains(low, "client #") && strings.Contains(low, "disconnected") {
		return "Телефон отключился от USB-туннеля.", true
	}
	if strings.Contains(low, "client #") && strings.Contains(low, "connected") {
		return "Телефон подключился к USB-туннелю.", true
	}
	if strings.Contains(low, "dropping invalid packet") {
		return "", false
	}
	isErr := strings.Contains(low, " error ") || strings.Contains(low, " warn ") ||
		strings.Contains(low, "cannot ") || strings.Contains(low, "failed") ||
		strings.Contains(low, "panic") || strings.Contains(low, "fatal")
	if !isErr {
		return "", false
	}
	if idx := strings.Index(clean, "::"); idx >= 0 {
		if colon := strings.Index(clean[idx:], ": "); colon >= 0 {
			clean = strings.TrimSpace(clean[idx+colon+2:])
		}
	}
	if clean == "" {
		return "", false
	}
	return "gnirehtet: " + clean, true
}

func (a *ShareApp) logf(format string, args ...interface{}) {
	a.log(fmt.Sprintf(format, args...))
}

func (a *ShareApp) log(msg string) {
	msg = sanitizeLogLine(msg)
	if msg == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := len(a.logs) - 1; i >= 0 && i >= len(a.logs)-4; i-- {
		if a.logs[i].Text == msg {
			return
		}
	}
	a.nextLogID++
	a.logs = append(a.logs, LogEntry{
		ID:   a.nextLogID,
		Time: time.Now().Format("15:04:05"),
		Text: msg,
	})
	if len(a.logs) > maxLogRows {
		a.logs = a.logs[len(a.logs)-maxLogRows:]
	}
}

func (a *ShareApp) snapshot() AppState {
	a.mu.Lock()
	wv := a.wv
	dark := winui.IsWindowsDarkTheme()
	titleBarChanged := dark != a.darkTheme
	if titleBarChanged {
		a.darkTheme = dark
	}
	logsCopy := make([]LogEntry, len(a.logs))
	copy(logsCopy, a.logs)
	st := AppState{
		DeviceState:    a.deviceState,
		DeviceName:     a.deviceName,
		ActiveSerial:   a.activeSerial,
		Hotspot:        a.hotspot,
		Dns:            a.dnsValue,
		IsSharing:      a.isSharing,
		IsBusy:         a.isBusy,
		DarkTheme:      dark,
		AutoStartUsb:   a.autoStartUsb,
		DisablePrivDns: a.disablePrivDns,
		StartProxy:     a.startProxy,
		ProxyPort:      ProxyPort,
		UsbSocksPort:   UsbSocksPort,
		GitHubURL:      "https://github.com/TPpixel",
		Logs:           logsCopy,
	}
	vpnMgr := a.vpn
	a.mu.Unlock()
	st.Theme = winui.GetWindowsThemeWithDark(dark)
	if titleBarChanged && wv != nil {
		wv.Dispatch(func() { wv.UpdateTheme(dark) })
	}
	if vpnMgr != nil {
		st.Vpn = vpnMgr.Snapshot()
	}
	return st
}

func (a *ShareApp) refreshDevices() {
	devices := a.adb.GetDevices()
	var ready *adb.DeviceInfo
	for i := range devices {
		if devices[i].State == "device" {
			ready = &devices[i]
			break
		}
	}

	var hotspot adb.HotspotStatus
	if ready != nil {
		hotspot = a.adb.DetectHotspotStatus(ready.Serial)
	}

	a.mu.Lock()
	a.hotspot = hotspot

	if len(devices) == 0 {
		shouldStop := a.isSharing || a.gnirehtetCmd != nil || a.socks != nil
		a.activeSerial = ""
		a.autoStartedSerial = ""
		a.deviceState = "none"
		a.deviceName = "Телефон не обнаружен. Подключите USB и включите Отладку по USB."
		a.mu.Unlock()
		if shouldStop {
			go a.stopSharingOnUsbDisconnect()
		}
		return
	}

	chosen := devices[0]
	if ready != nil {
		chosen = *ready
	}
	a.activeSerial = chosen.Serial
	display := chosen.Serial
	if chosen.Model != "" {
		display = fmt.Sprintf("%s (%s)", chosen.Model, chosen.Serial)
	}
	a.deviceName = display

	shouldAutoStart := false
	switch chosen.State {
	case "device":
		a.deviceState = "ready"
		if a.autoStartUsb && !a.isSharing && !a.isBusy && a.autoStartedSerial != chosen.Serial {
			a.autoStartedSerial = chosen.Serial
			shouldAutoStart = true
		}
	case "unauthorized", "authorizing":
		a.deviceState = "unauthorized"
		a.deviceName = display + " — подтвердите отладку по USB на экране телефона"
	default:
		a.deviceState = "error"
		a.deviceName = fmt.Sprintf("%s [%s]", display, chosen.State)
	}
	dns := a.dnsValue
	disablePriv := a.disablePrivDns
	startProxy := a.startProxy
	a.mu.Unlock()

	if shouldAutoStart {
		a.logf("Автозапуск для %s...", display)
		go a.startSharing(dns, disablePriv, startProxy)
	}
}

func (a *ShareApp) startSharing(dns string, disablePrivDns, startProxy bool) {
	a.mu.Lock()
	if a.isBusy || a.isSharing {
		a.mu.Unlock()
		return
	}
	if _, err := os.Stat(a.adb.GnirehtetExePath); err != nil {
		a.mu.Unlock()
		a.logf("Ошибка: не найден файл %s", a.adb.GnirehtetExePath)
		return
	}
	dns = strings.TrimSpace(dns)
	if dns == "" {
		dns = netutil.DetectPrimaryDns()
	}
	a.dnsValue = dns
	a.disablePrivDns = disablePrivDns
	a.startProxy = startProxy
	a.isBusy = true
	a.mu.Unlock()

	a.logf("Запуск USB-раздачи (DNS %s, SOCKS5 :%d)...", dns, UsbSocksPort)

	devices := a.adb.GetDevices()
	var ready *adb.DeviceInfo
	for i := range devices {
		if devices[i].State == "device" {
			ready = &devices[i]
			break
		}
	}
	if ready == nil {
		a.mu.Lock()
		a.isBusy = false
		a.mu.Unlock()
		a.log("Нет авторизованного устройства. Подключите телефон и разрешите Отладку по USB.")
		return
	}

	serial := ready.Serial
	a.mu.Lock()
	a.activeSerial = serial
	a.autoStartedSerial = serial
	a.mu.Unlock()

	a.adb.EnsureRequiredApksInstalled(serial, startProxy, false, a.log)

	var dnsBackup adb.PrivateDnsBackup
	if disablePrivDns {
		dnsBackup = a.adb.DisablePrivateDnsTemporarily(serial)
		if dnsBackup.WasModified {
			a.logf("Персональный DNS (%s) временно отключён для работы туннеля.", dnsBackup.Specifier)
		}
	}
	a.mu.Lock()
	a.dnsBackup = dnsBackup
	a.mu.Unlock()

	a.adb.ResetGnirehtetState(serial)

	a.mu.Lock()
	if a.socks == nil {
		a.socks = socks5.New(UsbSocksPort)
		if err := a.socks.Start(); err != nil {
			a.logf("Предупреждение SOCKS5 :%d: %v", UsbSocksPort, err)
		}
	}
	a.mu.Unlock()

	_ = a.adb.RunAdb(3*time.Second, "-s", serial, "reverse", "--remove", fmt.Sprintf("tcp:%d", UsbSocksPort))
	revRes := a.adb.RunAdb(5*time.Second, "-s", serial, "reverse", fmt.Sprintf("tcp:%d", UsbSocksPort), fmt.Sprintf("tcp:%d", UsbSocksPort))
	if revRes.ExitCode != 0 {
		a.log("Предупреждение: adb reverse для SOCKS5 не сработал.")
	}

	dnsCsv := netutil.NormalizeDnsCsv(dns)
	routes := netutil.BuildGnirehtetRoutes(dnsCsv)
	cmd := exec.Command(a.adb.GnirehtetExePath, "run", serial, "-d", dnsCsv, "-r", routes)
	cmd.Dir = a.adb.GnirehtetDir
	cmd.Env = a.adb.EffectiveEnv()
	netutil.HideWindow(cmd)

	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		a.mu.Lock()
		a.isBusy = false
		a.mu.Unlock()
		a.logf("Ошибка запуска gnirehtet: %v", err)
		return
	}

	a.mu.Lock()
	a.gnirehtetCmd = cmd
	a.isBusy = false
	a.isSharing = true
	a.mu.Unlock()

	pipeReader := func(r io.ReadCloser) {
		if r == nil {
			return
		}
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			if formatted, ok := formatGnirehtetLog(sc.Text()); ok {
				a.log(formatted)
			}
		}
	}
	go pipeReader(stdoutPipe)
	go pipeReader(stderrPipe)

	go func(c *exec.Cmd) {
		_ = c.Wait()
		a.mu.Lock()
		wasActive := a.isSharing && a.gnirehtetCmd == c
		if wasActive {
			a.gnirehtetCmd = nil
			a.isSharing = false
		}
		a.mu.Unlock()
		if wasActive {
			a.log("Раздача по USB завершена.")
		}
	}(cmd)

	if startProxy {
		time.Sleep(1200 * time.Millisecond)
		res := a.adb.StartHotspotProxyApp(serial, ProxyPort)
		if res.ExitCode == 0 {
			a.logf("ShareNet запущен на телефоне (:%d).", ProxyPort)
		} else {
			a.logf("Не удалось автоматически запустить ShareNet на телефоне: %s", res.Output)
		}
	}

	hotspot := a.adb.DetectHotspotStatus(serial)
	a.mu.Lock()
	a.hotspot = hotspot
	a.mu.Unlock()
}

func (a *ShareApp) stopSharing() {
	a.log("Остановка раздачи интернета и ShareNet...")
	a.cleanupProcessesAndRestoreDns()
	a.log("Раздача остановлена.")
}

func (a *ShareApp) stopSharingOnUsbDisconnect() {
	a.log("USB-кабель отключён — остановка USB-раздачи и прокси...")
	a.cleanupProcessesAndRestoreDns()
	a.log("USB-раздача остановлена после отключения кабеля.")
}

func (a *ShareApp) cleanupProcessesAndRestoreDns() {
	a.mu.Lock()
	socks := a.socks
	a.socks = nil
	gnCmd := a.gnirehtetCmd
	a.gnirehtetCmd = nil
	serial := a.activeSerial
	backup := a.dnsBackup
	a.dnsBackup = adb.PrivateDnsBackup{}
	startProxy := a.startProxy
	a.isSharing = false
	a.isBusy = false
	a.mu.Unlock()

	if socks != nil {
		socks.Stop()
	}
	if gnCmd != nil && gnCmd.Process != nil {
		_ = gnCmd.Process.Kill()
	}
	if serial != "" {
		_ = a.adb.RunGnirehtet(5*time.Second, "stop", serial)
		_ = a.adb.RunAdb(4*time.Second, "-s", serial, "shell", "am", "force-stop", "com.genymobile.gnirehtet")
		_ = a.adb.RunAdb(3*time.Second, "-s", serial, "reverse", "--remove", fmt.Sprintf("tcp:%d", UsbSocksPort))
		if startProxy {
			_ = a.adb.StopHotspotProxyApp(serial)
		}
		if backup.WasModified {
			a.adb.RestorePrivateDns(serial, backup)
			a.logf("Персональный DNS (%s) восстановлен.", backup.Specifier)
		}
	}
}

func isAllowedExternalURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t\r\n\"'`\\") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return strings.EqualFold(u.Host, "github.com") && (u.Path == "/TPpixel" || strings.HasPrefix(u.Path, "/TPpixel/"))
	case "tg":
		return strings.EqualFold(u.Host, "socks")
	default:
		return false
	}
}

func (a *ShareApp) startServer() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	subFS, err := fs.Sub(uiFS, "internal/ui")
	if err != nil {
		return "", err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(subFS, "index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rendered := strings.ReplaceAll(string(data), "__API_TOKEN__", a.apiToken)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, rendered)
	})

	handleJSON := func(path string, fn func(*http.Request) interface{}) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Session-Token") != a.apiToken {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			res := fn(r)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(res)
		})
	}

	handleJSON("/api/state", func(r *http.Request) interface{} { return a.snapshot() })
	handleJSON("/api/refresh", func(r *http.Request) interface{} {
		a.refreshDevices()
		return a.snapshot()
	})
	handleJSON("/api/auto-dns", func(r *http.Request) interface{} {
		dns := netutil.DetectPrimaryDns()
		a.mu.Lock()
		a.dnsValue = dns
		a.mu.Unlock()
		a.logf("Определён DNS сети ПК: %s", dns)
		return a.snapshot()
	})
	handleJSON("/api/settings", func(r *http.Request) interface{} {
		var req struct {
			AutoStartUsb   *bool   `json:"autoStartUsb"`
			DisablePrivDns *bool   `json:"disablePrivDns"`
			StartProxy     *bool   `json:"startProxy"`
			Dns            *string `json:"dns"`
		}
		if json.NewDecoder(r.Body).Decode(&req) == nil {
			a.mu.Lock()
			if req.AutoStartUsb != nil {
				a.autoStartUsb = *req.AutoStartUsb
			}
			if req.DisablePrivDns != nil {
				a.disablePrivDns = *req.DisablePrivDns
			}
			if req.StartProxy != nil {
				a.startProxy = *req.StartProxy
			}
			if req.Dns != nil {
				a.dnsValue = strings.TrimSpace(*req.Dns)
			}
			a.mu.Unlock()
		}
		return a.snapshot()
	})
	handleJSON("/api/toggle", func(r *http.Request) interface{} {
		var req struct {
			Dns            string `json:"dns"`
			DisablePrivDns bool   `json:"disablePrivDns"`
			StartProxy     bool   `json:"startProxy"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		a.mu.Lock()
		sharing, busy := a.isSharing, a.isBusy
		a.mu.Unlock()
		if !busy {
			if sharing {
				go a.stopSharing()
			} else {
				go a.startSharing(req.Dns, req.DisablePrivDns, req.StartProxy)
			}
		}
		return a.snapshot()
	})
	handleJSON("/api/open-hotspot", func(r *http.Request) interface{} {
		a.mu.Lock()
		serial := a.activeSerial
		a.mu.Unlock()
		if serial == "" {
			a.log("Сначала подключите телефон по USB.")
		} else {
			go func() {
				res := a.adb.OpenHotspotSettings(serial)
				if res.ExitCode == 0 {
					a.log("Открыты настройки точки доступа на телефоне.")
				} else {
					a.log(res.Output)
				}
			}()
		}
		return a.snapshot()
	})
	handleJSON("/api/reinstall-apks", func(r *http.Request) interface{} {
		a.mu.Lock()
		serial := a.activeSerial
		a.mu.Unlock()
		if serial == "" {
			a.log("Сначала подключите телефон по USB.")
		} else {
			go a.adb.EnsureRequiredApksInstalled(serial, true, true, a.log)
		}
		return a.snapshot()
	})
	handleJSON("/api/send-file", func(r *http.Request) interface{} {
		a.mu.Lock()
		serial := a.activeSerial
		a.mu.Unlock()
		if serial == "" {
			a.log("Сначала подключите телефон по USB.")
		} else {
			go func() {
				path, ok, err := winui.PickFile("Выберите файл, изображение или APK для отправки на телефон")
				if err != nil {
					a.logf("Ошибка диалога выбора файла: %v", err)
					return
				}
				if !ok || path == "" {
					return
				}
				a.logf("Отправка: %s...", filepath.Base(path))
				a.log(a.adb.SendFileSmart(serial, path))
			}()
		}
		return a.snapshot()
	})
	handleJSON("/api/install-adb", func(r *http.Request) interface{} {
		msg, err := netutil.InstallAdbToUserProfile(a.adb.BundledPlatformToolsDir)
		if err != nil {
			a.logf("Ошибка установки ADB: %v", err)
		} else {
			a.log(msg + " Новые окна терминала увидят adb автоматически.")
		}
		return a.snapshot()
	})
	handleJSON("/api/open-terminal", func(r *http.Request) interface{} {
		if err := winui.OpenInteractiveAdbTerminal(a.adb.EffectiveEnv()); err != nil {
			a.logf("Не удалось открыть cmd: %v", err)
		}
		return a.snapshot()
	})
	handleJSON("/api/open-url", func(r *http.Request) interface{} {
		var req struct {
			URL string `json:"url"`
		}
		if json.NewDecoder(r.Body).Decode(&req) == nil && isAllowedExternalURL(req.URL) {
			_ = netutil.OpenURL(req.URL)
		}
		return a.snapshot()
	})
	handleJSON("/api/clear-logs", func(r *http.Request) interface{} {
		a.mu.Lock()
		a.logs = nil
		a.mu.Unlock()
		return a.snapshot()
	})
	handleJSON("/api/vpn-toggle", func(r *http.Request) interface{} {
		var req struct {
			Gateway string `json:"gateway"`
			Mode    string `json:"mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		st := a.vpn.Snapshot()
		if !st.IsBusy {
			if st.IsConnected {
				go a.vpn.Stop()
			} else {
				go func(gw, m string) { _ = a.vpn.Start(gw, m) }(req.Gateway, req.Mode)
			}
		}
		return a.snapshot()
	})
	handleJSON("/api/vpn-settings", func(r *http.Request) interface{} {
		var req struct {
			Mode    string  `json:"mode"`
			Gateway *string `json:"gateway"`
		}
		if json.NewDecoder(r.Body).Decode(&req) == nil {
			if req.Mode == "tun" || req.Mode == "proxy" {
				a.vpn.SetMode(req.Mode)
			}
			if req.Gateway != nil {
				a.vpn.SetManualGateway(*req.Gateway)
			}
		}
		return a.snapshot()
	})
	handleJSON("/api/vpn-refresh", func(r *http.Request) interface{} {
		var req struct {
			Gateway *string `json:"gateway"`
		}
		if json.NewDecoder(r.Body).Decode(&req) == nil && req.Gateway != nil {
			a.vpn.SetManualGateway(*req.Gateway)
		}
		a.vpn.Refresh()
		return a.snapshot()
	})
	handleJSON("/api/vpn-check", func(r *http.Request) interface{} {
		go a.vpn.CheckConnectivity()
		return a.snapshot()
	})
	handleJSON("/api/reset-all", func(r *http.Request) interface{} {
		go func() {
			a.log("Выполняется полный сброс и откат (Раздача USB + Приём VPN/Прокси)...")
			a.cleanupProcessesAndRestoreDns()
			a.mu.Lock()
			serial := a.activeSerial
			a.mu.Unlock()
			a.adb.ResetGnirehtetState(serial)
			a.vpn.Stop()
			a.log("Полный откат завершён.")
		}()
		return a.snapshot()
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String() + "/", nil
}

func main() {
	winui.EnableDPIAware()
	app := newShareApp()

	appURL, err := app.startServer()
	if err != nil {
		return
	}

	go func() {
		app.refreshDevices()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			app.refreshDevices()
			app.vpn.TailTunLog()
			app.vpn.Refresh()
		}
	}()

	app.mu.Lock()
	dark := app.darkTheme
	app.mu.Unlock()

	dataDir := filepath.Join(os.TempDir(), "ShareNet-WebView2")
	w := winui.NewWebViewWindow(winui.WebViewOptions{
		Title:     "ShareNet",
		DataPath:  dataDir,
		URL:       appURL,
		Width:     360,
		Height:    620,
		MinWidth:  340,
		MinHeight: 580,
		Dark:      dark,
	})

	if w != nil {
		app.mu.Lock()
		app.wv = w
		app.mu.Unlock()

		w.Run()
		w.Destroy()
	} else {
		_ = winui.OpenAppFallback(appURL)
	}

	app.cleanupProcessesAndRestoreDns()
	app.vpn.Stop()
}
