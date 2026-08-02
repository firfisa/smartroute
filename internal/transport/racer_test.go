package transport

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

type controlledDialer struct {
	path     model.Path
	delay    time.Duration
	failure  string
	attempts atomic.Int32
	canceled chan struct{}
}

func (d *controlledDialer) Dial(ctx context.Context, _ model.Target) (net.Conn, model.Observation, error) {
	d.attempts.Add(1)
	started := time.Now()
	select {
	case <-time.After(d.delay):
	case <-ctx.Done():
		if d.canceled != nil {
			select {
			case <-d.canceled:
			default:
				close(d.canceled)
			}
		}
		return nil, model.Observation{
			Path: d.path, StageReached: model.StageOutbound,
			Latency: time.Since(started), FailureClass: "canceled",
		}, ctx.Err()
	}
	if d.failure != "" {
		return nil, model.Observation{
			Path: d.path, StageReached: model.StageOutbound,
			Latency: time.Since(started), FailureClass: d.failure,
		}, errors.New(d.failure)
	}
	left, right := net.Pipe()
	_ = right.Close()
	return left, model.Observation{
		Path: d.path, Success: true, StageReached: model.StageTCP,
		Latency: time.Since(started),
	}, nil
}

func TestRacerDirectReadyDoesNotStartProxy(t *testing.T) {
	direct := &controlledDialer{path: model.PathDirect}
	proxy := &controlledDialer{path: model.PathProxy}
	racer := Racer{Direct: direct, Proxy: proxy, HeadStart: 50 * time.Millisecond, Timeout: time.Second}

	result, err := racer.Race(context.Background(), testTarget())
	if err != nil {
		t.Fatalf("Race() error = %v", err)
	}
	defer result.Conn.Close()
	if result.Observation.Path != model.PathDirect || result.ReasonCode != ReasonDirectCandidateBeforeHeadStart {
		t.Fatalf("Race() = %+v", result)
	}
	if proxy.attempts.Load() != 0 {
		t.Fatalf("proxy attempts = %d, want 0", proxy.attempts.Load())
	}
}

func TestRacerProxyWinsAndCancelsDirect(t *testing.T) {
	directCanceled := make(chan struct{})
	direct := &controlledDialer{path: model.PathDirect, delay: time.Second, canceled: directCanceled}
	proxy := &controlledDialer{path: model.PathProxy, delay: time.Millisecond}
	racer := Racer{Direct: direct, Proxy: proxy, HeadStart: 10 * time.Millisecond, Timeout: time.Second}

	result, err := racer.Race(context.Background(), testTarget())
	if err != nil {
		t.Fatalf("Race() error = %v", err)
	}
	defer result.Conn.Close()
	if result.Observation.Path != model.PathProxy || result.ReasonCode != ReasonProxyCandidateWon {
		t.Fatalf("Race() = %+v", result)
	}
	if result.OtherObservation != nil {
		t.Fatalf("canceled in-flight Direct was reported as completed evidence: %+v", result.OtherObservation)
	}
	select {
	case <-directCanceled:
	case <-time.After(time.Second):
		t.Fatal("Direct candidate was not canceled")
	}
}

func TestRacerProxySuccessPreservesPriorDirectFailure(t *testing.T) {
	direct := &controlledDialer{path: model.PathDirect, failure: "direct_reset"}
	proxy := &controlledDialer{path: model.PathProxy, delay: time.Millisecond}
	racer := Racer{Direct: direct, Proxy: proxy, HeadStart: 50 * time.Millisecond, Timeout: time.Second}

	result, err := racer.Race(context.Background(), testTarget())
	if err != nil {
		t.Fatal(err)
	}
	defer result.Conn.Close()
	if result.Observation.Path != model.PathProxy || result.OtherObservation == nil {
		t.Fatalf("Race() = %+v", result)
	}
	if result.OtherObservation.Path != model.PathDirect || result.OtherObservation.Success || result.OtherObservation.FailureClass != "direct_reset" {
		t.Fatalf("other observation = %+v", result.OtherObservation)
	}
}

