package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// Path is a concrete routing candidate or the user's original fallback.
type Path string

const (
	PathDirect   Path = "direct"
	PathProxy    Path = "proxy"
	PathOriginal Path = "original"
)

func (p Path) Valid() bool {
	switch p {
	case PathDirect, PathProxy, PathOriginal:
		return true
	default:
		return false
	}
}

// Transport identifies the transport-level semantics available to the engine.
type Transport string

const (
	TransportTCP Transport = "tcp"
	TransportUDP Transport = "udp"
)

func (t Transport) Valid() bool {
	return t == TransportTCP || t == TransportUDP
}

// Stage is the highest readiness layer reached by a path observation.
type Stage uint8

const (
	StageNone Stage = iota
	StageDNS
	StageOutbound
	StageTCP
	StageTLS
	StageApplication
)

var stageNames = map[Stage]string{
	StageNone:        "none",
	StageDNS:         "dns",
	StageOutbound:    "outbound",
	StageTCP:         "tcp",
	StageTLS:         "tls",
	StageApplication: "application",
}

func (s Stage) String() string {
	if name, ok := stageNames[s]; ok {
		return name
	}
	return fmt.Sprintf("stage(%d)", s)
}

func ParseStage(value string) (Stage, error) {
	for stage, name := range stageNames {
		if value == name {
			return stage, nil
		}
	}
	return StageNone, fmt.Errorf("unknown readiness stage %q", value)
}

// Target is the minimum identity used by Phase 0 decisions.
type Target struct {
	NetworkProfileID string    `json:"network_profile_id"`
	Hostname         string    `json:"hostname"`
	Port             uint16    `json:"port"`
	Transport        Transport `json:"transport"`
}

// Observation is structured evidence from exactly one candidate path.
type Observation struct {
	Path         Path
	Success      bool
	StageReached Stage
	Latency      time.Duration
	FailureClass string
}

func (o Observation) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Path         Path   `json:"path"`
		Success      bool   `json:"success"`
		StageReached string `json:"stage_reached"`
		LatencyMS    int64  `json:"latency_ms"`
		FailureClass string `json:"failure_class,omitempty"`
	}{
		Path:         o.Path,
		Success:      o.Success,
		StageReached: o.StageReached.String(),
		LatencyMS:    o.Latency.Milliseconds(),
		FailureClass: o.FailureClass,
	})
}

func (o Observation) Validate() error {
	if o.Path != PathDirect && o.Path != PathProxy {
		return fmt.Errorf("observation path must be direct or proxy, got %q", o.Path)
	}
	if _, ok := stageNames[o.StageReached]; !ok {
		return fmt.Errorf("invalid readiness stage %d", o.StageReached)
	}
	if o.Latency < 0 {
		return fmt.Errorf("latency must not be negative")
	}
	if o.Success && o.StageReached < StageTCP {
		return fmt.Errorf("successful observation must reach at least tcp stage")
	}
	if !o.Success && o.FailureClass == "" {
		return fmt.Errorf("failed observation requires failure_class")
	}
	return nil
}

type PolicyState string

const (
	StateUnknown         PolicyState = "unknown"
	StateDirectPreferred PolicyState = "direct_preferred"
	StateProxyPreferred  PolicyState = "proxy_preferred"
	StateUnstable        PolicyState = "unstable"
	StateDirectLocked    PolicyState = "direct_locked"
	StateProxyLocked     PolicyState = "proxy_locked"
)

type Confidence string

const (
	ConfidenceNone   Confidence = "none"
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

// EvidenceSummary is deliberately compact and safe to expose in a CLI or UI.
type EvidenceSummary struct {
	Direct Observation `json:"direct"`
	Proxy  Observation `json:"proxy"`
}

// Decision is the explainable result produced by the decision engine.
type Decision struct {
	SelectedPath Path            `json:"selected_path"`
	State        PolicyState     `json:"state"`
	Confidence   Confidence      `json:"confidence"`
	ReasonCode   string          `json:"reason_code"`
	Evidence     EvidenceSummary `json:"evidence"`
}
