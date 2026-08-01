// Package mihomolab runs the pinned Mihomo binary as an isolated child process
// and validates the complete Mihomo -> SmartRoute -> forced-listener topology.
package mihomolab

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/sidecar"
	"github.com/firfisa/smartroute/internal/socks5"
	"github.com/firfisa/smartroute/internal/testlab"
	"github.com/firfisa/smartroute/internal/tlsinspect"
	"github.com/firfisa/smartroute/internal/transport"
)

const mappedHostname = "echo.test"

type Report struct {
	MihomoVersion        string           `json:"mihomo_version"`
	Isolation            IsolationResult  `json:"isolation"`
	ConfigValidated      bool             `json:"config_validated"`
	LoopPrevented        bool             `json:"loop_prevented"`
	ReadinessGapDetected bool             `json:"readiness_gap_detected"`
	TLSReadinessVerified bool             `json:"tls_readiness_verified"`
	DecisionEventCount   int              `json:"decision_event_count"`
	DecisionEvents       []EventSummary   `json:"decision_events"`
	GatewayLastHost      string           `json:"gateway_last_host,omitempty"`
	Scenarios            []ScenarioResult `json:"scenarios"`
	Passed               bool             `json:"passed"`
}

type IsolationResult struct {
	DedicatedChildProcess bool `json:"dedicated_child_process"`
	TemporaryHome         bool `json:"temporary_home"`
	LoopbackOnly          bool `json:"loopback_only"`
	EphemeralPortsOnly    bool `json:"ephemeral_ports_only"`
	TUNEnabled            bool `json:"tun_enabled"`
	SystemProxyModified   bool `json:"system_proxy_modified"`
	ExternalNetwork       bool `json:"external_network"`
	ActiveClashRead       bool `json:"active_clash_read"`
	ActiveClashWritten    bool `json:"active_clash_written"`
}

type ScenarioResult struct {
	Name            string     `json:"name"`
	TargetHost      string     `json:"target_host"`
	ExpectedPath    model.Path `json:"expected_path"`
	ExpectedPayload bool       `json:"expected_payload"`
	SelectedPath    model.Path `json:"selected_path,omitempty"`
	ReasonCode      string     `json:"reason_code,omitempty"`
	PayloadVerified bool       `json:"payload_verified"`
	DomainPreserved bool       `json:"domain_preserved,omitempty"`
	ProxyAttempts   int        `json:"proxy_attempts"`
	Passed          bool       `json:"passed"`
	ObservedError   string     `json:"observed_error,omitempty"`
}

type EventSummary struct {
	TargetHost   string     `json:"target_host"`
	SelectedPath model.Path `json:"selected_path"`
	ReasonCode   string     `json:"reason_code"`
	Stage        string     `json:"stage_reached"`
	Committed    bool       `json:"committed"`
}

type portSet struct {
	front   int
	direct  int
	proxy   int
	sidecar int
}

