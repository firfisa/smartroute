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
}

func TestRequireLoopback(t *testing.T) {
	if err := requireLoopback("127.0.0.1:1234"); err != nil {
		t.Fatalf("requireLoopback(loopback) error = %v", err)
	}
	if err := requireLoopback("192.0.2.1:1234"); err == nil {
		t.Fatal("requireLoopback(non-loopback) error = nil")
	}
}
