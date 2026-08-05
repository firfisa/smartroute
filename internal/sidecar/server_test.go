package sidecar

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/privacy"
	"github.com/firfisa/smartroute/internal/socks5"
	"github.com/firfisa/smartroute/internal/tlsinspect"
	"github.com/firfisa/smartroute/internal/transport"
)

type stageDialer struct {
	path  model.Path
	stage model.Stage
	peer  chan net.Conn
}

func (d stageDialer) Dial(_ context.Context, _ model.Target) (net.Conn, model.Observation, error) {
	left, right := net.Pipe()
	d.peer <- right
	return left, model.Observation{
		Path: d.path, Success: true, StageReached: d.stage,
	}, nil
}

func TestServerNeverCommitsCandidateBelowTCP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	peers := make(chan net.Conn, 1)
	events := make(chan DecisionEvent, 1)
	server := Server{
		HandshakeTimeout:  time.Second,
		DirectProbePolicy: mustPrivacyPolicy(t, privacy.ModeExplicitOptIn, nil),
		// An accidentally weak override must not bypass the hard L2 floor.
		MinimumCommitStage: model.StageOutbound,
		Racer: transport.Racer{
			Direct:    stageDialer{path: model.PathDirect, stage: model.StageOutbound, peer: peers},
			Proxy:     stageDialer{path: model.PathProxy, stage: model.StageOutbound, peer: make(chan net.Conn, 1)},
			HeadStart: 100 * time.Millisecond,
			Timeout:   time.Second,
		},
		OnDecision: func(event DecisionEvent) { events <- event },
	}
	handled := make(chan struct{})
	go func() {
		server.handle(ctx, serverConn)
		close(handled)
	}()

	if _, err := clientConn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(clientConn, methodReply); err != nil {
		t.Fatal(err)
	}
	request := append([]byte{0x05, 0x01, 0x00, 0x03, 0x09}, []byte("echo.test")...)
	request = append(request, 0x01, 0xbb)
	if _, err := clientConn.Write(request); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(clientConn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != byte(socks5.ReplyConnectionRefused) {
		t.Fatalf("SOCKS reply = 0x%02x", reply[1])
	}

	select {
	case event := <-events:
		if event.EventType != EventTypeDecision || event.Committed || event.ReasonCode != ReasonCandidateBelowCommitStage || event.Observation.StageReached != model.StageOutbound {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("missing below-stage decision event")
	}

	peer := <-peers
	defer peer.Close()
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 1)
	if _, err := peer.Read(buffer); err == nil {
		t.Fatalf("candidate peer remained open: %v", err)
	}

	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop")
	}
}

type sidecarTLSCandidate struct {
	path     model.Path
	handler  func(net.Conn)
	attempts atomic.Int32
}

type stubLearningEngine struct {
	preferred     model.Path
	fixed         model.Path
	update        learning.Update
	err           error
	observed      atomic.Int32
	bothFailed    atomic.Int32
	proxyFailed   atomic.Int32
	pathSucceeded atomic.Int32
}

func (s *stubLearningEngine) ObserveBothPathsFailed(model.Target)           { s.bothFailed.Add(1) }
func (s *stubLearningEngine) ObserveProxyPathFailed(model.Target)           { s.proxyFailed.Add(1) }
func (s *stubLearningEngine) ObservePathSucceeded(model.Target, model.Path) { s.pathSucceeded.Add(1) }

func (s *stubLearningEngine) PreferredPath(model.Target) model.Path { return s.preferred }
func (s *stubLearningEngine) FixedPath(model.Target) model.Path     { return s.fixed }

func (s *stubLearningEngine) Observe(model.Target, model.Observation, *model.Observation) (learning.Update, error) {
	s.observed.Add(1)
	return s.update, s.err
}

func (d *sidecarTLSCandidate) Dial(_ context.Context, _ model.Target) (net.Conn, model.Observation, error) {
	d.attempts.Add(1)
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		d.handler(server)
	}()
	return client, model.Observation{Path: d.path, Success: true, StageReached: model.StageOutbound}, nil
}

