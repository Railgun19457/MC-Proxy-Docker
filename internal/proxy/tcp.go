package proxy

import (
	"bufio"
	"bytes"
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

const proxyHeaderDetectTimeout = time.Second

var proxyV2Signature = []byte{0x0d, 0x0a, 0x0d, 0x0a, 0x00, 0x0d, 0x0a, 0x51, 0x55, 0x49, 0x54, 0x0a}

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

	clientReader := io.Reader(client)

	if p.cfg.Rule == config.RuleProxyProtocol {
		hasHeader, bufferedReader, err := p.detectProxyHeader(client)
		if err != nil {
			p.logger.Printf("proxy=%s detect PROXY header failed: %v", p.cfg.Name, err)
			return
		}
		clientReader = bufferedReader

		if !hasHeader {
			if err := protocol.WriteHeader(backend, client.RemoteAddr(), client.LocalAddr(), p.cfg.ProxyVersion, false); err != nil {
				p.logger.Printf("proxy=%s write PROXY header failed: %v", p.cfg.Name, err)
				return
			}
		}
	}

	p.copyBidirectional(clientReader, client, backend)
}

func (p *tcpProxy) detectProxyHeader(client net.Conn) (bool, *bufio.Reader, error) {
	reader := bufio.NewReader(client)

	if err := client.SetReadDeadline(time.Now().Add(proxyHeaderDetectTimeout)); err != nil {
		return false, nil, fmt.Errorf("set detect deadline failed: %w", err)
	}

	hasHeader, err := detectProxyHeaderPrefix(reader)
	if resetErr := client.SetReadDeadline(time.Time{}); resetErr != nil {
		return false, nil, fmt.Errorf("clear detect deadline failed: %w", resetErr)
	}

	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, reader, nil
		}

		var nerr net.Error
		if errors.As(err, &nerr) && nerr.Timeout() {
			return false, reader, nil
		}

		return false, nil, err
	}

	return hasHeader, reader, nil
}

func detectProxyHeaderPrefix(reader *bufio.Reader) (bool, error) {
	first, err := reader.Peek(1)
	if err != nil {
		return false, err
	}

	switch first[0] {
	case 'P':
		return detectProxySignature(reader, []byte("PROXY"))
	case proxyV2Signature[0]:
		return detectProxySignature(reader, proxyV2Signature)
	default:
		return false, nil
	}
}

func detectProxySignature(reader *bufio.Reader, signature []byte) (bool, error) {
	buf, err := reader.Peek(len(signature))
	if err == nil {
		return bytes.Equal(buf, signature), nil
	}

	if !errors.Is(err, io.EOF) {
		var nerr net.Error
		if !errors.As(err, &nerr) || !nerr.Timeout() {
			return false, err
		}
	}

	buffered := reader.Buffered()
	if buffered == 0 {
		return false, nil
	}
	if buffered > len(signature) {
		buffered = len(signature)
	}

	partial, peekErr := reader.Peek(buffered)
	if peekErr != nil {
		return false, err
	}

	if bytes.HasPrefix(signature, partial) {
		return true, nil
	}

	return false, nil
}

func (p *tcpProxy) copyBidirectional(clientReader io.Reader, client, backend net.Conn) {
	done := make(chan struct{}, 2)

	go p.pipe(backend, clientReader, done)
	go p.pipe(client, backend, done)

	<-done
	_ = client.SetDeadline(time.Now())
	_ = backend.SetDeadline(time.Now())
	<-done
}

func (p *tcpProxy) pipe(dst net.Conn, src io.Reader, done chan<- struct{}) {
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
