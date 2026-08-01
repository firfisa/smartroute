package decision

import (
	"fmt"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

const (
	ReasonDirectOnlySuccess = "direct_only_success"
	ReasonProxyOnlySuccess  = "proxy_only_success"
	ReasonBothSuccessDirect = "both_success_direct_within_budget"
	ReasonBothSuccessProxy  = "both_success_proxy_materially_faster"
	ReasonBothFailed        = "both_failed_use_original"
)

// PairEvaluator evaluates one paired observation. Persistence and multi-session
// promotion are intentionally separate from this Phase 0 component.
type PairEvaluator struct {
	OriginalFallback model.Path
	MaxDirectPenalty time.Duration
}

func (e PairEvaluator) Evaluate(direct, proxy model.Observation) (model.Decision, error) {
	if err := direct.Validate(); err != nil {
		return model.Decision{}, fmt.Errorf("direct observation: %w", err)
	}
	if err := proxy.Validate(); err != nil {
		return model.Decision{}, fmt.Errorf("proxy observation: %w", err)
	}
	if direct.Success && direct.StageReached < model.StageTCP {
		return model.Decision{}, fmt.Errorf("direct observation does not prove target TCP readiness")
	}
	if proxy.Success && proxy.StageReached < model.StageTCP {
		return model.Decision{}, fmt.Errorf("proxy observation does not prove target TCP readiness")
	}
	if direct.Path != model.PathDirect || proxy.Path != model.PathProxy {
		return model.Decision{}, fmt.Errorf("paired observations must be ordered direct then proxy")
	}
	if e.OriginalFallback != model.PathDirect && e.OriginalFallback != model.PathProxy {
		return model.Decision{}, fmt.Errorf("original fallback must be direct or proxy")
	}
	if e.MaxDirectPenalty < 0 {
		return model.Decision{}, fmt.Errorf("max direct penalty must not be negative")
	}

	decision := model.Decision{
		Evidence: model.EvidenceSummary{Direct: direct, Proxy: proxy},
	}

	switch {
	case direct.Success && !proxy.Success:
		decision.SelectedPath = model.PathDirect
		decision.State = model.StateDirectPreferred
		decision.Confidence = model.ConfidenceHigh
		decision.ReasonCode = ReasonDirectOnlySuccess
	case !direct.Success && proxy.Success:
		decision.SelectedPath = model.PathProxy
		decision.State = model.StateProxyPreferred
		decision.Confidence = model.ConfidenceHigh
		decision.ReasonCode = ReasonProxyOnlySuccess
	case direct.Success && proxy.Success:
		if direct.Latency <= proxy.Latency+e.MaxDirectPenalty {
			decision.SelectedPath = model.PathDirect
			decision.State = model.StateDirectPreferred
			decision.ReasonCode = ReasonBothSuccessDirect
		} else {
			decision.SelectedPath = model.PathProxy
			decision.State = model.StateProxyPreferred
			decision.ReasonCode = ReasonBothSuccessProxy
		}
		decision.Confidence = model.ConfidenceMedium
	default:
		decision.SelectedPath = e.OriginalFallback
		decision.State = model.StateUnknown
		decision.Confidence = model.ConfidenceNone
		decision.ReasonCode = ReasonBothFailed
	}

	return decision, nil
}
