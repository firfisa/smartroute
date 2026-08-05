package mihomolab

import (
	"bytes"
	"context"
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
	"sync/atomic"
	"time"

	"github.com/firfisa/smartroute/internal/config"
	"github.com/firfisa/smartroute/internal/guard"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/socks5"
	"github.com/firfisa/smartroute/internal/store"
	"github.com/firfisa/smartroute/internal/testlab"
	"github.com/firfisa/smartroute/internal/tlsinspect"
	"github.com/firfisa/smartroute/internal/transport"
)

const (
	RuntimeReportVersion = 1
	runtimeProfileID     = "isolated-runtime-lab"
)

type RuntimeOptions struct {
	MihomoBinary     string
	SmartRouteBinary string
	NodeBinary       string
	ComposerScript   string
	ApplyScript      string
}

type RuntimeReport struct {
	ReportVersion                 int                     `json:"report_version"`
	GeneratedAt                   time.Time               `json:"generated_at"`
	MihomoVersion                 string                  `json:"mihomo_version"`
	SmartRouteVersion             string                  `json:"smartroute_version"`
	Isolation                     RuntimeIsolation        `json:"isolation"`
	TransformApplied              bool                    `json:"transform_applied"`
	MihomoConfigValidated         bool                    `json:"mihomo_config_validated"`
	SupervisorProcessVerified     bool                    `json:"supervisor_process_verified"`
	PolicyOnlyPersistenceVerified bool                    `json:"policy_only_persistence_verified"`
	RestartPersistenceVerified    bool                    `json:"restart_persistence_verified"`
	OppositeOverwriteVerified     bool                    `json:"opposite_overwrite_verified"`
	Scenarios                     []RuntimeScenarioResult `json:"scenarios"`
	Passed                        bool                    `json:"passed"`
}

type RuntimeIsolation struct {
	TemporaryWorkspace  bool `json:"temporary_workspace"`
	LoopbackOnly        bool `json:"loopback_only"`
	EphemeralPortsOnly  bool `json:"ephemeral_ports_only"`
	ExternalNetwork     bool `json:"external_network"`
	TUNEnabled          bool `json:"tun_enabled"`
	SystemProxyModified bool `json:"system_proxy_modified"`
	ActiveClashRead     bool `json:"active_clash_read"`
	ActiveClashWritten  bool `json:"active_clash_written"`
}

type RuntimeScenarioResult struct {
	Name                  string     `json:"name"`
	ExpectedPath          model.Path `json:"expected_path"`
	SelectedPath          model.Path `json:"selected_path,omitempty"`
	ReasonCode            string     `json:"reason_code,omitempty"`
	LearningReason        string     `json:"learning_reason,omitempty"`
	DurableReason         string     `json:"durable_reason,omitempty"`
	GuardLane             string     `json:"guard_lane,omitempty"`
	GuardReason           string     `json:"guard_reason,omitempty"`
	ProxyAttempts         int        `json:"proxy_attempts"`
	DirectTargetAccepts   int        `json:"direct_target_accepts"`
	DirectTripwireAccepts int        `json:"direct_tripwire_accepts"`
	ReadinessVerified     bool       `json:"readiness_verified"`
	MappingAfterStop      model.Path `json:"mapping_after_stop,omitempty"`
	Passed                bool       `json:"passed"`
	ObservedError         string     `json:"observed_error,omitempty"`
}