// Run starts a separately supplied Mihomo binary. It never discovers an
// installed Clash/Mihomo binary or reads the active application directory.
func Run(parent context.Context, binaryPath string) (Report, error) {
	report := Report{Isolation: IsolationResult{
		DedicatedChildProcess: true,
		TemporaryHome:         true,
		LoopbackOnly:          true,
		EphemeralPortsOnly:    true,
		TUNEnabled:            false,
		SystemProxyModified:   false,
		ExternalNetwork:       false,
		ActiveClashRead:       false,
		ActiveClashWritten:    false,
	}}

	binary, err := validateBinary(binaryPath)
	if err != nil {
		return report, err
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	report.MihomoVersion, err = binaryVersion(ctx, binary)
	if err != nil {
		return report, err
	}

	home, err := os.MkdirTemp("", "smartroute-mihomo-lab-*")
	if err != nil {
		return report, fmt.Errorf("create Mihomo lab home: %w", err)
	}
	defer os.RemoveAll(home)

	echo, err := testlab.StartEchoTarget(ctx)
	if err != nil {
		return report, err
	}
	defer echo.Close()
	tlsTarget, err := startTLSTarget(ctx)
	if err != nil {
		return report, err
	}
	defer tlsTarget.Close()
	gateway, err := testlab.StartSOCKSGateway(ctx, tlsTarget.Address())
	if err != nil {
		return report, err
	}
	defer gateway.Close()
	dnsServer, err := startDNSServer(ctx)
	if err != nil {
		return report, err
	}
	defer dnsServer.Close()

	ports, err := allocatePorts()
	if err != nil {
		return report, err
	}
	sidecarListener, err := net.Listen("tcp", loopbackAddress(ports.sidecar))
	if err != nil {
		return report, fmt.Errorf("listen SmartRoute sidecar: %w", err)
	}
	defer sidecarListener.Close()

	configPath := filepath.Join(home, "config.yaml")
	configBytes := renderConfig(ports, gateway.Address(), dnsServer.Address())
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		return report, fmt.Errorf("write temporary Mihomo config: %w", err)
	}
	if output, err := runMihomoConfigTest(ctx, binary, home, configPath); err != nil {
		return report, fmt.Errorf("Mihomo config validation: %w: %s", err, sanitizeProcessOutput(output))
	}
	report.ConfigValidated = true

	var eventMu sync.Mutex
	events := make([]sidecar.DecisionEvent, 0, 4)
	server := sidecar.Server{
		NetworkProfileID: "isolated-mihomo-lab",
		HandshakeTimeout: 2 * time.Second,
		TLSRacer: &transport.TLSRacer{
			Direct:    transport.SOCKS5Dialer{Path: model.PathDirect, Endpoint: loopbackAddress(ports.direct)},
			Proxy:     transport.SOCKS5Dialer{Path: model.PathProxy, Endpoint: loopbackAddress(ports.proxy)},
			Gate:      transport.TLSServerHelloGate{},
			HeadStart: 40 * time.Millisecond,
			Timeout:   2 * time.Second,
		},
		OnDecision: func(event sidecar.DecisionEvent) {
			eventMu.Lock()
			events = append(events, event)
			eventMu.Unlock()
		},
	}
	sidecarErrors := make(chan error, 1)
	go func() { sidecarErrors <- server.Serve(ctx, sidecarListener) }()

	child, err := startMihomo(binary, home, configPath)
	if err != nil {
		return report, err
	}
	defer child.Stop()
	if err := child.WaitReady(ctx, loopbackAddress(ports.front)); err != nil {
		return report, err
	}

	frontEndpoint := loopbackAddress(ports.front)
	directResult := runScenario(ctx, loopbackAddress(ports.direct), "forced_direct_loopback", "127.0.0.1", echo.Port(), model.PathDirect, true, gateway)
	directResult.SelectedPath = model.PathDirect
	directResult.ReasonCode = "forced_listener_direct"
	directResult.Passed = directResult.PayloadVerified && directResult.ProxyAttempts == 0
	report.Scenarios = append(report.Scenarios, directResult)
	proxyListenerResult := runTLSScenario(ctx, loopbackAddress(ports.proxy), "forced_proxy_preserves_domain", mappedHostname, tlsTarget.Port(), model.PathProxy, true, gateway)
	proxyListenerResult.SelectedPath = model.PathProxy
	proxyListenerResult.ReasonCode = "forced_listener_proxy"
	proxyListenerResult.Passed = proxyListenerResult.PayloadVerified && proxyListenerResult.DomainPreserved && proxyListenerResult.ProxyAttempts == 1
	report.Scenarios = append(report.Scenarios, proxyListenerResult)
	gapResult, gapObservation := runOutboundGap(ctx, loopbackAddress(ports.direct), "mihomo_socks_ack_is_not_target_readiness", mappedHostname, tlsTarget.Port(), gateway)
	gapResult.SelectedPath = model.PathDirect
	gapResult.ReasonCode = "mihomo_socks_ack_outbound_only"
	report.ReadinessGapDetected = !gapResult.PayloadVerified && gapObservation.Success &&
		gapObservation.StageReached == model.StageOutbound && gapResult.ProxyAttempts == 0
	gapResult.Passed = report.ReadinessGapDetected
	report.Scenarios = append(report.Scenarios, gapResult)
	adaptiveResult := runTLSScenario(ctx, frontEndpoint, "tls_proxy_recovers_unreachable_direct", mappedHostname, tlsTarget.Port(), model.PathProxy, true, gateway)
	event, ok := waitForEvent(ctx, &eventMu, &events, adaptiveResult.TargetHost)
	if !ok {
		adaptiveResult.ObservedError = "missing SmartRoute TLS decision event"
	} else {
		adaptiveResult.SelectedPath = event.SelectedPath
		adaptiveResult.ReasonCode = event.ReasonCode
		report.TLSReadinessVerified = adaptiveResult.PayloadVerified && adaptiveResult.DomainPreserved &&
			adaptiveResult.ProxyAttempts == 1 && event.Committed && event.SelectedPath == model.PathProxy &&
			event.Observation.StageReached == model.StageTLS
		adaptiveResult.Passed = report.TLSReadinessVerified
	}
	report.Scenarios = append(report.Scenarios, adaptiveResult)

	time.Sleep(100 * time.Millisecond)
	eventMu.Lock()
	report.DecisionEventCount = len(events)
	for _, event := range events {
		report.DecisionEvents = append(report.DecisionEvents, EventSummary{
			TargetHost: event.Target.Hostname, SelectedPath: event.SelectedPath,
			ReasonCode: event.ReasonCode, Stage: event.Observation.StageReached.String(),
			Committed: event.Committed,
		})
	}
	eventMu.Unlock()
	_, report.GatewayLastHost = gateway.Snapshot()
	report.LoopPrevented = report.DecisionEventCount == 1 && child.Running()
	report.Passed = report.ConfigValidated && report.LoopPrevented && report.ReadinessGapDetected && report.TLSReadinessVerified
	for _, scenario := range report.Scenarios {
		report.Passed = report.Passed && scenario.Passed
	}
	if !report.Passed {
		encoded, _ := json.Marshal(report)
		return report, fmt.Errorf("isolated Mihomo lab invariant failed: %s", encoded)
	}

	select {
	case err := <-sidecarErrors:
		if err != nil {
			return report, fmt.Errorf("sidecar stopped unexpectedly: %w", err)
		}
	default:
	}
	return report, nil
}

