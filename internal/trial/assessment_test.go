package trial

import (
	"math"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/observe"
)

func TestAssessObservationsAcceptsSingleCompleteWindowWithoutAuthorizingPolicy(t *testing.T) {
	report := validObservationReport()
	assessment, err := AssessObservations(report, DefaultAssessmentThresholds(), func() time.Time { return time.Unix(200, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if !assessment.ReadyForDescriptiveAnalysis || assessment.Counts.Fail != 0 || assessment.Counts.Warn != 1 {
		t.Fatalf("assessment=%+v", assessment)
	}
	if assessment.PersistentStateChanged || assessment.ActiveClashInspected || assessment.AuthorizesPolicyChange ||
		assessment.StaticBaselineVerified || assessment.ClientOutcomeAvailable {
		t.Fatalf("unsafe assessment claims=%+v", assessment)
	}
	if assessment.Metrics.CommittedSelections != 100 || assessment.Metrics.ConnectionScopeRatio != .99 ||
		assessment.Metrics.BaselineScopeRatio != 1 || assessment.Metrics.PairCompletenessRatio != .98 ||
		assessment.Metrics.CancellationRatio != 2.0/99.0 || assessment.Metrics.ChangedSelectionRatio != .25 ||
		assessment.Metrics.DirectInsteadOfProxy != 25 || assessment.Metrics.ChangedRelayOutcomes != 24 {
		t.Fatalf("metrics=%+v", assessment.Metrics)
	}
}

func TestAssessObservationsFailsClosedOnDataQualityGates(t *testing.T) {
	tests := []struct {
		name   string
		check  string
		mutate func(*observe.Report)
	}{
		{"version", "report.version", func(r *observe.Report) { r.ReportVersion-- }},
		{"recording", "recording.paused", func(r *observe.Report) { r.RecordingPaused = false }},
		{"session", "trial.session_scope", func(r *observe.Report) { r.TrialSessionsObserved = 2 }},
		{"missing expected session", "trial.session_scope", func(r *observe.Report) { r.ExpectedTrialSessionMatched = false }},
		{"unexpected session event", "trial.session_scope", func(r *observe.Report) { r.UnexpectedTrialSessionEvents = 1 }},
		{"sample", "sample.committed_selections", func(r *observe.Report) {
			r.ConnectionScope.ScopedCommittedDecisions, r.DeclaredBaseline.ScopedSelections = 10, 10
		}},
		{"connection scope", "scope.connection", func(r *observe.Report) {
			r.ConnectionScope.ScopedCommittedDecisions, r.ConnectionScope.UnscopedCommittedDecisions = 90, 10
		}},
		{"baseline scope", "scope.declared_baseline", func(r *observe.Report) {
			r.DeclaredBaseline.ScopedSelections, r.DeclaredBaseline.UnscopedSelections = 90, 10
		}},
		{"pairing", "scope.pair_completeness", func(r *observe.Report) {
			r.ConnectionScope.PairedRelayOutcomes, r.ConnectionScope.CommittedDecisionsWithoutOutcome = 80, 20
		}},
		{"cancellation", "relay.cancellation", func(r *observe.Report) { r.Adaptive.Relay.Canceled = 20 }},
		{"interpretation", "report.interpretation", func(r *observe.Report) { r.Interpretation.ConnectionIDsOmitted = false }},
		{"consistency", "report.internal_consistency", func(r *observe.Report) { r.DeclaredBaseline.ScopedSelections = 99 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validObservationReport()
			test.mutate(&report)
			assessment, err := AssessObservations(report, DefaultAssessmentThresholds(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if assessment.ReadyForDescriptiveAnalysis || checkStatusFromAssessment(assessment, test.check) != StatusFail {
				t.Fatalf("assessment=%+v", assessment)
			}
		})
	}
}

func TestAssessmentThresholdValidation(t *testing.T) {
	thresholds := DefaultAssessmentThresholds()
	thresholds.MinCommittedSelections = 0
	if _, err := AssessObservations(validObservationReport(), thresholds, nil); err == nil {
		t.Fatal("zero minimum accepted")
	}
	thresholds = DefaultAssessmentThresholds()
	thresholds.MinPairCompletenessRatio = 1.1
	if _, err := AssessObservations(validObservationReport(), thresholds, nil); err == nil {
		t.Fatal("ratio above one accepted")
	}
	thresholds = DefaultAssessmentThresholds()
	thresholds.MinBaselineScopeRatio = math.NaN()
	if _, err := AssessObservations(validObservationReport(), thresholds, nil); err == nil {
		t.Fatal("NaN ratio accepted")
	}
}

func validObservationReport() observe.Report {
	return observe.Report{
		ReportVersion: observe.ReportVersion, RecordingPaused: true, TrialSessionsObserved: 1,
		ExpectedTrialSessionMatched: true,
		ConnectionScope: observe.ConnectionScopeReport{
			ScopedCommittedDecisions: 99, UnscopedCommittedDecisions: 1, ScopedRelayOutcomes: 99, PairedRelayOutcomes: 98,
			UnmatchedRelayOutcomes: 1, CommittedDecisionsWithoutOutcome: 1,
		},
		DeclaredBaseline: observe.DeclaredBaselineReport{
			ScopedSelections: 100, ChangedSelectionRatio: .25, DirectInsteadOfProxy: 25,
			ChangedFromDeclared: 25, SameAsDeclared: 75, ScopedRelayOutcomes: 99, ChangedRelayOutcomes: 24,
		},
		Adaptive: observe.AdaptiveReport{Relay: observe.RelayReport{Outcomes: 99, Canceled: 2}},
		Interpretation: observe.ReportInterpretation{
			ReadinessNotApplicationSuccess: true, RelayRemoteBytesNotApplicationSuccess: true,
			RelayBytesPostCommitAdaptiveOnly: true, LatencyStartsAfterClientHello: true, NoVerifiedStaticBaseline: true,
			TargetIdentitiesOmitted: true, TrialSessionIDsOmitted: true, ConnectionIDsOmitted: true,
			BaselineIsDeclaredNotObserved: true, ChangedBytesNotCounterfactualSavings: true,
			DirectionEndsNotApplicationSuccess: true,
		},
	}
}

func checkStatusFromAssessment(assessment ObservationAssessment, id string) Status {
	for _, check := range assessment.Checks {
		if check.ID == id {
			return check.Status
		}
	}
	return ""
}
