package netrelay

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
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
		if result.LeftToRightBytes != int64(len(payload)) || result.RightToLeftBytes != 0 || !result.Canceled ||
			result.LeftToRightEnd != EndCanceled || result.RightToLeftEnd != EndCanceled {
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

func TestBidirectionalReportsEOFWhenBothPeersClose(t *testing.T) {
	leftClient, leftRelay := net.Pipe()
	rightRelay, rightPeer := net.Pipe()
	done := make(chan Result, 1)
	go func() { done <- Bidirectional(context.Background(), leftRelay, rightRelay) }()
	if err := leftClient.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rightPeer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.Canceled || result.LeftToRightEnd != EndEOF || result.RightToLeftEnd != EndEOF {
			t.Fatalf("result=%+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not finish after both peer EOFs")
	}
}

func TestClassifyCopyEndUsesBoundedReasons(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want EndReason
	}{
		{name: "eof", err: nil, want: EndEOF},
		{name: "timeout", err: timeoutError{}, want: EndTimeout},
		{name: "reset", err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}, want: EndReset},
		{name: "closed", err: net.ErrClosed, want: EndClosed},
		{name: "other", err: errors.New("sensitive endpoint detail"), want: EndIOError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyCopyEnd(test.err); got != test.want {
				t.Fatalf("classifyCopyEnd()=%q want %q", got, test.want)
			}
		})
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "private timeout detail" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
