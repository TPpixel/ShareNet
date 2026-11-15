package socks5

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

const (
	MaxConcurrent    = 96
	HandshakeTimeout = 15 * time.Second
	IdleTimeout      = 120 * time.Second
	DnsCacheTTL      = 60 * time.Second
)

type dnsEntry struct {
	ips       []net.IP
	expiresAt time.Time
}

// Server is a hardened local SOCKS5 TCP server on 127.0.0.1 with Cloudflare DoH (1.1.1.1) resolution
// and SSRF protection, exposed to Android over `adb reverse tcp:10808 tcp:10808`.
type Server struct {
	port     int
	listener net.Listener
	sem      chan struct{}
	cacheMu  sync.RWMutex
	cache    map[string]dnsEntry
	httpCli  *http.Client
	running  atomic.Bool
	mu       sync.Mutex
}

// New creates a SOCKS5 server bound to 127.0.0.1:<port>.
func New(port int) *Server {
	return &Server{
		port:  port,
		sem:   make(chan struct{}, MaxConcurrent),
		cache: make(map[string]dnsEntry),
		httpCli: &http.Client{
			Timeout: 2 * time.Second,
		},
	}
}

// Start begins listening on 127.0.0.1:<port>.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running.Load() {
		return nil
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return err
	}
	s.listener = ln
	s.running.Store(true)
	go s.acceptLoop(ln)
	return nil
}

// Stop shuts down the SOCKS5 listener.
func (s *Server) Stop() {
	s.mu.Lock()
	ln := s.listener
	s.listener = nil
	s.running.Store(false)
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
}

func (s *Server) acceptLoop(ln net.Listener) {
	for s.running.Load() {
		conn, err := ln.Accept()
		if err != nil {
			if !s.running.Load() {
				return
			}
			time.Sleep(30 * time.Millisecond)
			continue
		}
		select {
		case s.sem <- struct{}{}:
			go func(c net.Conn) {
				defer func() { <-s.sem }()
				s.handleClient(c)
			}(conn)
		default:
			_ = conn.Close()
		}
	}
}

