// Package tlsinspect implements the minimal, non-decrypting TLS handshake
// inspection needed by SmartRoute's safe readiness race.
package tlsinspect

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	DefaultMaxHandshakeBytes = 64 << 10
	maxPlaintextRecordBytes  = 16 << 10

	contentTypeAlert     = 21
	contentTypeHandshake = 22
	handshakeClientHello = 1
	handshakeServerHello = 2
	extensionEarlyData   = 42
)

const (
	FailureNotTLS        = "not_tls"
	FailureTLSMalformed  = "tls_malformed"
	FailureTLSAlert      = "tls_alert"
	FailureTLSTooLarge   = "tls_too_large"
	FailureTLSEarlyData  = "tls_early_data"
	FailureTLSUnexpected = "tls_unexpected_message"
)

// ProtocolError provides a stable failure class without exposing payload data.
type ProtocolError struct {
	Class string
	Err   error
}

func (e *ProtocolError) Error() string { return e.Err.Error() }
func (e *ProtocolError) Unwrap() error { return e.Err }

func FailureClass(err error) string {
	var protocolError *ProtocolError
	if errors.As(err, &protocolError) {
		return protocolError.Class
	}
	return FailureTLSMalformed
}

// ClientHello contains an exact, replayable TLS record sequence. Values can
// only be created by ReadClientHello, which rejects the early_data extension.
type ClientHello struct {
	wire []byte
}

func (h ClientHello) WireBytes() []byte { return bytes.Clone(h.wire) }
func (h ClientHello) Len() int          { return len(h.wire) }

// ServerHello contains every TLS record consumed through the structurally
// valid ServerHello. These bytes must be replayed to the selected client.
type ServerHello struct {
	Wire []byte
}

// ReadClientHello reads complete TLS records until exactly one complete
// ClientHello is present. Record fragmentation is supported; coalesced bytes
// after ClientHello are rejected because they may carry unsafe first-flight
// data.
func ReadClientHello(reader io.Reader, maxBytes int) (ClientHello, error) {
	maxBytes = normalizedLimit(maxBytes)
	var wire bytes.Buffer
	var handshake bytes.Buffer
	declaredLength := -1

	for {
		record, payload, contentType, err := readRecord(reader)
		if err != nil {
			return ClientHello{}, err
		}
		if contentType != contentTypeHandshake {
			return ClientHello{}, protocolError(FailureNotTLS, "first TLS flight must contain handshake records")
		}
		if wire.Len()+len(record) > maxBytes {
			return ClientHello{}, protocolError(FailureTLSTooLarge, "ClientHello exceeds configured limit")
		}
		wire.Write(record)
		handshake.Write(payload)

		data := handshake.Bytes()
		if declaredLength < 0 && len(data) >= 4 {
			if data[0] != handshakeClientHello {
				return ClientHello{}, protocolError(FailureTLSUnexpected, "first handshake message is not ClientHello")
			}
			declaredLength = readUint24(data[1:4])
			if declaredLength < 0 || declaredLength+4 > maxBytes {
				return ClientHello{}, protocolError(FailureTLSTooLarge, "ClientHello handshake exceeds configured limit")
			}
		}
		if declaredLength < 0 || len(data) < declaredLength+4 {
			continue
		}
		if len(data) != declaredLength+4 {
			return ClientHello{}, protocolError(FailureTLSUnexpected, "ClientHello record contains trailing first-flight bytes")
		}
		if err := validateClientHello(data[4:]); err != nil {
			return ClientHello{}, err
		}
		return ClientHello{wire: bytes.Clone(wire.Bytes())}, nil
	}
}

// ReadServerHello reads and returns the record prefix through a complete,
// structurally valid ServerHello. A TLS alert is classified as path failure.
func ReadServerHello(reader io.Reader, maxBytes int) (ServerHello, error) {
	maxBytes = normalizedLimit(maxBytes)
	var wire bytes.Buffer
	var handshake bytes.Buffer
	declaredLength := -1

	for {
		record, payload, contentType, err := readRecord(reader)
		if err != nil {
			return ServerHello{}, err
		}
		if wire.Len()+len(record) > maxBytes {
			return ServerHello{}, protocolError(FailureTLSTooLarge, "ServerHello exceeds configured limit")
		}
		wire.Write(record)
		switch contentType {
		case contentTypeAlert:
			if len(payload) < 2 {
				return ServerHello{}, protocolError(FailureTLSMalformed, "TLS alert record is truncated")
			}
			return ServerHello{}, protocolError(FailureTLSAlert, "server returned a TLS alert")
		case contentTypeHandshake:
			handshake.Write(payload)
		default:
			return ServerHello{}, protocolError(FailureTLSUnexpected, fmt.Sprintf("unexpected TLS content type %d before ServerHello", contentType))
		}

		data := handshake.Bytes()
		if declaredLength < 0 && len(data) >= 4 {
			if data[0] != handshakeServerHello {
				return ServerHello{}, protocolError(FailureTLSUnexpected, "first server handshake message is not ServerHello")
			}
			declaredLength = readUint24(data[1:4])
			if declaredLength < 0 || declaredLength+4 > maxBytes {
				return ServerHello{}, protocolError(FailureTLSTooLarge, "ServerHello handshake exceeds configured limit")
			}
		}
		if declaredLength < 0 || len(data) < declaredLength+4 {
			continue
		}
		if err := validateServerHello(data[4 : declaredLength+4]); err != nil {
			return ServerHello{}, err
		}
		return ServerHello{Wire: bytes.Clone(wire.Bytes())}, nil
	}
}

