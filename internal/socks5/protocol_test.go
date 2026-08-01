package socks5

import (
	"bytes"
	"testing"
)

func TestReadRequestPreservesDomain(t *testing.T) {
	wire := []byte{version5, 0x01, methodNoAuth, version5, commandConnect, 0x00, addressDomain, 0x0b}
	wire = append(wire, []byte("example.com")...)
	wire = append(wire, 0x01, 0xbb)
	buffer := bytes.NewBuffer(wire)

	request, err := ReadRequest(buffer)
	if err != nil {
		t.Fatalf("ReadRequest() error = %v", err)
	}
	if request.Host != "example.com" || request.Port != 443 {
		t.Fatalf("ReadRequest() = %+v", request)
	}
}

func TestEncodeConnectRequestRejectsLongDomain(t *testing.T) {
	host := make([]byte, 256)
	for index := range host {
		host[index] = 'a'
	}
	if _, err := encodeConnectRequest(Request{Host: string(host), Port: 443}); err == nil {
		t.Fatal("encodeConnectRequest() error = nil")
	}
}
