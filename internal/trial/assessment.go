package trial

import (
	"errors"
	"math"
	"time"

	"github.com/firfisa/smartroute/internal/observe"
)

type AssessmentThresholds struct {
	MinCommittedSelections   int     `json:"min_committed_selections"`
	MinConnectionScopeRatio  float64 `json:"min_connection_scope_ratio"`
	MinBaselineScopeRatio    float64 `json:"min_baseline_scope_ratio"`
	MinPairCompletenessRatio float64 `json:"min_pair_completeness_ratio"`
	MaxCancellationRatio     float64 `json:"max_cancellation_ratio"`
}

func DefaultAssessmentThresholds() AssessmentThresholds {
	return AssessmentThresholds{
		MinCommittedSelections: 20, MinConnectionScopeRatio: 0.99,
		MinBaselineScopeRatio: 0.99, MinPairCompletenessRatio: 0.95,
		MaxCancellationRatio: 0.10,
	}
}

func (t AssessmentThresholds) Validate() error {
	if t.MinCommittedSelections < 1 {
		return errors.New("minimum committed selections must be positive")
	}
	for _, value := range []float64{t.MinConnectionScopeRatio, t.MinBaselineScopeRatio, t.MinPairCompletenessRatio, t.MaxCancellationRatio} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return errors.New("assessment ratios must be between zero and one")
		}
	}
	return nil
}

type AssessmentMetrics struct {
	CommittedSelections   int     `json:"committed_selections"`
	ConnectionScopeRatio  float64 `json:"connection_scope_ratio"`
	BaselineScopeRatio    float64 `json:"baseline_scope_ratio"`
	PairCompletenessRatio float64 `json:"pair_completeness_ratio"`
	CancellationRatio     float64 `json:"cancellation_ratio"`
	ChangedSelectionRatio float64 `json:"changed_selection_ratio"`
	DirectInsteadOfProxy  int     `json:"direct_instead_of_proxy"`
	ProxyInsteadOfDirect  int     `json:"proxy_instead_of_direct"`
	ChangedRelayOutcomes  int     `json:"changed_relay_outcomes"`
}

type ObservationAssessment struct {
	GeneratedAt                 time.Time            `json:"generated_at"`
	ReadyForDescriptiveAnalysis bool                 `json:"ready_for_descriptive_analysis"`
	Checks                      []Check              `json:"checks"`
	Counts                      Counts               `json:"counts"`
	Thresholds                  AssessmentThresholds `json:"thresholds"`
	Metrics                     AssessmentMetrics    `json:"metrics"`
	PersistentStateChanged      bool                 `json:"persistent_state_changed"`
	ActiveClashInspected        bool                 `json:"active_clash_inspected"`
	AuthorizesPolicyChange      bool                 `json:"authorizes_policy_change"`
	StaticBaselineVerified      bool                 `json:"static_baseline_verified"`
	ClientOutcomeAvailable      bool                 `json:"client_outcome_available"`
}

