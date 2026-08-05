package testlab

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/tlsinspect"
)

func TestSyntheticTLSFixtureIsStructurallyValidAndHasNoEarlyData(t *testing.T) {
	hello, err := tlsinspect.ReadClientHello(bytes.NewReader(SyntheticClientHelloRecords()), 0)
	if err != nil {
		t.Fatalf("ReadClientHello() error=%v", err)
	}
	if hello.Len() == 0 {
		t.Fatal("parsed ClientHello is empty")
	}
	serverHello, err := tlsinspect.ReadServerHello(bytes.NewReader(SyntheticServerHelloRecord()), 0)
	if err != nil {
		t.Fatalf("ReadServerHello() error=%v", err)
	}
	if !bytes.Equal(serverHello.Wire, SyntheticServerHelloRecord()) {
		t.Fatal("ServerHello parser did not preserve exact fixture bytes")
	}
}

func TestTLSTargetAcceptsClientHelloAndReturnsExactServerHello(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target, err := StartTLSTarget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	conn, err := net.DialTimeout("tcp", target.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write(SyntheticClientHelloRecords()); err != nil {
		t.Fatal(err)
	}
	hello, err := tlsinspect.ReadServerHello(conn, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hello.Wire, SyntheticServerHelloRecord()) || target.AcceptedClientHellos() != 1 {
		t.Fatalf("hello=%x accepted=%d", hello.Wire, target.AcceptedClientHellos())
	}
}
