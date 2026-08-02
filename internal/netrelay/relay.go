// Package netrelay provides byte-transparent, bidirectional connection relay.
package netrelay

import (
	"context"
	"io"
	"net"
	"sync"
)

// Bidirectional relays until both directions finish. Cancellation closes both
// owned connections so a peer that never responds cannot outlive its server.
func Bidirectional(ctx context.Context, left, right net.Conn) {
	finished := make(chan struct{})
	closerDone := make(chan struct{})
	go func() {
		defer close(closerDone)
		select {
		case <-ctx.Done():
			_ = left.Close()
			_ = right.Close()
		case <-finished:
		}
	}()

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
	close(finished)
	<-closerDone
}
