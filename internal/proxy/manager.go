package proxy

import (
	"context"
	"fmt"
	"log"
	"sync"

	"mc-proxy/internal/config"
)

type managedProxy interface {
	Name() string
	Start(ctx context.Context) error
	Close() error
}

type Manager struct {
	logger   *log.Logger
	proxies  []managedProxy
	closeMux sync.Once
}

func NewManager(cfg config.Config, logger *log.Logger) (*Manager, error) {
	if logger == nil {
		logger = log.Default()
	}

	items := make([]managedProxy, 0, len(cfg.Proxies))
	for _, pc := range cfg.Proxies {
		if config.IsTCPNet(pc.ListenNet) {
			items = append(items, newTCPProxy(pc, logger))
			continue
		}
		if config.IsUDPNet(pc.ListenNet) {
			items = append(items, newUDPProxy(pc, logger))
			continue
		}
		return nil, fmt.Errorf("proxy %q has unsupported network %q", pc.Name, pc.ListenNet)
	}

	return &Manager{logger: logger, proxies: items}, nil
}

func (m *Manager) Run(ctx context.Context) error {
	for _, p := range m.proxies {
		if err := p.Start(ctx); err != nil {
			_ = m.Close()
			return fmt.Errorf("start proxy %q failed: %w", p.Name(), err)
		}
	}

	m.logger.Printf("started %d proxy instance(s)", len(m.proxies))

	<-ctx.Done()
	return m.Close()
}

func (m *Manager) Close() error {
	var firstErr error
	m.closeMux.Do(func() {
		for _, p := range m.proxies {
			if err := p.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	})
	return firstErr
}