func AssessObservations(report observe.Report, thresholds AssessmentThresholds, clock func() time.Time) (ObservationAssessment, error) {
	if err := thresholds.Validate(); err != nil {
		return ObservationAssessment{}, err
	}
	if clock == nil {
		clock = time.Now
	}
	assessment := ObservationAssessment{GeneratedAt: clock().UTC(), Thresholds: thresholds}
	add := func(id string, status Status, summary string) {
		assessment.Checks = append(assessment.Checks, Check{ID: id, Status: status, Summary: summary})
		switch status {
		case StatusPass:
			assessment.Counts.Pass++
		case StatusWarn:
			assessment.Counts.Warn++
		case StatusFail:
			assessment.Counts.Fail++
		}
	}

	if report.ReportVersion != observe.ReportVersion {
		add("report.version", StatusFail, "observation report version is not current")
	} else {
		add("report.version", StatusPass, "observation report version is current")
	}
	if report.RecordingPaused {
		add("recording.paused", StatusPass, "recording was paused for a stable analysis window")
	} else {
		add("recording.paused", StatusFail, "recording must be paused before assessment")
	}
	if report.TrialSessionsObserved == 1 && report.UnscopedEvents == 0 && report.ExpectedTrialSessionMatched && report.UnexpectedTrialSessionEvents == 0 {
		add("trial.session_scope", StatusPass, "analysis window contains only the preflight-planned trial session")
	} else {
		add("trial.session_scope", StatusFail, "analysis window must contain only the preflight-planned trial session and no unscoped events")
	}

	connectionTotal := report.ConnectionScope.ScopedCommittedDecisions + report.ConnectionScope.UnscopedCommittedDecisions
	baselineTotal := report.DeclaredBaseline.ScopedSelections + report.DeclaredBaseline.UnscopedSelections
	assessment.Metrics.CommittedSelections = connectionTotal
	consistent := report.UnexpectedTrialSessionEvents >= 0 && report.ConnectionScope.ScopedCommittedDecisions >= 0 && report.ConnectionScope.UnscopedCommittedDecisions >= 0 &&
		report.ConnectionScope.ScopedRelayOutcomes >= 0 && report.ConnectionScope.PairedRelayOutcomes >= 0 &&
		report.ConnectionScope.UnmatchedRelayOutcomes >= 0 && report.ConnectionScope.CommittedDecisionsWithoutOutcome >= 0 &&
		report.DeclaredBaseline.ScopedSelections >= 0 && report.DeclaredBaseline.UnscopedSelections >= 0 &&
		report.DeclaredBaseline.SameAsDeclared >= 0 && report.DeclaredBaseline.ChangedFromDeclared >= 0 &&
		report.DeclaredBaseline.DirectInsteadOfProxy >= 0 && report.DeclaredBaseline.ProxyInsteadOfDirect >= 0 &&
		report.DeclaredBaseline.ScopedRelayOutcomes >= 0 && report.DeclaredBaseline.UnscopedRelayOutcomes >= 0 &&
		report.DeclaredBaseline.ChangedRelayOutcomes >= 0 && report.Adaptive.Relay.Outcomes >= 0 && report.Adaptive.Relay.Canceled >= 0 &&
		connectionTotal == baselineTotal && report.DeclaredBaseline.ScopedSelections == report.DeclaredBaseline.SameAsDeclared+report.DeclaredBaseline.ChangedFromDeclared &&
		report.DeclaredBaseline.ChangedFromDeclared == report.DeclaredBaseline.DirectInsteadOfProxy+report.DeclaredBaseline.ProxyInsteadOfDirect &&
		report.DeclaredBaseline.ScopedRelayOutcomes+report.DeclaredBaseline.UnscopedRelayOutcomes == report.Adaptive.Relay.Outcomes &&
		report.DeclaredBaseline.ChangedRelayOutcomes <= report.DeclaredBaseline.ScopedRelayOutcomes && report.Adaptive.Relay.Canceled <= report.Adaptive.Relay.Outcomes &&
		math.Abs(report.DeclaredBaseline.ChangedSelectionRatio-ratio(report.DeclaredBaseline.ChangedFromDeclared, report.DeclaredBaseline.ScopedSelections)) <= 1e-12 &&
		report.ConnectionScope.ScopedCommittedDecisions == report.ConnectionScope.PairedRelayOutcomes+report.ConnectionScope.CommittedDecisionsWithoutOutcome &&
		report.ConnectionScope.ScopedRelayOutcomes == report.ConnectionScope.PairedRelayOutcomes+report.ConnectionScope.UnmatchedRelayOutcomes
	if consistent {
		add("report.internal_consistency", StatusPass, "committed selection and pair denominators are internally consistent")
	} else {
		add("report.internal_consistency", StatusFail, "committed selection or pair denominators are inconsistent")
	}
	if connectionTotal >= thresholds.MinCommittedSelections {
		add("sample.committed_selections", StatusPass, "committed selection sample reaches the configured minimum")
	} else {
		add("sample.committed_selections", StatusFail, "committed selection sample is below the configured minimum")
	}

	assessment.Metrics.ConnectionScopeRatio = ratio(report.ConnectionScope.ScopedCommittedDecisions, connectionTotal)
	if connectionTotal > 0 && assessment.Metrics.ConnectionScopeRatio >= thresholds.MinConnectionScopeRatio {
		add("scope.connection", StatusPass, "committed connection scope coverage reaches the configured minimum")
	} else {
		add("scope.connection", StatusFail, "committed connection scope coverage is below the configured minimum")
	}
	assessment.Metrics.BaselineScopeRatio = ratio(report.DeclaredBaseline.ScopedSelections, baselineTotal)
	if baselineTotal > 0 && assessment.Metrics.BaselineScopeRatio >= thresholds.MinBaselineScopeRatio {
		add("scope.declared_baseline", StatusPass, "declared baseline coverage reaches the configured minimum")
	} else {
		add("scope.declared_baseline", StatusFail, "declared baseline coverage is below the configured minimum")
	}

	pairDenominator := report.ConnectionScope.PairedRelayOutcomes + report.ConnectionScope.UnmatchedRelayOutcomes + report.ConnectionScope.CommittedDecisionsWithoutOutcome
	assessment.Metrics.PairCompletenessRatio = ratio(report.ConnectionScope.PairedRelayOutcomes, pairDenominator)
	if pairDenominator > 0 && assessment.Metrics.PairCompletenessRatio >= thresholds.MinPairCompletenessRatio {
		add("scope.pair_completeness", StatusPass, "terminal-to-relay pair completeness reaches the configured minimum")
	} else {
		add("scope.pair_completeness", StatusFail, "terminal-to-relay pair completeness is below the configured minimum")
	}

	assessment.Metrics.CancellationRatio = ratio(report.Adaptive.Relay.Canceled, report.Adaptive.Relay.Outcomes)
	if report.Adaptive.Relay.Outcomes > 0 && assessment.Metrics.CancellationRatio <= thresholds.MaxCancellationRatio {
		add("relay.cancellation", StatusPass, "relay cancellation ratio is within the configured limit")
	} else {
		add("relay.cancellation", StatusFail, "relay cancellation ratio exceeds the configured limit or has no outcomes")
	}

	i := report.Interpretation
	limitationsPresent := i.ReadinessNotApplicationSuccess && i.RelayRemoteBytesNotApplicationSuccess && i.RelayBytesPostCommitAdaptiveOnly &&
		i.LatencyStartsAfterClientHello && i.NoVerifiedStaticBaseline && i.TargetIdentitiesOmitted && i.TrialSessionIDsOmitted && i.ConnectionIDsOmitted &&
		i.BaselineIsDeclaredNotObserved && i.ChangedBytesNotCounterfactualSavings && i.DirectionEndsNotApplicationSuccess
	if limitationsPresent {
		add("report.interpretation", StatusPass, "required privacy and interpretation limitations are present")
	} else {
		add("report.interpretation", StatusFail, "required privacy or interpretation limitations are missing")
	}
	add("evidence.product_claim", StatusWarn, "data quality readiness does not prove a verified static baseline, client-visible success, or product improvement")

	assessment.Metrics.ChangedSelectionRatio = report.DeclaredBaseline.ChangedSelectionRatio
	assessment.Metrics.DirectInsteadOfProxy = report.DeclaredBaseline.DirectInsteadOfProxy
	assessment.Metrics.ProxyInsteadOfDirect = report.DeclaredBaseline.ProxyInsteadOfDirect
	assessment.Metrics.ChangedRelayOutcomes = report.DeclaredBaseline.ChangedRelayOutcomes
	assessment.ReadyForDescriptiveAnalysis = assessment.Counts.Fail == 0
	return assessment, nil
}

func ratio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
