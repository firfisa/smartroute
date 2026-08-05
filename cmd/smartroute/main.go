package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/firfisa/smartroute/internal/config"
	"github.com/firfisa/smartroute/internal/decision"
	"github.com/firfisa/smartroute/internal/fixedpolicy"
	"github.com/firfisa/smartroute/internal/guard"
	"github.com/firfisa/smartroute/internal/health"
	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/observe"
	"github.com/firfisa/smartroute/internal/privacy"
	"github.com/firfisa/smartroute/internal/runtimecheck"
	"github.com/firfisa/smartroute/internal/sidecar"
	"github.com/firfisa/smartroute/internal/store"
	"github.com/firfisa/smartroute/internal/supervisor"
	"github.com/firfisa/smartroute/internal/transport"
	"github.com/firfisa/smartroute/internal/trial"
)

var (
	version = "0.1.0-dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("missing command")
	}

	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "smartroute %s commit=%s built=%s\n", version, commit, date)
		return nil
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "trace":
		return runTrace(args[1:], stdout, stderr)
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "guard":
		return runGuard(args[1:], stdout, stderr)
	case "supervise":
		return runSupervise(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "observations":
		return runObservations(args[1:], stdout, stderr)
	case "learning":
		return runLearning(args[1:], stdout, stderr)
	case "policy":
		return runPolicy(args[1:], stdout, stderr)
	case "trial":
		return runTrial(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runDoctor(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	phaseValue := flags.String("phase", "", "local topology phase: baseline, armed, or running")
	timeout := flags.Duration("timeout", 250*time.Millisecond, "per-listener local SOCKS probe timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	phase, err := runtimecheck.ParsePhase(*phaseValue)
	if err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	report, err := runtimecheck.CheckTopology(context.Background(), cfg, phase, *timeout)
	if err != nil {
		return err
	}
	if err := encodeIndentedJSON(stdout, report); err != nil {
		return err
	}
	if !report.Passed {
		return errors.New("local topology doctor failed; inspect the JSON checks")
	}
	return nil
}

func runPolicy(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("policy requires list, lock, or revoke")
	}
	switch args[0] {
	case "list":
		return runPolicyList(args[1:], stdout, stderr)
	case "lock":
		return runPolicyLock(args[1:], stdout, stderr)
	case "revoke":
		return runPolicyRevoke(args[1:], stdout, stderr)
	default:
		return errors.New("policy requires list, lock, or revoke")
	}
}

func runPolicyList(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("policy list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	includeInactive := flags.Bool("all", false, "include expired and revoked policies")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	result, err := fixedpolicy.ListReadOnly(context.Background(), fixedpolicy.Config{Path: cfg.FixedPolicy.DatabasePath}, *includeInactive, time.Now().UTC())
	if err != nil {
		return err
	}
	return encodeIndentedJSON(stdout, result)
}

func runPolicyLock(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("policy lock", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	profileID := flags.String("network-profile", "", "exact local network profile scope")
	hostname := flags.String("hostname", "", "exact cleartext hostname or IP")
	port := flags.Int("port", 0, "exact destination port")
	transportValue := flags.String("transport", "tcp", "transport; Phase 0 accepts tcp only")
	pathValue := flags.String("path", "", "locked route: direct or proxy")
	expiresIn := flags.Duration("expires-in", 0, "optional positive TTL; zero creates a permanent manual lock")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *port < 1 || *port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if *expiresIn < 0 {
		return errors.New("expires-in must not be negative")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var expiresAt *time.Time
	if *expiresIn > 0 {
		value := now.Add(*expiresIn)
		expiresAt = &value
	}
	store, err := fixedpolicy.Open(context.Background(), fixedpolicy.Config{Path: cfg.FixedPolicy.DatabasePath})
	if err != nil {
		return err
	}
	defer store.Close()
	result, err := store.Lock(context.Background(), fixedpolicy.LockRequest{
		Target: model.Target{NetworkProfileID: *profileID, Hostname: *hostname, Port: uint16(*port), Transport: model.Transport(*transportValue)},
		Path:   model.Path(*pathValue), CreatedAt: now, ExpiresAt: expiresAt,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(stderr, "manual fixed policy stored locally in cleartext; runtime activation is not implemented")
	return encodeIndentedJSON(stdout, result)
}

func runPolicyRevoke(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("policy revoke", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	ruleID := flags.String("id", "", "exact policy-... rule ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfg.FixedPolicy.DatabasePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fixedpolicy.ErrNotFound
		}
		return fmt.Errorf("inspect fixed-policy database: %w", err)
	}
	store, err := fixedpolicy.Open(context.Background(), fixedpolicy.Config{Path: cfg.FixedPolicy.DatabasePath})
	if err != nil {
		return err
	}
	defer store.Close()
	rule, err := store.Revoke(context.Background(), *ruleID, time.Now().UTC())
	if err != nil {
		return err
	}
	return encodeIndentedJSON(stdout, rule)
}

func encodeIndentedJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func runTrial(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("trial requires preflight or assess")
	}
	switch args[0] {
	case "preflight":
		return runTrialPreflight(args[1:], stdout, stderr)
	case "assess":
		return runTrialAssess(args[1:], stdout, stderr)
	default:
		return errors.New("trial requires preflight or assess")
	}
}

func runTrialPreflight(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("trial preflight", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	testLabReport := flags.String("testlab-report", "", "path to a fresh smartroute-testlab JSON report")
	mihomoLabReport := flags.String("mihomo-lab-report", "", "path to a fresh smartroute-mihomo-lab JSON report")
	learningBackup := flags.String("learning-backup", "", "verified backup directory for an existing durable store")
	maxEvidenceAge := flags.Duration("max-evidence-age", trial.DefaultMaxEvidenceAge, "maximum accepted age for isolated-lab reports")
	trialSession := flags.String("trial-session", "", "preallocated trial session ID; generated when omitted")
	assessmentWindow := flags.Duration("assessment-window", 168*time.Hour, "pre-registered observation assessment window")
	assessmentDefaults := trial.DefaultAssessmentThresholds()
	minSelections := flags.Int("min-committed-selections", assessmentDefaults.MinCommittedSelections, "pre-registered minimum committed adaptive selections")
	minConnectionScope := flags.Float64("min-connection-scope-ratio", assessmentDefaults.MinConnectionScopeRatio, "pre-registered minimum committed connection scope coverage")
	minBaselineScope := flags.Float64("min-baseline-scope-ratio", assessmentDefaults.MinBaselineScopeRatio, "pre-registered minimum declared baseline scope coverage")
	minPairing := flags.Float64("min-pair-completeness-ratio", assessmentDefaults.MinPairCompletenessRatio, "pre-registered minimum terminal-to-relay pair completeness")
	maxCancellation := flags.Float64("max-cancellation-ratio", assessmentDefaults.MaxCancellationRatio, "pre-registered maximum relay lifecycle cancellation ratio")
	acknowledgeDirectProbes := flags.Bool("acknowledge-direct-probes", false, "acknowledge Direct candidates in explicit-opt-in privacy mode")
	acknowledgeOriginalBaseline := flags.Bool("acknowledge-original-baseline", false, "confirm original_fallback matches the planned original-policy listener")
	acknowledgeCleartext := flags.Bool("acknowledge-cleartext-hostnames", false, "acknowledge cleartext hostname observation")
	acknowledgeEphemeralAuto := flags.Bool("acknowledge-ephemeral-auto", false, "acknowledge experimental ephemeral automatic routing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *maxEvidenceAge <= 0 {
		return errors.New("max-evidence-age must be positive")
	}
	if *assessmentWindow <= 0 || *assessmentWindow%time.Second != 0 {
		return errors.New("assessment-window must be a positive whole number of seconds")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	sessionID := *trialSession
	if sessionID == "" {
		sessionID, err = observe.NewTrialSessionID()
		if err != nil {
			return err
		}
	}
	thresholds := trial.AssessmentThresholds{
		MinCommittedSelections: *minSelections, MinConnectionScopeRatio: *minConnectionScope,
		MinBaselineScopeRatio: *minBaselineScope, MinPairCompletenessRatio: *minPairing,
		MaxCancellationRatio: *maxCancellation,
	}
	report := trial.Preflight(context.Background(), trial.Options{
		Config: cfg, TestLabReportPath: *testLabReport, MihomoLabReportPath: *mihomoLabReport,
		LearningBackupPath: *learningBackup, MaxEvidenceAge: *maxEvidenceAge,
		TrialSessionID: sessionID, AssessmentWindow: *assessmentWindow, AssessmentThresholds: thresholds,
		AcknowledgeDirectProbes:      *acknowledgeDirectProbes,
		AcknowledgeOriginalBaseline:  *acknowledgeOriginalBaseline,
		AcknowledgeCleartextHostname: *acknowledgeCleartext,
		AcknowledgeEphemeralAuto:     *acknowledgeEphemeralAuto,
	})
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return err
	}
	if !report.Ready {
		return errors.New("trial preflight failed; inspect the JSON checks")
	}
	return nil
}

func runTrialAssess(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("trial assess", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	preflightReport := flags.String("preflight-report", "", "path to the successful preflight JSON report that fixed the assessment plan")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if !cfg.Observation.Enabled {
		return errors.New("trial assessment requires observation.enabled=true")
	}
	plan, err := trial.LoadAssessmentPlan(*preflightReport, cfg)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	since := now.Add(-plan.Window())
	if plan.NotBefore.After(since) {
		since = plan.NotBefore
	}
	observationReport, err := observe.BuildReport(cfg.Observation.Directory, observe.ReportOptions{
		Since: since, ExpectedTrialSessionID: plan.TrialSessionID, Clock: func() time.Time { return now },
	})
	if err != nil {
		return fmt.Errorf("build observation report for trial assessment: %w", err)
	}
	assessment, err := trial.AssessObservations(observationReport, plan.Thresholds, func() time.Time { return now })
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(assessment); err != nil {
		return err
	}
	if !assessment.ReadyForDescriptiveAnalysis {
		return errors.New("trial assessment failed; inspect the JSON checks")
	}
	return nil
}

func runSupervise(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("supervise", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	profileID := flags.String("network-profile", "manual-experimental", "local network profile label for child events")
	allowDirectProbes := flags.Bool("acknowledge-direct-probes", false, "acknowledge Direct candidates in explicit-opt-in privacy mode")
	trialSession := flags.String("trial-session", "", "random trial-... observation session; generated when recording is enabled")
	minBackoff := flags.Duration("restart-min-backoff", 100*time.Millisecond, "minimum child restart backoff")
	maxBackoff := flags.Duration("restart-max-backoff", 5*time.Second, "maximum child restart backoff")
	stableAfter := flags.Duration("restart-stable-after", 30*time.Second, "runtime that resets consecutive-failure backoff")
	shutdownGrace := flags.Duration("shutdown-grace", 2*time.Second, "grace period before a child is killed")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *profileID == "" {
		return errors.New("network-profile must not be empty")
	}
	if err := validateSupervisorDurations(*minBackoff, *maxBackoff, *stableAfter, *shutdownGrace); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if err := validateDirectProbeAcknowledgement(cfg.Privacy.Mode, *allowDirectProbes); err != nil {
		return err
	}
	trialSessionID, err := resolveRuntimeTrialSession(cfg, *trialSession)
	if err != nil {
		return err
	}
	recorder, err := openObservationRecorder(cfg, observe.SourceSupervisor, trialSessionID)
	if err != nil {
		return err
	}
	if recorder != nil {
		defer recorder.Close()
	}
	absoluteConfig, err := filepath.Abs(*path)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve SmartRoute executable: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	stdoutWriter := &synchronizedWriter{writer: stdout}
	stderrWriter := &synchronizedWriter{writer: stderr}
	events := newRuntimeEventSink(stdoutWriter, stderrWriter, recorder)
	monitor := supervisor.Supervisor{
		Services: supervisedServices(executable, absoluteConfig, *profileID, *allowDirectProbes, trialSessionID),
		Starter: supervisor.CommandStarter{
			Stdout: stdoutWriter, Stderr: stderrWriter, ShutdownGrace: *shutdownGrace,
		},
		MinBackoff: *minBackoff, MaxBackoff: *maxBackoff, StableAfter: *stableAfter,
		OnEvent: func(event supervisor.Event) {
			events.Emit(event, observe.Event{
				EventType: event.EventType, Service: event.Service, State: event.State,
				Attempt: event.Attempt, FailureClass: event.FailureClass, BackoffMS: event.BackoffMS,
			})
		},
	}
	fmt.Fprintln(stderrWriter, "supervising separate adaptive-engine and guard child processes; no Clash files are read or modified")
	return monitor.Run(ctx)
}

func validateSupervisorDurations(minimum, maximum, stableAfter, shutdownGrace time.Duration) error {
	if minimum < 0 || maximum < 0 || stableAfter < 0 || shutdownGrace < 0 {
		return errors.New("supervisor durations must not be negative")
	}
	if minimum > 0 && maximum > 0 && maximum < minimum {
		return errors.New("restart-max-backoff must not be less than restart-min-backoff")
	}
	return nil
}

func supervisedServices(executable, configPath, profileID string, acknowledgeDirect bool, trialSessionID string) []supervisor.Service {
	serveArgs := []string{"serve", "-config", configPath, "-network-profile", profileID}
	guardArgs := []string{"guard", "-config", configPath, "-network-profile", profileID}
	if acknowledgeDirect {
		serveArgs = append(serveArgs, "-acknowledge-direct-probes")
	}
	if trialSessionID != "" {
		serveArgs = append(serveArgs, "-trial-session", trialSessionID)
		guardArgs = append(guardArgs, "-trial-session", trialSessionID)
	}
	return []supervisor.Service{
		{Name: "adaptive_engine", Executable: executable, Args: serveArgs},
		{Name: "availability_guard", Executable: executable, Args: guardArgs},
	}
}

type synchronizedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *synchronizedWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(value)
}

func runGuard(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("guard", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	profileID := flags.String("network-profile", "manual-experimental", "local network profile label for guard events")
	trialSession := flags.String("trial-session", "", "random trial-... observation session; generated when recording is enabled")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *profileID == "" {
		return errors.New("network-profile must not be empty")
	}

	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	trialSessionID, err := resolveRuntimeTrialSession(cfg, *trialSession)
	if err != nil {
		return err
	}
	recorder, err := openObservationRecorder(cfg, observe.SourceGuard, trialSessionID)
	if err != nil {
		return err
	}
	if recorder != nil {
		defer recorder.Close()
	}
	listener, err := net.Listen("tcp", cfg.GuardListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.GuardListenAddress, err)
	}
	defer listener.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var outputMu sync.Mutex
	events := newRuntimeEventSink(&synchronizedWriter{writer: stdout}, &synchronizedWriter{writer: stderr}, recorder)
	server := guard.Server{
		Adaptive:         guard.SOCKS5Dialer{Endpoint: cfg.ListenAddress},
		Original:         guard.SOCKS5Dialer{Endpoint: cfg.OriginalEndpoint},
		NetworkProfileID: *profileID,
		AdaptiveTimeout:  cfg.GuardAdaptiveTimeout(),
		HandshakeTimeout: cfg.CandidateTimeout(),
		OnDecision: func(event guard.DecisionEvent) {
			outputMu.Lock()
			defer outputMu.Unlock()
			committed := event.Committed
			events.Emit(event, observe.Event{
				EventType: event.EventType, Target: &event.Target, SelectedLane: event.SelectedLane,
				ReasonCode: event.ReasonCode, AdaptiveFailure: event.AdaptiveFailure,
				OriginalFailure: event.OriginalFailure, Committed: &committed,
			})
		},
	}
	fmt.Fprintf(stderr, "availability guard listening on %s; adaptive=%s original=%s; no Clash files are read or modified\n", listener.Addr(), cfg.ListenAddress, cfg.OriginalEndpoint)
	return server.Serve(ctx, listener)
}

func runServe(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	profileID := flags.String("network-profile", "manual-experimental", "local network profile label for decision events")
	allowDirectProbes := flags.Bool("acknowledge-direct-probes", false, "acknowledge Direct candidates in explicit-opt-in privacy mode")
	trialSession := flags.String("trial-session", "", "random trial-... observation session; generated when recording is enabled")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *profileID == "" {
		return errors.New("network-profile must not be empty")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	privacyPolicy, err := privacy.New(cfg.Privacy.Mode, cfg.Privacy.NeverDirectProbe)
	if err != nil {
		return fmt.Errorf("compile privacy policy: %w", err)
	}
	if err := validateDirectProbeAcknowledgement(cfg.Privacy.Mode, *allowDirectProbes); err != nil {
		return err
	}
	trialSessionID, err := resolveRuntimeTrialSession(cfg, *trialSession)
	if err != nil {
		return err
	}
	var ephemeralLearner *learning.Engine
	if !learning.UsesAutomaticPolicy(cfg.Learning.Mode) {
		ephemeralLearner, err = learning.New(learning.Config{
			Mode:                cfg.Learning.Mode,
			MaxEntries:          cfg.Learning.MaxEntries,
			DirectPromotionWins: cfg.Learning.DirectPromotionWins,
			ProxyPromotionWins:  cfg.Learning.ProxyPromotionWins,
			TTL:                 time.Duration(cfg.Learning.PolicyTTLHours) * time.Hour,
		})
		if err != nil {
			return fmt.Errorf("initialize legacy learning engine: %w", err)
		}
	}
	recorder, err := openObservationRecorder(cfg, observe.SourceEngine, trialSessionID)
	if err != nil {
		return err
	}
	if recorder != nil {
		defer recorder.Close()
	}
	var outputMu sync.Mutex
	stdoutWriter := &synchronizedWriter{writer: stdout}
	stderrWriter := &synchronizedWriter{writer: stderr}
	events := newRuntimeEventSink(stdoutWriter, stderrWriter, recorder)
	var healthGate *health.Gate
	if cfg.Learning.Health.Enabled && !learning.UsesAutomaticPolicy(cfg.Learning.Mode) {
		healthGate, err = health.New(health.Config{
			FailureThreshold:  cfg.Learning.Health.FailureThreshold,
			RecoveryThreshold: cfg.Learning.Health.RecoveryThreshold,
			FailureWindow:     cfg.LearningHealthFailureWindow(),
			FreezeDuration:    cfg.LearningHealthFreezeDuration(),
		})
		if err != nil {
			return fmt.Errorf("initialize learning health gate: %w", err)
		}
	}
	var durableWarnOnce sync.Once
	durable, err := openDurableLearning(context.Background(), cfg, func(error) {
		durableWarnOnce.Do(func() {
			fmt.Fprintln(stderrWriter, "durable learning write or assessment failed; routing and process-local learning continue")
		})
	}, func(event learning.DurableAssessmentEvent) {
		outputMu.Lock()
		defer outputMu.Unlock()
		assessment := event.Assessment
		events.Emit(event, observe.Event{
			EventType: event.EventType, Target: &event.Target,
			ReasonCode: assessment.ReasonCode, DurableAssessment: &assessment,
		})
	})
	if err != nil {
		return err
	}
	if durable != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.LearningShutdownTimeout())
			defer cancel()
			stats, closeErr := durable.Close(shutdownCtx)
			fmt.Fprintf(stderrWriter, "durable learning stopped: queued=%d written=%d skipped=%d dropped=%d errors=%d\n",
				stats.Queued, stats.Written, stats.Skipped, stats.Dropped, stats.Errors)
			if closeErr != nil {
				fmt.Fprintln(stderrWriter, "durable learning shutdown incomplete; process exit remains safe")
			}
		}()
	}
	learner := &runtimeLearningEngine{
		ephemeral: ephemeralLearner,
		automatic: learning.UsesAutomaticPolicy(cfg.Learning.Mode),
		health:    healthGate,
	}
	learner.onHealth = func(event health.Event) {
		outputMu.Lock()
		defer outputMu.Unlock()
		var frozenUntil *time.Time
		if !event.FrozenUntil.IsZero() {
			value := event.FrozenUntil
			frozenUntil = &value
		}
		events.Emit(event, observe.Event{EventType: event.EventType, Target: event.Target,
			ReasonCode: event.ReasonCode, State: event.State, Trigger: event.Trigger,
			FrozenUntil: frozenUntil, FailureTargets: event.FailureTargets, RecoveryTargets: event.RecoveryTargets})
	}
	learner.onError = func(err error) {
		fmt.Fprintf(stderrWriter, "learning health signal rejected; routing continues: %v\n", err)
	}
	attachDurableLearning(learner, durable)
	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddress, err)
	}
	defer listener.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := sidecar.Server{
		NetworkProfileID:     *profileID,
		DeclaredBaselinePath: cfg.OriginalFallback,
		DirectProbePolicy:    privacyPolicy,
		Learning:             learner,
		HandshakeTimeout:     cfg.CandidateTimeout(),
		TLSRacer: &transport.TLSRacer{
			Direct:    transport.SOCKS5Dialer{Path: model.PathDirect, Endpoint: cfg.DirectEndpoint},
			Proxy:     transport.SOCKS5Dialer{Path: model.PathProxy, Endpoint: cfg.ProxyEndpoint},
			Gate:      transport.TLSServerHelloGate{},
			HeadStart: cfg.DirectHeadStart(),
			Timeout:   cfg.CandidateTimeout(),
		},
		OnDecision: func(event sidecar.DecisionEvent) {
			outputMu.Lock()
			defer outputMu.Unlock()
			committed := event.Committed
			observation := event.Observation
			events.Emit(event, observe.Event{
				EventType: event.EventType, ConnectionID: event.ConnectionID,
				Target: &event.Target, DeclaredBaselinePath: event.DeclaredBaselinePath, SelectedPath: event.SelectedPath,
				ReasonCode: event.ReasonCode, PolicyReason: event.PolicyReason,
				Observation: &observation, OtherObservation: event.OtherObservation, Committed: &committed,
				LearningReason: event.LearningReason, DurableReason: event.DurableReason, PolicyState: event.PolicyState,
				DecisionLatencyMS: event.DecisionLatencyMS,
			})
		},
		OnDiagnostic: func(event sidecar.DiagnosticEvent) {
			outputMu.Lock()
			defer outputMu.Unlock()
			events.Emit(event, observe.Event{
				EventType: event.EventType, ConnectionID: event.ConnectionID,
				Target: &event.Target, DeclaredBaselinePath: event.DeclaredBaselinePath, ReasonCode: event.ReasonCode,
				PolicyReason: event.PolicyReason, FailureClass: event.FailureClass,
				DirectFailure: event.DirectFailure, ProxyFailure: event.ProxyFailure,
			})
		},
		OnRelayOutcome: func(event sidecar.RelayOutcomeEvent) {
			outputMu.Lock()
			defer outputMu.Unlock()
			clientToRemote := event.ClientToRemoteBytes
			remoteToClient := event.RemoteToClientBytes
			duration := event.RelayDurationMS
			events.Emit(event, observe.Event{
				EventType: event.EventType, ConnectionID: event.ConnectionID,
				Target: &event.Target, DeclaredBaselinePath: event.DeclaredBaselinePath, SelectedPath: event.SelectedPath,
				ClientToRemoteBytes: &clientToRemote, RemoteToClientBytes: &remoteToClient,
				ClientToRemoteEnd: string(event.ClientToRemoteEnd), RemoteToClientEnd: string(event.RemoteToClientEnd),
				RelayDurationMS: &duration, Termination: event.Termination,
			})
		},
	}
	fmt.Fprintf(stderr, "experimental TLS sidecar listening on %s; privacy=%s; declared_original=%s; no Clash files are read or modified\n", listener.Addr(), cfg.Privacy.Mode, cfg.OriginalFallback)
	return server.Serve(ctx, listener)
}

type durableEvidenceWriter interface {
	Enqueue(store.WriteRequest) (bool, string)
}

type durablePolicyWriter interface {
	Enqueue(store.PolicyWriteRequest) (bool, string)
}

type runtimeLearningEngine struct {
	mu           sync.Mutex
	ephemeral    *learning.Engine
	automatic    bool
	durable      *store.DurablePolicyIndex
	writer       durableEvidenceWriter
	policyWriter durablePolicyWriter
	clock        func() time.Time
	health       *health.Gate
	onHealth     func(health.Event)
	onError      func(error)
}

func (e *runtimeLearningEngine) FixedPath(target model.Target) model.Path {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.durable == nil {
		return ""
	}
	if e.health != nil {
		transition := e.health.Check()
		e.handleHealthTransition(nil, transition)
		if transition.Snapshot.State == health.StateFrozen {
			return ""
		}
	}
	return e.durable.PreferredPath(target)
}

func (e *runtimeLearningEngine) PreferredPath(target model.Target) model.Path {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ephemeral == nil {
		return ""
	}
	if e.health != nil {
		transition := e.health.Check()
		e.handleHealthTransition(nil, transition)
		if transition.Snapshot.State == health.StateFrozen {
			return ""
		}
	}
	return e.ephemeral.PreferredPath(target)
}

func (e *runtimeLearningEngine) Observe(target model.Target, winner model.Observation, other *model.Observation) (learning.Update, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	direction, nonAppliedReason, err := learning.ClassifyStrongPair(winner, other)
	if err != nil {
		return learning.Update{}, err
	}
	if e.health != nil {
		transition, healthErr := e.health.ObservePathSucceeded(target, winner.Path)
		if healthErr != nil {
			return learning.Update{}, healthErr
		}
		e.handleHealthTransition(&target, transition)
		if direction == model.PathDirect {
			transition, healthErr = e.health.ObserveProxyPathFailed(target)
			if healthErr != nil {
				return learning.Update{}, healthErr
			}
			e.handleHealthTransition(&target, transition)
		}
		if e.health.Snapshot().State == health.StateFrozen {
			return learning.Update{ReasonCode: learning.ReasonHealthFrozen}, nil
		}
	}
	now := time.Now
	if e.clock != nil {
		now = e.clock
	}
	observedAt := now().UTC()
	policyChanged := false
	if e.durable != nil {
		change, err := e.durable.Remember(target, winner.Path, observedAt)
		if err != nil {
			return learning.Update{}, err
		}
		policyChanged = change.Applied
	}
	update := learning.Update{ReasonCode: nonAppliedReason}
	if e.ephemeral != nil && direction != "" {
		update, err = e.ephemeral.Observe(target, winner, other)
		if err != nil {
			return update, err
		}
	}
	if e.automatic && e.durable != nil {
		state := model.StateDirectPreferred
		reason := learning.ReasonAutoDirectRemembered
		if winner.Path == model.PathProxy {
			state = model.StateProxyPreferred
			reason = learning.ReasonAutoProxyRemembered
		}
		if !policyChanged {
			reason = learning.ReasonAutoPathUnchanged
		}
		update.Applied = policyChanged
		update.Policy = learning.Policy{State: state, PreferredPath: winner.Path, UpdatedAt: observedAt}
		update.ReasonCode = reason
	}
	if e.writer != nil && direction != "" {
		_, update.DurableReason = e.writer.Enqueue(store.WriteRequest{
			Target: target, Winner: winner, Other: other, ObservedAt: observedAt,
		})
	}
	if e.policyWriter != nil && policyChanged {
		_, update.DurableReason = e.policyWriter.Enqueue(store.PolicyWriteRequest{
			Target: target, Path: winner.Path, ObservedAt: observedAt,
		})
	}
	return update, nil
}

func (e *runtimeLearningEngine) ObserveBothPathsFailed(target model.Target) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.health == nil {
		return
	}
	transition, err := e.health.ObserveBothPathsFailed(target)
	if err != nil {
		e.handleHealthError(err)
		return
	}
	e.handleHealthTransition(&target, transition)
}

func (e *runtimeLearningEngine) ObserveProxyPathFailed(target model.Target) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.health == nil {
		return
	}
	transition, err := e.health.ObserveProxyPathFailed(target)
	if err != nil {
		e.handleHealthError(err)
		return
	}
	e.handleHealthTransition(&target, transition)
}

func (e *runtimeLearningEngine) ObservePathSucceeded(target model.Target, path model.Path) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.health == nil {
		return
	}
	transition, err := e.health.ObservePathSucceeded(target, path)
	if err != nil {
		e.handleHealthError(err)
		return
	}
	e.handleHealthTransition(&target, transition)
}

func (e *runtimeLearningEngine) handleHealthTransition(target *model.Target, transition health.Transition) {
	if !transition.Changed {
		return
	}
	if transition.Snapshot.State == health.StateFrozen {
		if e.ephemeral != nil {
			e.ephemeral.Clear()
		}
	}
	if e.onHealth != nil {
		event := health.NewEvent(target, transition)
		e.onHealth(event)
	}
}

func (e *runtimeLearningEngine) handleHealthError(err error) {
	if e.onError != nil {
		e.onError(err)
	}
}

type durableLearningRuntime struct {
	store           *store.Store
	writer          *store.AsyncWriter
	policyWriter    *store.AsyncPolicyWriter
	index           *store.DurablePolicyIndex
	maxEvidenceRows int
}

func attachDurableLearning(learner *runtimeLearningEngine, durable *durableLearningRuntime) {
	if learner == nil || durable == nil {
		return
	}
	if durable.writer != nil {
		learner.writer = durable.writer
	}
	if durable.policyWriter != nil {
		learner.policyWriter = durable.policyWriter
	}
	learner.durable = durable.index
}

func openDurableLearning(ctx context.Context, cfg config.Config, onError func(error), onAssessment func(learning.DurableAssessmentEvent)) (*durableLearningRuntime, error) {
	if !cfg.Learning.Persistence.Enabled {
		return nil, nil
	}
	evidenceStore, err := store.Open(ctx, store.Config{
		Path: cfg.Learning.Persistence.DatabasePath, BusyTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize durable learning store: %w", err)
	}
	closeOnError := func(resultErr error) (*durableLearningRuntime, error) {
		return nil, errors.Join(resultErr, evidenceStore.Close())
	}
	if learning.UsesAutomaticPolicy(cfg.Learning.Mode) {
		policyIndex, err := evidenceStore.NewDurablePolicyIndex(ctx, cfg.Learning.MaxEntries)
		if err != nil {
			return closeOnError(fmt.Errorf("load durable policy index: %w", err))
		}
		policyWriter, err := store.NewAsyncPolicyWriter(
			evidenceStore, cfg.Learning.MaxEntries, cfg.Learning.Persistence.QueueSize, onError,
		)
		if err != nil {
			return closeOnError(fmt.Errorf("initialize durable policy writer: %w", err))
		}
		return &durableLearningRuntime{store: evidenceStore, policyWriter: policyWriter, index: policyIndex}, nil
	}
	evaluator, err := durableEvaluatorFromConfig(cfg)
	if err != nil {
		return closeOnError(fmt.Errorf("initialize durable learning evaluator: %w", err))
	}
	sessionID, err := newLearningSessionID()
	if err != nil {
		return closeOnError(err)
	}
	now := time.Now().UTC()
	if _, err := evidenceStore.PruneEvidence(ctx, now.Add(-cfg.LearningEvidenceRetention())); err != nil {
		return closeOnError(fmt.Errorf("prune durable learning evidence: %w", err))
	}
	if _, err := evidenceStore.TrimEvidenceTo(ctx, cfg.Learning.Persistence.MaxEvidenceRows); err != nil {
		return closeOnError(fmt.Errorf("trim durable learning evidence: %w", err))
	}
	if err := evidenceStore.StartSession(ctx, sessionID, now); err != nil {
		return closeOnError(fmt.Errorf("start durable learning session: %w", err))
	}
	writerOptions := store.WriterOptions{Capacity: cfg.Learning.Persistence.QueueSize, OnError: onError}
	writesSinceTrim := 0
	if onAssessment != nil {
		writerOptions.OnWritten = func(request store.WriteRequest) error {
			summary, err := evidenceStore.Summarize(context.Background(), request.Target, request.ObservedAt.Add(-cfg.LearningEvidenceRetention()))
			if err != nil {
				return fmt.Errorf("summarize durable learning evidence: %w", err)
			}
			assessment, err := evaluator.Evaluate(durableSummary(summary))
			if err != nil {
				return fmt.Errorf("evaluate durable learning evidence: %w", err)
			}
			onAssessment(learning.DurableAssessmentEvent{
				EventType: learning.EventTypeDurableAssessment,
				Target:    request.Target, Assessment: assessment,
			})
			return nil
		}
	}
	writerOptions.OnProcessed = func(request store.WriteRequest, _ bool) error {
		writesSinceTrim++
		if writesSinceTrim >= 256 {
			if _, err := evidenceStore.TrimEvidenceTo(context.Background(), cfg.Learning.Persistence.MaxEvidenceRows); err != nil {
				return fmt.Errorf("enforce durable evidence capacity: %w", err)
			}
			writesSinceTrim = 0
		}
		return nil
	}
	writer, err := store.NewAsyncWriterWithOptions(evidenceStore, sessionID, writerOptions)
	if err != nil {
		return closeOnError(fmt.Errorf("initialize durable learning writer: %w", err))
	}
	return &durableLearningRuntime{store: evidenceStore, writer: writer, maxEvidenceRows: cfg.Learning.Persistence.MaxEvidenceRows}, nil
}

func (r *durableLearningRuntime) Close(ctx context.Context) (store.WriterStats, error) {
	if r == nil {
		return store.WriterStats{}, nil
	}
	if r.policyWriter != nil {
		if err := r.policyWriter.Close(ctx); err != nil {
			return r.policyWriter.Stats(), err
		}
		checkpointErr := r.store.Checkpoint(ctx)
		closeErr := r.store.Close()
		return r.policyWriter.Stats(), errors.Join(checkpointErr, closeErr)
	}
	if err := r.writer.Close(ctx); err != nil {
		return r.writer.Stats(), err
	}
	if _, err := r.store.TrimEvidenceTo(ctx, r.maxEvidenceRows); err != nil {
		return r.writer.Stats(), err
	}
	checkpointErr := r.store.Checkpoint(ctx)
	closeErr := r.store.Close()
	return r.writer.Stats(), errors.Join(checkpointErr, closeErr)
}

func newLearningSessionID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate durable learning session ID: %w", err)
	}
	return "session-" + hex.EncodeToString(value), nil
}

