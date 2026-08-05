package trial

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/config"
	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/mihomolab"
	"github.com/firfisa/smartroute/internal/observe"
	"github.com/firfisa/smartroute/internal/store"
	"github.com/firfisa/smartroute/internal/testlab"
)

func TestPreflightAcceptsFreshIsolatedShadowTrial(t *testing.T) {
	options := validOptions(t)
	before := directoryEntries(t, options.Config.Observation.Directory)
	report := Preflight(context.Background(), options)
	after := directoryEntries(t, options.Config.Observation.Directory)
	if !report.Ready || report.Counts.Fail != 0 {
		t.Fatalf("report = %+v", report)
	}
	if report.ReportVersion != PreflightReportVersion || report.AssessmentPlan == nil || report.AssessmentPlan.PlanSHA256 == "" {
		t.Fatalf("missing assessment plan = %+v", report)
	}
	reportPath := filepath.Join(t.TempDir(), "preflight.json")
	writeJSON(t, reportPath, report)
	if _, err := LoadAssessmentPlan(reportPath, options.Config); err != nil {
		t.Fatalf("load emitted assessment plan: %v", err)
	}
	if report.PersistentStateChanged || report.ActiveClashInspected || report.AuthorizesLiveActivation {
		t.Fatalf("unsafe report claims = %+v", report)
	}
	if len(before) != len(after) {
		t.Fatalf("preflight changed observation directory: before=%v after=%v", before, after)
	}
}

func TestPreflightFailsClosedOnAcknowledgementsAndEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Options)
		check  string
	}{
		{"direct acknowledgment", func(o *Options) { o.AcknowledgeDirectProbes = false }, "privacy.direct_probes"},
		{"original baseline acknowledgment", func(o *Options) { o.AcknowledgeOriginalBaseline = false }, "baseline.original_path"},
		{"cleartext acknowledgment", func(o *Options) { o.Config.Observation.IncludeCleartextHostname = true }, "privacy.cleartext_hostname"},
		{"ephemeral auto acknowledgment", func(o *Options) { o.Config.Learning.Mode = learning.ModeEphemeralAuto }, "learning.mode"},
		{"missing test report", func(o *Options) { o.TestLabReportPath = "" }, "lab.testlab"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := validOptions(t)
			test.mutate(&options)
			report := Preflight(context.Background(), options)
			if report.Ready || checkStatus(report, test.check) != StatusFail {
				t.Fatalf("report = %+v", report)
			}
		})
	}
}

func TestPreflightTreatsDurableAutoConfigAsGlobalOptIn(t *testing.T) {
	options := validOptions(t)
	options.Config.Learning.Mode = learning.ModeDurableAuto
	options.Config.Learning.Persistence.Enabled = true
	report := Preflight(context.Background(), options)
	if got := checkStatus(report, "learning.mode"); got != StatusWarn {
		t.Fatalf("learning.mode status=%q report=%+v", got, report)
	}
}

func TestPreflightRejectsStaleOrFalseIsolationEvidence(t *testing.T) {
	options := validOptions(t)
	now := options.Clock()
	writeJSON(t, options.TestLabReportPath, validTestLab(now.Add(-25*time.Hour)))
	report := Preflight(context.Background(), options)
	if report.Ready || checkStatus(report, "lab.testlab") != StatusFail {
		t.Fatalf("stale report = %+v", report)
	}

	writeJSON(t, options.TestLabReportPath, validTestLab(now))
	mihomoReport := validMihomoLab(now)
	mihomoReport.Isolation.ActiveClashRead = true
	writeJSON(t, options.MihomoLabReportPath, mihomoReport)
	report = Preflight(context.Background(), options)
	if report.Ready || checkStatus(report, "lab.mihomo") != StatusFail {
		t.Fatalf("false isolation report = %+v", report)
	}

	mihomoReport = validMihomoLab(now)
	mihomoReport.Scenarios = mihomoReport.Scenarios[:1]
	writeJSON(t, options.MihomoLabReportPath, mihomoReport)
	report = Preflight(context.Background(), options)
	if report.Ready || checkStatus(report, "lab.mihomo") != StatusFail {
		t.Fatalf("incomplete scenario report = %+v", report)
	}

	testReport := validTestLab(now)
	testReport.Scenarios[3].LearnedPath = "proxy"
	writeJSON(t, options.TestLabReportPath, testReport)
	writeJSON(t, options.MihomoLabReportPath, validMihomoLab(now))
	report = Preflight(context.Background(), options)
	if report.Ready || checkStatus(report, "lab.testlab") != StatusFail {
		t.Fatalf("forged automatic-learning report = %+v", report)
	}
}

