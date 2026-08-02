package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/privacy"
)

const CurrentVersion = 1

type Config struct {
	Version                int               `json:"version"`
	ListenAddress          string            `json:"listen_address"`
	DirectEndpoint         string            `json:"direct_endpoint"`
	ProxyEndpoint          string            `json:"proxy_endpoint"`
	GuardListenAddress     string            `json:"guard_listen_address"`
	OriginalEndpoint       string            `json:"original_endpoint"`
	GuardAdaptiveTimeoutMS int               `json:"guard_adaptive_timeout_ms"`
	OriginalFallback       model.Path        `json:"original_fallback"`
	Decision               DecisionConfig    `json:"decision"`
	Learning               LearningConfig    `json:"learning"`
	Privacy                PrivacyConfig     `json:"privacy"`
	Observation            ObservationConfig `json:"observation"`
}

type DecisionConfig struct {
	DirectHeadStartMS  int `json:"direct_head_start_ms"`
	MaxDirectPenaltyMS int `json:"max_direct_penalty_ms"`
	CandidateTimeoutMS int `json:"candidate_timeout_ms"`
}

type LearningConfig struct {
	Mode                string                    `json:"mode"`
	MaxEntries          int                       `json:"max_entries"`
	ProxyPromotionWins  int                       `json:"proxy_promotion_wins"`
	DirectPromotionWins int                       `json:"direct_promotion_wins"`
	PolicyTTLHours      int                       `json:"policy_ttl_hours"`
	Persistence         LearningPersistenceConfig `json:"persistence"`
}

type LearningPersistenceConfig struct {
	Enabled                  bool   `json:"enabled"`
	DatabasePath             string `json:"database_path"`
	QueueSize                int    `json:"queue_size"`
	RetentionHours           int    `json:"retention_hours"`
	ShutdownTimeoutMS        int    `json:"shutdown_timeout_ms"`
	DirectSuggestionSessions int    `json:"direct_suggestion_sessions"`
	ProxySuggestionSessions  int    `json:"proxy_suggestion_sessions"`
}

type PrivacyConfig struct {
	Mode             string   `json:"mode"`
	NeverDirectProbe []string `json:"never_direct_probe"`
}