type runtimeEventSink struct {
	output   io.Writer
	warnings io.Writer
	recorder *observe.Recorder
	warnOnce sync.Once
}

func newRuntimeEventSink(output, warnings io.Writer, recorder *observe.Recorder) *runtimeEventSink {
	return &runtimeEventSink{output: output, warnings: warnings, recorder: recorder}
}

func (s *runtimeEventSink) Emit(raw any, persistent observe.Event) {
	if s.recorder == nil {
		_ = json.NewEncoder(s.output).Encode(raw)
		return
	}
	if err := s.recorder.Record(persistent); err != nil {
		s.warnOnce.Do(func() {
			fmt.Fprintf(s.warnings, "observation write failed; routing continues: %v\n", err)
		})
	}
}

func openObservationRecorder(cfg config.Config, source, trialSessionID string) (*observe.Recorder, error) {
	if !cfg.Observation.Enabled {
		return nil, nil
	}
	if trialSessionID == "" {
		return nil, errors.New("enabled observation recorder requires a trial session ID")
	}
	recorder, err := observe.New(observe.Options{
		Directory: cfg.Observation.Directory, Source: source,
		MaxFileBytes: cfg.Observation.MaxFileBytes, MaxFiles: cfg.Observation.MaxFilesPerSource,
		Retention:                time.Duration(cfg.Observation.RetentionHours) * time.Hour,
		IncludeCleartextHostname: cfg.Observation.IncludeCleartextHostname,
		TrialSessionID:           trialSessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize observation recorder: %w", err)
	}
	return recorder, nil
}

func resolveRuntimeTrialSession(cfg config.Config, requested string) (string, error) {
	if !cfg.Observation.Enabled {
		if requested != "" {
			return "", errors.New("trial-session requires observation.enabled=true")
		}
		return "", nil
	}
	if requested != "" {
		if err := observe.ValidateTrialSessionID(requested); err != nil {
			return "", err
		}
		return requested, nil
	}
	return observe.NewTrialSessionID()
}

func runObservations(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("observations requires status, report, pause, resume, clear, or export")
	}
	action := args[0]
	flags := flag.NewFlagSet("observations "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	confirmClear := flags.Bool("confirm-clear", false, "confirm deletion of all local observation JSONL files")
	destination := flags.String("destination", "", "new directory for a redacted observation export")
	sinceValue := flags.String("since", "", "RFC3339 lower bound for an identity-free observation report")
	hours := flags.Int("hours", 0, "whole hours to include in an observation report; defaults to configured retention")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	directory := cfg.Observation.Directory
	switch action {
	case "status":
		status, err := observe.Inspect(directory)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(status)
	case "report":
		status, err := observe.Inspect(directory)
		if err != nil {
			return err
		}
		if !status.Paused {
			return errors.New("observations report requires recording to be paused")
		}
		if *sinceValue != "" && *hours != 0 {
			return errors.New("observations report accepts either -since or -hours, not both")
		}
		var since time.Time
		if *sinceValue != "" {
			parsed, err := time.Parse(time.RFC3339, *sinceValue)
			if err != nil {
				return fmt.Errorf("parse observations report since: %w", err)
			}
			since = parsed.UTC()
		} else {
			windowHours := *hours
			if windowHours == 0 {
				windowHours = cfg.Observation.RetentionHours
			}
			if windowHours < 1 || windowHours > 8760 {
				return errors.New("observations report hours must be between 1 and 8760")
			}
			since = time.Now().UTC().Add(-time.Duration(windowHours) * time.Hour)
		}
		report, err := observe.BuildReport(directory, observe.ReportOptions{Since: since})
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	case "pause":
		return observe.Pause(directory)
	case "resume":
		return observe.Resume(directory)
	case "clear":
		if !*confirmClear {
			return errors.New("clear requires -confirm-clear and recording must already be paused")
		}
		return observe.Clear(directory)
	case "export":
		if *destination == "" {
			return errors.New("export requires -destination")
		}
		return observe.Export(directory, *destination)
	default:
		return fmt.Errorf("unknown observations action %q", action)
	}
}

type learningStoreStatus struct {
	ConfiguredEnabled bool               `json:"configured_enabled"`
	DatabasePath      string             `json:"database_path"`
	DatabaseExists    bool               `json:"database_exists"`
	KeyExists         bool               `json:"key_exists"`
	DatabaseBytes     int64              `json:"database_bytes,omitempty"`
	Health            string             `json:"health"`
	Store             *store.StoreStatus `json:"store,omitempty"`
}

type learningBackupResult struct {
	Destination string               `json:"destination"`
	Manifest    store.BackupManifest `json:"manifest"`
}

type learningReportResult struct {
	GeneratedAt    time.Time              `json:"generated_at"`
	Since          time.Time              `json:"since"`
	RetentionHours int                    `json:"retention_hours"`
	Report         learning.DurableReport `json:"report"`
}

type clearDurablePoliciesResult struct {
	ClearedPolicies  int64 `json:"cleared_policies"`
	EvidenceRetained bool  `json:"evidence_retained"`
	RestartRequired  bool  `json:"restart_required"`
}

func runLearning(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("learning requires status, evaluate, report, clear-policies, backup, verify-backup, or restore")
	}
	action := args[0]
	flags := flag.NewFlagSet("learning "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	source := flags.String("source", "", "completed SmartRoute backup directory")
	destination := flags.String("destination", "", "new backup directory or restored database path")
	networkProfile := flags.String("network-profile", "", "exact local network profile label to evaluate")
	hostname := flags.String("hostname", "", "exact target hostname to evaluate locally")
	port := flags.Uint("port", 0, "exact target port to evaluate")
	transportName := flags.String("transport", string(model.TransportTCP), "target transport: tcp or udp")
	confirmClearPolicies := flags.Bool("confirm-clear-policies", false, "confirm clearing automatic policies while retaining evidence")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	switch action {
	case "status":
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		status, err := inspectLearningStore(context.Background(), cfg)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(status)
	case "backup":
		if *destination == "" {
			return errors.New("learning backup requires -destination")
		}
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		evidenceStore, err := openExistingLearningStore(context.Background(), cfg.Learning.Persistence.DatabasePath)
		if err != nil {
			return err
		}
		defer evidenceStore.Close()
		manifest, err := evidenceStore.Backup(context.Background(), *destination)
		if err != nil {
			return err
		}
		absoluteDestination, err := filepath.Abs(*destination)
		if err != nil {
			return fmt.Errorf("resolve completed backup destination: %w", err)
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(learningBackupResult{Destination: absoluteDestination, Manifest: manifest})
	case "evaluate":
		if *networkProfile == "" || *hostname == "" || *port == 0 || *port > 65535 {
			return errors.New("learning evaluate requires -network-profile, -hostname, and -port 1..65535")
		}
		transport := model.Transport(*transportName)
		if !transport.Valid() {
			return errors.New("learning evaluate transport must be tcp or udp")
		}
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		evidenceStore, err := openExistingLearningStore(context.Background(), cfg.Learning.Persistence.DatabasePath)
		if err != nil {
			return err
		}
		defer evidenceStore.Close()
		summary, err := evidenceStore.Summarize(context.Background(), model.Target{
			NetworkProfileID: *networkProfile, Hostname: *hostname,
			Port: uint16(*port), Transport: transport,
		}, time.Now().UTC().Add(-cfg.LearningEvidenceRetention()))
		if err != nil {
			return err
		}
		evaluator, err := durableEvaluatorFromConfig(cfg)
		if err != nil {
			return err
		}
		assessment, err := evaluator.Evaluate(durableSummary(summary))
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(assessment)
	case "report":
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		evidenceStore, err := openExistingLearningStore(context.Background(), cfg.Learning.Persistence.DatabasePath)
		if err != nil {
			return err
		}
		defer evidenceStore.Close()
		generatedAt := time.Now().UTC()
		since := generatedAt.Add(-cfg.LearningEvidenceRetention())
		storedSummaries, err := evidenceStore.ListTargetSummaries(context.Background(), since)
		if err != nil {
			return err
		}
		summaries := make([]learning.DurableEvidenceSummary, 0, len(storedSummaries))
		for _, summary := range storedSummaries {
			summaries = append(summaries, durableSummary(summary))
		}
		evaluator, err := durableEvaluatorFromConfig(cfg)
		if err != nil {
			return err
		}
		report, err := evaluator.Report(summaries)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(learningReportResult{
			GeneratedAt: generatedAt, Since: since,
			RetentionHours: cfg.Learning.Persistence.RetentionHours, Report: report,
		})
	case "clear-policies":
		if !*confirmClearPolicies {
			return errors.New("learning clear-policies requires -confirm-clear-policies")
		}
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		if _, err := os.Stat(cfg.Learning.Persistence.DatabasePath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return errors.New("durable learning database does not exist")
			}
			return fmt.Errorf("inspect durable learning database: %w", err)
		}
		evidenceStore, err := store.Open(context.Background(), store.Config{Path: cfg.Learning.Persistence.DatabasePath, BusyTimeout: 5 * time.Second})
		if err != nil {
			return err
		}
		defer evidenceStore.Close()
		cleared, err := evidenceStore.ClearDurablePolicies(context.Background())
		if err != nil {
			return err
		}
		fmt.Fprintln(stderr, "automatic policies cleared; restart SmartRoute to discard the running in-memory snapshot; evidence was retained")
		return encodeIndentedJSON(stdout, clearDurablePoliciesResult{ClearedPolicies: cleared, EvidenceRetained: true, RestartRequired: true})
	case "verify-backup":
		if *source == "" {
			return errors.New("learning verify-backup requires -source")
		}
		manifest, err := store.VerifyBackup(context.Background(), *source)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(manifest)
	case "restore":
		if *source == "" || *destination == "" {
			return errors.New("learning restore requires -source and -destination")
		}
		result, err := store.RestoreBackup(context.Background(), *source, *destination)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	default:
		return fmt.Errorf("unknown learning action %q", action)
	}
}

func inspectLearningStore(ctx context.Context, cfg config.Config) (learningStoreStatus, error) {
	path := cfg.Learning.Persistence.DatabasePath
	absolute, err := filepath.Abs(path)
	if err != nil {
		return learningStoreStatus{}, fmt.Errorf("resolve learning database path: %w", err)
	}
	result := learningStoreStatus{ConfiguredEnabled: cfg.Learning.Persistence.Enabled, DatabasePath: absolute}
	databaseInfo, databaseErr := os.Stat(path)
	keyInfo, keyErr := os.Stat(path + ".key")
	if databaseErr != nil && !errors.Is(databaseErr, os.ErrNotExist) {
		return result, fmt.Errorf("inspect learning database: %w", databaseErr)
	}
	if keyErr != nil && !errors.Is(keyErr, os.ErrNotExist) {
		return result, fmt.Errorf("inspect learning database key: %w", keyErr)
	}
	result.DatabaseExists = databaseErr == nil
	result.KeyExists = keyErr == nil
	if result.DatabaseExists {
		if !databaseInfo.Mode().IsRegular() {
			return result, errors.New("learning database path must be a regular file")
		}
		result.DatabaseBytes = databaseInfo.Size()
	}
	if result.KeyExists && !keyInfo.Mode().IsRegular() {
		return result, errors.New("learning database key path must be a regular file")
	}
	switch {
	case !result.DatabaseExists && !result.KeyExists:
		result.Health = "absent"
		return result, nil
	case !result.DatabaseExists:
		result.Health = "orphaned_key"
		return result, nil
	case !result.KeyExists:
		result.Health = "missing_key"
		return result, nil
	}
	evidenceStore, err := store.OpenReadOnly(ctx, store.Config{Path: path, BusyTimeout: 5 * time.Second})
	if err != nil {
		return result, fmt.Errorf("inspect durable learning store: %w", err)
	}
	defer evidenceStore.Close()
	storeStatus, err := evidenceStore.Status(ctx)
	if err != nil {
		return result, err
	}
	result.Health = "ok"
	result.Store = &storeStatus
	return result, nil
}

func openExistingLearningStore(ctx context.Context, path string) (*store.Store, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("durable learning database does not exist")
	}
	if err != nil {
		return nil, fmt.Errorf("inspect durable learning database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("durable learning database must be a regular file")
	}
	evidenceStore, err := store.OpenReadOnly(ctx, store.Config{Path: path, BusyTimeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open durable learning database: %w", err)
	}
	return evidenceStore, nil
}

func durableEvaluatorFromConfig(cfg config.Config) (*learning.DurableEvaluator, error) {
	return learning.NewDurableEvaluator(learning.DurableEvaluatorConfig{
		DirectWins: cfg.Learning.DirectPromotionWins, ProxyWins: cfg.Learning.ProxyPromotionWins,
		DirectSessions: cfg.Learning.Persistence.DirectSuggestionSessions,
		ProxySessions:  cfg.Learning.Persistence.ProxySuggestionSessions,
	})
}

func durableSummary(summary store.Summary) learning.DurableEvidenceSummary {
	return learning.DurableEvidenceSummary{
		DirectWins: summary.DirectWins, ProxyWins: summary.ProxyWins,
		DirectSessions: summary.DirectSessions, ProxySessions: summary.ProxySessions,
	}
}

func validateDirectProbeAcknowledgement(mode string, acknowledged bool) error {
	if mode == privacy.ModeExplicitOptIn && !acknowledged {
		return errors.New("serve requires -acknowledge-direct-probes in explicit-opt-in privacy mode")
	}
	return nil
}

func runValidate(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if _, err := config.Load(*path); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "valid: %s\n", *path)
	return nil
}

func runTrace(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("trace", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	directSpec := flags.String("direct", "success:tls:35", "success:stage:latency_ms or failure:stage:latency_ms:class")
	proxySpec := flags.String("proxy", "success:tls:120", "success:stage:latency_ms or failure:stage:latency_ms:class")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	directObservation, err := parseObservation(model.PathDirect, *directSpec)
	if err != nil {
		return fmt.Errorf("direct: %w", err)
	}
	proxyObservation, err := parseObservation(model.PathProxy, *proxySpec)
	if err != nil {
		return fmt.Errorf("proxy: %w", err)
	}

	evaluator := decision.PairEvaluator{
		OriginalFallback: cfg.OriginalFallback,
		MaxDirectPenalty: time.Duration(cfg.Decision.MaxDirectPenaltyMS) * time.Millisecond,
	}
	result, err := evaluator.Evaluate(directObservation, proxyObservation)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func parseObservation(path model.Path, spec string) (model.Observation, error) {
	parts := strings.Split(spec, ":")
	if len(parts) < 3 || len(parts) > 4 {
		return model.Observation{}, errors.New("expected success:stage:latency_ms or failure:stage:latency_ms:class")
	}
	success := parts[0] == "success"
	if !success && parts[0] != "failure" {
		return model.Observation{}, errors.New("status must be success or failure")
	}
	stage, err := model.ParseStage(parts[1])
	if err != nil {
		return model.Observation{}, err
	}
	latencyMS, err := strconv.Atoi(parts[2])
	if err != nil || latencyMS < 0 {
		return model.Observation{}, errors.New("latency_ms must be a non-negative integer")
	}
	failureClass := ""
	if !success {
		if len(parts) != 4 || parts[3] == "" {
			return model.Observation{}, errors.New("failed observation requires a failure class")
		}
		failureClass = parts[3]
	} else if len(parts) == 4 {
		return model.Observation{}, errors.New("successful observation must not include a failure class")
	}

	observation := model.Observation{
		Path: path, Success: success, StageReached: stage,
		Latency: time.Duration(latencyMS) * time.Millisecond, FailureClass: failureClass,
	}
	if err := observation.Validate(); err != nil {
		return model.Observation{}, err
	}
	return observation, nil
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, `SmartRoute experimental CLI

Usage:
  smartroute version
  smartroute validate [-config path]
  smartroute trace [-config path] [-direct spec] [-proxy spec]
  smartroute serve -acknowledge-direct-probes [-config path] [-network-profile label] [-trial-session random-id]
  smartroute guard [-config path] [-network-profile label] [-trial-session random-id]
  smartroute supervise [-acknowledge-direct-probes] [-config path] [-network-profile label] [-trial-session random-id]
  smartroute doctor -phase baseline|armed|running [-config path] [-timeout 250ms]
  smartroute observations status|report|pause|resume|clear|export [-config path]
  smartroute learning status [-config path]
  smartroute learning evaluate -network-profile label -hostname host -port port [-transport tcp|udp] [-config path]
  smartroute learning report [-config path]
  smartroute learning clear-policies -confirm-clear-policies [-config path]
  smartroute learning backup -destination new-directory [-config path]
  smartroute learning verify-backup -source backup-directory
  smartroute learning restore -source backup-directory -destination new-database
  smartroute policy list [--all] [-config path]
  smartroute policy lock -network-profile label -hostname host -port port -path direct|proxy [-expires-in duration] [-config path]
  smartroute policy revoke -id policy-id [-config path]
  smartroute trial preflight -testlab-report report.json -mihomo-lab-report report.json -acknowledge-original-baseline [-assessment-window 168h] [-config path]
  smartroute trial assess -preflight-report preflight.json [-config path]

The trace command evaluates one synthetic paired observation. The experimental
serve command accepts TLS-over-SOCKS on the configured loopback listener and
does not read or modify Clash configuration. Explicit-opt-in privacy mode
requires a Direct-probe acknowledgment; privacy-first opens Proxy only. The guard
command falls back to the configured original SOCKS listener if the adaptive
engine cannot accept a target connection. The supervise command runs Guard and
engine as independently restartable child processes; it does not replay a
connection lost while Guard itself is down. The doctor command checks only the
five configured loopback ports; SOCKS probes stop after method negotiation and
never send a destination CONNECT request.`)
}