func TestPreflightRequiresMatchingBackupForExistingDurableStore(t *testing.T) {
	options := validOptions(t)
	databasePath := filepath.Join(t.TempDir(), "learning.db")
	durable, err := store.Open(context.Background(), store.Config{Path: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "backup")
	if _, err := durable.Backup(context.Background(), backupPath); err != nil {
		t.Fatal(err)
	}
	if err := durable.Close(); err != nil {
		t.Fatal(err)
	}
	options.Config.Learning.Persistence.Enabled = true
	options.Config.Learning.Persistence.DatabasePath = databasePath

	report := Preflight(context.Background(), options)
	if report.Ready || checkStatus(report, "learning.backup") != StatusFail {
		t.Fatalf("missing backup report = %+v", report)
	}
	options.LearningBackupPath = backupPath
	report = Preflight(context.Background(), options)
	if !report.Ready || checkStatus(report, "learning.backup") != StatusPass {
		t.Fatalf("verified backup report = %+v", report)
	}
}

func TestPreflightRejectsCancelledContext(t *testing.T) {
	options := validOptions(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := Preflight(ctx, options)
	if report.Ready || len(report.Checks) != 1 || report.Checks[0].ID != "preflight.context" {
		t.Fatalf("report = %+v", report)
	}
}

func validOptions(t *testing.T) Options {
	t.Helper()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	observationDirectory := filepath.Join(root, "observations")
	if err := observe.Pause(observationDirectory); err != nil {
		t.Fatal(err)
	}
	testReportPath := filepath.Join(root, "testlab.json")
	mihomoReportPath := filepath.Join(root, "mihomo-lab.json")
	writeJSON(t, testReportPath, validTestLab(now))
	writeJSON(t, mihomoReportPath, validMihomoLab(now))
	cfg := config.Default()
	cfg.Observation.Enabled = true
	cfg.Observation.Directory = observationDirectory
	return Options{
		Config: cfg, TestLabReportPath: testReportPath, MihomoLabReportPath: mihomoReportPath,
		AcknowledgeDirectProbes: true, AcknowledgeOriginalBaseline: true,
		TrialSessionID: "trial-0123456789abcdef0123456789abcdef", AssessmentWindow: 168 * time.Hour,
		AssessmentThresholds: DefaultAssessmentThresholds(), Clock: func() time.Time { return now },
	}
}

func validTestLab(now time.Time) testlab.Report {
	return testlab.Report{
		ReportVersion: testlab.CurrentReportVersion, GeneratedAt: now,
		Isolation: testlab.IsolationResult{LoopbackOnly: true, EphemeralPortsOnly: true},
		Scenarios: testScenarioResults(), Passed: true,
	}
}

func validMihomoLab(now time.Time) mihomolab.Report {
	return mihomolab.Report{
		ReportVersion: mihomolab.CurrentReportVersion, GeneratedAt: now,
		MihomoVersion: "Mihomo " + mihomolab.PinnedMihomoVersion + " " + mihomolab.PinnedBuildMarker,
		Isolation: mihomolab.IsolationResult{DedicatedChildProcess: true, TemporaryHome: true,
			LoopbackOnly: true, EphemeralPortsOnly: true},
		ConfigValidated: true, LoopPrevented: true, ReadinessGapDetected: true,
		TLSReadinessVerified: true, GuardFallbackVerified: true, GuardRecoveryVerified: true,
		Scenarios: mihomoScenarioResults(), Passed: true,
	}
}

func testScenarioResults() []testlab.ScenarioResult {
	return []testlab.ScenarioResult{
		{Name: "direct_candidate_before_head_start", ExpectedPath: "direct", SelectedPath: "direct", ReasonCode: "direct_candidate_before_head_start",
			DirectAttempts: 1, DomainPreserved: true, PayloadVerified: true, Passed: true},
		{Name: "proxy_recovers_slow_direct", ExpectedPath: "proxy", SelectedPath: "proxy", ReasonCode: "proxy_candidate_won",
			DirectAttempts: 1, ProxyAttempts: 1, DomainPreserved: true, PayloadVerified: true, Passed: true},
		{Name: "both_paths_fail", DirectAttempts: 1, ProxyAttempts: 1, DomainPreserved: true,
			FailureExpected: true, FailureObserved: true, Passed: true},
		{Name: "auto_first_ready_remembered", ExpectedPath: "direct", SelectedPath: "direct", ReasonCode: "direct_candidate_before_head_start",
			DirectAttempts: 1, DomainPreserved: true, ReadinessVerified: true, LearnedPath: "direct", Passed: true},
		{Name: "auto_reuses_direct_without_proxy", ExpectedPath: "direct", SelectedPath: "direct", ReasonCode: "durable_policy_selected",
			DirectAttempts: 1, DomainPreserved: true, ReadinessVerified: true, LearnedPath: "direct", Passed: true},
		{Name: "auto_fallback_overwrites_proxy", ExpectedPath: "proxy", SelectedPath: "proxy", ReasonCode: "durable_policy_fallback",
			DirectAttempts: 1, ProxyAttempts: 1, DomainPreserved: true, ReadinessVerified: true, LearnedPath: "proxy", Passed: true},
		{Name: "auto_reuses_proxy_without_direct", ExpectedPath: "proxy", SelectedPath: "proxy", ReasonCode: "durable_policy_selected",
			ProxyAttempts: 1, DomainPreserved: true, ReadinessVerified: true, LearnedPath: "proxy", Passed: true},
	}
}

func mihomoScenarioResults() []mihomolab.ScenarioResult {
	results := make([]mihomolab.ScenarioResult, 0, len(requiredMihomoLabScenarios))
	for _, name := range requiredMihomoLabScenarios {
		results = append(results, mihomolab.ScenarioResult{Name: name, Passed: true})
	}
	return results
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func checkStatus(report Report, id string) Status {
	for _, check := range report.Checks {
		if check.ID == id {
			return check.Status
		}
	}
	return ""
}

func directoryEntries(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	return result
}
