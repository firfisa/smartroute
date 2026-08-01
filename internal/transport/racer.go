package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

const (
	ReasonDirectReadyBeforeHeadStart = "direct_ready_before_head_start"
	ReasonDirectWonRace              = "direct_won_race"
	ReasonProxyWonRace               = "proxy_won_race"
)

// RaceResult owns the selected connection. The caller must close it.
type RaceResult struct {
	Conn        net.Conn
	Observation model.Observation
	ReasonCode  string
}

// RaceError reports both terminal candidate observations when no path became
// ready. Candidate cancellation is not treated as route evidence.
type RaceError struct {
	Direct model.Observation
	Proxy  model.Observation
}

func (e *RaceError) Error() string {
	return fmt.Sprintf("both candidates failed: direct=%s proxy=%s", e.Direct.FailureClass, e.Proxy.FailureClass)
}

// Racer starts Direct first and starts Proxy after HeadStart, or immediately
// after an early Direct failure. It returns the first TCP-ready candidate and
// closes every losing connection.
type Racer struct {
	Direct    CandidateDialer
	Proxy     CandidateDialer
	HeadStart time.Duration
	Timeout   time.Duration
}

type candidateResult struct {
	conn        net.Conn
	observation model.Observation
	err         error
}

func (r Racer) Race(ctx context.Context, target model.Target) (RaceResult, error) {
	if r.Direct == nil || r.Proxy == nil {
		return RaceResult{}, errors.New("direct and proxy dialers are required")
	}
	if r.HeadStart < 0 {
		return RaceResult{}, errors.New("head start must not be negative")
	}
	if r.Timeout <= r.HeadStart {
		return RaceResult{}, errors.New("timeout must exceed head start")
	}

	raceCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	results := make(chan candidateResult, 2)
	started := 1
	received := 0
	startCandidate := func(dialer CandidateDialer) {
		go func() {
			conn, observation, err := dialer.Dial(raceCtx, target)
			results <- candidateResult{conn: conn, observation: observation, err: err}
		}()
	}
	startCandidate(r.Direct)

	timer := time.NewTimer(r.HeadStart)
	defer timer.Stop()
	proxyStarted := false
	observations := map[model.Path]model.Observation{}

	for {
		select {
		case <-raceCtx.Done():
			cancel()
			drainLosers(results, started-received)
			return RaceResult{}, fmt.Errorf("candidate race: %w", raceCtx.Err())
		case <-timer.C:
			if !proxyStarted {
				startCandidate(r.Proxy)
				proxyStarted = true
				started++
			}
		case result := <-results:
			received++
			observations[result.observation.Path] = result.observation
			if result.err == nil && result.conn != nil {
				reason := ReasonProxyWonRace
				if result.observation.Path == model.PathDirect {
					reason = ReasonDirectWonRace
					if !proxyStarted {
						reason = ReasonDirectReadyBeforeHeadStart
					}
				}
				cancel()
				drainLosers(results, started-received)
				return RaceResult{Conn: result.conn, Observation: result.observation, ReasonCode: reason}, nil
			}

			if result.observation.Path == model.PathDirect && !proxyStarted {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				startCandidate(r.Proxy)
				proxyStarted = true
				started++
			}
			if received == started && proxyStarted {
				cancel()
				return RaceResult{}, &RaceError{
					Direct: observationOrFailure(observations, model.PathDirect),
					Proxy:  observationOrFailure(observations, model.PathProxy),
				}
			}
		}
	}
}

func drainLosers(results <-chan candidateResult, count int) {
	if count <= 0 {
		return
	}
	go func() {
		for range count {
			result := <-results
			if result.conn != nil {
				_ = result.conn.Close()
			}
		}
	}()
}

func observationOrFailure(observations map[model.Path]model.Observation, path model.Path) model.Observation {
	if observation, ok := observations[path]; ok {
		return observation
	}
	return model.Observation{Path: path, StageReached: model.StageNone, FailureClass: "not_started"}
}
