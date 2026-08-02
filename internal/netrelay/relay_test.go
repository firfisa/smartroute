package netrelay

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestBidirectionalCancellationClosesBothOwnedConnections(t *testing.T) {
	leftClient, leftRelay := net.Pipe()
	rightRelay, rightPeer := net.Pipe()
	defer leftClient.Close()
	defer rightPeer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() {
		done <- Bidirectional(ctx, leftRelay, rightRelay)
	}()

	payload := []byte("payload")
	writeDone := make(chan error, 1)
	go func() {
		_, err := leftClient.Write(payload)
		writeDone <- err
	}()
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(rightPeer, received); err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if string(received) != string(payload) {
		t.Fatalf("received %q", received)
	}

	cancel()
	select {
	case result := <-done:
		if result.LeftToRightBytes != int64(len(payload)) || result.RightToLeftBytes != 0 || !result.Canceled {
			t.Fatalf("result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not stop after context cancellation")
	}
	if _, err := leftClient.Write([]byte("after-cancel")); err == nil {
		t.Fatal("left relay connection remained open after cancellation")
	}
	if _, err := rightPeer.Write([]byte("after-cancel")); err == nil {
		t.Fatal("right relay connection remained open after cancellation")
	}
}
