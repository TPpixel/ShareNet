package adb

import (
	"strings"
	"testing"
)

func TestParseDevicesOutput(t *testing.T) {
	raw := `List of devices attached
R5CX123456A            device product:dm3qxx model:SM_S918B device:dm3q transport_id:1
emulator-5554          unauthorized transport_id:2
* daemon started successfully
`
	devs := ParseDevicesOutput(raw)
	if len(devs) != 2 {
		t.Fatalf("got %d devices, want 2: %+v", len(devs), devs)
	}
	if devs[0].Serial != "R5CX123456A" || devs[0].State != "device" || devs[0].Model != "SM S918B" {
		t.Fatalf("unexpected first device: %+v", devs[0])
	}
	if devs[1].Serial != "emulator-5554" || devs[1].State != "unauthorized" {
		t.Fatalf("unexpected second device: %+v", devs[1])
	}
}

func TestParseHotspotStatus(t *testing.T) {
	tethering := `Tethering:
  wlan2 - TetheredState - UpstreamWanted - AutoRestart
`
	ipAddr := `1: lo: <LOOPBACK,UP>
    inet 127.0.0.1/8 scope host lo
28: wlan2: <BROADCAST,MULTICAST,UP,LOWER_UP>
    inet 192.168.43.1/24 brd 192.168.43.255 scope global wlan2
`
	st := ParseHotspotStatus(tethering, ipAddr)
	if !st.IsActive || st.InterfaceName != "wlan2" || st.IpAddress != "192.168.43.1" {
		t.Fatalf("unexpected hotspot status from tethered iface: %+v", st)
	}

	// Fallback prefix matching when dumpsys output does not show TetheredState
	ipFallback := `15: swlan0: <BROADCAST,UP>
    inet 192.168.137.1/24 brd 192.168.137.255 scope global swlan0
`
	st2 := ParseHotspotStatus("", ipFallback)
	if !st2.IsActive || st2.InterfaceName != "swlan0" || st2.IpAddress != "192.168.137.1" {
		t.Fatalf("unexpected fallback hotspot status: %+v", st2)
	}
}

func TestPrimaryDnsIP(t *testing.T) {
	if got := PrimaryDnsIP("192.168.1.1:1.1.1.1"); got != "1.1.1.1" {
		t.Fatalf("got %q, want 1.1.1.1", got)
	}
	if got := PrimaryDnsIP("192.168.0.1"); got != "192.168.0.1" {
		t.Fatalf("got %q, want 192.168.0.1", got)
	}
	if got := PrimaryDnsIP("invalid"); got != "1.1.1.1" {
		t.Fatalf("got %q, want 1.1.1.1 fallback", got)
	}
}

func TestMiniShareConfigsAndUUID(t *testing.T) {
	u1 := GenerateUUIDv4()
	u2 := GenerateUUIDv4()
	if len(u1) != 36 || u1 == u2 {
		t.Fatalf("expected unique 36-char UUIDv4s, got %q and %q", u1, u2)
	}
	srv := BuildMiniShareServerJSON(u1, 10808)
	for _, want := range []string{u1, `"127.0.0.1"`, `"vless"`, `"warn"`, `"127.0.0.0/8"`, `"198.18.0.0/15"`, `"ip_is_private": true`, `"reject"`, `"resolve"`} {
		if !strings.Contains(srv, want) {
			t.Fatalf("unexpected server config (missing %s): %s", want, srv)
		}
	}
	srvWithLanDns := BuildMiniShareServerJSON(u1, 10808, "192.168.1.1")
	if !strings.Contains(srvWithLanDns, `"192.168.1.1/32"`) {
		t.Fatalf("expected private router DNS allow rule on port 53: %s", srvWithLanDns)
	}
	srvLoopbackDns := BuildMiniShareServerJSON(u1, 10808, "127.0.0.1")
	if strings.Contains(srvLoopbackDns, `"127.0.0.1/32"`) {
		t.Fatalf("loopback DNS must never be exempted from server SSRF reject rules: %s", srvLoopbackDns)
	}
	cli := BuildMiniShareClientJSON(u1, 10808, 1053, "192.168.1.1:1.1.1.1")
	for _, want := range []string{u1, `"1.1.1.1"`, `"https"`, `"/dns-query"`, `"xudp"`, `"mtu": 1500`, `"gvisor"`, `"warn"`, `"198.18.0.0/15"`, `"reject"`} {
		if !strings.Contains(cli, want) {
			t.Fatalf("unexpected client config (missing %s): %s", want, cli)
		}
	}
	if strings.Contains(cli, `"outbound": "direct"`) {
		t.Fatalf("client config must not route private IPs to direct: %s", cli)
	}
	cliLanDns := BuildMiniShareClientJSON(u1, 10808, 1053, "10.1.0.5")
	if !strings.Contains(cliLanDns, `"type": "udp"`) || !strings.Contains(cliLanDns, `"10.1.0.5"`) {
		t.Fatalf("expected UDP DNS for explicit private DNS server, got: %s", cliLanDns)
	}
	srvWithProxy := buildMiniShareServerJSONWithProxy(u1, 10808, "1.1.1.1", "10.5.0.10", 3128)
	if !strings.Contains(srvWithProxy, `"upstream-proxy"`) || !strings.Contains(srvWithProxy, `"10.5.0.10"`) || !strings.Contains(srvWithProxy, `"final": "upstream-proxy"`) {
		t.Fatalf("expected HTTP upstream proxy config, got: %s", srvWithProxy)
	}
}

func TestMiniShareIfaceValidation(t *testing.T) {
	for _, valid := range []string{"ap0", "swlan0", "wlan2", "rndis0", "wlan0.ap"} {
		if !IsValidIfaceName(valid) {
			t.Fatalf("expected %q to be valid iface", valid)
		}
	}
	for _, bad := range []string{"", "ap0; id", "ap0 && rm -rf /", "../ap0", "ap0$(id)", "iface_name_exceeding_thirty_two_chars"} {
		if IsValidIfaceName(bad) {
			t.Fatalf("expected %q to be rejected as invalid iface", bad)
		}
	}
	h := NewHelper()
	if _, err := h.StartMiniShare("SERIAL123", GenerateUUIDv4(), "ap0;id", "1.1.1.1", 10808); err == nil || !strings.Contains(err.Error(), "недопустимое имя интерфейса") {
		t.Fatalf("expected StartMiniShare to reject unsafe interface name, got err=%v", err)
	}
}

func TestHasReversePort(t *testing.T) {
	if HasReversePort("", 10808) {
		t.Fatal("expected false on empty output")
	}
	if HasReversePort("UsbFfs tcp:108080 tcp:108080\n", 10808) {
		t.Fatal("must not match port prefix 108080 for 10808")
	}
	if HasReversePort("UsbFfs tcp:10808 tcp:10809\n", 10808) {
		t.Fatal("must require matching remote and local port")
	}
	raw := "UsbFfs tcp:31416 tcp:31416\nUsbFfs tcp:10808 tcp:10808\r\n"
	if !HasReversePort(raw, 10808) {
		t.Fatalf("expected reverse port 10808 detected in %q", raw)
	}
	if HasReversePort(raw, 0) {
		t.Fatal("expected false for invalid port 0")
	}
}