// RunRuntime executes the actual composed Clash transform, pinned Mihomo,
// smartroute supervise process, and SQLite restart path inside one removed
// loopback-only workspace. It never discovers the active Clash installation.
func RunRuntime(parent context.Context, options RuntimeOptions) (RuntimeReport, error) {
	report := RuntimeReport{
		ReportVersion: RuntimeReportVersion,
		GeneratedAt:   time.Now().UTC(),
		Isolation: RuntimeIsolation{
			TemporaryWorkspace: true, LoopbackOnly: true, EphemeralPortsOnly: true,
			ExternalNetwork: false, TUNEnabled: false, SystemProxyModified: false,
			ActiveClashRead: false, ActiveClashWritten: false,
		},
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()

	mihomoBinary, err := validateBinary(options.MihomoBinary)
	if err != nil {
		return report, err
	}
	report.MihomoVersion, err = binaryVersion(ctx, mihomoBinary)
	if err != nil {
		return report, err
	}
	smartRouteBinary, err := resolveExecutable(options.SmartRouteBinary, "SmartRoute")
	if err != nil {
		return report, err
	}
	report.SmartRouteVersion, err = smartRouteVersion(ctx, smartRouteBinary)
	if err != nil {
		return report, err
	}
	nodeBinary, err := resolveExecutable(options.NodeBinary, "Node.js")
	if err != nil {
		return report, err
	}
	composerScript, err := validateRuntimeScript(options.ComposerScript, "composer")
	if err != nil {
		return report, err
	}
	applyScript, err := validateRuntimeScript(options.ApplyScript, "apply")
	if err != nil {
		return report, err
	}

	workspace, err := os.MkdirTemp("", "smartroute-runtime-lab-*")
	if err != nil {
		return report, fmt.Errorf("create runtime lab workspace: %w", err)
	}
	defer os.RemoveAll(workspace)
	mihomoHome := filepath.Join(workspace, "mihomo-home")
	if err := os.Mkdir(mihomoHome, 0o700); err != nil {
		return report, fmt.Errorf("create runtime Mihomo home: %w", err)
	}

	directTarget, err := testlab.StartTLSTarget(ctx)
	if err != nil {
		return report, err
	}
	directTargetAddress := directTarget.Address()
	directTargetPort := directTarget.Port()
	defer directTarget.Close()
	proxyTarget, err := testlab.StartTLSTarget(ctx)
	if err != nil {
		return report, err
	}
	defer proxyTarget.Close()
	adaptiveGateway, err := testlab.StartSOCKSGateway(ctx, proxyTarget.Address())
	if err != nil {
		return report, err
	}
	defer adaptiveGateway.Close()
	originalGateway, err := testlab.StartSOCKSGateway(ctx, proxyTarget.Address())
	if err != nil {
		return report, err
	}
	defer originalGateway.Close()
	dnsServer, err := startDNSServerFor(ctx, [4]byte{127, 0, 0, 1})
	if err != nil {
		return report, err
	}
	defer dnsServer.Close()
	ports, err := allocatePorts()
	if err != nil {
		return report, err
	}

	baseConfig := runtimeBaseConfig(ports.front, adaptiveGateway.Address(), originalGateway.Address(), dnsServer.Address())
	candidateConfig, err := composeRuntimeCandidate(
		ctx, nodeBinary, composerScript, applyScript, workspace, ports, baseConfig,
	)
	if err != nil {
		return report, err
	}
	if err := validateRuntimeCandidate(candidateConfig, ports); err != nil {
		return report, err
	}
	report.TransformApplied = true
	candidatePath := filepath.Join(workspace, "candidate.json")
	if err := os.WriteFile(candidatePath, candidateConfig, 0o600); err != nil {
		return report, fmt.Errorf("write runtime candidate config: %w", err)
	}
	if output, err := runMihomoConfigTest(ctx, mihomoBinary, mihomoHome, candidatePath); err != nil {
		return report, fmt.Errorf("runtime candidate Mihomo validation: %w: %s", err, sanitizeProcessOutput(output))
	}
	report.MihomoConfigValidated = true

	mihomo, err := startMihomo(mihomoBinary, mihomoHome, candidatePath)
	if err != nil {
		return report, err
	}
	defer mihomo.Stop()
	if err := mihomo.WaitReady(ctx, loopbackAddress(ports.front)); err != nil {
		return report, err
	}

	smartRouteConfigPath, databasePath, err := writeRuntimeSmartRouteConfig(workspace, ports)
	if err != nil {
		return report, err
	}
	target := model.Target{
		NetworkProfileID: runtimeProfileID, Hostname: mappedHostname,
		Port: directTargetPort, Transport: model.TransportTCP,
	}
	frontEndpoint := loopbackAddress(ports.front)

	firstProcess, err := startRuntimeSupervisor(ctx, smartRouteBinary, smartRouteConfigPath)
	if err != nil {
		return report, err
	}
	if err := firstProcess.WaitReady(ctx, ports); err != nil {
		firstProcess.Stop()
		return report, err
	}
	first := runRuntimeScenario(ctx, firstProcess, frontEndpoint, target, "first_ready_direct", model.PathDirect, directTarget, adaptiveGateway, nil)
	if err := firstProcess.Stop(); err != nil {
		return report, err
	}
	first.MappingAfterStop, report.PolicyOnlyPersistenceVerified, err = inspectRuntimePolicy(ctx, databasePath, target)
	if err != nil {
		return report, err
	}
	first.Passed = first.ReadinessVerified && first.SelectedPath == model.PathDirect &&
		first.ReasonCode == transport.ReasonDirectCandidateBeforeHeadStart && first.ProxyAttempts == 0 &&
		first.DirectTargetAccepts == 1 && first.GuardLane == guard.LaneAdaptive &&
		first.MappingAfterStop == model.PathDirect && report.PolicyOnlyPersistenceVerified
	report.Scenarios = append(report.Scenarios, first)

	secondProcess, err := startRuntimeSupervisor(ctx, smartRouteBinary, smartRouteConfigPath)
	if err != nil {
		return report, err
	}
	if err := secondProcess.WaitReady(ctx, ports); err != nil {
		secondProcess.Stop()
		return report, err
	}
	second := runRuntimeScenario(ctx, secondProcess, frontEndpoint, target, "restart_reuses_direct", model.PathDirect, directTarget, adaptiveGateway, nil)
	second.Passed = second.ReadinessVerified && second.SelectedPath == model.PathDirect &&
		second.ReasonCode == transport.ReasonDurablePolicySelected && second.ProxyAttempts == 0 &&
		second.DirectTargetAccepts == 1 && second.GuardLane == guard.LaneAdaptive
	report.Scenarios = append(report.Scenarios, second)
	report.RestartPersistenceVerified = second.Passed

	directTarget.Close()
	third := runRuntimeScenario(ctx, secondProcess, frontEndpoint, target, "direct_failure_overwrites_proxy", model.PathProxy, directTarget, adaptiveGateway, nil)
	if err := secondProcess.Stop(); err != nil {
		return report, err
	}
	third.MappingAfterStop, report.PolicyOnlyPersistenceVerified, err = inspectRuntimePolicy(ctx, databasePath, target)
	if err != nil {
		return report, err
	}
	third.Passed = third.ReadinessVerified && third.SelectedPath == model.PathProxy &&
		third.ReasonCode == transport.ReasonDurablePolicyFallback && third.ProxyAttempts == 1 &&
		third.GuardLane == guard.LaneAdaptive && third.MappingAfterStop == model.PathProxy &&
		report.PolicyOnlyPersistenceVerified
	report.Scenarios = append(report.Scenarios, third)
	report.OppositeOverwriteVerified = third.Passed

	tripwire, err := startDirectTripwire(ctx, directTargetAddress)
	if err != nil {
		return report, err
	}
	defer tripwire.Close()
	thirdProcess, err := startRuntimeSupervisor(ctx, smartRouteBinary, smartRouteConfigPath)
	if err != nil {
		return report, err
	}
	if err := thirdProcess.WaitReady(ctx, ports); err != nil {
		thirdProcess.Stop()
		return report, err
	}
	fourth := runRuntimeScenario(ctx, thirdProcess, frontEndpoint, target, "restart_reuses_proxy", model.PathProxy, directTarget, adaptiveGateway, tripwire)
	if err := thirdProcess.Stop(); err != nil {
		return report, err
	}
	fourth.MappingAfterStop, report.PolicyOnlyPersistenceVerified, err = inspectRuntimePolicy(ctx, databasePath, target)
	if err != nil {
		return report, err
	}
	fourth.Passed = fourth.ReadinessVerified && fourth.SelectedPath == model.PathProxy &&
		fourth.ReasonCode == transport.ReasonDurablePolicySelected && fourth.ProxyAttempts == 1 &&
		fourth.DirectTripwireAccepts == 0 && fourth.GuardLane == guard.LaneAdaptive &&
		fourth.MappingAfterStop == model.PathProxy && report.PolicyOnlyPersistenceVerified
	report.Scenarios = append(report.Scenarios, fourth)
	report.RestartPersistenceVerified = report.RestartPersistenceVerified && fourth.Passed

	originalAttempts, _ := originalGateway.Snapshot()
	report.SupervisorProcessVerified = first.GuardLane == guard.LaneAdaptive && second.GuardLane == guard.LaneAdaptive &&
		third.GuardLane == guard.LaneAdaptive && fourth.GuardLane == guard.LaneAdaptive && originalAttempts == 0
	report.Passed = report.TransformApplied && report.MihomoConfigValidated && report.SupervisorProcessVerified &&
		report.PolicyOnlyPersistenceVerified && report.RestartPersistenceVerified && report.OppositeOverwriteVerified && mihomo.Running()
	for _, scenario := range report.Scenarios {
		report.Passed = report.Passed && scenario.Passed
	}
	if !report.Passed {
		encoded, _ := json.Marshal(report)
		return report, fmt.Errorf("runtime lab invariant failed: %s", encoded)
	}
	return report, nil
}

func runtimeBaseConfig(frontPort int, adaptiveGatewayAddress, originalGatewayAddress, dnsAddress string) map[string]any {
	adaptiveHost, adaptivePort, _ := net.SplitHostPort(adaptiveGatewayAddress)
	originalHost, originalPort, _ := net.SplitHostPort(originalGatewayAddress)
	return map[string]any{
		"mixed-port": frontPort, "bind-address": "127.0.0.1", "allow-lan": false,
		"mode": "rule", "log-level": "warning", "ipv6": false,
		"tun": map[string]any{"enable": false},
		"dns": map[string]any{
			"enable": true, "listen": "", "ipv6": false, "use-hosts": false,
			"use-system-hosts": false, "enhanced-mode": "redir-host", "nameserver": []string{dnsAddress},
		},
		"proxies": []map[string]any{
			{"name": "LAB-PROXY", "type": "socks5", "server": adaptiveHost, "port": mustPort(adaptivePort), "udp": false},
			{"name": "ORIGINAL-PROXY", "type": "socks5", "server": originalHost, "port": mustPort(originalPort), "udp": false},
		},
		"proxy-groups": []map[string]any{
			{"name": "PROXY-BRANCH", "type": "select", "proxies": []string{"LAB-PROXY"}},
			{"name": "DIRECT-BRANCH", "type": "select", "proxies": []string{"DIRECT"}},
			{"name": "ROOT", "type": "select", "proxies": []string{"PROXY-BRANCH", "DIRECT-BRANCH"}},
		},
		"rules": []string{"MATCH,ROOT"},
	}
}

func mustPort(value string) int {
	port, _ := strconv.Atoi(value)
	return port
}

func composeRuntimeCandidate(ctx context.Context, node, composer, apply, workspace string, ports portSet, base map[string]any) ([]byte, error) {
	baseScript := filepath.Join(workspace, "base.js")
	composedScript := filepath.Join(workspace, "composed.js")
	if err := os.WriteFile(baseScript, []byte("function main(config) { return config; }\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write runtime base script: %w", err)
	}
	command := exec.CommandContext(ctx, node, composer,
		"--base", baseScript, "--output", composedScript,
		"--guard-port", fmt.Sprint(ports.guard), "--direct-port", fmt.Sprint(ports.direct),
		"--proxy-port", fmt.Sprint(ports.proxy), "--original-port", fmt.Sprint(ports.original),
	)
	command.Env = isolatedEnvironment()
	if output, err := command.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("compose runtime Clash script: %w: %s", err, sanitizeProcessOutput(output))
	}
	encoded, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("encode runtime base config: %w", err)
	}
	command = exec.CommandContext(ctx, node, apply, "--script", composedScript)
	command.Env = isolatedEnvironment()
	command.Stdin = bytes.NewReader(encoded)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("apply runtime Clash script: %w: %s", err, sanitizeProcessOutput(stderr.Bytes()))
	}
	if stdout.Len() > 4<<20 {
		return nil, errors.New("runtime transformed config exceeded 4 MiB")
	}
	return stdout.Bytes(), nil
}

