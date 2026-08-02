package learning

import (
	"errors"

	"github.com/firfisa/smartroute/internal/model"
)

const (
	DurableStateInsufficient      = "insufficient"
	DurableStateConflicting       = "conflicting"
	DurableStateDirectSuggested   = "direct_suggested"
	DurableStateProxySuggested    = "proxy_suggested"
	ReasonDurableNoEvidence       = "durable_no_evidence"
	ReasonDurableConflict         = "durable_conflicting_evidence"
	ReasonDurableDirectIncomplete = "durable_direct_evidence_insufficient"
	ReasonDurableProxyIncomplete  = "durable_proxy_evidence_insufficient"
	ReasonDurableDirectSuggested  = "durable_direct_route_suggested"
	ReasonDurableProxySuggested   = "durable_proxy_route_suggested"
	EventTypeDurableAssessment    = "durable_learning_assessment"
)

type DurableEvidenceSummary struct {
	DirectWins     int `json:"direct_wins"`
	ProxyWins      int `json:"proxy_wins"`
	DirectSessions int `json:"direct_sessions"`
	ProxySessions  int `json:"proxy_sessions"`
}

type DurableEvaluatorConfig struct {
	DirectWins     int
	ProxyWins      int
	DirectSessions int
	ProxySessions  int
}

type DurableAssessment struct {
	State            string                 `json:"state"`
	SuggestedPath    model.Path             `json:"suggested_path,omitempty"`
	ReasonCode       string                 `json:"reason_code"`
	Evidence         DurableEvidenceSummary `json:"evidence"`
	RequiredWins     int                    `json:"required_wins,omitempty"`
	RequiredSessions int                    `json:"required_sessions,omitempty"`
}

type DurableAssessmentEvent struct {
	EventType  string            `json:"event_type"`
	Target     model.Target      `json:"target"`
	Assessment DurableAssessment `json:"assessment"`
}

type DurableEvaluator struct {
	config DurableEvaluatorConfig
}

func NewDurableEvaluator(config DurableEvaluatorConfig) (*DurableEvaluator, error) {
	if config.DirectWins < 2 || config.ProxyWins < 2 {
		return nil, errors.New("durable suggestion win thresholds must be at least 2")
	}
	if config.DirectSessions < 2 || config.ProxySessions < 2 {
		return nil, errors.New("durable suggestion session thresholds must be at least 2")
	}
	return &DurableEvaluator{config: config}, nil
}

func (e *DurableEvaluator) Evaluate(summary DurableEvidenceSummary) (DurableAssessment, error) {
	if summary.DirectWins < 0 || summary.ProxyWins < 0 || summary.DirectSessions < 0 || summary.ProxySessions < 0 ||
		summary.DirectSessions > summary.DirectWins || summary.ProxySessions > summary.ProxyWins ||
		(summary.DirectWins == 0) != (summary.DirectSessions == 0) ||
		(summary.ProxyWins == 0) != (summary.ProxySessions == 0) {
		return DurableAssessment{}, errors.New("durable evidence summary is invalid")
	}
	assessment := DurableAssessment{State: DurableStateInsufficient, Evidence: summary}
	if summary.DirectWins == 0 && summary.ProxyWins == 0 {
		assessment.ReasonCode = ReasonDurableNoEvidence
		return assessment, nil
	}
	if summary.DirectWins > 0 && summary.ProxyWins > 0 {
		assessment.State = DurableStateConflicting
		assessment.ReasonCode = ReasonDurableConflict
		return assessment, nil
	}
	if summary.DirectWins > 0 {
		assessment.RequiredWins = e.config.DirectWins
		assessment.RequiredSessions = e.config.DirectSessions
		if summary.DirectWins >= e.config.DirectWins && summary.DirectSessions >= e.config.DirectSessions {
			assessment.State = DurableStateDirectSuggested
			assessment.SuggestedPath = model.PathDirect
			assessment.ReasonCode = ReasonDurableDirectSuggested
		} else {
			assessment.ReasonCode = ReasonDurableDirectIncomplete
		}
		return assessment, nil
	}
	assessment.RequiredWins = e.config.ProxyWins
	assessment.RequiredSessions = e.config.ProxySessions
	if summary.ProxyWins >= e.config.ProxyWins && summary.ProxySessions >= e.config.ProxySessions {
		assessment.State = DurableStateProxySuggested
		assessment.SuggestedPath = model.PathProxy
		assessment.ReasonCode = ReasonDurableProxySuggested
	} else {
		assessment.ReasonCode = ReasonDurableProxyIncomplete
	}
	return assessment, nil
}
