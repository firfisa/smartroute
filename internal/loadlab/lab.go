// Package loadlab measures concurrent chunked-echo relay behavior through the
// same loopback gateway with and without the SmartRoute sidecar. It is separate
// from benchlab so throughput load cannot change the latency benchmark's scope.
package loadlab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"runtime"
	"runtime/metrics"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/firfisa/smartroute/internal/mihomolab"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/sidecar"
	"github.com/firfisa/smartroute/internal/socks5"
	"github.com/firfisa/smartroute/internal/testlab"
	"github.com/firfisa/smartroute/internal/transport"
)

const (
	CurrentReportVersion = 2
	loadHostname         = "echo.test"
)

type Options struct {
	Runs                     int
	Concurrency              int
	BytesPerConnection       int64
	ChunkBytes               int
	WarmupConnections        int
	MinThroughputRatio       float64
	EnforceThroughputGate    bool
	MihomoPath               string
	AggregateOfferedLoadMbps float64
}

func DefaultOptions() Options {
	return Options{
		Runs: 3, Concurrency: 16, BytesPerConnection: 1 << 20, ChunkBytes: 32 << 10,
		WarmupConnections: 4, MinThroughputRatio: 0.70,
	}
}

func (o Options) Validate() error {
	if o.Runs < 1 || o.Runs > 20 {
		return errors.New("load runs must be between 1 and 20")
	}
	if o.Concurrency < 1 || o.Concurrency > 512 {
		return errors.New("load concurrency must be between 1 and 512")
	}
	if o.BytesPerConnection < 1024 || o.BytesPerConnection > 64<<20 {
		return errors.New("load bytes per connection must be between 1 KiB and 64 MiB")
	}
	if o.ChunkBytes < 256 || o.ChunkBytes > 1<<20 || int64(o.ChunkBytes) > o.BytesPerConnection {
		return errors.New("load chunk bytes must be between 256 bytes and 1 MiB and not exceed bytes per connection")
	}
	if o.WarmupConnections < 0 || o.WarmupConnections > o.Concurrency {
		return errors.New("load warmup connections must be between zero and concurrency")
	}
	if o.MinThroughputRatio < 0.10 || o.MinThroughputRatio > 1.0 {
		return errors.New("load minimum throughput ratio must be between 0.10 and 1.0")
	}
	if o.AggregateOfferedLoadMbps < 0 || o.AggregateOfferedLoadMbps > 20_000 {
		return errors.New("load aggregate offered load must be zero or at most 20000 Mbps")
	}
	if o.AggregateOfferedLoadMbps > 0 && offeredLoadDuration(o.Concurrency, o.BytesPerConnection, o.AggregateOfferedLoadMbps) > 20*time.Second {
		return errors.New("load aggregate offered load must complete its byte budget within 20 seconds")
	}
	return nil
}

type Report struct {
	ReportVersion           int                 `json:"report_version"`
	Tier                    string              `json:"tier"`
	GeneratedAt             time.Time           `json:"generated_at"`
	MihomoVersion           string              `json:"mihomo_version,omitempty"`
	MihomoConfigValidated   bool                `json:"mihomo_config_validated"`
	MihomoChildHealthy      bool                `json:"mihomo_child_healthy"`
	Environment             Environment         `json:"environment"`
	Isolation               IsolationResult     `json:"isolation"`
	Measurement             MeasurementContract `json:"measurement"`
	Runs                    []RunResult         `json:"runs"`
	Aggregate               AggregateResult     `json:"aggregate"`
	Correctness             CorrectnessResult   `json:"correctness"`
	MinThroughputRatio      float64             `json:"min_throughput_ratio"`
	WorstRunThroughputRatio float64             `json:"worst_run_throughput_ratio"`
	MeetsThroughputGate     bool                `json:"meets_throughput_gate"`
	ThroughputGateEnforced  bool                `json:"throughput_gate_enforced"`
	AuthorizesLiveTrial     bool                `json:"authorizes_live_trial"`
	AuthorizesPolicyChange  bool                `json:"authorizes_policy_change"`
	Passed                  bool                `json:"passed"`
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
	Runs                         int     `json:"runs"`
	Concurrency                  int     `json:"concurrency"`
	BytesPerConnection           int64   `json:"bytes_per_connection"`
	ChunkBytes                   int     `json:"chunk_bytes"`
	WarmupConnections            int     `json:"warmup_connections_per_arm_per_run"`
	WarmupBytesPerConnection     int64   `json:"warmup_bytes_per_connection"`
	PairOrdering                 string  `json:"pair_ordering"`
	BaselinePath                 string  `json:"baseline_path"`
	SidecarPath                  string  `json:"sidecar_path"`
	ThroughputNumerator          string  `json:"throughput_numerator"`
	BidirectionalBytesReported   bool    `json:"bidirectional_relay_bytes_reported"`
	RepresentsMaximumThroughput  bool    `json:"represents_maximum_throughput"`
	RepresentsApplicationSuccess bool    `json:"represents_application_success"`
	RuntimeMetricsScope          string  `json:"runtime_metrics_scope"`
	AggregateOfferedLoadMbps     float64 `json:"aggregate_offered_load_mbps,omitempty"`
	OfferedLoadPacing            string  `json:"offered_load_pacing,omitempty"`
}

