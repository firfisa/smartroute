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
	delete(learningFields, "persistence")
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
	if cfg.Learning.Persistence != defaults.Learning.Persistence {
		t.Fatalf("legacy learning persistence defaults = %+v", cfg.Learning.Persistence)
	}
}

func TestLoadAppliesMissingLearningPersistenceDefaults(t *testing.T) {
	encoded, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	var learningFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["learning"], &learningFields); err != nil {
		t.Fatal(err)
	}
	learningFields["persistence"] = json.RawMessage(`{"enabled":true}`)
	fields["learning"], err = json.Marshal(learningFields)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "partial.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	defaults := Default().Learning.Persistence
	if !cfg.Learning.Persistence.Enabled || cfg.Learning.Persistence.DatabasePath != defaults.DatabasePath ||
		cfg.Learning.Persistence.QueueSize != defaults.QueueSize || cfg.Learning.Persistence.RetentionHours != defaults.RetentionHours ||
		cfg.Learning.Persistence.ShutdownTimeoutMS != defaults.ShutdownTimeoutMS {
		t.Fatalf("partial persistence defaults = %+v", cfg.Learning.Persistence)
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

func TestValidateRejectsUnsafeLearningPersistence(t *testing.T) {
	cfg := Default()
	cfg.Learning.Persistence.DatabasePath = "."
	cfg.Learning.Persistence.QueueSize = 0
	cfg.Learning.Persistence.RetentionHours = 0
	cfg.Learning.Persistence.ShutdownTimeoutMS = 50
	cfg.Learning.Persistence.DirectSuggestionSessions = 1
	cfg.Learning.Persistence.ProxySuggestionSessions = 1001

	err := cfg.Validate()
	for _, field := range []string{"database_path", "queue_size", "retention_hours", "shutdown_timeout_ms", "direct_suggestion_sessions", "proxy_suggestion_sessions"} {
		if err == nil || !strings.Contains(err.Error(), field) {
			t.Fatalf("Validate() error = %v, want %s error", err, field)
		}
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

func TestValidateRejectsInvalidLearningHealthBounds(t *testing.T) {
	cfg := Default()
	cfg.Learning.Health.FailureThreshold = 1
	cfg.Learning.Health.FreezeDurationSeconds = 0
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "learning.health.failure_threshold") || !strings.Contains(err.Error(), "learning.health.freeze_duration_seconds") {
		t.Fatalf("Validate() error = %v, want health bounds", err)
	}
}

func TestLoadDefaultsPartialLearningHealth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	learningFields := fields["learning"].(map[string]any)
	learningFields["health"] = map[string]any{"failure_threshold": float64(4)}
	data, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Learning.Health.Enabled || loaded.Learning.Health.FailureThreshold != 4 || loaded.Learning.Health.RecoveryThreshold != 3 || loaded.Learning.Health.FreezeDurationSeconds != 300 {
		t.Fatalf("health defaults=%+v", loaded.Learning.Health)
	}
}
