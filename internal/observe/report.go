package observe

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

const ReportVersion = 1
const maxReportLineBytes = 1 << 20

var knownReportReasons = map[string]struct{}{
	"direct_candidate_before_head_start": {}, "proxy_candidate_before_head_start": {},
	"direct_candidate_won": {}, "proxy_candidate_won": {}, "direct_policy_only": {}, "proxy_policy_only": {},
	"direct_probe_allowed_explicit_opt_in": {}, "privacy_first_proxy_only": {},
	"never_direct_probe_exact": {}, "never_direct_probe_suffix": {},
	"invalid_target_proxy_only": {}, "missing_privacy_policy_proxy_only": {},
	"candidate_below_commit_stage": {}, "client_hello_rejected": {},
	"tls_candidates_failed": {}, "privacy_proxy_path_failed": {},
	"adaptive_available": {}, "adaptive_unavailable_use_original": {}, "adaptive_and_original_unavailable": {},
	"learning_health_active": {}, "learning_frozen_global_outage": {}, "learning_frozen_proxy_outage": {},
	"learning_frozen_network_change": {}, "learning_frozen_captive_portal": {},
	"learning_health_recovered": {}, "learning_health_freeze_expired": {},
	"durable_no_evidence": {}, "durable_conflicting_evidence": {},
	"durable_direct_evidence_insufficient": {}, "durable_proxy_evidence_insufficient": {},
	"durable_direct_route_suggested": {}, "durable_proxy_route_suggested": {},
}

var knownReportEventTypes = map[string]struct{}{
	"decision": {}, "diagnostic": {}, "guard_decision": {}, "supervisor": {},
	"learning_health": {}, "durable_learning_assessment": {},
}

type ReportOptions struct {
	Since time.Time
	Clock func() time.Time
}

// Report contains identity-free aggregates over bounded observation JSONL.
// Its readiness success is not an application-level or client-visible result.
type Report struct {
	ReportVersion           int                  `json:"report_version"`
	GeneratedAt             time.Time            `json:"generated_at"`
	Since                   time.Time            `json:"since"`
	FirstRecordedAt         *time.Time           `json:"first_recorded_at,omitempty"`
	LastRecordedAt          *time.Time           `json:"last_recorded_at,omitempty"`
	FilesScanned            int                  `json:"files_scanned"`
	EventsIncluded          int                  `json:"events_included"`
	RecordingPaused         bool                 `json:"recording_paused"`
	SourceCounts            map[string]int       `json:"source_counts"`
	EventCounts             map[string]int       `json:"event_counts"`
	ReasonCounts            map[string]int       `json:"reason_counts"`
	TargetScopesObserved    int                  `json:"target_scopes_observed"`
	NetworkProfilesObserved int                  `json:"network_profiles_observed"`
	TrialSessionsObserved   int                  `json:"trial_sessions_observed"`
	UnscopedEvents          int                  `json:"unscoped_events"`
	Adaptive                AdaptiveReport       `json:"adaptive"`
	Guard                   GuardReport          `json:"guard"`
	HealthTransitions       int                  `json:"health_transitions"`
	DurableAssessments      int                  `json:"durable_assessments"`
	Interpretation          ReportInterpretation `json:"interpretation"`
}

type AdaptiveReport struct {
	ReadinessOutcomes          int                     `json:"readiness_outcomes"`
	Ready                      int                     `json:"ready"`
	FailedBeforeReadiness      int                     `json:"failed_before_readiness"`
	ReadinessSuccessRatio      float64                 `json:"readiness_success_ratio"`
	SelectedDirect             int                     `json:"selected_direct"`
	SelectedProxy              int                     `json:"selected_proxy"`
	ProxySelectionRatio        float64                 `json:"proxy_selection_ratio"`
	NoCompletedOpposite        int                     `json:"no_completed_opposite"`
	DirectFailedProxySucceeded int                     `json:"direct_failed_proxy_succeeded"`
	DirectSucceededProxyFailed int                     `json:"direct_succeeded_proxy_failed"`
	OtherCompletedOutcomes     int                     `json:"other_completed_outcomes"`
	DecisionReadinessLatencyMS MillisecondDistribution `json:"decision_readiness_latency_ms"`
	WinnerCandidateLatencyMS   MillisecondDistribution `json:"winner_candidate_latency_ms"`
}

type GuardReport struct {
	Decisions        int `json:"decisions"`
	AdaptiveSelected int `json:"adaptive_selected"`
	OriginalSelected int `json:"original_selected"`
	Unavailable      int `json:"unavailable"`
}

type MillisecondDistribution struct {
	Samples int    `json:"samples"`
	P50     *int64 `json:"p50,omitempty"`
	P95     *int64 `json:"p95,omitempty"`
	P99     *int64 `json:"p99,omitempty"`
}

type ReportInterpretation struct {
	ReadinessNotApplicationSuccess bool `json:"readiness_not_application_success"`
	LatencyStartsAfterClientHello  bool `json:"latency_starts_after_client_hello"`
	NoStaticBaseline               bool `json:"no_static_baseline"`
	NoByteVolume                   bool `json:"no_byte_volume"`
	TargetIdentitiesOmitted        bool `json:"target_identities_omitted"`
	TrialSessionIDsOmitted         bool `json:"trial_session_ids_omitted"`
}

