// Package trial evaluates whether local evidence satisfies the prerequisites
// for a user-coordinated SmartRoute trial. It performs no network operations,
// opens no listeners, and never reads the active Clash environment.
package trial

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/firfisa/smartroute/internal/config"
	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/mihomolab"
	"github.com/firfisa/smartroute/internal/observe"
	"github.com/firfisa/smartroute/internal/privacy"
	"github.com/firfisa/smartroute/internal/store"
	"github.com/firfisa/smartroute/internal/testlab"
)

const DefaultMaxEvidenceAge = 24 * time.Hour

var requiredTestLabScenarios = []string{
	"direct_candidate_before_head_start",
	"proxy_recovers_slow_direct",
	"both_paths_fail",
}

var requiredMihomoLabScenarios = []string{
	"forced_direct_loopback",
	"forced_proxy_preserves_domain",
	"mihomo_socks_ack_is_not_target_readiness",
	"tls_proxy_recovers_unreachable_direct",
	"guard_falls_back_when_engine_unavailable",
	"guard_returns_to_adaptive_after_restart",
}

type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

type Check struct {
	ID      string `json:"id"`
	Status  Status `json:"status"`
	Summary string `json:"summary"`
}

type Counts struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

type Report struct {
	GeneratedAt              time.Time `json:"generated_at"`
	Ready                    bool      `json:"ready"`
	Checks                   []Check   `json:"checks"`
	Counts                   Counts    `json:"counts"`
	PersistentStateChanged   bool      `json:"persistent_state_changed"`
	ActiveClashInspected     bool      `json:"active_clash_inspected"`
	AuthorizesLiveActivation bool      `json:"authorizes_live_activation"`
}

type Options struct {
	Config                       config.Config
	TestLabReportPath            string
	MihomoLabReportPath          string
	LearningBackupPath           string
	AcknowledgeDirectProbes      bool
	AcknowledgeCleartextHostname bool
	AcknowledgeEphemeralAuto     bool
	MaxEvidenceAge               time.Duration
	Clock                        func() time.Time
}

// Preflight performs bounded, read-only validation of configuration, local
// control state, durable evidence, and isolated-lab reports.
func Preflight(ctx context.Context, options Options) Report {
	clock := options.Clock
	if clock == nil {
		clock = time.Now
	}
	now := clock().UTC()
	maxAge := options.MaxEvidenceAge
	if maxAge <= 0 {
		maxAge = DefaultMaxEvidenceAge
	}
	report := Report{GeneratedAt: now}
	add := func(id string, status Status, summary string) {
		report.Checks = append(report.Checks, Check{ID: id, Status: status, Summary: summary})
		switch status {
		case StatusPass:
			report.Counts.Pass++
		case StatusWarn:
			report.Counts.Warn++
		case StatusFail:
			report.Counts.Fail++
		}
	}

	if err := ctx.Err(); err != nil {
		add("preflight.context", StatusFail, "preflight context is unavailable")
		return finalize(report)
	}
	if err := options.Config.Validate(); err != nil {
		add("config.valid", StatusFail, "configuration validation failed")
	} else {
		add("config.valid", StatusPass, "configuration is valid")
	}
	checkPrivacy(options, add)
	checkObservation(options.Config.Observation, options.AcknowledgeCleartextHostname, add)
	checkLearning(ctx, options, add)
	checkTestLab(options.TestLabReportPath, now, maxAge, add)
	checkMihomoLab(options.MihomoLabReportPath, now, maxAge, add)
	add("safety.active_clash", StatusPass, "active Clash configuration and runtime were not inspected")
	return finalize(report)
}

func finalize(report Report) Report {
	report.Ready = report.Counts.Fail == 0
	return report
}

func checkPrivacy(options Options, add func(string, Status, string)) {
	switch options.Config.Privacy.Mode {
	case privacy.ModePrivacyFirst:
		add("privacy.direct_probes", StatusPass, "privacy-first mode does not open Direct candidates")
	case privacy.ModeExplicitOptIn:
		if options.AcknowledgeDirectProbes {
			add("privacy.direct_probes", StatusPass, "Direct candidate privacy impact was explicitly acknowledged")
		} else {
			add("privacy.direct_probes", StatusFail, "explicit-opt-in mode requires Direct-probe acknowledgment")
		}
	default:
		add("privacy.direct_probes", StatusFail, "privacy mode is unsupported")
	}
}

