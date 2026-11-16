package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatGnirehtetLog(t *testing.T) {
	if _, ok := formatGnirehtetLog("2026-11-15 16:05:12.100 DEBUG relaylib::relay::tcp_connection: Open"); ok {
		t.Fatal("expected DEBUG line filtered")
	}
	if _, ok := formatGnirehtetLog("2026-11-15 16:05:12.100 INFO relaylib::relay::tcp_connection: Open 1.2.3.4:443"); ok {
		t.Fatal("expected noisy INFO connection line filtered")
	}
	if _, ok := formatGnirehtetLog("2026-11-15 15:58:34.168 WARN Router: Dropping invalid packet"); ok {
		t.Fatal("expected dropping invalid packet warning filtered")
	}
	if got, ok := formatGnirehtetLog("2026-11-15 16:05:12.100 INFO relaylib::relay::tunnel_server: Client #0 connected"); !ok || got != "Телефон подключился к USB-туннелю." {
		t.Fatalf("unexpected client connected log: %q %v", got, ok)
	}
	if got, ok := formatGnirehtetLog("2026-11-15 16:05:12.100 ERROR relaylib::relay::tunnel_server: Cannot accept client: bind failed"); !ok || got != "gnirehtet: Cannot accept client: bind failed" {
		t.Fatalf("unexpected error log: %q %v", got, ok)
	}
}

func TestShareAppLogDeduplication(t *testing.T) {
	a := &ShareApp{}
	a.log("Повтор")
	a.log("Повтор")
	a.log("Важное событие")
	a.log("Повтор")
	if len(a.logs) != 2 {
		t.Fatalf("expected 2 deduplicated logs, got %d", len(a.logs))
	}
}

func TestIsAllowedExternalURL(t *testing.T) {
	valid := []string{
		"https://github.com/TPpixel",
		"https://github.com/TPpixel/ShareNet",
		"tg://socks?server=192.168.43.1&port=8080",
	}
	for _, u := range valid {
		if !isAllowedExternalURL(u) {
			t.Errorf("expected allowed URL %q", u)
		}
	}

	invalid := []string{
		"",
		"file:///C:/Windows/System32/cmd.exe",
		"https://evil.com/TPpixel",
		"https://github.com/other",
		"https://github.com/TPpixel\" --bad",
		"tg://resolve?domain=someone",
	}
	for _, u := range invalid {
		if isAllowedExternalURL(u) {
			t.Errorf("expected rejected URL %q", u)
		}
	}
}

func TestSanitizeDnsSetting(t *testing.T) {
	if got := sanitizeDnsSetting("1.1.1.1, 8.8.8.8"); got != "1.1.1.1:8.8.8.8" {
		t.Fatalf("got %q, want 1.1.1.1:8.8.8.8", got)
	}
	if got := sanitizeDnsSetting("1.1.1.1; rm -rf /"); got != "1.1.1.1" {
		t.Fatalf("expected non-IP injection stripped, got %q", got)
	}
	if got := sanitizeDnsSetting("not-an-ip"); got != "8.8.8.8" {
		t.Fatalf("expected safe fallback 8.8.8.8 for invalid DNS, got %q", got)
	}
	if got := sanitizeDnsSetting(""); strings.TrimSpace(got) == "" {
		t.Fatal("expected auto-detected fallback for empty DNS")
	}
}

func TestCleanupStaleMiniShareConfigs(t *testing.T) {
	tmp := t.TempDir()
	origTmp := os.Getenv("TMP")
	origTemp := os.Getenv("TEMP")
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	defer func() {
		_ = os.Setenv("TMP", origTmp)
		_ = os.Setenv("TEMP", origTemp)
	}()

	stale := filepath.Join(tmp, "sharenet_pc_server_12345.json")
	keep := filepath.Join(tmp, "unrelated_keep.json")
	if err := os.WriteFile(stale, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	cleanupStaleMiniShareConfigs()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale temp config removed, err=%v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("expected unrelated file preserved, err=%v", err)
	}
}