func validateBinary(path string) (string, error) {
	if path == "" {
		return "", errors.New("explicit -mihomo binary path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve Mihomo binary: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("stat Mihomo binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("Mihomo path must be an executable regular file")
	}
	return absolute, nil
}

func binaryVersion(ctx context.Context, binary string) (string, error) {
	command := exec.CommandContext(ctx, binary, "-v")
	command.Env = isolatedEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read Mihomo version: %w", err)
	}
	version := strings.TrimSpace(string(output))
	if !strings.Contains(version, "Mihomo") || !strings.Contains(version, "v1.19.29") ||
		!strings.Contains(version, "SmartRoute-isolated-lab") {
		return "", fmt.Errorf("unexpected Mihomo binary version %q; want pinned v1.19.29 build", version)
	}
	return version, nil
}

func renderConfig(ports portSet, gatewayAddress, dnsAddress string) []byte {
	gatewayHost, gatewayPort, _ := net.SplitHostPort(gatewayAddress)
	dnsHost, dnsPort, _ := net.SplitHostPort(dnsAddress)
	return []byte(fmt.Sprintf(`mixed-port: %d
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
proxies:
  - name: LAB-PROXY
    type: socks5
    server: %s
    port: %s
    udp: false
  - name: SMARTROUTE-ADAPTER
    type: socks5
    server: 127.0.0.1
    port: %d
    udp: false
listeners:
  - name: smartroute-lab-direct
    type: mixed
    listen: 127.0.0.1
    port: %d
    proxy: DIRECT
    udp: false
  - name: smartroute-lab-proxy
    type: mixed
    listen: 127.0.0.1
    port: %d
    proxy: LAB-PROXY
    udp: false
rules:
  - MATCH,SMARTROUTE-ADAPTER
`, ports.front, dnsHost, dnsPort, gatewayHost, gatewayPort, ports.sidecar, ports.direct, ports.proxy))
}

func runMihomoConfigTest(ctx context.Context, binary, home, configPath string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, "-t", "-d", home, "-f", configPath)
	command.Env = isolatedEnvironment()
	return command.CombinedOutput()
}

type mihomoChild struct {
	command *exec.Cmd
	done    chan error
	log     *bytes.Buffer
	mu      sync.Mutex
	exited  bool
}

