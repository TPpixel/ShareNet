package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestIsValidIPv4(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"172.20.10.1", true},
		{"192.168.43.1", true},
		{"0.0.0.0", true},
		{"255.255.255.255", true},
		{"256.1.1.1", false},
		{"172.20.10", false},
		{"", false},
		{"abc.def.1.1", false},
	}
	for _, tc := range cases {
		if got := isValidIPv4(tc.in); got != tc.want {
			t.Errorf("isValidIPv4(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseMacNetworkOutputs(t *testing.T) {
	routeOut := `
   route to: default
destination: default
       mask: default
    gateway: 172.20.10.1
  interface: en0
`
	if got := parseRouteField(routeOut, "gateway"); got != "172.20.10.1" {
		t.Fatalf("gateway = %q", got)
	}
	if got := parseRouteField(routeOut, "interface"); got != "en0" {
		t.Fatalf("interface = %q", got)
	}

	portsOut := `
Hardware Port: Thunderbolt Bridge
Device: bridge0
Ethernet Address: 00:11:22:33:44:55

Hardware Port: Wi-Fi
Device: en0
Ethernet Address: aa:bb:cc:dd:ee:ff
`
	if got := parseHardwarePort(portsOut, "en0"); got != "Wi-Fi" {
		t.Fatalf("port = %q", got)
	}

	infoOut := `
DHCP Configuration
IP address: 172.20.10.4
Subnet mask: 255.255.255.240
Router: 172.20.10.1
`
	if got := parseRouterField(infoOut); got != "172.20.10.1" {
		t.Fatalf("router = %q", got)
	}
	if got := parseRouterField("Router: none\n"); got != "" {
		t.Fatalf("expected empty router for none, got %q", got)
	}
}

func TestBuildSingBoxConfig(t *testing.T) {
	raw, err := buildSingBoxConfig("172.20.10.1", 8080, "socks5")
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"172.20.10.1"`) || !strings.Contains(s, `8080`) || !strings.Contains(s, `"tun-in"`) || !strings.Contains(s, `"type": "socks"`) {
		t.Fatalf("unexpected socks5 sing-box config:\n%s", s)
	}

	rawHTTP, err := buildSingBoxConfig("172.20.10.1", 8080, "http")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawHTTP), `"type": "http"`) {
		t.Fatalf("expected http outbound in sing-box config:\n%s", string(rawHTTP))
	}
}

func TestEmbeddedUIAndAPI(t *testing.T) {
	if !strings.Contains(string(uiHTML), "ShareNet") || !strings.Contains(string(uiHTML), "github.com/TPpixel/ShareNet") {
		t.Fatal("uiHTML missing expected branding/links")
	}
	b := NewBackend("")
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	writeJSON(rec, StatusResponse{
		HasSingBox: b.HasSingBox(),
		Service:    "Wi-Fi",
		Gateway:    "172.20.10.1",
		Logs:       []string{"ok"},
	})
	_ = req
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"gateway":"172.20.10.1"`) {
		t.Fatalf("unexpected api response: %d %s", rec.Code, rec.Body.String())
	}

	tok := generateSessionToken()
	if len(tok) != 32 {
		t.Fatalf("expected 32-char hex token, got %d", len(tok))
	}
	validReq := httptest.NewRequest(http.MethodPost, "/api/status", nil)
	validReq.Header.Set("Origin", "http://127.0.0.1:18080")
	validReq.Header.Set("X-Session-Token", tok)
	if !isAllowedRequest(validReq, tok) {
		t.Fatal("expected valid loopback + token request to pass")
	}

	noTokReq := httptest.NewRequest(http.MethodPost, "/api/status", nil)
	noTokReq.Header.Set("Origin", "http://127.0.0.1:18080")
	if isAllowedRequest(noTokReq, tok) {
		t.Fatal("expected missing token to be rejected")
	}

	evilOriginReq := httptest.NewRequest(http.MethodPost, "/api/status", nil)
	evilOriginReq.Header.Set("Origin", "https://evil.example")
	evilOriginReq.Header.Set("X-Session-Token", tok)
	if isAllowedRequest(evilOriginReq, tok) {
		t.Fatal("expected foreign Origin to be rejected")
	}
}

func TestDNSAndProxyParsers(t *testing.T) {
	if got := parseDNSServers("There aren't any DNS Servers set on Wi-Fi.\n"); len(got) != 0 {
		t.Fatalf("expected empty DNS list, got %v", got)
	}
	gotDNS := parseDNSServers("172.19.0.2\n8.8.8.8\n")
	if len(gotDNS) != 2 || gotDNS[0] != "172.19.0.2" || gotDNS[1] != "8.8.8.8" {
		t.Fatalf("unexpected parsed DNS: %v", gotDNS)
	}

	bkp := sanitizeBackupDNS([]string{"172.19.0.2", "1.1.1.1", "8.8.8.8"})
	if len(bkp) != 2 || bkp[0] != "1.1.1.1" || bkp[1] != "8.8.8.8" {
		t.Fatalf("unexpected sanitized backup DNS: %v", bkp)
	}
	bkpEmpty := sanitizeBackupDNS([]string{"172.19.0.2"})
	if len(bkpEmpty) != 1 || bkpEmpty[0] != "Empty" {
		t.Fatalf("expected Empty fallback, got %v", bkpEmpty)
	}

	svcs := parseNetworkServices("An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n*Thunderbolt Bridge\niPhone USB\n")
	if len(svcs) != 2 || svcs[0] != "Wi-Fi" || svcs[1] != "iPhone USB" {
		t.Fatalf("unexpected network services: %v", svcs)
	}

	if !parseSocksProxyEnabled("Enabled: Yes\nServer: 10.72.31.3\nPort: 8080\n") {
		t.Fatal("expected Enabled: Yes to be true")
	}
	if parseSocksProxyEnabled("Enabled: No\nServer: 10.72.31.3\nPort: 8080\n") {
		t.Fatal("expected Enabled: No to be false")
	}

	b := NewBackend("")
	cmd := b.buildRestoreDNSShellCmd([]string{"Wi-Fi", "iPhone USB"}, "Wi-Fi", []string{"1.1.1.1", "8.8.8.8"})
	if !strings.Contains(cmd, "networksetup -setdnsservers 'Wi-Fi' '1.1.1.1' '8.8.8.8'") {
		t.Fatalf("expected primary service DNS restore in cmd, got: %s", cmd)
	}
	if !strings.Contains(cmd, "networksetup -setdnsservers 'iPhone USB' 'Empty'") {
		t.Fatalf("expected secondary service Empty restore in cmd, got: %s", cmd)
	}
	if !strings.Contains(cmd, singBoxPgrepPattern) {
		t.Fatalf("expected safe pgrep pattern %q in cmd: %s", singBoxPgrepPattern, cmd)
	}

	code, detail := parseCurlCheckOutput("curl: (56) Recv failure: Connection reset by peer\nCODE:000\n")
	if code != "000" || !strings.Contains(detail, "Connection reset by peer") {
		t.Fatalf("unexpected parseCurlCheckOutput result: code=%q detail=%q", code, detail)
	}
}

func TestDetectProxyProtocol(t *testing.T) {
	// Mock HTTP CONNECT proxy that closes immediately on SOCKS5 greeting
	lnHTTP, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lnHTTP.Close()
	go func() {
		for {
			c, err := lnHTTP.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buf := make([]byte, 256)
				n, _ := conn.Read(buf)
				if n > 0 && buf[0] == 'C' {
					_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				}
			}(c)
		}
	}()
	_, portStr, _ := net.SplitHostPort(lnHTTP.Addr().String())
	portHTTP, _ := strconv.Atoi(portStr)
	proto, diag := detectProxyProtocol("127.0.0.1", portHTTP)
	if proto != "http" || diag != "" {
		t.Fatalf("expected http with empty diag, got proto=%q diag=%q", proto, diag)
	}

	// Mock SOCKS5 proxy
	lnSocks, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lnSocks.Close()
	go func() {
		for {
			c, err := lnSocks.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buf := make([]byte, 64)
				n, err := conn.Read(buf)
				if err != nil || n < 2 || buf[0] != 0x05 {
					return
				}
				_, _ = conn.Write([]byte{0x05, 0x00})
				n, err = conn.Read(buf)
				if err == nil && n >= 4 && buf[0] == 0x05 && buf[1] == 0x01 {
					_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
				}
			}(c)
		}
	}()
	_, portStr2, _ := net.SplitHostPort(lnSocks.Addr().String())
	portSocks, _ := strconv.Atoi(portStr2)
	proto, diag = detectProxyProtocol("127.0.0.1", portSocks)
	if proto != "socks5" || diag != "" {
		t.Fatalf("expected socks5 with empty diag, got proto=%q diag=%q", proto, diag)
	}
}