func validateRuntimeCandidate(encoded []byte, ports portSet) error {
	var candidate struct {
		Mode    string `json:"mode"`
		Rules   []string
		Proxies []struct {
			Name, Type, Server string
			Port               int
			UDP                bool
		}
		Listeners []struct {
			Name, Type, Listen, Proxy string
			Port                      int
			UDP                       bool
		}
		TUN struct{ Enable bool }
	}
	if err := json.Unmarshal(encoded, &candidate); err != nil {
		return fmt.Errorf("decode runtime transformed config: %w", err)
	}
	if candidate.Mode != "rule" || candidate.TUN.Enable || len(candidate.Rules) != 1 || candidate.Rules[0] != "MATCH,SMARTROUTE-GUARD-ADAPTER" {
		return errors.New("runtime transformed config has unexpected mode, TUN, or final MATCH")
	}
	adapterOK := false
	for _, proxy := range candidate.Proxies {
		if proxy.Name == "SMARTROUTE-GUARD-ADAPTER" {
			adapterOK = proxy.Type == "socks5" && proxy.Server == "127.0.0.1" && proxy.Port == ports.guard && !proxy.UDP
		}
	}
	expected := map[string]struct {
		port  int
		proxy string
	}{
		"smartroute-direct":   {ports.direct, "DIRECT"},
		"smartroute-proxy":    {ports.proxy, "PROXY-BRANCH"},
		"smartroute-original": {ports.original, "ROOT"},
	}
	seen := make(map[string]bool, len(expected))
	for _, listener := range candidate.Listeners {
		want, ok := expected[listener.Name]
		if ok && listener.Type == "mixed" && listener.Listen == "127.0.0.1" && listener.Port == want.port && listener.Proxy == want.proxy && !listener.UDP {
			seen[listener.Name] = true
		}
	}
	if !adapterOK || len(seen) != len(expected) {
		return errors.New("runtime transformed config is missing the exact Guard/Direct/Proxy/Original objects")
	}
	return nil
}

