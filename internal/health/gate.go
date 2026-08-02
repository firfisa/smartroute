// Package health prevents broad network or proxy failures from poisoning
// adaptive-learning state. It never selects or changes the route of a live
// connection.
package health

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/model"
)

const (
	EventType = "learning_health"

	StateActive = "active"
	StateFrozen = "frozen"

	TriggerBothPathsFailed      = "both_paths_failed"
	TriggerProxyPathFailed      = "proxy_path_failed"
	TriggerPathSucceeded        = "path_succeeded"
	TriggerNetworkProfileChange = "network_profile_changed"
	TriggerCaptivePortal        = "captive_portal_suspected"
	TriggerStatusCheck          = "status_check"

	ReasonActive        = "learning_health_active"
	ReasonGlobalOutage  = "learning_frozen_global_outage"
	ReasonProxyOutage   = "learning_frozen_proxy_outage"
	ReasonNetworkChange = "learning_frozen_network_change"
	ReasonCaptivePortal = "learning_frozen_captive_portal"
	ReasonRecovered     = "learning_health_recovered"
	ReasonFreezeExpired = "learning_health_freeze_expired"
)

type Event struct {
	EventType       string        `json:"event_type"`
	Target          *model.Target `json:"target,omitempty"`
	Trigger         string        `json:"trigger"`
	ReasonCode      string        `json:"reason_code"`
	State           string        `json:"state"`
	FrozenUntil     time.Time     `json:"frozen_until,omitempty"`
	FailureTargets  int           `json:"failure_targets"`
	RecoveryTargets int           `json:"recovery_targets"`
}

func NewEvent(target *model.Target, transition Transition) Event {
	failures := transition.Snapshot.GlobalFailureTargets
	if transition.Snapshot.ReasonCode == ReasonProxyOutage {
		failures = transition.Snapshot.ProxyFailureTargets
	}
	return Event{EventType: EventType, Target: target, Trigger: transition.Trigger,
		ReasonCode: transition.ReasonCode, State: transition.Snapshot.State,
		FrozenUntil: transition.Snapshot.FrozenUntil, FailureTargets: failures,
		RecoveryTargets: transition.Snapshot.RecoveryTargets}
}

type Config struct {
	FailureThreshold  int
	RecoveryThreshold int
	FailureWindow     time.Duration
	FreezeDuration    time.Duration
	Clock             func() time.Time
}

type Snapshot struct {
	State                string    `json:"state"`
	ReasonCode           string    `json:"reason_code"`
	FrozenUntil          time.Time `json:"frozen_until,omitempty"`
	GlobalFailureTargets int       `json:"global_failure_targets"`
	ProxyFailureTargets  int       `json:"proxy_failure_targets"`
	RecoveryTargets      int       `json:"recovery_targets"`
}

type Transition struct {
	Changed    bool     `json:"changed"`
	Trigger    string   `json:"trigger"`
	ReasonCode string   `json:"reason_code"`
	Snapshot   Snapshot `json:"snapshot"`
}

type Gate struct {
	mu             sync.Mutex
	config         Config
	state          string
	reason         string
	frozenUntil    time.Time
	globalFailures targetSet
	proxyFailures  targetSet
	recoveries     targetSet
}

type targetSet struct {
	startedAt time.Time
	values    map[[sha256.Size]byte]struct{}
}

func New(config Config) (*Gate, error) {
	if config.FailureThreshold < 2 {
		return nil, errors.New("health failure threshold must be at least 2")
	}
	if config.RecoveryThreshold < 2 {
		return nil, errors.New("health recovery threshold must be at least 2")
	}
	if config.FailureWindow <= 0 || config.FreezeDuration <= 0 {
		return nil, errors.New("health failure window and freeze duration must be positive")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Gate{config: config, state: StateActive, reason: ReasonActive}, nil
}

func (g *Gate) Snapshot() Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.expireLocked(g.config.Clock().UTC())
	return g.snapshotLocked()
}

func (g *Gate) Check() Transition {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.transitionLocked(g.config.Clock().UTC(), TriggerStatusCheck, func(time.Time) {})
}

func (g *Gate) ObserveBothPathsFailed(target model.Target) (Transition, error) {
	key, err := targetHash(target)
	if err != nil {
		return Transition{}, err
	}
	return g.withTarget(TriggerBothPathsFailed, key, func(now time.Time) {
		if g.state == StateFrozen {
			g.recoveries.clear()
			g.extendLocked(now)
			return
		}
		g.globalFailures.add(now, g.config.FailureWindow, key)
		if g.globalFailures.len() >= g.config.FailureThreshold {
			g.freezeLocked(now, ReasonGlobalOutage)
		}
	}), nil
}

