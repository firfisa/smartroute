package health

import (
	"sync"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func testGate(t *testing.T) (*Gate, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Unix(100, 0).UTC()}
	gate, err := New(Config{FailureThreshold: 3, RecoveryThreshold: 2, FailureWindow: 30 * time.Second, FreezeDuration: time.Minute, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	return gate, clock
}

func target(host string) model.Target {
	return model.Target{NetworkProfileID: "profile", Hostname: host, Port: 443, Transport: model.TransportTCP}
}

func TestGlobalFailureUsesDistinctTargetsAndRecovers(t *testing.T) {
	gate, _ := testGate(t)
	for _, host := range []string{"a.example", "a.example", "b.example"} {
		transition, err := gate.ObserveBothPathsFailed(target(host))
		if err != nil {
			t.Fatal(err)
		}
		if transition.Snapshot.State != StateActive {
			t.Fatalf("premature freeze: %+v", transition)
		}
	}
	transition, _ := gate.ObserveBothPathsFailed(target("c.example"))
	if !transition.Changed || transition.ReasonCode != ReasonGlobalOutage || transition.Snapshot.State != StateFrozen {
		t.Fatalf("freeze=%+v", transition)
	}
	transition, _ = gate.ObservePathSucceeded(target("a.example"), model.PathDirect)
	if transition.Snapshot.State != StateFrozen {
		t.Fatal("one success recovered freeze")
	}
	transition, _ = gate.ObservePathSucceeded(target("d.example"), model.PathProxy)
	if transition.Snapshot.State != StateActive || transition.ReasonCode != ReasonRecovered {
		t.Fatalf("recovery=%+v", transition)
	}
}

func TestActiveSuccessResetsFailureWindow(t *testing.T) {
	gate, _ := testGate(t)
	_, _ = gate.ObserveBothPathsFailed(target("a.example"))
	_, _ = gate.ObserveBothPathsFailed(target("b.example"))
	_, _ = gate.ObservePathSucceeded(target("ok.example"), model.PathDirect)
	transition, _ := gate.ObserveBothPathsFailed(target("c.example"))
	if transition.Snapshot.GlobalFailureTargets != 1 || transition.Snapshot.State != StateActive {
		t.Fatalf("snapshot=%+v", transition.Snapshot)
	}
}

func TestProxyFreezeRequiresProxyRecovery(t *testing.T) {
	gate, _ := testGate(t)
	for _, host := range []string{"a.example", "b.example", "c.example"} {
		_, _ = gate.ObserveProxyPathFailed(target(host))
	}
	if gate.Snapshot().ReasonCode != ReasonProxyOutage {
		t.Fatalf("snapshot=%+v", gate.Snapshot())
	}
	_, _ = gate.ObservePathSucceeded(target("d.example"), model.PathDirect)
	_, _ = gate.ObservePathSucceeded(target("e.example"), model.PathDirect)
	if gate.Snapshot().State != StateFrozen {
		t.Fatal("direct successes recovered proxy outage")
	}
	_, _ = gate.ObservePathSucceeded(target("d.example"), model.PathProxy)
	transition, _ := gate.ObservePathSucceeded(target("e.example"), model.PathProxy)
	if transition.Snapshot.State != StateActive {
		t.Fatalf("proxy recovery=%+v", transition)
	}
}

func TestImmediateSignalsAndExpiry(t *testing.T) {
	gate, clock := testGate(t)
	transition := gate.ObserveNetworkProfileChanged()
	if transition.Snapshot.ReasonCode != ReasonNetworkChange {
		t.Fatalf("network=%+v", transition)
	}
	clock.Advance(time.Minute)
	transition = gate.Check()
	if transition.ReasonCode != ReasonFreezeExpired || transition.Snapshot.State != StateActive {
		t.Fatalf("expiry=%+v", transition)
	}
	transition = gate.ObserveCaptivePortal()
	if transition.Snapshot.ReasonCode != ReasonCaptivePortal {
		t.Fatalf("portal=%+v", transition)
	}
}

func TestValidationAndConcurrentUse(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("invalid config accepted")
	}
	gate, _ := testGate(t)
	if _, err := gate.ObservePathSucceeded(target("a.example"), model.PathOriginal); err == nil {
		t.Fatal("invalid path accepted")
	}
	var wait sync.WaitGroup
	for index := range 20 {
		wait.Add(1)
		go func() { defer wait.Done(); _, _ = gate.ObserveBothPathsFailed(target(string(rune('a' + index)))) }()
	}
	wait.Wait()
}