func readRecord(reader io.Reader) (record, payload []byte, contentType byte, err error) {
	header := make([]byte, 5)
	if _, err = io.ReadFull(reader, header); err != nil {
		return nil, nil, 0, fmt.Errorf("read TLS record header: %w", err)
	}
	if header[1] != 3 {
		return nil, nil, 0, protocolError(FailureNotTLS, "record does not use a TLS legacy major version")
	}
	length := int(binary.BigEndian.Uint16(header[3:5]))
	if length == 0 || length > maxPlaintextRecordBytes {
		return nil, nil, 0, protocolError(FailureTLSMalformed, "TLS plaintext record length is invalid")
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(reader, payload); err != nil {
		return nil, nil, 0, fmt.Errorf("read TLS record payload: %w", err)
	}
	record = append(bytes.Clone(header), payload...)
	return record, payload, header[0], nil
}

func validateClientHello(body []byte) error {
	if len(body) < 2+32+1+2+1 {
		return protocolError(FailureTLSMalformed, "ClientHello fixed fields are truncated")
	}
	position := 2 + 32
	if body[0] != 3 {
		return protocolError(FailureTLSMalformed, "ClientHello legacy version is invalid")
	}
	sessionLength := int(body[position])
	position++
	if sessionLength > 32 || position+sessionLength+2 > len(body) {
		return protocolError(FailureTLSMalformed, "ClientHello session ID is invalid")
	}
	position += sessionLength
	cipherLength := int(binary.BigEndian.Uint16(body[position : position+2]))
	position += 2
	if cipherLength == 0 || cipherLength%2 != 0 || position+cipherLength+1 > len(body) {
		return protocolError(FailureTLSMalformed, "ClientHello cipher suites are invalid")
	}
	position += cipherLength
	compressionLength := int(body[position])
	position++
	if compressionLength == 0 || position+compressionLength > len(body) {
		return protocolError(FailureTLSMalformed, "ClientHello compression methods are invalid")
	}
	if !bytes.Contains(body[position:position+compressionLength], []byte{0}) {
		return protocolError(FailureTLSMalformed, "ClientHello does not offer null compression")
	}
	position += compressionLength
	if position == len(body) {
		return nil
	}
	if position+2 > len(body) {
		return protocolError(FailureTLSMalformed, "ClientHello extensions length is truncated")
	}
	extensionsLength := int(binary.BigEndian.Uint16(body[position : position+2]))
	position += 2
	if position+extensionsLength != len(body) {
		return protocolError(FailureTLSMalformed, "ClientHello extensions length does not match")
	}
	end := position + extensionsLength
	for position < end {
		if position+4 > end {
			return protocolError(FailureTLSMalformed, "ClientHello extension header is truncated")
		}
		extensionType := binary.BigEndian.Uint16(body[position : position+2])
		extensionLength := int(binary.BigEndian.Uint16(body[position+2 : position+4]))
		position += 4
		if position+extensionLength > end {
			return protocolError(FailureTLSMalformed, "ClientHello extension data is truncated")
		}
		if extensionType == extensionEarlyData {
			return protocolError(FailureTLSEarlyData, "TLS early_data is not safe to duplicate")
		}
		position += extensionLength
	}
	return nil
}

func validateServerHello(body []byte) error {
	if len(body) < 2+32+1+2+1+2 {
		return protocolError(FailureTLSMalformed, "ServerHello fixed fields are truncated")
	}
	position := 2 + 32
	if body[0] != 3 {
		return protocolError(FailureTLSMalformed, "ServerHello legacy version is invalid")
	}
	sessionLength := int(body[position])
	position++
	if sessionLength > 32 || position+sessionLength+2+1+2 > len(body) {
		return protocolError(FailureTLSMalformed, "ServerHello session ID is invalid")
	}
	position += sessionLength
	if binary.BigEndian.Uint16(body[position:position+2]) == 0 {
		return protocolError(FailureTLSMalformed, "ServerHello cipher suite is invalid")
	}
	position += 2
	if body[position] != 0 {
		return protocolError(FailureTLSMalformed, "ServerHello compression method is invalid")
	}
	position++
	extensionsLength := int(binary.BigEndian.Uint16(body[position : position+2]))
	position += 2
	if position+extensionsLength != len(body) {
		return protocolError(FailureTLSMalformed, "ServerHello extensions length does not match")
	}
	end := position + extensionsLength
	for position < end {
		if position+4 > end {
			return protocolError(FailureTLSMalformed, "ServerHello extension header is truncated")
		}
		extensionLength := int(binary.BigEndian.Uint16(body[position+2 : position+4]))
		position += 4
		if position+extensionLength > end {
			return protocolError(FailureTLSMalformed, "ServerHello extension data is truncated")
		}
		position += extensionLength
	}
	return nil
}

func normalizedLimit(limit int) int {
	if limit <= 0 {
		return DefaultMaxHandshakeBytes
	}
	return limit
}

func readUint24(value []byte) int {
	return int(value[0])<<16 | int(value[1])<<8 | int(value[2])
}

func protocolError(class, message string) error {
	return &ProtocolError{Class: class, Err: errors.New(message)}
}
