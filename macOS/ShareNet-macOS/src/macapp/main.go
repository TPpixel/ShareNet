package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

//go:embed ui.html
var uiHTML []byte

const (
	defaultUIPort = 18080
	uiURLPath     = "/tmp/hotspot-vpn-ui.url"
)

func openAppWindow(uiURL string) {
	chromeBin := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(chromeBin); err == nil {
		cmd := exec.Command(chromeBin, "--app="+uiURL, "--window-size=500,760")
		if err := cmd.Start(); err == nil {
			go func() { _ = cmd.Wait() }()
			return
		}
	}
	_ = exec.Command("/usr/bin/open", uiURL).Run()
}

func generateSessionToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func isAllowedRequest(r *http.Request, token string) bool {
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		if !strings.HasPrefix(origin, "http://127.0.0.1:") && !strings.HasPrefix(origin, "http://localhost:") {
			return false
		}
	}
	return r.Header.Get("X-Session-Token") == token || r.URL.Query().Get("token") == token
}

func readMacTheme() map[string]any {
	styleOut, styleErr := exec.Command("/usr/bin/defaults", "read", "-g", "AppleInterfaceStyle").Output()
	dark := styleErr == nil && strings.EqualFold(strings.TrimSpace(string(styleOut)), "Dark")

	accentOut, accentErr := exec.Command("/usr/bin/defaults", "read", "-g", "AppleAccentColor").Output()
	accentKey := "default"
	if accentErr == nil {
		if s := strings.TrimSpace(string(accentOut)); s != "" {
			accentKey = s
		}
	}

	themeCache.Lock()
	defer themeCache.Unlock()
	if themeCache.valid && themeCache.dark == dark && themeCache.key == accentKey {
		return themeCache.val
	}

	r, g, b := fallbackAccentRGB(accentKey, dark)
	appName := "NSAppearanceNameAqua"
	if dark {
		appName = "NSAppearanceNameDarkAqua"
	}
	script := `ObjC.import("Cocoa");
$.NSAppearance.setCurrentAppearance($.NSAppearance.appearanceNamed("` + appName + `"));
var c = $.NSColor.controlAccentColor.colorUsingColorSpace($.NSColorSpace.sRGBColorSpace);
Math.round(c.redComponent*255) + "," + Math.round(c.greenComponent*255) + "," + Math.round(c.blueComponent*255);`
	if out, err := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", script).Output(); err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), ",")
		if len(parts) == 3 {
			if rv, e1 := strconv.Atoi(parts[0]); e1 == nil {
				if gv, e2 := strconv.Atoi(parts[1]); e2 == nil {
					if bv, e3 := strconv.Atoi(parts[2]); e3 == nil {
						r, g, b = clampByte(rv), clampByte(gv), clampByte(bv)
					}
				}
			}
		}
	}

	accent := fmt.Sprintf("#%02x%02x%02x", r, g, b)
	hr, hg, hb := adjustHoverRGB(r, g, b, dark)
	accentHover := fmt.Sprintf("#%02x%02x%02x", hr, hg, hb)
	accentFg := "#ffffff"
	// Relative luminance check so bright accents (like yellow) keep crisp text contrast
	if 0.2126*float64(r)+0.7152*float64(g)+0.0722*float64(b) > 175 {
		accentFg = "#1d1d1f"
	}

	res := map[string]any{
		"dark":        dark,
		"accent":      accent,
		"accentHover": accentHover,
		"accentFg":    accentFg,
	}
	themeCache.valid = true
	themeCache.dark = dark
	themeCache.key = accentKey
	themeCache.val = res
	return res
}

var themeCache struct {
	sync.Mutex
	valid bool
	dark  bool
	key   string
	val   map[string]any
}

