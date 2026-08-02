package sidecar

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
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
