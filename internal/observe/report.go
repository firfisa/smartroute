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

	"github.com/firfisa/smartroute/internal/connectionid"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/netrelay"
)

const ReportVersion = 7
const maxReportLineBytes = 1 << 20
const maxInt64 = int64(^uint64(0) >> 1)

var knownReportReasons = map[string]struct{}{
	"direct_candidate_before_head_start": {}, "proxy_candidate_before_head_start": {},
	"direct_candidate_won": {}, "proxy_candidate_won": {}, "direct_policy_only": {}, "proxy_policy_only": {},
	"direct_probe_allowed_explicit_opt_in": {}, "privacy_first_proxy_only": {},
	"never_direct_probe_exact": {}, "never_direct_probe_suffix": {},
	"invalid_target_proxy_only": {}, "missing_privacy_policy_proxy_only": {},
	"candidate_below_commit_stage": {}, "client_hello_rejected": {},
	"tls_candidates_failed": {}, "privacy_proxy_path_failed": {},
	"learning_update_error": {}, "learning_skipped_by_policy": {},
	"durable_policy_selected": {}, "durable_policy_fallback": {},
	"adaptive_available": {}, "adaptive_unavailable_use_original": {}, "adaptive_and_original_unavailable": {},
	"learning_health_active": {}, "learning_frozen_global_outage": {}, "learning_frozen_proxy_outage": {},
	"learning_frozen_network_change": {}, "learning_frozen_captive_portal": {},
	"learning_health_recovered": {}, "learning_health_freeze_expired": {},
	"durable_no_evidence": {}, "durable_conflicting_evidence": {},
	"durable_direct_evidence_insufficient": {}, "durable_proxy_evidence_insufficient": {},
	"durable_direct_route_suggested": {}, "durable_proxy_route_suggested": {},
}

var knownReportLearningReasons = map[string]struct{}{
	"incomplete_paired_evidence_no_update": {}, "weak_path_failure_no_update": {},
	"learning_capacity_reached_no_update": {}, "strong_direct_evidence_recorded": {},
	"strong_proxy_evidence_recorded": {}, "ephemeral_direct_preference_promoted": {},
	"ephemeral_proxy_preference_promoted": {}, "ephemeral_preference_refreshed": {},
	"ephemeral_preference_contradicted": {}, "learning_skipped_health_frozen": {},
	"automatic_direct_path_remembered": {}, "automatic_proxy_path_remembered": {},
	"automatic_path_unchanged": {}, "learning_update_error": {}, "learning_skipped_by_policy": {},
}

var knownReportDurableReasons = map[string]struct{}{
	"durable_evidence_queued": {}, "durable_evidence_queue_full": {},
	"durable_evidence_writer_closed": {}, "durable_policy_queued": {},
	"durable_policy_queue_full": {}, "durable_policy_writer_closed": {},
}

var knownReportEventTypes = map[string]struct{}{
	"decision": {}, "diagnostic": {}, "guard_decision": {}, "supervisor": {},
	"learning_health": {}, "durable_learning_assessment": {},
	"relay_outcome": {},
}

type ReportOptions struct {
	Since                  time.Time
	ExpectedTrialSessionID string
	Clock                  func() time.Time
}

// Report contains identity-free aggregates over bounded observation JSONL.
// Its readiness success is not an application-level or client-visible result.
type Report struct {
	ReportVersion                int                    `json:"report_version"`
	GeneratedAt                  time.Time              `json:"generated_at"`
	Since                        time.Time              `json:"since"`
	FirstRecordedAt              *time.Time             `json:"first_recorded_at,omitempty"`
	LastRecordedAt               *time.Time             `json:"last_recorded_at,omitempty"`
	FilesScanned                 int                    `json:"files_scanned"`
	EventsIncluded               int                    `json:"events_included"`
	RecordingPaused              bool                   `json:"recording_paused"`
	SourceCounts                 map[string]int         `json:"source_counts"`
	EventCounts                  map[string]int         `json:"event_counts"`
	ReasonCounts                 map[string]int         `json:"reason_counts"`
	LearningReasonCounts         map[string]int         `json:"learning_reason_counts"`
	DurableReasonCounts          map[string]int         `json:"durable_reason_counts"`
	TargetScopesObserved         int                    `json:"target_scopes_observed"`
	NetworkProfilesObserved      int                    `json:"network_profiles_observed"`
	TrialSessionsObserved        int                    `json:"trial_sessions_observed"`
	UnscopedEvents               int                    `json:"unscoped_events"`
	ExpectedTrialSessionMatched  bool                   `json:"expected_trial_session_matched"`
	UnexpectedTrialSessionEvents int                    `json:"unexpected_trial_session_events"`
	ConnectionScope              ConnectionScopeReport  `json:"connection_scope"`
	DeclaredBaseline             DeclaredBaselineReport `json:"declared_baseline"`
	Adaptive                     AdaptiveReport         `json:"adaptive"`
	Guard                        GuardReport            `json:"guard"`
	HealthTransitions            int                    `json:"health_transitions"`
	DurableAssessments           int                    `json:"durable_assessments"`
	Interpretation               ReportInterpretation   `json:"interpretation"`
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
	Relay                      RelayReport             `json:"relay"`
}

