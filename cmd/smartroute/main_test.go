package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/config"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/observe"
)

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

func writeObservationConfig(t *testing.T, directory string) string {
	t.Helper()
	cfg := config.Default()
	cfg.Observation.Directory = directory
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