func writeRuntimeSmartRouteConfig(workspace string, ports portSet) (string, string, error) {
	cfg := config.Default()
	cfg.ListenAddress = loopbackAddress(ports.sidecar)
	cfg.GuardListenAddress = loopbackAddress(ports.guard)
	cfg.DirectEndpoint = loopbackAddress(ports.direct)
	cfg.ProxyEndpoint = loopbackAddress(ports.proxy)
	cfg.OriginalEndpoint = loopbackAddress(ports.original)
	cfg.GuardAdaptiveTimeoutMS = 500
	cfg.Decision.DirectHeadStartMS = 100
	cfg.Decision.MaxDirectPenaltyMS = 0
	cfg.Decision.CandidateTimeoutMS = 2000
	cfg.Learning.MaxEntries = 64
	cfg.Learning.Persistence.QueueSize = 8
	cfg.Learning.Persistence.ShutdownTimeoutMS = 2000
	databasePath := filepath.Join(workspace, "learning.db")
	cfg.Learning.Persistence.DatabasePath = databasePath
	cfg.FixedPolicy.DatabasePath = filepath.Join(workspace, "fixed-policies.db")
	cfg.Observation.Enabled = false
	cfg.Observation.Directory = filepath.Join(workspace, "observations")
	if err := cfg.Validate(); err != nil {
		return "", "", fmt.Errorf("validate runtime SmartRoute config: %w", err)
	}
	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("encode runtime SmartRoute config: %w", err)
	}
	path := filepath.Join(workspace, "smartroute.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return "", "", fmt.Errorf("write runtime SmartRoute config: %w", err)
	}
	return path, databasePath, nil
}

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(value)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type runtimeSupervisor struct {
	command *exec.Cmd
	done    chan error
	stdout  *synchronizedBuffer
	stderr  *synchronizedBuffer
	stop    sync.Once
	stopErr error
}