func TestServerTLSModeCommitsProxyAfterServerHello(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	serverConn, clientConn := net.Pipe()
	helloWire := fragmentedClientHello(false)
	serverWire := validServerHelloRecord()
	direct := &sidecarTLSCandidate{path: model.PathDirect, handler: func(conn net.Conn) {
		_, _ = io.CopyN(io.Discard, conn, int64(len(helloWire)))
		_, _ = io.Copy(io.Discard, conn)
	}}
	proxy := &sidecarTLSCandidate{path: model.PathProxy, handler: func(conn net.Conn) {
		received := make([]byte, len(helloWire))
		if _, err := io.ReadFull(conn, received); err == nil && bytes.Equal(received, helloWire) {
			_, _ = conn.Write(serverWire)
		}
	}}
	events := make(chan DecisionEvent, 1)
	diagnostics := make(chan DiagnosticEvent, 1)
	server := Server{
		HandshakeTimeout:  time.Second,
		DirectProbePolicy: mustPrivacyPolicy(t, privacy.ModeExplicitOptIn, nil),
		TLSRacer: &transport.TLSRacer{
			Direct: direct, Proxy: proxy, Gate: transport.TLSServerHelloGate{},
			HeadStart: 10 * time.Millisecond, Timeout: time.Second,
		},
		OnDecision:   func(event DecisionEvent) { events <- event },
		OnDiagnostic: func(event DiagnosticEvent) { diagnostics <- event },
	}
	handled := make(chan struct{})
	go func() {
		server.handle(ctx, serverConn)
		close(handled)
	}()

	performSOCKSRequest(t, clientConn)
	for _, fragment := range [][]byte{helloWire[:7], helloWire[7:23], helloWire[23:]} {
		if _, err := clientConn.Write(fragment); err != nil {
			t.Fatal(err)
		}
	}
	receivedServerHello := make([]byte, len(serverWire))
	if _, err := io.ReadFull(clientConn, receivedServerHello); err != nil || !bytes.Equal(receivedServerHello, serverWire) {
		t.Fatalf("server replay error=%v bytes=%x", err, receivedServerHello)
	}
	select {
	case event := <-events:
		if event.EventType != EventTypeDecision || !event.Committed || event.SelectedPath != model.PathProxy || event.Observation.StageReached != model.StageTLS {
			t.Fatalf("decision event = %+v", event)
		}
	case <-ctx.Done():
		t.Fatal("missing TLS decision event")
	}
	select {
	case diagnostic := <-diagnostics:
		t.Fatalf("unexpected diagnostic = %+v", diagnostic)
	default:
	}
	_ = clientConn.Close()
	select {
	case <-handled:
	case <-ctx.Done():
		t.Fatal("TLS handler did not stop")
	}
}

func TestServerTLSUsesLearnedProxyPreferenceWithoutDisablingFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	serverConn, clientConn := net.Pipe()
	helloWire := fragmentedClientHello(false)
	serverWire := validServerHelloRecord()
	direct := &sidecarTLSCandidate{path: model.PathDirect, handler: func(net.Conn) {}}
	proxy := &sidecarTLSCandidate{path: model.PathProxy, handler: func(conn net.Conn) {
		_, _ = io.CopyN(io.Discard, conn, int64(len(helloWire)))
		_, _ = conn.Write(serverWire)
	}}
	learner := &stubLearningEngine{
		preferred: model.PathProxy,
		update: learning.Update{
			Applied: true, ReasonCode: learning.ReasonPreferenceRefreshed,
			DurableReason: "durable_evidence_queued",
			Policy:        learning.Policy{State: model.StateProxyPreferred, PreferredPath: model.PathProxy},
		},
	}
	events := make(chan DecisionEvent, 1)
	server := Server{
		HandshakeTimeout: time.Second, Learning: learner,
		DirectProbePolicy: mustPrivacyPolicy(t, privacy.ModeExplicitOptIn, nil),
		TLSRacer: &transport.TLSRacer{
			Direct: direct, Proxy: proxy, Gate: transport.TLSServerHelloGate{},
			HeadStart: 100 * time.Millisecond, Timeout: time.Second,
		},
		OnDecision: func(event DecisionEvent) { events <- event },
	}
	go server.handle(ctx, serverConn)
	performSOCKSRequest(t, clientConn)
	if _, err := clientConn.Write(helloWire); err != nil {
		t.Fatal(err)
	}
	replayed := make([]byte, len(serverWire))
	if _, err := io.ReadFull(clientConn, replayed); err != nil {
		t.Fatal(err)
	}
	event := <-events
	if direct.attempts.Load() != 0 || proxy.attempts.Load() != 1 || learner.observed.Load() != 1 {
		t.Fatalf("attempts direct=%d proxy=%d observed=%d", direct.attempts.Load(), proxy.attempts.Load(), learner.observed.Load())
	}
	if event.ReasonCode != transport.ReasonProxyCandidateBeforeHeadStart || event.LearningReason != learning.ReasonPreferenceRefreshed ||
		event.DurableReason != "durable_evidence_queued" || event.PolicyState != model.StateProxyPreferred {
		t.Fatalf("event = %+v", event)
	}
	_ = clientConn.Close()
}

