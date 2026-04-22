package proxy

import (
	"context"
	"io"
	"log"
	"net"
	"testing"
	"time"

	"mc-proxy/internal/config"
)

func TestUDPSessionCreateRefreshExpire(t *testing.T) {
	backend, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer backend.Close()

	cfg := config.ProxyConfig{
		Name:              "udp-session-test",
		ListenNet:         "udp4",
		ListenAddr:        "127.0.0.1:0",
		BackendAddr:       backend.LocalAddr().String(),
		Rule:              config.RulePassthrough,
		UDPSessionTimeout: config.Duration{Duration: 100 * time.Millisecond},
		ReadBufferSize:    1024,
		WriteBufferSize:   1024,
		ConnectTimeout:    config.Duration{Duration: time.Second},
	}

	p := newUDPProxy(cfg, log.New(io.Discard, "", 0))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer p.Close()

	clientAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 35000}
	s1, err := p.getOrCreateSession(ctx, clientAddr)
	if err != nil {
		t.Fatalf("getOrCreateSession() error = %v", err)
	}

	s2, err := p.getOrCreateSession(ctx, clientAddr)
	if err != nil {
		t.Fatalf("getOrCreateSession() second call error = %v", err)
	}
	if s1 != s2 {
		t.Fatal("expected session reuse for same client")
	}

	s1.lastActive.Store(time.Now().Add(-1 * time.Second).UnixNano())
	expired := p.expireIdleSessions(time.Now())
	if expired != 1 {
		t.Fatalf("expireIdleSessions() = %d, want 1", expired)
	}

	p.sessionsMux.RLock()
	_, ok := p.sessions[clientAddr.String()]
	p.sessionsMux.RUnlock()
	if ok {
		t.Fatal("session should be removed after expiration")
	}
}
