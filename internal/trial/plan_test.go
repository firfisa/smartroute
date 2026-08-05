package trial

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/config"
)

func TestAssessmentPlanBindsSessionWindowThresholdsAndConfig(t *testing.T) {
	cfg := config.Default()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan, err := NewAssessmentPlan(cfg, "trial-0123456789abcdef0123456789abcdef", now, 24*time.Hour, DefaultAssessmentThresholds())
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil || plan.Window() != 24*time.Hour {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	path := writePreflightReport(t, readyPreflightReport(now, plan))
	loaded, err := LoadAssessmentPlan(path, cfg)
	if err != nil || loaded.PlanSHA256 != plan.PlanSHA256 {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}

	drifted := cfg
	drifted.Decision.DirectHeadStartMS++
	if _, err := LoadAssessmentPlan(path, drifted); err == nil || !strings.Contains(err.Error(), "configuration") {
		t.Fatalf("config drift error=%v", err)
	}
}

func TestAssessmentPlanRejectsTamperingAndUnreadyReport(t *testing.T) {
	cfg := config.Default()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan, err := NewAssessmentPlan(cfg, "trial-0123456789abcdef0123456789abcdef", now, time.Hour, DefaultAssessmentThresholds())
	if err != nil {
		t.Fatal(err)
	}
	plan.WindowSeconds++
	path := writePreflightReport(t, readyPreflightReport(now, plan))
	if _, err := LoadAssessmentPlan(path, cfg); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tamper error=%v", err)
	}

	valid, err := NewAssessmentPlan(cfg, "trial-0123456789abcdef0123456789abcdef", now, time.Hour, DefaultAssessmentThresholds())
	if err != nil {
		t.Fatal(err)
	}
	path = writePreflightReport(t, Report{ReportVersion: PreflightReportVersion, GeneratedAt: now, Ready: false, AssessmentPlan: &valid})
	if _, err := LoadAssessmentPlan(path, cfg); err == nil {
		t.Fatalf("unready error=%v", err)
	}
}

func TestAssessmentPlanRejectsInvalidInputs(t *testing.T) {
	cfg := config.Default()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	thresholds := DefaultAssessmentThresholds()
	thresholds.MinCommittedSelections = 0
	for _, test := range []struct {
		session    string
		notBefore  time.Time
		window     time.Duration
		thresholds AssessmentThresholds
	}{
		{"bad", now, time.Hour, DefaultAssessmentThresholds()},
		{"trial-0123456789abcdef0123456789abcdef", time.Time{}, time.Hour, DefaultAssessmentThresholds()},
		{"trial-0123456789abcdef0123456789abcdef", now, 0, DefaultAssessmentThresholds()},
		{"trial-0123456789abcdef0123456789abcdef", now, time.Second + time.Millisecond, DefaultAssessmentThresholds()},
		{"trial-0123456789abcdef0123456789abcdef", now, time.Hour, thresholds},
	} {
		if _, err := NewAssessmentPlan(cfg, test.session, test.notBefore, test.window, test.thresholds); err == nil {
			t.Fatalf("accepted invalid input %+v", test)
		}
	}
}

func writePreflightReport(t *testing.T, report Report) string {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "preflight.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readyPreflightReport(now time.Time, plan AssessmentPlan) Report {
	ids := []string{
		"assessment.plan", "config.valid", "privacy.direct_probes", "baseline.original_path",
		"observation.enabled", "privacy.cleartext_hostname", "observation.paused", "observation.existing_files",
		"learning.mode", "learning.persistence", "learning.backup", "lab.testlab", "lab.mihomo", "safety.active_clash",
	}
	report := Report{ReportVersion: PreflightReportVersion, GeneratedAt: now, Ready: true, AssessmentPlan: &plan}
	for _, id := range ids {
		report.Checks = append(report.Checks, Check{ID: id, Status: StatusPass})
		report.Counts.Pass++
	}
	return report
}