func TestServerTLSDurablePolicyAvoidsParallelCandidate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	serverConn, clientConn := net.Pipe()
	helloWire := fragmentedClientHello(false)
	serverWire := validServerHelloRecord()
	direct := &sidecarTLSCandidate{path: model.PathDirect, handler: func(net.Conn) {}}
	proxy := &sidecarTLSCandidate{path: model.PathProxy, handler: func(conn net.Conn) {
		_, _ = io.CopyN(io.Discard, conn, int64(len(helloWire)))
		_, _ = conn.Write(serverWire)
	}}
	learner := &stubLearningEngine{fixed: model.PathProxy, update: learning.Update{ReasonCode: learning.ReasonIncompleteEvidence}}
	events := make(chan DecisionEvent, 1)
	server := Server{
		HandshakeTimeout: time.Second, Learning: learner,
		DirectProbePolicy: mustPrivacyPolicy(t, privacy.ModeExplicitOptIn, nil),
		TLSRacer:          &transport.TLSRacer{Direct: direct, Proxy: proxy, Gate: transport.TLSServerHelloGate{}, Timeout: time.Second},
		OnDecision:        func(event DecisionEvent) { events <- event },
	}
	go server.handle(ctx, serverConn)
	performSOCKSRequest(t, clientConn)
	if _, err := clientConn.Write(helloWire); err != nil {
		t.Fatal(err)
	}
	replayed := make([]byte, len(serverWire))
	if _, err := io.ReadFull(clientConn, replayed); err != nil {
		t.Fatal(err)
	}
	event := <-events
	if direct.attempts.Load() != 0 || proxy.attempts.Load() != 1 || event.ReasonCode != transport.ReasonDurablePolicySelected || event.PolicyState != model.StateProxyPreferred {
		t.Fatalf("event=%+v direct=%d proxy=%d", event, direct.attempts.Load(), proxy.attempts.Load())
	}
	_ = clientConn.Close()
}

func TestServerLearningFailureDoesNotRejectWinner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	serverConn, clientConn := net.Pipe()
	helloWire := fragmentedClientHello(false)
	serverWire := validServerHelloRecord()
	direct := &sidecarTLSCandidate{path: model.PathDirect, handler: func(conn net.Conn) {
		_, _ = io.CopyN(io.Discard, conn, int64(len(helloWire)))
		_, _ = conn.Write(serverWire)
	}}
	learner := &stubLearningEngine{err: errors.New("synthetic learning failure")}
	events := make(chan DecisionEvent, 1)
	server := Server{
		HandshakeTimeout: time.Second, Learning: learner,
		DirectProbePolicy: mustPrivacyPolicy(t, privacy.ModeExplicitOptIn, nil),
		TLSRacer: &transport.TLSRacer{
			Direct: direct, Proxy: &sidecarTLSCandidate{path: model.PathProxy, handler: func(net.Conn) {}},
			Gate: transport.TLSServerHelloGate{}, HeadStart: 100 * time.Millisecond, Timeout: time.Second,
		},
		OnDecision: func(event DecisionEvent) { events <- event },
	}
	go server.handle(ctx, serverConn)
	performSOCKSRequest(t, clientConn)
	if _, err := clientConn.Write(helloWire); err != nil {
		t.Fatal(err)
	}
	replayed := make([]byte, len(serverWire))
	if _, err := io.ReadFull(clientConn, replayed); err != nil {
		t.Fatalf("winner was rejected after learning error: %v", err)
	}
	if event := <-events; !event.Committed || event.LearningReason != ReasonLearningUpdateError {
		t.Fatalf("event = %+v", event)
	}
	_ = clientConn.Close()
}