type RunResult struct {
	Index           int         `json:"index"`
	Order           string      `json:"order"`
	Baseline        BatchResult `json:"baseline"`
	Sidecar         BatchResult `json:"sidecar"`
	ThroughputRatio float64     `json:"throughput_ratio"`
}

type BatchResult struct {
	Connections             int                     `json:"connections"`
	CompletedConnections    int                     `json:"completed_connections"`
	OneWayPayloadBytes      int64                   `json:"one_way_payload_bytes"`
	BidirectionalRelayBytes int64                   `json:"bidirectional_relay_bytes"`
	DurationUS              int64                   `json:"duration_us"`
	PayloadMiBPerSecond     float64                 `json:"payload_mib_per_second"`
	ConnectionCompletionUS  MicrosecondDistribution `json:"connection_completion_us"`
	Runtime                 RuntimeDelta            `json:"runtime"`
	Pacing                  PacingResult            `json:"pacing"`
	connectionLatenciesUS   []int64
}

type PacingResult struct {
	Enabled           bool    `json:"enabled"`
	OfferedLoadMbps   float64 `json:"aggregate_offered_load_mbps,omitempty"`
	TargetDurationUS  int64   `json:"target_duration_us,omitempty"`
	DeadlineOverrunUS int64   `json:"deadline_overrun_us"`
}

type RuntimeDelta struct {
	Available         bool    `json:"available"`
	UserCPUSeconds    float64 `json:"user_cpu_seconds"`
	GCCPUSeconds      float64 `json:"gc_cpu_seconds"`
	AllocatedBytes    uint64  `json:"allocated_bytes"`
	AllocationObjects uint64  `json:"allocation_objects"`
	GCCycles          uint64  `json:"gc_cycles"`
}

type AggregateResult struct {
	BaselinePayloadMiBPerSecond FloatDistribution       `json:"baseline_payload_mib_per_second"`
	SidecarPayloadMiBPerSecond  FloatDistribution       `json:"sidecar_payload_mib_per_second"`
	ThroughputRatio             FloatDistribution       `json:"throughput_ratio"`
	BaselineConnectionUS        MicrosecondDistribution `json:"baseline_connection_completion_us"`
	SidecarConnectionUS         MicrosecondDistribution `json:"sidecar_connection_completion_us"`
}

type FloatDistribution struct {
	Samples int     `json:"samples"`
	Min     float64 `json:"min"`
	P50     float64 `json:"p50"`
	P95     float64 `json:"p95"`
	Max     float64 `json:"max"`
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
	BaselineConnections              int   `json:"baseline_connections"`
	SidecarConnections               int   `json:"sidecar_connections"`
	ExpectedConnectionsPerArm        int   `json:"expected_connections_per_arm"`
	BaselineOneWayPayloadBytes       int64 `json:"baseline_one_way_payload_bytes"`
	SidecarOneWayPayloadBytes        int64 `json:"sidecar_one_way_payload_bytes"`
	ExpectedOneWayPayloadBytesPerArm int64 `json:"expected_one_way_payload_bytes_per_arm"`
	DirectSelections                 int   `json:"direct_selections"`
	ExpectedDirectSelections         int   `json:"expected_direct_selections"`
	DirectGatewayAttempts            int   `json:"direct_gateway_attempts"`
	DirectGatewayAttemptsAvailable   bool  `json:"direct_gateway_attempts_available"`
	ProxyGatewayAttempts             int   `json:"proxy_gateway_attempts"`
	DomainTargetVerified             bool  `json:"domain_target_verified"`
	Passed                           bool  `json:"passed"`
}

