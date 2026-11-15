package main

import "testing"

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
