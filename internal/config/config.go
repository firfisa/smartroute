package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/privacy"
)

const CurrentVersion = 1

type Config struct {
	Version                int            `json:"version"`
	ListenAddress          string         `json:"listen_address"`
	DirectEndpoint         string         `json:"direct_endpoint"`
	ProxyEndpoint          string         `json:"proxy_endpoint"`
	GuardListenAddress     string         `json:"guard_listen_address"`
	OriginalEndpoint       string         `json:"original_endpoint"`
	GuardAdaptiveTimeoutMS int            `json:"guard_adaptive_timeout_ms"`
	OriginalFallback       model.Path     `json:"original_fallback"`
	Decision               DecisionConfig `json:"decision"`
	Learning               LearningConfig `json:"learning"`
	Privacy                PrivacyConfig  `json:"privacy"`
}

type DecisionConfig struct {
	DirectHeadStartMS  int `json:"direct_head_start_ms"`
	MaxDirectPenaltyMS int `json:"max_direct_penalty_ms"`
	CandidateTimeoutMS int `json:"candidate_timeout_ms"`
}

type LearningConfig struct {
	ProxyPromotionWins  int `json:"proxy_promotion_wins"`
	DirectPromotionWins int `json:"direct_promotion_wins"`
	PolicyTTLHours      int `json:"policy_ttl_hours"`
}

type PrivacyConfig struct {
	Mode             string   `json:"mode"`
	NeverDirectProbe []string `json:"never_direct_probe"`
}

func Default() Config {
	return Config{
		Version:                CurrentVersion,
		ListenAddress:          "127.0.0.1:17890",
		DirectEndpoint:         "127.0.0.1:17891",
		ProxyEndpoint:          "127.0.0.1:17892",
		GuardListenAddress:     "127.0.0.1:17893",
		OriginalEndpoint:       "127.0.0.1:17894",
		GuardAdaptiveTimeoutMS: 250,
		OriginalFallback:       model.PathProxy,
		Decision: DecisionConfig{
			DirectHeadStartMS:  200,
			MaxDirectPenaltyMS: 150,
			CandidateTimeoutMS: 5000,
		},
		Learning: LearningConfig{
			ProxyPromotionWins:  3,
			DirectPromotionWins: 5,
			PolicyTTLHours:      72,
		},
		Privacy: PrivacyConfig{
			Mode:             "explicit-opt-in",
			NeverDirectProbe: []string{},
		},
	}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Config{}, fmt.Errorf("decode config fields: %w", err)
	}
	defaults := Default()
	if _, present := fields["guard_listen_address"]; !present {
		cfg.GuardListenAddress = defaults.GuardListenAddress
	}
	if _, present := fields["original_endpoint"]; !present {
		cfg.OriginalEndpoint = defaults.OriginalEndpoint
	}
	if _, present := fields["guard_adaptive_timeout_ms"]; !present {
		cfg.GuardAdaptiveTimeoutMS = defaults.GuardAdaptiveTimeoutMS
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var validationErrors []error

	if c.Version != CurrentVersion {
		validationErrors = append(validationErrors, fmt.Errorf("version must be %d", CurrentVersion))
	}
	for name, address := range map[string]string{
		"listen_address":       c.ListenAddress,
		"direct_endpoint":      c.DirectEndpoint,
		"proxy_endpoint":       c.ProxyEndpoint,
		"guard_listen_address": c.GuardListenAddress,
		"original_endpoint":    c.OriginalEndpoint,
	} {
		if err := validateLoopbackAddress(address); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: %w", name, err))
		}
	}
	seenAddresses := map[string]string{}
	for name, address := range map[string]string{
		"listen_address":       c.ListenAddress,
		"direct_endpoint":      c.DirectEndpoint,
		"proxy_endpoint":       c.ProxyEndpoint,
		"guard_listen_address": c.GuardListenAddress,
		"original_endpoint":    c.OriginalEndpoint,
	} {
		if previous, exists := seenAddresses[address]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s and %s must use distinct addresses", previous, name))
		}
		seenAddresses[address] = name
	}
	if c.OriginalFallback != model.PathDirect && c.OriginalFallback != model.PathProxy {
		validationErrors = append(validationErrors, errors.New("original_fallback must be direct or proxy"))
	}
	if c.Decision.DirectHeadStartMS < 10 || c.Decision.DirectHeadStartMS > 2000 {
		validationErrors = append(validationErrors, errors.New("direct_head_start_ms must be between 10 and 2000"))
	}
	if c.GuardAdaptiveTimeoutMS < 10 || c.GuardAdaptiveTimeoutMS > 2000 {
		validationErrors = append(validationErrors, errors.New("guard_adaptive_timeout_ms must be between 10 and 2000"))
	}
	if c.Decision.MaxDirectPenaltyMS < 0 || c.Decision.MaxDirectPenaltyMS > 5000 {
		validationErrors = append(validationErrors, errors.New("max_direct_penalty_ms must be between 0 and 5000"))
	}
	if c.Decision.CandidateTimeoutMS <= c.Decision.DirectHeadStartMS {
		validationErrors = append(validationErrors, errors.New("candidate_timeout_ms must exceed direct_head_start_ms"))
	}
	if c.Learning.ProxyPromotionWins < 2 || c.Learning.DirectPromotionWins < 2 {
		validationErrors = append(validationErrors, errors.New("promotion thresholds must be at least 2"))
	}
	if c.Learning.PolicyTTLHours < 1 {
		validationErrors = append(validationErrors, errors.New("policy_ttl_hours must be positive"))
	}
	if _, err := privacy.New(c.Privacy.Mode, c.Privacy.NeverDirectProbe); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("privacy: %w", err))
	}

	return errors.Join(validationErrors...)
}

func (c Config) DirectHeadStart() time.Duration {
	return time.Duration(c.Decision.DirectHeadStartMS) * time.Millisecond
}

func (c Config) CandidateTimeout() time.Duration {
	return time.Duration(c.Decision.CandidateTimeoutMS) * time.Millisecond
}

func (c Config) GuardAdaptiveTimeout() time.Duration {
	return time.Duration(c.GuardAdaptiveTimeoutMS) * time.Millisecond
}

func validateLoopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid host:port: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("port must be an integer between 1 and 65535")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("address must use a literal loopback IP")
	}
	return nil
}
