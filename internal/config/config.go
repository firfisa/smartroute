package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

const CurrentVersion = 1

type Config struct {
	Version          int            `json:"version"`
	ListenAddress    string         `json:"listen_address"`
	DirectEndpoint   string         `json:"direct_endpoint"`
	ProxyEndpoint    string         `json:"proxy_endpoint"`
	OriginalFallback model.Path     `json:"original_fallback"`
	Decision         DecisionConfig `json:"decision"`
	Learning         LearningConfig `json:"learning"`
	Privacy          PrivacyConfig  `json:"privacy"`
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
		Version:          CurrentVersion,
		ListenAddress:    "127.0.0.1:17890",
		DirectEndpoint:   "127.0.0.1:17891",
		ProxyEndpoint:    "127.0.0.1:17892",
		OriginalFallback: model.PathProxy,
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
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
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
		"listen_address":  c.ListenAddress,
		"direct_endpoint": c.DirectEndpoint,
		"proxy_endpoint":  c.ProxyEndpoint,
	} {
		if err := validateLoopbackAddress(address); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: %w", name, err))
		}
	}
	if c.ListenAddress == c.DirectEndpoint || c.ListenAddress == c.ProxyEndpoint || c.DirectEndpoint == c.ProxyEndpoint {
		validationErrors = append(validationErrors, errors.New("listen and candidate endpoints must use distinct addresses"))
	}
	if c.OriginalFallback != model.PathDirect && c.OriginalFallback != model.PathProxy {
		validationErrors = append(validationErrors, errors.New("original_fallback must be direct or proxy"))
	}
	if c.Decision.DirectHeadStartMS < 10 || c.Decision.DirectHeadStartMS > 2000 {
		validationErrors = append(validationErrors, errors.New("direct_head_start_ms must be between 10 and 2000"))
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
	if c.Privacy.Mode != "explicit-opt-in" && c.Privacy.Mode != "privacy-first" {
		validationErrors = append(validationErrors, errors.New("privacy mode must be explicit-opt-in or privacy-first"))
	}

	return errors.Join(validationErrors...)
}

func (c Config) DirectHeadStart() time.Duration {
	return time.Duration(c.Decision.DirectHeadStartMS) * time.Millisecond
}

func (c Config) CandidateTimeout() time.Duration {
	return time.Duration(c.Decision.CandidateTimeoutMS) * time.Millisecond
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
