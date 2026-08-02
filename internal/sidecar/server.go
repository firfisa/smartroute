// Package sidecar implements SmartRoute's loopback-only inbound SOCKS5 relay.
package sidecar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

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
	EventType        string             `json:"event_type"`
	Target           model.Target       `json:"target"`
	SelectedPath     model.Path         `json:"selected_path"`
	ReasonCode       string             `json:"reason_code"`
	PolicyReason     string             `json:"policy_reason,omitempty"`
	Observation      model.Observation  `json:"observation"`
	OtherObservation *model.Observation `json:"other_observation,omitempty"`
	Committed        bool               `json:"committed"`
	LearningReason   string             `json:"learning_reason,omitempty"`
	DurableReason    string             `json:"durable_reason,omitempty"`
	PolicyState      model.PolicyState  `json:"policy_state,omitempty"`
}

type DiagnosticEvent struct {
	EventType     string       `json:"event_type"`
	Target        model.Target `json:"target"`
	ReasonCode    string       `json:"reason_code"`
	FailureClass  string       `json:"failure_class"`
	DirectFailure string       `json:"direct_failure,omitempty"`
	ProxyFailure  string       `json:"proxy_failure,omitempty"`
	PolicyReason  string       `json:"policy_reason,omitempty"`
}

const (
	EventTypeDecision               = "decision"
	EventTypeDiagnostic             = "diagnostic"
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

type Server struct {
	Racer               transport.Racer
	TLSRacer            *transport.TLSRacer
	NetworkProfileID    string
	HandshakeTimeout    time.Duration
	MaxClientHelloBytes int
	MinimumCommitStage  model.Stage
	DirectProbePolicy   DirectProbePolicy
	Learning            LearningEngine
	OnDecision          func(DecisionEvent)
	OnDiagnostic        func(DiagnosticEvent)
}

func (s Server) Serve(ctx context.Context, listener net.Listener) error {
	if listener == nil {
		return errors.New("listener is required")
	}
	if s.HandshakeTimeout <= 0 {
		s.HandshakeTimeout = 5 * time.Second
	}

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept sidecar connection: %w", err)
		}
		go s.handle(ctx, conn)
	}
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
	if s.TLSRacer != nil {
		s.handleTLS(ctx, inbound, target)
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
				EventType: EventTypeDecision,
				Target:    target, SelectedPath: result.Observation.Path,
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
			EventType: EventTypeDecision,
			Target:    target, SelectedPath: result.Observation.Path,
			ReasonCode: result.ReasonCode, Observation: result.Observation,
			OtherObservation: result.OtherObservation,
			Committed:        true,
		})
	}
	netrelay.Bidirectional(inbound, result.Conn)
}

func (s Server) handleTLS(ctx context.Context, inbound net.Conn, target model.Target) {
	// A SOCKS client does not send ClientHello until CONNECT succeeds. This
	// acknowledgment is local admission only; no remote path is committed yet.
	if err := socks5.WriteReply(inbound, socks5.ReplySucceeded); err != nil {
		return
	}
	hello, err := tlsinspect.ReadClientHello(inbound, s.MaxClientHelloBytes)
	if err != nil {
		s.emitDiagnostic(DiagnosticEvent{
			Target: target, ReasonCode: ReasonClientHelloRejected,
			FailureClass: classifyClientHelloFailure(ctx, err),
		})
		return
	}
	privacyDecision := privacy.Decision{ReasonCode: privacy.ReasonMissingRuntimePolicy}
	if s.DirectProbePolicy != nil {
		privacyDecision = s.DirectProbePolicy.Evaluate(target)
	}
	var result transport.RaceResult
	learningReason := ""
	durableReason := ""
	policyState := model.PolicyState("")
	if privacyDecision.AllowDirect {
		preferred := model.PathDirect
		if s.Learning != nil {
			if learned := s.Learning.PreferredPath(target); learned == model.PathDirect || learned == model.PathProxy {
				preferred = learned
			}
		}
		result, err = s.TLSRacer.RacePreferred(ctx, target, hello, preferred)
	} else {
		learningReason = ReasonLearningSkippedByPolicy
		result, err = s.TLSRacer.ConnectPath(ctx, target, hello, model.PathProxy)
		if err == nil {
			result.ReasonCode = privacyDecision.ReasonCode
		}
	}
	if err != nil {
		if !privacyDecision.AllowDirect {
			event := DiagnosticEvent{
				Target: target, ReasonCode: ReasonPrivacyProxyPathFailed,
				FailureClass: "proxy_only_tls_failed", DirectFailure: "skipped_by_privacy",
				PolicyReason: privacyDecision.ReasonCode,
			}
			var pathError *transport.TLSPathError
			if errors.As(err, &pathError) {
				event.ProxyFailure = pathError.Observation.FailureClass
			}
			s.emitDiagnostic(event)
			return
		}
		event := DiagnosticEvent{
			Target: target, ReasonCode: ReasonTLSCandidatesFailed,
			FailureClass: "all_tls_candidates_failed", PolicyReason: privacyDecision.ReasonCode,
		}
		var raceError *transport.RaceError
		if errors.As(err, &raceError) {
			event.DirectFailure = raceError.Direct.FailureClass
			event.ProxyFailure = raceError.Proxy.FailureClass
		}
		s.emitDiagnostic(event)
		return
	}
	defer result.Conn.Close()
	if result.Observation.StageReached < model.StageTLS {
		s.emitDiagnostic(DiagnosticEvent{
			Target: target, ReasonCode: ReasonCandidateBelowCommitStage,
			FailureClass: "tls_candidate_below_tls_stage",
		})
		return
	}
	if privacyDecision.AllowDirect && s.Learning != nil {
		update, learningErr := s.Learning.Observe(target, result.Observation, result.OtherObservation)
		if learningErr != nil {
			learningReason = ReasonLearningUpdateError
		} else {
			learningReason = update.ReasonCode
			durableReason = update.DurableReason
			if update.Applied {
				policyState = update.Policy.State
			}
		}
	}
	_ = inbound.SetDeadline(time.Time{})
	if s.OnDecision != nil {
		s.OnDecision(DecisionEvent{
			EventType: EventTypeDecision,
			Target:    target, SelectedPath: result.Observation.Path,
			ReasonCode: result.ReasonCode, PolicyReason: privacyDecision.ReasonCode,
			Observation:      result.Observation,
			OtherObservation: result.OtherObservation,
			Committed:        true,
			LearningReason:   learningReason,
			DurableReason:    durableReason,
			PolicyState:      policyState,
		})
	}
	netrelay.Bidirectional(inbound, result.Conn)
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
