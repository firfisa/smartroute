// Package privacy owns the local Direct-probe admission policy. It never
// performs network I/O and returns stable reason codes for every decision.
package privacy

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/firfisa/smartroute/internal/model"
)

const (
	ModeExplicitOptIn = "explicit-opt-in"
	ModePrivacyFirst  = "privacy-first"

	ReasonDirectAllowed        = "direct_probe_allowed_explicit_opt_in"
	ReasonPrivacyFirst         = "privacy_first_proxy_only"
	ReasonNeverDirectExact     = "never_direct_probe_exact"
	ReasonNeverDirectSuffix    = "never_direct_probe_suffix"
	ReasonInvalidTarget        = "invalid_target_proxy_only"
	ReasonMissingRuntimePolicy = "missing_privacy_policy_proxy_only"
)

type Decision struct {
	AllowDirect bool
	ReasonCode  string
}

type Policy struct {
	mode     string
	exact    map[string]struct{}
	suffixes []string
}

// New validates and compiles a privacy policy. Plain entries are exact
// matches. Entries beginning with "." or "*." match an apex and every
// label-boundary subdomain beneath it.
func New(mode string, neverDirectProbe []string) (Policy, error) {
	if mode != ModeExplicitOptIn && mode != ModePrivacyFirst {
		return Policy{}, fmt.Errorf("unsupported privacy mode %q", mode)
	}
	policy := Policy{mode: mode, exact: make(map[string]struct{})}
	suffixSet := make(map[string]struct{})
	for index, raw := range neverDirectProbe {
		value := raw
		if strings.TrimSpace(value) != value || value == "" {
			return Policy{}, fmt.Errorf("never_direct_probe[%d]: entry must be non-empty and contain no surrounding whitespace", index)
		}
		suffix := false
		switch {
		case strings.HasPrefix(value, "*."):
			suffix = true
			value = strings.TrimPrefix(value, "*.")
		case strings.HasPrefix(value, "."):
			suffix = true
			value = strings.TrimPrefix(value, ".")
		}
		normalized, err := normalizeHost(value)
		if err != nil {
			return Policy{}, fmt.Errorf("never_direct_probe[%d]: %w", index, err)
		}
		if suffix {
			if net.ParseIP(normalized) != nil {
				return Policy{}, fmt.Errorf("never_direct_probe[%d]: IP addresses support exact matching only", index)
			}
			suffixSet[normalized] = struct{}{}
			continue
		}
		policy.exact[normalized] = struct{}{}
	}
	for suffix := range suffixSet {
		policy.suffixes = append(policy.suffixes, suffix)
	}
	sort.Slice(policy.suffixes, func(i, j int) bool {
		if len(policy.suffixes[i]) == len(policy.suffixes[j]) {
			return policy.suffixes[i] < policy.suffixes[j]
		}
		return len(policy.suffixes[i]) > len(policy.suffixes[j])
	})
	return policy, nil
}

func (p Policy) Evaluate(target model.Target) Decision {
	if p.mode != ModeExplicitOptIn && p.mode != ModePrivacyFirst {
		return Decision{ReasonCode: ReasonMissingRuntimePolicy}
	}
	host, err := normalizeHost(target.Hostname)
	if err != nil {
		return Decision{ReasonCode: ReasonInvalidTarget}
	}
	if p.mode == ModePrivacyFirst {
		return Decision{ReasonCode: ReasonPrivacyFirst}
	}
	if _, denied := p.exact[host]; denied {
		return Decision{ReasonCode: ReasonNeverDirectExact}
	}
	if net.ParseIP(host) == nil {
		for _, suffix := range p.suffixes {
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return Decision{ReasonCode: ReasonNeverDirectSuffix}
			}
		}
	}
	return Decision{AllowDirect: true, ReasonCode: ReasonDirectAllowed}
}

func normalizeHost(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return "", errors.New("host must be non-empty and contain no surrounding whitespace")
	}
	value = strings.ToLower(strings.TrimSuffix(value, "."))
	if value == "" || strings.HasSuffix(value, ".") {
		return "", errors.New("host has an invalid trailing root label")
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip.String(), nil
	}
	if len(value) > 253 {
		return "", errors.New("host exceeds 253 bytes")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", errors.New("host contains an empty or oversized label")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("host label must not begin or end with a hyphen")
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return "", errors.New("host labels must use ASCII letters, digits, or hyphens")
		}
	}
	return value, nil
}
