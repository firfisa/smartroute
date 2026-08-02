package learning

import (
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

func testEngine(t *testing.T, now *time.Time) *Engine {
	t.Helper()
	engine, err := New(Config{
		Mode: ModeEphemeralAuto, DirectPromotionWins: 3, ProxyPromotionWins: 2, TTL: time.Hour, MaxEntries: 32,
		Clock: func() time.Time { return *now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestShadowModeLearnsWithoutChangingPreferredPath(t *testing.T) {
	now := time.Now()
	engine, err := New(Config{
		Mode: ModeShadow, DirectPromotionWins: 2, ProxyPromotionWins: 2,
		TTL: time.Hour, MaxEntries: 32, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	target := learningTarget("home", "example.com", 443)
	for i := 0; i < 2; i++ {
		_, _ = engine.Observe(target, ready(model.PathProxy), failed(model.PathDirect, model.StageOutbound, "tls_timeout"))
	}
	policy, ok := engine.Lookup(target)
	if !ok || policy.State != model.StateProxyPreferred {
		t.Fatalf("shadow policy = %+v, ok=%v", policy, ok)
	}
	if preferred := engine.PreferredPath(target); preferred != "" {
		t.Fatalf("shadow preferred path = %q", preferred)
	}
}

func learningTarget(profile, host string, port uint16) model.Target {
	return model.Target{NetworkProfileID: profile, Hostname: host, Port: port, Transport: model.TransportTCP}
}

func ready(path model.Path) model.Observation {
	return model.Observation{Path: path, Success: true, StageReached: model.StageTLS, Latency: 10 * time.Millisecond}
}

func failed(path model.Path, stage model.Stage, class string) *model.Observation {
	return &model.Observation{Path: path, StageReached: stage, FailureClass: class, Latency: 20 * time.Millisecond}
}

func TestProxyPromotionRequiresRepeatedStrongPairs(t *testing.T) {
	now := time.Now()
	engine := testEngine(t, &now)
	target := learningTarget("home", "Example.COM.", 443)
	for attempt := 1; attempt <= 2; attempt++ {
		update, err := engine.Observe(target, ready(model.PathProxy), failed(model.PathDirect, model.StageOutbound, "tls_timeout"))
		if err != nil {
			t.Fatal(err)
		}
		if !update.Applied {
			t.Fatalf("attempt %d update = %+v", attempt, update)
		}
	}
	policy, ok := engine.Lookup(learningTarget("home", "example.com", 443))
	if !ok || policy.State != model.StateProxyPreferred || policy.PreferredPath != model.PathProxy || policy.ProxyStrongWins != 2 {
		t.Fatalf("policy = %+v, ok=%v", policy, ok)
	}
}

func TestIncompleteCanceledAndWeakEvidenceNeverUpdate(t *testing.T) {
	now := time.Now()
	engine := testEngine(t, &now)
	target := learningTarget("home", "example.com", 443)
	for _, other := range []*model.Observation{
		nil,
		failed(model.PathDirect, model.StageOutbound, "canceled"),
		failed(model.PathDirect, model.StageNone, "endpoint_unavailable"),
	} {
		update, err := engine.Observe(target, ready(model.PathProxy), other)
		if err != nil {
			t.Fatal(err)
		}
		if update.Applied {
			t.Fatalf("weak update = %+v", update)
		}
	}
	if _, ok := engine.Lookup(target); ok {
		t.Fatal("weak evidence created a policy")
	}
}

func TestContradictionImmediatelyMakesPreferenceUnstable(t *testing.T) {
	now := time.Now()
	engine := testEngine(t, &now)
	target := learningTarget("home", "example.com", 443)
	for i := 0; i < 2; i++ {
		if _, err := engine.Observe(target, ready(model.PathProxy), failed(model.PathDirect, model.StageOutbound, "tls_timeout")); err != nil {
			t.Fatal(err)
		}
	}
	update, err := engine.Observe(target, ready(model.PathDirect), failed(model.PathProxy, model.StageOutbound, "tls_timeout"))
	if err != nil {
		t.Fatal(err)
	}
	if update.Policy.State != model.StateUnstable || update.Policy.PreferredPath != "" || update.ReasonCode != ReasonPreferenceContradicted {
		t.Fatalf("contradiction update = %+v", update)
	}
	if got := engine.PreferredPath(target); got != "" {
		t.Fatalf("preferred path after contradiction = %q", got)
	}
}

func TestSameStrongEvidenceRefreshesPreferenceTTL(t *testing.T) {
	now := time.Now()
	engine := testEngine(t, &now)
	target := learningTarget("home", "example.com", 443)
	for i := 0; i < 2; i++ {
		_, _ = engine.Observe(target, ready(model.PathProxy), failed(model.PathDirect, model.StageOutbound, "tls_timeout"))
	}
	now = now.Add(30 * time.Minute)
	update, err := engine.Observe(target, ready(model.PathProxy), failed(model.PathDirect, model.StageOutbound, "tls_timeout"))
	if err != nil {
		t.Fatal(err)
	}
	if update.ReasonCode != ReasonPreferenceRefreshed || !update.Policy.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("refresh update = %+v", update)
	}
}

func TestTTLExpiryAndScopeIsolation(t *testing.T) {
	now := time.Now()
	engine := testEngine(t, &now)
	target := learningTarget("home", "example.com", 443)
	for i := 0; i < 2; i++ {
		_, _ = engine.Observe(target, ready(model.PathProxy), failed(model.PathDirect, model.StageOutbound, "tls_timeout"))
	}
	for _, other := range []model.Target{
		learningTarget("office", "example.com", 443),
		learningTarget("home", "example.com", 8443),
		{NetworkProfileID: "home", Hostname: "example.com", Port: 443, Transport: model.TransportUDP},
	} {
		if got := engine.PreferredPath(other); got != "" {
			t.Fatalf("isolated target %+v preferred %q", other, got)
		}
	}
	now = now.Add(time.Hour)
	if got := engine.PreferredPath(target); got != "" {
		t.Fatalf("expired preferred path = %q", got)
	}
	if _, ok := engine.Lookup(target); ok {
		t.Fatal("expired policy remains present")
	}
}

func TestOppositeEvidenceMustBeValidPair(t *testing.T) {
	now := time.Now()
	engine := testEngine(t, &now)
	target := learningTarget("home", "example.com", 443)
	if _, err := engine.Observe(target, ready(model.PathProxy), failed(model.PathProxy, model.StageOutbound, "failed")); err == nil {
		t.Fatal("same-path pair error = nil")
	}
	if _, err := engine.Observe(target, model.Observation{Path: model.PathProxy, StageReached: model.StageTLS, FailureClass: "failed"}, failed(model.PathDirect, model.StageOutbound, "failed")); err == nil {
		t.Fatal("failed winner error = nil")
	}
}

func TestCapacityRejectsNewTargetButReclaimsExpiredEntry(t *testing.T) {
	now := time.Now()
	engine, err := New(Config{
		Mode: ModeEphemeralAuto, DirectPromotionWins: 2, ProxyPromotionWins: 2,
		TTL: time.Hour, MaxEntries: 1, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	first := learningTarget("home", "first.example", 443)
	second := learningTarget("home", "second.example", 443)
	if update, err := engine.Observe(first, ready(model.PathProxy), failed(model.PathDirect, model.StageOutbound, "failed")); err != nil || !update.Applied {
		t.Fatalf("first update=%+v err=%v", update, err)
	}
	if update, err := engine.Observe(second, ready(model.PathProxy), failed(model.PathDirect, model.StageOutbound, "failed")); err != nil || update.Applied || update.ReasonCode != ReasonCapacityReached {
		t.Fatalf("capacity update=%+v err=%v", update, err)
	}
	now = now.Add(time.Hour)
	if update, err := engine.Observe(second, ready(model.PathProxy), failed(model.PathDirect, model.StageOutbound, "failed")); err != nil || !update.Applied {
		t.Fatalf("reclaimed update=%+v err=%v", update, err)
	}
}

func TestNewRejectsUnsafeConfig(t *testing.T) {
	if _, err := New(Config{Mode: ModeEphemeralAuto, DirectPromotionWins: 1, ProxyPromotionWins: 2, TTL: time.Hour, MaxEntries: 1}); err == nil {
		t.Fatal("invalid threshold error = nil")
	}
	if _, err := New(Config{Mode: ModeEphemeralAuto, DirectPromotionWins: 2, ProxyPromotionWins: 2, MaxEntries: 1}); err == nil {
		t.Fatal("invalid TTL error = nil")
	}
	if _, err := New(Config{Mode: "auto", DirectPromotionWins: 2, ProxyPromotionWins: 2, TTL: time.Hour, MaxEntries: 1}); err == nil {
		t.Fatal("invalid mode error = nil")
	}
	if _, err := New(Config{Mode: ModeShadow, DirectPromotionWins: 2, ProxyPromotionWins: 2, TTL: time.Hour}); err == nil {
		t.Fatal("invalid capacity error = nil")
	}
}