func clampByte(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func adjustHoverRGB(r, g, b int, dark bool) (int, int, int) {
	if dark {
		return clampByte(r + (255-r)/8), clampByte(g + (255-g)/8), clampByte(b + (255-b)/8)
	}
	return clampByte(r * 9 / 10), clampByte(g * 9 / 10), clampByte(b * 9 / 10)
}

func fallbackAccentRGB(key string, dark bool) (int, int, int) {
	switch key {
	case "-1":
		if dark {
			return 152, 152, 157
		}
		return 142, 142, 147
	case "0":
		return 255, 59, 48
	case "1":
		return 255, 149, 0
	case "2":
		return 255, 204, 0
	case "3":
		return 40, 205, 65
	case "5":
		return 165, 80, 167
	case "6":
		return 247, 79, 158
	default:
		if dark {
			return 10, 132, 255
		}
		return 0, 122, 255
	}
}

func focusRunningInstance() bool {
	script := `ObjC.import("Cocoa");
var me = ` + fmt.Sprintf("%d", os.Getpid()) + `;
var apps = $.NSRunningApplication.runningApplicationsWithBundleIdentifier("com.tppixel.sharenet");
var ok = false;
for (var i = 0; i < apps.count; i++) {
  var a = apps.objectAtIndex(i);
  if (a.processIdentifier !== me) {
    a.unhide;
    a.activateWithOptions(3);
    ok = true;
  }
}
ok ? "1" : "0";`
	out, err := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", script).Output()
	return err == nil && strings.TrimSpace(string(out)) == "1"
}

func isAlreadyRunning(url string) bool {
	client := &http.Client{Timeout: 350 * time.Millisecond}
	resp, err := client.Get(url + "api/ping")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func waitForServerURL(fallbackURL string) string {
	for i := 0; i < 60; i++ {
		if raw, err := os.ReadFile(uiURLPath); err == nil {
			u := string(raw)
			if u != "" && isAlreadyRunning(u) {
				return u
			}
		}
		if isAlreadyRunning(fallbackURL) {
			return fallbackURL
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ""
}

func main() {
	exe, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	macosDir := filepath.Dir(exe)
	contentsDir := filepath.Dir(macosDir)
	resDir := filepath.Join(contentsDir, "Resources")
	appDir := filepath.Dir(contentsDir)
	appParent := filepath.Dir(appDir)

	serverMode := false
	for _, arg := range os.Args[1:] {
		if arg == "--server" {
			serverMode = true
			break
		}
	}

	// Best-effort clear quarantine on self & ensure sing-box has +x
	_ = exec.Command("/usr/bin/xattr", "-dr", "com.apple.quarantine", appDir).Run()
	singBoxPath := resolveSingBox(resDir, appParent)
	if singBoxPath != "" {
		_ = os.Chmod(singBoxPath, 0o755)
	}

	existingURL := fmt.Sprintf("http://127.0.0.1:%d/", defaultUIPort)
	windowJSPath := filepath.Join(resDir, "window.js")
	_, hasWindowJS := os.Stat(windowJSPath)
	_, hasOsascript := os.Stat("/usr/bin/osascript")

	if !serverMode {
		// If another instance is already running on :18080, focus it or open UI and exit cleanly
		if isAlreadyRunning(existingURL) {
			if focusRunningInstance() {
				return
			}
			if hasWindowJS == nil && hasOsascript == nil {
				_ = syscall.Exec("/usr/bin/osascript", []string{"osascript", "-l", "JavaScript", windowJSPath, existingURL, resDir}, os.Environ())
			}
			openAppWindow(existingURL)
			return
		}

		// Launch backend in --server child process and exec native Dock window (keeps bundle PID)
		if hasWindowJS == nil && hasOsascript == nil {
			_ = os.Remove(uiURLPath)
			cmd := exec.Command(exe, "--server")
			if err := cmd.Start(); err == nil {
				if readyURL := waitForServerURL(existingURL); readyURL != "" {
					if err := syscall.Exec("/usr/bin/osascript", []string{"osascript", "-l", "JavaScript", windowJSPath, readyURL, resDir}, os.Environ()); err == nil {
						return
					}
				}
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			}
		}
	}

	backend := NewBackend(singBoxPath)
	sessionToken := generateSessionToken()
	var lastSeen atomic.Int64
	lastSeen.Store(time.Now().UnixNano())
	var pendingBye atomic.Bool
	quitCh := make(chan struct{}, 1)

	protect := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !isAllowedRequest(r, sessionToken) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		lastSeen.Store(time.Now().UnixNano())
		pendingBye.Store(false)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		rendered := strings.ReplaceAll(string(uiHTML), "__SESSION_TOKEN__", sessionToken)
		_, _ = w.Write([]byte(rendered))
	})
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		lastSeen.Store(time.Now().UnixNano())
		pendingBye.Store(false)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/api/theme", protect(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, readMacTheme())
	}))
	mux.HandleFunc("/api/status", protect(func(w http.ResponseWriter, r *http.Request) {
		lastSeen.Store(time.Now().UnixNano())
		pendingBye.Store(false)
		writeJSON(w, backend.Status(r.URL.Query().Get("gw")))
	}))
	mux.HandleFunc("/api/connect", protect(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Mode    string `json:"mode"`
			Gateway string `json:"gateway"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeJSON(w, backend.Connect(req.Mode, req.Gateway))
	}))
	mux.HandleFunc("/api/disconnect", protect(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, backend.Disconnect())
	}))
	mux.HandleFunc("/api/check", protect(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Gateway string `json:"gateway"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeJSON(w, backend.CheckNetwork(req.Gateway))
	}))
	mux.HandleFunc("/api/copy", protect(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Gateway string `json:"gateway"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeJSON(w, backend.CopyAddress(req.Gateway))
	}))
	mux.HandleFunc("/api/copy-logs", protect(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeJSON(w, backend.CopyLogs(req.Text))
	}))
	mux.HandleFunc("/api/open-github", protect(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, backend.OpenGitHub())
	}))
	mux.HandleFunc("/api/bye", protect(func(w http.ResponseWriter, r *http.Request) {
		pendingBye.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/quit", protect(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"quitting": true})
		select {
		case quitCh <- struct{}{}:
		default:
		}
	}))

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", defaultUIPort))
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ShareNet error: %v\n", err)
		os.Exit(1)
	}

	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(ln)
	}()

	uiURL := fmt.Sprintf("http://%s/", ln.Addr().String())
	_ = writeFileSafe(uiURLPath, []byte(uiURL), 0o600)
	defer os.Remove(uiURLPath)

	fmt.Printf("ShareNet запущен: %s\n", uiURL)
	if !serverMode {
		openAppWindow(uiURL)
	}

	initialPPID := os.Getppid()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-sigCh:
			backend.Disconnect()
			_ = srv.Close()
			return
		case <-quitCh:
			backend.Disconnect()
			_ = srv.Close()
			return
		case <-ticker.C:
			if serverMode && initialPPID > 1 && os.Getppid() != initialPPID {
				backend.Disconnect()
				_ = srv.Close()
				return
			}
			if pendingBye.Load() && time.Since(time.Unix(0, lastSeen.Load())) > 3*time.Second {
				backend.Disconnect()
				_ = srv.Close()
				return
			}
		}
	}
}
