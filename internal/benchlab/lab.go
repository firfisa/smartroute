// Package benchlab measures the local connection-and-first-byte overhead of
// inserting the SmartRoute sidecar in front of the same synthetic SOCKS path.
// It uses loopback-only ephemeral listeners and never inspects active Clash.
package benchlab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"sort"
	"sync/atomic"
	"time"

	"github.com/firfisa/smartroute/internal/mihomolab"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/privacy"
	"github.com/firfisa/smartroute/internal/sidecar"
	"github.com/firfisa/smartroute/internal/socks5"
	"github.com/firfisa/smartroute/internal/testlab"
	"github.com/firfisa/smartroute/internal/tlsinspect"
	"github.com/firfisa/smartroute/internal/transport"
)

const CurrentReportVersion = 3

const benchmarkHostname = "echo.test"

type Options struct {
	Runs               int
	Samples            int
	Warmup             int
	MaxP95Overhead     time.Duration
	EnforceLatencyGate bool
	MihomoPath         string
	TLS                bool
}

func DefaultOptions() Options {
	return Options{Runs: 5, Samples: 200, Warmup: 20, MaxP95Overhead: 5 * time.Millisecond}
}

func (o Options) Validate() error {
	if o.Runs < 1 || o.Runs > 100 {
		return errors.New("benchmark runs must be between 1 and 100")
	}
	if o.Samples < 10 || o.Samples > 10000 {
		return errors.New("benchmark samples must be between 10 and 10000")
	}
	if o.Warmup < 0 || o.Warmup > 10000 {
		return errors.New("benchmark warmup must be between 0 and 10000")
	}
	if o.MaxP95Overhead < time.Microsecond || o.MaxP95Overhead > time.Second {
		return errors.New("benchmark max p95 overhead must be between one microsecond and one second")
	}
	return nil
}

type Report struct {
	ReportVersion          int                     `json:"report_version"`
	Tier                   string                  `json:"tier"`
	Protocol               string                  `json:"protocol"`
	GeneratedAt            time.Time               `json:"generated_at"`
	MihomoVersion          string                  `json:"mihomo_version,omitempty"`
	MihomoConfigValidated  bool                    `json:"mihomo_config_validated"`
	MihomoChildHealthy     bool                    `json:"mihomo_child_healthy"`
	Environment            Environment             `json:"environment"`
	Isolation              IsolationResult         `json:"isolation"`
	Measurement            MeasurementContract     `json:"measurement"`
	BaselineUS             MicrosecondDistribution `json:"baseline_us"`
	SidecarUS              MicrosecondDistribution `json:"sidecar_us"`
	PairedOverheadUS       MicrosecondDistribution `json:"paired_overhead_us"`
	Runs                   []RunResult             `json:"runs"`
	Correctness            CorrectnessResult       `json:"correctness"`
	MaxP95OverheadUS       int64                   `json:"max_p95_overhead_us"`
	WorstRunP95OverheadUS  int64                   `json:"worst_run_p95_overhead_us"`
	MeetsP95Gate           bool                    `json:"meets_p95_gate"`
	LatencyGateEnforced    bool                    `json:"latency_gate_enforced"`
	AuthorizesLiveTrial    bool                    `json:"authorizes_live_trial"`
	AuthorizesPolicyChange bool                    `json:"authorizes_policy_change"`
	Passed                 bool                    `json:"passed"`
}

type Environment struct {
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	GoVersion  string `json:"go_version"`
	GOMAXPROCS int    `json:"gomaxprocs"`
}

type IsolationResult struct {
	LoopbackOnly         bool `json:"loopback_only"`
	EphemeralPortsOnly   bool `json:"ephemeral_ports_only"`
	ExternalNetwork      bool `json:"external_network"`
	ActiveClashRead      bool `json:"active_clash_read"`
	ActiveClashWritten   bool `json:"active_clash_written"`
	SystemProxyModified  bool `json:"system_proxy_modified"`
	DedicatedMihomoChild bool `json:"dedicated_mihomo_child"`
	TemporaryMihomoHome  bool `json:"temporary_mihomo_home"`
	TUNEnabled           bool `json:"tun_enabled"`
}

