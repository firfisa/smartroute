package loadlab

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOptionsValidation(t *testing.T) {
	if err := DefaultOptions().Validate(); err != nil {
		t.Fatal(err)
	}
	valid := DefaultOptions()
	tests := []Options{
		{Runs: 0, Concurrency: 1, BytesPerConnection: 1024, ChunkBytes: 256, MinThroughputRatio: 0.7},
		{Runs: 1, Concurrency: 0, BytesPerConnection: 1024, ChunkBytes: 256, MinThroughputRatio: 0.7},
		{Runs: 1, Concurrency: 1, BytesPerConnection: 1023, ChunkBytes: 256, MinThroughputRatio: 0.7},
		{Runs: 1, Concurrency: 1, BytesPerConnection: 1024, ChunkBytes: 128, MinThroughputRatio: 0.7},
		{Runs: 1, Concurrency: 1, BytesPerConnection: 1024, ChunkBytes: 2048, MinThroughputRatio: 0.7},
		{Runs: 1, Concurrency: 1, BytesPerConnection: 1024, ChunkBytes: 256, WarmupConnections: 2, MinThroughputRatio: 0.7},
		{Runs: 1, Concurrency: 1, BytesPerConnection: 1024, ChunkBytes: 256, MinThroughputRatio: 0.09},
		{Runs: 1, Concurrency: 1, BytesPerConnection: 1024, ChunkBytes: 256, MinThroughputRatio: 0.7, AggregateOfferedLoadMbps: -1},
		{Runs: 1, Concurrency: 1, BytesPerConnection: 64 << 20, ChunkBytes: 256, MinThroughputRatio: 0.7, AggregateOfferedLoadMbps: 1},
	}
	for _, options := range tests {
		if err := options.Validate(); err == nil {
			t.Fatalf("accepted invalid options=%+v", options)
		}
	}
	valid.WarmupConnections = 0
	if err := valid.Validate(); err != nil {
		t.Fatalf("zero warmup rejected: %v", err)
	}
}

func TestOfferedLoadDurationUsesAggregateOneWayRate(t *testing.T) {
	got := offeredLoadDuration(16, 1<<20, 100)
	want := 1*time.Second + 342177280*time.Nanosecond
	if got != want {
		t.Fatalf("offeredLoadDuration()=%v want %v", got, want)
	}
	if offeredLoadDuration(0, 1, 1) != 0 || offeredLoadDuration(1, 0, 1) != 0 || offeredLoadDuration(1, 1, 0) != 0 {
		t.Fatal("invalid offered-load duration input did not return zero")
	}
}

func TestWaitUntilHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if err := waitUntil(ctx, time.Now().Add(time.Second)); err == nil {
		t.Fatal("waitUntil(canceled) error=nil")
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("waitUntil did not stop promptly")
	}
}

func TestSummariesUseNearestRank(t *testing.T) {
	integers := summarizeMicroseconds([]int64{9, 1, 5, 3, 7, 2, 4, 6, 8, 10})
	if integers.Samples != 10 || integers.Min != 1 || integers.P50 != 5 || integers.P95 != 10 || integers.P99 != 10 || integers.Max != 10 {
		t.Fatalf("integers=%+v", integers)
	}
	floats := summarizeFloats([]float64{0.9, 0.7, 1.1})
	if floats.Samples != 3 || floats.Min != 0.7 || floats.P50 != 0.9 || floats.P95 != 1.1 || floats.Max != 1.1 {
		t.Fatalf("floats=%+v", floats)
	}
}

