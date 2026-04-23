package proxy

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"strings"
	"testing"
	"time"

	"mc-proxy/internal/config"
)

func TestTCPPassthroughIntegration(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("backend listen failed: %v", err)
	}
	defer backend.Close()

	go func() {
		conn, err := backend.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()

	cfg := config.ProxyConfig{
		Name:            "tcp-pass",
		ListenNet:       "tcp",
		ListenAddr:      "127.0.0.1:0",
		BackendAddr:     backend.Addr().String(),
		Rule:            config.RulePassthrough,
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		ConnectTimeout:  config.Duration{Duration: time.Second},
	}

	p := newTCPProxy(cfg, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("proxy start failed: %v", err)
	}
	defer p.Close()

	client, err := net.Dial("tcp", p.listener.Addr().String())
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}
	defer client.Close()

	if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set deadline failed: %v", err)
	}

	payload := []byte("minecraft")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("client write failed: %v", err)
	}

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatalf("client read failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo mismatch: got=%q want=%q", string(got), string(payload))
	}
}

func TestTCPProxyProtocolV1Integration(t *testing.T) {
	headerCh := make(chan string, 1)

	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("backend listen failed: %v", err)
	}
	defer backend.Close()

	go func() {
		conn, err := backend.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

		reader := bufio.NewReader(conn)
		header, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		headerCh <- header

		payload := make([]byte, 4)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return
		}
		_, _ = conn.Write(payload)
	}()

	cfg := config.ProxyConfig{
		Name:            "tcp-proxy-v1",
		ListenNet:       "tcp",
		ListenAddr:      "127.0.0.1:0",
		BackendAddr:     backend.Addr().String(),
		Rule:            config.RuleProxyProtocol,
		ProxyVersion:    1,
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		ConnectTimeout:  config.Duration{Duration: time.Second},
	}

	p := newTCPProxy(cfg, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("proxy start failed: %v", err)
	}
	defer p.Close()

	client, err := net.Dial("tcp", p.listener.Addr().String())
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}
	defer client.Close()

	if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set deadline failed: %v", err)
	}

	payload := []byte("ping")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("client write failed: %v", err)
	}

	response := make([]byte, len(payload))
	if _, err := io.ReadFull(client, response); err != nil {
		t.Fatalf("client read failed: %v", err)
	}
	if !bytes.Equal(response, payload) {
		t.Fatalf("response mismatch: got=%q want=%q", string(response), string(payload))
	}

	select {
	case header := <-headerCh:
		if !strings.HasPrefix(header, "PROXY TCP4 ") {
			t.Fatalf("unexpected PROXY header: %q", header)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive PROXY header on backend")
	}
}

func TestTCPProxyProtocolKeepsExistingHeader(t *testing.T) {
	headerCh := make(chan string, 1)
	payloadCh := make(chan []byte, 1)

	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("backend listen failed: %v", err)
	}
	defer backend.Close()

	go func() {
		conn, err := backend.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

		reader := bufio.NewReader(conn)
		header, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		headerCh <- header

		payload := make([]byte, 4)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return
		}
		payloadCh <- append([]byte(nil), payload...)
		_, _ = conn.Write(payload)
	}()

	cfg := config.ProxyConfig{
		Name:            "tcp-proxy-keep-existing-header",
		ListenNet:       "tcp",
		ListenAddr:      "127.0.0.1:0",
		BackendAddr:     backend.Addr().String(),
		Rule:            config.RuleProxyProtocol,
		ProxyVersion:    1,
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		ConnectTimeout:  config.Duration{Duration: time.Second},
	}

	p := newTCPProxy(cfg, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("proxy start failed: %v", err)
	}
	defer p.Close()

	client, err := net.Dial("tcp", p.listener.Addr().String())
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}
	defer client.Close()

	if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set deadline failed: %v", err)
	}

	header := "PROXY TCP4 198.51.100.10 203.0.113.5 34567 25565\r\n"
	payload := []byte("ping")
	packet := append([]byte(header), payload...)
	if _, err := client.Write(packet); err != nil {
		t.Fatalf("client write failed: %v", err)
	}

	response := make([]byte, len(payload))
	if _, err := io.ReadFull(client, response); err != nil {
		t.Fatalf("client read failed: %v", err)
	}
	if !bytes.Equal(response, payload) {
		t.Fatalf("response mismatch: got=%q want=%q", string(response), string(payload))
	}

	select {
	case gotHeader := <-headerCh:
		if gotHeader != header {
			t.Fatalf("backend header mismatch: got=%q want=%q", gotHeader, header)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive header on backend")
	}

	select {
	case gotPayload := <-payloadCh:
		if !bytes.Equal(gotPayload, payload) {
			t.Fatalf("backend payload mismatch: got=%q want=%q", string(gotPayload), string(payload))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive payload on backend")
	}
}

func TestDetectProxyHeaderFragmentedPrefix(t *testing.T) {
	t.Parallel()

	clientSide, upstreamSide := net.Pipe()
	defer clientSide.Close()
	defer upstreamSide.Close()

	header := "PROXY TCP4 198.51.100.10 203.0.113.5 34567 25565\r\n"
	payload := "ping"

	go func() {
		_, _ = upstreamSide.Write([]byte("PRO"))
		time.Sleep(proxyHeaderDetectTimeout + 150*time.Millisecond)
		_, _ = upstreamSide.Write([]byte("XY TCP4 198.51.100.10 203.0.113.5 34567 25565\r\n" + payload))
	}()

	p := newTCPProxy(config.ProxyConfig{ReadBufferSize: 1024, WriteBufferSize: 1024}, log.New(io.Discard, "", 0))
	hasHeader, reader, err := p.detectProxyHeader(clientSide)
	if err != nil {
		t.Fatalf("detectProxyHeader() error = %v", err)
	}
	if !hasHeader {
		t.Fatal("detectProxyHeader() should detect fragmented PROXY header")
	}

	gotHeader, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString() error = %v", err)
	}
	if gotHeader != header {
		t.Fatalf("header mismatch: got=%q want=%q", gotHeader, header)
	}

	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(reader, buf); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	if string(buf) != payload {
		t.Fatalf("payload mismatch: got=%q want=%q", string(buf), payload)
	}
}

