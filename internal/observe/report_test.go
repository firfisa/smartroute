package observe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/netrelay"
)

func TestBuildReportAggregatesReadinessWithoutIdentity(t *testing.T) {
	directory := t.TempDir()
	now := time.Unix(1000, 0).UTC()
	clock := func() time.Time { return now }
	trialSession := "trial-0123456789abcdef0123456789abcdef"
	engine, err := New(Options{Directory: directory, Source: SourceEngine, MaxFileBytes: 1 << 20, MaxFiles: 4, Retention: time.Hour, Clock: clock, TrialSessionID: trialSession})
	if err != nil {
		t.Fatal(err)
	}
	target := model.Target{NetworkProfileID: "home-secret", Hostname: "private.example", Port: 443, Transport: model.TransportTCP}
	committed := true
	proxyConnection := "conn-0123456789abcdef0123456789abcdef"
	directConnection := "conn-1123456789abcdef0123456789abcdef"
	diagnosticConnection := "conn-2123456789abcdef0123456789abcdef"
	proxyDecisionMS := int64(120)
	directFailure := model.Observation{Path: model.PathDirect, StageReached: model.StageOutbound, FailureClass: "timeout"}
	if err := engine.Record(Event{EventType: "decision", ConnectionID: proxyConnection, Target: &target, SelectedPath: model.PathProxy,
		DeclaredBaselinePath: model.PathProxy,
		ReasonCode:           "proxy_candidate_won", Committed: &committed, DecisionLatencyMS: &proxyDecisionMS,
		Observation:      &model.Observation{Path: model.PathProxy, Success: true, StageReached: model.StageTLS, Latency: 80 * time.Millisecond},
		OtherObservation: &directFailure}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	directDecisionMS := int64(40)
	if err := engine.Record(Event{EventType: "decision", ConnectionID: directConnection, Target: &target, SelectedPath: model.PathDirect,
		DeclaredBaselinePath: model.PathProxy,
		ReasonCode:           "direct_candidate_won", Committed: &committed, DecisionLatencyMS: &directDecisionMS,
		Observation: &model.Observation{Path: model.PathDirect, Success: true, StageReached: model.StageTLS, Latency: 30 * time.Millisecond}}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	proxyUp, proxyDown, proxyDuration := int64(100), int64(1000), int64(500)
	if err := engine.Record(Event{EventType: "relay_outcome", ConnectionID: proxyConnection, Target: &target, SelectedPath: model.PathProxy,
		DeclaredBaselinePath: model.PathProxy,
		ClientToRemoteBytes:  &proxyUp, RemoteToClientBytes: &proxyDown,
		ClientToRemoteEnd: string(netrelay.EndEOF), RemoteToClientEnd: string(netrelay.EndReset),
		RelayDurationMS: &proxyDuration, Termination: "ended"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	directUp, directDown, directDuration := int64(50), int64(0), int64(200)
	if err := engine.Record(Event{EventType: "relay_outcome", ConnectionID: directConnection, Target: &target, SelectedPath: model.PathDirect,
		DeclaredBaselinePath: model.PathProxy,
		ClientToRemoteBytes:  &directUp, RemoteToClientBytes: &directDown,
		ClientToRemoteEnd: string(netrelay.EndCanceled), RemoteToClientEnd: string(netrelay.EndCanceled),
		RelayDurationMS: &directDuration, Termination: "canceled"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := engine.Record(Event{EventType: "diagnostic", ConnectionID: diagnosticConnection, Target: &target, ReasonCode: "tls_candidates_failed"}); err != nil {
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

	guard, err := New(Options{Directory: directory, Source: SourceGuard, MaxFileBytes: 1 << 20, MaxFiles: 4, Retention: time.Hour, Clock: clock, TrialSessionID: trialSession})
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Record(Event{EventType: "guard_decision", Target: &target, SelectedLane: "original", ReasonCode: "adaptive_unavailable_use_original", Committed: &committed}); err != nil {
		t.Fatal(err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := BuildReport(directory, ReportOptions{Since: time.Unix(900, 0), ExpectedTrialSessionID: trialSession, Clock: func() time.Time { return time.Unix(2000, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	if report.ReportVersion != ReportVersion || report.EventsIncluded != 8 || report.FilesScanned != 2 || report.TargetScopesObserved != 1 || report.NetworkProfilesObserved != 1 || report.TrialSessionsObserved != 1 || report.UnscopedEvents != 0 || !report.ExpectedTrialSessionMatched || report.UnexpectedTrialSessionEvents != 0 {
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
	if report.Adaptive.Relay.Outcomes != 2 || report.Adaptive.Relay.Ended != 1 || report.Adaptive.Relay.Canceled != 1 ||
		report.Adaptive.Relay.WithRemoteToClientBytes != 1 || report.Adaptive.Relay.RemoteToClientCoverageRatio != .5 ||
		report.Adaptive.Relay.ClientToRemoteBytes != 150 || report.Adaptive.Relay.RemoteToClientBytes != 1000 ||
		report.Adaptive.Relay.Direct.Connections != 1 || report.Adaptive.Relay.Direct.ClientToRemoteBytes != 50 ||
		report.Adaptive.Relay.Proxy.Connections != 1 || report.Adaptive.Relay.Proxy.RemoteToClientBytes != 1000 {
		t.Fatalf("relay=%+v", report.Adaptive.Relay)
	}
	if report.Adaptive.Relay.ClientToRemoteEnd.EOF != 1 || report.Adaptive.Relay.ClientToRemoteEnd.Canceled != 1 ||
		report.Adaptive.Relay.RemoteToClientEnd.Reset != 1 || report.Adaptive.Relay.RemoteToClientEnd.Canceled != 1 ||
		!report.Interpretation.DirectionEndsNotApplicationSuccess {
		t.Fatalf("relay ends=%+v/%+v interpretation=%+v", report.Adaptive.Relay.ClientToRemoteEnd, report.Adaptive.Relay.RemoteToClientEnd, report.Interpretation)
	}
	assertDistribution(t, report.Adaptive.Relay.DurationMS, 2, 200, 500, 500)
	if !report.Interpretation.RelayRemoteBytesNotApplicationSuccess || !report.Interpretation.RelayBytesPostCommitAdaptiveOnly {
		t.Fatalf("interpretation=%+v", report.Interpretation)
	}
	if report.ConnectionScope.ScopedDecisions != 2 || report.ConnectionScope.ScopedCommittedDecisions != 2 || report.ConnectionScope.ScopedDiagnostics != 1 ||
		report.ConnectionScope.ScopedRelayOutcomes != 2 || report.ConnectionScope.PairedRelayOutcomes != 2 ||
		report.ConnectionScope.UnmatchedRelayOutcomes != 0 || report.ConnectionScope.CommittedDecisionsWithoutOutcome != 0 ||
		!report.Interpretation.ConnectionIDsOmitted {
		t.Fatalf("connection scope=%+v interpretation=%+v", report.ConnectionScope, report.Interpretation)
	}
	if report.DeclaredBaseline.ScopedSelections != 2 || report.DeclaredBaseline.UnscopedSelections != 0 ||
		report.DeclaredBaseline.SameAsDeclared != 1 || report.DeclaredBaseline.ChangedFromDeclared != 1 ||
		report.DeclaredBaseline.DirectInsteadOfProxy != 1 || report.DeclaredBaseline.ProxyInsteadOfDirect != 0 ||
		report.DeclaredBaseline.ChangedSelectionRatio != .5 || report.DeclaredBaseline.ScopedRelayOutcomes != 2 ||
		report.DeclaredBaseline.ChangedRelayOutcomes != 1 || report.DeclaredBaseline.ChangedClientToRemoteBytes != 50 ||
		report.DeclaredBaseline.ChangedRemoteToClientBytes != 0 || !report.Interpretation.BaselineIsDeclaredNotObserved ||
		!report.Interpretation.ChangedBytesNotCounterfactualSavings || !report.Interpretation.NoVerifiedStaticBaseline {
		t.Fatalf("declared baseline=%+v interpretation=%+v", report.DeclaredBaseline, report.Interpretation)
	}
	if report.Guard.Decisions != 1 || report.Guard.OriginalSelected != 1 || report.HealthTransitions != 1 || report.DurableAssessments != 1 {
		t.Fatalf("guard/health report=%+v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), target.Hostname) || strings.Contains(string(encoded), target.NetworkProfileID) ||
		strings.Contains(string(encoded), trialSession) || strings.Contains(string(encoded), proxyConnection) ||
		strings.Contains(string(encoded), "hostname_hash") || strings.Contains(string(encoded), `"connection_id":`) ||
		strings.Contains(string(encoded), `"declared_baseline_path":`) {
		t.Fatalf("identity leaked: %s", encoded)
	}
}

func TestBuildReportAcceptsAndCountsAutomaticPolicyReasons(t *testing.T) {
	directory := t.TempDir()
	recorder, err := New(Options{Directory: directory, Source: SourceEngine, MaxFileBytes: 1 << 20, MaxFiles: 2, Retention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	target := model.Target{NetworkProfileID: "profile", Hostname: "example.com", Port: 443, Transport: model.TransportTCP}
	committed := true
	for _, event := range []Event{
		{EventType: "decision", Target: &target, SelectedPath: model.PathProxy,
			ReasonCode: "durable_policy_selected", LearningReason: "automatic_path_unchanged",
			Committed: &committed, Observation: &model.Observation{Path: model.PathProxy, Success: true, StageReached: model.StageTLS}},
		{EventType: "decision", Target: &target, SelectedPath: model.PathDirect,
			ReasonCode: "durable_policy_fallback", LearningReason: "automatic_direct_path_remembered",
			DurableReason: "durable_policy_queued", Committed: &committed,
			Observation: &model.Observation{Path: model.PathDirect, Success: true, StageReached: model.StageTLS}},
	} {
		if err := recorder.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if report.ReasonCounts["durable_policy_selected"] != 1 || report.ReasonCounts["durable_policy_fallback"] != 1 ||
		report.LearningReasonCounts["automatic_path_unchanged"] != 1 ||
		report.LearningReasonCounts["automatic_direct_path_remembered"] != 1 ||
		report.DurableReasonCounts["durable_policy_queued"] != 1 {
		t.Fatalf("report=%+v", report)
	}
}

func TestBuildReportVerifiesExpectedTrialSessionWithoutExposingIt(t *testing.T) {
	directory := t.TempDir()
	now := time.Unix(1000, 0).UTC()
	expected := "trial-0123456789abcdef0123456789abcdef"
	unexpected := "trial-1123456789abcdef0123456789abcdef"
	committed := true
	for _, session := range []string{expected, unexpected} {
		recorder, err := New(Options{Directory: directory, Source: SourceEngine, MaxFileBytes: 4096, MaxFiles: 4,
			Retention: time.Hour, Clock: func() time.Time { return now }, TrialSessionID: session})
		if err != nil {
			t.Fatal(err)
		}
		target := model.Target{NetworkProfileID: "profile", Hostname: "example.test", Port: 443, Transport: model.TransportTCP}
		if err := recorder.Record(Event{EventType: "decision", Target: &target, SelectedPath: model.PathDirect,
			ReasonCode: "direct_candidate_won", Committed: &committed,
			Observation: &model.Observation{Path: model.PathDirect, Success: true, StageReached: model.StageTCP}}); err != nil {
			t.Fatal(err)
		}
		if err := recorder.Close(); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
	}
	report, err := BuildReport(directory, ReportOptions{Since: time.Unix(900, 0), ExpectedTrialSessionID: expected})
	if err != nil {
		t.Fatal(err)
	}
	if !report.ExpectedTrialSessionMatched || report.UnexpectedTrialSessionEvents != 1 || report.TrialSessionsObserved != 2 {
		t.Fatalf("report=%+v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), expected) || strings.Contains(string(encoded), unexpected) {
		t.Fatalf("trial session leaked: %s", encoded)
	}
	if _, err := BuildReport(directory, ReportOptions{Since: time.Unix(900, 0), ExpectedTrialSessionID: "bad"}); err == nil {
		t.Fatal("invalid expected trial session accepted")
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
	if _, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)}); err == nil || !strings.Contains(err.Error(), "unknown reason_code") || strings.Contains(err.Error(), "0123456789abcdef") {
		t.Fatalf("reason error=%v", err)
	}
}

func TestBuildReportAcceptsLegacyDecisionsButRejectsLegacyRelayOutcome(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, SourceEngine), 0o700); err != nil {
		t.Fatal(err)
	}
	committed := true
	bytesUp, bytesDown, duration := int64(1), int64(2), int64(3)
	target := &storedTarget{NetworkProfileHash: strings.Repeat("a", 64), HostnameHash: strings.Repeat("b", 64), Port: 443, Transport: model.TransportTCP}
	legacyDecision := storedEvent{
		SchemaVersion: legacySchemaVersion, RecordedAt: time.Unix(100, 0).UTC(), Source: SourceEngine,
		EventType: "decision", Target: target, SelectedPath: model.PathDirect, ReasonCode: "direct_candidate_won",
		Committed: &committed, Observation: &model.Observation{Path: model.PathDirect, Success: true, StageReached: model.StageTCP},
	}
	decisionJSON, err := json.Marshal(legacyDecision)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, SourceEngine, "legacy.jsonl")
	if err := os.WriteFile(path, append(decisionJSON, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if report, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)}); err != nil || report.Adaptive.Ready != 1 {
		t.Fatalf("legacy decision report=%+v err=%v", report, err)
	}

	legacyRelay := storedEvent{
		SchemaVersion: legacySchemaVersion, RecordedAt: time.Unix(101, 0).UTC(), Source: SourceEngine,
		EventType: "relay_outcome", Target: target, SelectedPath: model.PathDirect,
		ClientToRemoteBytes: &bytesUp, RemoteToClientBytes: &bytesDown, RelayDurationMS: &duration, Termination: "ended",
	}
	relayJSON, err := json.Marshal(legacyRelay)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(append(relayJSON, '\n')); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)}); err == nil || !strings.Contains(err.Error(), "requires observation schema 2 or newer") {
		t.Fatalf("legacy relay error=%v", err)
	}
}

func TestBuildReportRejectsRelayByteOverflow(t *testing.T) {
	directory := t.TempDir()
	recorder, err := New(Options{Directory: directory, Source: SourceEngine, MaxFileBytes: 1 << 20, MaxFiles: 2, Retention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	target := model.Target{NetworkProfileID: "profile", Hostname: "example.com", Port: 443, Transport: model.TransportTCP}
	zero, duration := int64(0), int64(1)
	for _, bytes := range []int64{maxInt64, 1} {
		value := bytes
		if err := recorder.Record(Event{EventType: "relay_outcome", Target: &target, SelectedPath: model.PathProxy,
			ClientToRemoteBytes: &value, RemoteToClientBytes: &zero,
			ClientToRemoteEnd: string(netrelay.EndEOF), RemoteToClientEnd: string(netrelay.EndEOF),
			RelayDurationMS: &duration, Termination: "ended"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)}); err == nil || !strings.Contains(err.Error(), "exceeds int64") {
		t.Fatalf("overflow error=%v", err)
	}
}

func TestBuildReportAcceptsSchemaTwoRelayAsUnscoped(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, SourceEngine), 0o700); err != nil {
		t.Fatal(err)
	}
	up, down, duration := int64(10), int64(20), int64(30)
	event := storedEvent{
		SchemaVersion: relaySchemaVersion, RecordedAt: time.Unix(100, 0).UTC(), Source: SourceEngine,
		EventType: "relay_outcome", Target: &storedTarget{
			NetworkProfileHash: strings.Repeat("a", 64), HostnameHash: strings.Repeat("b", 64),
			Port: 443, Transport: model.TransportTCP,
		},
		SelectedPath: model.PathProxy, ClientToRemoteBytes: &up, RemoteToClientBytes: &down,
		RelayDurationMS: &duration, Termination: "ended",
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, SourceEngine, "schema2.jsonl"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Adaptive.Relay.Outcomes != 1 || report.ConnectionScope.UnscopedRelayOutcomes != 1 || report.ConnectionScope.ScopedRelayOutcomes != 0 {
		t.Fatalf("report=%+v", report)
	}
	if report.Adaptive.Relay.ClientToRemoteEnd.Unclassified != 1 || report.Adaptive.Relay.RemoteToClientEnd.Unclassified != 1 {
		t.Fatalf("legacy relay ends=%+v/%+v", report.Adaptive.Relay.ClientToRemoteEnd, report.Adaptive.Relay.RemoteToClientEnd)
	}
}

func TestBuildReportRejectsUnsafeSchemaFiveRelayEnd(t *testing.T) {
	directory := t.TempDir()
	engineDirectory := filepath.Join(directory, SourceEngine)
	if err := os.MkdirAll(engineDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	event := storedEvent{SchemaVersion: schemaVersion, RecordedAt: time.Unix(100, 0).UTC(), Source: SourceEngine,
		EventType: "relay_outcome", Target: &storedTarget{NetworkProfileHash: strings.Repeat("a", 64),
			HostnameHash: strings.Repeat("b", 64), Port: 443, Transport: model.TransportTCP},
		SelectedPath: model.PathProxy, ClientToRemoteBytes: &zero, RemoteToClientBytes: &zero,
		ClientToRemoteEnd: string(netrelay.EndEOF), RemoteToClientEnd: "reset private.example:443",
		RelayDurationMS: &zero, Termination: "ended"}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engineDirectory, "unsafe-end.jsonl"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)}); err == nil ||
		!strings.Contains(err.Error(), "bounded direction end reasons") || strings.Contains(err.Error(), "private.example") {
		t.Fatalf("unsafe end report error=%v", err)
	}
}

func TestBuildReportAcceptsSchemaThreePairWithoutDeclaredBaseline(t *testing.T) {
	directory := t.TempDir()
	engineDirectory := filepath.Join(directory, SourceEngine)
	if err := os.MkdirAll(engineDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	committed := true
	up, down, duration := int64(1), int64(2), int64(3)
	connection := "conn-7123456789abcdef0123456789abcdef"
	target := &storedTarget{NetworkProfileHash: strings.Repeat("a", 64), HostnameHash: strings.Repeat("b", 64), Port: 443, Transport: model.TransportTCP}
	events := []storedEvent{
		{SchemaVersion: connectionSchemaVersion, RecordedAt: time.Unix(100, 0).UTC(), Source: SourceEngine,
			EventType: "decision", ConnectionID: connection, Target: target, SelectedPath: model.PathDirect,
			ReasonCode: "direct_candidate_won", Committed: &committed,
			Observation: &model.Observation{Path: model.PathDirect, Success: true, StageReached: model.StageTCP}},
		{SchemaVersion: connectionSchemaVersion, RecordedAt: time.Unix(101, 0).UTC(), Source: SourceEngine,
			EventType: "relay_outcome", ConnectionID: connection, Target: target, SelectedPath: model.PathDirect,
			ClientToRemoteBytes: &up, RemoteToClientBytes: &down, RelayDurationMS: &duration, Termination: "ended"},
	}
	file, err := os.OpenFile(filepath.Join(engineDirectory, "schema3.jsonl"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		data, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			_ = file.Close()
			t.Fatal(marshalErr)
		}
		if _, writeErr := file.Write(append(data, '\n')); writeErr != nil {
			_ = file.Close()
			t.Fatal(writeErr)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if report.ConnectionScope.PairedRelayOutcomes != 1 || report.DeclaredBaseline.UnscopedSelections != 1 ||
		report.DeclaredBaseline.UnscopedRelayOutcomes != 1 || report.DeclaredBaseline.ScopedSelections != 0 {
		t.Fatalf("report=%+v", report)
	}
	if report.Adaptive.Relay.ClientToRemoteEnd.Unclassified != 1 || report.Adaptive.Relay.RemoteToClientEnd.Unclassified != 1 {
		t.Fatalf("schema-3 relay ends=%+v/%+v", report.Adaptive.Relay.ClientToRemoteEnd, report.Adaptive.Relay.RemoteToClientEnd)
	}
}

func TestBuildReportAcceptsSchemaFourRelayWithoutDirectionEnds(t *testing.T) {
	directory := t.TempDir()
	engineDirectory := filepath.Join(directory, SourceEngine)
	if err := os.MkdirAll(engineDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	up, down, duration := int64(4), int64(5), int64(6)
	event := storedEvent{SchemaVersion: baselineSchemaVersion, RecordedAt: time.Unix(100, 0).UTC(), Source: SourceEngine,
		EventType: "relay_outcome", ConnectionID: "conn-9123456789abcdef0123456789abcdef",
		Target:               &storedTarget{NetworkProfileHash: strings.Repeat("a", 64), HostnameHash: strings.Repeat("b", 64), Port: 443, Transport: model.TransportTCP},
		DeclaredBaselinePath: model.PathProxy, SelectedPath: model.PathDirect,
		ClientToRemoteBytes: &up, RemoteToClientBytes: &down, RelayDurationMS: &duration, Termination: "ended"}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engineDirectory, "schema4.jsonl"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if report.ConnectionScope.UnmatchedRelayOutcomes != 1 || report.DeclaredBaseline.ScopedRelayOutcomes != 1 ||
		report.DeclaredBaseline.ChangedRelayOutcomes != 1 || report.Adaptive.Relay.ClientToRemoteEnd.Unclassified != 1 ||
		report.Adaptive.Relay.RemoteToClientEnd.Unclassified != 1 {
		t.Fatalf("schema-4 report=%+v", report)
	}
}

func TestBuildReportCountsWindowTruncatedConnectionPairs(t *testing.T) {
	directory := t.TempDir()
	recorder, err := New(Options{Directory: directory, Source: SourceEngine, MaxFileBytes: 1 << 20, MaxFiles: 2, Retention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	target := model.Target{NetworkProfileID: "profile", Hostname: "example.com", Port: 443, Transport: model.TransportTCP}
	committed := true
	missingOutcomeID := "conn-3123456789abcdef0123456789abcdef"
	unmatchedOutcomeID := "conn-4123456789abcdef0123456789abcdef"
	if err := recorder.Record(Event{EventType: "decision", ConnectionID: missingOutcomeID, Target: &target,
		SelectedPath: model.PathDirect, ReasonCode: "direct_candidate_won", Committed: &committed,
		Observation: &model.Observation{Path: model.PathDirect, Success: true, StageReached: model.StageTCP}}); err != nil {
		t.Fatal(err)
	}
	up, down, duration := int64(1), int64(2), int64(3)
	if err := recorder.Record(Event{EventType: "relay_outcome", ConnectionID: unmatchedOutcomeID, Target: &target,
		SelectedPath: model.PathProxy, ClientToRemoteBytes: &up, RemoteToClientBytes: &down,
		ClientToRemoteEnd: string(netrelay.EndEOF), RemoteToClientEnd: string(netrelay.EndEOF),
		RelayDurationMS: &duration, Termination: "ended"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Record(Event{EventType: "relay_outcome", Target: &target, SelectedPath: model.PathProxy,
		ClientToRemoteBytes: &up, RemoteToClientBytes: &down,
		ClientToRemoteEnd: string(netrelay.EndEOF), RemoteToClientEnd: string(netrelay.EndEOF),
		RelayDurationMS: &duration, Termination: "ended"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if report.ConnectionScope.ScopedDecisions != 1 || report.ConnectionScope.ScopedRelayOutcomes != 1 ||
		report.ConnectionScope.UnmatchedRelayOutcomes != 1 || report.ConnectionScope.CommittedDecisionsWithoutOutcome != 1 ||
		report.ConnectionScope.UnscopedRelayOutcomes != 1 || report.ConnectionScope.PairedRelayOutcomes != 0 {
		t.Fatalf("connection scope=%+v", report.ConnectionScope)
	}
}

func TestBuildReportRejectsContradictoryScopedPair(t *testing.T) {
	directory := t.TempDir()
	recorder, err := New(Options{Directory: directory, Source: SourceEngine, MaxFileBytes: 1 << 20, MaxFiles: 2, Retention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	target := model.Target{NetworkProfileID: "profile", Hostname: "example.com", Port: 443, Transport: model.TransportTCP}
	connection := "conn-5123456789abcdef0123456789abcdef"
	committed := true
	if err := recorder.Record(Event{EventType: "decision", ConnectionID: connection, Target: &target,
		SelectedPath: model.PathDirect, ReasonCode: "direct_candidate_won", Committed: &committed,
		Observation: &model.Observation{Path: model.PathDirect, Success: true, StageReached: model.StageTCP}}); err != nil {
		t.Fatal(err)
	}
	up, down, duration := int64(1), int64(2), int64(3)
	if err := recorder.Record(Event{EventType: "relay_outcome", ConnectionID: connection, Target: &target,
		SelectedPath: model.PathProxy, ClientToRemoteBytes: &up, RemoteToClientBytes: &down,
		ClientToRemoteEnd: string(netrelay.EndEOF), RemoteToClientEnd: string(netrelay.EndEOF),
		RelayDurationMS: &duration, Termination: "ended"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)}); err == nil || !strings.Contains(err.Error(), "disagree on target, selected path, or declared baseline") || strings.Contains(err.Error(), connection) {
		t.Fatalf("pairing error=%v", err)
	}
}

func TestBuildReportRejectsScopedPairWithDifferentDeclaredBaseline(t *testing.T) {
	directory := t.TempDir()
	recorder, err := New(Options{Directory: directory, Source: SourceEngine, MaxFileBytes: 1 << 20, MaxFiles: 2, Retention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	target := model.Target{NetworkProfileID: "profile", Hostname: "example.com", Port: 443, Transport: model.TransportTCP}
	connection := "conn-8123456789abcdef0123456789abcdef"
	committed := true
	if err := recorder.Record(Event{EventType: "decision", ConnectionID: connection, Target: &target,
		DeclaredBaselinePath: model.PathProxy, SelectedPath: model.PathDirect, ReasonCode: "direct_candidate_won", Committed: &committed,
		Observation: &model.Observation{Path: model.PathDirect, Success: true, StageReached: model.StageTCP}}); err != nil {
		t.Fatal(err)
	}
	up, down, duration := int64(1), int64(2), int64(3)
	if err := recorder.Record(Event{EventType: "relay_outcome", ConnectionID: connection, Target: &target,
		DeclaredBaselinePath: model.PathDirect, SelectedPath: model.PathDirect,
		ClientToRemoteBytes: &up, RemoteToClientBytes: &down,
		ClientToRemoteEnd: string(netrelay.EndEOF), RemoteToClientEnd: string(netrelay.EndEOF),
		RelayDurationMS: &duration, Termination: "ended"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildReport(directory, ReportOptions{Since: time.Unix(1, 0)}); err == nil ||
		!strings.Contains(err.Error(), "declared baseline") || strings.Contains(err.Error(), connection) {
		t.Fatalf("baseline pairing error=%v", err)
	}
}

func assertDistribution(t *testing.T, got MillisecondDistribution, samples int, p50, p95, p99 int64) {
	t.Helper()
	if got.Samples != samples || got.P50 == nil || *got.P50 != p50 || got.P95 == nil || *got.P95 != p95 || got.P99 == nil || *got.P99 != p99 {
		t.Fatalf("distribution=%+v", got)
	}
}
