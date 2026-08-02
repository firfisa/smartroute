package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/tlsinspect"
)

type pipeCandidateDialer struct {
	path     model.Path
	handler  func(net.Conn)
	attempts atomic.Int32
}

func (d *pipeCandidateDialer) Dial(_ context.Context, _ model.Target) (net.Conn, model.Observation, error) {
	d.attempts.Add(1)
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		d.handler(server)
	}()
	return client, model.Observation{Path: d.path, Success: true, StageReached: model.StageOutbound}, nil
}

func TestTLSRacerSelectsFirstServerHelloAndReplaysPrefix(t *testing.T) {
	hello, wire := parsedClientHello(t)
	serverWire := syntheticServerHelloRecord()
	directClosed := make(chan struct{})
	direct := &pipeCandidateDialer{path: model.PathDirect, handler: func(conn net.Conn) {
		received := make([]byte, len(wire))
		_, _ = io.ReadFull(conn, received)
		buffer := make([]byte, 1)
		_, _ = conn.Read(buffer)
		close(directClosed)
	}}
	proxy := &pipeCandidateDialer{path: model.PathProxy, handler: func(conn net.Conn) {
		received := make([]byte, len(wire))
		if _, err := io.ReadFull(conn, received); err == nil && bytes.Equal(received, wire) {
			_, _ = conn.Write(serverWire)
		}
	}}
	racer := TLSRacer{
		Direct: direct, Proxy: proxy, Gate: TLSServerHelloGate{},
		HeadStart: 10 * time.Millisecond, Timeout: time.Second,
	}
	result, err := racer.Race(context.Background(), testTarget(), hello)
	if err != nil {
		t.Fatalf("Race() error = %v", err)
	}
	defer result.Conn.Close()
	if result.Observation.Path != model.PathProxy || result.Observation.StageReached != model.StageTLS || result.ReasonCode != ReasonProxyCandidateWon {
		t.Fatalf("Race() = %+v", result)
	}
	replayed := make([]byte, len(serverWire))
	if _, err := io.ReadFull(result.Conn, replayed); err != nil || !bytes.Equal(replayed, serverWire) {
		t.Fatalf("prefetched ServerHello replay error=%v bytes=%x", err, replayed)
	}
	select {
	case <-directClosed:
	case <-time.After(time.Second):
		t.Fatal("losing TLS candidate was not canceled")
	}
}

func TestTLSRacerDirectServerHelloAvoidsProxy(t *testing.T) {
	hello, wire := parsedClientHello(t)
	serverWire := syntheticServerHelloRecord()
	direct := &pipeCandidateDialer{path: model.PathDirect, handler: func(conn net.Conn) {
		_, _ = io.CopyN(io.Discard, conn, int64(len(wire)))
		_, _ = conn.Write(serverWire)
	}}
	proxy := &pipeCandidateDialer{path: model.PathProxy, handler: func(net.Conn) {}}
	result, err := (TLSRacer{
		Direct: direct, Proxy: proxy, Gate: TLSServerHelloGate{},
		HeadStart: 100 * time.Millisecond, Timeout: time.Second,
	}).Race(context.Background(), testTarget(), hello)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Conn.Close()
	if result.Observation.Path != model.PathDirect || result.ReasonCode != ReasonDirectCandidateBeforeHeadStart {
		t.Fatalf("Race() = %+v", result)
	}
	if proxy.attempts.Load() != 0 {
		t.Fatalf("proxy attempts = %d", proxy.attempts.Load())
	}
}

func TestTLSRacerProxyPreferenceAvoidsDirectWhenProxyReady(t *testing.T) {
	hello, wire := parsedClientHello(t)
	serverWire := syntheticServerHelloRecord()
	direct := &pipeCandidateDialer{path: model.PathDirect, handler: func(net.Conn) {}}
	proxy := &pipeCandidateDialer{path: model.PathProxy, handler: func(conn net.Conn) {
		_, _ = io.CopyN(io.Discard, conn, int64(len(wire)))
		_, _ = conn.Write(serverWire)
	}}
	result, err := (TLSRacer{
		Direct: direct, Proxy: proxy, Gate: TLSServerHelloGate{},
		HeadStart: 100 * time.Millisecond, Timeout: time.Second,
	}).RacePreferred(context.Background(), testTarget(), hello, model.PathProxy)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Conn.Close()
	if result.Observation.Path != model.PathProxy || result.ReasonCode != ReasonProxyCandidateBeforeHeadStart || direct.attempts.Load() != 0 {
		t.Fatalf("direct=%d result=%+v", direct.attempts.Load(), result)
	}
}

