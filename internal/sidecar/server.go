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

	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/socks5"
	"github.com/firfisa/smartroute/internal/transport"
)

// DecisionEvent is the minimum explainable runtime event emitted by the
// Phase 0 sidecar. It contains no payload or credentials.
type DecisionEvent struct {
	Target       model.Target      `json:"target"`
	SelectedPath model.Path        `json:"selected_path"`
	ReasonCode   string            `json:"reason_code"`
	Observation  model.Observation `json:"observation"`
	Committed    bool              `json:"committed"`
}

const ReasonCandidateBelowCommitStage = "candidate_below_commit_stage"

type Server struct {
	Racer              transport.Racer
	NetworkProfileID   string
	HandshakeTimeout   time.Duration
	MinimumCommitStage model.Stage
	OnDecision         func(DecisionEvent)
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
				Target: target, SelectedPath: result.Observation.Path,
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
			Target: target, SelectedPath: result.Observation.Path,
			ReasonCode: result.ReasonCode, Observation: result.Observation,
			Committed: true,
		})
	}
	relay(inbound, result.Conn)
}

func relay(left, right net.Conn) {
	var wait sync.WaitGroup
	wait.Add(2)
	copyDirection := func(dst, src net.Conn) {
		defer wait.Done()
		_, _ = io.Copy(dst, src)
		if closeWriter, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		}
	}
	go copyDirection(right, left)
	go copyDirection(left, right)
	wait.Wait()
}
