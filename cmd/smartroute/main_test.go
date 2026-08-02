package main

import (
	"bytes"
	"strings"
	"testing"

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