// Run compares sequential baseline/sidecar batches while every batch contains
// concurrently started, fresh SOCKS connections and exact chunked echoes.
func Run(parent context.Context, options Options) (Report, error) {
	report := newReport(options)
	if err := options.Validate(); err != nil {
		return report, err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	var direct *testlab.SOCKSGateway
	var topology *mihomolab.DirectBenchmarkTopology
	var baselineEndpoint, directEndpoint, targetAddress string
	var target socks5.Request
	if options.MihomoPath == "" {
		echo, err := testlab.StartEchoTarget(ctx)
		if err != nil {
			return report, fmt.Errorf("start load echo target: %w", err)
		}
		defer echo.Close()
		direct, err = testlab.StartSOCKSGateway(ctx, echo.Address())
		if err != nil {
			return report, fmt.Errorf("start load Direct gateway: %w", err)
		}
		defer direct.Close()
		baselineEndpoint, directEndpoint, targetAddress = direct.Address(), direct.Address(), echo.Address()
		target = socks5.Request{Host: loadHostname, Port: echo.Port()}
	} else {
		var err error
		topology, err = mihomolab.StartDirectBenchmarkTopology(ctx, options.MihomoPath)
		if err != nil {
			return report, fmt.Errorf("start pinned Mihomo load topology: %w", err)
		}
		defer topology.Close()
		baselineEndpoint, directEndpoint, targetAddress = topology.Endpoint(), topology.Endpoint(), topology.TargetAddress()
		target = socks5.Request{Host: topology.TargetHost(), Port: topology.TargetPort()}
		report.MihomoVersion = topology.Version()
		report.MihomoConfigValidated = topology.ConfigValidated()
		report.Isolation.DedicatedMihomoChild = true
		report.Isolation.TemporaryMihomoHome = true
	}
	proxy, err := testlab.StartSOCKSGateway(ctx, targetAddress)
	if err != nil {
		return report, fmt.Errorf("start load unused Proxy gateway: %w", err)
	}
	defer proxy.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return report, fmt.Errorf("listen load sidecar: %w", err)
	}
	serverCtx, stopServer := context.WithCancel(ctx)
	var directSelections atomic.Int64
	server := sidecar.Server{
		NetworkProfileID: "isolated-load-lab", DeclaredBaselinePath: model.PathDirect,
		HandshakeTimeout: 5 * time.Second, MinimumCommitStage: model.StageTCP,
		Racer: transport.Racer{
			Direct:    transport.SOCKS5Dialer{Path: model.PathDirect, Endpoint: directEndpoint, ReadinessStage: model.StageTCP},
			Proxy:     transport.SOCKS5Dialer{Path: model.PathProxy, Endpoint: proxy.Address(), ReadinessStage: model.StageTCP},
			HeadStart: 500 * time.Millisecond, Timeout: 3 * time.Second,
		},
		OnDecision: func(event sidecar.DecisionEvent) {
			if event.Committed && event.SelectedPath == model.PathDirect {
				directSelections.Add(1)
			}
		},
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
	warmupBytes := min(options.BytesPerConnection, int64(64<<10))
	baselineRates := make([]float64, 0, options.Runs)
	sidecarRates := make([]float64, 0, options.Runs)
	ratios := make([]float64, 0, options.Runs)
	baselineLatencies := make([]int64, 0, options.Runs*options.Concurrency)
	sidecarLatencies := make([]int64, 0, options.Runs*options.Concurrency)
	var baselineCompleted, sidecarCompleted int
	var baselineBytes, sidecarBytes int64
	for run := 0; run < options.Runs; run++ {
		baselineFirst := run%2 == 0
		if options.WarmupConnections > 0 {
			if baselineFirst {
				if _, err := runBatch(ctx, baselineEndpoint, target, options.WarmupConnections, warmupBytes, options.ChunkBytes); err != nil {
					return report, fmt.Errorf("load run %d baseline warmup: %w", run+1, err)
				}
				if _, err := runBatch(ctx, sidecarEndpoint, target, options.WarmupConnections, warmupBytes, options.ChunkBytes); err != nil {
					return report, fmt.Errorf("load run %d sidecar warmup: %w", run+1, err)
				}
			} else {
				if _, err := runBatch(ctx, sidecarEndpoint, target, options.WarmupConnections, warmupBytes, options.ChunkBytes); err != nil {
					return report, fmt.Errorf("load run %d sidecar warmup: %w", run+1, err)
				}
				if _, err := runBatch(ctx, baselineEndpoint, target, options.WarmupConnections, warmupBytes, options.ChunkBytes); err != nil {
					return report, fmt.Errorf("load run %d baseline warmup: %w", run+1, err)
				}
			}
		}
		var baseline, sidecarBatch BatchResult
		if baselineFirst {
			baseline, err = runBatchAtRate(ctx, baselineEndpoint, target, options.Concurrency, options.BytesPerConnection, options.ChunkBytes, options.AggregateOfferedLoadMbps)
			if err == nil {
				sidecarBatch, err = runBatchAtRate(ctx, sidecarEndpoint, target, options.Concurrency, options.BytesPerConnection, options.ChunkBytes, options.AggregateOfferedLoadMbps)
			}
		} else {
			sidecarBatch, err = runBatchAtRate(ctx, sidecarEndpoint, target, options.Concurrency, options.BytesPerConnection, options.ChunkBytes, options.AggregateOfferedLoadMbps)
			if err == nil {
				baseline, err = runBatchAtRate(ctx, baselineEndpoint, target, options.Concurrency, options.BytesPerConnection, options.ChunkBytes, options.AggregateOfferedLoadMbps)
			}
		}
		if err != nil {
			return report, fmt.Errorf("load run %d measured batch: %w", run+1, err)
		}
		ratio := sidecarBatch.PayloadMiBPerSecond / baseline.PayloadMiBPerSecond
		order := "baseline_then_sidecar"
		if !baselineFirst {
			order = "sidecar_then_baseline"
		}
		report.Runs = append(report.Runs, RunResult{Index: run + 1, Order: order, Baseline: baseline, Sidecar: sidecarBatch, ThroughputRatio: ratio})
		baselineRates = append(baselineRates, baseline.PayloadMiBPerSecond)
		sidecarRates = append(sidecarRates, sidecarBatch.PayloadMiBPerSecond)
		ratios = append(ratios, ratio)
		baselineLatencies = append(baselineLatencies, baseline.connectionLatenciesUS...)
		sidecarLatencies = append(sidecarLatencies, sidecarBatch.connectionLatenciesUS...)
		baselineCompleted += baseline.CompletedConnections
		sidecarCompleted += sidecarBatch.CompletedConnections
		baselineBytes += baseline.OneWayPayloadBytes
		sidecarBytes += sidecarBatch.OneWayPayloadBytes
	}

	stopServer()
	if err := <-serveDone; err != nil {
		return report, fmt.Errorf("stop load sidecar: %w", err)
	}
	serverStopped = true
	if topology != nil {
		report.MihomoChildHealthy = topology.Running()
	}
	directAttempts := 0
	directAttemptsAvailable := direct != nil
	domainVerified := false
	if direct != nil {
		directAttempts, domainVerified = direct.Stats(loadHostname)
	} else {
		domainVerified = report.MihomoConfigValidated && report.MihomoChildHealthy && baselineCompleted == options.Runs*options.Concurrency && sidecarCompleted == options.Runs*options.Concurrency
	}
	proxyAttempts, _ := proxy.Stats(loadHostname)
	expectedConnections := options.Runs * options.Concurrency
	expectedBytes := int64(expectedConnections) * options.BytesPerConnection
	expectedSelections := options.Runs * (options.Concurrency + options.WarmupConnections)
	expectedAttempts := 2 * expectedSelections
	report.Aggregate = AggregateResult{
		BaselinePayloadMiBPerSecond: summarizeFloats(baselineRates), SidecarPayloadMiBPerSecond: summarizeFloats(sidecarRates),
		ThroughputRatio: summarizeFloats(ratios), BaselineConnectionUS: summarizeMicroseconds(baselineLatencies), SidecarConnectionUS: summarizeMicroseconds(sidecarLatencies),
	}
	report.WorstRunThroughputRatio = report.Aggregate.ThroughputRatio.Min
	report.MeetsThroughputGate = report.WorstRunThroughputRatio >= options.MinThroughputRatio
	report.Correctness = CorrectnessResult{
		BaselineConnections: baselineCompleted, SidecarConnections: sidecarCompleted, ExpectedConnectionsPerArm: expectedConnections,
		BaselineOneWayPayloadBytes: baselineBytes, SidecarOneWayPayloadBytes: sidecarBytes, ExpectedOneWayPayloadBytesPerArm: expectedBytes,
		DirectSelections: int(directSelections.Load()), ExpectedDirectSelections: expectedSelections,
		DirectGatewayAttempts: directAttempts, DirectGatewayAttemptsAvailable: directAttemptsAvailable,
		ProxyGatewayAttempts: proxyAttempts, DomainTargetVerified: domainVerified,
	}
	directAttemptsCorrect := !directAttemptsAvailable || directAttempts == expectedAttempts
	report.Correctness.Passed = baselineCompleted == expectedConnections && sidecarCompleted == expectedConnections &&
		baselineBytes == expectedBytes && sidecarBytes == expectedBytes && int(directSelections.Load()) == expectedSelections &&
		directAttemptsCorrect && proxyAttempts == 0 && domainVerified
	report.Passed = report.Correctness.Passed && (!options.EnforceThroughputGate || report.MeetsThroughputGate)
	if !report.Correctness.Passed {
		return report, errors.New("load correctness contract failed")
	}
	if options.EnforceThroughputGate && !report.MeetsThroughputGate {
		return report, errors.New("load throughput ratio is below the enforced gate")
	}
	return report, nil
}

func newReport(options Options) Report {
	warmupBytes := min(options.BytesPerConnection, int64(64<<10))
	report := Report{
		ReportVersion: CurrentReportVersion, Tier: "fake_socks_gateway", GeneratedAt: time.Now().UTC(),
		Environment: Environment{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(), GOMAXPROCS: runtime.GOMAXPROCS(0)},
		Isolation:   IsolationResult{LoopbackOnly: true, EphemeralPortsOnly: true},
		Measurement: MeasurementContract{
			Runs: options.Runs, Concurrency: options.Concurrency, BytesPerConnection: options.BytesPerConnection,
			ChunkBytes: options.ChunkBytes, WarmupConnections: options.WarmupConnections, WarmupBytesPerConnection: warmupBytes,
			PairOrdering: "alternating_baseline_first_and_sidecar_first_by_run",
			BaselinePath: "clients_to_direct_socks_gateway", SidecarPath: "clients_to_smartroute_to_same_direct_socks_gateway",
			ThroughputNumerator: "one_way_verified_payload_bytes", BidirectionalBytesReported: true,
			RepresentsMaximumThroughput: false, RepresentsApplicationSuccess: false,
			RuntimeMetricsScope:      "current_go_process_only_excludes_kernel_and_mihomo_child",
			AggregateOfferedLoadMbps: options.AggregateOfferedLoadMbps,
		},
		MinThroughputRatio: options.MinThroughputRatio, ThroughputGateEnforced: options.EnforceThroughputGate,
		AuthorizesLiveTrial: false, AuthorizesPolicyChange: false,
	}
	if options.MihomoPath != "" {
		report.Tier = "pinned_mihomo_forced_direct"
		report.Measurement.BaselinePath = "clients_to_pinned_mihomo_forced_direct"
		report.Measurement.SidecarPath = "clients_to_smartroute_to_pinned_mihomo_forced_direct"
	}
	if options.AggregateOfferedLoadMbps > 0 {
		report.Measurement.OfferedLoadPacing = "measured_clients_absolute_cumulative_byte_schedule_warmup_unpaced"
	}
	return report
}

type connectionResult struct {
	durationUS int64
	bytes      int64
	err        error
}

func runBatch(parent context.Context, endpoint string, target socks5.Request, concurrency int, bytesPerConnection int64, chunkBytes int) (BatchResult, error) {
	return runBatchAtRate(parent, endpoint, target, concurrency, bytesPerConnection, chunkBytes, 0)
}

func runBatchAtRate(parent context.Context, endpoint string, target socks5.Request, concurrency int, bytesPerConnection int64, chunkBytes int, aggregateOfferedLoadMbps float64) (BatchResult, error) {
	runtimeBefore := readRuntimeSnapshot()
	result := BatchResult{Connections: concurrency}
	if aggregateOfferedLoadMbps > 0 {
		result.Pacing = PacingResult{
			Enabled: true, OfferedLoadMbps: aggregateOfferedLoadMbps,
			TargetDurationUS: offeredLoadDuration(concurrency, bytesPerConnection, aggregateOfferedLoadMbps).Microseconds(),
		}
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	start := make(chan struct{})
	results := make(chan connectionResult, concurrency)
	var wait sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			<-start
			connection := runConnectionAtRate(ctx, endpoint, target, worker, bytesPerConnection, chunkBytes, aggregateOfferedLoadMbps/float64(concurrency))
			if connection.err != nil {
				cancel()
			}
			results <- connection
		}(worker)
	}
	started := time.Now()
	close(start)
	wait.Wait()
	close(results)
	result.DurationUS = time.Since(started).Microseconds()
	if result.Pacing.Enabled && result.DurationUS > result.Pacing.TargetDurationUS {
		result.Pacing.DeadlineOverrunUS = result.DurationUS - result.Pacing.TargetDurationUS
	}
	latencies := make([]int64, 0, concurrency)
	var firstErr error
	for connection := range results {
		if connection.err != nil {
			if firstErr == nil {
				firstErr = connection.err
			}
			continue
		}
		result.CompletedConnections++
		result.OneWayPayloadBytes += connection.bytes
		latencies = append(latencies, connection.durationUS)
	}
	result.BidirectionalRelayBytes = 2 * result.OneWayPayloadBytes
	result.connectionLatenciesUS = append([]int64(nil), latencies...)
	result.ConnectionCompletionUS = summarizeMicroseconds(latencies)
	if result.DurationUS > 0 {
		result.PayloadMiBPerSecond = float64(result.OneWayPayloadBytes) / (1024 * 1024) / (float64(result.DurationUS) / 1e6)
	}
	result.Runtime = runtimeDelta(runtimeBefore, readRuntimeSnapshot())
	if firstErr != nil {
		return result, firstErr
	}
	if result.CompletedConnections != concurrency || result.OneWayPayloadBytes != int64(concurrency)*bytesPerConnection {
		return result, errors.New("load batch completed with missing connections or bytes")
	}
	return result, nil
}