type MeasurementContract struct {
	Runs                             int    `json:"runs"`
	Samples                          int    `json:"samples"`
	Warmup                           int    `json:"warmup"`
	Unit                             string `json:"unit"`
	Scope                            string `json:"scope"`
	PairOrdering                     string `json:"pair_ordering"`
	BaselinePath                     string `json:"baseline_path"`
	SidecarPath                      string `json:"sidecar_path"`
	FreshConnectionPerSample         bool   `json:"fresh_connection_per_sample"`
	TLSIncluded                      bool   `json:"tls_included"`
	RepresentsRealApplicationSuccess bool   `json:"represents_real_application_success"`
}

type RunResult struct {
	Index            int                     `json:"index"`
	BaselineUS       MicrosecondDistribution `json:"baseline_us"`
	SidecarUS        MicrosecondDistribution `json:"sidecar_us"`
	PairedOverheadUS MicrosecondDistribution `json:"paired_overhead_us"`
}

type MicrosecondDistribution struct {
	Samples int   `json:"samples"`
	Min     int64 `json:"min"`
	P50     int64 `json:"p50"`
	P95     int64 `json:"p95"`
	P99     int64 `json:"p99"`
	Max     int64 `json:"max"`
}

type CorrectnessResult struct {
	PayloadsVerified               int  `json:"payloads_verified"`
	ExpectedPayloads               int  `json:"expected_payloads"`
	DirectSelections               int  `json:"direct_selections"`
	ExpectedSelections             int  `json:"expected_selections"`
	DirectGatewayAttempts          int  `json:"direct_gateway_attempts"`
	DirectGatewayAttemptsAvailable bool `json:"direct_gateway_attempts_available"`
	ProxyGatewayAttempts           int  `json:"proxy_gateway_attempts"`
	DomainTargetVerified           bool `json:"domain_target_verified"`
	TLSClientHellosAccepted        int  `json:"tls_client_hellos_accepted"`
	ExpectedTLSClientHellos        int  `json:"expected_tls_client_hellos"`
	TLSReadinessVerified           bool `json:"tls_readiness_verified"`
	Passed                         bool `json:"passed"`
}