type ObservationConfig struct {
	Enabled                  bool   `json:"enabled"`
	Directory                string `json:"directory"`
	MaxFileBytes             int64  `json:"max_file_bytes"`
	MaxFilesPerSource        int    `json:"max_files_per_source"`
	RetentionHours           int    `json:"retention_hours"`
	IncludeCleartextHostname bool   `json:"include_cleartext_hostname"`
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
			Mode:                learning.ModeShadow,
			MaxEntries:          10000,
			ProxyPromotionWins:  3,
			DirectPromotionWins: 5,
			PolicyTTLHours:      72,
			Persistence: LearningPersistenceConfig{
				Enabled: false, DatabasePath: "data/learning.db", QueueSize: 256,
				RetentionHours: 720, ShutdownTimeoutMS: 2000,
				DirectSuggestionSessions: 3, ProxySuggestionSessions: 2,
			},
		},
		Privacy: PrivacyConfig{
			Mode:             "explicit-opt-in",
			NeverDirectProbe: []string{},
		},
		Observation: ObservationConfig{
			Enabled: false, Directory: "data/observations",
			MaxFileBytes: 8 << 20, MaxFilesPerSource: 4, RetentionHours: 168,
			IncludeCleartextHostname: false,
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
	if _, present := fields["observation"]; !present {
		cfg.Observation = defaults.Observation
	}
	if raw, present := fields["learning"]; present {
		var learningFields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &learningFields); err == nil {
			if _, modePresent := learningFields["mode"]; !modePresent {
				cfg.Learning.Mode = defaults.Learning.Mode
			}
			if _, capacityPresent := learningFields["max_entries"]; !capacityPresent {
				cfg.Learning.MaxEntries = defaults.Learning.MaxEntries
			}
			if persistenceRaw, persistencePresent := learningFields["persistence"]; !persistencePresent {
				cfg.Learning.Persistence = defaults.Learning.Persistence
			} else {
				var persistenceFields map[string]json.RawMessage
				if err := json.Unmarshal(persistenceRaw, &persistenceFields); err == nil {
					if _, present := persistenceFields["database_path"]; !present {
						cfg.Learning.Persistence.DatabasePath = defaults.Learning.Persistence.DatabasePath
					}
					if _, present := persistenceFields["queue_size"]; !present {
						cfg.Learning.Persistence.QueueSize = defaults.Learning.Persistence.QueueSize
					}
					if _, present := persistenceFields["retention_hours"]; !present {
						cfg.Learning.Persistence.RetentionHours = defaults.Learning.Persistence.RetentionHours
					}
					if _, present := persistenceFields["shutdown_timeout_ms"]; !present {
						cfg.Learning.Persistence.ShutdownTimeoutMS = defaults.Learning.Persistence.ShutdownTimeoutMS
					}
					if _, present := persistenceFields["direct_suggestion_sessions"]; !present {
						cfg.Learning.Persistence.DirectSuggestionSessions = defaults.Learning.Persistence.DirectSuggestionSessions
					}
					if _, present := persistenceFields["proxy_suggestion_sessions"]; !present {
						cfg.Learning.Persistence.ProxySuggestionSessions = defaults.Learning.Persistence.ProxySuggestionSessions
					}
				}
			}
		}
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
	if c.Learning.Mode != learning.ModeShadow && c.Learning.Mode != learning.ModeEphemeralAuto {
		validationErrors = append(validationErrors, errors.New("learning.mode must be shadow or ephemeral-auto"))
	}
	if c.Learning.MaxEntries < 1 || c.Learning.MaxEntries > 1000000 {
		validationErrors = append(validationErrors, errors.New("learning.max_entries must be between 1 and 1000000"))
	}
	if c.Learning.PolicyTTLHours < 1 {
		validationErrors = append(validationErrors, errors.New("policy_ttl_hours must be positive"))
	}
	if c.Learning.Persistence.DatabasePath == "" {
		validationErrors = append(validationErrors, errors.New("learning.persistence.database_path must not be empty"))
	} else if clean := filepath.Clean(c.Learning.Persistence.DatabasePath); clean == "." || clean == string(filepath.Separator) {
		validationErrors = append(validationErrors, errors.New("learning.persistence.database_path must name a file"))
	}
	if c.Learning.Persistence.QueueSize < 1 || c.Learning.Persistence.QueueSize > 65536 {
		validationErrors = append(validationErrors, errors.New("learning.persistence.queue_size must be between 1 and 65536"))
	}
	if c.Learning.Persistence.RetentionHours < 1 || c.Learning.Persistence.RetentionHours > 87600 {
		validationErrors = append(validationErrors, errors.New("learning.persistence.retention_hours must be between 1 and 87600"))
	}
	if c.Learning.Persistence.ShutdownTimeoutMS < 100 || c.Learning.Persistence.ShutdownTimeoutMS > 30000 {
		validationErrors = append(validationErrors, errors.New("learning.persistence.shutdown_timeout_ms must be between 100 and 30000"))
	}
	if c.Learning.Persistence.DirectSuggestionSessions < 2 || c.Learning.Persistence.DirectSuggestionSessions > 1000 {
		validationErrors = append(validationErrors, errors.New("learning.persistence.direct_suggestion_sessions must be between 2 and 1000"))
	}
	if c.Learning.Persistence.ProxySuggestionSessions < 2 || c.Learning.Persistence.ProxySuggestionSessions > 1000 {
		validationErrors = append(validationErrors, errors.New("learning.persistence.proxy_suggestion_sessions must be between 2 and 1000"))
	}
	if _, err := privacy.New(c.Privacy.Mode, c.Privacy.NeverDirectProbe); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("privacy: %w", err))
	}
	if c.Observation.Directory == "" {
		validationErrors = append(validationErrors, errors.New("observation.directory must not be empty"))
	} else if clean := filepath.Clean(c.Observation.Directory); clean == "." || clean == string(filepath.Separator) {
		validationErrors = append(validationErrors, errors.New("observation.directory must not be the working directory or filesystem root"))
	}
	if c.Observation.MaxFileBytes < 1024 || c.Observation.MaxFileBytes > 1<<30 {
		validationErrors = append(validationErrors, errors.New("observation.max_file_bytes must be between 1024 and 1073741824"))
	}
	if c.Observation.MaxFilesPerSource < 1 || c.Observation.MaxFilesPerSource > 100 {
		validationErrors = append(validationErrors, errors.New("observation.max_files_per_source must be between 1 and 100"))
	}
	if c.Observation.RetentionHours < 1 || c.Observation.RetentionHours > 8760 {
		validationErrors = append(validationErrors, errors.New("observation.retention_hours must be between 1 and 8760"))
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

func (c Config) LearningEvidenceRetention() time.Duration {
	return time.Duration(c.Learning.Persistence.RetentionHours) * time.Hour
}

func (c Config) LearningShutdownTimeout() time.Duration {
	return time.Duration(c.Learning.Persistence.ShutdownTimeoutMS) * time.Millisecond
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
