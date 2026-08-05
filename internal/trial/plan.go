package trial

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/firfisa/smartroute/internal/config"
	"github.com/firfisa/smartroute/internal/observe"
)

const AssessmentPlanVersion = 1

type AssessmentPlan struct {
	PlanVersion    int                  `json:"plan_version"`
	TrialSessionID string               `json:"trial_session_id"`
	ConfigSHA256   string               `json:"config_sha256"`
	NotBefore      time.Time            `json:"not_before"`
	WindowSeconds  int64                `json:"window_seconds"`
	Thresholds     AssessmentThresholds `json:"thresholds"`
	PlanSHA256     string               `json:"plan_sha256"`
}

type assessmentPlanPayload struct {
	PlanVersion    int                  `json:"plan_version"`
	TrialSessionID string               `json:"trial_session_id"`
	ConfigSHA256   string               `json:"config_sha256"`
	NotBefore      time.Time            `json:"not_before"`
	WindowSeconds  int64                `json:"window_seconds"`
	Thresholds     AssessmentThresholds `json:"thresholds"`
}

func NewAssessmentPlan(cfg config.Config, trialSessionID string, notBefore time.Time, window time.Duration, thresholds AssessmentThresholds) (AssessmentPlan, error) {
	if err := observe.ValidateTrialSessionID(trialSessionID); err != nil {
		return AssessmentPlan{}, err
	}
	if window <= 0 || window%time.Second != 0 {
		return AssessmentPlan{}, errors.New("assessment window must be a positive whole number of seconds")
	}
	_, offset := notBefore.Zone()
	if notBefore.IsZero() || offset != 0 {
		return AssessmentPlan{}, errors.New("assessment not-before time must be non-zero UTC")
	}
	if err := thresholds.Validate(); err != nil {
		return AssessmentPlan{}, err
	}
	fingerprint, err := configFingerprint(cfg)
	if err != nil {
		return AssessmentPlan{}, err
	}
	plan := AssessmentPlan{
		PlanVersion: AssessmentPlanVersion, TrialSessionID: trialSessionID,
		ConfigSHA256: fingerprint, NotBefore: notBefore, WindowSeconds: int64(window / time.Second), Thresholds: thresholds,
	}
	plan.PlanSHA256, err = assessmentPlanDigest(plan)
	if err != nil {
		return AssessmentPlan{}, err
	}
	return plan, nil
}

func (p AssessmentPlan) Validate() error {
	if p.PlanVersion != AssessmentPlanVersion {
		return errors.New("assessment plan version is not current")
	}
	if err := observe.ValidateTrialSessionID(p.TrialSessionID); err != nil {
		return err
	}
	if !sha256Hex(p.ConfigSHA256) || !sha256Hex(p.PlanSHA256) {
		return errors.New("assessment plan digests must be lowercase SHA-256 values")
	}
	_, offset := p.NotBefore.Zone()
	if p.NotBefore.IsZero() || offset != 0 {
		return errors.New("assessment plan not-before time must be non-zero UTC")
	}
	if p.WindowSeconds <= 0 || p.WindowSeconds > math.MaxInt64/int64(time.Second) {
		return errors.New("assessment plan window must be positive")
	}
	if err := p.Thresholds.Validate(); err != nil {
		return err
	}
	expected, err := assessmentPlanDigest(p)
	if err != nil {
		return err
	}
	if expected != p.PlanSHA256 {
		return errors.New("assessment plan digest does not match its contents")
	}
	return nil
}

func (p AssessmentPlan) Window() time.Duration {
	return time.Duration(p.WindowSeconds) * time.Second
}

func LoadAssessmentPlan(path string, cfg config.Config) (AssessmentPlan, error) {
	var report Report
	if err := decodeStrictFile(path, &report); err != nil {
		return AssessmentPlan{}, fmt.Errorf("read preflight report: %w", err)
	}
	if report.ReportVersion != PreflightReportVersion || report.AssessmentPlan == nil {
		return AssessmentPlan{}, errors.New("a ready current preflight report with an assessment plan is required")
	}
	if err := validateReadyPreflightReport(report); err != nil {
		return AssessmentPlan{}, err
	}
	if !report.GeneratedAt.Equal(report.AssessmentPlan.NotBefore) {
		return AssessmentPlan{}, errors.New("preflight generation time does not match the assessment plan")
	}
	if report.PersistentStateChanged || report.ActiveClashInspected || report.AuthorizesLiveActivation {
		return AssessmentPlan{}, errors.New("preflight report contains unsafe state or authorization claims")
	}
	if err := report.AssessmentPlan.Validate(); err != nil {
		return AssessmentPlan{}, fmt.Errorf("validate assessment plan: %w", err)
	}
	fingerprint, err := configFingerprint(cfg)
	if err != nil {
		return AssessmentPlan{}, err
	}
	if fingerprint != report.AssessmentPlan.ConfigSHA256 {
		return AssessmentPlan{}, errors.New("current configuration does not match the preflight assessment plan")
	}
	return *report.AssessmentPlan, nil
}

func validateReadyPreflightReport(report Report) error {
	required := map[string]bool{
		"assessment.plan": false, "config.valid": false, "privacy.direct_probes": false,
		"baseline.original_path": false, "observation.enabled": false, "privacy.cleartext_hostname": false,
		"observation.paused": false, "observation.existing_files": false, "learning.mode": false,
		"learning.persistence": false, "learning.backup": false, "lab.testlab": false,
		"lab.mihomo": false, "safety.active_clash": false,
	}
	var counts Counts
	allowedWarnings := map[string]bool{
		"privacy.cleartext_hostname": true, "observation.existing_files": true,
		"learning.mode": true, "learning.persistence": true, "learning.backup": true,
	}
	for _, check := range report.Checks {
		seen, known := required[check.ID]
		if !known || seen {
			return errors.New("preflight report check set is not current")
		}
		required[check.ID] = true
		switch check.Status {
		case StatusPass:
			counts.Pass++
		case StatusWarn:
			if !allowedWarnings[check.ID] {
				return errors.New("preflight report contains a warning for a mandatory pass check")
			}
			counts.Warn++
		case StatusFail:
			counts.Fail++
		default:
			return errors.New("preflight report contains an invalid check status")
		}
	}
	for _, seen := range required {
		if !seen {
			return errors.New("preflight report check set is not current")
		}
	}
	if counts != report.Counts || counts.Fail != 0 || !report.Ready {
		return errors.New("preflight report readiness and check counts are inconsistent")
	}
	return nil
}

func configFingerprint(cfg config.Config) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("encode configuration fingerprint: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func assessmentPlanDigest(plan AssessmentPlan) (string, error) {
	payload := assessmentPlanPayload{
		PlanVersion: plan.PlanVersion, TrialSessionID: plan.TrialSessionID,
		ConfigSHA256: plan.ConfigSHA256, NotBefore: plan.NotBefore,
		WindowSeconds: plan.WindowSeconds, Thresholds: plan.Thresholds,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode assessment plan digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func sha256Hex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == hex.EncodeToString(decoded)
}
