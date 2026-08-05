package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/config"
	"github.com/firfisa/smartroute/internal/fixedpolicy"
	"github.com/firfisa/smartroute/internal/health"
	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/observe"
	"github.com/firfisa/smartroute/internal/store"
	"github.com/firfisa/smartroute/internal/trial"
)

type recordingDurableWriter struct {
	requests []store.WriteRequest
	reason   string
}

type recordingPolicyWriter struct {
	requests []store.PolicyWriteRequest
	reason   string
}

func (w *recordingPolicyWriter) Enqueue(request store.PolicyWriteRequest) (bool, string) {
	w.requests = append(w.requests, request)
	return true, w.reason
}

func (w *recordingDurableWriter) Enqueue(request store.WriteRequest) (bool, string) {
	w.requests = append(w.requests, request)
	return true, w.reason
}

func testRuntimeLearner(t *testing.T, writer durableEvidenceWriter) *runtimeLearningEngine {
	t.Helper()
	engine, err := learning.New(learning.Config{
		Mode: learning.ModeShadow, DirectPromotionWins: 2, ProxyPromotionWins: 2,
		TTL: time.Hour, MaxEntries: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &runtimeLearningEngine{ephemeral: engine, writer: writer, clock: func() time.Time {
		return time.Unix(123, 0).UTC()
	}}
}

func TestRuntimeLearningQueuesOnlyStrongEvidence(t *testing.T) {
	writer := &recordingDurableWriter{reason: store.ReasonDurableQueued}
	learner := testRuntimeLearner(t, writer)
	target := model.Target{NetworkProfileID: "profile", Hostname: "example.com", Port: 443, Transport: model.TransportTCP}
	winner := model.Observation{Path: model.PathProxy, Success: true, StageReached: model.StageTLS}
	failed := model.Observation{Path: model.PathDirect, StageReached: model.StageTCP, FailureClass: "timeout"}

	update, err := learner.Observe(target, winner, &failed)
	if err != nil {
		t.Fatal(err)
	}
	if update.DurableReason != store.ReasonDurableQueued || len(writer.requests) != 1 || !writer.requests[0].ObservedAt.Equal(time.Unix(123, 0).UTC()) {
		t.Fatalf("update=%+v requests=%+v", update, writer.requests)
	}
	if _, err := learner.Observe(target, winner, nil); err != nil {
		t.Fatal(err)
	}
	if len(writer.requests) != 1 {
		t.Fatalf("weak evidence queued: %+v", writer.requests)
	}
}

func TestRuntimeHealthFreezeClearsPreferenceAndSuppressesDurableWrite(t *testing.T) {
	clock := time.Unix(123, 0).UTC()
	engine, err := learning.New(learning.Config{Mode: learning.ModeEphemeralAuto,
		DirectPromotionWins: 2, ProxyPromotionWins: 2, TTL: time.Hour, MaxEntries: 10,
		Clock: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := health.New(health.Config{FailureThreshold: 3, RecoveryThreshold: 2,
		FailureWindow: time.Minute, FreezeDuration: time.Minute, Clock: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	writer := &recordingDurableWriter{reason: store.ReasonDurableQueued}
	events := make(chan health.Event, 1)
	learner := &runtimeLearningEngine{ephemeral: engine, writer: writer, health: gate,
		clock: func() time.Time { return clock }, onHealth: func(event health.Event) { events <- event }}
	winner := model.Observation{Path: model.PathDirect, Success: true, StageReached: model.StageTLS}
	failed := model.Observation{Path: model.PathProxy, StageReached: model.StageOutbound, FailureClass: "timeout"}
	first := model.Target{NetworkProfileID: "profile", Hostname: "a.example", Port: 443, Transport: model.TransportTCP}
	for range 2 {
		if _, err := learner.Observe(first, winner, &failed); err != nil {
			t.Fatal(err)
		}
	}
	if learner.PreferredPath(first) != model.PathDirect {
		t.Fatal("expected promoted Direct preference")
	}
	for _, host := range []string{"b.example", "c.example"} {
		update, err := learner.Observe(model.Target{NetworkProfileID: "profile", Hostname: host, Port: 443, Transport: model.TransportTCP}, winner, &failed)
		if err != nil {
			t.Fatal(err)
		}
		if host == "c.example" && update.ReasonCode != learning.ReasonHealthFrozen {
			t.Fatalf("freeze update=%+v", update)
		}
	}
	if len(writer.requests) != 3 {
		t.Fatalf("durable writes=%d, want 3 before threshold-triggering evidence", len(writer.requests))
	}
	if learner.PreferredPath(first) != "" {
		t.Fatal("frozen learner retained preference")
	}
	if _, ok := engine.Lookup(first); ok {
		t.Fatal("freeze did not clear ephemeral policy table")
	}
	select {
	case event := <-events:
		if event.ReasonCode != health.ReasonProxyOutage {
			t.Fatalf("event=%+v", event)
		}
	default:
		t.Fatal("missing health event")
	}
}

func TestDurableLearningCanBeDisabledAndDrainsWhenEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.Learning.Mode = learning.ModeShadow
	cfg.Learning.Persistence.DatabasePath = filepath.Join(t.TempDir(), "learning.db")
	cfg.Learning.Persistence.Enabled = false
	runtime, err := openDurableLearning(context.Background(), cfg, nil, nil)
	if err != nil || runtime != nil {
		t.Fatalf("disabled runtime=%v error=%v", runtime, err)
	}
	if _, err := os.Stat(cfg.Learning.Persistence.DatabasePath); !os.IsNotExist(err) {
		t.Fatalf("disabled persistence created database: %v", err)
	}

	cfg.Learning.Persistence.Enabled = true
	runtime, err = openDurableLearning(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	target := model.Target{NetworkProfileID: "profile", Hostname: "secret.example", Port: 443, Transport: model.TransportTCP}
	winner := model.Observation{Path: model.PathProxy, Success: true, StageReached: model.StageTLS}
	failed := model.Observation{Path: model.PathDirect, StageReached: model.StageTCP, FailureClass: "timeout"}
	if accepted, reason := runtime.writer.Enqueue(store.WriteRequest{Target: target, Winner: winner, Other: &failed, ObservedAt: time.Now()}); !accepted || reason != store.ReasonDurableQueued {
		t.Fatalf("enqueue accepted=%v reason=%q", accepted, reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stats, err := runtime.Close(ctx)
	if err != nil || stats.Written != 1 {
		t.Fatalf("close stats=%+v error=%v", stats, err)
	}
	if _, err := os.Stat(cfg.Learning.Persistence.DatabasePath); err != nil {
		t.Fatal(err)
	}
}

func TestDurableLearningEmitsCrossSessionShadowAssessment(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "learning.db")
	historical, err := store.Open(context.Background(), store.Config{Path: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := historical.StartSession(context.Background(), "historical", now); err != nil {
		t.Fatal(err)
	}
	target := model.Target{NetworkProfileID: "profile", Hostname: "example.com", Port: 443, Transport: model.TransportTCP}
	winner := model.Observation{Path: model.PathProxy, Success: true, StageReached: model.StageTLS}
	failed := model.Observation{Path: model.PathDirect, StageReached: model.StageOutbound, FailureClass: "timeout"}
	for index := range 2 {
		if written, err := historical.AppendStrongEvidence(context.Background(), target, "historical", winner, &failed, now.Add(time.Duration(index)*time.Millisecond)); err != nil || !written {
			t.Fatalf("historical append written=%v error=%v", written, err)
		}
	}
	if err := historical.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Learning.Mode = learning.ModeShadow
	cfg.Learning.Persistence.Enabled = true
	cfg.Learning.Persistence.DatabasePath = databasePath
	events := make(chan learning.DurableAssessmentEvent, 1)
	runtime, err := openDurableLearning(context.Background(), cfg, nil, func(event learning.DurableAssessmentEvent) {
		events <- event
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted, reason := runtime.writer.Enqueue(store.WriteRequest{
		Target: target, Winner: winner, Other: &failed, ObservedAt: now.Add(time.Second),
	}); !accepted || reason != store.ReasonDurableQueued {
		t.Fatalf("enqueue accepted=%v reason=%q", accepted, reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := runtime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.EventType != learning.EventTypeDurableAssessment || event.Assessment.State != learning.DurableStateProxySuggested ||
			event.Assessment.SuggestedPath != model.PathProxy || event.Assessment.Evidence.ProxyWins != 3 || event.Assessment.Evidence.ProxySessions != 2 {
			t.Fatalf("assessment event = %+v", event)
		}
	default:
		t.Fatal("durable assessment event was not emitted")
	}
}

func TestDurableAutoMaterializesAndReloadsPolicyWithoutPerTargetApproval(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "learning.db")
	now := time.Now().UTC()
	target := model.Target{NetworkProfileID: "home", Hostname: "auto.example", Port: 443, Transport: model.TransportTCP}
	winner := model.Observation{Path: model.PathProxy, Success: true, StageReached: model.StageTLS}
	cfg := config.Default()
	cfg.Learning.Mode = learning.ModeDurableAuto
	cfg.Learning.Persistence.Enabled = true
	cfg.Learning.Persistence.DatabasePath = databasePath
	runtime, err := openDurableLearning(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	learner := &runtimeLearningEngine{
		automatic: true, durable: runtime.index, policyWriter: runtime.policyWriter,
		clock: func() time.Time { return now },
	}
	update, err := learner.Observe(target, winner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.index.PreferredPath(target); got != model.PathProxy {
		t.Fatalf("first readiness result was not remembered immediately: %q", got)
	}
	if update.DurableReason != store.ReasonPolicyQueued {
		t.Fatalf("durable reason=%q", update.DurableReason)
	}
	if update.ReasonCode != learning.ReasonAutoProxyRemembered || !update.Policy.ExpiresAt.IsZero() {
		t.Fatalf("automatic update unexpectedly used promotion or TTL state: %+v", update)
	}
	secondUpdate, err := learner.Observe(target, winner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if secondUpdate.DurableReason != "" {
		t.Fatalf("unchanged path queued another durable write: %+v", secondUpdate)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stats, err := runtime.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Queued != 1 || stats.Written != 1 || stats.Skipped != 0 {
		t.Fatalf("writer stats=%+v", stats)
	}
	reloaded, err := openDurableLearning(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.index.PreferredPath(target); got != model.PathProxy {
		t.Fatalf("reloaded path=%q", got)
	}
	status, err := reloaded.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.SessionCount != 0 || status.EvidenceCount != 0 || status.DurablePolicies != 1 {
		t.Fatalf("automatic mode stored non-policy history: %+v", status)
	}
	if _, err := reloaded.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticRuntimeAttachmentDoesNotInstallTypedNilEvidenceWriter(t *testing.T) {
	cfg := config.Default()
	cfg.Learning.Persistence.DatabasePath = filepath.Join(t.TempDir(), "learning.db")
	runtime, err := openDurableLearning(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	learner := &runtimeLearningEngine{automatic: true}
	attachDurableLearning(learner, runtime)
	if learner.writer != nil || learner.policyWriter == nil || learner.durable == nil {
		t.Fatalf("automatic attachment writer=%T policyWriter=%T durable=%v", learner.writer, learner.policyWriter, learner.durable != nil)
	}
	target := model.Target{NetworkProfileID: "home", Hostname: "example.test", Port: 443, Transport: model.TransportTCP}
	winner := model.Observation{Path: model.PathProxy, Success: true, StageReached: model.StageTLS}
	if _, err := learner.Observe(target, winner, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := runtime.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDurableAutoOppositeSuccessOverwritesRememberedPath(t *testing.T) {
	cfg := config.Default()
	cfg.Learning.Mode = learning.ModeDurableAuto
	cfg.Learning.Persistence.Enabled = true
	cfg.Learning.Persistence.DatabasePath = filepath.Join(t.TempDir(), "learning.db")
	runtime, err := openDurableLearning(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target := model.Target{NetworkProfileID: "home", Hostname: "changed.example", Port: 443, Transport: model.TransportTCP}
	if _, err := runtime.index.Remember(target, model.PathDirect, now); err != nil {
		t.Fatal(err)
	}
	learner := &runtimeLearningEngine{
		automatic: true, durable: runtime.index, policyWriter: runtime.policyWriter,
		clock: func() time.Time { return now.Add(time.Minute) },
	}
	winner := model.Observation{Path: model.PathProxy, Success: true, StageReached: model.StageTLS}
	failed := model.Observation{Path: model.PathDirect, StageReached: model.StageOutbound, FailureClass: "timeout"}
	if _, err := learner.Observe(target, winner, &failed); err != nil {
		t.Fatal(err)
	}
	if got := runtime.index.PreferredPath(target); got != model.PathProxy {
		t.Fatalf("opposite success did not overwrite immediately: %q", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := runtime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	readOnly, err := store.OpenReadOnly(context.Background(), store.Config{Path: cfg.Learning.Persistence.DatabasePath})
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	policies, err := readOnly.LoadDurablePolicies(context.Background(), cfg.Learning.MaxEntries)
	if err != nil || len(policies) != 1 || policies[0].Path != model.PathProxy {
		t.Fatalf("policies=%+v err=%v", policies, err)
	}
}

func TestLearningSessionIDsAreSafeAndDistinct(t *testing.T) {
	first, err := newLearningSessionID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newLearningSessionID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "session-") || len(first) != len("session-")+32 {
		t.Fatalf("session IDs first=%q second=%q", first, second)
	}
}

func TestParseObservation(t *testing.T) {
	got, err := parseObservation(model.PathDirect, "failure:tcp:250:timeout")
	if err != nil {
		t.Fatalf("parseObservation() error = %v", err)
	}
	if got.Success || got.FailureClass != "timeout" || got.StageReached != model.StageTCP {
		t.Fatalf("parseObservation() = %#v", got)
	}
}

func TestRuntimeEventSinkSuppressesRawOutputWhenRecording(t *testing.T) {
	directory := t.TempDir()
	recorder, err := observe.New(observe.Options{
		Directory: directory, Source: "engine", MaxFileBytes: 4096,
		MaxFiles: 2, Retention: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	sink := newRuntimeEventSink(&stdout, &stderr, recorder)
	target := model.Target{NetworkProfileID: "private-network", Hostname: "secret.example", Port: 443, Transport: model.TransportTCP}
	sink.Emit(struct {
		Target string `json:"target"`
	}{Target: target.Hostname}, observe.Event{EventType: "decision", Target: &target})
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	data := readObservationFiles(t, directory)
	if strings.Contains(data, target.Hostname) || strings.Contains(data, target.NetworkProfileID) {
		t.Fatalf("persistent data contains cleartext identity: %s", data)
	}
}

func TestRuntimeEventSinkPreservesDebugOutputWhenRecordingDisabled(t *testing.T) {
	var stdout, stderr bytes.Buffer
	newRuntimeEventSink(&stdout, &stderr, nil).Emit(map[string]string{"event_type": "decision"}, observe.Event{})
	if !strings.Contains(stdout.String(), "decision") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunObservationsClearRequiresConfirmation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "observations")
	configPath := writeObservationConfig(t, directory)
	var stdout, stderr bytes.Buffer
	err := run([]string{"observations", "clear", "-config", configPath}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "confirm-clear") {
		t.Fatalf("clear error = %v", err)
	}
}

func TestRunObservationsPauseAndStatus(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "observations")
	configPath := writeObservationConfig(t, directory)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"observations", "pause", "-config", configPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"observations", "status", "-config", configPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"paused":true`) {
		t.Fatalf("status = %q", stdout.String())
	}
}

func TestRunTrialPreflightEmitsMachineReadableFailure(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "observations")
	if err := observe.Pause(directory); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Observation.Enabled = true
	cfg.Observation.Directory = directory
	configPath := writeConfig(t, cfg)
	var stdout, stderr bytes.Buffer
	err := run([]string{"trial", "preflight", "-config", configPath, "-acknowledge-direct-probes", "-acknowledge-original-baseline"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "preflight failed") {
		t.Fatalf("error = %v", err)
	}
	var report trial.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if report.Ready || report.Counts.Fail != 2 || report.ActiveClashInspected || report.AuthorizesLiveActivation {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunTrialAssessEmitsMachineReadableDataQualityFailure(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "observations")
	if err := observe.Pause(directory); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Observation.Enabled = true
	cfg.Observation.Directory = directory
	configPath := writeConfig(t, cfg)
	preflightPath := writeReadyPreflight(t, cfg)
	var stdout, stderr bytes.Buffer
	err := run([]string{"trial", "assess", "-config", configPath, "-preflight-report", preflightPath}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "assessment failed") {
		t.Fatalf("error=%v", err)
	}
	var assessment trial.ObservationAssessment
	if err := json.Unmarshal(stdout.Bytes(), &assessment); err != nil {
		t.Fatalf("decode assessment: %v\n%s", err, stdout.String())
	}
	if assessment.ReadyForDescriptiveAnalysis || assessment.Counts.Fail == 0 || assessment.PersistentStateChanged ||
		assessment.ActiveClashInspected || assessment.AuthorizesPolicyChange {
		t.Fatalf("assessment=%+v", assessment)
	}
}

func TestRunTrialAssessRejectsMissingPreflightPlan(t *testing.T) {
	cfg := config.Default()
	cfg.Observation.Enabled = true
	cfg.Observation.Directory = filepath.Join(t.TempDir(), "observations")
	configPath := writeConfig(t, cfg)
	var stdout, stderr bytes.Buffer
	err := run([]string{"trial", "assess", "-config", configPath}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "preflight report") || stdout.Len() != 0 {
		t.Fatalf("error=%v stdout=%q", err, stdout.String())
	}
}

func writeReadyPreflight(t *testing.T, cfg config.Config) string {
	t.Helper()
	now := time.Now().UTC()
	plan, err := trial.NewAssessmentPlan(cfg, "trial-0123456789abcdef0123456789abcdef", now, 168*time.Hour, trial.DefaultAssessmentThresholds())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "preflight.json")
	ids := []string{
		"assessment.plan", "config.valid", "privacy.direct_probes", "baseline.original_path",
		"observation.enabled", "privacy.cleartext_hostname", "observation.paused", "observation.existing_files",
		"learning.mode", "learning.persistence", "learning.backup", "lab.testlab", "lab.mihomo", "safety.active_clash",
	}
	report := trial.Report{ReportVersion: trial.PreflightReportVersion, GeneratedAt: now, Ready: true, AssessmentPlan: &plan}
	for _, id := range ids {
		report.Checks = append(report.Checks, trial.Check{ID: id, Status: trial.StatusPass})
		report.Counts.Pass++
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunObservationsReportIsIdentityFree(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "observations")
	configPath := writeObservationConfig(t, directory)
	recorder, err := observe.New(observe.Options{Directory: directory, Source: observe.SourceEngine,
		MaxFileBytes: 4096, MaxFiles: 2, Retention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	committed := true
	latency := int64(25)
	target := model.Target{NetworkProfileID: "private-profile", Hostname: "secret.example", Port: 443, Transport: model.TransportTCP}
	if err := recorder.Record(observe.Event{EventType: "decision", Target: &target, SelectedPath: model.PathDirect,
		ReasonCode: "direct_candidate_won", Committed: &committed, DecisionLatencyMS: &latency,
		Observation: &model.Observation{Path: model.PathDirect, Success: true, StageReached: model.StageTLS, Latency: 20 * time.Millisecond}}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"observations", "report", "-config", configPath, "-hours", "1"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("unpaused report error=%v", err)
	}
	if err := observe.Pause(directory); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"observations", "report", "-config", configPath, "-hours", "1"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var report observe.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.RecordingPaused || report.UnscopedEvents != 1 || report.TrialSessionsObserved != 0 || report.Adaptive.Ready != 1 || report.Adaptive.DecisionReadinessLatencyMS.P50 == nil || *report.Adaptive.DecisionReadinessLatencyMS.P50 != 25 {
		t.Fatalf("report=%+v", report)
	}
	if strings.Contains(stdout.String(), target.Hostname) || strings.Contains(stdout.String(), target.NetworkProfileID) {
		t.Fatalf("identity leaked: %s", stdout.String())
	}
	if err := run([]string{"observations", "report", "-config", configPath, "-hours", "1", "-since", time.Now().Format(time.RFC3339)}, &stdout, &stderr); err == nil {
		t.Fatal("conflicting report window accepted")
	}
}

func TestRunLearningStatusDoesNotCreateDisabledStore(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "learning.db")
	cfg := config.Default()
	cfg.Learning.Mode = learning.ModeShadow
	cfg.Learning.Persistence.Enabled = false
	cfg.Learning.Persistence.DatabasePath = databasePath
	configPath := writeConfig(t, cfg)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"learning", "status", "-config", configPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var status learningStoreStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.ConfiguredEnabled || status.DatabaseExists || status.KeyExists || status.Health != "absent" {
		t.Fatalf("status = %+v", status)
	}
	for _, path := range []string{databasePath, databasePath + ".key"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("status created %s: %v", path, err)
		}
	}
}

func TestRunLearningStatusAndBackupExistingStore(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "learning.db")
	evidenceStore, err := store.Open(context.Background(), store.Config{Path: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := evidenceStore.StartSession(context.Background(), "session", now); err != nil {
		t.Fatal(err)
	}
	target := model.Target{NetworkProfileID: "private-profile", Hostname: "secret.example", Port: 443, Transport: model.TransportTCP}
	winner := model.Observation{Path: model.PathProxy, Success: true, StageReached: model.StageTLS}
	failed := model.Observation{Path: model.PathDirect, StageReached: model.StageOutbound, FailureClass: "timeout"}
	if written, err := evidenceStore.AppendStrongEvidence(context.Background(), target, "session", winner, &failed, now); err != nil || !written {
		t.Fatalf("written=%v error=%v", written, err)
	}
	if err := evidenceStore.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Learning.Persistence.Enabled = true
	cfg.Learning.Persistence.DatabasePath = databasePath
	configPath := writeConfig(t, cfg)

	var statusOutput, stderr bytes.Buffer
	if err := run([]string{"learning", "status", "-config", configPath}, &statusOutput, &stderr); err != nil {
		t.Fatal(err)
	}
	var status learningStoreStatus
	if err := json.Unmarshal(statusOutput.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Health != "ok" || status.Store == nil || status.Store.EvidenceCount != 1 || status.Store.ProxyEvidence != 1 {
		t.Fatalf("status = %+v", status)
	}
	var evaluateOutput bytes.Buffer
	if err := run([]string{
		"learning", "evaluate", "-config", configPath,
		"-network-profile", target.NetworkProfileID, "-hostname", target.Hostname, "-port", "443",
	}, &evaluateOutput, &stderr); err != nil {
		t.Fatal(err)
	}
	var assessment learning.DurableAssessment
	if err := json.Unmarshal(evaluateOutput.Bytes(), &assessment); err != nil {
		t.Fatal(err)
	}
	if assessment.ReasonCode != learning.ReasonDurableProxyIncomplete || assessment.Evidence.ProxyWins != 1 || strings.Contains(evaluateOutput.String(), target.Hostname) {
		t.Fatalf("assessment = %+v output=%s", assessment, evaluateOutput.String())
	}
	var reportOutput bytes.Buffer
	if err := run([]string{"learning", "report", "-config", configPath}, &reportOutput, &stderr); err != nil {
		t.Fatal(err)
	}
	var report learningReportResult
	if err := json.Unmarshal(reportOutput.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Report.TargetsWithEvidence != 1 || report.Report.ProxyEvidence != 1 || report.Report.InsufficientTargets != 1 ||
		strings.Contains(reportOutput.String(), target.Hostname) || strings.Contains(reportOutput.String(), target.NetworkProfileID) {
		t.Fatalf("report = %+v output=%s", report, reportOutput.String())
	}

	destination := filepath.Join(t.TempDir(), "backup")
	var backupOutput bytes.Buffer
	if err := run([]string{"learning", "backup", "-config", configPath, "-destination", destination}, &backupOutput, &stderr); err != nil {
		t.Fatal(err)
	}
	var result learningBackupResult
	if err := json.Unmarshal(backupOutput.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Manifest.Status.EvidenceCount != 1 || result.Destination == "" {
		t.Fatalf("backup result = %+v", result)
	}
	manifest, err := os.ReadFile(filepath.Join(destination, store.BackupManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), target.Hostname) || strings.Contains(string(manifest), target.NetworkProfileID) {
		t.Fatalf("backup manifest contains target identity: %s", manifest)
	}
	var verifyOutput bytes.Buffer
	if err := run([]string{"learning", "verify-backup", "-source", destination}, &verifyOutput, &stderr); err != nil {
		t.Fatal(err)
	}
	var verified store.BackupManifest
	if err := json.Unmarshal(verifyOutput.Bytes(), &verified); err != nil || verified.Status.EvidenceCount != 1 {
		t.Fatalf("verified=%+v error=%v", verified, err)
	}
	restoredPath := filepath.Join(t.TempDir(), "restored.db")
	var restoreOutput bytes.Buffer
	if err := run([]string{"learning", "restore", "-source", destination, "-destination", restoredPath}, &restoreOutput, &stderr); err != nil {
		t.Fatal(err)
	}
	var restored store.RestoreResult
	if err := json.Unmarshal(restoreOutput.Bytes(), &restored); err != nil || restored.Status.EvidenceCount != 1 || restored.DatabasePath != restoredPath {
		t.Fatalf("restored=%+v error=%v", restored, err)
	}
}

func TestRunLearningClearPoliciesKeepsEvidenceAndRequiresRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "learning.db")
	evidenceStore, err := store.Open(context.Background(), store.Config{Path: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := evidenceStore.StartSession(context.Background(), "session", now); err != nil {
		t.Fatal(err)
	}
	target := model.Target{NetworkProfileID: "home", Hostname: "example.com", Port: 443, Transport: model.TransportTCP}
	winner := model.Observation{Path: model.PathProxy, Success: true, StageReached: model.StageTLS}
	failed := model.Observation{Path: model.PathDirect, StageReached: model.StageOutbound, FailureClass: "timeout"}
	if written, err := evidenceStore.AppendStrongEvidence(context.Background(), target, "session", winner, &failed, now); err != nil || !written {
		t.Fatalf("written=%v err=%v", written, err)
	}
	if change, err := evidenceStore.RememberDurablePath(context.Background(), target, model.PathProxy, now, 10); err != nil || !change.Applied {
		t.Fatalf("change=%+v err=%v", change, err)
	}
	if err := evidenceStore.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Learning.Persistence.Enabled = true
	cfg.Learning.Persistence.DatabasePath = databasePath
	configPath := writeConfig(t, cfg)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"learning", "clear-policies", "-config", configPath}, &stdout, &stderr); err == nil {
		t.Fatal("clear without confirmation succeeded")
	}
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"learning", "clear-policies", "-confirm-clear-policies", "-config", configPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var result clearDurablePoliciesResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ClearedPolicies != 1 || !result.EvidenceRetained || !result.RestartRequired || !strings.Contains(stderr.String(), "restart") {
		t.Fatalf("result=%+v stderr=%s", result, stderr.String())
	}
	readOnly, err := store.OpenReadOnly(context.Background(), store.Config{Path: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	status, err := readOnly.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.DurablePolicies != 0 || status.EvidenceCount != 1 {
		t.Fatalf("status=%+v", status)
	}
}

func TestRunLearningBackupRequiresExistingSourceAndNewDestination(t *testing.T) {
	cfg := config.Default()
	cfg.Learning.Persistence.DatabasePath = filepath.Join(t.TempDir(), "missing.db")
	configPath := writeConfig(t, cfg)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"learning", "backup", "-config", configPath}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "destination") {
		t.Fatalf("missing destination error = %v", err)
	}
	if err := run([]string{"learning", "backup", "-config", configPath, "-destination", filepath.Join(t.TempDir(), "backup")}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing source error = %v", err)
	}
}

func TestRunLearningVerifyAndRestoreRequireExplicitPaths(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"learning", "verify-backup"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "-source") {
		t.Fatalf("verify error = %v", err)
	}
	if err := run([]string{"learning", "restore", "-source", "/tmp/backup"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "-destination") {
		t.Fatalf("restore error = %v", err)
	}
}

func TestRunLearningEvaluateRequiresExactTarget(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"learning", "evaluate"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "network-profile") {
		t.Fatalf("evaluate error = %v", err)
	}
}

func TestRunPolicyLifecycleIsExplicitAndManagementOnly(t *testing.T) {
	cfg := config.Default()
	cfg.FixedPolicy.DatabasePath = filepath.Join(t.TempDir(), "fixed-policies.db")
	configPath := writeConfig(t, cfg)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"policy", "list", "-config", configPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var missing fixedpolicy.ListResult
	if err := json.Unmarshal(stdout.Bytes(), &missing); err != nil {
		t.Fatal(err)
	}
	if missing.DatabaseExists || len(missing.Rules) != 0 {
		t.Fatalf("missing=%+v", missing)
	}
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{
		"policy", "lock", "-config", configPath,
		"-network-profile", "office", "-hostname", "API.Example.COM.", "-port", "443", "-path", "proxy",
	}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "runtime activation is not implemented") {
		t.Fatalf("stderr=%q", stderr.String())
	}
	var locked fixedpolicy.LockResult
	if err := json.Unmarshal(stdout.Bytes(), &locked); err != nil {
		t.Fatal(err)
	}
	if locked.Rule.Hostname != "api.example.com" || locked.Rule.Path != model.PathProxy || locked.Rule.Source != fixedpolicy.SourceManual {
		t.Fatalf("locked=%+v", locked)
	}
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"policy", "list", "-config", configPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var listed fixedpolicy.ListResult
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if !listed.DatabaseExists || len(listed.Rules) != 1 || listed.Rules[0].RuleID != locked.Rule.RuleID {
		t.Fatalf("listed=%+v", listed)
	}
	stdout.Reset()
	if err := run([]string{"policy", "revoke", "-config", configPath, "-id", locked.Rule.RuleID}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var revoked fixedpolicy.Rule
	if err := json.Unmarshal(stdout.Bytes(), &revoked); err != nil || revoked.RevokedAt == nil {
		t.Fatalf("revoked=%+v err=%v", revoked, err)
	}
	stdout.Reset()
	if err := run([]string{"policy", "list", "-config", configPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil || len(listed.Rules) != 0 {
		t.Fatalf("active after revoke=%+v err=%v", listed, err)
	}
}

func TestRunPolicyRejectsImplicitOrUnsupportedScope(t *testing.T) {
	cfg := config.Default()
	cfg.FixedPolicy.DatabasePath = filepath.Join(t.TempDir(), "fixed-policies.db")
	configPath := writeConfig(t, cfg)
	var stdout, stderr bytes.Buffer
	for _, args := range [][]string{
		{"policy", "lock", "-config", configPath, "-hostname", "example.com", "-port", "443", "-path", "direct"},
		{"policy", "lock", "-config", configPath, "-network-profile", "profile", "-hostname", "example.com", "-port", "443", "-transport", "udp", "-path", "direct"},
		{"policy", "lock", "-config", configPath, "-network-profile", "profile", "-hostname", "example.com", "-port", "443", "-path", "original"},
	} {
		if err := run(args, &stdout, &stderr); err == nil {
			t.Fatalf("accepted invalid args=%q", args)
		}
	}
}

func writeObservationConfig(t *testing.T, directory string) string {
	t.Helper()
	cfg := config.Default()
	cfg.Observation.Directory = directory
	return writeConfig(t, cfg)
}

func writeConfig(t *testing.T, cfg config.Config) string {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readObservationFiles(t *testing.T, directory string) string {
	t.Helper()
	var combined strings.Builder
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".jsonl" {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			combined.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return combined.String()
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "smartroute 0.1.0-dev") || !strings.Contains(stdout.String(), "commit=none built=unknown") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunDoctorBaselineAndRejectsUnknownPhase(t *testing.T) {
	cfg := config.Default()
	addresses := make([]string, 0, 5)
	for range 5 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addresses = append(addresses, listener.Addr().String())
		_ = listener.Close()
	}
	cfg.ListenAddress = addresses[0]
	cfg.DirectEndpoint = addresses[1]
	cfg.ProxyEndpoint = addresses[2]
	cfg.GuardListenAddress = addresses[3]
	cfg.OriginalEndpoint = addresses[4]
	configPath := writeConfig(t, cfg)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"doctor", "-phase", "baseline", "-config", configPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Passed bool `json:"passed"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || !report.Passed {
		t.Fatalf("report=%+v error=%v output=%s", report, err, stdout.String())
	}
	if err := run([]string{"doctor", "-phase", "unknown", "-config", configPath}, &stdout, &stderr); err == nil {
		t.Fatal("doctor accepted unknown phase")
	}
}

func TestRunServeRejectsEmptyNetworkProfileBeforeListening(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"serve", "-network-profile", ""}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "network-profile") {
		t.Fatalf("run(serve) error = %v", err)
	}
}

func TestRunServeRequiresDirectProbeAcknowledgmentBeforeListening(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"serve", "-config", "../../configs/smartroute.example.json"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "acknowledge-direct-probes") {
		t.Fatalf("run(serve) error = %v", err)
	}
}

func TestPrivacyFirstDoesNotRequireDirectProbeAcknowledgment(t *testing.T) {
	if err := validateDirectProbeAcknowledgement("privacy-first", false); err != nil {
		t.Fatalf("privacy-first acknowledgment error = %v", err)
	}
}

func TestExplicitOptInRequiresDirectProbeAcknowledgment(t *testing.T) {
	if err := validateDirectProbeAcknowledgement("explicit-opt-in", false); err == nil {
		t.Fatal("explicit-opt-in acknowledgment error = nil")
	}
}

func TestRunGuardRejectsEmptyNetworkProfileBeforeListening(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"guard", "-network-profile", ""}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "network-profile") {
		t.Fatalf("run(guard) error = %v", err)
	}
}

func TestRunSuperviseRejectsEmptyNetworkProfileBeforeSpawning(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"supervise", "-network-profile", ""}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "network-profile") {
		t.Fatalf("run(supervise) error = %v", err)
	}
}

func TestSupervisedServicesKeepGuardAndEngineSeparate(t *testing.T) {
	trialSession := "trial-0123456789abcdef0123456789abcdef"
	services := supervisedServices("/smartroute", "/config.json", "profile", true, trialSession)
	if len(services) != 2 || services[0].Name != "adaptive_engine" || services[1].Name != "availability_guard" {
		t.Fatalf("services = %+v", services)
	}
	if !strings.Contains(strings.Join(services[0].Args, " "), "serve -config /config.json -network-profile profile -acknowledge-direct-probes") {
		t.Fatalf("engine args = %q", services[0].Args)
	}
	if strings.Contains(strings.Join(services[1].Args, " "), "acknowledge-direct-probes") || services[1].Args[0] != "guard" {
		t.Fatalf("guard args = %q", services[1].Args)
	}
	for _, service := range services {
		args := strings.Join(service.Args, " ")
		if !strings.Contains(args, "-trial-session "+trialSession) {
			t.Fatalf("service did not share trial session: %+v", service)
		}
	}
}

func TestResolveRuntimeTrialSession(t *testing.T) {
	cfg := config.Default()
	if value, err := resolveRuntimeTrialSession(cfg, ""); err != nil || value != "" {
		t.Fatalf("disabled value=%q err=%v", value, err)
	}
	if _, err := resolveRuntimeTrialSession(cfg, "trial-0123456789abcdef0123456789abcdef"); err == nil {
		t.Fatal("disabled recording accepted trial session")
	}
	cfg.Observation.Enabled = true
	generated, err := resolveRuntimeTrialSession(cfg, "")
	if err != nil || observe.ValidateTrialSessionID(generated) != nil {
		t.Fatalf("generated=%q err=%v", generated, err)
	}
	explicit := "trial-fedcba9876543210fedcba9876543210"
	if value, err := resolveRuntimeTrialSession(cfg, explicit); err != nil || value != explicit {
		t.Fatalf("explicit=%q err=%v", value, err)
	}
	if _, err := resolveRuntimeTrialSession(cfg, "home-wifi"); err == nil {
		t.Fatal("identity-bearing session accepted")
	}
	if _, err := openObservationRecorder(cfg, observe.SourceEngine, ""); err == nil {
		t.Fatal("runtime recorder accepted unscoped events")
	}
}

func TestValidateSupervisorDurations(t *testing.T) {
	if err := validateSupervisorDurations(time.Millisecond, 2*time.Millisecond, time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := validateSupervisorDurations(2*time.Millisecond, time.Millisecond, time.Second, time.Second); err == nil {
		t.Fatal("inverted restart backoff error = nil")
	}
	if err := validateSupervisorDurations(time.Millisecond, time.Second, time.Second, -time.Millisecond); err == nil {
		t.Fatal("negative shutdown grace error = nil")
	}
}