func TestServerStrongPairsPromoteEphemeralProxyFirst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	helloWire := fragmentedClientHello(false)
	serverWire := validServerHelloRecord()
	direct := &sidecarTLSCandidate{path: model.PathDirect, handler: func(conn net.Conn) {
		_, _ = io.CopyN(io.Discard, conn, int64(len(helloWire)))
	}}
	proxy := &sidecarTLSCandidate{path: model.PathProxy, handler: func(conn net.Conn) {
		_, _ = io.CopyN(io.Discard, conn, int64(len(helloWire)))
		_, _ = conn.Write(serverWire)
	}}
	learner, err := learning.New(learning.Config{
		Mode: learning.ModeEphemeralAuto, DirectPromotionWins: 3, ProxyPromotionWins: 2, TTL: time.Hour, MaxEntries: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan DecisionEvent, 3)
	server := Server{
		NetworkProfileID: "home", HandshakeTimeout: time.Second, Learning: learner,
		DirectProbePolicy: mustPrivacyPolicy(t, privacy.ModeExplicitOptIn, nil),
		TLSRacer: &transport.TLSRacer{
			Direct: direct, Proxy: proxy, Gate: transport.TLSServerHelloGate{},
			HeadStart: 100 * time.Millisecond, Timeout: time.Second,
		},
		OnDecision: func(event DecisionEvent) { events <- event },
	}

	var decisions []DecisionEvent
	for attempt := 0; attempt < 3; attempt++ {
		serverConn, clientConn := net.Pipe()
		go server.handle(ctx, serverConn)
		performSOCKSRequest(t, clientConn)
		if _, err := clientConn.Write(helloWire); err != nil {
			t.Fatal(err)
		}
		replayed := make([]byte, len(serverWire))
		if _, err := io.ReadFull(clientConn, replayed); err != nil {
			t.Fatal(err)
		}
		select {
		case event := <-events:
			decisions = append(decisions, event)
		case <-ctx.Done():
			t.Fatal("missing decision")
		}
		_ = clientConn.Close()
	}
	if decisions[1].PolicyState != model.StateProxyPreferred || decisions[1].LearningReason != learning.ReasonProxyPromoted {
		t.Fatalf("promotion decision = %+v", decisions[1])
	}
	if decisions[2].ReasonCode != transport.ReasonProxyCandidateBeforeHeadStart || direct.attempts.Load() != 2 || proxy.attempts.Load() != 3 {
		t.Fatalf("third decision=%+v attempts direct=%d proxy=%d", decisions[2], direct.attempts.Load(), proxy.attempts.Load())
	}
}

func TestServerTLSModeRejectsEarlyDataBeforeCandidateDial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	serverConn, clientConn := net.Pipe()
	direct := &sidecarTLSCandidate{path: model.PathDirect, handler: func(net.Conn) {}}
	proxy := &sidecarTLSCandidate{path: model.PathProxy, handler: func(net.Conn) {}}
	diagnostics := make(chan DiagnosticEvent, 1)
	server := Server{
		HandshakeTimeout: time.Second,
		TLSRacer: &transport.TLSRacer{
			Direct: direct, Proxy: proxy, Gate: transport.TLSServerHelloGate{},
			HeadStart: 10 * time.Millisecond, Timeout: time.Second,
		},
		OnDiagnostic: func(event DiagnosticEvent) { diagnostics <- event },
	}
	go server.handle(ctx, serverConn)
	performSOCKSRequest(t, clientConn)
	if _, err := clientConn.Write(fragmentedClientHello(true)); err != nil {
		t.Fatal(err)
	}
	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 1)
	if _, err := clientConn.Read(buffer); err == nil {
		t.Fatal("early-data connection remained open")
	}
	select {
	case event := <-diagnostics:
		if event.EventType != EventTypeDiagnostic || event.ReasonCode != ReasonClientHelloRejected || event.FailureClass != tlsinspect.FailureTLSEarlyData {
			t.Fatalf("diagnostic event = %+v", event)
		}
	case <-ctx.Done():
		t.Fatal("missing early-data diagnostic")
	}
	if direct.attempts.Load() != 0 || proxy.attempts.Load() != 0 {
		t.Fatalf("candidate attempts direct=%d proxy=%d", direct.attempts.Load(), proxy.attempts.Load())
	}
}

