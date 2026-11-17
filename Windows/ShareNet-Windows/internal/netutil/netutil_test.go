package netutil

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsPrivateIPv4(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.255.254", true},
		{"172.32.0.1", false},
		{"192.168.43.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
	}
	for _, tc := range cases {
		if got := IsPrivateIPv4(net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("IsPrivateIPv4(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestBuildGnirehtetRoutes(t *testing.T) {
	routes := BuildGnirehtetRoutes("192.168.1.1, 8.8.8.8, 10.5.5.1")
	parts := strings.Split(routes, ",")

	contains := func(target string) bool {
		for _, p := range parts {
			if p == target {
				return true
			}
		}
		return false
	}

	if !contains("10.0.0.2/32") || !contains("10.0.2.2/32") {
		t.Fatalf("missing control routes in %q", routes)
	}
	if !contains("192.168.1.1/32") || !contains("10.5.5.1/32") {
		t.Fatalf("expected private DNS routes in %q", routes)
	}
	if contains("8.8.8.8/32") {
		t.Fatalf("public DNS should not be added as extra host route in %q", routes)
	}

	colonRoutes := BuildGnirehtetRoutes("192.168.1.1:1.1.1.1:10.5.5.1")
	if !strings.Contains(colonRoutes, "192.168.1.1/32") || !strings.Contains(colonRoutes, "10.5.5.1/32") {
		t.Fatalf("expected private colon-separated DNS routes in %q", colonRoutes)
	}
}

func TestNormalizeDnsCsv(t *testing.T) {
	if got := NormalizeDnsCsv("192.168.1.1:1.1.1.1"); got != "192.168.1.1,1.1.1.1" {
		t.Fatalf("got %q, want 192.168.1.1,1.1.1.1", got)
	}
	if got := NormalizeDnsCsv("1.1.1.1; 8.8.8.8"); got != "1.1.1.1,8.8.8.8" {
		t.Fatalf("got %q, want 1.1.1.1,8.8.8.8", got)
	}
	if got := NormalizeDnsCsv(""); got != "8.8.8.8" {
		t.Fatalf("got %q, want fallback 8.8.8.8", got)
	}
}

func TestPathListHasExistingAdb(t *testing.T) {
	tmp := t.TempDir()
	adbDir := filepath.Join(tmp, "platform-tools")
	if err := os.MkdirAll(adbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if pathListHasExistingAdb(`C:\missing;` + adbDir) {
		t.Fatalf("expected false when adb.exe does not exist")
	}
	if err := os.WriteFile(filepath.Join(adbDir, "adb.exe"), []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !pathListHasExistingAdb(`C:\missing;"` + adbDir + `"`) {
		t.Fatalf("expected true when adb.exe exists in quoted path")
	}
	t.Setenv("TEST_SDK_ROOT", tmp)
	if !pathListHasExistingAdb(`%TEST_SDK_ROOT%\platform-tools`) {
		t.Fatalf("expected true when %%ENV%% expands to existing adb.exe dir")
	}
}

func TestParseWindowsProxyServer(t *testing.T) {
	if h, p, ok := ParseWindowsProxyServer("10.5.0.10:3128"); !ok || h != "10.5.0.10" || p != 3128 {
		t.Fatalf("got (%q, %d, %v), want (10.5.0.10, 3128, true)", h, p, ok)
	}
	if h, p, ok := ParseWindowsProxyServer("http=192.168.10.2:8080;https=192.168.10.3:8443;socks=192.168.10.4:1080"); !ok || h != "192.168.10.3" || p != 8443 {
		t.Fatalf("got (%q, %d, %v), want https candidate", h, p, ok)
	}
	for _, reject := range []string{"", "127.0.0.1:8080", "localhost:3128", "socks=10.0.0.1:1080", "0.0.0.0:8080"} {
		if h, p, ok := ParseWindowsProxyServer(reject); ok {
			t.Fatalf("expected %q rejected, got (%q, %d)", reject, h, p)
		}
	}
}