// Run measures fresh connection + SOCKS admission through either an exact
// one-byte echo or a structurally valid ClientHello-to-ServerHello exchange.
// It alternates the order within every baseline/sidecar pair to reduce drift.
func Run(parent context.Context, options Options) (Report, error) {
	report := newReport(options)
	if err := options.Validate(); err != nil {
		return report, err
	}
	var probePolicy privacy.Policy
	if options.TLS {
		var err error
		probePolicy, err = privacy.New(privacy.ModeExplicitOptIn, nil)
		if err != nil {
			return report, fmt.Errorf("compile benchmark Direct-probe policy: %w", err)
		}
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var direct *testlab.SOCKSGateway
	var topology *mihomolab.DirectBenchmarkTopology
	var echo *testlab.EchoTarget
	var tlsTarget *testlab.TLSTarget
	var baselineEndpoint, directEndpoint, targetAddress string
	var target socks5.Request
	var err error
	if options.MihomoPath == "" {
		if options.TLS {
			tlsTarget, err = testlab.StartTLSTarget(ctx)
			if err != nil {
				return report, fmt.Errorf("start benchmark TLS target: %w", err)
			}
			defer tlsTarget.Close()
			targetAddress = tlsTarget.Address()
			target = socks5.Request{Host: benchmarkHostname, Port: tlsTarget.Port()}
		} else {
			echo, err = testlab.StartEchoTarget(ctx)
			if err != nil {
				return report, fmt.Errorf("start benchmark echo target: %w", err)
			}
			defer echo.Close()
			targetAddress = echo.Address()
			target = socks5.Request{Host: benchmarkHostname, Port: echo.Port()}
		}
		direct, err = testlab.StartSOCKSGateway(ctx, targetAddress)
		if err != nil {
			return report, fmt.Errorf("start benchmark direct gateway: %w", err)
		}
		defer direct.Close()
		baselineEndpoint = direct.Address()
		directEndpoint = direct.Address()
	} else {
		if options.TLS {
			topology, err = mihomolab.StartTLSBenchmarkTopology(ctx, options.MihomoPath)
		} else {
			topology, err = mihomolab.StartDirectBenchmarkTopology(ctx, options.MihomoPath)
		}
		if err != nil {
			return report, fmt.Errorf("start pinned Mihomo benchmark topology: %w", err)
		}
		defer topology.Close()
		baselineEndpoint = topology.Endpoint()
		directEndpoint = topology.Endpoint()
		targetAddress = topology.TargetAddress()
		target = socks5.Request{Host: topology.TargetHost(), Port: topology.TargetPort()}
		report.Tier = "pinned_mihomo_forced_direct"
		report.MihomoVersion = topology.Version()
		report.MihomoConfigValidated = topology.ConfigValidated()
		report.Isolation.DedicatedMihomoChild = true
		report.Isolation.TemporaryMihomoHome = true
		report.Measurement.BaselinePath = "client_to_pinned_mihomo_forced_direct"
		report.Measurement.SidecarPath = "client_to_smartroute_to_pinned_mihomo_forced_direct"
	}
	proxy, err := testlab.StartSOCKSGateway(ctx, targetAddress)
	if err != nil {
		return report, fmt.Errorf("start benchmark unused proxy gateway: %w", err)
	}
	defer proxy.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return report, fmt.Errorf("listen benchmark sidecar: %w", err)
	}
	serverCtx, stopServer := context.WithCancel(ctx)
	var directSelections atomic.Int64
	server := sidecar.Server{
		NetworkProfileID: "isolated-benchmark-lab", DeclaredBaselinePath: model.PathDirect,
		HandshakeTimeout: 3 * time.Second, MinimumCommitStage: model.StageTCP,
		Racer: transport.Racer{
			Direct:    transport.SOCKS5Dialer{Path: model.PathDirect, Endpoint: directEndpoint, ReadinessStage: model.StageTCP},
			Proxy:     transport.SOCKS5Dialer{Path: model.PathProxy, Endpoint: proxy.Address(), ReadinessStage: model.StageTCP},
			HeadStart: 500 * time.Millisecond, Timeout: 2 * time.Second,
		},
		OnDecision: func(event sidecar.DecisionEvent) {
			if event.Committed && event.SelectedPath == model.PathDirect {
				directSelections.Add(1)
			}
		},
	}
	if options.TLS {
		server.DirectProbePolicy = probePolicy
		server.TLSRacer = &transport.TLSRacer{
			Direct: transport.SOCKS5Dialer{Path: model.PathDirect, Endpoint: directEndpoint},
			Proxy:  transport.SOCKS5Dialer{Path: model.PathProxy, Endpoint: proxy.Address()},
			Gate:   transport.TLSServerHelloGate{}, HeadStart: 500 * time.Millisecond, Timeout: 2 * time.Second,
		}
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(serverCtx, listener) }()
	serverStopped := false
	defer func() {
		if !serverStopped {
			stopServer()
			<-serveDone
		}
	}()

	sidecarEndpoint := listener.Addr().String()
	baselineSamples := make([]int64, 0, options.Runs*options.Samples)
	sidecarSamples := make([]int64, 0, options.Runs*options.Samples)
	overheadSamples := make([]int64, 0, options.Runs*options.Samples)
	payloadsVerified := 0
	for run := 0; run < options.Runs; run++ {
		for index := 0; index < options.Warmup; index++ {
			if err := measurePair(ctx, baselineEndpoint, sidecarEndpoint, target, options.TLS, (run*options.Warmup+index)%2 == 0, nil, nil, nil); err != nil {
				return report, fmt.Errorf("benchmark run %d warmup: %w", run+1, err)
			}
		}
		runBaseline := make([]int64, 0, options.Samples)
		runSidecar := make([]int64, 0, options.Samples)
		runOverhead := make([]int64, 0, options.Samples)
		for index := 0; index < options.Samples; index++ {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			if err := measurePair(ctx, baselineEndpoint, sidecarEndpoint, target, options.TLS, (run*options.Samples+index)%2 == 0,
				&runBaseline, &runSidecar, &payloadsVerified); err != nil {
				return report, fmt.Errorf("benchmark run %d sample %d: %w", run+1, index+1, err)
			}
			runOverhead = append(runOverhead, runSidecar[len(runSidecar)-1]-runBaseline[len(runBaseline)-1])
		}
		baselineSamples = append(baselineSamples, runBaseline...)
		sidecarSamples = append(sidecarSamples, runSidecar...)
		overheadSamples = append(overheadSamples, runOverhead...)
		runResult := RunResult{Index: run + 1, BaselineUS: summarize(runBaseline), SidecarUS: summarize(runSidecar), PairedOverheadUS: summarize(runOverhead)}
		report.Runs = append(report.Runs, runResult)
		if run == 0 || runResult.PairedOverheadUS.P95 > report.WorstRunP95OverheadUS {
			report.WorstRunP95OverheadUS = runResult.PairedOverheadUS.P95
		}
	}
	stopServer()
	if err := <-serveDone; err != nil {
		return report, fmt.Errorf("stop benchmark sidecar: %w", err)
	}
	serverStopped = true

	directAttempts := 0
	directAttemptsAvailable := direct != nil
	domainTargetVerified := false
	if direct != nil {
		directAttempts, domainTargetVerified = direct.Stats(benchmarkHostname)
	} else {
		report.MihomoChildHealthy = topology.Running()
		domainTargetVerified = report.MihomoConfigValidated && report.MihomoChildHealthy && payloadsVerified == 2*options.Runs*options.Samples
	}
	proxyAttempts, _ := proxy.Stats(benchmarkHostname)
	expectedSelections := options.Runs * (options.Warmup + options.Samples)
	expectedPayloads := 2 * options.Runs * options.Samples
	expectedTLSClientHellos := 0
	tlsClientHellosAccepted := 0
	if options.TLS {
		expectedTLSClientHellos = 2 * expectedSelections
		if tlsTarget != nil {
			tlsClientHellosAccepted = tlsTarget.AcceptedClientHellos()
		} else {
			tlsClientHellosAccepted = topology.AcceptedClientHellos()
		}
	}
	report.BaselineUS = summarize(baselineSamples)
	report.SidecarUS = summarize(sidecarSamples)
	report.PairedOverheadUS = summarize(overheadSamples)
	report.Correctness = CorrectnessResult{
		PayloadsVerified: payloadsVerified, ExpectedPayloads: expectedPayloads,
		DirectSelections: int(directSelections.Load()), ExpectedSelections: expectedSelections,
		DirectGatewayAttempts: directAttempts, DirectGatewayAttemptsAvailable: directAttemptsAvailable,
		ProxyGatewayAttempts: proxyAttempts, DomainTargetVerified: domainTargetVerified,
		TLSClientHellosAccepted: tlsClientHellosAccepted, ExpectedTLSClientHellos: expectedTLSClientHellos,
		TLSReadinessVerified: options.TLS && tlsClientHellosAccepted == expectedTLSClientHellos,
	}
	directAttemptsCorrect := !directAttemptsAvailable || directAttempts == 2*options.Runs*(options.Warmup+options.Samples)
	tlsCorrect := !options.TLS || report.Correctness.TLSReadinessVerified
	report.Correctness.Passed = payloadsVerified == expectedPayloads && int(directSelections.Load()) == expectedSelections &&
		directAttemptsCorrect && proxyAttempts == 0 && domainTargetVerified && tlsCorrect
	report.MeetsP95Gate = report.WorstRunP95OverheadUS <= report.MaxP95OverheadUS
	report.Passed = report.Correctness.Passed && (!options.EnforceLatencyGate || report.MeetsP95Gate)
	if !report.Correctness.Passed {
		return report, errors.New("benchmark correctness contract failed")
	}
	if options.EnforceLatencyGate && !report.MeetsP95Gate {
		return report, errors.New("benchmark p95 overhead exceeds the enforced gate")
	}
	return report, nil
}

