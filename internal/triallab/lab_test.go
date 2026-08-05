package triallab

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunRehearsesCleanAndUnexpectedSessionContractsWithoutAuthority(t *testing.T) {
	report, err := Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error=%v report=%+v", err, report)
	}
	if !report.Passed || !report.SyntheticInputs || report.PreflightEvidence || report.AuthorizesLiveTrial || report.AuthorizesPolicyChange {
		t.Fatalf("unsafe report=%+v", report)
	}
	if !report.Isolation.TemporaryWorkspace || !report.Isolation.TemporaryWorkspaceRemoved || report.Isolation.ListenersOpened ||
		report.Isolation.ExternalNetwork || report.Isolation.ActiveClashRead || report.Isolation.ActiveClashWritten || report.Isolation.SystemProxyModified {
		t.Fatalf("isolation=%+v", report.Isolation)
	}
	if len(report.Scenarios) != 2 || !report.Scenarios[0].ObservedAnalysisReady || report.Scenarios[1].ObservedAnalysisReady ||
		report.Scenarios[1].UnexpectedTrialSessionEvents != 1 {
		t.Fatalf("scenarios=%+v", report.Scenarios)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{plannedSession, unexpectedSession, "target-00.invalid", "synthetic-trial-lab", "conn-"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("identity leaked in report: %s", encoded)
		}
	}
}

func TestRunHonorsCanceledContextWithoutAuthority(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := Run(ctx)
	if err == nil || report.Passed || report.PreflightEvidence || report.AuthorizesLiveTrial || report.AuthorizesPolicyChange {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}