func TestRuntimeDeltaRequiresAvailableMonotonicSnapshots(t *testing.T) {
	before := runtimeSnapshot{available: true, userCPUSeconds: 1, gcCPUSeconds: 0.2, allocatedBytes: 100, allocationObjects: 10, gcCycles: 2}
	after := runtimeSnapshot{available: true, userCPUSeconds: 1.5, gcCPUSeconds: 0.3, allocatedBytes: 350, allocationObjects: 30, gcCycles: 3}
	delta := runtimeDelta(before, after)
	if !delta.Available || delta.UserCPUSeconds != 0.5 || delta.AllocatedBytes != 250 || delta.AllocationObjects != 20 || delta.GCCycles != 1 {
		t.Fatalf("delta=%+v", delta)
	}
	if invalid := runtimeDelta(runtimeSnapshot{}, after); invalid.Available {
		t.Fatalf("invalid delta=%+v", invalid)
	}
	if subtractUint(1, 2) != 0 {
		t.Fatal("subtractUint underflowed")
	}
}

func TestRunLoopbackLoad(t *testing.T) {
	options := DefaultOptions()
	options.Runs = 2
	options.Concurrency = 4
	options.BytesPerConnection = 64 << 10
	options.ChunkBytes = 4 << 10
	options.WarmupConnections = 2
	report, err := Run(context.Background(), options)
	if err != nil {
		t.Fatalf("Run() error=%v report=%+v", err, report)
	}
	if !report.Passed || !report.Correctness.Passed || report.ReportVersion != CurrentReportVersion || report.Tier != "fake_socks_gateway" || len(report.Runs) != 2 ||
		report.Runs[0].Order != "baseline_then_sidecar" || report.Runs[1].Order != "sidecar_then_baseline" ||
		report.Correctness.BaselineConnections != 8 || report.Correctness.SidecarConnections != 8 ||
		report.Correctness.BaselineOneWayPayloadBytes != 8*(64<<10) || report.Correctness.SidecarOneWayPayloadBytes != 8*(64<<10) ||
		report.Correctness.DirectSelections != 12 || report.Correctness.DirectGatewayAttempts != 24 ||
		report.Correctness.ProxyGatewayAttempts != 0 || !report.Correctness.DomainTargetVerified ||
		report.Aggregate.BaselineConnectionUS.Samples != 8 || report.Aggregate.SidecarConnectionUS.Samples != 8 ||
		report.ThroughputGateEnforced || report.AuthorizesLiveTrial || report.AuthorizesPolicyChange {
		t.Fatalf("report=%+v", report)
	}
	for _, run := range report.Runs {
		if run.Baseline.BidirectionalRelayBytes != 2*run.Baseline.OneWayPayloadBytes ||
			run.Sidecar.BidirectionalRelayBytes != 2*run.Sidecar.OneWayPayloadBytes || run.ThroughputRatio <= 0 {
			t.Fatalf("run=%+v", run)
		}
		if !run.Baseline.Runtime.Available || !run.Sidecar.Runtime.Available {
			t.Fatalf("runtime metrics unavailable: run=%+v", run)
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "connectionLatenciesUS") {
		t.Fatal("report exposed raw per-connection latency rows")
	}
}

func TestSweepValidationAndLoopbackRun(t *testing.T) {
	options := SweepOptions{
		Runs: 1, ChunkBytes: 1024, WarmupMax: 1, MinThroughputRatio: 0.70,
		Cells: []SweepCell{{Concurrency: 1, BytesPerConnection: 8 << 10}, {Concurrency: 2, BytesPerConnection: 8 << 10}},
	}
	if err := options.Validate(); err != nil {
		t.Fatal(err)
	}
	report, err := RunSweep(context.Background(), options)
	if err != nil {
		t.Fatalf("RunSweep() error=%v report=%+v", err, report)
	}
	if !report.Passed || !report.AllCorrect || !report.AllRuntimeMetricsAvailable || len(report.Cells) != 2 || len(report.Summaries) != 2 || report.PerformanceGateEnforced ||
		report.AuthorizesLiveTrial || report.AuthorizesPolicyChange {
		t.Fatalf("report=%+v", report)
	}
	for _, summary := range report.Summaries {
		if summary.BaselineAllocatedBytesTotal == 0 || summary.SidecarAllocatedBytesTotal == 0 || summary.AllocatedBytesRatio <= 0 || !summary.UserCPUShortWindowDiagnosticOnly {
			t.Fatalf("summary=%+v", summary)
		}
	}
	duplicate := options
	duplicate.Cells = []SweepCell{options.Cells[0], options.Cells[0]}
	if err := duplicate.Validate(); err == nil {
		t.Fatal("duplicate sweep cell accepted")
	}
}

func TestCapacityValidationAndLoopbackRun(t *testing.T) {
	options := CapacityOptions{
		Runs: 1, Concurrency: 2, BytesPerConnection: 64 << 10, ChunkBytes: 4 << 10, WarmupConnections: 1,
		DeadlineToleranceRatio: 0.03, MinimumDeadlineGrace: 20 * time.Millisecond,
		AggregateOfferedLoadMbps: []float64{100},
	}
	if err := options.Validate(); err != nil {
		t.Fatal(err)
	}
	report, err := RunCapacity(context.Background(), options)
	if err != nil {
		t.Fatalf("RunCapacity() error=%v report=%+v", err, report)
	}
	if !report.Passed || !report.AllCorrect || !report.AllCellsComparable || !report.AllBaselineCellsMeet || !report.AllSidecarCellsMeet ||
		report.PerformanceGateEnforced || report.AuthorizesLiveTrial || report.AuthorizesPolicyChange || len(report.Summaries) != 1 || len(report.Cells) != 1 {
		t.Fatalf("report=%+v", report)
	}
	summary := report.Summaries[0]
	if !summary.Correct || summary.TargetDurationUS <= 0 || summary.DeadlineAllowanceUS != 20_000 || !summary.Comparable {
		t.Fatalf("summary=%+v", summary)
	}
	for _, run := range report.Cells[0].Report.Runs {
		if !run.Baseline.Pacing.Enabled || !run.Sidecar.Pacing.Enabled || run.Baseline.Pacing.OfferedLoadMbps != 100 || run.Sidecar.Pacing.OfferedLoadMbps != 100 {
			t.Fatalf("run pacing=%+v %+v", run.Baseline.Pacing, run.Sidecar.Pacing)
		}
	}
	duplicate := options
	duplicate.AggregateOfferedLoadMbps = []float64{100, 100}
	if err := duplicate.Validate(); err == nil {
		t.Fatal("duplicate capacity cell accepted")
	}
}

func TestRunPinnedMihomoLoad(t *testing.T) {
	binary := os.Getenv("SMARTROUTE_TEST_MIHOMO")
	if binary == "" {
		t.Skip("set SMARTROUTE_TEST_MIHOMO to the absolute pinned lab binary path")
	}
	options := DefaultOptions()
	options.Runs = 1
	options.Concurrency = 2
	options.BytesPerConnection = 32 << 10
	options.ChunkBytes = 4 << 10
	options.WarmupConnections = 1
	options.MihomoPath = binary
	report, err := Run(context.Background(), options)
	if err != nil {
		t.Fatalf("Run() error=%v report=%+v", err, report)
	}
	if report.Tier != "pinned_mihomo_forced_direct" || !report.Passed || !report.MihomoConfigValidated || !report.MihomoChildHealthy ||
		report.Correctness.DirectGatewayAttemptsAvailable || report.Correctness.BaselineConnections != 2 ||
		report.Correctness.SidecarConnections != 2 || report.Correctness.DirectSelections != 3 || report.Correctness.ProxyGatewayAttempts != 0 {
		t.Fatalf("report=%+v", report)
	}
}

func TestRunHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	options := DefaultOptions()
	options.Runs = 1
	options.Concurrency = 1
	options.BytesPerConnection = 1024
	options.ChunkBytes = 256
	options.WarmupConnections = 0
	started := time.Now()
	if _, err := Run(ctx, options); err == nil {
		t.Fatal("Run(canceled) error=nil")
	}
	if time.Since(started) > time.Second {
		t.Fatal("canceled load lab did not stop promptly")
	}
}