type RelayReport struct {
	Outcomes                    int                     `json:"outcomes"`
	Ended                       int                     `json:"ended"`
	Canceled                    int                     `json:"canceled"`
	WithRemoteToClientBytes     int                     `json:"with_remote_to_client_bytes"`
	RemoteToClientCoverageRatio float64                 `json:"remote_to_client_coverage_ratio"`
	ClientToRemoteBytes         int64                   `json:"client_to_remote_bytes"`
	RemoteToClientBytes         int64                   `json:"remote_to_client_bytes"`
	Direct                      RelayPathReport         `json:"direct"`
	Proxy                       RelayPathReport         `json:"proxy"`
	ClientToRemoteEnd           RelayEndReport          `json:"client_to_remote_end"`
	RemoteToClientEnd           RelayEndReport          `json:"remote_to_client_end"`
	DurationMS                  MillisecondDistribution `json:"duration_ms"`
}

type RelayEndReport struct {
	EOF          int `json:"eof"`
	Timeout      int `json:"timeout"`
	Reset        int `json:"reset"`
	Closed       int `json:"closed"`
	IOError      int `json:"io_error"`
	Canceled     int `json:"canceled"`
	Unclassified int `json:"unclassified"`
}

type RelayPathReport struct {
	Connections         int   `json:"connections"`
	ClientToRemoteBytes int64 `json:"client_to_remote_bytes"`
	RemoteToClientBytes int64 `json:"remote_to_client_bytes"`
}

type ConnectionScopeReport struct {
	ScopedDecisions                  int `json:"scoped_decisions"`
	ScopedCommittedDecisions         int `json:"scoped_committed_decisions"`
	ScopedDiagnostics                int `json:"scoped_diagnostics"`
	ScopedRelayOutcomes              int `json:"scoped_relay_outcomes"`
	UnscopedDecisions                int `json:"unscoped_decisions"`
	UnscopedCommittedDecisions       int `json:"unscoped_committed_decisions"`
	UnscopedDiagnostics              int `json:"unscoped_diagnostics"`
	UnscopedRelayOutcomes            int `json:"unscoped_relay_outcomes"`
	PairedRelayOutcomes              int `json:"paired_relay_outcomes"`
	UnmatchedRelayOutcomes           int `json:"unmatched_relay_outcomes"`
	CommittedDecisionsWithoutOutcome int `json:"committed_decisions_without_outcome"`
}

// DeclaredBaselineReport compares adaptive selection with the configured
// original fallback. It does not claim the counterfactual route was executed.
type DeclaredBaselineReport struct {
	ScopedSelections           int     `json:"scoped_selections"`
	UnscopedSelections         int     `json:"unscoped_selections"`
	SameAsDeclared             int     `json:"same_as_declared"`
	ChangedFromDeclared        int     `json:"changed_from_declared"`
	DirectInsteadOfProxy       int     `json:"direct_instead_of_proxy"`
	ProxyInsteadOfDirect       int     `json:"proxy_instead_of_direct"`
	ChangedSelectionRatio      float64 `json:"changed_selection_ratio"`
	ScopedRelayOutcomes        int     `json:"scoped_relay_outcomes"`
	UnscopedRelayOutcomes      int     `json:"unscoped_relay_outcomes"`
	ChangedRelayOutcomes       int     `json:"changed_relay_outcomes"`
	ChangedClientToRemoteBytes int64   `json:"changed_client_to_remote_bytes"`
	ChangedRemoteToClientBytes int64   `json:"changed_remote_to_client_bytes"`
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
	ReadinessNotApplicationSuccess        bool `json:"readiness_not_application_success"`
	RelayRemoteBytesNotApplicationSuccess bool `json:"relay_remote_bytes_not_application_success"`
	RelayBytesPostCommitAdaptiveOnly      bool `json:"relay_bytes_post_commit_adaptive_only"`
	LatencyStartsAfterClientHello         bool `json:"latency_starts_after_client_hello"`
	NoVerifiedStaticBaseline              bool `json:"no_verified_static_baseline"`
	TargetIdentitiesOmitted               bool `json:"target_identities_omitted"`
	TrialSessionIDsOmitted                bool `json:"trial_session_ids_omitted"`
	ConnectionIDsOmitted                  bool `json:"connection_ids_omitted"`
	BaselineIsDeclaredNotObserved         bool `json:"baseline_is_declared_not_observed"`
	ChangedBytesNotCounterfactualSavings  bool `json:"changed_bytes_not_counterfactual_savings"`
	DirectionEndsNotApplicationSuccess    bool `json:"direction_ends_not_application_success"`
}

