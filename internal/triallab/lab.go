// Package triallab rehearses SmartRoute's local observation and assessment
// control plane with synthetic data. It opens no listener, makes no network
// connection, and never creates evidence accepted by trial preflight.
package triallab

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/netrelay"
	"github.com/firfisa/smartroute/internal/observe"
	"github.com/firfisa/smartroute/internal/trial"
)

const CurrentReportVersion = 1

const (
	plannedSession    = "trial-0123456789abcdef0123456789abcdef"
	unexpectedSession = "trial-1123456789abcdef0123456789abcdef"
)

type Report struct {
	ReportVersion          int              `json:"report_version"`
	GeneratedAt            time.Time        `json:"generated_at"`
	Isolation              IsolationResult  `json:"isolation"`
	SyntheticInputs        bool             `json:"synthetic_inputs"`
	PreflightEvidence      bool             `json:"preflight_evidence"`
	AuthorizesLiveTrial    bool             `json:"authorizes_live_trial"`
	AuthorizesPolicyChange bool             `json:"authorizes_policy_change"`
	Scenarios              []ScenarioResult `json:"scenarios"`
	Passed                 bool             `json:"passed"`
}

type IsolationResult struct {
	TemporaryWorkspace        bool `json:"temporary_workspace"`
	TemporaryWorkspaceRemoved bool `json:"temporary_workspace_removed"`
	ListenersOpened           bool `json:"listeners_opened"`
	ExternalNetwork           bool `json:"external_network"`
	ActiveClashRead           bool `json:"active_clash_read"`
	ActiveClashWritten        bool `json:"active_clash_written"`
	SystemProxyModified       bool `json:"system_proxy_modified"`
}

type ScenarioResult struct {
	Name                         string  `json:"name"`
	ExpectedAnalysisReady        bool    `json:"expected_analysis_ready"`
	ObservedAnalysisReady        bool    `json:"observed_analysis_ready"`
	ExpectedSessionMatched       bool    `json:"expected_session_matched"`
	UnexpectedTrialSessionEvents int     `json:"unexpected_trial_session_events"`
	CommittedSelections          int     `json:"committed_selections"`
	ConnectionScopeRatio         float64 `json:"connection_scope_ratio"`
	BaselineScopeRatio           float64 `json:"baseline_scope_ratio"`
	PairCompletenessRatio        float64 `json:"pair_completeness_ratio"`
	CancellationRatio            float64 `json:"cancellation_ratio"`
	Passed                       bool    `json:"passed"`
	FailureReason                string  `json:"failure_reason,omitempty"`
}

// Run creates and removes one private temporary workspace. Its synthetic
// output is a rehearsal result only and cannot satisfy trial preflight.
func Run(ctx context.Context) (report Report, runErr error) {
	report = Report{
		ReportVersion: CurrentReportVersion, GeneratedAt: time.Now().UTC(),
		Isolation:       IsolationResult{TemporaryWorkspace: true},
		SyntheticInputs: true, PreflightEvidence: false,
		AuthorizesLiveTrial: false, AuthorizesPolicyChange: false,
		Passed: true,
	}
	workspace, err := os.MkdirTemp("", "smartroute-trial-lab-*")
	if err != nil {
		return report, fmt.Errorf("create trial lab workspace: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(workspace); removeErr != nil {
			report.Passed = false
			runErr = errors.Join(runErr, fmt.Errorf("remove trial lab workspace: %w", removeErr))
			return
		}
		report.Isolation.TemporaryWorkspaceRemoved = true
	}()

	specs := []struct {
		name          string
		includeMixed  bool
		expectedReady bool
	}{
		{name: "planned_session_complete_window", expectedReady: true},
		{name: "unexpected_session_fails_closed", includeMixed: true, expectedReady: false},
	}
	for _, spec := range specs {
		if err := ctx.Err(); err != nil {
			report.Passed = false
			return report, err
		}
		result, scenarioErr := runScenario(ctx, filepath.Join(workspace, spec.name), spec.name, spec.includeMixed, spec.expectedReady)
		if scenarioErr != nil {
			result.Name = spec.name
			result.ExpectedAnalysisReady = spec.expectedReady
			result.FailureReason = "scenario_execution_failed"
			report.Passed = false
			report.Scenarios = append(report.Scenarios, result)
			return report, fmt.Errorf("trial lab scenario %s: %w", spec.name, scenarioErr)
		}
		report.Scenarios = append(report.Scenarios, result)
		report.Passed = report.Passed && result.Passed
	}
	if !report.Passed {
		return report, errors.New("one or more synthetic trial-lab scenarios failed")
	}
	return report, nil
}

