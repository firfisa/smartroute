package learning

import (
	"testing"

	"github.com/firfisa/smartroute/internal/model"
)

func testDurableEvaluator(t *testing.T) *DurableEvaluator {
	t.Helper()
	evaluator, err := NewDurableEvaluator(DurableEvaluatorConfig{
		DirectWins: 5, ProxyWins: 3, DirectSessions: 3, ProxySessions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return evaluator
}

func TestDurableEvaluatorOutcomeMatrix(t *testing.T) {
	evaluator := testDurableEvaluator(t)
	tests := []struct {
		name       string
		summary    DurableEvidenceSummary
		state      string
		reason     string
		suggestion model.Path
	}{
		{name: "none", state: DurableStateInsufficient, reason: ReasonDurableNoEvidence},
		{name: "proxy wins need sessions", summary: DurableEvidenceSummary{ProxyWins: 3, ProxySessions: 1}, state: DurableStateInsufficient, reason: ReasonDurableProxyIncomplete},
		{name: "proxy sessions need wins", summary: DurableEvidenceSummary{ProxyWins: 2, ProxySessions: 2}, state: DurableStateInsufficient, reason: ReasonDurableProxyIncomplete},
		{name: "proxy suggested", summary: DurableEvidenceSummary{ProxyWins: 3, ProxySessions: 2}, state: DurableStateProxySuggested, reason: ReasonDurableProxySuggested, suggestion: model.PathProxy},
		{name: "direct wins need sessions", summary: DurableEvidenceSummary{DirectWins: 5, DirectSessions: 2}, state: DurableStateInsufficient, reason: ReasonDurableDirectIncomplete},
		{name: "direct sessions need wins", summary: DurableEvidenceSummary{DirectWins: 4, DirectSessions: 3}, state: DurableStateInsufficient, reason: ReasonDurableDirectIncomplete},
		{name: "direct suggested", summary: DurableEvidenceSummary{DirectWins: 5, DirectSessions: 3}, state: DurableStateDirectSuggested, reason: ReasonDurableDirectSuggested, suggestion: model.PathDirect},
		{name: "any contradiction conflicts", summary: DurableEvidenceSummary{DirectWins: 20, DirectSessions: 8, ProxyWins: 1, ProxySessions: 1}, state: DurableStateConflicting, reason: ReasonDurableConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assessment, err := evaluator.Evaluate(test.summary)
			if err != nil {
				t.Fatal(err)
			}
			if assessment.State != test.state || assessment.ReasonCode != test.reason || assessment.SuggestedPath != test.suggestion || assessment.Evidence != test.summary {
				t.Fatalf("assessment = %+v", assessment)
			}
		})
	}
}

func TestDurableEvaluatorRejectsInvalidConfigurationAndEvidence(t *testing.T) {
	if _, err := NewDurableEvaluator(DurableEvaluatorConfig{DirectWins: 1, ProxyWins: 3, DirectSessions: 2, ProxySessions: 2}); err == nil {
		t.Fatal("low win threshold error = nil")
	}
	if _, err := NewDurableEvaluator(DurableEvaluatorConfig{DirectWins: 3, ProxyWins: 3, DirectSessions: 1, ProxySessions: 2}); err == nil {
		t.Fatal("low session threshold error = nil")
	}
	evaluator := testDurableEvaluator(t)
	for _, summary := range []DurableEvidenceSummary{
		{DirectWins: -1},
		{ProxyWins: 1, ProxySessions: 2},
		{DirectWins: 1, DirectSessions: 0},
	} {
		if _, err := evaluator.Evaluate(summary); err == nil {
			t.Fatalf("Evaluate(%+v) error = nil", summary)
		}
	}
}
