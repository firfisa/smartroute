package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/config"
	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/observe"
	"github.com/firfisa/smartroute/internal/store"
)

type recordingDurableWriter struct {
	requests []store.WriteRequest
	reason   string
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

func TestDurableLearningIsOptInAndDrains(t *testing.T) {
	cfg := config.Default()
	cfg.Learning.Persistence.DatabasePath = filepath.Join(t.TempDir(), "learning.db")
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

func TestRunLearningStatusDoesNotCreateDisabledStore(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "learning.db")
	cfg := config.Default()
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
	if !strings.Contains(stdout.String(), "smartroute") {
		t.Fatalf("stdout = %q", stdout.String())
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
	services := supervisedServices("/smartroute", "/config.json", "profile", true)
	if len(services) != 2 || services[0].Name != "adaptive_engine" || services[1].Name != "availability_guard" {
		t.Fatalf("services = %+v", services)
	}
	if !strings.Contains(strings.Join(services[0].Args, " "), "serve -config /config.json -network-profile profile -acknowledge-direct-probes") {
		t.Fatalf("engine args = %q", services[0].Args)
	}
	if strings.Contains(strings.Join(services[1].Args, " "), "acknowledge-direct-probes") || services[1].Args[0] != "guard" {
		t.Fatalf("guard args = %q", services[1].Args)
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