func BuildReport(directory string, options ReportOptions) (Report, error) {
	if strings.TrimSpace(directory) == "" {
		return Report{}, errors.New("observation report directory is required")
	}
	if options.Since.IsZero() {
		return Report{}, errors.New("observation report since time is required")
	}
	now := options.Clock
	if now == nil {
		now = time.Now
	}
	report := Report{ReportVersion: ReportVersion, GeneratedAt: now().UTC(), Since: options.Since.UTC(),
		SourceCounts: map[string]int{}, EventCounts: map[string]int{}, ReasonCounts: map[string]int{},
		Interpretation: ReportInterpretation{ReadinessNotApplicationSuccess: true,
			LatencyStartsAfterClientHello: true, NoStaticBaseline: true, NoByteVolume: true,
			TargetIdentitiesOmitted: true, TrialSessionIDsOmitted: true}}
	status, err := Inspect(directory)
	if err != nil {
		return Report{}, fmt.Errorf("inspect observation report source: %w", err)
	}
	report.RecordingPaused = status.Paused
	targets := map[string]struct{}{}
	profiles := map[string]struct{}{}
	trialSessions := map[string]struct{}{}
	var decisionLatencies, winnerLatencies []int64
	for _, source := range managedSources {
		err := walkManagedJSONL(directory, source, func(path string, _ fs.DirEntry) error {
			report.FilesScanned++
			return scanReportFile(path, source, report.Since, func(event storedEvent) error {
				report.EventsIncluded++
				report.SourceCounts[event.Source]++
				report.EventCounts[event.EventType]++
				if event.TrialSessionID == "" {
					report.UnscopedEvents++
				} else {
					trialSessions[event.TrialSessionID] = struct{}{}
				}
				if event.ReasonCode != "" {
					report.ReasonCounts[event.ReasonCode]++
				}
				updateReportRange(&report, event.RecordedAt)
				if event.Target != nil {
					profiles[event.Target.NetworkProfileHash] = struct{}{}
					targets[targetScopeKey(*event.Target)] = struct{}{}
				}
				switch event.EventType {
				case "decision":
					consumeDecision(&report.Adaptive, event, &decisionLatencies, &winnerLatencies)
				case "diagnostic":
					report.Adaptive.ReadinessOutcomes++
					report.Adaptive.FailedBeforeReadiness++
				case "guard_decision":
					consumeGuard(&report.Guard, event)
				case "learning_health":
					report.HealthTransitions++
				case "durable_learning_assessment":
					report.DurableAssessments++
				}
				return nil
			})
		})
		if err != nil {
			return Report{}, err
		}
	}
	report.TargetScopesObserved = len(targets)
	report.NetworkProfilesObserved = len(profiles)
	report.TrialSessionsObserved = len(trialSessions)
	if report.Adaptive.ReadinessOutcomes > 0 {
		report.Adaptive.ReadinessSuccessRatio = float64(report.Adaptive.Ready) / float64(report.Adaptive.ReadinessOutcomes)
	}
	selected := report.Adaptive.SelectedDirect + report.Adaptive.SelectedProxy
	if selected > 0 {
		report.Adaptive.ProxySelectionRatio = float64(report.Adaptive.SelectedProxy) / float64(selected)
	}
	report.Adaptive.DecisionReadinessLatencyMS = summarizeMilliseconds(decisionLatencies)
	report.Adaptive.WinnerCandidateLatencyMS = summarizeMilliseconds(winnerLatencies)
	return report, nil
}

func scanReportFile(path, expectedSource string, since time.Time, consume func(storedEvent) error) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open observation report input %s: %w", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxReportLineBytes)
	line := 0
	for scanner.Scan() {
		line++
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			return fmt.Errorf("%s:%d: empty observation row", path, line)
		}
		var event storedEvent
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			return fmt.Errorf("%s:%d: decode observation: %w", path, line, err)
		}
		if event.SchemaVersion != schemaVersion {
			return fmt.Errorf("%s:%d: unsupported observation schema %d", path, line, event.SchemaVersion)
		}
		if event.Source != expectedSource {
			return fmt.Errorf("%s:%d: source %q does not match directory %q", path, line, event.Source, expectedSource)
		}
		if event.RecordedAt.IsZero() || event.EventType == "" {
			return fmt.Errorf("%s:%d: recorded_at and event_type are required", path, line)
		}
		if !safeReportToken(event.EventType) || (event.ReasonCode != "" && !safeReportToken(event.ReasonCode)) {
			return fmt.Errorf("%s:%d: event_type and reason_code must be bounded safe tokens", path, line)
		}
		if _, known := knownReportEventTypes[event.EventType]; !known {
			return fmt.Errorf("%s:%d: unknown event_type %q", path, line, event.EventType)
		}
		if event.ReasonCode != "" {
			if _, known := knownReportReasons[event.ReasonCode]; !known {
				return fmt.Errorf("%s:%d: unknown reason_code %q", path, line, event.ReasonCode)
			}
		}
		if err := validateReportEvent(event); err != nil {
			return fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if event.RecordedAt.Before(since) {
			continue
		}
		if err := consume(event); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan observation report input %s: %w", path, err)
	}
	return nil
}