func TestTLSRacerBothPathsCloseBeforeServerHello(t *testing.T) {
	hello, wire := parsedClientHello(t)
	closedHandler := func(conn net.Conn) { _, _ = io.CopyN(io.Discard, conn, int64(len(wire))) }
	direct := &pipeCandidateDialer{path: model.PathDirect, handler: closedHandler}
	proxy := &pipeCandidateDialer{path: model.PathProxy, handler: closedHandler}
	_, err := (TLSRacer{
		Direct: direct, Proxy: proxy, Gate: TLSServerHelloGate{},
		HeadStart: time.Millisecond, Timeout: time.Second,
	}).Race(context.Background(), testTarget(), hello)
	var raceError *RaceError
	if !errors.As(err, &raceError) {
		t.Fatalf("Race() error = %v", err)
	}
	if raceError.Direct.FailureClass != FailureTLSConnectionClosed || raceError.Proxy.FailureClass != FailureTLSConnectionClosed {
		t.Fatalf("RaceError = %+v", raceError)
	}
}

func TestTLSServerHelloGateClassifiesAlertAtTLSStage(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() {
		defer server.Close()
		_, _ = server.Write(tlsRecordForTest(21, []byte{2, 40}))
	}()
	result, err := (TLSServerHelloGate{}).Await(context.Background(), client, testTarget())
	if err == nil || result.FailureClass != tlsinspect.FailureTLSAlert || result.StageReached != model.StageTLS {
		t.Fatalf("Await() result=%+v error=%v", result, err)
	}
}

func TestTLSRacerRequiresBothCandidates(t *testing.T) {
	hello, _ := parsedClientHello(t)
	_, err := (TLSRacer{Gate: TLSServerHelloGate{}, HeadStart: time.Millisecond, Timeout: time.Second}).Race(
		context.Background(), testTarget(), hello,
	)
	if err == nil {
		t.Fatal("Race() error = nil")
	}
}

func TestTLSRacerConnectPathUsesOnlySelectedProxyAndReplaysPrefix(t *testing.T) {
	hello, wire := parsedClientHello(t)
	serverWire := syntheticServerHelloRecord()
	direct := &pipeCandidateDialer{path: model.PathDirect, handler: func(net.Conn) {}}
	proxy := &pipeCandidateDialer{path: model.PathProxy, handler: func(conn net.Conn) {
		received := make([]byte, len(wire))
		if _, err := io.ReadFull(conn, received); err == nil && bytes.Equal(received, wire) {
			_, _ = conn.Write(serverWire)
		}
	}}
	result, err := (TLSRacer{
		Direct: direct, Proxy: proxy, Gate: TLSServerHelloGate{}, Timeout: time.Second,
	}).ConnectPath(context.Background(), testTarget(), hello, model.PathProxy)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Conn.Close()
	if direct.attempts.Load() != 0 || proxy.attempts.Load() != 1 || result.ReasonCode != ReasonProxyPolicyOnly {
		t.Fatalf("attempts direct=%d proxy=%d result=%+v", direct.attempts.Load(), proxy.attempts.Load(), result)
	}
	replayed := make([]byte, len(serverWire))
	if _, err := io.ReadFull(result.Conn, replayed); err != nil || !bytes.Equal(replayed, serverWire) {
		t.Fatalf("prefetched ServerHello replay error=%v bytes=%x", err, replayed)
	}
}

func TestTLSRacerConnectPathReportsSelectedFailure(t *testing.T) {
	hello, wire := parsedClientHello(t)
	proxy := &pipeCandidateDialer{path: model.PathProxy, handler: func(conn net.Conn) {
		_, _ = io.CopyN(io.Discard, conn, int64(len(wire)))
	}}
	_, err := (TLSRacer{Proxy: proxy, Gate: TLSServerHelloGate{}, Timeout: time.Second}).ConnectPath(
		context.Background(), testTarget(), hello, model.PathProxy,
	)
	var pathError *TLSPathError
	if !errors.As(err, &pathError) || pathError.Observation.Path != model.PathProxy || pathError.Observation.FailureClass != FailureTLSConnectionClosed {
		t.Fatalf("ConnectPath() error = %#v", err)
	}
}

func parsedClientHello(t *testing.T) (tlsinspect.ClientHello, []byte) {
	t.Helper()
	body := []byte{0x03, 0x03}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0x00, 0x00, 0x02, 0x13, 0x01, 0x01, 0x00, 0x00, 0x00)
	handshake := append([]byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
	wire := tlsRecordForTest(22, handshake)
	hello, err := tlsinspect.ReadClientHello(bytes.NewReader(wire), 0)
	if err != nil {
		t.Fatal(err)
	}
	return hello, wire
}

func syntheticServerHelloRecord() []byte {
	body := []byte{0x03, 0x03}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0x00, 0x13, 0x01, 0x00, 0x00, 0x06, 0x00, 0x2b, 0x00, 0x02, 0x03, 0x04)
	handshake := append([]byte{2, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
	return tlsRecordForTest(22, handshake)
}

func tlsRecordForTest(contentType byte, payload []byte) []byte {
	return append([]byte{contentType, 0x03, 0x03, byte(len(payload) >> 8), byte(len(payload))}, payload...)
}