func startRuntimeSupervisor(ctx context.Context, binary, configPath string) (*runtimeSupervisor, error) {
	stdout, stderr := &synchronizedBuffer{}, &synchronizedBuffer{}
	command := exec.CommandContext(ctx, binary,
		"supervise", "-config", configPath, "-network-profile", runtimeProfileID,
		"-acknowledge-direct-probes", "-restart-min-backoff", "20ms",
		"-restart-max-backoff", "100ms", "-restart-stable-after", "2s", "-shutdown-grace", "2s",
	)
	command.Env = isolatedEnvironment()
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start actual SmartRoute supervisor: %w", err)
	}
	process := &runtimeSupervisor{command: command, done: make(chan error, 1), stdout: stdout, stderr: stderr}
	go func() { process.done <- command.Wait() }()
	return process, nil
}

func (p *runtimeSupervisor) WaitReady(ctx context.Context, ports portSet) error {
	for _, address := range []string{loopbackAddress(ports.guard), loopbackAddress(ports.sidecar)} {
		if err := waitProcessListener(ctx, p.done, address); err != nil {
			return fmt.Errorf("wait for actual SmartRoute process at %s: %w: %s", address, err, sanitizeProcessOutput([]byte(p.stderr.String())))
		}
	}
	return nil
}

func waitProcessListener(ctx context.Context, done <-chan error, address string) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := net.DialTimeout("tcp", address, 40*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case processErr := <-done:
			return fmt.Errorf("process exited before ready: %v", processErr)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (p *runtimeSupervisor) Stop() error {
	p.stop.Do(func() {
		if p.command.Process == nil {
			return
		}
		_ = p.command.Process.Signal(os.Interrupt)
		select {
		case err := <-p.done:
			if err != nil {
				p.stopErr = fmt.Errorf("actual SmartRoute supervisor stopped with error: %w: %s", err, sanitizeProcessOutput([]byte(p.stderr.String())))
			}
		case <-time.After(6 * time.Second):
			_ = p.command.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(time.Second):
			}
			p.stopErr = errors.New("timed out stopping actual SmartRoute supervisor")
		}
	})
	return p.stopErr
}

