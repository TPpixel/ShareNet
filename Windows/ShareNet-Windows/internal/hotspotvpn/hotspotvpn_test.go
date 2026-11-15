package hotspotvpn

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildSingBoxConfig(t *testing.T) {
	raw, err := BuildSingBoxConfig("192.168.43.1", 8080, `C:\Temp\sing.log`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	outbounds := parsed["outbounds"].([]any)
	socks := outbounds[0].(map[string]any)
	if socks["server"] != "192.168.43.1" || int(socks["server_port"].(float64)) != 8080 {
		t.Fatalf("unexpected socks outbound: %#v", socks)
	}

	if _, err := BuildSingBoxConfig("invalid-ip", 8080, "x.log"); err == nil {
		t.Fatal("expected error for invalid IP")
	}
	if _, err := BuildSingBoxConfig("192.168.43.1", 70000, "x.log"); err == nil {
		t.Fatal("expected error for invalid port")
	}
}

func TestNewManagerDefaultsToTunWhenSingBoxPresent(t *testing.T) {
	tmp := t.TempDir()
	sbDir := filepath.Join(tmp, "tools", "sing-box")
	if err := os.MkdirAll(sbDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sbDir, "sing-box.exe"), []byte("stub"), 0755); err != nil {
		t.Fatal(err)
	}

	m := NewManagerWithRoot(tmp, nil)
	if st := m.Snapshot(); st.Mode != "tun" {
		t.Fatalf("expected default mode 'tun' when sing-box exists, got %q", st.Mode)
	}

	empty := NewManagerWithRoot(t.TempDir(), nil)
	if st := empty.Snapshot(); st.Mode != "proxy" {
		t.Fatalf("expected fallback mode 'proxy' without sing-box, got %q", st.Mode)
	}
}

func TestIsPrivateHotspotIPv4(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"192.168.43.1", true},
		{"10.72.31.3", true},
		{"172.20.10.1", true},
		{"172.19.0.1", false},
		{"8.8.8.8", false},
	}
	for _, tc := range cases {
		if got := IsPrivateHotspotIPv4(net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("IsPrivateHotspotIPv4(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestIsIgnoredGatewayAdapter(t *testing.T) {
	cases := []struct {
		friendly string
		desc     string
		want     bool
	}{
		{"Radmin VPN", "Famatech Radmin VPN Ethernet Adapter", true},
		{"Беспроводная сеть", "Intel(R) Wi-Fi 6 AX201", false},
		{"Ethernet", "Realtek PCIe GbE Family Controller", false},
		{"vEthernet (WSL)", "Hyper-V Virtual Ethernet Adapter", true},
		{"Tailscale", "Tailscale Tunnel", true},
	}
	for _, tc := range cases {
		if got := IsIgnoredGatewayAdapter(tc.friendly, tc.desc); got != tc.want {
			t.Errorf("IsIgnoredGatewayAdapter(%q, %q) = %v, want %v", tc.friendly, tc.desc, got, tc.want)
		}
	}
}