func TestRacerDirectSuccessPreservesPriorProxyFailure(t *testing.T) {
	direct := &controlledDialer{path: model.PathDirect, delay: 30 * time.Millisecond}
	proxy := &controlledDialer{path: model.PathProxy, failure: "proxy_unavailable"}
	racer := Racer{Direct: direct, Proxy: proxy, HeadStart: 5 * time.Millisecond, Timeout: time.Second}

	result, err := racer.Race(context.Background(), testTarget())
	if err != nil {
		t.Fatal(err)
	}
	defer result.Conn.Close()
	if result.Observation.Path != model.PathDirect || result.OtherObservation == nil {
		t.Fatalf("Race() = %+v", result)
	}
	if result.OtherObservation.Path != model.PathProxy || result.OtherObservation.Success || result.OtherObservation.FailureClass != "proxy_unavailable" {
		t.Fatalf("other observation = %+v", result.OtherObservation)
	}
}

func TestRacerProxyPreferenceCanWinBeforeDirectStarts(t *testing.T) {
	direct := &controlledDialer{path: model.PathDirect}
	proxy := &controlledDialer{path: model.PathProxy}
	racer := Racer{
		Direct: direct, Proxy: proxy, HeadStart: 50 * time.Millisecond,
		Timeout: time.Second, PreferredPath: model.PathProxy,
	}
	result, err := racer.Race(context.Background(), testTarget())
	if err != nil {
		t.Fatal(err)
	}
	defer result.Conn.Close()
	if result.Observation.Path != model.PathProxy || result.ReasonCode != ReasonProxyCandidateBeforeHeadStart {
		t.Fatalf("Race() = %+v", result)
	}
	if direct.attempts.Load() != 0 {
		t.Fatalf("direct attempts = %d", direct.attempts.Load())
	}
}

func TestRacerProxyPreferenceFailureStartsDirectImmediately(t *testing.T) {
	direct := &controlledDialer{path: model.PathDirect, delay: time.Millisecond}
	proxy := &controlledDialer{path: model.PathProxy, failure: "proxy_unavailable"}
	racer := Racer{
		Direct: direct, Proxy: proxy, HeadStart: time.Second,
		Timeout: 2 * time.Second, PreferredPath: model.PathProxy,
	}
	started := time.Now()
	result, err := racer.Race(context.Background(), testTarget())
	if err != nil {
		t.Fatal(err)
	}
	defer result.Conn.Close()
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("fallback waited for head start: %s", elapsed)
	}
	if result.Observation.Path != model.PathDirect || result.OtherObservation == nil || result.OtherObservation.Path != model.PathProxy {
		t.Fatalf("Race() = %+v", result)
	}
}

func TestRacerRejectsInvalidPreferredPath(t *testing.T) {
	racer := Racer{
		Direct: &controlledDialer{path: model.PathDirect}, Proxy: &controlledDialer{path: model.PathProxy},
		HeadStart: time.Millisecond, Timeout: time.Second, PreferredPath: model.PathOriginal,
	}
	if _, err := racer.Race(context.Background(), testTarget()); err == nil {
		t.Fatal("invalid preferred path error = nil")
	}
}

func TestRacerBothFailuresPreserveClassification(t *testing.T) {
	direct := &controlledDialer{path: model.PathDirect, failure: "direct_reset"}
	proxy := &controlledDialer{path: model.PathProxy, failure: "proxy_unavailable"}
	racer := Racer{Direct: direct, Proxy: proxy, HeadStart: 10 * time.Millisecond, Timeout: time.Second}

	_, err := racer.Race(context.Background(), testTarget())
	var raceError *RaceError
	if !errors.As(err, &raceError) {
		t.Fatalf("Race() error = %v, want RaceError", err)
	}
	if raceError.Direct.FailureClass != "direct_reset" || raceError.Proxy.FailureClass != "proxy_unavailable" {
		t.Fatalf("RaceError = %+v", raceError)
	}
}

func testTarget() model.Target {
	return model.Target{Hostname: "echo.test", Port: 443, Transport: model.TransportTCP}
}
