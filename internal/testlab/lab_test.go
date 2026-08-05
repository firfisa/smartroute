package testlab

import (
	"context"
	"testing"
	"time"
)

func TestRunAllIsolatedScenarios(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := RunAll(ctx)
	if err != nil {
		t.Fatalf("RunAll() error = %v; report = %+v", err, report)
	}
	if !report.Passed || !report.Isolation.LoopbackOnly || !report.Isolation.EphemeralPortsOnly {
		t.Fatalf("RunAll() report = %+v", report)
	}
	if report.Isolation.ExternalNetwork || report.Isolation.ClashFilesRead || report.Isolation.ClashFilesWritten {
		t.Fatalf("RunAll() violated isolation: %+v", report.Isolation)
	}
	if report.ReportVersion != CurrentReportVersion || len(report.Scenarios) != 7 || !ScenariosComplete(report.Scenarios) {
		t.Fatalf("RunAll() incomplete scenario contract: %+v", report)
	}
	if got := report.Scenarios[3]; got.Name != "auto_first_ready_remembered" || got.LearnedPath != "direct" || !got.ReadinessVerified {
		t.Fatalf("first automatic scenario = %+v", got)
	}
	if got := report.Scenarios[5]; got.Name != "auto_fallback_overwrites_proxy" || got.LearnedPath != "proxy" || got.DirectAttempts != 1 || got.ProxyAttempts != 1 {
		t.Fatalf("fallback automatic scenario = %+v", got)
	}
}

func TestScenariosCompleteRejectsForgedAutomaticResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := RunAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	report.Scenarios[4].ProxyAttempts = 1
	if ScenariosComplete(report.Scenarios) {
		t.Fatal("forged extra Proxy attempt was accepted")
	}
}

func TestRequireLoopback(t *testing.T) {
	if err := requireLoopback("127.0.0.1:1234"); err != nil {
		t.Fatalf("requireLoopback(loopback) error = %v", err)
	}
	if err := requireLoopback("192.0.2.1:1234"); err == nil {
		t.Fatal("requireLoopback(non-loopback) error = nil")
	}
}

func TestStartEchoTargetOnRejectsNonLoopback(t *testing.T) {
	if _, err := StartEchoTargetOn(context.Background(), "192.0.2.1"); err == nil {
		t.Fatal("non-loopback echo target accepted")
	}
	if _, err := StartEchoTargetOn(context.Background(), "localhost"); err == nil {
		t.Fatal("non-literal echo target accepted")
	}
}
