// Package guard implements a small availability boundary in front of the
// adaptive engine. It falls back before client payload bytes are forwarded to
// either availability lane.
package guard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/netrelay"
	"github.com/firfisa/smartroute/internal/socks5"
)

const (
	LaneAdaptive = "adaptive"
	LaneOriginal = "original"

	ReasonAdaptiveAvailable              = "adaptive_available"
	ReasonAdaptiveUnavailableUseOriginal = "adaptive_unavailable_use_original"
	ReasonAllGuardPathsUnavailable       = "adaptive_and_original_unavailable"
)

type Dialer interface {
	Dial(ctx context.Context, request socks5.Request) (net.Conn, error)
}

type SOCKS5Dialer struct {
	Endpoint string
}

func (d SOCKS5Dialer) Dial(ctx context.Context, request socks5.Request) (net.Conn, error) {
	return socks5.DialContext(ctx, d.Endpoint, request)
}

type DecisionEvent struct {
	EventType       string       `json:"event_type"`
	Target          model.Target `json:"target"`
	SelectedLane    string       `json:"selected_lane,omitempty"`
	ReasonCode      string       `json:"reason_code"`
	AdaptiveFailure string       `json:"adaptive_failure,omitempty"`
	OriginalFailure string       `json:"original_failure,omitempty"`
	Committed       bool         `json:"committed"`
}

type Server struct {
	Adaptive         Dialer
	Original         Dialer
	NetworkProfileID string
	AdaptiveTimeout  time.Duration
	HandshakeTimeout time.Duration
	OnDecision       func(DecisionEvent)
}

func (s Server) Serve(ctx context.Context, listener net.Listener) error {
	if listener == nil {
		return errors.New("guard listener is required")
	}
	if s.Adaptive == nil || s.Original == nil {
		return errors.New("adaptive and original guard dialers are required")
	}
	if s.HandshakeTimeout <= 0 {
		s.HandshakeTimeout = 5 * time.Second
	}
	if s.AdaptiveTimeout <= 0 {
		s.AdaptiveTimeout = 250 * time.Millisecond
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
			return fmt.Errorf("accept guard connection: %w", err)
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
	adaptiveTimeout := s.AdaptiveTimeout
	if adaptiveTimeout <= 0 {
		adaptiveTimeout = 250 * time.Millisecond
	}
	handshakeTimeout := s.HandshakeTimeout
	if handshakeTimeout <= 0 {
		handshakeTimeout = 5 * time.Second
	}
	_ = inbound.SetDeadline(time.Now().Add(adaptiveTimeout + handshakeTimeout))
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
		Hostname:         request.Host, Port: request.Port, Transport: model.TransportTCP,
	}
	_ = inbound.SetDeadline(time.Now().Add(adaptiveTimeout + handshakeTimeout))
	adaptiveCtx, adaptiveCancel := context.WithTimeout(ctx, adaptiveTimeout)
	selected, adaptiveErr := s.Adaptive.Dial(adaptiveCtx, request)
	adaptiveFailure := ""
	if adaptiveErr != nil {
		adaptiveFailure = classifyFailure(adaptiveCtx, adaptiveErr)
	}
	adaptiveCancel()
	lane := LaneAdaptive
	reason := ReasonAdaptiveAvailable
	if adaptiveErr != nil {
		originalCtx, originalCancel := context.WithTimeout(ctx, handshakeTimeout)
		selected, err = s.Original.Dial(originalCtx, request)
		originalFailure := ""
		if err != nil {
			originalFailure = classifyFailure(originalCtx, err)
		}
		originalCancel()
		lane = LaneOriginal
		reason = ReasonAdaptiveUnavailableUseOriginal
		if err != nil {
			s.emit(DecisionEvent{
				EventType: "guard_decision", Target: target,
				ReasonCode:      ReasonAllGuardPathsUnavailable,
				AdaptiveFailure: adaptiveFailure,
				OriginalFailure: originalFailure, Committed: false,
			})
			_ = socks5.WriteReply(inbound, socks5.ReplyConnectionRefused)
			return
		}
	}
	defer selected.Close()
	if err := socks5.WriteReply(inbound, socks5.ReplySucceeded); err != nil {
		return
	}
	_ = inbound.SetDeadline(time.Time{})
	event := DecisionEvent{
		EventType: "guard_decision", Target: target, SelectedLane: lane,
		ReasonCode: reason, Committed: true,
	}
	if adaptiveErr != nil {
		event.AdaptiveFailure = adaptiveFailure
	}
	s.emit(event)
	netrelay.Bidirectional(ctx, inbound, selected)
}

func (s Server) emit(event DecisionEvent) {
	if s.OnDecision != nil {
		s.OnDecision(event)
	}
}

func classifyFailure(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		return "timeout"
	}
	return "unavailable"
}
