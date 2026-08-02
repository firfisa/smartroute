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
	"github.com/firfisa/smartroute/internal/guard"
	"github.com/firfisa/smartroute/internal/health"
	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/observe"
	"github.com/firfisa/smartroute/internal/privacy"
	"github.com/firfisa/smartroute/internal/sidecar"
	"github.com/firfisa/smartroute/internal/store"
	"github.com/firfisa/smartroute/internal/supervisor"
	"github.com/firfisa/smartroute/internal/transport"
)

var (
	version = "dev"
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
	case "observations":
		return runObservations(args[1:], stdout, stderr)
	case "learning":
		return runLearning(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runSupervise(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("supervise", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	profileID := flags.String("network-profile", "manual-experimental", "local network profile label for child events")
	allowDirectProbes := flags.Bool("acknowledge-direct-probes", false, "acknowledge Direct candidates in explicit-opt-in privacy mode")
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
	recorder, err := openObservationRecorder(cfg, observe.SourceSupervisor)
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
		Services: supervisedServices(executable, absoluteConfig, *profileID, *allowDirectProbes),
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

func supervisedServices(executable, configPath, profileID string, acknowledgeDirect bool) []supervisor.Service {
	serveArgs := []string{"serve", "-config", configPath, "-network-profile", profileID}
	if acknowledgeDirect {
		serveArgs = append(serveArgs, "-acknowledge-direct-probes")
	}
	return []supervisor.Service{
		{Name: "adaptive_engine", Executable: executable, Args: serveArgs},
		{Name: "availability_guard", Executable: executable, Args: []string{"guard", "-config", configPath, "-network-profile", profileID}},
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
	recorder, err := openObservationRecorder(cfg, observe.SourceGuard)
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
	ephemeralLearner, err := learning.New(learning.Config{
		Mode:                cfg.Learning.Mode,
		MaxEntries:          cfg.Learning.MaxEntries,
		DirectPromotionWins: cfg.Learning.DirectPromotionWins,
		ProxyPromotionWins:  cfg.Learning.ProxyPromotionWins,
		TTL:                 time.Duration(cfg.Learning.PolicyTTLHours) * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("initialize learning engine: %w", err)
	}
	recorder, err := openObservationRecorder(cfg, observe.SourceEngine)
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
	if cfg.Learning.Health.Enabled {
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
	learner := &runtimeLearningEngine{ephemeral: ephemeralLearner, health: healthGate}
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
	if durable != nil {
		learner.writer = durable.writer
	}
	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddress, err)
	}
	defer listener.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := sidecar.Server{
		NetworkProfileID:  *profileID,
		DirectProbePolicy: privacyPolicy,
		Learning:          learner,
		HandshakeTimeout:  cfg.CandidateTimeout(),
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
				EventType: event.EventType, Target: &event.Target, SelectedPath: event.SelectedPath,
				ReasonCode: event.ReasonCode, PolicyReason: event.PolicyReason,
				Observation: &observation, OtherObservation: event.OtherObservation, Committed: &committed,
				LearningReason: event.LearningReason, DurableReason: event.DurableReason, PolicyState: event.PolicyState,
			})
		},
		OnDiagnostic: func(event sidecar.DiagnosticEvent) {
			outputMu.Lock()
			defer outputMu.Unlock()
			events.Emit(event, observe.Event{
				EventType: event.EventType, Target: &event.Target, ReasonCode: event.ReasonCode,
				PolicyReason: event.PolicyReason, FailureClass: event.FailureClass,
				DirectFailure: event.DirectFailure, ProxyFailure: event.ProxyFailure,
			})
		},
	}
	fmt.Fprintf(stderr, "experimental TLS sidecar listening on %s; privacy=%s; no Clash files are read or modified\n", listener.Addr(), cfg.Privacy.Mode)
	return server.Serve(ctx, listener)
}

type durableEvidenceWriter interface {
	Enqueue(store.WriteRequest) (bool, string)
}

type runtimeLearningEngine struct {
	mu        sync.Mutex
	ephemeral *learning.Engine
	writer    durableEvidenceWriter
	clock     func() time.Time
	health    *health.Gate
	onHealth  func(health.Event)
	onError   func(error)
}

func (e *runtimeLearningEngine) PreferredPath(target model.Target) model.Path {
	e.mu.Lock()
	defer e.mu.Unlock()
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
	if direction == "" {
		return learning.Update{ReasonCode: nonAppliedReason}, nil
	}
	update, err := e.ephemeral.Observe(target, winner, other)
	if err != nil || e.writer == nil {
		return update, err
	}
	now := time.Now
	if e.clock != nil {
		now = e.clock
	}
	_, update.DurableReason = e.writer.Enqueue(store.WriteRequest{
		Target: target, Winner: winner, Other: other, ObservedAt: now().UTC(),
	})
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
		e.ephemeral.Clear()
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
	store  *store.Store
	writer *store.AsyncWriter
}

func openDurableLearning(ctx context.Context, cfg config.Config, onError func(error), onAssessment func(learning.DurableAssessmentEvent)) (*durableLearningRuntime, error) {
	if !cfg.Learning.Persistence.Enabled {
		return nil, nil
	}
	evaluator, err := durableEvaluatorFromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize durable learning evaluator: %w", err)
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
	sessionID, err := newLearningSessionID()
	if err != nil {
		return closeOnError(err)
	}
	now := time.Now().UTC()
	if _, err := evidenceStore.PruneEvidence(ctx, now.Add(-cfg.LearningEvidenceRetention())); err != nil {
		return closeOnError(fmt.Errorf("prune durable learning evidence: %w", err))
	}
	if err := evidenceStore.StartSession(ctx, sessionID, now); err != nil {
		return closeOnError(fmt.Errorf("start durable learning session: %w", err))
	}
	writerOptions := store.WriterOptions{Capacity: cfg.Learning.Persistence.QueueSize, OnError: onError}
	if onAssessment != nil {
		writerOptions.OnWritten = func(request store.WriteRequest) error {
			summary, err := evidenceStore.Summarize(
				context.Background(), request.Target,
				request.ObservedAt.Add(-cfg.LearningEvidenceRetention()),
			)
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
	writer, err := store.NewAsyncWriterWithOptions(evidenceStore, sessionID, writerOptions)
	if err != nil {
		return closeOnError(fmt.Errorf("initialize durable learning writer: %w", err))
	}
	return &durableLearningRuntime{store: evidenceStore, writer: writer}, nil
}

func (r *durableLearningRuntime) Close(ctx context.Context) (store.WriterStats, error) {
	if r == nil {
		return store.WriterStats{}, nil
	}
	if err := r.writer.Close(ctx); err != nil {
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

func openObservationRecorder(cfg config.Config, source string) (*observe.Recorder, error) {
	if !cfg.Observation.Enabled {
		return nil, nil
	}
	recorder, err := observe.New(observe.Options{
		Directory: cfg.Observation.Directory, Source: source,
		MaxFileBytes: cfg.Observation.MaxFileBytes, MaxFiles: cfg.Observation.MaxFilesPerSource,
		Retention:                time.Duration(cfg.Observation.RetentionHours) * time.Hour,
		IncludeCleartextHostname: cfg.Observation.IncludeCleartextHostname,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize observation recorder: %w", err)
	}
	return recorder, nil
}

func runObservations(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("observations requires status, pause, resume, clear, or export")
	}
	action := args[0]
	flags := flag.NewFlagSet("observations "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	confirmClear := flags.Bool("confirm-clear", false, "confirm deletion of all local observation JSONL files")
	destination := flags.String("destination", "", "new directory for a redacted observation export")
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

func runLearning(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("learning requires status, evaluate, report, backup, verify-backup, or restore")
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
  smartroute serve -acknowledge-direct-probes [-config path] [-network-profile label]
  smartroute guard [-config path] [-network-profile label]
  smartroute supervise [-acknowledge-direct-probes] [-config path] [-network-profile label]
  smartroute observations status|pause|resume|clear|export [-config path]
  smartroute learning status [-config path]
  smartroute learning evaluate -network-profile label -hostname host -port port [-transport tcp|udp] [-config path]
  smartroute learning report [-config path]
  smartroute learning backup -destination new-directory [-config path]
  smartroute learning verify-backup -source backup-directory
  smartroute learning restore -source backup-directory -destination new-database

The trace command evaluates one synthetic paired observation. The experimental
serve command accepts TLS-over-SOCKS on the configured loopback listener and
does not read or modify Clash configuration. Explicit-opt-in privacy mode
requires a Direct-probe acknowledgment; privacy-first opens Proxy only. The guard
command falls back to the configured original SOCKS listener if the adaptive
engine cannot accept a target connection. The supervise command runs Guard and
engine as independently restartable child processes; it does not replay a
connection lost while Guard itself is down.`)
}