func checkObservation(cfg config.ObservationConfig, cleartextAcknowledged bool, add func(string, Status, string)) {
	if !cfg.Enabled {
		add("observation.enabled", StatusFail, "controlled trials require local observation recording")
	} else {
		add("observation.enabled", StatusPass, "local observation recording is enabled")
	}
	if cfg.IncludeCleartextHostname {
		if cleartextAcknowledged {
			add("privacy.cleartext_hostname", StatusWarn, "cleartext hostname recording was explicitly acknowledged")
		} else {
			add("privacy.cleartext_hostname", StatusFail, "cleartext hostname recording requires explicit acknowledgment")
		}
	} else {
		add("privacy.cleartext_hostname", StatusPass, "observation target identity remains hashed")
	}
	status, err := observe.Inspect(cfg.Directory)
	if err != nil {
		add("observation.paused", StatusFail, "observation control state could not be inspected")
		return
	}
	if status.Paused {
		add("observation.paused", StatusPass, "recording is paused before the trial window")
	} else {
		add("observation.paused", StatusFail, "recording must be paused before preflight")
	}
	if status.Files == 0 {
		add("observation.existing_files", StatusPass, "no earlier managed observation files are present")
	} else {
		add("observation.existing_files", StatusWarn, "earlier managed observation files are present and should be scoped during analysis")
	}
}

func checkLearning(ctx context.Context, options Options, add func(string, Status, string)) {
	switch options.Config.Learning.Mode {
	case learning.ModeShadow:
		add("learning.mode", StatusPass, "learning is in non-authoritative shadow mode")
	case learning.ModeEphemeralAuto:
		if options.AcknowledgeEphemeralAuto {
			add("learning.mode", StatusWarn, "ephemeral automatic decisions were explicitly acknowledged")
		} else {
			add("learning.mode", StatusFail, "ephemeral-auto mode requires explicit acknowledgment")
		}
	default:
		add("learning.mode", StatusFail, "learning mode is unsupported")
	}

	persistence := options.Config.Learning.Persistence
	if !persistence.Enabled {
		add("learning.persistence", StatusWarn, "durable cross-session evidence is disabled")
		if options.LearningBackupPath != "" {
			add("learning.backup", StatusWarn, "backup input is ignored because durable learning is disabled")
		} else {
			add("learning.backup", StatusPass, "no durable store requires backup")
		}
		return
	}
	databaseExists, databaseErr := regularFileExists(persistence.DatabasePath)
	keyExists, keyErr := regularFileExists(persistence.DatabasePath + ".key")
	if databaseErr != nil || keyErr != nil {
		add("learning.persistence", StatusFail, "durable store paths could not be safely inspected")
		add("learning.backup", StatusFail, "durable backup could not be matched")
		return
	}
	if !databaseExists && !keyExists {
		add("learning.persistence", StatusPass, "durable learning is configured for a fresh store")
		add("learning.backup", StatusPass, "a fresh durable store has no state to back up")
		return
	}
	if databaseExists != keyExists {
		add("learning.persistence", StatusFail, "durable database and key presence do not match")
		add("learning.backup", StatusFail, "durable backup could not be matched")
		return
	}
	current, err := store.OpenReadOnly(ctx, store.Config{Path: persistence.DatabasePath})
	if err != nil {
		add("learning.persistence", StatusFail, "existing durable store failed read-only validation")
		add("learning.backup", StatusFail, "durable backup could not be matched")
		return
	}
	status, statusErr := current.Status(ctx)
	closeErr := current.Close()
	if errors.Join(statusErr, closeErr) != nil {
		add("learning.persistence", StatusFail, "existing durable store failed read-only validation")
		add("learning.backup", StatusFail, "durable backup could not be matched")
		return
	}
	add("learning.persistence", StatusPass, "existing durable store passed read-only validation")
	if options.LearningBackupPath == "" {
		add("learning.backup", StatusFail, "existing durable state requires a verified matching backup")
		return
	}
	manifest, err := store.VerifyBackup(ctx, options.LearningBackupPath)
	if err != nil {
		add("learning.backup", StatusFail, "durable backup verification failed")
		return
	}
	if !reflect.DeepEqual(status, manifest.Status) {
		add("learning.backup", StatusFail, "verified backup does not match current durable store status")
		return
	}
	add("learning.backup", StatusPass, "verified backup matches current durable store status")
}

