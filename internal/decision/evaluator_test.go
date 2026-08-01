package decision

import (
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

func success(path model.Path, latency time.Duration) model.Observation {
	return model.Observation{
		Path: path, Success: true, StageReached: model.StageTLS, Latency: latency,
	}
}

func failure(path model.Path, stage model.Stage, class string) model.Observation {
	return model.Observation{
		Path: path, Success: false, StageReached: stage, Latency: time.Second, FailureClass: class,
	}
}

func TestPairEvaluatorOutcomeMatrix(t *testing.T) {
	evaluator := PairEvaluator{OriginalFallback: model.PathProxy, MaxDirectPenalty: 150 * time.Millisecond}

	tests := []struct {
		name       string
		direct     model.Observation
		proxy      model.Observation
		wantPath   model.Path
		wantReason string
	}{
		{
			name: "direct only succeeds", direct: success(model.PathDirect, 30*time.Millisecond),
			proxy:    failure(model.PathProxy, model.StageOutbound, "proxy_unavailable"),
			wantPath: model.PathDirect, wantReason: ReasonDirectOnlySuccess,
		},
		{
			name: "proxy only succeeds", direct: failure(model.PathDirect, model.StageTCP, "tls_reset"),
			proxy:    success(model.PathProxy, 120*time.Millisecond),
			wantPath: model.PathProxy, wantReason: ReasonProxyOnlySuccess,
		},
		{
			name: "both succeed and direct is within budget", direct: success(model.PathDirect, 180*time.Millisecond),
			proxy:    success(model.PathProxy, 80*time.Millisecond),
			wantPath: model.PathDirect, wantReason: ReasonBothSuccessDirect,
		},
		{
			name: "both succeed and proxy is materially faster", direct: success(model.PathDirect, 400*time.Millisecond),
			proxy:    success(model.PathProxy, 80*time.Millisecond),
			wantPath: model.PathProxy, wantReason: ReasonBothSuccessProxy,
		},
		{
			name: "both fail uses original", direct: failure(model.PathDirect, model.StageTCP, "timeout"),
			proxy:    failure(model.PathProxy, model.StageOutbound, "proxy_unavailable"),
			wantPath: model.PathProxy, wantReason: ReasonBothFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evaluator.Evaluate(tt.direct, tt.proxy)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if got.SelectedPath != tt.wantPath || got.ReasonCode != tt.wantReason {
				t.Fatalf("Evaluate() = path %q reason %q, want path %q reason %q", got.SelectedPath, got.ReasonCode, tt.wantPath, tt.wantReason)
			}
		})
	}
}

func TestPairEvaluatorRejectsDirectFailureWithoutClass(t *testing.T) {
	evaluator := PairEvaluator{OriginalFallback: model.PathProxy}
	direct := model.Observation{Path: model.PathDirect, StageReached: model.StageTCP}
	proxy := success(model.PathProxy, 100*time.Millisecond)

	if _, err := evaluator.Evaluate(direct, proxy); err == nil {
		t.Fatal("Evaluate() error = nil, want validation error")
	}
}

func TestPairEvaluatorRejectsOutboundAdmissionAsRouteSuccess(t *testing.T) {
	evaluator := PairEvaluator{OriginalFallback: model.PathProxy}
	direct := model.Observation{
		Path: model.PathDirect, Success: true,
		StageReached: model.StageOutbound, Latency: 10 * time.Millisecond,
	}
	proxy := success(model.PathProxy, 100*time.Millisecond)

	if _, err := evaluator.Evaluate(direct, proxy); err == nil {
		t.Fatal("Evaluate() error = nil, want target-readiness error")
	}
}
