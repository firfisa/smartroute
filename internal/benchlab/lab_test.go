package benchlab

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestOptionsValidation(t *testing.T) {
	valid := DefaultOptions()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []Options{
		{Runs: 1, Samples: 9, MaxP95Overhead: time.Millisecond},
		{Runs: 0, Samples: 10, MaxP95Overhead: time.Millisecond},
		{Runs: 1, Samples: 10, Warmup: -1, MaxP95Overhead: time.Millisecond},
		{Runs: 1, Samples: 10, MaxP95Overhead: 0},
		{Runs: 1, Samples: 10, MaxP95Overhead: 500 * time.Nanosecond},
		{Runs: 1, Samples: 10, MaxP95Overhead: time.Second + time.Nanosecond},
	}
	for _, options := range tests {
		if err := options.Validate(); err == nil {
			t.Fatalf("accepted invalid options=%+v", options)
		}
	}
}

func TestRunPinnedMihomoBenchmark(t *testing.T) {
	binary := os.Getenv("SMARTROUTE_TEST_MIHOMO")
	if binary == "" {
		t.Skip("set SMARTROUTE_TEST_MIHOMO to the explicit pinned lab binary")
	}
	options := DefaultOptions()
	options.Runs = 1
	options.Samples = 10
	options.Warmup = 2
	options.MihomoPath = binary
	report, err := Run(context.Background(), options)
	if err != nil {
		t.Fatalf("Run() error=%v report=%+v", err, report)
	}
	if report.Tier != "pinned_mihomo_forced_direct" || !report.Passed || !report.MihomoConfigValidated || !report.MihomoChildHealthy ||
		!report.Isolation.DedicatedMihomoChild || !report.Isolation.TemporaryMihomoHome || report.Isolation.TUNEnabled ||
		report.Correctness.DirectGatewayAttemptsAvailable || !report.Correctness.DomainTargetVerified || report.Correctness.ProxyGatewayAttempts != 0 {
		t.Fatalf("report=%+v", report)
	}
}

func TestRunPinnedMihomoTLSBenchmark(t *testing.T) {
	binary := os.Getenv("SMARTROUTE_TEST_MIHOMO")
	if binary == "" {
		t.Skip("set SMARTROUTE_TEST_MIHOMO to the absolute pinned lab binary path")
	}
	options := DefaultOptions()
	options.Runs = 1
	options.Samples = 10
	options.Warmup = 2
	options.MihomoPath = binary
	options.TLS = true
	report, err := Run(context.Background(), options)
	if err != nil {
		t.Fatalf("Run() error=%v report=%+v", err, report)
	}
	if report.Protocol != "tls_server_hello" || !report.Measurement.TLSIncluded || !report.Passed ||
		!report.Correctness.TLSReadinessVerified || report.Correctness.TLSClientHellosAccepted != 24 ||
		report.Correctness.ExpectedTLSClientHellos != 24 || report.Correctness.ProxyGatewayAttempts != 0 {
		t.Fatalf("report=%+v", report)
	}
}

func TestSummarizeUsesNearestRankAndPreservesSignedOverhead(t *testing.T) {
	distribution := summarize([]int64{9, -2, 5, 1, 3, 4, 2, 8, 7, 6})
	if distribution.Samples != 10 || distribution.Min != -2 || distribution.P50 != 4 ||
		distribution.P95 != 9 || distribution.P99 != 9 || distribution.Max != 9 {
		t.Fatalf("distribution=%+v", distribution)
	}
	if zero := summarize(nil); zero != (MicrosecondDistribution{}) {
		t.Fatalf("zero=%+v", zero)
	}
}

func TestRunLoopbackBenchmark(t *testing.T) {
	options := DefaultOptions()
	options.Runs = 1
	options.Samples = 10
	options.Warmup = 2
	report, err := Run(context.Background(), options)
	if err != nil {
		t.Fatalf("Run() error=%v report=%+v", err, report)
	}
	if !report.Passed || !report.Correctness.Passed || report.BaselineUS.Samples != 10 || report.SidecarUS.Samples != 10 ||
		report.PairedOverheadUS.Samples != 10 || len(report.Runs) != 1 || report.LatencyGateEnforced || report.AuthorizesLiveTrial || report.AuthorizesPolicyChange {
		t.Fatalf("report=%+v", report)
	}
	if report.Tier != "fake_socks_gateway" || !report.Correctness.DirectGatewayAttemptsAvailable || !report.Correctness.DomainTargetVerified ||
		report.Protocol != "tcp_echo" || report.Measurement.TLSIncluded || report.Correctness.TLSReadinessVerified ||
		report.MihomoConfigValidated || report.MihomoChildHealthy {
		t.Fatalf("tier/correctness=%+v %+v", report, report.Correctness)
	}
	if !report.Isolation.LoopbackOnly || !report.Isolation.EphemeralPortsOnly || report.Isolation.ExternalNetwork ||
		report.Isolation.ActiveClashRead || report.Isolation.ActiveClashWritten || report.Isolation.SystemProxyModified {
		t.Fatalf("isolation=%+v", report.Isolation)
	}
}

func TestRunLoopbackTLSBenchmark(t *testing.T) {
	options := DefaultOptions()
	options.Runs = 1
	options.Samples = 10
	options.Warmup = 2
	options.TLS = true
	report, err := Run(context.Background(), options)
	if err != nil {
		t.Fatalf("Run() error=%v report=%+v", err, report)
	}
	if report.Tier != "fake_socks_gateway" || report.Protocol != "tls_server_hello" || !report.Measurement.TLSIncluded ||
		report.Measurement.Scope != "fresh_tcp_plus_socks_connect_plus_clienthello_to_serverhello" || !report.Passed ||
		!report.Correctness.TLSReadinessVerified || report.Correctness.TLSClientHellosAccepted != 24 ||
		report.Correctness.ExpectedTLSClientHellos != 24 || report.Correctness.DirectGatewayAttempts != 24 ||
		report.Correctness.ProxyGatewayAttempts != 0 {
		t.Fatalf("report=%+v", report)
	}
}