func newReport(options Options) Report {
	report := Report{
		ReportVersion: CurrentReportVersion, Tier: "fake_socks_gateway", Protocol: "tcp_echo", GeneratedAt: time.Now().UTC(),
		Environment: Environment{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(), GOMAXPROCS: runtime.GOMAXPROCS(0)},
		Isolation:   IsolationResult{LoopbackOnly: true, EphemeralPortsOnly: true},
		Measurement: MeasurementContract{
			Runs: options.Runs, Samples: options.Samples, Warmup: options.Warmup, Unit: "microseconds",
			Scope:                            "fresh_tcp_plus_socks_connect_plus_one_byte_echo",
			PairOrdering:                     "alternating_baseline_first_and_sidecar_first",
			BaselinePath:                     "client_to_direct_socks_gateway",
			SidecarPath:                      "client_to_smartroute_to_same_direct_socks_gateway",
			FreshConnectionPerSample:         true,
			TLSIncluded:                      false,
			RepresentsRealApplicationSuccess: false,
		},
		MaxP95OverheadUS:    options.MaxP95Overhead.Microseconds(),
		LatencyGateEnforced: options.EnforceLatencyGate,
		AuthorizesLiveTrial: false, AuthorizesPolicyChange: false,
	}
	if options.MihomoPath != "" {
		report.Tier = "pinned_mihomo_forced_direct"
		report.Measurement.BaselinePath = "client_to_pinned_mihomo_forced_direct"
		report.Measurement.SidecarPath = "client_to_smartroute_to_pinned_mihomo_forced_direct"
	}
	if options.TLS {
		report.Protocol = "tls_server_hello"
		report.Measurement.Scope = "fresh_tcp_plus_socks_connect_plus_clienthello_to_serverhello"
		report.Measurement.TLSIncluded = true
	}
	return report
}