func TestSingBoxLogFormatterAndDedupe(t *testing.T) {
	noise := "+0300 2026-11-15 16:01:08 \x1b[36mINFO\x1b[0m [\x1b[38;5;103m2024452183\x1b[0m 0ms] inbound/tun[tun-in]: inbound DNS packet from 172.19.0.1:62517"
	if _, _, ok := formatSingBoxLogLine(noise); ok {
		t.Fatalf("expected noisy inbound DNS INFO line to be filtered out")
	}

	err1 := "+0300 2026-11-15 16:01:07 \x1b[31mERROR\x1b[0m [\x1b[38;5;215m3722159303\x1b[0m 21ms] connection: open connection to 149.154.167.41:80 using outbound/socks[proxy-out]: read tcp 10.72.31.67:56759->10.72.31.3:8080: read: connection reset by peer"
	err2 := "+0300 2026-11-15 16:01:07 \x1b[31mERROR\x1b[0m [\x1b[38;5;153m3094306697\x1b[0m 25ms] connection: open connection to 149.154.167.50:443 using outbound/socks[proxy-out]: read tcp 10.72.31.67:56760->10.72.31.3:8080: read: connection reset by peer"
	msg1, key1, ok1 := formatSingBoxLogLine(err1)
	_, key2, ok2 := formatSingBoxLogLine(err2)
	if !ok1 || !ok2 {
		t.Fatalf("expected error lines to be kept")
	}
	if strings.Contains(msg1, "\x1b[") || strings.Contains(msg1, "3722159303") {
		t.Fatalf("expected clean formatted message without ANSI/ID, got: %q", msg1)
	}
	if key1 != key2 {
		t.Fatalf("expected matching dedupe keys for repeated reset errors:\n%q\n%q", key1, key2)
	}
}
