package mihomolab

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/firfisa/smartroute/internal/testlab"
)

// DirectBenchmarkTopology owns one pinned Mihomo child, a synthetic DNS
// server, and an echo target. It exposes only a forced-DIRECT SOCKS listener.
type DirectBenchmarkTopology struct {
	mu              sync.Mutex
	cancel          context.CancelFunc
	home            string
	child           *mihomoChild
	dns             *dnsServer
	echo            *testlab.EchoTarget
	tls             *testlab.TLSTarget
	endpoint        string
	targetHost      string
	version         string
	configValidated bool
	closed          bool
}

// StartDirectBenchmarkTopology starts an isolated pinned-Mihomo forced-DIRECT
// listener backed by synthetic DNS and a loopback echo target. Distinct
// ephemeral ports, rather than a platform-specific loopback alias, separate
// the Mihomo listener from the target.
func StartDirectBenchmarkTopology(parent context.Context, binaryPath string) (*DirectBenchmarkTopology, error) {
	return startDirectBenchmarkTopology(parent, binaryPath, false)
}

// StartTLSBenchmarkTopology exposes the same isolated forced-DIRECT Mihomo
// boundary backed by a deterministic ClientHello-to-ServerHello target.
func StartTLSBenchmarkTopology(parent context.Context, binaryPath string) (*DirectBenchmarkTopology, error) {
	return startDirectBenchmarkTopology(parent, binaryPath, true)
}

func startDirectBenchmarkTopology(parent context.Context, binaryPath string, tlsReady bool) (*DirectBenchmarkTopology, error) {
	binary, err := validateBinary(binaryPath)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	setupCtx, setupCancel := context.WithTimeout(ctx, 20*time.Second)
	defer setupCancel()
	topology := &DirectBenchmarkTopology{cancel: cancel, targetHost: mappedHostname}
	failed := true
	defer func() {
		if failed {
			_ = topology.Close()
		}
	}()
	topology.version, err = binaryVersion(setupCtx, binary)
	if err != nil {
		return nil, err
	}
	topology.home, err = os.MkdirTemp("", "smartroute-mihomo-benchmark-*")
	if err != nil {
		return nil, fmt.Errorf("create Mihomo benchmark home: %w", err)
	}
	if tlsReady {
		topology.tls, err = testlab.StartTLSTarget(ctx)
		if err != nil {
			return nil, fmt.Errorf("start Mihomo benchmark TLS target: %w", err)
		}
	} else {
		topology.echo, err = testlab.StartEchoTargetOn(ctx, "127.0.0.1")
		if err != nil {
			return nil, fmt.Errorf("start Mihomo benchmark echo target: %w", err)
		}
	}
	topology.dns, err = startDNSServerFor(ctx, [4]byte{127, 0, 0, 1})
	if err != nil {
		return nil, fmt.Errorf("start Mihomo benchmark DNS: %w", err)
	}
	port, err := allocateBenchmarkPort()
	if err != nil {
		return nil, err
	}
	topology.endpoint = loopbackAddress(port)
	configPath := filepath.Join(topology.home, "config.yaml")
	config := renderDirectBenchmarkConfig(port, topology.dns.Address())
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		return nil, fmt.Errorf("write Mihomo benchmark config: %w", err)
	}
	if output, err := runMihomoConfigTest(setupCtx, binary, topology.home, configPath); err != nil {
		return nil, fmt.Errorf("validate Mihomo benchmark config: %w: %s", err, sanitizeProcessOutput(output))
	}
	topology.configValidated = true
	topology.child, err = startMihomo(binary, topology.home, configPath)
	if err != nil {
		return nil, err
	}
	if err := topology.child.WaitReady(setupCtx, topology.endpoint); err != nil {
		return nil, err
	}
	failed = false
	return topology, nil
}

func (t *DirectBenchmarkTopology) Endpoint() string   { return t.endpoint }
func (t *DirectBenchmarkTopology) TargetHost() string { return t.targetHost }
func (t *DirectBenchmarkTopology) TargetPort() uint16 {
	if t.tls != nil {
		return t.tls.Port()
	}
	if t.echo == nil {
		return 0
	}
	return t.echo.Port()
}
func (t *DirectBenchmarkTopology) TargetAddress() string {
	if t.tls != nil {
		return t.tls.Address()
	}
	if t.echo == nil {
		return ""
	}
	return t.echo.Address()
}
func (t *DirectBenchmarkTopology) Version() string       { return t.version }
func (t *DirectBenchmarkTopology) ConfigValidated() bool { return t.configValidated }
func (t *DirectBenchmarkTopology) Running() bool {
	return t.child != nil && t.child.Running()
}
func (t *DirectBenchmarkTopology) AcceptedClientHellos() int {
	if t.tls == nil {
		return 0
	}
	return t.tls.AcceptedClientHellos()
}

// Close stops only the owned child and synthetic services, then removes the
// exact temporary home. It is idempotent.
func (t *DirectBenchmarkTopology) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()
	if t.child != nil {
		t.child.Stop()
	}
	if t.dns != nil {
		t.dns.Close()
	}
	if t.echo != nil {
		t.echo.Close()
	}
	if t.tls != nil {
		t.tls.Close()
	}
	if t.cancel != nil {
		t.cancel()
	}
	if t.home == "" {
		return nil
	}
	if err := os.RemoveAll(t.home); err != nil {
		return fmt.Errorf("remove Mihomo benchmark home: %w", err)
	}
	return nil
}

func allocateBenchmarkPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate Mihomo benchmark port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return 0, fmt.Errorf("release Mihomo benchmark port: %w", err)
	}
	return port, nil
}

func renderDirectBenchmarkConfig(port int, dnsAddress string) []byte {
	dnsHost, dnsPort, _ := net.SplitHostPort(dnsAddress)
	return []byte(fmt.Sprintf(`mixed-port: 0
bind-address: 127.0.0.1
allow-lan: false
mode: rule
log-level: warning
ipv6: false
unified-delay: true
profile:
  store-selected: false
  store-fake-ip: false
tun:
  enable: false
dns:
  enable: true
  listen: ''
  ipv6: false
  use-hosts: false
  use-system-hosts: false
  enhanced-mode: redir-host
  nameserver:
    - %s:%s
listeners:
  - name: smartroute-benchmark-direct
    type: mixed
    listen: 127.0.0.1
    port: %d
    proxy: DIRECT
    udp: false
rules:
  - MATCH,DIRECT
`, dnsHost, dnsPort, port))
}
