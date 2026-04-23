package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadYAMLDefaults(t *testing.T) {
	t.Parallel()

	content := `proxies:
  - name: "tcp-proxy"
    listen_net: "tcp"
    listen_addr: "127.0.0.1:25565"
    backend_addr: "127.0.0.1:25566"
    rule: "proxy_protocol"
`

	path := writeTempConfig(t, ".yaml", content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	proxy := cfg.Proxies[0]
	if proxy.ProxyVersion != defaultProxyVersion {
		t.Fatalf("ProxyVersion = %d, want %d", proxy.ProxyVersion, defaultProxyVersion)
	}
	if proxy.ConnectTimeout.Duration != defaultConnectTimeout {
		t.Fatalf("ConnectTimeout = %s, want %s", proxy.ConnectTimeout.Duration, defaultConnectTimeout)
	}
	if proxy.ReadBufferSize != defaultBufferSize {
		t.Fatalf("ReadBufferSize = %d, want %d", proxy.ReadBufferSize, defaultBufferSize)
	}
	if proxy.WriteBufferSize != defaultBufferSize {
		t.Fatalf("WriteBufferSize = %d, want %d", proxy.WriteBufferSize, defaultBufferSize)
	}
	if proxy.UDPSessionTimeout.Duration != 0 {
		t.Fatalf("UDPSessionTimeout = %s, want 0", proxy.UDPSessionTimeout.Duration)
	}
}

func TestLoadRejectsUDPProxyProtocolV1(t *testing.T) {
	t.Parallel()

	content := `proxies:
  - name: "udp-proxy"
    listen_net: "udp"
    listen_addr: "127.0.0.1:19132"
    backend_addr: "127.0.0.1:19133"
    rule: "proxy_protocol"
    proxy_version: 1
`

	path := writeTempConfig(t, ".yaml", content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "only supports PROXY protocol v2") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadJSONDurationFields(t *testing.T) {
	t.Parallel()

	content := `{
  "proxies": [
    {
      "name": "udp-proxy",
      "listen_net": "udp",
      "listen_addr": "127.0.0.1:19132",
      "backend_addr": "127.0.0.1:19133",
      "rule": "passthrough",
      "udp_session_timeout": "5s",
      "connect_timeout": "1s",
      "read_buffer_size": 8192,
      "write_buffer_size": 16384
    }
  ]
}`

	path := writeTempConfig(t, ".json", content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	proxy := cfg.Proxies[0]
	if proxy.UDPSessionTimeout.Duration != 5*time.Second {
		t.Fatalf("UDPSessionTimeout = %s, want 5s", proxy.UDPSessionTimeout.Duration)
	}
	if proxy.ConnectTimeout.Duration != 1*time.Second {
		t.Fatalf("ConnectTimeout = %s, want 1s", proxy.ConnectTimeout.Duration)
	}
	if proxy.ReadBufferSize != 8192 {
		t.Fatalf("ReadBufferSize = %d, want 8192", proxy.ReadBufferSize)
	}
	if proxy.WriteBufferSize != 16384 {
		t.Fatalf("WriteBufferSize = %d, want 16384", proxy.WriteBufferSize)
	}
}

func TestLoadAllowsBackendAddressDifferentFamily(t *testing.T) {
	t.Parallel()

	content := `proxies:
  - name: "tcp-proxy"
    listen_net: "tcp"
    listen_addr: "127.0.0.1:25565"
    backend_addr: "[::1]:25566"
    rule: "passthrough"
`

	path := writeTempConfig(t, ".yaml", content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := BackendDialNet(cfg.Proxies[0].ListenNet); got != "tcp" {
		t.Fatalf("BackendDialNet() = %q, want %q", got, "tcp")
	}
}

func TestLoadSupportsGenericTCPUDPListenNet(t *testing.T) {
	t.Parallel()

	content := `proxies:
  - name: "java"
    listen_net: "tcp"
    listen_addr: ":25565"
    backend_addr: "127.0.0.1:25566"
    rule: "proxy_protocol"
  - name: "bedrock"
    listen_net: "udp"
    listen_addr: ":19132"
    backend_addr: "127.0.0.1:19133"
    rule: "passthrough"
`

	path := writeTempConfig(t, ".yaml", content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := BackendDialNet(cfg.Proxies[0].ListenNet); got != "tcp" {
		t.Fatalf("BackendDialNet(tcp) = %q, want %q", got, "tcp")
	}
	if got := BackendDialNet(cfg.Proxies[1].ListenNet); got != "udp" {
		t.Fatalf("BackendDialNet(udp) = %q, want %q", got, "udp")
	}
}

func writeTempConfig(t *testing.T, ext, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config"+ext)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config failed: %v", err)
	}
	return path
}