func validateReportEvent(event storedEvent) error {
	if event.TrialSessionID != "" {
		if err := ValidateTrialSessionID(event.TrialSessionID); err != nil {
			return err
		}
	}
	if event.Target != nil {
		if !hexDigest(event.Target.NetworkProfileHash) || !hexDigest(event.Target.HostnameHash) || event.Target.Port == 0 || !event.Target.Transport.Valid() {
			return errors.New("target requires two SHA-256 hashes, a non-zero port, and valid transport")
		}
	}
	if event.DecisionLatencyMS != nil && *event.DecisionLatencyMS < 0 {
		return errors.New("decision_latency_ms must not be negative")
	}
	switch event.EventType {
	case "decision":
		if event.Target == nil || event.Committed == nil || event.Observation == nil || event.ReasonCode == "" {
			return errors.New("decision requires target, committed, observation, and reason")
		}
		if event.SelectedPath != model.PathDirect && event.SelectedPath != model.PathProxy {
			return errors.New("decision requires a Direct or Proxy selected path")
		}
		if event.Observation.Path != event.SelectedPath {
			return errors.New("decision observation path must match selected path")
		}
		if *event.Committed && (!event.Observation.Success || event.Observation.StageReached < model.StageTCP) {
			return errors.New("committed decision must contain a ready successful observation")
		}
	case "diagnostic", "durable_learning_assessment":
		if event.Target == nil || event.ReasonCode == "" {
			return fmt.Errorf("%s requires target and reason", event.EventType)
		}
	case "guard_decision":
		if event.Target == nil || event.Committed == nil || event.ReasonCode == "" {
			return errors.New("guard_decision requires target, committed, and reason")
		}
	case "learning_health":
		if event.ReasonCode == "" {
			return errors.New("learning_health requires reason")
		}
	case "supervisor":
		if event.Target != nil {
			return errors.New("supervisor event must not contain target")
		}
	}
	return nil
}

func hexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') {
			continue
		}
		return false
	}
	return true
}

func safeReportToken(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func consumeDecision(report *AdaptiveReport, event storedEvent, decisionLatencies, winnerLatencies *[]int64) {
	report.ReadinessOutcomes++
	committed := event.Committed != nil && *event.Committed
	if !committed {
		report.FailedBeforeReadiness++
		return
	}
	report.Ready++
	switch event.SelectedPath {
	case model.PathDirect:
		report.SelectedDirect++
	case model.PathProxy:
		report.SelectedProxy++
	}
	if event.DecisionLatencyMS != nil && *event.DecisionLatencyMS >= 0 {
		*decisionLatencies = append(*decisionLatencies, *event.DecisionLatencyMS)
	}
	if event.Observation != nil && event.Observation.Latency >= 0 {
		*winnerLatencies = append(*winnerLatencies, event.Observation.Latency.Milliseconds())
	}
	if event.OtherObservation == nil {
		report.NoCompletedOpposite++
		return
	}
	other := event.OtherObservation
	if !other.Success && other.StageReached >= model.StageOutbound && other.FailureClass != "canceled" && other.FailureClass != "not_started" {
		if event.SelectedPath == model.PathProxy && other.Path == model.PathDirect {
			report.DirectFailedProxySucceeded++
			return
		}
		if event.SelectedPath == model.PathDirect && other.Path == model.PathProxy {
			report.DirectSucceededProxyFailed++
			return
		}
	}
	report.OtherCompletedOutcomes++
}

func consumeGuard(report *GuardReport, event storedEvent) {
	report.Decisions++
	switch event.SelectedLane {
	case "adaptive":
		report.AdaptiveSelected++
	case "original":
		report.OriginalSelected++
	default:
		report.Unavailable++
	}
}

func updateReportRange(report *Report, value time.Time) {
	value = value.UTC()
	if report.FirstRecordedAt == nil || value.Before(*report.FirstRecordedAt) {
		copy := value
		report.FirstRecordedAt = &copy
	}
	if report.LastRecordedAt == nil || value.After(*report.LastRecordedAt) {
		copy := value
		report.LastRecordedAt = &copy
	}
}

func targetScopeKey(target storedTarget) string {
	return target.NetworkProfileHash + "\x00" + target.HostnameHash + fmt.Sprintf("\x00%d\x00%s", target.Port, target.Transport)
}

func summarizeMilliseconds(values []int64) MillisecondDistribution {
	result := MillisecondDistribution{Samples: len(values)}
	if len(values) == 0 {
		return result
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	p50, p95, p99 := nearestRank(values, 50), nearestRank(values, 95), nearestRank(values, 99)
	result.P50, result.P95, result.P99 = &p50, &p95, &p99
	return result
}

func nearestRank(values []int64, percentile int) int64 {
	index := (percentile*len(values)+99)/100 - 1
	if index < 0 {
		index = 0
	}
	return values[index]
}
