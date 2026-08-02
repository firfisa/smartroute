// Package learning owns the explainable, process-local adaptive preference
// state machine. Durable cross-session policy is intentionally separate.
package learning

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

const (
	ModeShadow        = "shadow"
	ModeEphemeralAuto = "ephemeral-auto"

	ReasonIncompleteEvidence     = "incomplete_paired_evidence_no_update"
	ReasonWeakFailure            = "weak_path_failure_no_update"
	ReasonCapacityReached        = "learning_capacity_reached_no_update"
	ReasonDirectEvidence         = "strong_direct_evidence_recorded"
	ReasonProxyEvidence          = "strong_proxy_evidence_recorded"
	ReasonDirectPromoted         = "ephemeral_direct_preference_promoted"
	ReasonProxyPromoted          = "ephemeral_proxy_preference_promoted"
	ReasonPreferenceRefreshed    = "ephemeral_preference_refreshed"
	ReasonPreferenceContradicted = "ephemeral_preference_contradicted"
)

type Config struct {
	Mode                string
	DirectPromotionWins int
	ProxyPromotionWins  int
	TTL                 time.Duration
	MaxEntries          int
	Clock               func() time.Time
}

type Policy struct {
	State            model.PolicyState `json:"state"`
	PreferredPath    model.Path        `json:"preferred_path,omitempty"`
	DirectStrongWins int               `json:"direct_strong_wins"`
	ProxyStrongWins  int               `json:"proxy_strong_wins"`
	UpdatedAt        time.Time         `json:"updated_at"`
	ExpiresAt        time.Time         `json:"expires_at"`
}

type Update struct {
	Applied       bool              `json:"applied"`
	PreviousState model.PolicyState `json:"previous_state"`
	Policy        Policy            `json:"policy"`
	ReasonCode    string            `json:"reason_code"`
}

type targetKey struct {
	NetworkProfileID string
	Hostname         string
	Port             uint16
	Transport        model.Transport
}

type Engine struct {
	mu       sync.Mutex
	config   Config
	policies map[targetKey]Policy
}

