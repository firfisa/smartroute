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

	"github.com/firfisa/smartroute/internal/guard"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/privacy"
	"github.com/firfisa/smartroute/internal/sidecar"
	"github.com/firfisa/smartroute/internal/socks5"
	"github.com/firfisa/smartroute/internal/testlab"
	"github.com/firfisa/smartroute/internal/tlsinspect"
	"github.com/firfisa/smartroute/internal/transport"
)

const mappedHostname = "echo.test"

type Report struct {
	MihomoVersion           string              `json:"mihomo_version"`
	Isolation               IsolationResult     `json:"isolation"`
	ConfigValidated         bool                `json:"config_validated"`
	LoopPrevented           bool                `json:"loop_prevented"`
	ReadinessGapDetected    bool                `json:"readiness_gap_detected"`
	TLSReadinessVerified    bool                `json:"tls_readiness_verified"`
	GuardFallbackVerified   bool                `json:"guard_fallback_verified"`
	GuardRecoveryVerified   bool                `json:"guard_recovery_verified"`
	DecisionEventCount      int                 `json:"decision_event_count"`
	DecisionEvents          []EventSummary      `json:"decision_events"`
	GuardEventCount         int                 `json:"guard_event_count"`
	GuardEvents             []GuardEventSummary `json:"guard_events"`
	AdaptiveGatewayLastHost string              `json:"adaptive_gateway_last_host,omitempty"`
	OriginalGatewayLastHost string              `json:"original_gateway_last_host,omitempty"`
	Scenarios               []ScenarioResult    `json:"scenarios"`
	Passed                  bool                `json:"passed"`
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

type GuardEventSummary struct {
	TargetHost      string `json:"target_host"`
	SelectedLane    string `json:"selected_lane,omitempty"`
	ReasonCode      string `json:"reason_code"`
	AdaptiveFailure string `json:"adaptive_failure,omitempty"`
	Committed       bool   `json:"committed"`
}

type portSet struct {
	front    int
	direct   int
	proxy    int
	sidecar  int
	guard    int
	original int
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
	adaptiveGateway, err := testlab.StartSOCKSGateway(ctx, tlsTarget.Address())
	if err != nil {
		return report, err
	}
	defer adaptiveGateway.Close()
	originalGateway, err := testlab.StartSOCKSGateway(ctx, tlsTarget.Address())
	if err != nil {
		return report, err
	}
	defer originalGateway.Close()
	dnsServer, err := startDNSServer(ctx)
	if err != nil {
		return report, err
	}
	defer dnsServer.Close()

	ports, err := allocatePorts()
	if err != nil {
		return report, err
	}
	configPath := filepath.Join(home, "config.yaml")
	configBytes := renderConfig(ports, adaptiveGateway.Address(), originalGateway.Address(), dnsServer.Address())
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		return report, fmt.Errorf("write temporary Mihomo config: %w", err)
	}
	if output, err := runMihomoConfigTest(ctx, binary, home, configPath); err != nil {
		return report, fmt.Errorf("Mihomo config validation: %w: %s", err, sanitizeProcessOutput(output))
	}
	report.ConfigValidated = true

	var eventMu sync.Mutex
	events := make([]sidecar.DecisionEvent, 0, 4)
	guardEvents := make([]guard.DecisionEvent, 0, 4)
	labPrivacyPolicy, err := privacy.New(privacy.ModeExplicitOptIn, nil)
	if err != nil {
		return report, fmt.Errorf("compile lab privacy policy: %w", err)
	}
	server := sidecar.Server{
		NetworkProfileID:  "isolated-mihomo-lab",
		DirectProbePolicy: labPrivacyPolicy,
		HandshakeTimeout:  2 * time.Second,
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
	adaptiveService, err := startSidecar(ctx, loopbackAddress(ports.sidecar), server)
	if err != nil {
		return report, err
	}
	defer func() {
		if adaptiveService != nil {
			_ = adaptiveService.Stop()
		}
	}()

	guardListener, err := net.Listen("tcp", loopbackAddress(ports.guard))
	if err != nil {
		return report, fmt.Errorf("listen SmartRoute availability guard: %w", err)
	}
	defer guardListener.Close()
	guardServer := guard.Server{
		Adaptive:         guard.SOCKS5Dialer{Endpoint: loopbackAddress(ports.sidecar)},
		Original:         guard.SOCKS5Dialer{Endpoint: loopbackAddress(ports.original)},
		NetworkProfileID: "isolated-mihomo-lab",
		HandshakeTimeout: 2 * time.Second,
		OnDecision: func(event guard.DecisionEvent) {
			eventMu.Lock()
			guardEvents = append(guardEvents, event)
			eventMu.Unlock()
		},
	}
	guardErrors := make(chan error, 1)
	go func() { guardErrors <- guardServer.Serve(ctx, guardListener) }()

	child, err := startMihomo(binary, home, configPath)
	if err != nil {
		return report, err
	}
	defer child.Stop()
	if err := child.WaitReady(ctx, loopbackAddress(ports.front)); err != nil {
		return report, err
	}

	frontEndpoint := loopbackAddress(ports.front)
	directResult := runScenario(ctx, loopbackAddress(ports.direct), "forced_direct_loopback", "127.0.0.1", echo.Port(), model.PathDirect, true, adaptiveGateway)
	directResult.SelectedPath = model.PathDirect
	directResult.ReasonCode = "forced_listener_direct"
	directResult.Passed = directResult.PayloadVerified && directResult.ProxyAttempts == 0
	report.Scenarios = append(report.Scenarios, directResult)
	proxyListenerResult := runTLSScenario(ctx, loopbackAddress(ports.proxy), "forced_proxy_preserves_domain", mappedHostname, tlsTarget.Port(), model.PathProxy, true, adaptiveGateway)
	proxyListenerResult.SelectedPath = model.PathProxy
	proxyListenerResult.ReasonCode = "forced_listener_proxy"
	proxyListenerResult.Passed = proxyListenerResult.PayloadVerified && proxyListenerResult.DomainPreserved && proxyListenerResult.ProxyAttempts == 1
	report.Scenarios = append(report.Scenarios, proxyListenerResult)
	gapResult, gapObservation := runOutboundGap(ctx, loopbackAddress(ports.direct), "mihomo_socks_ack_is_not_target_readiness", mappedHostname, tlsTarget.Port(), adaptiveGateway)
	gapResult.SelectedPath = model.PathDirect
	gapResult.ReasonCode = "mihomo_socks_ack_outbound_only"
	report.ReadinessGapDetected = !gapResult.PayloadVerified && gapObservation.Success &&
		gapObservation.StageReached == model.StageOutbound && gapResult.ProxyAttempts == 0
	gapResult.Passed = report.ReadinessGapDetected
	report.Scenarios = append(report.Scenarios, gapResult)
	decisionStart, guardStart := eventCounts(&eventMu, &events, &guardEvents)
	adaptiveResult := runTLSScenario(ctx, frontEndpoint, "tls_proxy_recovers_unreachable_direct", mappedHostname, tlsTarget.Port(), model.PathProxy, true, adaptiveGateway)
	event, eventOK := waitForEventAfter(ctx, &eventMu, &events, decisionStart, adaptiveResult.TargetHost)
	guardEvent, guardOK := waitForGuardEventAfter(ctx, &eventMu, &guardEvents, guardStart, adaptiveResult.TargetHost)
	if !eventOK || !guardOK {
		adaptiveResult.ObservedError = "missing SmartRoute TLS decision event"
	} else {
		adaptiveResult.SelectedPath = event.SelectedPath
		adaptiveResult.ReasonCode = event.ReasonCode
		report.TLSReadinessVerified = adaptiveResult.PayloadVerified && adaptiveResult.DomainPreserved &&
			adaptiveResult.ProxyAttempts == 1 && event.Committed && event.SelectedPath == model.PathProxy &&
			event.Observation.StageReached == model.StageTLS && guardEvent.Committed &&
			guardEvent.SelectedLane == guard.LaneAdaptive && guardEvent.ReasonCode == guard.ReasonAdaptiveAvailable
		adaptiveResult.Passed = report.TLSReadinessVerified
	}
	report.Scenarios = append(report.Scenarios, adaptiveResult)

	if err := adaptiveService.Stop(); err != nil {
		return report, fmt.Errorf("stop adaptive sidecar for fallback scenario: %w", err)
	}
	adaptiveService = nil
	decisionStart, guardStart = eventCounts(&eventMu, &events, &guardEvents)
	fallbackResult := runTLSScenario(ctx, frontEndpoint, "guard_falls_back_when_engine_unavailable", mappedHostname, tlsTarget.Port(), model.PathProxy, true, originalGateway)
	guardEvent, guardOK = waitForGuardEventAfter(ctx, &eventMu, &guardEvents, guardStart, fallbackResult.TargetHost)
	decisionCountAfterFallback, _ := eventCounts(&eventMu, &events, &guardEvents)
	if !guardOK {
		fallbackResult.ObservedError = "missing SmartRoute guard fallback event"
	} else {
		fallbackResult.SelectedPath = model.PathProxy
		fallbackResult.ReasonCode = guardEvent.ReasonCode
		report.GuardFallbackVerified = fallbackResult.PayloadVerified && fallbackResult.DomainPreserved &&
			fallbackResult.ProxyAttempts == 1 && decisionCountAfterFallback == decisionStart && guardEvent.Committed &&
			guardEvent.SelectedLane == guard.LaneOriginal && guardEvent.ReasonCode == guard.ReasonAdaptiveUnavailableUseOriginal &&
			guardEvent.AdaptiveFailure == "unavailable"
		fallbackResult.Passed = report.GuardFallbackVerified
	}
	report.Scenarios = append(report.Scenarios, fallbackResult)

	adaptiveService, err = startSidecar(ctx, loopbackAddress(ports.sidecar), server)
	if err != nil {
		return report, fmt.Errorf("restart adaptive sidecar: %w", err)
	}
	decisionStart, guardStart = eventCounts(&eventMu, &events, &guardEvents)
	recoveryResult := runTLSScenario(ctx, frontEndpoint, "guard_returns_to_adaptive_after_restart", mappedHostname, tlsTarget.Port(), model.PathProxy, true, adaptiveGateway)
	event, eventOK = waitForEventAfter(ctx, &eventMu, &events, decisionStart, recoveryResult.TargetHost)
	guardEvent, guardOK = waitForGuardEventAfter(ctx, &eventMu, &guardEvents, guardStart, recoveryResult.TargetHost)
	if !eventOK || !guardOK {
		recoveryResult.ObservedError = "missing SmartRoute recovery events"
	} else {
		recoveryResult.SelectedPath = event.SelectedPath
		recoveryResult.ReasonCode = guardEvent.ReasonCode
		report.GuardRecoveryVerified = recoveryResult.PayloadVerified && recoveryResult.DomainPreserved &&
			recoveryResult.ProxyAttempts == 1 && event.Committed && event.SelectedPath == model.PathProxy &&
			event.Observation.StageReached == model.StageTLS && guardEvent.Committed &&
			guardEvent.SelectedLane == guard.LaneAdaptive && guardEvent.ReasonCode == guard.ReasonAdaptiveAvailable
		recoveryResult.Passed = report.GuardRecoveryVerified
	}
	report.Scenarios = append(report.Scenarios, recoveryResult)

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
	report.GuardEventCount = len(guardEvents)
	for _, event := range guardEvents {
		report.GuardEvents = append(report.GuardEvents, GuardEventSummary{
			TargetHost: event.Target.Hostname, SelectedLane: event.SelectedLane,
			ReasonCode: event.ReasonCode, AdaptiveFailure: event.AdaptiveFailure,
			Committed: event.Committed,
		})
	}
	eventMu.Unlock()
	_, report.AdaptiveGatewayLastHost = adaptiveGateway.Snapshot()
	_, report.OriginalGatewayLastHost = originalGateway.Snapshot()
	report.LoopPrevented = report.DecisionEventCount == 2 && report.GuardEventCount == 3 && child.Running()
	report.Passed = report.ConfigValidated && report.LoopPrevented && report.ReadinessGapDetected &&
		report.TLSReadinessVerified && report.GuardFallbackVerified && report.GuardRecoveryVerified
	for _, scenario := range report.Scenarios {
		report.Passed = report.Passed && scenario.Passed
	}
	if !report.Passed {
		encoded, _ := json.Marshal(report)
		return report, fmt.Errorf("isolated Mihomo lab invariant failed: %s", encoded)
	}

	select {
	case err := <-adaptiveService.done:
		return report, fmt.Errorf("sidecar stopped unexpectedly: %v", err)
	default:
	}
	select {
	case err := <-guardErrors:
		return report, fmt.Errorf("guard stopped unexpectedly: %v", err)
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

func renderConfig(ports portSet, adaptiveGatewayAddress, originalGatewayAddress, dnsAddress string) []byte {
	adaptiveGatewayHost, adaptiveGatewayPort, _ := net.SplitHostPort(adaptiveGatewayAddress)
	originalGatewayHost, originalGatewayPort, _ := net.SplitHostPort(originalGatewayAddress)
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
  - name: ORIGINAL-PROXY
    type: socks5
    server: %s
    port: %s
    udp: false
  - name: SMARTROUTE-GUARD-ADAPTER
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
  - name: smartroute-lab-original
    type: mixed
    listen: 127.0.0.1
    port: %d
    proxy: ORIGINAL-PROXY
    udp: false
rules:
  - MATCH,SMARTROUTE-GUARD-ADAPTER
`, ports.front, dnsHost, dnsPort,
		adaptiveGatewayHost, adaptiveGatewayPort, originalGatewayHost, originalGatewayPort,
		ports.guard, ports.direct, ports.proxy, ports.original))
}

func runMihomoConfigTest(ctx context.Context, binary, home, configPath string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, "-t", "-d", home, "-f", configPath)
	command.Env = isolatedEnvironment()
	return command.CombinedOutput()
}

type sidecarInstance struct {
	cancel   context.CancelFunc
	listener net.Listener
	done     chan error
}

func startSidecar(parent context.Context, address string, server sidecar.Server) (*sidecarInstance, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen SmartRoute sidecar: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	instance := &sidecarInstance{cancel: cancel, listener: listener, done: make(chan error, 1)}
	go func() { instance.done <- server.Serve(ctx, listener) }()
	return instance, nil
}

func (s *sidecarInstance) Stop() error {
	s.cancel()
	_ = s.listener.Close()
	select {
	case err := <-s.done:
		return err
	case <-time.After(time.Second):
		return errors.New("timed out waiting for sidecar shutdown")
	}
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

func eventCounts(mu *sync.Mutex, events *[]sidecar.DecisionEvent, guardEvents *[]guard.DecisionEvent) (int, int) {
	mu.Lock()
	defer mu.Unlock()
	return len(*events), len(*guardEvents)
}

func waitForEventAfter(ctx context.Context, mu *sync.Mutex, events *[]sidecar.DecisionEvent, start int, host string) (sidecar.DecisionEvent, bool) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		mu.Lock()
		for _, event := range (*events)[start:] {
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

func waitForGuardEventAfter(ctx context.Context, mu *sync.Mutex, events *[]guard.DecisionEvent, start int, host string) (guard.DecisionEvent, bool) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		mu.Lock()
		for _, event := range (*events)[start:] {
			if event.Target.Hostname == host {
				mu.Unlock()
				return event, true
			}
		}
		mu.Unlock()
		select {
		case <-ctx.Done():
			return guard.DecisionEvent{}, false
		case <-ticker.C:
		}
	}
}

func allocatePorts() (portSet, error) {
	listeners := make([]net.Listener, 0, 6)
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	ports := make([]int, 0, 6)
	for range 6 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return portSet{}, fmt.Errorf("allocate isolated Mihomo port: %w", err)
		}
		listeners = append(listeners, listener)
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	return portSet{
		front: ports[0], direct: ports[1], proxy: ports[2], sidecar: ports[3],
		guard: ports[4], original: ports[5],
	}, nil
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
