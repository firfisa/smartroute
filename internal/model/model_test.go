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

func TestObservationJSONRoundTrip(t *testing.T) {
	want := Observation{
		Path: PathDirect, StageReached: StageTCP, Latency: 125 * time.Millisecond,
		FailureClass: "direct_reset",
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Observation
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestObservationJSONRejectsInvalidStage(t *testing.T) {
	var observation Observation
	if err := json.Unmarshal([]byte(`{"path":"direct","success":false,"stage_reached":"bogus","latency_ms":1,"failure_class":"failed"}`), &observation); err == nil {
		t.Fatal("invalid stage unmarshal error = nil")
	}
}
