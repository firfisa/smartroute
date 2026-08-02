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
	ReasonDirectCandidateBeforeHeadStart = "direct_candidate_before_head_start"
	ReasonProxyCandidateBeforeHeadStart  = "proxy_candidate_before_head_start"
	ReasonDirectCandidateWon             = "direct_candidate_won"
	ReasonProxyCandidateWon              = "proxy_candidate_won"
)

// RaceResult owns the selected connection. The caller must close it.
type RaceResult struct {
	Conn             net.Conn
	Observation      model.Observation
	OtherObservation *model.Observation
	ReasonCode       string
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

// Racer starts PreferredPath first (Direct by default) and starts the opposite
// path after HeadStart, or immediately after an early preferred-path failure.
// It returns the first candidate admitted by its
// dialer and closes every losing connection. Observation.StageReached records
// whether admission means only an outbound handshake or stronger readiness.
type Racer struct {
	Direct        CandidateDialer
	Proxy         CandidateDialer
	HeadStart     time.Duration
	Timeout       time.Duration
	PreferredPath model.Path
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
	preferred := r.PreferredPath
	if preferred == "" {
		preferred = model.PathDirect
	}
	if preferred != model.PathDirect && preferred != model.PathProxy {
		return RaceResult{}, fmt.Errorf("preferred path must be direct or proxy, got %q", preferred)
	}
	first, second := r.Direct, r.Proxy
	if preferred == model.PathProxy {
		first, second = r.Proxy, r.Direct
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
	startCandidate(first)

	timer := time.NewTimer(r.HeadStart)
	defer timer.Stop()
	secondStarted := false
	observations := map[model.Path]model.Observation{}

	for {
		select {
		case <-raceCtx.Done():
			cancel()
			drainLosers(results, started-received)
			return RaceResult{}, fmt.Errorf("candidate race: %w", raceCtx.Err())
		case <-timer.C:
			if !secondStarted {
				startCandidate(second)
				secondStarted = true
				started++
			}
		case result := <-results:
			received++
			observations[result.observation.Path] = result.observation
			if result.err == nil && result.conn != nil {
				reason := ReasonProxyCandidateWon
				if result.observation.Path == model.PathDirect {
					reason = ReasonDirectCandidateWon
					if !secondStarted {
						reason = ReasonDirectCandidateBeforeHeadStart
					}
				} else if !secondStarted {
					reason = ReasonProxyCandidateBeforeHeadStart
				}
				cancel()
				drainLosers(results, started-received)
				return RaceResult{
					Conn: result.conn, Observation: result.observation,
					OtherObservation: completedOtherObservation(observations, result.observation.Path),
					ReasonCode:       reason,
				}, nil
			}

			if result.observation.Path == preferred && !secondStarted {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				startCandidate(second)
				secondStarted = true
				started++
			}
			if received == started && secondStarted {
				cancel()
				return RaceResult{}, &RaceError{
					Direct: observationOrFailure(observations, model.PathDirect),
					Proxy:  observationOrFailure(observations, model.PathProxy),
				}
			}
		}
	}
}

func completedOtherObservation(observations map[model.Path]model.Observation, selected model.Path) *model.Observation {
	other := model.PathDirect
	if selected == model.PathDirect {
		other = model.PathProxy
	}
	observation, ok := observations[other]
	if !ok {
		return nil
	}
	copy := observation
	return &copy
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