func checkTestLab(path string, now time.Time, maxAge time.Duration, add func(string, Status, string)) {
	var report testlab.Report
	if err := decodeStrictFile(path, &report); err != nil {
		add("lab.testlab", StatusFail, "a valid Test Lab report is required")
		return
	}
	valid := report.ReportVersion == testlab.CurrentReportVersion && report.Passed && len(report.Scenarios) > 0 &&
		report.Isolation.LoopbackOnly && report.Isolation.EphemeralPortsOnly && !report.Isolation.ExternalNetwork &&
		!report.Isolation.ClashFilesRead && !report.Isolation.ClashFilesWritten && testScenariosComplete(report.Scenarios) &&
		fresh(report.GeneratedAt, now, maxAge)
	if !valid {
		add("lab.testlab", StatusFail, "Test Lab report is stale or does not prove all isolation and scenario gates")
		return
	}
	add("lab.testlab", StatusPass, "fresh Test Lab report proves all isolation and scenario gates")
}

func checkMihomoLab(path string, now time.Time, maxAge time.Duration, add func(string, Status, string)) {
	var report mihomolab.Report
	if err := decodeStrictFile(path, &report); err != nil {
		add("lab.mihomo", StatusFail, "a valid isolated Mihomo Lab report is required")
		return
	}
	i := report.Isolation
	versionOK := containsToken(report.MihomoVersion, "Mihomo") &&
		containsToken(report.MihomoVersion, mihomolab.PinnedMihomoVersion) &&
		containsToken(report.MihomoVersion, mihomolab.PinnedBuildMarker)
	valid := report.ReportVersion == mihomolab.CurrentReportVersion && report.Passed && versionOK &&
		report.ConfigValidated && report.LoopPrevented && report.ReadinessGapDetected && report.TLSReadinessVerified &&
		report.GuardFallbackVerified && report.GuardRecoveryVerified && len(report.Scenarios) > 0 && mihomoScenariosComplete(report.Scenarios) &&
		i.DedicatedChildProcess && i.TemporaryHome && i.LoopbackOnly && i.EphemeralPortsOnly &&
		!i.TUNEnabled && !i.SystemProxyModified && !i.ExternalNetwork && !i.ActiveClashRead && !i.ActiveClashWritten &&
		fresh(report.GeneratedAt, now, maxAge)
	if !valid {
		add("lab.mihomo", StatusFail, "Mihomo Lab report is stale or does not prove the pinned topology and isolation gates")
		return
	}
	add("lab.mihomo", StatusPass, "fresh Mihomo Lab report proves the pinned topology and isolation gates")
}

func decodeStrictFile(path string, destination any) error {
	if path == "" {
		return errors.New("report path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("report contains trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("path is not a regular file")
	}
	return true, nil
}

func fresh(generatedAt, now time.Time, maxAge time.Duration) bool {
	_, offset := generatedAt.Zone()
	if generatedAt.IsZero() || offset != 0 || generatedAt.After(now.Add(5*time.Minute)) {
		return false
	}
	return now.Sub(generatedAt) <= maxAge
}

func containsToken(value, expected string) bool {
	for _, token := range strings.Fields(value) {
		if strings.Trim(token, ",;()[]") == expected {
			return true
		}
	}
	return false
}

func testScenariosComplete(scenarios []testlab.ScenarioResult) bool {
	names := make(map[string]bool, len(scenarios))
	for _, scenario := range scenarios {
		if !scenario.Passed || names[scenario.Name] {
			return false
		}
		names[scenario.Name] = true
	}
	return exactNames(names, requiredTestLabScenarios)
}

func mihomoScenariosComplete(scenarios []mihomolab.ScenarioResult) bool {
	names := make(map[string]bool, len(scenarios))
	for _, scenario := range scenarios {
		if !scenario.Passed || names[scenario.Name] {
			return false
		}
		names[scenario.Name] = true
	}
	return exactNames(names, requiredMihomoLabScenarios)
}

func exactNames(observed map[string]bool, required []string) bool {
	if len(observed) != len(required) {
		return false
	}
	for _, name := range required {
		if !observed[name] {
			return false
		}
	}
	return true
}
