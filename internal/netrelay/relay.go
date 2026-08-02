// Package netrelay provides byte-transparent, bidirectional connection relay.
package netrelay

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

type Result struct {
	LeftToRightBytes int64
	RightToLeftBytes int64
	Canceled         bool
}

// Bidirectional relays until both directions finish. Cancellation closes both
// owned connections so a peer that never responds cannot outlive its server.
func Bidirectional(ctx context.Context, left, right net.Conn) Result {
	finished := make(chan struct{})
	closerDone := make(chan struct{})
	canceled := make(chan struct{}, 1)
	go func() {
		defer close(closerDone)
		select {
		case <-ctx.Done():
			select {
			case <-finished:
				return
			default:
			}
			canceled <- struct{}{}
			_ = left.Close()
			_ = right.Close()
		case <-finished:
		}
	}()

	type directionResult struct {
		leftToRight bool
		bytes       int64
	}
	directions := make(chan directionResult, 2)
	var wait sync.WaitGroup
	var remaining atomic.Int32
	remaining.Store(2)
	wait.Add(2)
	copyDirection := func(dst, src net.Conn, leftToRight bool) {
		defer func() {
			if remaining.Add(-1) == 0 {
				close(finished)
			}
			wait.Done()
		}()
		copied, _ := io.Copy(dst, src)
		directions <- directionResult{leftToRight: leftToRight, bytes: copied}
		if closeWriter, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		}
	}
	go copyDirection(right, left, true)
	go copyDirection(left, right, false)
	wait.Wait()
	<-closerDone
	close(directions)
	var result Result
	for direction := range directions {
		if direction.leftToRight {
			result.LeftToRightBytes = direction.bytes
		} else {
			result.RightToLeftBytes = direction.bytes
		}
	}
	select {
	case <-canceled:
		result.Canceled = true
	default:
	}
	return result
}