func startMihomo(binary, home, configPath string) (*mihomoChild, error) {
	logBuffer := &bytes.Buffer{}
	command := exec.Command(binary, "-d", home, "-f", configPath)
	command.Env = isolatedEnvironment()
	command.Stdout = logBuffer
	command.Stderr = logBuffer
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start isolated Mihomo: %w", err)
	}
	child := &mihomoChild{command: command, done: make(chan error, 1), log: logBuffer}
	go func() {
		err := command.Wait()
		child.mu.Lock()
		child.exited = true
		child.mu.Unlock()
		child.done <- err
	}()
	return child, nil
}

func (c *mihomoChild) WaitReady(ctx context.Context, address string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case childErr := <-c.done:
			return fmt.Errorf("isolated Mihomo exited before ready: %v: %s", childErr, sanitizeProcessOutput(c.log.Bytes()))
		case <-ctx.Done():
			return fmt.Errorf("wait for isolated Mihomo: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (c *mihomoChild) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.exited
}

func (c *mihomoChild) Stop() {
	if c.command.Process == nil {
		return
	}
	_ = c.command.Process.Signal(os.Interrupt)
	select {
	case <-c.done:
		return
	case <-time.After(3 * time.Second):
		_ = c.command.Process.Kill()
		select {
		case <-c.done:
		case <-time.After(time.Second):
		}
	}
}

func runScenario(ctx context.Context, frontEndpoint, name, host string, port uint16, expected model.Path, expectedPayload bool, gateway *testlab.SOCKSGateway) ScenarioResult {
	beforeAttempts, _ := gateway.Snapshot()
	result := ScenarioResult{Name: name, TargetHost: host, ExpectedPath: expected, ExpectedPayload: expectedPayload}
	client, err := socks5.DialContext(ctx, frontEndpoint, socks5.Request{Host: host, Port: port})
	if err != nil {
		result.ObservedError = err.Error()
		return result
	}
	defer client.Close()
	payload := []byte("smartroute-mihomo-lab-" + name)
	if _, err := client.Write(payload); err != nil {
		result.ObservedError = fmt.Sprintf("write payload: %v", err)
		return result
	}
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(client, received); err != nil {
		result.ObservedError = fmt.Sprintf("read payload: %v", err)
		return result
	}
	result.PayloadVerified = bytes.Equal(payload, received)
	afterAttempts, lastHost := gateway.Snapshot()
	result.ProxyAttempts = afterAttempts - beforeAttempts
	result.DomainPreserved = result.ProxyAttempts > 0 && lastHost == mappedHostname
	return result
}

func runTLSScenario(ctx context.Context, endpoint, name, host string, port uint16, expected model.Path, expectedPayload bool, gateway *testlab.SOCKSGateway) ScenarioResult {
	beforeAttempts, _ := gateway.Snapshot()
	result := ScenarioResult{Name: name, TargetHost: host, ExpectedPath: expected, ExpectedPayload: expectedPayload}
	client, err := socks5.DialContext(ctx, endpoint, socks5.Request{Host: host, Port: port})
	if err != nil {
		result.ObservedError = err.Error()
		return result
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write(syntheticClientHelloRecords()); err != nil {
		result.ObservedError = fmt.Sprintf("write ClientHello: %v", err)
		return result
	}
	hello, err := tlsinspect.ReadServerHello(client, 0)
	if err != nil {
		result.ObservedError = fmt.Sprintf("read ServerHello: %v", err)
	} else {
		result.PayloadVerified = bytes.Equal(hello.Wire, syntheticServerHelloRecord())
	}
	afterAttempts, lastHost := gateway.Snapshot()
	result.ProxyAttempts = afterAttempts - beforeAttempts
	result.DomainPreserved = result.ProxyAttempts > 0 && lastHost == mappedHostname
	return result
}

func runOutboundGap(ctx context.Context, endpoint, name, host string, port uint16, gateway *testlab.SOCKSGateway) (ScenarioResult, model.Observation) {
	beforeAttempts, _ := gateway.Snapshot()
	result := ScenarioResult{
		Name: name, TargetHost: host, ExpectedPath: model.PathDirect, ExpectedPayload: false,
	}
	client, observation, err := (transport.SOCKS5Dialer{Path: model.PathDirect, Endpoint: endpoint}).Dial(ctx, model.Target{
		Hostname: host, Port: port, Transport: model.TransportTCP,
	})
	if err != nil {
		result.ObservedError = err.Error()
		return result, observation
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write(syntheticClientHelloRecords()); err != nil {
		result.ObservedError = fmt.Sprintf("write ClientHello: %v", err)
		return result, observation
	}
	if _, err := tlsinspect.ReadServerHello(client, 0); err != nil {
		result.ObservedError = fmt.Sprintf("read ServerHello: %v", err)
	} else {
		result.PayloadVerified = true
	}
	afterAttempts, _ := gateway.Snapshot()
	result.ProxyAttempts = afterAttempts - beforeAttempts
	return result, observation
}

type dnsServer struct {
	conn   net.PacketConn
	cancel context.CancelFunc
}

func startDNSServer(parent context.Context) (*dnsServer, error) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen synthetic DNS: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	server := &dnsServer{conn: conn, cancel: cancel}
	go server.serve(ctx)
	return server, nil
}

func (s *dnsServer) Address() string { return s.conn.LocalAddr().String() }

func (s *dnsServer) Close() {
	s.cancel()
	_ = s.conn.Close()
}

func (s *dnsServer) serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = s.conn.Close()
	}()
	buffer := make([]byte, 1500)
	for {
		n, address, err := s.conn.ReadFrom(buffer)
		if err != nil {
			return
		}
		response, ok := syntheticDNSResponse(buffer[:n])
		if ok {
			_, _ = s.conn.WriteTo(response, address)
		}
	}
}

