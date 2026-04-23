package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultProxyVersion      = 2
	defaultUDPSessionTimeout = 2 * time.Minute
	defaultBufferSize        = 32 * 1024
	defaultConnectTimeout    = 3 * time.Second
)

type Rule string

const (
	RulePassthrough   Rule = "passthrough"
	RuleProxyProtocol Rule = "proxy_protocol"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return errors.New("duration is nil")
	}

	switch value.Kind {
	case yaml.ScalarNode:
		if value.Tag == "!!int" {
			var raw int64
			if err := value.Decode(&raw); err != nil {
				return err
			}
			d.Duration = time.Duration(raw)
			return nil
		}

		var text string
		if err := value.Decode(&text); err != nil {
			return err
		}
		parsed, err := time.ParseDuration(text)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", text, err)
		}
		d.Duration = parsed
		return nil
	default:
		return fmt.Errorf("invalid duration YAML kind: %d", value.Kind)
	}
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return errors.New("empty duration")
	}

	if data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		parsed, err := time.ParseDuration(text)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", text, err)
		}
		d.Duration = parsed
		return nil
	}

	var raw int64
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	d.Duration = time.Duration(raw)
	return nil
}

type Config struct {
	Proxies []ProxyConfig `yaml:"proxies" json:"proxies"`
}

type ProxyConfig struct {
	Name              string   `yaml:"name" json:"name"`
	ListenNet         string   `yaml:"listen_net" json:"listen_net"`
	ListenAddr        string   `yaml:"listen_addr" json:"listen_addr"`
	BackendAddr       string   `yaml:"backend_addr" json:"backend_addr"`
	Rule              Rule     `yaml:"rule" json:"rule"`
	ProxyVersion      int      `yaml:"proxy_version" json:"proxy_version"`
	UDPSessionTimeout Duration `yaml:"udp_session_timeout" json:"udp_session_timeout"`
	ReadBufferSize    int      `yaml:"read_buffer_size" json:"read_buffer_size"`
	WriteBufferSize   int      `yaml:"write_buffer_size" json:"write_buffer_size"`
	ConnectTimeout    Duration `yaml:"connect_timeout" json:"connect_timeout"`
}

func Load(path string) (*Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config failed: %w", err)
	}

	var cfg Config
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		if err := json.Unmarshal(content, &cfg); err != nil {
			return nil, fmt.Errorf("parse JSON config failed: %w", err)
		}
	default:
		if err := yaml.Unmarshal(content, &cfg); err != nil {
			return nil, fmt.Errorf("parse YAML config failed: %w", err)
		}
	}

	if err := cfg.Normalize(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) Normalize() error {
	if len(c.Proxies) == 0 {
		return errors.New("config.proxies is required")
	}

	names := make(map[string]struct{}, len(c.Proxies))
	for i := range c.Proxies {
		if err := c.Proxies[i].normalize(i, names); err != nil {
			return err
		}
	}

	return nil
}

func (p *ProxyConfig) normalize(index int, names map[string]struct{}) error {
	label := fmt.Sprintf("proxies[%d]", index)
	if p.Name == "" {
		return fmt.Errorf("%s.name is required", label)
	}
	if _, ok := names[p.Name]; ok {
		return fmt.Errorf("proxy name %q is duplicated", p.Name)
	}
	names[p.Name] = struct{}{}

	if !isSupportedNetwork(p.ListenNet) {
		return fmt.Errorf("proxy %q has unsupported listen_net %q", p.Name, p.ListenNet)
	}
	if p.ListenAddr == "" {
		return fmt.Errorf("proxy %q listen_addr is required", p.Name)
	}
	if p.BackendAddr == "" {
		return fmt.Errorf("proxy %q backend_addr is required", p.Name)
	}

	if err := validateAddress(p.ListenNet, p.ListenAddr, "listen_addr"); err != nil {
		return fmt.Errorf("proxy %q: %w", p.Name, err)
	}
	if err := validateAddress(BackendDialNet(p.ListenNet), p.BackendAddr, "backend_addr"); err != nil {
		return fmt.Errorf("proxy %q: %w", p.Name, err)
	}

	switch p.Rule {
	case RulePassthrough, RuleProxyProtocol:
	default:
		return fmt.Errorf("proxy %q has invalid rule %q", p.Name, p.Rule)
	}

	if p.ConnectTimeout.Duration < 0 {
		return fmt.Errorf("proxy %q connect_timeout cannot be negative", p.Name)
	}
	if p.ConnectTimeout.Duration == 0 {
		p.ConnectTimeout.Duration = defaultConnectTimeout
	}

	if p.ReadBufferSize < 0 || p.WriteBufferSize < 0 {
		return fmt.Errorf("proxy %q buffer size cannot be negative", p.Name)
	}
	if p.ReadBufferSize == 0 {
		p.ReadBufferSize = defaultBufferSize
	}
	if p.WriteBufferSize == 0 {
		p.WriteBufferSize = defaultBufferSize
	}

	if IsUDPNet(p.ListenNet) {
		if p.UDPSessionTimeout.Duration < 0 {
			return fmt.Errorf("proxy %q udp_session_timeout cannot be negative", p.Name)
		}
		if p.UDPSessionTimeout.Duration == 0 {
			p.UDPSessionTimeout.Duration = defaultUDPSessionTimeout
		}
	} else {
		p.UDPSessionTimeout.Duration = 0
	}

	if p.Rule == RuleProxyProtocol {
		if p.ProxyVersion == 0 {
			p.ProxyVersion = defaultProxyVersion
		}

		if IsUDPNet(p.ListenNet) && p.ProxyVersion != 2 {
			return fmt.Errorf("proxy %q uses UDP and only supports PROXY protocol v2", p.Name)
		}
		if !IsUDPNet(p.ListenNet) && p.ProxyVersion != 1 && p.ProxyVersion != 2 {
			return fmt.Errorf("proxy %q has unsupported proxy_version %d", p.Name, p.ProxyVersion)
		}
	} else {
		if p.ProxyVersion != 0 && p.ProxyVersion != 1 && p.ProxyVersion != 2 {
			return fmt.Errorf("proxy %q has unsupported proxy_version %d", p.Name, p.ProxyVersion)
		}
	}

	return nil
}

func IsTCPNet(network string) bool {
	return network == "tcp"
}

func IsUDPNet(network string) bool {
	return network == "udp"
}

func isSupportedNetwork(network string) bool {
	return IsTCPNet(network) || IsUDPNet(network)
}

func BackendDialNet(listenNet string) string {
	if IsTCPNet(listenNet) {
		return "tcp"
	}
	if IsUDPNet(listenNet) {
		return "udp"
	}
	return listenNet
}

func validateAddress(network, address, field string) error {
	if network == "tcp" || IsTCPNet(network) {
		if _, err := net.ResolveTCPAddr(network, address); err != nil {
			return fmt.Errorf("invalid %s %q: %w", field, address, err)
		}
		return nil
	}

	if network == "udp" || IsUDPNet(network) {
		if _, err := net.ResolveUDPAddr(network, address); err != nil {
			return fmt.Errorf("invalid %s %q: %w", field, address, err)
		}
		return nil
	}

	return fmt.Errorf("unsupported network %q", network)
}