func TestUDPIntegrationSessionReuseAndExpire(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("backend listen failed: %v", err)
	}
	defer backend.Close()

	stopBackend := make(chan struct{})
	go func() {
		buf := make([]byte, 2048)
		for {
			_ = backend.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			n, addr, err := backend.ReadFromUDP(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					select {
					case <-stopBackend:
						return
					default:
						continue
					}
				}
				return
			}
			_, _ = backend.WriteToUDP(buf[:n], addr)
		}
	}()
	defer close(stopBackend)

	cfg := config.ProxyConfig{
		Name:              "udp-pass",
		ListenNet:         "udp",
		ListenAddr:        "127.0.0.1:0",
		BackendAddr:       backend.LocalAddr().String(),
		Rule:              config.RulePassthrough,
		UDPSessionTimeout: config.Duration{Duration: 200 * time.Millisecond},
		ReadBufferSize:    2048,
		WriteBufferSize:   2048,
		ConnectTimeout:    config.Duration{Duration: time.Second},
	}

	p := newUDPProxy(cfg, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("proxy start failed: %v", err)
	}
	defer p.Close()

	proxyAddr := p.listener.LocalAddr().(*net.UDPAddr)
	client, err := net.DialUDP("udp", nil, proxyAddr)
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}
	defer client.Close()

	sendAndExpectEcho := func(data string) {
		t.Helper()
		if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("set client deadline failed: %v", err)
		}
		if _, err := client.Write([]byte(data)); err != nil {
			t.Fatalf("client write failed: %v", err)
		}
		buf := make([]byte, 64)
		n, err := client.Read(buf)
		if err != nil {
			t.Fatalf("client read failed: %v", err)
		}
		if string(buf[:n]) != data {
			t.Fatalf("echo mismatch: got=%q want=%q", string(buf[:n]), data)
		}
	}

	key := client.LocalAddr().String()
	sendAndExpectEcho("a")
	s1 := waitSession(t, p, key, 2*time.Second)

	sendAndExpectEcho("b")
	s2 := waitSession(t, p, key, 2*time.Second)
	if s1 != s2 {
		t.Fatal("expected reused UDP session for same client")
	}

	s1.lastActive.Store(time.Now().Add(-1 * time.Second).UnixNano())
	if expired := p.expireIdleSessions(time.Now()); expired != 1 {
		t.Fatalf("expireIdleSessions() = %d, want 1", expired)
	}

	waitForCondition(t, 2*time.Second, func() bool {
		p.sessionsMux.RLock()
		_, ok := p.sessions[key]
		p.sessionsMux.RUnlock()
		return !ok
	})

	sendAndExpectEcho("c")
	s3 := waitSession(t, p, key, 2*time.Second)
	if s3 == s1 {
		t.Fatal("expected a new UDP session after expiration")
	}
}

func waitSession(t *testing.T, p *udpProxy, key string, timeout time.Duration) *udpSession {
	t.Helper()

	var session *udpSession
	waitForCondition(t, timeout, func() bool {
		p.sessionsMux.RLock()
		session = p.sessions[key]
		p.sessionsMux.RUnlock()
		return session != nil
	})
	return session
}

func waitForCondition(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
