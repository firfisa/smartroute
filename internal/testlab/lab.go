// Package testlab provides a deterministic, loopback-only SmartRoute
// integration environment. It never reads or modifies a Clash installation.
package testlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/sidecar"
	"github.com/firfisa/smartroute/internal/socks5"
	"github.com/firfisa/smartroute/internal/transport"
)

const (
	testHostname         = "echo.test"
	CurrentReportVersion = 1
)

type Report struct {
	ReportVersion int              `json:"report_version"`
	GeneratedAt   time.Time        `json:"generated_at"`
	Isolation     IsolationResult  `json:"isolation"`
	Scenarios     []ScenarioResult `json:"scenarios"`
	Passed        bool             `json:"passed"`
}

type IsolationResult struct {
	LoopbackOnly       bool `json:"loopback_only"`
	EphemeralPortsOnly bool `json:"ephemeral_ports_only"`
	ExternalNetwork    bool `json:"external_network"`
	ClashFilesRead     bool `json:"clash_files_read"`
	ClashFilesWritten  bool `json:"clash_files_written"`
}

type ScenarioResult struct {
	Name            string     `json:"name"`
	ExpectedPath    model.Path `json:"expected_path,omitempty"`
	SelectedPath    model.Path `json:"selected_path,omitempty"`
	ReasonCode      string     `json:"reason_code,omitempty"`
	DirectAttempts  int        `json:"direct_attempts"`
	ProxyAttempts   int        `json:"proxy_attempts"`
	DomainPreserved bool       `json:"domain_preserved"`
	PayloadVerified bool       `json:"payload_verified"`
	FailureExpected bool       `json:"failure_expected"`
	FailureObserved bool       `json:"failure_observed"`
	ElapsedMS       int64      `json:"elapsed_ms"`
	Passed          bool       `json:"passed"`
	FailureReason   string     `json:"failure_reason,omitempty"`
}

type scenarioSpec struct {
	name            string
	direct          gatewayBehavior
	proxy           gatewayBehavior
	expectedPath    model.Path
	failureExpected bool
	headStart       time.Duration
}

type gatewayBehavior struct {
	delay time.Duration
	fail  bool
}

// EchoTarget is a loopback-only TCP echo service reusable by higher-level
// integration labs.
type EchoTarget struct {
	server *echoServer
}

func StartEchoTarget(ctx context.Context) (*EchoTarget, error) {
	server, err := startEchoServer(ctx)
	if err != nil {
		return nil, err
	}
	return &EchoTarget{server: server}, nil
}

func (t *EchoTarget) Address() string { return t.server.Address() }
func (t *EchoTarget) Port() uint16    { return t.server.Port() }
func (t *EchoTarget) Close()          { t.server.Close() }

// SOCKSGateway is a loopback-only, no-authentication SOCKS5 gateway that maps
// every accepted target to one synthetic loopback destination while recording
// whether the domain-form target was preserved.
type SOCKSGateway struct {
	gateway *fakeGateway
}

func StartSOCKSGateway(ctx context.Context, targetAddress string) (*SOCKSGateway, error) {
	gateway, err := startGateway(ctx, targetAddress, gatewayBehavior{})
	if err != nil {
		return nil, err
	}
	return &SOCKSGateway{gateway: gateway}, nil
}

func (g *SOCKSGateway) Address() string { return g.gateway.Address() }
func (g *SOCKSGateway) Close()          { g.gateway.Close() }
func (g *SOCKSGateway) Stats(expectedHost string) (int, bool) {
	return g.gateway.Stats(expectedHost)
}
func (g *SOCKSGateway) Snapshot() (attempts int, lastHost string) {
	return g.gateway.Snapshot()
}

