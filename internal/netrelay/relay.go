// Package netrelay provides byte-transparent, bidirectional connection relay.
package netrelay

import (
	"io"
	"net"
	"sync"
)

func Bidirectional(left, right net.Conn) {
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
}