func TestServerTLSModeCompletesGoTLSHandshakeThroughWinner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverConn, clientConn := net.Pipe()
	certificate := testTLSCertificate(t)
	direct := &sidecarTLSCandidate{path: model.PathDirect, handler: func(conn net.Conn) {
		_, _ = tlsinspect.ReadClientHello(conn, 0)
		_, _ = io.Copy(io.Discard, conn)
	}}
	proxy := &sidecarTLSCandidate{path: model.PathProxy, handler: func(conn net.Conn) {
		server := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13})
		if err := server.HandshakeContext(ctx); err != nil {
			return
		}
		payload := make([]byte, 4)
		if _, err := io.ReadFull(server, payload); err == nil {
			_, _ = server.Write(payload)
		}
	}}
	events := make(chan DecisionEvent, 1)
	server := Server{
		HandshakeTimeout:  2 * time.Second,
		DirectProbePolicy: mustPrivacyPolicy(t, privacy.ModeExplicitOptIn, nil),
		TLSRacer: &transport.TLSRacer{
			Direct: direct, Proxy: proxy, Gate: transport.TLSServerHelloGate{},
			HeadStart: 10 * time.Millisecond, Timeout: 2 * time.Second,
		},
		OnDecision: func(event DecisionEvent) { events <- event },
	}
	go server.handle(ctx, serverConn)
	performSOCKSRequest(t, clientConn)
	client := tls.Client(clientConn, &tls.Config{
		ServerName: "echo.test", InsecureSkipVerify: true,
		MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13,
	})
	if err := client.HandshakeContext(ctx); err != nil {
		t.Fatalf("TLS handshake through sidecar: %v", err)
	}
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 4)
	if _, err := io.ReadFull(client, payload); err != nil || string(payload) != "ping" {
		t.Fatalf("TLS echo error=%v payload=%q", err, payload)
	}
	select {
	case event := <-events:
		if event.EventType != EventTypeDecision || event.SelectedPath != model.PathProxy || event.Observation.StageReached != model.StageTLS || !event.Committed {
			t.Fatalf("decision event = %+v", event)
		}
	case <-ctx.Done():
		t.Fatal("missing real TLS decision event")
	}
	_ = client.Close()
}

func TestServerPrivacyDenyUsesOnlyProxyTLSPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	serverConn, clientConn := net.Pipe()
	helloWire := fragmentedClientHello(false)
	serverWire := validServerHelloRecord()
	direct := &sidecarTLSCandidate{path: model.PathDirect, handler: func(net.Conn) {}}
	proxy := &sidecarTLSCandidate{path: model.PathProxy, handler: func(conn net.Conn) {
		received := make([]byte, len(helloWire))
		if _, err := io.ReadFull(conn, received); err == nil && bytes.Equal(received, helloWire) {
			_, _ = conn.Write(serverWire)
		}
	}}
	events := make(chan DecisionEvent, 1)
	learner := &stubLearningEngine{}
	decisionStart := time.Unix(100, 0)
	var clockCalls atomic.Int32
	server := Server{
		HandshakeTimeout:  time.Second,
		DirectProbePolicy: mustPrivacyPolicy(t, privacy.ModeExplicitOptIn, []string{"echo.test"}),
		Learning:          learner,
		Clock: func() time.Time {
			if clockCalls.Add(1) == 1 {
				return decisionStart
			}
			return decisionStart.Add(125 * time.Millisecond)
		},
		TLSRacer: &transport.TLSRacer{
			Direct: direct, Proxy: proxy, Gate: transport.TLSServerHelloGate{}, Timeout: time.Second,
		},
		OnDecision: func(event DecisionEvent) { events <- event },
	}
	go server.handle(ctx, serverConn)
	performSOCKSRequest(t, clientConn)
	if _, err := clientConn.Write(helloWire); err != nil {
		t.Fatal(err)
	}
	replayed := make([]byte, len(serverWire))
	if _, err := io.ReadFull(clientConn, replayed); err != nil || !bytes.Equal(replayed, serverWire) {
		t.Fatalf("server replay error=%v bytes=%x", err, replayed)
	}
	event := <-events
	if direct.attempts.Load() != 0 || proxy.attempts.Load() != 1 || event.SelectedPath != model.PathProxy || event.ReasonCode != privacy.ReasonNeverDirectExact || event.PolicyReason != privacy.ReasonNeverDirectExact || !event.Committed || event.DecisionLatencyMS == nil || *event.DecisionLatencyMS != 125 {
		t.Fatalf("attempts direct=%d proxy=%d event=%+v", direct.attempts.Load(), proxy.attempts.Load(), event)
	}
	if learner.pathSucceeded.Load() != 1 {
		t.Fatalf("health successes=%d", learner.pathSucceeded.Load())
	}
	_ = clientConn.Close()
}

