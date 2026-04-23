package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"mc-proxy/internal/config"
	"mc-proxy/internal/protocol"
)

type tcpProxy struct {
	cfg      config.ProxyConfig
	logger   *log.Logger
	listener net.Listener
	pool     sync.Pool
	wg       sync.WaitGroup
	closeMux sync.Once
}

func newTCPProxy(cfg config.ProxyConfig, logger *log.Logger) *tcpProxy {
	bufferSize := cfg.ReadBufferSize
	if cfg.WriteBufferSize > bufferSize {
		bufferSize = cfg.WriteBufferSize
	}
	if bufferSize <= 0 {
		bufferSize = 32 * 1024
	}

	return &tcpProxy{
		cfg:    cfg,
		logger: logger,
		pool: sync.Pool{
			New: func() any {
				return make([]byte, bufferSize)
			},
		},
	}
}

func (p *tcpProxy) Name() string {
	return p.cfg.Name
}

func (p *tcpProxy) Start(ctx context.Context) error {
	ln, err := net.Listen(p.cfg.ListenNet, p.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}
	p.listener = ln

	p.logger.Printf("proxy=%s net=%s listen=%s backend=%s rule=%s", p.cfg.Name, p.cfg.ListenNet, ln.Addr().String(), p.cfg.BackendAddr, p.cfg.Rule)

	p.wg.Add(1)
	go p.acceptLoop(ctx)

	return nil
}

func (p *tcpProxy) acceptLoop(ctx context.Context) {
	defer p.wg.Done()

	for {
		conn, err := p.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			var nerr net.Error
			if errors.As(err, &nerr) && nerr.Timeout() {
				continue
			}

			if ctx.Err() != nil {
				return
			}
			p.logger.Printf("proxy=%s accept error: %v", p.cfg.Name, err)
			continue
		}

		p.wg.Add(1)
		go func(client net.Conn) {
			defer p.wg.Done()
			p.handleConn(ctx, client)
		}(conn)
	}
}

func (p *tcpProxy) handleConn(ctx context.Context, client net.Conn) {
	defer client.Close()

	dialNet := config.BackendDialNet(p.cfg.ListenNet)
	dialer := net.Dialer{Timeout: p.cfg.ConnectTimeout.Duration}
	backend, err := dialer.DialContext(ctx, dialNet, p.cfg.BackendAddr)
	if err != nil {
		p.logger.Printf("proxy=%s backend connect failed net=%s addr=%s err=%v", p.cfg.Name, dialNet, p.cfg.BackendAddr, err)
		return
	}
	defer backend.Close()

	if p.cfg.Rule == config.RuleProxyProtocol {
		if err := protocol.WriteHeader(backend, client.RemoteAddr(), client.LocalAddr(), p.cfg.ProxyVersion, false); err != nil {
			p.logger.Printf("proxy=%s write PROXY header failed: %v", p.cfg.Name, err)
			return
		}
	}

	p.copyBidirectional(client, backend)
}

func (p *tcpProxy) copyBidirectional(client, backend net.Conn) {
	done := make(chan struct{}, 2)

	go p.pipe(backend, client, done)
	go p.pipe(client, backend, done)

	<-done
	_ = client.SetDeadline(time.Now())
	_ = backend.SetDeadline(time.Now())
	<-done
}

func (p *tcpProxy) pipe(dst, src net.Conn, done chan<- struct{}) {
	buf := p.pool.Get().([]byte)
	defer p.pool.Put(buf)
	defer func() { done <- struct{}{} }()

	_, _ = io.CopyBuffer(dst, src, buf)

	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = dst.Close()
}

func (p *tcpProxy) Close() error {
	var closeErr error
	p.closeMux.Do(func() {
		if p.listener != nil {
			closeErr = p.listener.Close()
		}
		p.wg.Wait()
	})
	return closeErr
}