func syntheticDNSResponse(query []byte) ([]byte, bool) {
	if len(query) < 17 {
		return nil, false
	}
	questionEnd := 12
	for {
		if questionEnd >= len(query) {
			return nil, false
		}
		length := int(query[questionEnd])
		questionEnd++
		if length == 0 {
			break
		}
		if length > 63 || questionEnd+length > len(query) {
			return nil, false
		}
		questionEnd += length
	}
	if questionEnd+4 > len(query) {
		return nil, false
	}
	questionEnd += 4
	queryType := binary.BigEndian.Uint16(query[questionEnd-4 : questionEnd-2])
	answerCount := uint16(0)
	if queryType == 1 {
		answerCount = 1
	}
	response := make([]byte, questionEnd)
	copy(response, query[:questionEnd])
	binary.BigEndian.PutUint16(response[2:4], 0x8180)
	binary.BigEndian.PutUint16(response[4:6], 1)
	binary.BigEndian.PutUint16(response[6:8], answerCount)
	binary.BigEndian.PutUint16(response[8:10], 0)
	binary.BigEndian.PutUint16(response[10:12], 0)
	if answerCount == 1 {
		response = append(response,
			0xc0, 0x0c,
			0x00, 0x01,
			0x00, 0x01,
			0x00, 0x00, 0x00, 0x01,
			0x00, 0x04,
			127, 0, 0, 2,
		)
	}
	return response, true
}

func waitForEvent(ctx context.Context, mu *sync.Mutex, events *[]sidecar.DecisionEvent, host string) (sidecar.DecisionEvent, bool) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		mu.Lock()
		for _, event := range *events {
			if event.Target.Hostname == host {
				mu.Unlock()
				return event, true
			}
		}
		mu.Unlock()
		select {
		case <-ctx.Done():
			return sidecar.DecisionEvent{}, false
		case <-ticker.C:
		}
	}
}

func allocatePorts() (portSet, error) {
	listeners := make([]net.Listener, 0, 4)
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	ports := make([]int, 0, 4)
	for range 4 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return portSet{}, fmt.Errorf("allocate isolated Mihomo port: %w", err)
		}
		listeners = append(listeners, listener)
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	return portSet{front: ports[0], direct: ports[1], proxy: ports[2], sidecar: ports[3]}, nil
}

func loopbackAddress(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func isolatedEnvironment() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(name, "CLASH_") || strings.HasPrefix(name, "MIHOMO_") {
			continue
		}
		result = append(result, item)
	}
	return result
}

func sanitizeProcessOutput(output []byte) string {
	value := strings.TrimSpace(string(output))
	if len(value) > 2000 {
		value = value[len(value)-2000:]
	}
	return value
}
