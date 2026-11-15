package socks5

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestIsForbiddenTarget(t *testing.T) {
	forbidden := []string{
		"127.0.0.1",
		"10.0.0.1",
		"172.16.5.1",
		"192.168.43.1",
		"169.254.169.254",
		"100.64.0.1",
		"224.0.0.1",
		"::1",
		"fc00::1",
	}
	for _, s := range forbidden {
		if !IsForbiddenTarget(net.ParseIP(s)) {
			t.Errorf("expected %s to be forbidden", s)
		}
	}
	if !IsForbiddenTarget(nil) {
		t.Error("expected nil IP to be forbidden")
	}

	allowed := []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700:4700::1111"}
	for _, s := range allowed {
		if IsForbiddenTarget(net.ParseIP(s)) {
			t.Errorf("expected %s to be allowed", s)
		}
	}
}

func TestHandleClientRejectsLoopbackSSRF(t *testing.T) {
	srv := New(0)
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	go srv.handleClient(serverConn)

	// Method greeting: VER=5, NMETHODS=1, NOAUTH=0
	if _, err := clientConn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	var methodResp [2]byte
	if _, err := io.ReadFull(clientConn, methodResp[:]); err != nil {
		t.Fatal(err)
	}
	if methodResp != [2]byte{0x05, 0x00} {
		t.Fatalf("unexpected method reply: %v", methodResp)
	}

	// CONNECT 127.0.0.1:8080 -> must be denied with 0x02 (connection not allowed by ruleset)
	req := []byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0x1F, 0x90}
	if _, err := clientConn.Write(req); err != nil {
		t.Fatal(err)
	}
	var reply [10]byte
	if _, err := io.ReadFull(clientConn, reply[:]); err != nil {
		t.Fatal(err)
	}
	if reply[0] != 0x05 || reply[1] != 0x02 {
		t.Fatalf("expected SSRF rejection 0x05 0x02, got %v", reply)
	}
}

func TestStartAndStopOnEphemeralPort(t *testing.T) {
	srv := New(0)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop()

	addr := srv.listener.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	var resp [2]byte
	if _, err := io.ReadFull(conn, resp[:]); err != nil {
		t.Fatal(err)
	}
	if resp != [2]byte{0x05, 0x00} {
		t.Fatalf("unexpected greeting reply: %v", resp)
	}
}

func TestResolveHostsSkipsForbiddenMixedWithPublicInFilter(t *testing.T) {
	srv := New(0)
	srv.cacheMu.Lock()
	srv.cache["mixed.example"] = dnsEntry{
		ips:       []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("93.184.216.34"), net.ParseIP("1.1.1.1")},
		expiresAt: time.Now().Add(time.Minute),
	}
	srv.cacheMu.Unlock()

	ips := srv.resolveHosts("mixed.example")
	if len(ips) != 3 {
		t.Fatalf("expected 3 cached IPs, got %d", len(ips))
	}
	var allowed []net.IP
	for _, ip := range ips {
		if !IsForbiddenTarget(ip) {
			allowed = append(allowed, ip)
		}
	}
	if len(allowed) != 2 || !allowed[0].Equal(net.ParseIP("93.184.216.34")) {
		t.Fatalf("unexpected filtered candidates: %v", allowed)
	}
}
