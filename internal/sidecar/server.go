// Package sidecar implements SmartRoute's loopback-only inbound SOCKS5 relay.
package sidecar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/firfisa/smartroute/internal/connectionid"
	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/netrelay"
	"github.com/firfisa/smartroute/internal/privacy"
	"github.com/firfisa/smartroute/internal/socks5"
	"github.com/firfisa/smartroute/internal/tlsinspect"
	"github.com/firfisa/smartroute/internal/transport"
)

// DecisionEvent is the minimum explainable runtime event emitted by the
// Phase 0 sidecar. It contains no payload or credentials.
type DecisionEvent struct {
	EventType            string             `json:"event_type"`
	ConnectionID         string             `json:"connection_id,omitempty"`
	Target               model.Target       `json:"target"`
	DeclaredBaselinePath model.Path         `json:"declared_baseline_path,omitempty"`
	SelectedPath         model.Path         `json:"selected_path"`
	ReasonCode           string             `json:"reason_code"`
	PolicyReason         string             `json:"policy_reason,omitempty"`
	Observation          model.Observation  `json:"observation"`
	OtherObservation     *model.Observation `json:"other_observation,omitempty"`
	Committed            bool               `json:"committed"`
	LearningReason       string             `json:"learning_reason,omitempty"`
	DurableReason        string             `json:"durable_reason,omitempty"`
	PolicyState          model.PolicyState  `json:"policy_state,omitempty"`
	DecisionLatencyMS    *int64             `json:"decision_latency_ms,omitempty"`
}

type DiagnosticEvent struct {
	EventType            string       `json:"event_type"`
	ConnectionID         string       `json:"connection_id,omitempty"`
	Target               model.Target `json:"target"`
	DeclaredBaselinePath model.Path   `json:"declared_baseline_path,omitempty"`
	ReasonCode           string       `json:"reason_code"`
	FailureClass         string       `json:"failure_class"`
	DirectFailure        string       `json:"direct_failure,omitempty"`
	ProxyFailure         string       `json:"proxy_failure,omitempty"`
	PolicyReason         string       `json:"policy_reason,omitempty"`
	DecisionLatencyMS    *int64       `json:"decision_latency_ms,omitempty"`
}

// RelayOutcomeEvent contains aggregate post-commit transfer metadata only. It
// never contains payload bytes and does not claim application-level success.
type RelayOutcomeEvent struct {
	EventType            string             `json:"event_type"`
	ConnectionID         string             `json:"connection_id,omitempty"`
	Target               model.Target       `json:"target"`
	DeclaredBaselinePath model.Path         `json:"declared_baseline_path,omitempty"`
	SelectedPath         model.Path         `json:"selected_path"`
	ClientToRemoteBytes  int64              `json:"client_to_remote_bytes"`
	RemoteToClientBytes  int64              `json:"remote_to_client_bytes"`
	ClientToRemoteEnd    netrelay.EndReason `json:"client_to_remote_end"`
	RemoteToClientEnd    netrelay.EndReason `json:"remote_to_client_end"`
	RelayDurationMS      int64              `json:"relay_duration_ms"`
	Termination          string             `json:"termination"`
}

const (
	EventTypeDecision               = "decision"
	EventTypeDiagnostic             = "diagnostic"
	EventTypeRelayOutcome           = "relay_outcome"
	RelayTerminationEnded           = "ended"
	RelayTerminationCanceled        = "canceled"
	ReasonCandidateBelowCommitStage = "candidate_below_commit_stage"
	ReasonClientHelloRejected       = "client_hello_rejected"
	ReasonTLSCandidatesFailed       = "tls_candidates_failed"
	ReasonPrivacyProxyPathFailed    = "privacy_proxy_path_failed"
	ReasonLearningUpdateError       = "learning_update_error"
	ReasonLearningSkippedByPolicy   = "learning_skipped_by_policy"
)

type DirectProbePolicy interface {
	Evaluate(target model.Target) privacy.Decision
}

type LearningEngine interface {
	PreferredPath(target model.Target) model.Path
	Observe(target model.Target, winner model.Observation, other *model.Observation) (learning.Update, error)
}

// FixedPathSelector is optional. It identifies an automatic last-known-good
// path so the sidecar can avoid parallel candidate work while still
// falling back once before commitment if the learned path has gone stale.
type FixedPathSelector interface {
	FixedPath(target model.Target) model.Path
}

