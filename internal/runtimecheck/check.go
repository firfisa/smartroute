// Package runtimecheck performs local-only topology checks for live-trial sequencing.
package runtimecheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/firfisa/smartroute/internal/config"
)

const SchemaVersion = 1

type Phase string

const (
	PhaseBaseline Phase = "baseline"
	PhaseArmed    Phase = "armed"
	PhaseRunning  Phase = "running"
)

func ParsePhase(value string) (Phase, error) {
	phase := Phase(value)
	switch phase {
	case PhaseBaseline, PhaseArmed, PhaseRunning:
		return phase, nil
	default:
		return "", errors.New("doctor phase must be baseline, armed, or running")
	}
}

type Check struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	Expectation string `json:"expectation"`
	Observed    string `json:"observed"`
	Passed      bool   `json:"passed"`
}

type Report struct {
	SchemaVersion     int       `json:"schema_version"`
	GeneratedAt       time.Time `json:"generated_at"`
	Phase             Phase     `json:"phase"`
	LocalOnly         bool      `json:"local_only"`
	ExternalNetwork   bool      `json:"external_network"`
	ClashFilesRead    bool      `json:"clash_files_read"`
	ClashFilesWritten bool      `json:"clash_files_written"`
	ClashReloaded     bool      `json:"clash_reloaded"`
	Checks            []Check   `json:"checks"`
	Passed            bool      `json:"passed"`
}

type endpoint struct {
	name    string
	address string
	owned   bool
}

// CheckTopology checks only literal loopback listeners from the already decoded
// SmartRoute config. A SOCKS check sends a method-negotiation greeting and never
// sends a CONNECT request or opens a destination connection.
func CheckTopology(ctx context.Context, cfg config.Config, phase Phase, timeout time.Duration) (Report, error) {
	if _, err := ParsePhase(string(phase)); err != nil {
		return Report{}, err
	}
	if timeout <= 0 || timeout > 5*time.Second {
		return Report{}, errors.New("doctor timeout must be positive and at most 5s")
	}
	endpoints := []endpoint{
		{name: "adaptive_engine", address: cfg.ListenAddress, owned: true},
		{name: "availability_guard", address: cfg.GuardListenAddress, owned: true},
		{name: "forced_direct", address: cfg.DirectEndpoint},
		{name: "forced_proxy", address: cfg.ProxyEndpoint},
		{name: "original_policy", address: cfg.OriginalEndpoint},
	}
	report := Report{
		SchemaVersion: SchemaVersion, GeneratedAt: time.Now().UTC(), Phase: phase,
		LocalOnly: true, ExternalNetwork: false, ClashFilesRead: false,
		ClashFilesWritten: false, ClashReloaded: false, Passed: true,
	}
	for _, candidate := range endpoints {
		expectSOCKS := phase == PhaseRunning || (phase == PhaseArmed && candidate.owned)
		check := checkEndpoint(ctx, candidate, expectSOCKS, timeout)
		report.Checks = append(report.Checks, check)
		report.Passed = report.Passed && check.Passed
	}
	return report, nil
}

func checkEndpoint(ctx context.Context, candidate endpoint, expectSOCKS bool, timeout time.Duration) Check {
	expectation := "available"
	if expectSOCKS {
		expectation = "socks5_ready"
	}
	check := Check{Name: candidate.name, Address: candidate.address, Expectation: expectation}
	if expectSOCKS {
		observed, ready := probeSOCKS5(ctx, candidate.address, timeout)
		check.Observed = observed
		check.Passed = ready
		return check
	}

	listener, err := net.Listen("tcp", candidate.address)
	if err == nil {
		check.Observed = "available"
		check.Passed = true
		_ = listener.Close()
		return check
	}
	observed, ready := probeSOCKS5(ctx, candidate.address, timeout)
	if ready {
		check.Observed = observed
	} else {
		check.Observed = "occupied"
	}
	return check
}

func probeSOCKS5(ctx context.Context, address string, timeout time.Duration) (string, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", address)
	if err != nil {
		return "unreachable", false
	}
	defer connection.Close()
	deadline := time.Now().Add(timeout)
	if value, ok := probeCtx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return "non_socks_listener", false
	}
	if _, err := connection.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return "non_socks_listener", false
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(connection, response); err != nil {
		return "non_socks_listener", false
	}
	if response[0] != 0x05 || response[1] != 0x00 {
		return fmt.Sprintf("socks5_rejected_%02x", response[1]), false
	}
	return "socks5_ready", true
}