func (g *Gate) ObserveProxyPathFailed(target model.Target) (Transition, error) {
	key, err := targetHash(target)
	if err != nil {
		return Transition{}, err
	}
	return g.withTarget(TriggerProxyPathFailed, key, func(now time.Time) {
		if g.state == StateFrozen {
			if g.reason == ReasonProxyOutage {
				g.recoveries.clear()
				g.extendLocked(now)
			}
			return
		}
		g.proxyFailures.add(now, g.config.FailureWindow, key)
		if g.proxyFailures.len() >= g.config.FailureThreshold {
			g.freezeLocked(now, ReasonProxyOutage)
		}
	}), nil
}

func (g *Gate) ObservePathSucceeded(target model.Target, path model.Path) (Transition, error) {
	if path != model.PathDirect && path != model.PathProxy {
		return Transition{}, fmt.Errorf("health success path must be direct or proxy, got %q", path)
	}
	key, err := targetHash(target)
	if err != nil {
		return Transition{}, err
	}
	return g.withTarget(TriggerPathSucceeded, key, func(now time.Time) {
		if g.state == StateActive {
			g.globalFailures.clear()
			if path == model.PathProxy {
				g.proxyFailures.clear()
			}
			return
		}
		if g.reason == ReasonProxyOutage && path != model.PathProxy {
			return
		}
		g.recoveries.add(now, g.config.FreezeDuration, key)
		if g.recoveries.len() >= g.config.RecoveryThreshold {
			g.activateLocked(ReasonRecovered)
		}
	}), nil
}

func (g *Gate) ObserveNetworkProfileChanged() Transition {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.transitionLocked(g.config.Clock().UTC(), TriggerNetworkProfileChange, func(now time.Time) {
		g.freezeLocked(now, ReasonNetworkChange)
	})
}

func (g *Gate) ObserveCaptivePortal() Transition {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.transitionLocked(g.config.Clock().UTC(), TriggerCaptivePortal, func(now time.Time) {
		g.freezeLocked(now, ReasonCaptivePortal)
	})
}

func (g *Gate) withTarget(trigger string, _ [sha256.Size]byte, action func(time.Time)) Transition {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.transitionLocked(g.config.Clock().UTC(), trigger, action)
}

func (g *Gate) transitionLocked(now time.Time, trigger string, action func(time.Time)) Transition {
	beforeState, beforeReason := g.state, g.reason
	expired := g.expireLocked(now)
	action(now)
	reason := g.reason
	changed := beforeState != g.state || beforeReason != g.reason
	if expired && g.state == StateActive && g.reason == ReasonActive {
		reason, changed = ReasonFreezeExpired, true
	}
	return Transition{Changed: changed, Trigger: trigger, ReasonCode: reason, Snapshot: g.snapshotLocked()}
}

func (g *Gate) expireLocked(now time.Time) bool {
	if g.state == StateFrozen && !now.Before(g.frozenUntil) {
		g.activateLocked(ReasonActive)
		return true
	}
	return false
}

func (g *Gate) freezeLocked(now time.Time, reason string) {
	g.state, g.reason = StateFrozen, reason
	g.frozenUntil = now.Add(g.config.FreezeDuration)
	g.recoveries.clear()
}

func (g *Gate) extendLocked(now time.Time) {
	until := now.Add(g.config.FreezeDuration)
	if until.After(g.frozenUntil) {
		g.frozenUntil = until
	}
}

func (g *Gate) activateLocked(reason string) {
	g.state, g.reason = StateActive, reason
	g.frozenUntil = time.Time{}
	g.globalFailures.clear()
	g.proxyFailures.clear()
	g.recoveries.clear()
}

func (g *Gate) snapshotLocked() Snapshot {
	return Snapshot{State: g.state, ReasonCode: g.reason, FrozenUntil: g.frozenUntil,
		GlobalFailureTargets: g.globalFailures.len(), ProxyFailureTargets: g.proxyFailures.len(), RecoveryTargets: g.recoveries.len()}
}

func (s *targetSet) add(now time.Time, window time.Duration, key [sha256.Size]byte) {
	if s.startedAt.IsZero() || now.Sub(s.startedAt) > window {
		s.clear()
		s.startedAt = now
	}
	if s.values == nil {
		s.values = make(map[[sha256.Size]byte]struct{})
	}
	s.values[key] = struct{}{}
}
func (s *targetSet) len() int { return len(s.values) }
func (s *targetSet) clear()   { s.startedAt = time.Time{}; s.values = nil }

func targetHash(target model.Target) ([sha256.Size]byte, error) {
	canonical, err := learning.CanonicalTargetKey(target)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("health target: %w", err)
	}
	return sha256.Sum256(canonical), nil
}
