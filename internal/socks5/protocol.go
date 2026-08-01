// Package socks5 implements the small, no-authentication SOCKS5 subset used by
// SmartRoute's loopback-only sidecar and deterministic test lab.
package socks5

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	version5       = 0x05
	methodNoAuth   = 0x00
	methodRejected = 0xff
	commandConnect = 0x01
	addressIPv4    = 0x01
	addressDomain  = 0x03
	addressIPv6    = 0x04
)

// Reply is a SOCKS5 reply status.
type Reply byte

const (
	ReplySucceeded          Reply = 0x00
	ReplyGeneralFailure     Reply = 0x01
	ReplyConnectionRefused  Reply = 0x05
	ReplyCommandUnsupported Reply = 0x07
	ReplyAddressUnsupported Reply = 0x08
)

// Request is the target requested by a SOCKS5 CONNECT client.
type Request struct {
	Host string
	Port uint16
}

func (r Request) Address() string {
	return net.JoinHostPort(r.Host, strconv.Itoa(int(r.Port)))
}

// RequestError carries the most appropriate SOCKS5 reply for a failed request.
type RequestError struct {
	Reply Reply
	Err   error
}

func (e *RequestError) Error() string { return e.Err.Error() }
func (e *RequestError) Unwrap() error { return e.Err }

// ReadRequest performs server-side no-authentication negotiation and reads one
// CONNECT request. It never accepts username/password authentication.
func ReadRequest(rw io.ReadWriter) (Request, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(rw, header); err != nil {
		return Request{}, fmt.Errorf("read greeting: %w", err)
	}
	if header[0] != version5 {
		return Request{}, fmt.Errorf("unsupported SOCKS version %d", header[0])
	}

	methods := make([]byte, int(header[1]))
	if len(methods) == 0 {
		return Request{}, errors.New("client offered no authentication methods")
	}
	if _, err := io.ReadFull(rw, methods); err != nil {
		return Request{}, fmt.Errorf("read authentication methods: %w", err)
	}
	accepted := false
	for _, method := range methods {
		if method == methodNoAuth {
			accepted = true
			break
		}
	}
	if !accepted {
		_, _ = rw.Write([]byte{version5, methodRejected})
		return Request{}, errors.New("no-authentication method not offered")
	}
	if _, err := rw.Write([]byte{version5, methodNoAuth}); err != nil {
		return Request{}, fmt.Errorf("write authentication selection: %w", err)
	}

	requestHeader := make([]byte, 4)
	if _, err := io.ReadFull(rw, requestHeader); err != nil {
		return Request{}, fmt.Errorf("read request header: %w", err)
	}
	if requestHeader[0] != version5 {
		return Request{}, fmt.Errorf("unsupported request version %d", requestHeader[0])
	}
	if requestHeader[1] != commandConnect {
		return Request{}, &RequestError{Reply: ReplyCommandUnsupported, Err: fmt.Errorf("unsupported command %d", requestHeader[1])}
	}

	host, err := readHost(rw, requestHeader[3])
	if err != nil {
		return Request{}, err
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(rw, portBytes); err != nil {
		return Request{}, fmt.Errorf("read target port: %w", err)
	}
	port := binary.BigEndian.Uint16(portBytes)
	if port == 0 {
		return Request{}, &RequestError{Reply: ReplyAddressUnsupported, Err: errors.New("target port must not be zero")}
	}
	return Request{Host: host, Port: port}, nil
}

func readHost(reader io.Reader, addressType byte) (string, error) {
	switch addressType {
	case addressIPv4:
		value := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", fmt.Errorf("read IPv4 target: %w", err)
		}
		return net.IP(value).String(), nil
	case addressIPv6:
		value := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", fmt.Errorf("read IPv6 target: %w", err)
		}
		return net.IP(value).String(), nil
	case addressDomain:
		length := []byte{0}
		if _, err := io.ReadFull(reader, length); err != nil {
			return "", fmt.Errorf("read domain length: %w", err)
		}
		if length[0] == 0 {
			return "", &RequestError{Reply: ReplyAddressUnsupported, Err: errors.New("target domain must not be empty")}
		}
		value := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", fmt.Errorf("read domain target: %w", err)
		}
		return string(value), nil
	default:
		return "", &RequestError{Reply: ReplyAddressUnsupported, Err: fmt.Errorf("unsupported address type %d", addressType)}
	}
}

// WriteReply writes a reply with an unspecified IPv4 bind address. SmartRoute
// does not expose upstream listener addresses to the inbound client.
func WriteReply(writer io.Writer, reply Reply) error {
	_, err := writer.Write([]byte{version5, byte(reply), 0x00, addressIPv4, 0, 0, 0, 0, 0, 0})
	return err
}

// DialContext opens a TCP connection through a no-authentication SOCKS5
// endpoint and preserves domain-form targets on the wire.
func DialContext(ctx context.Context, endpoint string, target Request) (net.Conn, error) {
	if target.Host == "" || target.Port == 0 {
		return nil, errors.New("SOCKS target requires host and non-zero port")
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, fmt.Errorf("connect SOCKS endpoint: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = conn.Close()
		}
	}()

	cancelWatcherStop := make(chan struct{})
	cancelWatcherStopped := make(chan struct{})
	go func() {
		defer close(cancelWatcherStopped)
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-cancelWatcherStop:
		}
	}()
	defer func() {
		close(cancelWatcherStop)
		<-cancelWatcherStopped
	}()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if _, err := conn.Write([]byte{version5, 0x01, methodNoAuth}); err != nil {
		return nil, fmt.Errorf("write SOCKS greeting: %w", err)
	}
	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(conn, methodReply); err != nil {
		return nil, fmt.Errorf("read SOCKS method reply: %w", err)
	}
	if methodReply[0] != version5 || methodReply[1] != methodNoAuth {
		return nil, fmt.Errorf("SOCKS endpoint rejected no-authentication method")
	}

	request, err := encodeConnectRequest(target)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(request); err != nil {
		return nil, fmt.Errorf("write SOCKS CONNECT: %w", err)
	}
	replyHeader := make([]byte, 4)
	if _, err := io.ReadFull(conn, replyHeader); err != nil {
		return nil, fmt.Errorf("read SOCKS CONNECT reply: %w", err)
	}
	if replyHeader[0] != version5 {
		return nil, fmt.Errorf("invalid SOCKS reply version %d", replyHeader[0])
	}
	if _, err := readHost(conn, replyHeader[3]); err != nil {
		return nil, fmt.Errorf("read SOCKS bind address: %w", err)
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return nil, fmt.Errorf("read SOCKS bind port: %w", err)
	}
	if Reply(replyHeader[1]) != ReplySucceeded {
		return nil, fmt.Errorf("SOCKS CONNECT failed with reply 0x%02x", replyHeader[1])
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	succeeded = true
	return conn, nil
}

func encodeConnectRequest(target Request) ([]byte, error) {
	request := []byte{version5, commandConnect, 0x00}
	if ip := net.ParseIP(target.Host); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			request = append(request, addressIPv4)
			request = append(request, ipv4...)
		} else {
			request = append(request, addressIPv6)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(target.Host) > 255 {
			return nil, errors.New("SOCKS target domain exceeds 255 bytes")
		}
		request = append(request, addressDomain, byte(len(target.Host)))
		request = append(request, target.Host...)
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, target.Port)
	request = append(request, portBytes...)
	return request, nil
}