func BuildReport(directory string, options ReportOptions) (Report, error) {
	if strings.TrimSpace(directory) == "" {
		return Report{}, errors.New("observation report directory is required")
	}
	if options.Since.IsZero() {
		return Report{}, errors.New("observation report since time is required")
	}
	if options.ExpectedTrialSessionID != "" {
		if err := ValidateTrialSessionID(options.ExpectedTrialSessionID); err != nil {
			return Report{}, err
		}
	}
	now := options.Clock
	if now == nil {
		now = time.Now
	}
	report := Report{ReportVersion: ReportVersion, GeneratedAt: now().UTC(), Since: options.Since.UTC(),
		SourceCounts: map[string]int{}, EventCounts: map[string]int{}, ReasonCounts: map[string]int{},
		LearningReasonCounts: map[string]int{}, DurableReasonCounts: map[string]int{},
		Interpretation: ReportInterpretation{ReadinessNotApplicationSuccess: true,
			RelayRemoteBytesNotApplicationSuccess: true, RelayBytesPostCommitAdaptiveOnly: true,
			LatencyStartsAfterClientHello: true, NoVerifiedStaticBaseline: true,
			TargetIdentitiesOmitted: true, TrialSessionIDsOmitted: true, ConnectionIDsOmitted: true,
			BaselineIsDeclaredNotObserved: true, ChangedBytesNotCounterfactualSavings: true,
			DirectionEndsNotApplicationSuccess: true}}
	status, err := Inspect(directory)
	if err != nil {
		return Report{}, fmt.Errorf("inspect observation report source: %w", err)
	}
	report.RecordingPaused = status.Paused
	targets := map[string]struct{}{}
	profiles := map[string]struct{}{}
	trialSessions := map[string]struct{}{}
	matchingTrialSessionEvents := 0
	pairs := newConnectionPairer(&report.ConnectionScope)
	var decisionLatencies, winnerLatencies, relayDurations []int64
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
					if options.ExpectedTrialSessionID != "" {
						if event.TrialSessionID == options.ExpectedTrialSessionID {
							matchingTrialSessionEvents++
						} else {
							report.UnexpectedTrialSessionEvents++
						}
					}
				}
				if event.ReasonCode != "" {
					report.ReasonCounts[event.ReasonCode]++
				}
				if event.LearningReason != "" {
					report.LearningReasonCounts[event.LearningReason]++
				}
				if event.DurableReason != "" {
					report.DurableReasonCounts[event.DurableReason]++
				}
				updateReportRange(&report, event.RecordedAt)
				if event.Target != nil {
					profiles[event.Target.NetworkProfileHash] = struct{}{}
					targets[targetScopeKey(*event.Target)] = struct{}{}
				}
				switch event.EventType {
				case "decision":
					consumeDecision(&report.Adaptive, event, &decisionLatencies, &winnerLatencies)
					consumeBaselineDecision(&report.DeclaredBaseline, event)
					if err := pairs.observeTerminal(event); err != nil {
						return fmt.Errorf("%s: pair decision: %w", path, err)
					}
				case "diagnostic":
					report.Adaptive.ReadinessOutcomes++
					report.Adaptive.FailedBeforeReadiness++
					if err := pairs.observeTerminal(event); err != nil {
						return fmt.Errorf("%s: pair diagnostic: %w", path, err)
					}
				case "guard_decision":
					consumeGuard(&report.Guard, event)
				case "learning_health":
					report.HealthTransitions++
				case "durable_learning_assessment":
					report.DurableAssessments++
				case "relay_outcome":
					if err := consumeRelay(&report.Adaptive.Relay, event, &relayDurations); err != nil {
						return fmt.Errorf("%s: aggregate relay outcome: %w", path, err)
					}
					if err := pairs.observeRelay(event); err != nil {
						return fmt.Errorf("%s: pair relay outcome: %w", path, err)
					}
					if err := consumeBaselineRelay(&report.DeclaredBaseline, event); err != nil {
						return fmt.Errorf("%s: aggregate declared baseline: %w", path, err)
					}
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
	report.ExpectedTrialSessionMatched = options.ExpectedTrialSessionID != "" && matchingTrialSessionEvents > 0
	if err := pairs.finalize(); err != nil {
		return Report{}, err
	}
	if report.Adaptive.ReadinessOutcomes > 0 {
		report.Adaptive.ReadinessSuccessRatio = float64(report.Adaptive.Ready) / float64(report.Adaptive.ReadinessOutcomes)
	}
	if report.DeclaredBaseline.ScopedSelections > 0 {
		report.DeclaredBaseline.ChangedSelectionRatio = float64(report.DeclaredBaseline.ChangedFromDeclared) / float64(report.DeclaredBaseline.ScopedSelections)
	}
	selected := report.Adaptive.SelectedDirect + report.Adaptive.SelectedProxy
	if selected > 0 {
		report.Adaptive.ProxySelectionRatio = float64(report.Adaptive.SelectedProxy) / float64(selected)
	}
	report.Adaptive.DecisionReadinessLatencyMS = summarizeMilliseconds(decisionLatencies)
	report.Adaptive.WinnerCandidateLatencyMS = summarizeMilliseconds(winnerLatencies)
	report.Adaptive.Relay.DurationMS = summarizeMilliseconds(relayDurations)
	if report.Adaptive.Relay.Outcomes > 0 {
		report.Adaptive.Relay.RemoteToClientCoverageRatio = float64(report.Adaptive.Relay.WithRemoteToClientBytes) / float64(report.Adaptive.Relay.Outcomes)
	}
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
		if event.SchemaVersion != legacySchemaVersion && event.SchemaVersion != relaySchemaVersion && event.SchemaVersion != connectionSchemaVersion && event.SchemaVersion != baselineSchemaVersion && event.SchemaVersion != schemaVersion {
			return fmt.Errorf("%s:%d: unsupported observation schema %d", path, line, event.SchemaVersion)
		}
		if event.Source != expectedSource {
			return fmt.Errorf("%s:%d: source %q does not match directory %q", path, line, event.Source, expectedSource)
		}
		if event.RecordedAt.IsZero() || event.EventType == "" {
			return fmt.Errorf("%s:%d: recorded_at and event_type are required", path, line)
		}
		if !safeReportToken(event.EventType) || (event.ReasonCode != "" && !safeReportToken(event.ReasonCode)) ||
			(event.LearningReason != "" && !safeReportToken(event.LearningReason)) ||
			(event.DurableReason != "" && !safeReportToken(event.DurableReason)) {
			return fmt.Errorf("%s:%d: event and reason fields must be bounded safe tokens", path, line)
		}
		if _, known := knownReportEventTypes[event.EventType]; !known {
			return fmt.Errorf("%s:%d: unknown event_type %q", path, line, event.EventType)
		}
		if event.ReasonCode != "" {
			if _, known := knownReportReasons[event.ReasonCode]; !known {
				return fmt.Errorf("%s:%d: unknown reason_code", path, line)
			}
		}
		if event.LearningReason != "" {
			if _, known := knownReportLearningReasons[event.LearningReason]; !known {
				return fmt.Errorf("%s:%d: unknown learning_reason", path, line)
			}
		}
		if event.DurableReason != "" {
			if _, known := knownReportDurableReasons[event.DurableReason]; !known {
				return fmt.Errorf("%s:%d: unknown durable_reason", path, line)
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
	if event.ConnectionID != "" {
		if event.SchemaVersion < connectionSchemaVersion {
			return errors.New("connection_id requires observation schema 3")
		}
		if err := connectionid.Validate(event.ConnectionID); err != nil {
			return err
		}
	}
	if event.DeclaredBaselinePath != "" {
		if event.SchemaVersion < baselineSchemaVersion {
			return errors.New("declared_baseline_path requires observation schema 4")
		}
		if event.DeclaredBaselinePath != model.PathDirect && event.DeclaredBaselinePath != model.PathProxy {
			return errors.New("declared_baseline_path must be direct or proxy")
		}
		if event.EventType != "decision" && event.EventType != "diagnostic" && event.EventType != "relay_outcome" {
			return errors.New("declared_baseline_path is valid only for sidecar terminal and relay events")
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
	relayFieldsPresent := event.ClientToRemoteBytes != nil || event.RemoteToClientBytes != nil ||
		event.ClientToRemoteEnd != "" || event.RemoteToClientEnd != "" || event.RelayDurationMS != nil || event.Termination != ""
	if event.EventType != "relay_outcome" && relayFieldsPresent {
		return errors.New("relay fields are valid only for relay_outcome")
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
	case "relay_outcome":
		if event.SchemaVersion < relaySchemaVersion {
			return errors.New("relay_outcome requires observation schema 2 or newer")
		}
		if event.Target == nil || event.ClientToRemoteBytes == nil || event.RemoteToClientBytes == nil || event.RelayDurationMS == nil {
			return errors.New("relay_outcome requires target and complete byte/duration counters")
		}
		if event.SelectedPath != model.PathDirect && event.SelectedPath != model.PathProxy {
			return errors.New("relay_outcome requires a Direct or Proxy selected path")
		}
		if *event.ClientToRemoteBytes < 0 || *event.RemoteToClientBytes < 0 || *event.RelayDurationMS < 0 {
			return errors.New("relay_outcome counters must not be negative")
		}
		if event.Termination != "ended" && event.Termination != "canceled" {
			return errors.New("relay_outcome termination must be ended or canceled")
		}
		clientEnd := netrelay.EndReason(event.ClientToRemoteEnd)
		remoteEnd := netrelay.EndReason(event.RemoteToClientEnd)
		if event.SchemaVersion < schemaVersion {
			if event.ClientToRemoteEnd != "" || event.RemoteToClientEnd != "" {
				return errors.New("relay direction end reasons require observation schema 5")
			}
		} else {
			if !clientEnd.Valid() || !remoteEnd.Valid() {
				return errors.New("schema-5 relay_outcome requires two bounded direction end reasons")
			}
			if event.Termination == "canceled" && (clientEnd != netrelay.EndCanceled || remoteEnd != netrelay.EndCanceled) {
				return errors.New("canceled relay_outcome requires canceled direction ends")
			}
			if event.Termination == "ended" && (clientEnd == netrelay.EndCanceled || remoteEnd == netrelay.EndCanceled) {
				return errors.New("ended relay_outcome must not contain canceled direction ends")
			}
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

func consumeBaselineDecision(report *DeclaredBaselineReport, event storedEvent) {
	if event.Committed == nil || !*event.Committed {
		return
	}
	if event.DeclaredBaselinePath == "" {
		report.UnscopedSelections++
		return
	}
	report.ScopedSelections++
	if event.SelectedPath == event.DeclaredBaselinePath {
		report.SameAsDeclared++
		return
	}
	report.ChangedFromDeclared++
	if event.DeclaredBaselinePath == model.PathProxy && event.SelectedPath == model.PathDirect {
		report.DirectInsteadOfProxy++
		return
	}
	if event.DeclaredBaselinePath == model.PathDirect && event.SelectedPath == model.PathProxy {
		report.ProxyInsteadOfDirect++
	}
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

func consumeRelay(report *RelayReport, event storedEvent, durations *[]int64) error {
	report.Outcomes++
	switch event.Termination {
	case "ended":
		report.Ended++
	case "canceled":
		report.Canceled++
	}
	clientToRemote := *event.ClientToRemoteBytes
	remoteToClient := *event.RemoteToClientBytes
	if remoteToClient > 0 {
		report.WithRemoteToClientBytes++
	}
	var err error
	if report.ClientToRemoteBytes, err = addInt64(report.ClientToRemoteBytes, clientToRemote); err != nil {
		return err
	}
	if report.RemoteToClientBytes, err = addInt64(report.RemoteToClientBytes, remoteToClient); err != nil {
		return err
	}
	path := &report.Direct
	if event.SelectedPath == model.PathProxy {
		path = &report.Proxy
	}
	path.Connections++
	if path.ClientToRemoteBytes, err = addInt64(path.ClientToRemoteBytes, clientToRemote); err != nil {
		return err
	}
	if path.RemoteToClientBytes, err = addInt64(path.RemoteToClientBytes, remoteToClient); err != nil {
		return err
	}
	*durations = append(*durations, *event.RelayDurationMS)
	consumeRelayEnd(&report.ClientToRemoteEnd, event.ClientToRemoteEnd)
	consumeRelayEnd(&report.RemoteToClientEnd, event.RemoteToClientEnd)
	return nil
}

func consumeRelayEnd(report *RelayEndReport, value string) {
	switch netrelay.EndReason(value) {
	case netrelay.EndEOF:
		report.EOF++
	case netrelay.EndTimeout:
		report.Timeout++
	case netrelay.EndReset:
		report.Reset++
	case netrelay.EndClosed:
		report.Closed++
	case netrelay.EndIOError:
		report.IOError++
	case netrelay.EndCanceled:
		report.Canceled++
	default:
		report.Unclassified++
	}
}

func consumeBaselineRelay(report *DeclaredBaselineReport, event storedEvent) error {
	if event.DeclaredBaselinePath == "" {
		report.UnscopedRelayOutcomes++
		return nil
	}
	report.ScopedRelayOutcomes++
	if event.SelectedPath == event.DeclaredBaselinePath {
		return nil
	}
	report.ChangedRelayOutcomes++
	var err error
	if report.ChangedClientToRemoteBytes, err = addInt64(report.ChangedClientToRemoteBytes, *event.ClientToRemoteBytes); err != nil {
		return err
	}
	if report.ChangedRemoteToClientBytes, err = addInt64(report.ChangedRemoteToClientBytes, *event.RemoteToClientBytes); err != nil {
		return err
	}
	return nil
}

func addInt64(current, value int64) (int64, error) {
	if value < 0 || current > maxInt64-value {
		return 0, errors.New("relay byte aggregate exceeds int64")
	}
	return current + value, nil
}

type connectionPairer struct {
	report    *ConnectionScopeReport
	terminals map[string]storedEvent
	outcomes  map[string]storedEvent
}

func newConnectionPairer(report *ConnectionScopeReport) *connectionPairer {
	return &connectionPairer{
		report: report, terminals: make(map[string]storedEvent), outcomes: make(map[string]storedEvent),
	}
}

func (p *connectionPairer) observeTerminal(event storedEvent) error {
	committedDecision := event.EventType == "decision" && event.Committed != nil && *event.Committed
	if event.ConnectionID == "" {
		if event.EventType == "decision" {
			p.report.UnscopedDecisions++
			if committedDecision {
				p.report.UnscopedCommittedDecisions++
			}
		} else {
			p.report.UnscopedDiagnostics++
		}
		return nil
	}
	if _, exists := p.terminals[event.ConnectionID]; exists {
		return errors.New("duplicate terminal event for one connection scope")
	}
	p.terminals[event.ConnectionID] = event
	if event.EventType == "decision" {
		p.report.ScopedDecisions++
		if committedDecision {
			p.report.ScopedCommittedDecisions++
		}
	} else {
		p.report.ScopedDiagnostics++
	}
	return nil
}

func (p *connectionPairer) observeRelay(event storedEvent) error {
	if event.ConnectionID == "" {
		p.report.UnscopedRelayOutcomes++
		return nil
	}
	if _, exists := p.outcomes[event.ConnectionID]; exists {
		return errors.New("duplicate relay outcome for one connection scope")
	}
	p.outcomes[event.ConnectionID] = event
	p.report.ScopedRelayOutcomes++
	return nil
}

func (p *connectionPairer) finalize() error {
	for id, outcome := range p.outcomes {
		terminal, exists := p.terminals[id]
		if !exists {
			p.report.UnmatchedRelayOutcomes++
			continue
		}
		if terminal.EventType != "decision" || terminal.Committed == nil || !*terminal.Committed {
			return errors.New("scoped relay outcome is paired with a non-committed terminal event")
		}
		if terminal.SelectedPath != outcome.SelectedPath || terminal.DeclaredBaselinePath != outcome.DeclaredBaselinePath || targetScopeKey(*terminal.Target) != targetScopeKey(*outcome.Target) {
			return errors.New("scoped decision and relay outcome disagree on target, selected path, or declared baseline")
		}
		p.report.PairedRelayOutcomes++
	}
	for id, terminal := range p.terminals {
		if terminal.EventType != "decision" || terminal.Committed == nil || !*terminal.Committed {
			continue
		}
		if _, exists := p.outcomes[id]; !exists {
			p.report.CommittedDecisionsWithoutOutcome++
		}
	}
	return nil
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
