package sidecar

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/socks5"
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
		HandshakeTimeout: time.Second,
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
		if event.Committed || event.ReasonCode != ReasonCandidateBelowCommitStage || event.Observation.StageReached != model.StageOutbound {
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