func measurePair(ctx context.Context, baselineEndpoint, sidecarEndpoint string, target socks5.Request, tlsReady, baselineFirst bool,
	baselineSamples, sidecarSamples *[]int64, payloadsVerified *int) error {
	measure := func(endpoint string) (int64, error) {
		requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		started := time.Now()
		conn, err := socks5.DialContext(requestCtx, endpoint, target)
		if err != nil {
			return 0, err
		}
		defer conn.Close()
		if tlsReady {
			if err := writeBenchmarkBytes(conn, testlab.SyntheticClientHelloRecords()); err != nil {
				return 0, err
			}
			hello, err := tlsinspect.ReadServerHello(conn, 0)
			if err != nil {
				return 0, err
			}
			if !bytes.Equal(hello.Wire, testlab.SyntheticServerHelloRecord()) {
				return 0, errors.New("benchmark ServerHello mismatch")
			}
		} else {
			payload := []byte{0x5a}
			if _, err := conn.Write(payload); err != nil {
				return 0, err
			}
			received := make([]byte, len(payload))
			if _, err := io.ReadFull(conn, received); err != nil {
				return 0, err
			}
			if received[0] != payload[0] {
				return 0, errors.New("benchmark echo payload mismatch")
			}
		}
		return time.Since(started).Microseconds(), nil
	}
	var baseline, sidecar int64
	var err error
	if baselineFirst {
		baseline, err = measure(baselineEndpoint)
		if err == nil {
			sidecar, err = measure(sidecarEndpoint)
		}
	} else {
		sidecar, err = measure(sidecarEndpoint)
		if err == nil {
			baseline, err = measure(baselineEndpoint)
		}
	}
	if err != nil {
		return err
	}
	if baselineSamples != nil {
		*baselineSamples = append(*baselineSamples, baseline)
		*sidecarSamples = append(*sidecarSamples, sidecar)
		*payloadsVerified += 2
	}
	return nil
}

func writeBenchmarkBytes(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

func summarize(values []int64) MicrosecondDistribution {
	if len(values) == 0 {
		return MicrosecondDistribution{}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return MicrosecondDistribution{
		Samples: len(sorted), Min: sorted[0], P50: nearestRank(sorted, 50),
		P95: nearestRank(sorted, 95), P99: nearestRank(sorted, 99), Max: sorted[len(sorted)-1],
	}
}

func nearestRank(sorted []int64, percentile int) int64 {
	index := (percentile*len(sorted)+99)/100 - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