// LearningHealthObserver is optional so routing tests and alternate learning
// implementations do not need to implement environmental health tracking.
type LearningHealthObserver interface {
	ObserveBothPathsFailed(target model.Target)
	ObserveProxyPathFailed(target model.Target)
	ObservePathSucceeded(target model.Target, path model.Path)
}

type Server struct {
	Racer                 transport.Racer
	TLSRacer              *transport.TLSRacer
	NetworkProfileID      string
	DeclaredBaselinePath  model.Path
	HandshakeTimeout      time.Duration
	MaxClientHelloBytes   int
	MinimumCommitStage    model.Stage
	DirectProbePolicy     DirectProbePolicy
	Learning              LearningEngine
	Clock                 func() time.Time
	ConnectionIDGenerator func() (string, error)
	OnDecision            func(DecisionEvent)
	OnDiagnostic          func(DiagnosticEvent)
	OnRelayOutcome        func(RelayOutcomeEvent)
}

func (s Server) Serve(ctx context.Context, listener net.Listener) error {
	if listener == nil {
		return errors.New("listener is required")
	}
	if s.DeclaredBaselinePath != "" && s.DeclaredBaselinePath != model.PathDirect && s.DeclaredBaselinePath != model.PathProxy {
		return errors.New("declared baseline path must be direct or proxy")
	}
	if s.HandshakeTimeout <= 0 {
		s.HandshakeTimeout = 5 * time.Second
	}

	serveCtx, cancel := context.WithCancel(ctx)
	listenerClosed := make(chan struct{})
	listenerCloserDone := make(chan struct{})
	go func() {
		defer close(listenerCloserDone)
		select {
		case <-serveCtx.Done():
			_ = listener.Close()
		case <-listenerClosed:
		}
	}()
	defer func() {
		cancel()
		close(listenerClosed)
		<-listenerCloserDone
	}()
	var handlers sync.WaitGroup
	for {
		conn, err := listener.Accept()
		if err != nil {
			cancel()
			handlers.Wait()
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept sidecar connection: %w", err)
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			s.serveConnection(serveCtx, conn)
		}()
	}
}

func (s Server) serveConnection(ctx context.Context, conn net.Conn) {
	finished := make(chan struct{})
	closerDone := make(chan struct{})
	go func() {
		defer close(closerDone)
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-finished:
		}
	}()
	s.handle(ctx, conn)
	close(finished)
	<-closerDone
}

func (s Server) handle(ctx context.Context, inbound net.Conn) {
	defer inbound.Close()
	_ = inbound.SetDeadline(time.Now().Add(s.HandshakeTimeout))
	request, err := socks5.ReadRequest(inbound)
	if err != nil {
		var requestError *socks5.RequestError
		if errors.As(err, &requestError) {
			_ = socks5.WriteReply(inbound, requestError.Reply)
		}
		return
	}

	target := model.Target{
		NetworkProfileID: s.NetworkProfileID,
		Hostname:         request.Host,
		Port:             request.Port,
		Transport:        model.TransportTCP,
	}
	connectionID := s.connectionID()
	if s.TLSRacer != nil {
		s.handleTLS(ctx, inbound, target, connectionID)
		return
	}
	result, err := s.Racer.Race(ctx, target)
	if err != nil {
		_ = socks5.WriteReply(inbound, socks5.ReplyConnectionRefused)
		return
	}
	defer result.Conn.Close()
	minimumCommitStage := s.MinimumCommitStage
	if minimumCommitStage < model.StageTCP {
		minimumCommitStage = model.StageTCP
	}
	if result.Observation.StageReached < minimumCommitStage {
		if s.OnDecision != nil {
			s.OnDecision(DecisionEvent{
				EventType:    EventTypeDecision,
				ConnectionID: connectionID,
				Target:       target, DeclaredBaselinePath: s.DeclaredBaselinePath, SelectedPath: result.Observation.Path,
				ReasonCode: ReasonCandidateBelowCommitStage, Observation: result.Observation,
				Committed: false,
			})
		}
		_ = socks5.WriteReply(inbound, socks5.ReplyConnectionRefused)
		return
	}
	if err := socks5.WriteReply(inbound, socks5.ReplySucceeded); err != nil {
		return
	}
	_ = inbound.SetDeadline(time.Time{})
	if s.OnDecision != nil {
		s.OnDecision(DecisionEvent{
			EventType:    EventTypeDecision,
			ConnectionID: connectionID,
			Target:       target, DeclaredBaselinePath: s.DeclaredBaselinePath, SelectedPath: result.Observation.Path,
			ReasonCode: result.ReasonCode, Observation: result.Observation,
			OtherObservation: result.OtherObservation,
			Committed:        true,
		})
	}
	s.relay(ctx, inbound, result.Conn, target, result.Observation.Path, connectionID)
}