func TestServerMissingPrivacyPolicyFailsClosedToProxyOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	serverConn, clientConn := net.Pipe()
	helloWire := fragmentedClientHello(false)
	direct := &sidecarTLSCandidate{path: model.PathDirect, handler: func(net.Conn) {}}
	proxy := &sidecarTLSCandidate{path: model.PathProxy, handler: func(conn net.Conn) {
		_, _ = io.CopyN(io.Discard, conn, int64(len(helloWire)))
		_, _ = conn.Write(validServerHelloRecord())
	}}
	events := make(chan DecisionEvent, 1)
	server := Server{
		HandshakeTimeout: time.Second,
		TLSRacer: &transport.TLSRacer{
			Direct: direct, Proxy: proxy, Gate: transport.TLSServerHelloGate{}, Timeout: time.Second,
		},
		OnDecision: func(event DecisionEvent) { events <- event },
	}
	go server.handle(ctx, serverConn)
	performSOCKSRequest(t, clientConn)
	if _, err := clientConn.Write(helloWire); err != nil {
		t.Fatal(err)
	}
	replayed := make([]byte, len(validServerHelloRecord()))
	if _, err := io.ReadFull(clientConn, replayed); err != nil {
		t.Fatal(err)
	}
	event := <-events
	if direct.attempts.Load() != 0 || proxy.attempts.Load() != 1 || event.ReasonCode != privacy.ReasonMissingRuntimePolicy {
		t.Fatalf("attempts direct=%d proxy=%d event=%+v", direct.attempts.Load(), proxy.attempts.Load(), event)
	}
	_ = clientConn.Close()
}

func TestServerPrivacyProxyOnlyFailureIsStructured(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	serverConn, clientConn := net.Pipe()
	helloWire := fragmentedClientHello(false)
	direct := &sidecarTLSCandidate{path: model.PathDirect, handler: func(net.Conn) {}}
	proxy := &sidecarTLSCandidate{path: model.PathProxy, handler: func(conn net.Conn) {
		_, _ = io.CopyN(io.Discard, conn, int64(len(helloWire)))
	}}
	diagnostics := make(chan DiagnosticEvent, 1)
	learner := &stubLearningEngine{}
	server := Server{
		HandshakeTimeout:  time.Second,
		DirectProbePolicy: mustPrivacyPolicy(t, privacy.ModePrivacyFirst, nil),
		Learning:          learner,
		TLSRacer: &transport.TLSRacer{
			Direct: direct, Proxy: proxy, Gate: transport.TLSServerHelloGate{}, Timeout: time.Second,
		},
		OnDiagnostic: func(event DiagnosticEvent) { diagnostics <- event },
	}
	go server.handle(ctx, serverConn)
	performSOCKSRequest(t, clientConn)
	if _, err := clientConn.Write(helloWire); err != nil {
		t.Fatal(err)
	}
	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 1)
	if _, err := clientConn.Read(buffer); err == nil {
		t.Fatal("failed privacy-only Proxy path left connection open")
	}
	event := <-diagnostics
	if event.ReasonCode != ReasonPrivacyProxyPathFailed || event.PolicyReason != privacy.ReasonPrivacyFirst || event.DirectFailure != "skipped_by_privacy" || event.ProxyFailure == "" || direct.attempts.Load() != 0 || proxy.attempts.Load() != 1 {
		t.Fatalf("attempts direct=%d proxy=%d event=%+v", direct.attempts.Load(), proxy.attempts.Load(), event)
	}
	if learner.proxyFailed.Load() != 1 || learner.bothFailed.Load() != 0 {
		t.Fatalf("health proxy=%d both=%d", learner.proxyFailed.Load(), learner.bothFailed.Load())
	}
}