func New(config Config) (*Engine, error) {
	if config.Mode != ModeShadow && config.Mode != ModeEphemeralAuto {
		return nil, fmt.Errorf("learning mode must be %q or %q", ModeShadow, ModeEphemeralAuto)
	}
	if config.DirectPromotionWins < 2 || config.ProxyPromotionWins < 2 {
		return nil, errors.New("learning promotion thresholds must be at least 2")
	}
	if config.TTL <= 0 {
		return nil, errors.New("learning TTL must be positive")
	}
	if config.MaxEntries < 1 {
		return nil, errors.New("learning max entries must be positive")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Engine{config: config, policies: make(map[targetKey]Policy)}, nil
}

// PreferredPath returns only a live automatic preference. Unknown, unstable,
// expired, and invalid targets deliberately return the empty path.
func (e *Engine) PreferredPath(target model.Target) model.Path {
	if e.config.Mode != ModeEphemeralAuto {
		return ""
	}
	key, err := keyForTarget(target)
	if err != nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	policy, ok := e.policies[key]
	if !ok {
		return ""
	}
	if !e.config.Clock().Before(policy.ExpiresAt) {
		delete(e.policies, key)
		return ""
	}
	if policy.State != model.StateDirectPreferred && policy.State != model.StateProxyPreferred {
		return ""
	}
	return policy.PreferredPath
}

func (e *Engine) Lookup(target model.Target) (Policy, bool) {
	key, err := keyForTarget(target)
	if err != nil {
		return Policy{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	policy, ok := e.policies[key]
	if !ok {
		return Policy{}, false
	}
	if !e.config.Clock().Before(policy.ExpiresAt) {
		delete(e.policies, key)
		return Policy{}, false
	}
	return policy, true
}

// Observe consumes only a winner plus an opposite path that completed before
// selection. It never converts cancellation, not-started, or weak local
// admission failure into routing evidence.
func (e *Engine) Observe(target model.Target, winner model.Observation, other *model.Observation) (Update, error) {
	key, err := keyForTarget(target)
	if err != nil {
		return Update{}, err
	}
	if err := winner.Validate(); err != nil {
		return Update{}, fmt.Errorf("winner: %w", err)
	}
	if !winner.Success || winner.StageReached < model.StageTCP {
		return Update{}, errors.New("winner must prove at least target TCP readiness")
	}
	if other == nil {
		return Update{ReasonCode: ReasonIncompleteEvidence}, nil
	}
	if err := other.Validate(); err != nil {
		return Update{}, fmt.Errorf("other observation: %w", err)
	}
	if other.Success || other.Path == winner.Path || !oppositePaths(winner.Path, other.Path) {
		return Update{}, errors.New("paired evidence requires a successful winner and failed opposite path")
	}
	if other.StageReached < model.StageOutbound || other.FailureClass == "canceled" || other.FailureClass == "not_started" {
		return Update{ReasonCode: ReasonWeakFailure}, nil
	}

	now := e.config.Clock().UTC()
	e.mu.Lock()
	defer e.mu.Unlock()
	policy, exists := e.policies[key]
	if !exists && len(e.policies) >= e.config.MaxEntries {
		e.pruneExpiredLocked(now)
		if len(e.policies) >= e.config.MaxEntries {
			return Update{ReasonCode: ReasonCapacityReached}, nil
		}
	}
	if policy.ExpiresAt.IsZero() || !now.Before(policy.ExpiresAt) {
		policy = Policy{State: model.StateUnknown}
	}
	previous := policy.State
	direction := winner.Path
	contradicted := (policy.State == model.StateDirectPreferred && direction == model.PathProxy) ||
		(policy.State == model.StateProxyPreferred && direction == model.PathDirect)
	if direction == model.PathDirect {
		policy.DirectStrongWins++
		policy.ProxyStrongWins = 0
	} else {
		policy.ProxyStrongWins++
		policy.DirectStrongWins = 0
	}

	reason := ReasonDirectEvidence
	threshold := e.config.DirectPromotionWins
	if direction == model.PathProxy {
		reason = ReasonProxyEvidence
		threshold = e.config.ProxyPromotionWins
	}
	if contradicted {
		policy.State = model.StateUnstable
		policy.PreferredPath = ""
		reason = ReasonPreferenceContradicted
	}
	count := policy.DirectStrongWins
	if direction == model.PathProxy {
		count = policy.ProxyStrongWins
	}
	samePreferred := (previous == model.StateDirectPreferred && direction == model.PathDirect) ||
		(previous == model.StateProxyPreferred && direction == model.PathProxy)
	if samePreferred {
		policy.State = previous
		policy.PreferredPath = direction
		reason = ReasonPreferenceRefreshed
	} else if count >= threshold {
		if direction == model.PathDirect {
			policy.State = model.StateDirectPreferred
			policy.PreferredPath = model.PathDirect
			reason = ReasonDirectPromoted
		} else {
			policy.State = model.StateProxyPreferred
			policy.PreferredPath = model.PathProxy
			reason = ReasonProxyPromoted
		}
	} else if policy.State == "" {
		policy.State = model.StateUnknown
	}
	policy.UpdatedAt = now
	policy.ExpiresAt = now.Add(e.config.TTL)
	e.policies[key] = policy
	return Update{Applied: true, PreviousState: previous, Policy: policy, ReasonCode: reason}, nil
}

func (e *Engine) pruneExpiredLocked(now time.Time) {
	for key, policy := range e.policies {
		if !now.Before(policy.ExpiresAt) {
			delete(e.policies, key)
		}
	}
}

func oppositePaths(left, right model.Path) bool {
	return (left == model.PathDirect && right == model.PathProxy) ||
		(left == model.PathProxy && right == model.PathDirect)
}

func keyForTarget(target model.Target) (targetKey, error) {
	if target.NetworkProfileID == "" || strings.TrimSpace(target.NetworkProfileID) != target.NetworkProfileID {
		return targetKey{}, errors.New("network profile ID must be non-empty without surrounding whitespace")
	}
	if target.Port == 0 {
		return targetKey{}, errors.New("target port must be non-zero")
	}
	if !target.Transport.Valid() {
		return targetKey{}, errors.New("target transport is invalid")
	}
	host := strings.ToLower(strings.TrimSuffix(target.Hostname, "."))
	if host == "" || strings.HasSuffix(host, ".") || strings.TrimSpace(host) != host {
		return targetKey{}, errors.New("target hostname is invalid")
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return targetKey{}, errors.New("target hostname is invalid")
			}
			for _, character := range label {
				if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
					continue
				}
				return targetKey{}, errors.New("target hostname is invalid")
			}
		}
	}
	return targetKey{
		NetworkProfileID: target.NetworkProfileID, Hostname: host,
		Port: target.Port, Transport: target.Transport,
	}, nil
}