func runConnectionAtRate(ctx context.Context, endpoint string, target socks5.Request, worker int, totalBytes int64, chunkBytes int, offeredLoadMbps float64) connectionResult {
	started := time.Now()
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := socks5.DialContext(requestCtx, endpoint, target)
	if err != nil {
		return connectionResult{err: err}
	}
	defer conn.Close()
	stopWatcher := closeConnectionOnCancel(requestCtx, conn)
	defer stopWatcher()
	chunk := make([]byte, chunkBytes)
	received := make([]byte, chunkBytes)
	var transferred int64
	for transferred < totalBytes {
		length := int64(chunkBytes)
		if remaining := totalBytes - transferred; remaining < length {
			length = remaining
		}
		value := byte((worker+int(transferred/int64(chunkBytes)))%251 + 1)
		for index := range chunk[:length] {
			chunk[index] = value
		}
		if err := writeFull(conn, chunk[:length]); err != nil {
			return connectionResult{err: err}
		}
		if _, err := io.ReadFull(conn, received[:length]); err != nil {
			return connectionResult{err: err}
		}
		if !bytes.Equal(chunk[:length], received[:length]) {
			return connectionResult{err: errors.New("load echo payload mismatch")}
		}
		transferred += length
		if offeredLoadMbps > 0 {
			due := started.Add(offeredLoadDuration(1, transferred, offeredLoadMbps))
			if err := waitUntil(requestCtx, due); err != nil {
				return connectionResult{err: err}
			}
		}
	}
	return connectionResult{durationUS: time.Since(started).Microseconds(), bytes: transferred}
}