type runtimeWireEvent struct {
	EventType      string            `json:"event_type"`
	Target         model.Target      `json:"target"`
	SelectedPath   model.Path        `json:"selected_path"`
	SelectedLane   string            `json:"selected_lane"`
	ReasonCode     string            `json:"reason_code"`
	LearningReason string            `json:"learning_reason"`
	DurableReason  string            `json:"durable_reason"`
	Observation    model.Observation `json:"observation"`
	Committed      bool              `json:"committed"`
}

func parseRuntimeEvents(value string, eventType string) []runtimeWireEvent {
	var events []runtimeWireEvent
	for _, line := range strings.Split(value, "\n") {
		var event runtimeWireEvent
		if json.Unmarshal([]byte(line), &event) == nil && event.EventType == eventType {
			events = append(events, event)
		}
	}
	return events
}

func waitRuntimeEvent(ctx context.Context, process *runtimeSupervisor, eventType string, start int) (runtimeWireEvent, bool) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		events := parseRuntimeEvents(process.stdout.String(), eventType)
		if len(events) > start {
			return events[start], true
		}
		select {
		case <-ctx.Done():
			return runtimeWireEvent{}, false
		case <-ticker.C:
		}
	}
}

func runRuntimeScenario(ctx context.Context, process *runtimeSupervisor, front string, target model.Target, name string, expected model.Path, direct *testlab.TLSTarget, proxy *testlab.SOCKSGateway, tripwire *directTripwire) RuntimeScenarioResult {
	result := RuntimeScenarioResult{Name: name, ExpectedPath: expected}
	decisionStart := len(parseRuntimeEvents(process.stdout.String(), "decision"))
	guardStart := len(parseRuntimeEvents(process.stdout.String(), "guard_decision"))
	proxyBefore, _ := proxy.Snapshot()
	directBefore := direct.AcceptedClientHellos()
	tripwireBefore := 0
	if tripwire != nil {
		tripwireBefore = tripwire.Accepts()
	}
	client, err := socks5.DialContext(ctx, front, socks5.Request{Host: target.Hostname, Port: target.Port})
	if err != nil {
		result.ObservedError = err.Error()
		return result
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write(testlab.SyntheticClientHelloRecords()); err != nil {
		result.ObservedError = fmt.Sprintf("write ClientHello: %v", err)
		return result
	}
	hello, err := tlsinspect.ReadServerHello(client, 0)
	if err != nil {
		result.ObservedError = fmt.Sprintf("read ServerHello: %v", err)
	} else {
		result.ReadinessVerified = bytes.Equal(hello.Wire, testlab.SyntheticServerHelloRecord())
	}
	decision, decisionOK := waitRuntimeEvent(ctx, process, "decision", decisionStart)
	guardEvent, guardOK := waitRuntimeEvent(ctx, process, "guard_decision", guardStart)
	if decisionOK {
		result.SelectedPath = decision.SelectedPath
		result.ReasonCode = decision.ReasonCode
		result.LearningReason = decision.LearningReason
		result.DurableReason = decision.DurableReason
		result.ReadinessVerified = result.ReadinessVerified && decision.Committed && decision.Observation.StageReached == model.StageTLS
	} else if result.ObservedError == "" {
		result.ObservedError = "missing actual SmartRoute decision event"
	}
	if guardOK {
		result.GuardLane = guardEvent.SelectedLane
		result.GuardReason = guardEvent.ReasonCode
	} else if result.ObservedError == "" {
		result.ObservedError = "missing actual SmartRoute Guard event"
	}
	proxyAfter, _ := proxy.Snapshot()
	result.ProxyAttempts = proxyAfter - proxyBefore
	result.DirectTargetAccepts = direct.AcceptedClientHellos() - directBefore
	if tripwire != nil {
		result.DirectTripwireAccepts = tripwire.Accepts() - tripwireBefore
	}
	return result
}

func inspectRuntimePolicy(ctx context.Context, databasePath string, target model.Target) (model.Path, bool, error) {
	evidenceStore, err := store.OpenReadOnly(ctx, store.Config{Path: databasePath, BusyTimeout: time.Second})
	if err != nil {
		return "", false, fmt.Errorf("open runtime policy store read-only: %w", err)
	}
	defer evidenceStore.Close()
	status, err := evidenceStore.Status(ctx)
	if err != nil {
		return "", false, err
	}
	index, err := evidenceStore.NewDurablePolicyIndex(ctx, 64)
	if err != nil {
		return "", false, err
	}
	policyOnly := status.SessionCount == 0 && status.EvidenceCount == 0 && status.DurablePolicies == 1
	return index.PreferredPath(target), policyOnly, nil
}

type directTripwire struct {
	listener net.Listener
	cancel   context.CancelFunc
	accepts  atomic.Int64
}

func startDirectTripwire(parent context.Context, address string) (*directTripwire, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen Direct tripwire: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	tripwire := &directTripwire{listener: listener, cancel: cancel}
	go func() {
		go func() {
			<-ctx.Done()
			_ = listener.Close()
		}()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			tripwire.accepts.Add(1)
			_ = conn.Close()
		}
	}()
	return tripwire, nil
}

func (t *directTripwire) Accepts() int { return int(t.accepts.Load()) }
func (t *directTripwire) Close() {
	t.cancel()
	_ = t.listener.Close()
}

func resolveExecutable(value, label string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("explicit %s executable is required", label)
	}
	resolved := value
	if !strings.ContainsRune(value, filepath.Separator) {
		path, err := exec.LookPath(value)
		if err != nil {
			return "", fmt.Errorf("resolve %s executable: %w", label, err)
		}
		resolved = path
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve %s executable: %w", label, err)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%s executable must be an executable regular file", label)
	}
	return absolute, nil
}

func validateRuntimeScript(value, label string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("explicit %s script is required", label)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s script must be a regular file", label)
	}
	return absolute, nil
}

func smartRouteVersion(ctx context.Context, binary string) (string, error) {
	command := exec.CommandContext(ctx, binary, "version")
	command.Env = isolatedEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read SmartRoute version: %w", err)
	}
	version := strings.TrimSpace(string(output))
	if !strings.Contains(strings.ToLower(version), "smartroute") {
		return "", fmt.Errorf("unexpected SmartRoute version output %q", version)
	}
	return version, nil
}

var _ io.Writer = (*synchronizedBuffer)(nil)
