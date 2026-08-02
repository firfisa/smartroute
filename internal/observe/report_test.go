package observe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

func TestBuildReportAggregatesReadinessWithoutIdentity(t *testing.T) {
	directory := t.TempDir()
	now := time.Unix(1000, 0).UTC()
	clock := func() time.Time { return now }
	engine, err := New(Options{Directory: directory, Source: SourceEngine, MaxFileBytes: 1 << 20, MaxFiles: 4, Retention: time.Hour, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	target := model.Target{NetworkProfileID: "home-secret", Hostname: "private.example", Port: 443, Transport: model.TransportTCP}
	committed := true
	proxyDecisionMS := int64(120)
	directFailure := model.Observation{Path: model.PathDirect, StageReached: model.StageOutbound, FailureClass: "timeout"}
	if err := engine.Record(Event{EventType: "decision", Target: &target, SelectedPath: model.PathProxy,
		ReasonCode: "proxy_candidate_won", Committed: &committed, DecisionLatencyMS: &proxyDecisionMS,
		Observation:      &model.Observation{Path: model.PathProxy, Success: true, StageReached: model.StageTLS, Latency: 80 * time.Millisecond},
		OtherObservation: &directFailure}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	directDecisionMS := int64(40)
	if err := engine.Record(Event{EventType: "decision", Target: &target, SelectedPath: model.PathDirect,
		ReasonCode: "direct_candidate_won", Committed: &committed, DecisionLatencyMS: &directDecisionMS,
		Observation: &model.Observation{Path: model.PathDirect, Success: true, StageReached: model.StageTLS, Latency: 30 * time.Millisecond}}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := engine.Record(Event{EventType: "diagnostic", Target: &target, ReasonCode: "tls_candidates_failed"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := engine.Record(Event{EventType: "learning_health", Target: &target, ReasonCode: "learning_frozen_global_outage"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := engine.Record(Event{EventType: "durable_learning_assessment", Target: &target, ReasonCode: "durable_proxy_route_suggested"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}

	guard, err := New(Options{Directory: directory, Source: SourceGuard, MaxFileBytes: 1 << 20, MaxFiles: 4, Retention: time.Hour, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Record(Event{EventType: "guard_decision", Target: &target, SelectedLane: "original", ReasonCode: "adaptive_unavailable_use_original", Committed: &committed}); err != nil {
		t.Fatal(err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := BuildReport(directory, ReportOptions{Since: time.Unix(900, 0), Clock: func() time.Time { return time.Unix(2000, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	if report.EventsIncluded != 6 || report.FilesScanned != 2 || report.TargetScopesObserved != 1 || report.NetworkProfilesObserved != 1 {
		t.Fatalf("report=%+v", report)
	}
	if report.Adaptive.ReadinessOutcomes != 3 || report.Adaptive.Ready != 2 || report.Adaptive.FailedBeforeReadiness != 1 || report.Adaptive.ReadinessSuccessRatio != 2.0/3.0 {
		t.Fatalf("adaptive=%+v", report.Adaptive)
	}
	if report.Adaptive.SelectedDirect != 1 || report.Adaptive.SelectedProxy != 1 || report.Adaptive.ProxySelectionRatio != .5 || report.Adaptive.DirectFailedProxySucceeded != 1 || report.Adaptive.NoCompletedOpposite != 1 {
		t.Fatalf("adaptive=%+v", report.Adaptive)
	}
	assertDistribution(t, report.Adaptive.DecisionReadinessLatencyMS, 2, 40, 120, 120)
	assertDistribution(t, report.Adaptive.WinnerCandidateLatencyMS, 2, 30, 80, 80)
	if report.Guard.Decisions != 1 || report.Guard.OriginalSelected != 1 || report.HealthTransitions != 1 || report.DurableAssessments != 1 {
		t.Fatalf("guard/health report=%+v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), target.Hostname) || strings.Contains(string(encoded), target.NetworkProfileID) || strings.Contains(string(encoded), "hostname_hash") {
		t.Fatalf("identity leaked: %s", encoded)
	}
}

func TestBuildReportHonorsSinceAndRejectsCorruptRows(t *testing.T) {
	directory := t.TempDir()
	now := time.Unix(100, 0).UTC()
	recorder, err := New(Options{Directory: directory, Source: SourceEngine, MaxFileBytes: 1 << 20, MaxFiles: 2, Retention: time.Hour, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	committed := true
	target := model.Target{NetworkProfileID: "profile", Hostname: "old.example", Port: 443, Transport: model.TransportTCP}
	if err := recorder.Record(Event{EventType: "decision", Target: &target, SelectedPath: model.PathDirect, ReasonCode: "direct_candidate_won", Committed: &committed,
		Observation: &model.Observation{Path: model.PathDirect, Success: true, StageReached: model.StageTCP}}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport(directory, ReportOptions{Since: now.Add(time.Second)})
	if err != nil || report.EventsIncluded != 0 || report.FilesScanned != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}

	corruptDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(corruptDir, SourceEngine), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(corruptDir, SourceEngine, "corrupt.jsonl")
	row := `{"schema_version":1,"recorded_at":"2026-08-02T00:00:00Z","source":"engine","event_type":"decision","unknown":true}` + "\n"
	if err := os.WriteFile(path, []byte(row), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildReport(corruptDir, ReportOptions{Since: time.Unix(1, 0)}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("corrupt error=%v", err)
	}
}

func TestBuildReportValidatesOptionsAndSourceBinding(t *testing.T) {
	if _, err := BuildReport("", ReportOptions{Since: time.Now()}); err == nil {
		t.Fatal("empty directory accepted")
	}
	if _, err := BuildReport(t.TempDir(), ReportOptions{}); err == nil {
		t.Fatal("zero since accepted")
	}
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, SourceGuard), 0o700); err != nil {
		t.Fatal(err)
	}
	row := `{"schema_version":1,"recorded_at":"2026-08-02T00:00:00Z","source":"engine","event_type":"guard_decision"}` + "\n"
	if err := os.WriteFile(filepath.Join(directory, SourceGuard, "wrong-source.jsonl"), []byte(row), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("source error=%v", err)
	}
}

func TestBuildReportRejectsIdentityLikeUnknownReason(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, SourceEngine), 0o700); err != nil {
		t.Fatal(err)
	}
	row := `{"schema_version":1,"recorded_at":"2026-08-02T00:00:00Z","source":"engine","event_type":"decision","reason_code":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}` + "\n"
	if err := os.WriteFile(filepath.Join(directory, SourceEngine, "unknown-reason.jsonl"), []byte(row), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)}); err == nil || !strings.Contains(err.Error(), "unknown reason_code") {
		t.Fatalf("reason error=%v", err)
	}
}

func assertDistribution(t *testing.T, got MillisecondDistribution, samples int, p50, p95, p99 int64) {
	t.Helper()
	if got.Samples != samples || got.P50 == nil || *got.P50 != p50 || got.P95 == nil || *got.P95 != p95 || got.P99 == nil || *got.P99 != p99 {
		t.Fatalf("distribution=%+v", got)
	}
}
