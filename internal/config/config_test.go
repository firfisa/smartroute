package config

import (
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
