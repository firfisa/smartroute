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

type DurableReport struct {
	TargetsWithEvidence    int            `json:"targets_with_evidence"`
	EvidenceRows           int            `json:"evidence_rows"`
	DirectEvidence         int            `json:"direct_evidence"`
	ProxyEvidence          int            `json:"proxy_evidence"`
	InsufficientTargets    int            `json:"insufficient_targets"`
	ConflictingTargets     int            `json:"conflicting_targets"`
	DirectSuggestedTargets int            `json:"direct_suggested_targets"`
	ProxySuggestedTargets  int            `json:"proxy_suggested_targets"`
	ReasonCounts           map[string]int `json:"reason_counts"`
	DirectRequiredWins     int            `json:"direct_required_wins"`
	DirectRequiredSessions int            `json:"direct_required_sessions"`
	ProxyRequiredWins      int            `json:"proxy_required_wins"`
	ProxyRequiredSessions  int            `json:"proxy_required_sessions"`
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

func (e *DurableEvaluator) Report(summaries []DurableEvidenceSummary) (DurableReport, error) {
	report := DurableReport{
		ReasonCounts:       make(map[string]int),
		DirectRequiredWins: e.config.DirectWins, DirectRequiredSessions: e.config.DirectSessions,
		ProxyRequiredWins: e.config.ProxyWins, ProxyRequiredSessions: e.config.ProxySessions,
	}
	for _, summary := range summaries {
		if summary.DirectWins+summary.ProxyWins == 0 {
			return DurableReport{}, errors.New("durable target report summary must contain evidence")
		}
		assessment, err := e.Evaluate(summary)
		if err != nil {
			return DurableReport{}, err
		}
		report.TargetsWithEvidence++
		report.DirectEvidence += summary.DirectWins
		report.ProxyEvidence += summary.ProxyWins
		report.EvidenceRows += summary.DirectWins + summary.ProxyWins
		report.ReasonCounts[assessment.ReasonCode]++
		switch assessment.State {
		case DurableStateInsufficient:
			report.InsufficientTargets++
		case DurableStateConflicting:
			report.ConflictingTargets++
		case DurableStateDirectSuggested:
			report.DirectSuggestedTargets++
		case DurableStateProxySuggested:
			report.ProxySuggestedTargets++
		default:
			return DurableReport{}, errors.New("durable assessment has unknown state")
		}
	}
	return report, nil
}
