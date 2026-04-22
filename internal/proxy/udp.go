package proxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"mc-proxy/internal/config"
	"mc-proxy/internal/protocol"
)

type udpSession struct {
	backendConn *net.UDPConn
	clientAddr  *net.UDPAddr
	lastActive  atomic.Int64
	closeMux    sync.Once
}

func newUDPSession(backendConn *net.UDPConn, clientAddr *net.UDPAddr) *udpSession {
	s := &udpSession{backendConn: backendConn, clientAddr: clientAddr}
	s.touch()
	return s
}

func (s *udpSession) touch() {
	s.lastActive.Store(time.Now().UnixNano())
}

func (s *udpSession) lastSeen() time.Time {
	return time.Unix(0, s.lastActive.Load())
}

func (s *udpSession) Close() error {
	var err error
	s.closeMux.Do(func() {
		err = s.backendConn.Close()
	})
	return err
}

type udpProxy struct {
	cfg         config.ProxyConfig
	logger      *log.Logger
	listener    *net.UDPConn
	runCancel   context.CancelFunc
	sessions    map[string]*udpSession
	sessionsMux sync.RWMutex
	wg          sync.WaitGroup
	closeMux    sync.Once
}

func newUDPProxy(cfg config.ProxyConfig, logger *log.Logger) *udpProxy {
	return &udpProxy{
		cfg:      cfg,
		logger:   logger,
		sessions: make(map[string]*udpSession),
	}
}

func (p *udpProxy) Name() string {
	return p.cfg.Name
}

func (p *udpProxy) Start(ctx context.Context) error {
	runCtx, runCancel := context.WithCancel(ctx)
	p.runCancel = runCancel

	listenAddr, err := net.ResolveUDPAddr(p.cfg.ListenNet, p.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("resolve listen address failed: %w", err)
	}

	conn, err := net.ListenUDP(p.cfg.ListenNet, listenAddr)
	if err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}
	_ = conn.SetReadBuffer(p.cfg.ReadBufferSize)
	_ = conn.SetWriteBuffer(p.cfg.WriteBufferSize)
	p.listener = conn

	p.logger.Printf("proxy=%s net=%s listen=%s backend=%s rule=%s", p.cfg.Name, p.cfg.ListenNet, conn.LocalAddr().String(), p.cfg.BackendAddr, p.cfg.Rule)

	p.wg.Add(2)
	go p.readLoop(runCtx)
	go p.cleanupLoop(runCtx)

	return nil
}

func (p *udpProxy) readLoop(ctx context.Context) {
	defer p.wg.Done()

	buffer := make([]byte, p.cfg.ReadBufferSize)
	for {
		n, clientAddr, err := p.listener.ReadFromUDP(buffer)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			p.logger.Printf("proxy=%s udp read error: %v", p.cfg.Name, err)
			continue
		}

		payload := make([]byte, n)
		copy(payload, buffer[:n])
		if err := p.handlePacket(ctx, clientAddr, payload); err != nil {
			p.logger.Printf("proxy=%s packet handling error: %v", p.cfg.Name, err)
		}
	}
}

func (p *udpProxy) handlePacket(ctx context.Context, clientAddr *net.UDPAddr, payload []byte) error {
	session, err := p.getOrCreateSession(ctx, clientAddr)
	if err != nil {
		return err
	}
	session.touch()

	out := payload
	if p.cfg.Rule == config.RuleProxyProtocol {
		header, err := protocol.BuildV2(clientAddr, p.listener.LocalAddr(), true)
		if err != nil {
			return fmt.Errorf("build PROXY v2 header failed: %w", err)
		}

		out = make([]byte, len(header)+len(payload))
		copy(out, header)
		copy(out[len(header):], payload)
	}

	if _, err := session.backendConn.Write(out); err != nil {
		p.removeSession(clientAddr.String(), session)
		return fmt.Errorf("write backend failed: %w", err)
	}

	return nil
}

func (p *udpProxy) getOrCreateSession(ctx context.Context, clientAddr *net.UDPAddr) (*udpSession, error) {
	key := clientAddr.String()

	p.sessionsMux.RLock()
	existing := p.sessions[key]
	p.sessionsMux.RUnlock()
	if existing != nil {
		existing.touch()
		return existing, nil
	}

	p.sessionsMux.Lock()
	defer p.sessionsMux.Unlock()

	if existing = p.sessions[key]; existing != nil {
		existing.touch()
		return existing, nil
	}

	dialer := net.Dialer{Timeout: p.cfg.ConnectTimeout.Duration}
	backendConn, err := dialer.DialContext(ctx, p.cfg.ListenNet, p.cfg.BackendAddr)
	if err != nil {
		return nil, fmt.Errorf("dial backend failed: %w", err)
	}

	udpConn, ok := backendConn.(*net.UDPConn)
	if !ok {
		_ = backendConn.Close()
		return nil, fmt.Errorf("backend connection is %T, expected UDP", backendConn)
	}
	_ = udpConn.SetReadBuffer(p.cfg.ReadBufferSize)
	_ = udpConn.SetWriteBuffer(p.cfg.WriteBufferSize)

	session := newUDPSession(udpConn, clientAddr)
	p.sessions[key] = session

	p.wg.Add(1)
	go p.backendToClientLoop(session, key)

	return session, nil
}

func (p *udpProxy) backendToClientLoop(session *udpSession, key string) {
	defer p.wg.Done()

	buffer := make([]byte, p.cfg.WriteBufferSize)
	for {
		n, err := session.backendConn.Read(buffer)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				p.logger.Printf("proxy=%s backend read error: %v", p.cfg.Name, err)
			}
			p.removeSession(key, session)
			return
		}

		session.touch()
		if _, err := p.listener.WriteToUDP(buffer[:n], session.clientAddr); err != nil {
			p.logger.Printf("proxy=%s write client failed: %v", p.cfg.Name, err)
			p.removeSession(key, session)
			return
		}
	}
}

func (p *udpProxy) cleanupLoop(ctx context.Context) {
	defer p.wg.Done()

	interval := p.cfg.UDPSessionTimeout.Duration / 2
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			expired := p.expireIdleSessions(now)
			if expired > 0 {
				p.logger.Printf("proxy=%s cleaned %d idle UDP session(s)", p.cfg.Name, expired)
			}
		}
	}
}

func (p *udpProxy) expireIdleSessions(now time.Time) int {
	var stale []*udpSession

	p.sessionsMux.Lock()
	for key, session := range p.sessions {
		if now.Sub(session.lastSeen()) > p.cfg.UDPSessionTimeout.Duration {
			delete(p.sessions, key)
			stale = append(stale, session)
		}
	}
	p.sessionsMux.Unlock()

	for _, session := range stale {
		_ = session.Close()
	}
	return len(stale)
}

func (p *udpProxy) removeSession(key string, target *udpSession) {
	p.sessionsMux.Lock()
	existing := p.sessions[key]
	if existing == target {
		delete(p.sessions, key)
	}
	p.sessionsMux.Unlock()

	_ = target.Close()
}

func (p *udpProxy) Close() error {
	var closeErr error

	p.closeMux.Do(func() {
		if p.runCancel != nil {
			p.runCancel()
		}

		if p.listener != nil {
			closeErr = p.listener.Close()
		}

		p.sessionsMux.Lock()
		sessions := make([]*udpSession, 0, len(p.sessions))
		for key, session := range p.sessions {
			sessions = append(sessions, session)
			delete(p.sessions, key)
		}
		p.sessionsMux.Unlock()

		for _, session := range sessions {
			_ = session.Close()
		}

		p.wg.Wait()
	})

	return closeErr
}