func (s Server) handleTLS(ctx context.Context, inbound net.Conn, target model.Target, connectionID string) {
	// A SOCKS client does not send ClientHello until CONNECT succeeds. This
	// acknowledgment is local admission only; no remote path is committed yet.
	if err := socks5.WriteReply(inbound, socks5.ReplySucceeded); err != nil {
		return
	}
	hello, err := tlsinspect.ReadClientHello(inbound, s.MaxClientHelloBytes)
	if err != nil {
		s.emitDiagnostic(DiagnosticEvent{
			ConnectionID: connectionID, Target: target, DeclaredBaselinePath: s.DeclaredBaselinePath, ReasonCode: ReasonClientHelloRejected,
			FailureClass: classifyClientHelloFailure(ctx, err),
		})
		return
	}
	privacyDecision := privacy.Decision{ReasonCode: privacy.ReasonMissingRuntimePolicy}
	if s.DirectProbePolicy != nil {
		privacyDecision = s.DirectProbePolicy.Evaluate(target)
	}
	var result transport.RaceResult
	now := s.Clock
	if now == nil {
		now = time.Now
	}
	decisionStarted := now()
	learningReason := ""
	durableReason := ""
	policyState := model.PolicyState("")
	if privacyDecision.AllowDirect {
		fixed := model.Path("")
		if selector, ok := s.Learning.(FixedPathSelector); ok {
			fixed = selector.FixedPath(target)
		}
		if fixed == model.PathDirect || fixed == model.PathProxy {
			result, err = s.TLSRacer.ConnectPreferredWithFallback(ctx, target, hello, fixed)
			if fixed == model.PathDirect {
				policyState = model.StateDirectPreferred
			} else {
				policyState = model.StateProxyPreferred
			}
		} else {
			preferred := model.PathDirect
			if s.Learning != nil {
				if learned := s.Learning.PreferredPath(target); learned == model.PathDirect || learned == model.PathProxy {
					preferred = learned
				}
			}
			result, err = s.TLSRacer.RacePreferred(ctx, target, hello, preferred)
		}
	} else {
		learningReason = ReasonLearningSkippedByPolicy
		result, err = s.TLSRacer.ConnectPath(ctx, target, hello, model.PathProxy)
		if err == nil {
			result.ReasonCode = privacyDecision.ReasonCode
		}
	}
	if err != nil {
		decisionLatencyMS := nonNegativeMilliseconds(now().Sub(decisionStarted))
		if !privacyDecision.AllowDirect {
			event := DiagnosticEvent{
				ConnectionID: connectionID, Target: target, DeclaredBaselinePath: s.DeclaredBaselinePath, ReasonCode: ReasonPrivacyProxyPathFailed,
				FailureClass: "proxy_only_tls_failed", DirectFailure: "skipped_by_privacy",
				PolicyReason:      privacyDecision.ReasonCode,
				DecisionLatencyMS: &decisionLatencyMS,
			}
			var pathError *transport.TLSPathError
			if errors.As(err, &pathError) {
				event.ProxyFailure = pathError.Observation.FailureClass
				if observer, ok := s.Learning.(LearningHealthObserver); ok && healthRelevantFailure(pathError.Observation) {
					observer.ObserveProxyPathFailed(target)
				}
			}
			s.emitDiagnostic(event)
			return
		}
		event := DiagnosticEvent{
			ConnectionID: connectionID, Target: target, DeclaredBaselinePath: s.DeclaredBaselinePath, ReasonCode: ReasonTLSCandidatesFailed,
			FailureClass: "all_tls_candidates_failed", PolicyReason: privacyDecision.ReasonCode,
			DecisionLatencyMS: &decisionLatencyMS,
		}
		var raceError *transport.RaceError
		if errors.As(err, &raceError) {
			event.DirectFailure = raceError.Direct.FailureClass
			event.ProxyFailure = raceError.Proxy.FailureClass
			if observer, ok := s.Learning.(LearningHealthObserver); ok && healthRelevantFailure(raceError.Direct) && healthRelevantFailure(raceError.Proxy) {
				observer.ObserveBothPathsFailed(target)
			}
		}
		s.emitDiagnostic(event)
		return
	}
	decisionLatencyMS := nonNegativeMilliseconds(now().Sub(decisionStarted))
	defer result.Conn.Close()
	if result.Observation.StageReached < model.StageTLS {
		s.emitDiagnostic(DiagnosticEvent{
			ConnectionID: connectionID, Target: target, DeclaredBaselinePath: s.DeclaredBaselinePath, ReasonCode: ReasonCandidateBelowCommitStage,
			FailureClass:      "tls_candidate_below_tls_stage",
			DecisionLatencyMS: &decisionLatencyMS,
		})
		return
	}
	if !privacyDecision.AllowDirect {
		if observer, ok := s.Learning.(LearningHealthObserver); ok {
			observer.ObservePathSucceeded(target, result.Observation.Path)
		}
	}
	if privacyDecision.AllowDirect && s.Learning != nil {
		update, learningErr := s.Learning.Observe(target, result.Observation, result.OtherObservation)
		if learningErr != nil {
			learningReason = ReasonLearningUpdateError
		} else {
			learningReason = update.ReasonCode
			durableReason = update.DurableReason
			if update.Applied && policyState == "" {
				policyState = update.Policy.State
			}
		}
	}
	_ = inbound.SetDeadline(time.Time{})
	if s.OnDecision != nil {
		s.OnDecision(DecisionEvent{
			EventType:    EventTypeDecision,
			ConnectionID: connectionID,
			Target:       target, DeclaredBaselinePath: s.DeclaredBaselinePath, SelectedPath: result.Observation.Path,
			ReasonCode: result.ReasonCode, PolicyReason: privacyDecision.ReasonCode,
			Observation:       result.Observation,
			OtherObservation:  result.OtherObservation,
			Committed:         true,
			LearningReason:    learningReason,
			DurableReason:     durableReason,
			PolicyState:       policyState,
			DecisionLatencyMS: &decisionLatencyMS,
		})
	}
	s.relay(ctx, inbound, result.Conn, target, result.Observation.Path, connectionID)
}

