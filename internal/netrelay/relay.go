// Package netrelay provides byte-transparent, bidirectional connection relay.
package netrelay

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
)

type Result struct {
	LeftToRightBytes int64
	RightToLeftBytes int64
	LeftToRightEnd   EndReason
	RightToLeftEnd   EndReason
	Canceled         bool
}

type EndReason string

const (
	EndEOF      EndReason = "eof"
	EndTimeout  EndReason = "timeout"
	EndReset    EndReason = "reset"
	EndClosed   EndReason = "closed"
	EndIOError  EndReason = "io_error"
	EndCanceled EndReason = "canceled"
)

func (r EndReason) Valid() bool {
	switch r {
	case EndEOF, EndTimeout, EndReset, EndClosed, EndIOError, EndCanceled:
		return true
	default:
		return false
	}
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
		end         EndReason
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
		copied, err := io.Copy(dst, src)
		directions <- directionResult{leftToRight: leftToRight, bytes: copied, end: classifyCopyEnd(err)}
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
			result.LeftToRightEnd = direction.end
		} else {
			result.RightToLeftBytes = direction.bytes
			result.RightToLeftEnd = direction.end
		}
	}
	select {
	case <-canceled:
		result.Canceled = true
		result.LeftToRightEnd = EndCanceled
		result.RightToLeftEnd = EndCanceled
	default:
	}
	return result
}

func classifyCopyEnd(err error) EndReason {
	if err == nil || errors.Is(err, io.EOF) {
		return EndEOF
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return EndTimeout
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return EndReset
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return EndClosed
	}
	return EndIOError
}
