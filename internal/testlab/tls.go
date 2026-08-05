package testlab

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/firfisa/smartroute/internal/tlsinspect"
)

// TLSTarget is a deterministic loopback target that accepts a structurally
// valid ClientHello and returns one fixed, structurally valid ServerHello.
// It does not complete a TLS handshake or claim application success.
type TLSTarget struct {
	listener net.Listener
	cancel   context.CancelFunc
	accepted atomic.Int64
}

func StartTLSTarget(parent context.Context) (*TLSTarget, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen synthetic TLS target: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	target := &TLSTarget{listener: listener, cancel: cancel}
	go target.serve(ctx)
	return target, nil
}

func (t *TLSTarget) Address() string { return t.listener.Addr().String() }
func (t *TLSTarget) Port() uint16    { return uint16(t.listener.Addr().(*net.TCPAddr).Port) }
func (t *TLSTarget) AcceptedClientHellos() int {
	return int(t.accepted.Load())
}
func (t *TLSTarget) Close() {
	t.cancel()
	_ = t.listener.Close()
}

func (t *TLSTarget) serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = t.listener.Close()
	}()
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			if _, err := tlsinspect.ReadClientHello(conn, 0); err == nil {
				t.accepted.Add(1)
				_ = writeTLSFixture(conn, SyntheticServerHelloRecord())
			}
		}()
	}
}

func writeTLSFixture(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

// SyntheticClientHelloRecords returns a fragmented, structurally valid TLS
// 1.3-compatible ClientHello with no early_data extension.
func SyntheticClientHelloRecords() []byte {
	extensions := []byte{0x00, 0x2b, 0x00, 0x03, 0x02, 0x03, 0x04}
	body := []byte{0x03, 0x03}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0x00, 0x00, 0x02, 0x13, 0x01, 0x01, 0x00)
	body = append(body, byte(len(extensions)>>8), byte(len(extensions)))
	body = append(body, extensions...)
	handshake := append([]byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
	split := 17
	return append(syntheticTLSRecord(22, handshake[:split]), syntheticTLSRecord(22, handshake[split:])...)
}

func SyntheticServerHelloRecord() []byte {
	body := []byte{0x03, 0x03}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0x00, 0x13, 0x01, 0x00, 0x00, 0x06, 0x00, 0x2b, 0x00, 0x02, 0x03, 0x04)
	handshake := append([]byte{2, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
	return syntheticTLSRecord(22, handshake)
}

func syntheticTLSRecord(contentType byte, payload []byte) []byte {
	return append([]byte{contentType, 0x03, 0x03, byte(len(payload) >> 8), byte(len(payload))}, payload...)
}