func (s Server) relay(ctx context.Context, inbound, outbound net.Conn, target model.Target, path model.Path, connectionID string) {
	if s.OnRelayOutcome == nil {
		_ = netrelay.Bidirectional(ctx, inbound, outbound)
		return
	}
	now := s.Clock
	if now == nil {
		now = time.Now
	}
	started := now()
	result := netrelay.Bidirectional(ctx, inbound, outbound)
	termination := RelayTerminationEnded
	if result.Canceled {
		termination = RelayTerminationCanceled
	}
	s.OnRelayOutcome(RelayOutcomeEvent{
		EventType: EventTypeRelayOutcome, ConnectionID: connectionID, Target: target,
		DeclaredBaselinePath: s.DeclaredBaselinePath, SelectedPath: path,
		ClientToRemoteBytes: result.LeftToRightBytes, RemoteToClientBytes: result.RightToLeftBytes,
		ClientToRemoteEnd: result.LeftToRightEnd, RemoteToClientEnd: result.RightToLeftEnd,
		RelayDurationMS: nonNegativeMilliseconds(now().Sub(started)), Termination: termination,
	})
}

func (s Server) connectionID() string {
	if s.OnDecision == nil && s.OnDiagnostic == nil && s.OnRelayOutcome == nil {
		return ""
	}
	generate := s.ConnectionIDGenerator
	if generate == nil {
		generate = connectionid.New
	}
	value, err := generate()
	if err != nil || connectionid.Validate(value) != nil {
		return ""
	}
	return value
}

func nonNegativeMilliseconds(duration time.Duration) int64 {
	if duration < 0 {
		return 0
	}
	return duration.Milliseconds()
}

func healthRelevantFailure(observation model.Observation) bool {
	return !observation.Success && observation.StageReached >= model.StageOutbound &&
		observation.FailureClass != "canceled" && observation.FailureClass != "not_started"
}

func (s Server) emitDiagnostic(event DiagnosticEvent) {
	if s.OnDiagnostic != nil {
		event.EventType = EventTypeDiagnostic
		s.OnDiagnostic(event)
	}
}

func classifyClientHelloFailure(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "client_hello_timeout"
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		return "client_hello_timeout"
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return "client_hello_connection_closed"
	}
	return tlsinspect.FailureClass(err)
}
