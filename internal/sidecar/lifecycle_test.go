package sidecar

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/connectionid"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/netrelay"
	"github.com/firfisa/smartroute/internal/transport"
)

func TestServeCancellationDrainsPendingHandshake(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	listener := newOneConnectionListener(serverConn)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Server{HandshakeTimeout: time.Hour}).Serve(ctx, listener) }()
	<-listener.accepted

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not wait for and drain the pending handler")
	}
	if _, err := clientConn.Write([]byte{0x05}); err == nil {
		t.Fatal("pending inbound connection remained open after Serve returned")
	}
}

func TestServeRejectsInvalidDeclaredBaselineBeforeAccept(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	listener := newOneConnectionListener(serverConn)
	err := (Server{DeclaredBaselinePath: model.PathOriginal}).Serve(context.Background(), listener)
	if err == nil || !strings.Contains(err.Error(), "declared baseline path") {
		t.Fatalf("Serve() error=%v", err)
	}
}

func TestRelayOutcomeCountsPostCommitBytesWithoutPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	peers := make(chan net.Conn, 1)
	decisions := make(chan DecisionEvent, 1)
	outcomes := make(chan RelayOutcomeEvent, 1)
	times := make(chan time.Time, 2)
	started := time.Unix(100, 0)
	times <- started
	times <- started.Add(250 * time.Millisecond)
	server := Server{
		HandshakeTimeout: time.Second, DeclaredBaselinePath: model.PathProxy,
		Racer: transport.Racer{
			Direct:    stageDialer{path: model.PathDirect, stage: model.StageTCP, peer: peers},
			Proxy:     stageDialer{path: model.PathProxy, stage: model.StageTCP, peer: make(chan net.Conn, 1)},
			HeadStart: 100 * time.Millisecond, Timeout: time.Second,
		},
		Clock: func() time.Time { return <-times },
		OnDecision: func(event DecisionEvent) {
			decisions <- event
		},
		OnRelayOutcome: func(event RelayOutcomeEvent) {
			outcomes <- event
		},
	}
	handled := make(chan struct{})
	go func() {
		server.handle(context.Background(), serverConn)
		close(handled)
	}()
	performSOCKSRequest(t, clientConn)
	peer := <-peers
	decision := <-decisions
	if err := connectionid.Validate(decision.ConnectionID); err != nil {
		t.Fatalf("decision connection ID: %v", err)
	}
	if decision.DeclaredBaselinePath != model.PathProxy {
		t.Fatalf("decision baseline=%q", decision.DeclaredBaselinePath)
	}

	request := []byte("request-body")
	requestWritten := make(chan error, 1)
	go func() {
		_, err := clientConn.Write(request)
		requestWritten <- err
	}()
	receivedRequest := make([]byte, len(request))
	if _, err := io.ReadFull(peer, receivedRequest); err != nil {
		t.Fatal(err)
	}
	if err := <-requestWritten; err != nil {
		t.Fatal(err)
	}

	response := []byte("response-body")
	responseWritten := make(chan error, 1)
	go func() {
		_, err := peer.Write(response)
		responseWritten <- err
	}()
	receivedResponse := make([]byte, len(response))
	if _, err := io.ReadFull(clientConn, receivedResponse); err != nil {
		t.Fatal(err)
	}
	if err := <-responseWritten; err != nil {
		t.Fatal(err)
	}
	_ = peer.Close()
	_ = clientConn.Close()

	select {
	case outcome := <-outcomes:
		if outcome.EventType != EventTypeRelayOutcome || outcome.SelectedPath != model.PathDirect ||
			outcome.ClientToRemoteBytes != int64(len(request)) || outcome.RemoteToClientBytes != int64(len(response)) ||
			!outcome.ClientToRemoteEnd.Valid() || !outcome.RemoteToClientEnd.Valid() ||
			outcome.ClientToRemoteEnd == netrelay.EndCanceled || outcome.RemoteToClientEnd == netrelay.EndCanceled ||
			outcome.RelayDurationMS != 250 || outcome.Termination != RelayTerminationEnded {
			t.Fatalf("outcome = %+v", outcome)
		}
		if outcome.ConnectionID != decision.ConnectionID {
			t.Fatalf("decision connection=%q outcome connection=%q", decision.ConnectionID, outcome.ConnectionID)
		}
		if outcome.DeclaredBaselinePath != decision.DeclaredBaselinePath {
			t.Fatalf("decision baseline=%q outcome baseline=%q", decision.DeclaredBaselinePath, outcome.DeclaredBaselinePath)
		}
		if outcome.Target.Hostname != "echo.test" {
			t.Fatalf("target = %+v", outcome.Target)
		}
		encoded, err := json.Marshal(outcome)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), string(request)) || strings.Contains(string(encoded), string(response)) {
			t.Fatalf("relay outcome leaked copied payload: %s", encoded)
		}
	case <-time.After(time.Second):
		t.Fatal("missing relay outcome")
	}
	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish after relay endpoints closed")
	}
}

func TestConnectionIDGenerationFailureLeavesEventsExplicitlyUnscoped(t *testing.T) {
	server := Server{OnDecision: func(DecisionEvent) {}}
	server.ConnectionIDGenerator = func() (string, error) { return "", errors.New("entropy unavailable") }
	if value := server.connectionID(); value != "" {
		t.Fatalf("failed generator returned %q", value)
	}
	server.ConnectionIDGenerator = func() (string, error) { return "personal-label", nil }
	if value := server.connectionID(); value != "" {
		t.Fatalf("invalid generator returned %q", value)
	}
	valid := "conn-0123456789abcdef0123456789abcdef"
	server.ConnectionIDGenerator = func() (string, error) { return valid, nil }
	if value := server.connectionID(); value != valid {
		t.Fatalf("valid generator returned %q", value)
	}
}

type oneConnectionListener struct {
	connections chan net.Conn
	accepted    chan struct{}
	closed      chan struct{}
	acceptOnce  sync.Once
	closeOnce   sync.Once
}

func newOneConnectionListener(conn net.Conn) *oneConnectionListener {
	listener := &oneConnectionListener{
		connections: make(chan net.Conn, 1), accepted: make(chan struct{}), closed: make(chan struct{}),
	}
	listener.connections <- conn
	return listener
}

func (l *oneConnectionListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connections:
		l.acceptOnce.Do(func() { close(l.accepted) })
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *oneConnectionListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *oneConnectionListener) Addr() net.Addr { return &net.TCPAddr{} }