// RunAll runs the first deterministic data-plane matrix. All sockets are
// created inside this process on OS-assigned loopback ports.
func RunAll(ctx context.Context) (Report, error) {
	specs := []scenarioSpec{
		{
			name: "direct_candidate_before_head_start", direct: gatewayBehavior{},
			proxy:        gatewayBehavior{delay: 80 * time.Millisecond},
			expectedPath: model.PathDirect, headStart: 30 * time.Millisecond,
		},
		{
			name: "proxy_recovers_slow_direct", direct: gatewayBehavior{delay: 200 * time.Millisecond},
			proxy:        gatewayBehavior{delay: 5 * time.Millisecond},
			expectedPath: model.PathProxy, headStart: 25 * time.Millisecond,
		},
		{
			name: "both_paths_fail", direct: gatewayBehavior{fail: true},
			proxy:           gatewayBehavior{fail: true},
			failureExpected: true, headStart: 20 * time.Millisecond,
		},
	}

	report := Report{
		ReportVersion: CurrentReportVersion,
		GeneratedAt:   time.Now().UTC(),
		Isolation: IsolationResult{
			LoopbackOnly: true, EphemeralPortsOnly: true, ExternalNetwork: false,
			ClashFilesRead: false, ClashFilesWritten: false,
		},
		Passed: true,
	}
	for _, spec := range specs {
		result, err := runScenario(ctx, spec)
		if err != nil {
			report.Passed = false
			return report, fmt.Errorf("scenario %s: %w", spec.name, err)
		}
		report.Scenarios = append(report.Scenarios, result)
		report.Passed = report.Passed && result.Passed
	}
	if !report.Passed {
		return report, errors.New("one or more isolated test-lab scenarios failed")
	}
	return report, nil
}

func runScenario(parent context.Context, spec scenarioSpec) (ScenarioResult, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	started := time.Now()

	echo, err := startEchoServer(ctx)
	if err != nil {
		return ScenarioResult{}, err
	}
	defer echo.Close()
	direct, err := startGateway(ctx, echo.Address(), spec.direct)
	if err != nil {
		return ScenarioResult{}, err
	}
	defer direct.Close()
	proxy, err := startGateway(ctx, echo.Address(), spec.proxy)
	if err != nil {
		return ScenarioResult{}, err
	}
	defer proxy.Close()

	listener, err := listenLoopback()
	if err != nil {
		return ScenarioResult{}, fmt.Errorf("listen sidecar: %w", err)
	}
	defer listener.Close()
	events := make(chan sidecar.DecisionEvent, 1)
	server := sidecar.Server{
		NetworkProfileID: "isolated-test-lab",
		HandshakeTimeout: time.Second,
		Racer: transport.Racer{
			Direct: transport.SOCKS5Dialer{
				Path: model.PathDirect, Endpoint: direct.Address(), ReadinessStage: model.StageTCP,
			},
			Proxy: transport.SOCKS5Dialer{
				Path: model.PathProxy, Endpoint: proxy.Address(), ReadinessStage: model.StageTCP,
			},
			HeadStart: spec.headStart,
			Timeout:   time.Second,
		},
		OnDecision: func(event sidecar.DecisionEvent) { events <- event },
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(ctx, listener) }()

	result := ScenarioResult{
		Name: spec.name, ExpectedPath: spec.expectedPath,
		FailureExpected: spec.failureExpected,
	}
	client, dialErr := socks5.DialContext(ctx, listener.Addr().String(), socks5.Request{
		Host: testHostname, Port: echo.Port(),
	})
	if dialErr != nil {
		result.FailureObserved = true
		if !spec.failureExpected {
			result.FailureReason = dialErr.Error()
		}
	} else {
		defer client.Close()
		payload := []byte("smartroute-isolated-test")
		if _, err := client.Write(payload); err != nil {
			result.FailureReason = fmt.Sprintf("write payload: %v", err)
		} else {
			received := make([]byte, len(payload))
			if _, err := io.ReadFull(client, received); err != nil {
				result.FailureReason = fmt.Sprintf("read payload: %v", err)
			} else {
				result.PayloadVerified = string(received) == string(payload)
			}
		}
		select {
		case event := <-events:
			result.SelectedPath = event.SelectedPath
			result.ReasonCode = event.ReasonCode
		case <-ctx.Done():
			result.FailureReason = "decision event timed out"
		}
	}

	result.DirectAttempts, result.DomainPreserved = direct.Stats(testHostname)
	result.ProxyAttempts, _ = proxy.Stats(testHostname)
	if result.SelectedPath == model.PathProxy {
		_, result.DomainPreserved = proxy.Stats(testHostname)
	}
	result.ElapsedMS = time.Since(started).Milliseconds()
	if spec.failureExpected {
		result.Passed = result.FailureObserved && result.DirectAttempts == 1 && result.ProxyAttempts == 1
	} else {
		result.Passed = !result.FailureObserved && result.SelectedPath == spec.expectedPath &&
			result.PayloadVerified && result.DomainPreserved
		if spec.expectedPath == model.PathDirect {
			result.Passed = result.Passed && result.ProxyAttempts == 0
		}
	}
	if !result.Passed && result.FailureReason == "" {
		encoded, _ := json.Marshal(result)
		result.FailureReason = "invariant mismatch: " + string(encoded)
	}
	return result, nil
}

