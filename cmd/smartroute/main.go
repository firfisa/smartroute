package main

import (
	"context"
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
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/observe"
	"github.com/firfisa/smartroute/internal/privacy"
	"github.com/firfisa/smartroute/internal/sidecar"
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
	recorder, err := openObservationRecorder(cfg, observe.SourceEngine)
	if err != nil {
		return err
	}
	if recorder != nil {
		defer recorder.Close()
	}
	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddress, err)
	}
	defer listener.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var outputMu sync.Mutex
	events := newRuntimeEventSink(&synchronizedWriter{writer: stdout}, &synchronizedWriter{writer: stderr}, recorder)
	server := sidecar.Server{
		NetworkProfileID:  *profileID,
		DirectProbePolicy: privacyPolicy,
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
				Observation: &observation, Committed: &committed,
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

The trace command evaluates one synthetic paired observation. The experimental
serve command accepts TLS-over-SOCKS on the configured loopback listener and
does not read or modify Clash configuration. Explicit-opt-in privacy mode
requires a Direct-probe acknowledgment; privacy-first opens Proxy only. The guard
command falls back to the configured original SOCKS listener if the adaptive
engine cannot accept a target connection. The supervise command runs Guard and
engine as independently restartable child processes; it does not replay a
connection lost while Guard itself is down.`)
}
