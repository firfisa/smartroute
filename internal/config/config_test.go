package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Default().Validate() error = %v", err)
	}
}

func TestValidateRejectsNonLoopbackListener(t *testing.T) {
	cfg := Default()
	cfg.ListenAddress = "0.0.0.0:17890"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("Validate() error = %v, want loopback error", err)
	}
}

func TestValidateRejectsEndpointCollision(t *testing.T) {
	cfg := Default()
	cfg.ProxyEndpoint = cfg.DirectEndpoint

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "distinct") {
		t.Fatalf("Validate() error = %v, want distinct address error", err)
	}
}

func TestValidateRejectsGuardEndpointCollision(t *testing.T) {
	cfg := Default()
	cfg.GuardListenAddress = cfg.OriginalEndpoint

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "distinct") {
		t.Fatalf("Validate() error = %v, want distinct address error", err)
	}
}

func TestLoadAppliesGuardDefaultsToLegacyConfig(t *testing.T) {
	encoded, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "guard_listen_address")
	delete(fields, "original_endpoint")
	delete(fields, "guard_adaptive_timeout_ms")
	delete(fields, "observation")
	var learningFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["learning"], &learningFields); err != nil {
		t.Fatal(err)
	}
	delete(learningFields, "mode")
	delete(learningFields, "max_entries")
	fields["learning"], err = json.Marshal(learningFields)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	defaults := Default()
	if cfg.GuardListenAddress != defaults.GuardListenAddress || cfg.OriginalEndpoint != defaults.OriginalEndpoint || cfg.GuardAdaptiveTimeoutMS != defaults.GuardAdaptiveTimeoutMS {
		t.Fatalf("legacy guard defaults = %q %q %d", cfg.GuardListenAddress, cfg.OriginalEndpoint, cfg.GuardAdaptiveTimeoutMS)
	}
	if cfg.Observation != defaults.Observation {
		t.Fatalf("legacy observation defaults = %+v", cfg.Observation)
	}
	if cfg.Learning.Mode != defaults.Learning.Mode {
		t.Fatalf("legacy learning mode = %q", cfg.Learning.Mode)
	}
	if cfg.Learning.MaxEntries != defaults.Learning.MaxEntries {
		t.Fatalf("legacy learning capacity = %d", cfg.Learning.MaxEntries)
	}
}

func TestValidateRejectsUnknownLearningMode(t *testing.T) {
	cfg := Default()
	cfg.Learning.Mode = "automatic"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "learning.mode") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsLearningCapacityOutsideBounds(t *testing.T) {
	cfg := Default()
	cfg.Learning.MaxEntries = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "learning.max_entries") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsUnsafeObservationBounds(t *testing.T) {
	cfg := Default()
	cfg.Observation.Directory = "."
	cfg.Observation.MaxFilesPerSource = 0

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "observation.directory") || !strings.Contains(err.Error(), "max_files_per_source") {
		t.Fatalf("Validate() error = %v, want observation errors", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"version":1,"unknown":true}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load() error = %v, want unknown field error", err)
	}
}

func TestValidateRejectsInvalidPrivacyPattern(t *testing.T) {
	cfg := Default()
	cfg.Privacy.NeverDirectProbe = []string{"https://example.com/private"}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "never_direct_probe") {
		t.Fatalf("Validate() error = %v, want privacy pattern error", err)
	}
}