func runScenario(ctx context.Context, directory, name string, includeMixed, expectedReady bool) (ScenarioResult, error) {
	notBefore := time.Now().UTC().Add(-time.Second)
	recordedAt := notBefore.Add(time.Millisecond)
	clock := func() time.Time {
		recordedAt = recordedAt.Add(time.Millisecond)
		return recordedAt
	}
	recorder, err := observe.New(observe.Options{
		Directory: directory, Source: observe.SourceEngine,
		MaxFileBytes: 1 << 20, MaxFiles: 2, Retention: time.Hour,
		Clock: clock, TrialSessionID: plannedSession,
	})
	if err != nil {
		return ScenarioResult{}, err
	}
	for index := 0; index < trial.DefaultAssessmentThresholds().MinCommittedSelections; index++ {
		if err := ctx.Err(); err != nil {
			_ = recorder.Close()
			return ScenarioResult{}, err
		}
		if err := recordConnection(recorder, index); err != nil {
			_ = recorder.Close()
			return ScenarioResult{}, err
		}
	}
	if err := recorder.Close(); err != nil {
		return ScenarioResult{}, err
	}
	if includeMixed {
		unexpected, err := observe.New(observe.Options{
			Directory: directory, Source: observe.SourceSupervisor,
			MaxFileBytes: 4096, MaxFiles: 1, Retention: time.Hour,
			Clock: clock, TrialSessionID: unexpectedSession,
		})
		if err != nil {
			return ScenarioResult{}, err
		}
		if err := unexpected.Record(observe.Event{EventType: "supervisor", Service: "engine", State: "started"}); err != nil {
			_ = unexpected.Close()
			return ScenarioResult{}, err
		}
		if err := unexpected.Close(); err != nil {
			return ScenarioResult{}, err
		}
	}
	if err := observe.Pause(directory); err != nil {
		return ScenarioResult{}, err
	}
	report, err := observe.BuildReport(directory, observe.ReportOptions{
		Since: notBefore, ExpectedTrialSessionID: plannedSession,
	})
	if err != nil {
		return ScenarioResult{}, err
	}
	assessment, err := trial.AssessObservations(report, trial.DefaultAssessmentThresholds(), time.Now)
	if err != nil {
		return ScenarioResult{}, err
	}
	result := ScenarioResult{
		Name: name, ExpectedAnalysisReady: expectedReady,
		ObservedAnalysisReady:        assessment.ReadyForDescriptiveAnalysis,
		ExpectedSessionMatched:       report.ExpectedTrialSessionMatched,
		UnexpectedTrialSessionEvents: report.UnexpectedTrialSessionEvents,
		CommittedSelections:          assessment.Metrics.CommittedSelections,
		ConnectionScopeRatio:         assessment.Metrics.ConnectionScopeRatio,
		BaselineScopeRatio:           assessment.Metrics.BaselineScopeRatio,
		PairCompletenessRatio:        assessment.Metrics.PairCompletenessRatio,
		CancellationRatio:            assessment.Metrics.CancellationRatio,
	}
	result.Passed = result.ObservedAnalysisReady == result.ExpectedAnalysisReady && result.ExpectedSessionMatched &&
		result.CommittedSelections == trial.DefaultAssessmentThresholds().MinCommittedSelections &&
		result.ConnectionScopeRatio == 1 && result.BaselineScopeRatio == 1 && result.PairCompletenessRatio == 1 && result.CancellationRatio == 0
	if includeMixed {
		result.Passed = result.Passed && result.UnexpectedTrialSessionEvents == 1
	} else {
		result.Passed = result.Passed && result.UnexpectedTrialSessionEvents == 0
	}
	if !result.Passed {
		result.FailureReason = "scenario_contract_mismatch"
	}
	return result, nil
}

func recordConnection(recorder *observe.Recorder, index int) error {
	target := model.Target{
		NetworkProfileID: "synthetic-trial-lab", Hostname: fmt.Sprintf("target-%02d.invalid", index),
		Port: 443, Transport: model.TransportTCP,
	}
	selected := model.PathProxy
	reason := "proxy_candidate_won"
	if index%2 == 0 {
		selected = model.PathDirect
		reason = "direct_candidate_won"
	}
	connectionID := fmt.Sprintf("conn-%032x", index+1)
	committed := true
	decisionMS := int64(20 + index)
	observation := model.Observation{Path: selected, Success: true, StageReached: model.StageTLS, Latency: time.Duration(10+index) * time.Millisecond}
	if err := recorder.Record(observe.Event{
		EventType: "decision", ConnectionID: connectionID, Target: &target,
		DeclaredBaselinePath: model.PathProxy, SelectedPath: selected, ReasonCode: reason,
		Observation: &observation, Committed: &committed, DecisionLatencyMS: &decisionMS,
	}); err != nil {
		return err
	}
	upload, download := int64(128+index), int64(1024+index)
	duration := int64(100 + index)
	return recorder.Record(observe.Event{
		EventType: "relay_outcome", ConnectionID: connectionID, Target: &target,
		DeclaredBaselinePath: model.PathProxy, SelectedPath: selected,
		ClientToRemoteBytes: &upload, RemoteToClientBytes: &download,
		ClientToRemoteEnd: string(netrelay.EndEOF), RemoteToClientEnd: string(netrelay.EndEOF),
		RelayDurationMS: &duration, Termination: "ended",
	})
}