func listenLoopback() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}

type echoServer struct {
	listener net.Listener
	cancel   context.CancelFunc
	closed   sync.Once
}

func startEchoServer(parent context.Context) (*echoServer, error) {
	listener, err := listenLoopback()
	if err != nil {
		return nil, fmt.Errorf("listen echo target: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	server := &echoServer{listener: listener, cancel: cancel}
	go server.serve(ctx)
	return server, nil
}

func (s *echoServer) Address() string { return s.listener.Addr().String() }

func (s *echoServer) Port() uint16 {
	return uint16(s.listener.Addr().(*net.TCPAddr).Port)
}

func (s *echoServer) Close() {
	s.closed.Do(func() {
		s.cancel()
		_ = s.listener.Close()
	})
}

func (s *echoServer) serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = s.listener.Close()
	}()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}()
	}
}

type fakeGateway struct {
	listener      net.Listener
	targetAddress string
	behavior      gatewayBehavior
	cancel        context.CancelFunc
	closed        sync.Once
	mu            sync.Mutex
	attempts      int
	lastHost      string
}

func startGateway(parent context.Context, targetAddress string, behavior gatewayBehavior) (*fakeGateway, error) {
	if err := requireLoopback(targetAddress); err != nil {
		return nil, err
	}
	listener, err := listenLoopback()
	if err != nil {
		return nil, fmt.Errorf("listen fake gateway: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	gateway := &fakeGateway{
		listener: listener, targetAddress: targetAddress,
		behavior: behavior, cancel: cancel,
	}
	go gateway.serve(ctx)
	return gateway, nil
}

func (g *fakeGateway) Address() string { return g.listener.Addr().String() }

func (g *fakeGateway) Close() {
	g.closed.Do(func() {
		g.cancel()
		_ = g.listener.Close()
	})
}

func (g *fakeGateway) Stats(expectedHost string) (int, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.attempts, g.attempts > 0 && g.lastHost == expectedHost
}

func (g *fakeGateway) Snapshot() (attempts int, lastHost string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.attempts, g.lastHost
}

func (g *fakeGateway) serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = g.listener.Close()
	}()
	for {
		conn, err := g.listener.Accept()
		if err != nil {
			return
		}
		go g.handle(ctx, conn)
	}
}

func (g *fakeGateway) handle(ctx context.Context, inbound net.Conn) {
	defer inbound.Close()
	request, err := socks5.ReadRequest(inbound)
	if err != nil {
		return
	}
	g.mu.Lock()
	g.attempts++
	g.lastHost = request.Host
	g.mu.Unlock()

	if g.behavior.delay > 0 {
		timer := time.NewTimer(g.behavior.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
	}
	if g.behavior.fail {
		_ = socks5.WriteReply(inbound, socks5.ReplyConnectionRefused)
		return
	}
	outbound, err := (&net.Dialer{}).DialContext(ctx, "tcp", g.targetAddress)
	if err != nil {
		_ = socks5.WriteReply(inbound, socks5.ReplyGeneralFailure)
		return
	}
	defer outbound.Close()
	if err := socks5.WriteReply(inbound, socks5.ReplySucceeded); err != nil {
		return
	}
	relay(inbound, outbound)
}

func relay(left, right net.Conn) {
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, _ = io.Copy(right, left)
		if tcp, ok := right.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	go func() {
		defer wait.Done()
		_, _ = io.Copy(left, right)
		if tcp, ok := left.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	wait.Wait()
}

func requireLoopback(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid test-lab address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("test lab refuses non-loopback address %q", address)
	}
	return nil
}