func TestServerBothTLSFailuresNotifyLearningHealth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	serverConn, clientConn := net.Pipe()
	direct := &sidecarTLSCandidate{path: model.PathDirect, handler: func(net.Conn) {}}
	proxy := &sidecarTLSCandidate{path: model.PathProxy, handler: func(net.Conn) {}}
	learner := &stubLearningEngine{}
	server := Server{HandshakeTimeout: time.Second,
		DirectProbePolicy: mustPrivacyPolicy(t, privacy.ModeExplicitOptIn, nil), Learning: learner,
		TLSRacer: &transport.TLSRacer{Direct: direct, Proxy: proxy, Gate: transport.TLSServerHelloGate{}, HeadStart: time.Millisecond, Timeout: time.Second}}
	go server.handle(ctx, serverConn)
	performSOCKSRequest(t, clientConn)
	if _, err := clientConn.Write(fragmentedClientHello(false)); err != nil {
		t.Fatal(err)
	}
	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))
	_, _ = clientConn.Read(make([]byte, 1))
	if learner.bothFailed.Load() != 1 || learner.proxyFailed.Load() != 0 {
		t.Fatalf("health both=%d proxy=%d", learner.bothFailed.Load(), learner.proxyFailed.Load())
	}
}

func TestHealthRelevantFailureRejectsCancellationAndPreOutbound(t *testing.T) {
	for _, observation := range []model.Observation{
		{Path: model.PathDirect, StageReached: model.StageOutbound, FailureClass: "canceled"},
		{Path: model.PathProxy, StageReached: model.StageOutbound, FailureClass: "not_started"},
		{Path: model.PathDirect, StageReached: model.StageDNS, FailureClass: "timeout"},
		{Path: model.PathProxy, Success: true, StageReached: model.StageTLS},
	} {
		if healthRelevantFailure(observation) {
			t.Fatalf("accepted irrelevant failure: %+v", observation)
		}
	}
	if !healthRelevantFailure(model.Observation{Path: model.PathProxy, StageReached: model.StageOutbound, FailureClass: "tls_eof"}) {
		t.Fatal("rejected completed outbound failure")
	}
}

func mustPrivacyPolicy(t *testing.T, mode string, patterns []string) privacy.Policy {
	t.Helper()
	policy, err := privacy.New(mode, patterns)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func performSOCKSRequest(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(conn, methodReply); err != nil {
		t.Fatal(err)
	}
	request := append([]byte{0x05, 0x01, 0x00, 0x03, 0x09}, []byte("echo.test")...)
	request = append(request, 0x01, 0xbb)
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != byte(socks5.ReplySucceeded) {
		t.Fatalf("SOCKS reply = 0x%02x", reply[1])
	}
}

func fragmentedClientHello(withEarlyData bool) []byte {
	extensions := []byte{}
	if withEarlyData {
		extensions = []byte{0x00, 0x2a, 0x00, 0x00}
	}
	body := []byte{0x03, 0x03}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0x00, 0x00, 0x02, 0x13, 0x01, 0x01, 0x00)
	body = append(body, byte(len(extensions)>>8), byte(len(extensions)))
	body = append(body, extensions...)
	handshake := append([]byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
	split := 13
	return append(tlsRecord(22, handshake[:split]), tlsRecord(22, handshake[split:])...)
}

func validServerHelloRecord() []byte {
	body := []byte{0x03, 0x03}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0x00, 0x13, 0x01, 0x00, 0x00, 0x06, 0x00, 0x2b, 0x00, 0x02, 0x03, 0x04)
	handshake := append([]byte{2, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
	return tlsRecord(22, handshake)
}

func tlsRecord(contentType byte, payload []byte) []byte {
	return append([]byte{contentType, 0x03, 0x03, byte(len(payload) >> 8), byte(len(payload))}, payload...)
}

func testTLSCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		DNSNames:     []string{"echo.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}