func (s *Server) handleClient(client net.Conn) {
	defer client.Close()
	if tc, ok := client.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	_ = client.SetDeadline(time.Now().Add(HandshakeTimeout))

	var hdr [2]byte
	if _, err := io.ReadFull(client, hdr[:]); err != nil {
		return
	}
	ver, nMethods := hdr[0], int(hdr[1])
	if ver != 0x05 || nMethods <= 0 || nMethods > 64 {
		return
	}
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(client, methods); err != nil {
		return
	}
	noAuth := false
	for _, m := range methods {
		if m == 0x00 {
			noAuth = true
			break
		}
	}
	if !noAuth {
		_, _ = client.Write([]byte{0x05, 0xFF})
		return
	}
	if _, err := client.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	var reqHdr [4]byte
	if _, err := io.ReadFull(client, reqHdr[:]); err != nil {
		return
	}
	if reqHdr[0] != 0x05 || reqHdr[1] != 0x01 {
		_, _ = client.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	var targetHost string
	var directIP net.IP
	switch reqHdr[3] {
	case 0x01:
		var ip4 [4]byte
		if _, err := io.ReadFull(client, ip4[:]); err != nil {
			return
		}
		directIP = net.IP(ip4[:])
		targetHost = directIP.String()
	case 0x03:
		var l [1]byte
		if _, err := io.ReadFull(client, l[:]); err != nil || l[0] == 0 {
			return
		}
		dom := make([]byte, int(l[0]))
		if _, err := io.ReadFull(client, dom); err != nil {
			return
		}
		targetHost = string(dom)
	case 0x04:
		var ip6 [16]byte
		if _, err := io.ReadFull(client, ip6[:]); err != nil {
			return
		}
		directIP = net.IP(ip6[:])
		targetHost = directIP.String()
	default:
		_, _ = client.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	var portBuf [2]byte
	if _, err := io.ReadFull(client, portBuf[:]); err != nil {
		return
	}
	targetPort := int(binary.BigEndian.Uint16(portBuf[:]))
	if targetPort < 1 || targetPort > 65535 {
		return
	}

	var candidates []net.IP
	if directIP != nil {
		candidates = []net.IP{directIP}
	} else {
		candidates = s.resolveHosts(targetHost)
	}
	if len(candidates) == 0 {
		_, _ = client.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	var allowed []net.IP
	sawForbidden := false
	for _, ip := range candidates {
		if IsForbiddenTarget(ip) {
			sawForbidden = true
			continue
		}
		allowed = append(allowed, ip)
	}
	if len(allowed) == 0 {
		if sawForbidden {
			_, _ = client.Write([]byte{0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		} else {
			_, _ = client.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		}
		return
	}

	maxAttempts := len(allowed)
	if maxAttempts > 4 {
		maxAttempts = 4
	}
	var remote net.Conn
	var dialErr error
	for i := 0; i < maxAttempts; i++ {
		timeout := HandshakeTimeout
		if maxAttempts > 1 {
			if i == maxAttempts-1 {
				timeout = 6 * time.Second
			} else {
				timeout = 3500 * time.Millisecond
			}
		}
		dialer := &net.Dialer{Timeout: timeout}
		targetAddr := net.JoinHostPort(allowed[i].String(), fmt.Sprintf("%d", targetPort))
		remote, dialErr = dialer.Dial("tcp", targetAddr)
		if dialErr == nil {
			break
		}
	}
	if dialErr != nil || remote == nil {
		_, _ = client.Write([]byte{0x05, 0x06, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()
	if tc, ok := remote.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}

	// Clear handshake deadline; bidirectional idle deadlines handled per I/O pump.
	_ = client.SetDeadline(time.Time{})

	if _, err := client.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		copyWithIdle(remote, client, IdleTimeout)
		if tc, ok := remote.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	copyWithIdle(client, remote, IdleTimeout)
	if tc, ok := client.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}
	wg.Wait()
}

func copyWithIdle(dst, src net.Conn, idle time.Duration) {
	buf := make([]byte, 16384)
	for {
		_ = src.SetReadDeadline(time.Now().Add(idle))
		n, err := src.Read(buf)
		if n > 0 {
			_ = dst.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// IsForbiddenTarget blocks loopback, private, link-local, CGNAT, documentation, and multicast IPs (SSRF guard).
func IsForbiddenTarget(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		b0, b1, b2 := ip4[0], ip4[1], ip4[2]
		if b0 == 0 || b0 == 10 || b0 == 127 {
			return true
		}
		if b0 == 100 && b1 >= 64 && b1 <= 127 {
			return true
		}
		if b0 == 169 && b1 == 254 {
			return true
		}
		if b0 == 172 && b1 >= 16 && b1 <= 31 {
			return true
		}
		if b0 == 192 && b1 == 0 && (b2 == 0 || b2 == 2) {
			return true
		}
		if b0 == 192 && b1 == 168 {
			return true
		}
		if b0 == 198 && (b1 == 18 || b1 == 19) {
			return true
		}
		if b0 == 198 && b1 == 51 && b2 == 100 {
			return true
		}
		if b0 == 203 && b1 == 0 && b2 == 113 {
			return true
		}
		if b0 >= 224 {
			return true
		}
		return false
	}

	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if len(ip) == net.IPv6len && (ip[0]&0xFE) == 0xFC {
		return true
	}
	return false
}

func (s *Server) resolveHosts(host string) []net.IP {
	if parsed := net.ParseIP(host); parsed != nil {
		return []net.IP{parsed}
	}
	now := time.Now()
	s.cacheMu.RLock()
	if ent, ok := s.cache[host]; ok && ent.expiresAt.After(now) && len(ent.ips) > 0 {
		out := append([]net.IP(nil), ent.ips...)
		s.cacheMu.RUnlock()
		return out
	}
	s.cacheMu.RUnlock()

	if ips := s.queryCloudflareDohAll(host); len(ips) > 0 {
		s.cacheMu.Lock()
		s.cache[host] = dnsEntry{ips: ips, expiresAt: now.Add(DnsCacheTTL)}
		s.cacheMu.Unlock()
		return ips
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err == nil && len(addrs) > 0 {
		s.cacheMu.Lock()
		s.cache[host] = dnsEntry{ips: addrs, expiresAt: now.Add(DnsCacheTTL)}
		s.cacheMu.Unlock()
		return addrs
	}
	addrs, err = net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err == nil && len(addrs) > 0 {
		return addrs
	}
	return nil
}

func (s *Server) queryCloudflareDohAll(host string) []net.IP {
	u := "https://1.1.1.1/dns-query?name=" + url.QueryEscape(host) + "&type=A"
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := s.httpCli.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var payload struct {
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&payload); err != nil {
		return nil
	}
	var out []net.IP
	seen := make(map[string]struct{})
	for _, ans := range payload.Answer {
		if ans.Type == 1 {
			if ip := net.ParseIP(ans.Data).To4(); ip != nil {
				key := ip.String()
				if _, exists := seen[key]; !exists {
					seen[key] = struct{}{}
					out = append(out, ip)
					if len(out) >= 6 {
						break
					}
				}
			}
		}
	}
	return out
}

// ValidateTargetErr is used by unit tests to inspect resolution/SSRF errors.
var ErrForbiddenTarget = errors.New("forbidden target IP")
