package tlsinspect

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestReadClientHelloAcrossRecords(t *testing.T) {
	handshake := clientHelloHandshake(nil)
	wire := append(tlsRecord(contentTypeHandshake, handshake[:11]), tlsRecord(contentTypeHandshake, handshake[11:])...)
	hello, err := ReadClientHello(bytes.NewReader(wire), 0)
	if err != nil {
		t.Fatalf("ReadClientHello() error = %v", err)
	}
	if !bytes.Equal(hello.WireBytes(), wire) {
		t.Fatal("ReadClientHello() did not preserve exact fragmented records")
	}
}

func TestReadClientHelloRejectsEarlyData(t *testing.T) {
	earlyData := []byte{0x00, extensionEarlyData, 0x00, 0x00}
	_, err := ReadClientHello(bytes.NewReader(tlsRecord(contentTypeHandshake, clientHelloHandshake(earlyData))), 0)
	if FailureClass(err) != FailureTLSEarlyData {
		t.Fatalf("failure class = %q, error = %v", FailureClass(err), err)
	}
}

func TestReadClientHelloRejectsTrailingFirstFlightBytes(t *testing.T) {
	handshake := append(clientHelloHandshake(nil), 0x14, 0, 0, 0)
	_, err := ReadClientHello(bytes.NewReader(tlsRecord(contentTypeHandshake, handshake)), 0)
	if FailureClass(err) != FailureTLSUnexpected {
		t.Fatalf("failure class = %q, error = %v", FailureClass(err), err)
	}
}

func TestReadClientHelloRejectsTruncation(t *testing.T) {
	wire := tlsRecord(contentTypeHandshake, clientHelloHandshake(nil))
	_, err := ReadClientHello(bytes.NewReader(wire[:len(wire)-1]), 0)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadClientHello() error = %v", err)
	}
}

func TestReadClientHelloHonorsConfiguredLimit(t *testing.T) {
	wire := tlsRecord(contentTypeHandshake, clientHelloHandshake(nil))
	_, err := ReadClientHello(bytes.NewReader(wire), len(wire)-1)
	if FailureClass(err) != FailureTLSTooLarge {
		t.Fatalf("failure class = %q, error = %v", FailureClass(err), err)
	}
}

func TestReadClientHelloAcceptsGoTLSClient(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	handshakeDone := make(chan error, 1)
	go func() {
		client := tls.Client(clientConn, &tls.Config{ServerName: "echo.test", MinVersion: tls.VersionTLS12})
		handshakeDone <- client.HandshakeContext(ctx)
		_ = client.Close()
	}()
	hello, err := ReadClientHello(serverConn, 0)
	if err != nil {
		t.Fatalf("ReadClientHello(crypto/tls) error = %v", err)
	}
	if hello.Len() < 64 {
		t.Fatalf("crypto/tls ClientHello length = %d", hello.Len())
	}
	_ = serverConn.Close()
	select {
	case <-handshakeDone:
	case <-ctx.Done():
		t.Fatal("crypto/tls client did not stop")
	}
}

func TestReadServerHelloAcrossRecords(t *testing.T) {
	handshake := serverHelloHandshake()
	wire := append(tlsRecord(contentTypeHandshake, handshake[:9]), tlsRecord(contentTypeHandshake, handshake[9:])...)
	hello, err := ReadServerHello(bytes.NewReader(wire), 0)
	if err != nil {
		t.Fatalf("ReadServerHello() error = %v", err)
	}
	if !bytes.Equal(hello.Wire, wire) {
		t.Fatal("ReadServerHello() did not preserve exact fragmented records")
	}
}

func TestReadServerHelloClassifiesAlert(t *testing.T) {
	_, err := ReadServerHello(bytes.NewReader(tlsRecord(contentTypeAlert, []byte{2, 40})), 0)
	if FailureClass(err) != FailureTLSAlert {
		t.Fatalf("failure class = %q, error = %v", FailureClass(err), err)
	}
}

func clientHelloHandshake(extensions []byte) []byte {
	body := []byte{0x03, 0x03}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0x00)
	body = append(body, 0x00, 0x02, 0x13, 0x01)
	body = append(body, 0x01, 0x00)
	body = append(body, byte(len(extensions)>>8), byte(len(extensions)))
	body = append(body, extensions...)
	return handshakeMessage(handshakeClientHello, body)
}

func serverHelloHandshake() []byte {
	body := []byte{0x03, 0x03}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0x00, 0x13, 0x01, 0x00)
	body = append(body, 0x00, 0x06, 0x00, 0x2b, 0x00, 0x02, 0x03, 0x04)
	return handshakeMessage(handshakeServerHello, body)
}

func handshakeMessage(messageType byte, body []byte) []byte {
	return append([]byte{messageType, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
}

func tlsRecord(contentType byte, payload []byte) []byte {
	return append([]byte{contentType, 0x03, 0x03, byte(len(payload) >> 8), byte(len(payload))}, payload...)
}
