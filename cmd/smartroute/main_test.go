package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
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
