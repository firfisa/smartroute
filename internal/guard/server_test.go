package guard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/socks5"
)

type controlledDialer struct {
	err            error
	waitForContext bool
	attempts       int
	request        socks5.Request
	peer           chan net.Conn
}

func (d *controlledDialer) Dial(ctx context.Context, request socks5.Request) (net.Conn, error) {
	d.attempts++
	d.request = request
	if d.waitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if d.err != nil {
		return nil, d.err
	}
	client, server := net.Pipe()
	d.peer <- server
	return client, nil
}

func TestGuardBoundsWedgedAdaptiveHandshakeBeforeOriginal(t *testing.T) {
	adaptive := &controlledDialer{waitForContext: true, peer: make(chan net.Conn, 1)}
	original := &controlledDialer{peer: make(chan net.Conn, 1)}
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	events := make(chan DecisionEvent, 1)
	server := Server{
		Adaptive: adaptive, Original: original, AdaptiveTimeout: 20 * time.Millisecond,
		HandshakeTimeout: time.Second, OnDecision: func(event DecisionEvent) { events <- event },
	}
	go server.handle(context.Background(), serverConn)
	started := time.Now()
	if reply := performRequest(t, clientConn); reply != socks5.ReplySucceeded {
		t.Fatalf("reply = %v", reply)
	}
	peer := <-original.peer
	defer peer.Close()
	event := <-events
	if event.SelectedLane != LaneOriginal || event.AdaptiveFailure != "timeout" || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("event=%+v elapsed=%s", event, time.Since(started))
	}
}

func TestGuardUsesAdaptiveWithoutTouchingOriginal(t *testing.T) {
	adaptive := &controlledDialer{peer: make(chan net.Conn, 1)}
	original := &controlledDialer{peer: make(chan net.Conn, 1)}
	event, payload := exerciseGuard(t, adaptive, original)
	if event.SelectedLane != LaneAdaptive || event.ReasonCode != ReasonAdaptiveAvailable || !event.Committed {
		t.Fatalf("event = %+v", event)
	}
	if original.attempts != 0 || adaptive.request.Host != "echo.test" || string(payload) != "guard-payload" {
		t.Fatalf("adaptive=%+v original attempts=%d payload=%q", adaptive.request, original.attempts, payload)
	}
}

func TestGuardFallsBackCurrentConnectionBeforePayload(t *testing.T) {
	adaptive := &controlledDialer{err: errors.New("connection refused"), peer: make(chan net.Conn, 1)}
	original := &controlledDialer{peer: make(chan net.Conn, 1)}
	event, payload := exerciseGuard(t, adaptive, original)
	if event.SelectedLane != LaneOriginal || event.ReasonCode != ReasonAdaptiveUnavailableUseOriginal || event.AdaptiveFailure != "unavailable" || !event.Committed {
		t.Fatalf("event = %+v", event)
	}
	if adaptive.attempts != 1 || original.attempts != 1 || original.request.Host != "echo.test" || string(payload) != "guard-payload" {
		t.Fatalf("attempts adaptive=%d original=%d request=%+v payload=%q", adaptive.attempts, original.attempts, original.request, payload)
	}
}

func TestGuardRejectsWhenBothEndpointsUnavailable(t *testing.T) {
	adaptive := &controlledDialer{err: errors.New("down"), peer: make(chan net.Conn, 1)}
	original := &controlledDialer{err: errors.New("down"), peer: make(chan net.Conn, 1)}
	serverConn, clientConn := net.Pipe()
	events := make(chan DecisionEvent, 1)
	server := Server{Adaptive: adaptive, Original: original, HandshakeTimeout: time.Second, OnDecision: func(event DecisionEvent) { events <- event }}
	go server.handle(context.Background(), serverConn)
	reply := performRequest(t, clientConn)
	if reply != socks5.ReplyConnectionRefused {
		t.Fatalf("reply = %v", reply)
	}
	event := <-events
	if event.Committed || event.ReasonCode != ReasonAllGuardPathsUnavailable || event.AdaptiveFailure == "" || event.OriginalFailure == "" {
		t.Fatalf("event = %+v", event)
	}
}

func exerciseGuard(t *testing.T, adaptive, original *controlledDialer) (DecisionEvent, []byte) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	events := make(chan DecisionEvent, 1)
	server := Server{Adaptive: adaptive, Original: original, HandshakeTimeout: time.Second, OnDecision: func(event DecisionEvent) { events <- event }}
	go server.handle(context.Background(), serverConn)
	if reply := performRequest(t, clientConn); reply != socks5.ReplySucceeded {
		t.Fatalf("reply = %v", reply)
	}
	var peer net.Conn
	if adaptive.err == nil {
		peer = <-adaptive.peer
	} else {
		peer = <-original.peer
	}
	defer peer.Close()
	payload := []byte("guard-payload")
	written := make(chan error, 1)
	go func() {
		_, err := clientConn.Write(payload)
		written <- err
	}()
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(peer, received); err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received, payload) {
		t.Fatalf("payload = %q", received)
	}
	return <-events, received
}

func performRequest(t *testing.T, conn net.Conn) socks5.Reply {
	t.Helper()
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(conn, method); err != nil {
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
	return socks5.Reply(reply[1])
}
