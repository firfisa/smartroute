package runtimecheck

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/config"
)

func TestTopologyPhases(t *testing.T) {
	cfg := testConfig(t)
	assertPhase(t, cfg, PhaseBaseline, true)

	stopOwned := startSOCKS(t, cfg.ListenAddress, cfg.GuardListenAddress)
	assertPhase(t, cfg, PhaseArmed, true)
	assertPhase(t, cfg, PhaseBaseline, false)

	stopUpstreams := startSOCKS(t, cfg.DirectEndpoint, cfg.ProxyEndpoint, cfg.OriginalEndpoint)
	assertPhase(t, cfg, PhaseRunning, true)
	assertPhase(t, cfg, PhaseArmed, false)
	stopUpstreams()
	stopOwned()
	assertPhase(t, cfg, PhaseBaseline, true)
}

func TestRunningRejectsNonSOCKSListener(t *testing.T) {
	cfg := testConfig(t)
	listeners := make([]net.Listener, 0, 5)
	for index, address := range []string{cfg.ListenAddress, cfg.GuardListenAddress, cfg.DirectEndpoint, cfg.ProxyEndpoint, cfg.OriginalEndpoint} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		if index == 2 {
			go func() {
				connection, acceptErr := listener.Accept()
				if acceptErr == nil {
					_, _ = io.Copy(io.Discard, connection)
					_ = connection.Close()
				}
			}()
		} else {
			serveSOCKS(listener)
		}
	}
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	report, err := CheckTopology(context.Background(), cfg, PhaseRunning, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.Checks[2].Observed != "non_socks_listener" {
		t.Fatalf("report=%+v", report)
	}
}

func TestParsePhaseAndTimeoutValidation(t *testing.T) {
	if _, err := ParsePhase("ready-ish"); err == nil {
		t.Fatal("accepted unknown phase")
	}
	if _, err := CheckTopology(context.Background(), config.Default(), PhaseBaseline, 0); err == nil {
		t.Fatal("accepted zero timeout")
	}
}

func assertPhase(t *testing.T, cfg config.Config, phase Phase, want bool) {
	t.Helper()
	report, err := CheckTopology(context.Background(), cfg, phase, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed != want || !report.LocalOnly || report.ExternalNetwork || report.ClashFilesRead || report.ClashFilesWritten || report.ClashReloaded {
		t.Fatalf("phase=%s report=%+v want=%v", phase, report, want)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	ports := make([]string, 0, 5)
	for range 5 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ports = append(ports, listener.Addr().String())
		_ = listener.Close()
	}
	cfg := config.Default()
	cfg.ListenAddress = ports[0]
	cfg.GuardListenAddress = ports[1]
	cfg.DirectEndpoint = ports[2]
	cfg.ProxyEndpoint = ports[3]
	cfg.OriginalEndpoint = ports[4]
	return cfg
}

func startSOCKS(t *testing.T, addresses ...string) func() {
	t.Helper()
	listeners := make([]net.Listener, 0, len(addresses))
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		serveSOCKS(listener)
	}
	return func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}
}

func serveSOCKS(listener net.Listener) {
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				greeting := make([]byte, 3)
				if _, err := io.ReadFull(connection, greeting); err != nil {
					return
				}
				_, _ = connection.Write([]byte{0x05, 0x00})
			}()
		}
	}()
}