func offeredLoadDuration(concurrency int, bytesPerConnection int64, aggregateMbps float64) time.Duration {
	if concurrency <= 0 || bytesPerConnection <= 0 || aggregateMbps <= 0 {
		return 0
	}
	nanoseconds := float64(concurrency) * float64(bytesPerConnection) * 8 * float64(time.Second) / (aggregateMbps * 1_000_000)
	return time.Duration(math.Ceil(nanoseconds))
}

func waitUntil(ctx context.Context, due time.Time) error {
	delay := time.Until(due)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeFull(writer io.Writer, value []byte) error {
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

func closeConnectionOnCancel(ctx context.Context, conn net.Conn) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func summarizeMicroseconds(values []int64) MicrosecondDistribution {
	if len(values) == 0 {
		return MicrosecondDistribution{}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return MicrosecondDistribution{
		Samples: len(sorted), Min: sorted[0], P50: nearestRankInt(sorted, 50), P95: nearestRankInt(sorted, 95),
		P99: nearestRankInt(sorted, 99), Max: sorted[len(sorted)-1],
	}
}

func summarizeFloats(values []float64) FloatDistribution {
	if len(values) == 0 {
		return FloatDistribution{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return FloatDistribution{
		Samples: len(sorted), Min: sorted[0], P50: nearestRankFloat(sorted, 50), P95: nearestRankFloat(sorted, 95), Max: sorted[len(sorted)-1],
	}
}

func nearestRankInt(sorted []int64, percentile int) int64 {
	index := (percentile*len(sorted)+99)/100 - 1
	return sorted[max(0, min(index, len(sorted)-1))]
}

func nearestRankFloat(sorted []float64, percentile int) float64 {
	index := (percentile*len(sorted)+99)/100 - 1
	return sorted[max(0, min(index, len(sorted)-1))]
}

const (
	metricUserCPU     = "/cpu/classes/user:cpu-seconds"
	metricGCCPU       = "/cpu/classes/gc/total:cpu-seconds"
	metricAllocBytes  = "/gc/heap/allocs:bytes"
	metricAllocObject = "/gc/heap/allocs:objects"
	metricGCCycles    = "/gc/cycles/total:gc-cycles"
)

type runtimeSnapshot struct {
	available         bool
	userCPUSeconds    float64
	gcCPUSeconds      float64
	allocatedBytes    uint64
	allocationObjects uint64
	gcCycles          uint64
}

func readRuntimeSnapshot() runtimeSnapshot {
	samples := [5]metrics.Sample{
		{Name: metricUserCPU}, {Name: metricGCCPU}, {Name: metricAllocBytes}, {Name: metricAllocObject}, {Name: metricGCCycles},
	}
	metrics.Read(samples[:])
	if samples[0].Value.Kind() != metrics.KindFloat64 || samples[1].Value.Kind() != metrics.KindFloat64 ||
		samples[2].Value.Kind() != metrics.KindUint64 || samples[3].Value.Kind() != metrics.KindUint64 || samples[4].Value.Kind() != metrics.KindUint64 {
		return runtimeSnapshot{}
	}
	return runtimeSnapshot{
		available: true, userCPUSeconds: samples[0].Value.Float64(), gcCPUSeconds: samples[1].Value.Float64(),
		allocatedBytes: samples[2].Value.Uint64(), allocationObjects: samples[3].Value.Uint64(), gcCycles: samples[4].Value.Uint64(),
	}
}

func runtimeDelta(before, after runtimeSnapshot) RuntimeDelta {
	if !before.available || !after.available {
		return RuntimeDelta{}
	}
	return RuntimeDelta{
		Available: true, UserCPUSeconds: max(0, after.userCPUSeconds-before.userCPUSeconds),
		GCCPUSeconds:      max(0, after.gcCPUSeconds-before.gcCPUSeconds),
		AllocatedBytes:    subtractUint(after.allocatedBytes, before.allocatedBytes),
		AllocationObjects: subtractUint(after.allocationObjects, before.allocationObjects),
		GCCycles:          subtractUint(after.gcCycles, before.gcCycles),
	}
}

func subtractUint(after, before uint64) uint64 {
	if after < before {
		return 0
	}
	return after - before
}
