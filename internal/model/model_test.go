package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestObservationJSONUsesReadableUnits(t *testing.T) {
	observation := Observation{
		Path: PathDirect, Success: false, StageReached: StageTLS,
		Latency: 250 * time.Millisecond, FailureClass: "tls_reset",
	}

	data, err := json.Marshal(observation)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	got := string(data)
	for _, expected := range []string{`"stage_reached":"tls"`, `"latency_ms":250`} {
		if !strings.Contains(got, expected) {
			t.Fatalf("json.Marshal() = %s, want %s", got, expected)
		}
	}
}
