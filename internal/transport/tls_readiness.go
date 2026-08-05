package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/tlsinspect"
)

const (
	FailureTLSCanceled          = "canceled"
	FailureTLSTimeout           = "tls_timeout"
	FailureTLSConnectionClosed  = "tls_connection_closed"
	FailureClientHelloWrite     = "client_hello_write_failed"
	ReasonDirectPolicyOnly      = "direct_policy_only"
	ReasonProxyPolicyOnly       = "proxy_policy_only"
	ReasonDurablePolicySelected = "durable_policy_selected"
	ReasonDurablePolicyFallback = "durable_policy_fallback"
)

type TLSPathError struct {
	Observation model.Observation
	Err         error
}

func (e *TLSPathError) Error() string {
	return fmt.Sprintf("%s TLS path failed: %v", e.Observation.Path, e.Err)
}
func (e *TLSPathError) Unwrap() error { return e.Err }

// TLSServerHelloGate promotes a candidate only after receiving a complete,
// structurally valid ServerHello. Every consumed server byte is returned for
// exact replay to the selected client.
type TLSServerHelloGate struct {
	MaxHandshakeBytes int
}

func (g TLSServerHelloGate) Await(ctx context.Context, conn net.Conn, _ model.Target) (ReadinessResult, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	}
	hello, err := tlsinspect.ReadServerHello(conn, g.MaxHandshakeBytes)
	if err != nil {
		result := ReadinessResult{StageReached: model.StageOutbound, FailureClass: classifyTLSReadinessFailure(ctx, err)}
		if result.FailureClass == tlsinspect.FailureTLSAlert || result.FailureClass == tlsinspect.FailureTLSUnexpected {
			result.StageReached = model.StageTLS
		}
		return result, err
	}
	_ = conn.SetReadDeadline(time.Time{})
	return ReadinessResult{StageReached: model.StageTLS, Prefetched: hello.Wire}, nil
}

// TLSRacer duplicates only a validated, no-early-data ClientHello across the
// two candidate paths, then reuses Racer to select the first valid ServerHello.
type TLSRacer struct {
	Direct    CandidateDialer
	Proxy     CandidateDialer
	Gate      ReadinessGate
	HeadStart time.Duration
	Timeout   time.Duration
}

func (r TLSRacer) Race(ctx context.Context, target model.Target, hello tlsinspect.ClientHello) (RaceResult, error) {
	return r.RacePreferred(ctx, target, hello, model.PathDirect)
}

// RacePreferred changes only candidate launch order. The opposite path still
// starts after HeadStart or immediately after an early preferred-path failure.
func (r TLSRacer) RacePreferred(ctx context.Context, target model.Target, hello tlsinspect.ClientHello, preferred model.Path) (RaceResult, error) {
	if hello.Len() == 0 {
		return RaceResult{}, errors.New("validated ClientHello is required")
	}
	if r.Direct == nil || r.Proxy == nil {
		return RaceResult{}, errors.New("direct and proxy TLS candidates are required")
	}
	if r.Gate == nil {
		return RaceResult{}, errors.New("TLS readiness gate is required")
	}
	wire := hello.WireBytes()
	return (Racer{
		Direct:        tlsReadyDialer{base: r.Direct, gate: r.Gate, clientHello: wire},
		Proxy:         tlsReadyDialer{base: r.Proxy, gate: r.Gate, clientHello: wire},
		HeadStart:     r.HeadStart,
		Timeout:       r.Timeout,
		PreferredPath: preferred,
	}).Race(ctx, target)
}

// ConnectPath applies the same ClientHello write, L3 readiness gate, timeout,
// and replay behavior to exactly one policy-selected path. It is used when a
// privacy or manual policy forbids opening the counterfactual candidate.
func (r TLSRacer) ConnectPath(ctx context.Context, target model.Target, hello tlsinspect.ClientHello, path model.Path) (RaceResult, error) {
	if hello.Len() == 0 {
		return RaceResult{}, errors.New("validated ClientHello is required")
	}
	if r.Gate == nil {
		return RaceResult{}, errors.New("TLS readiness gate is required")
	}
	if r.Timeout <= 0 {
		return RaceResult{}, errors.New("TLS path timeout must be positive")
	}
	var base CandidateDialer
	reason := ReasonProxyPolicyOnly
	switch path {
	case model.PathDirect:
		base = r.Direct
		reason = ReasonDirectPolicyOnly
	case model.PathProxy:
		base = r.Proxy
	default:
		return RaceResult{}, fmt.Errorf("unsupported TLS path %q", path)
	}
	if base == nil {
		return RaceResult{}, fmt.Errorf("%s TLS candidate is required", path)
	}
	pathCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	return r.connectPath(pathCtx, target, hello, path, reason)
}

// ConnectPreferredWithFallback avoids parallel candidate work for an automatic
// last-known-good mapping. If its selected path fails before commitment, the
// opposite path is attempted once within the same total timeout and the pair
// is exposed so a successful opposite result can overwrite the mapping.
func (r TLSRacer) ConnectPreferredWithFallback(ctx context.Context, target model.Target, hello tlsinspect.ClientHello, preferred model.Path) (RaceResult, error) {
	if preferred != model.PathDirect && preferred != model.PathProxy {
		return RaceResult{}, fmt.Errorf("unsupported durable TLS path %q", preferred)
	}
	if r.Timeout <= 0 {
		return RaceResult{}, errors.New("TLS path timeout must be positive")
	}
	pathCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	// A selected Mihomo listener can acknowledge SOCKS before the target is
	// ready. Reserve half of the total budget for the one allowed opposite-path
	// attempt so a silent selected path cannot consume all fallback time.
	firstCtx, firstCancel := context.WithTimeout(pathCtx, r.Timeout/2)
	first, firstErr := r.connectPath(firstCtx, target, hello, preferred, ReasonDurablePolicySelected)
	firstCancel()
	if firstErr == nil {
		return first, nil
	}
	var firstPathError *TLSPathError
	if !errors.As(firstErr, &firstPathError) {
		return RaceResult{}, firstErr
	}
	opposite := model.PathDirect
	if preferred == model.PathDirect {
		opposite = model.PathProxy
	}
	second, secondErr := r.connectPath(pathCtx, target, hello, opposite, ReasonDurablePolicyFallback)
	if secondErr == nil {
		firstObservation := firstPathError.Observation
		second.OtherObservation = &firstObservation
		return second, nil
	}
	var secondPathError *TLSPathError
	if !errors.As(secondErr, &secondPathError) {
		return RaceResult{}, secondErr
	}
	raceError := &RaceError{}
	if preferred == model.PathDirect {
		raceError.Direct = firstPathError.Observation
		raceError.Proxy = secondPathError.Observation
	} else {
		raceError.Proxy = firstPathError.Observation
		raceError.Direct = secondPathError.Observation
	}
	return RaceResult{}, raceError
}

func (r TLSRacer) connectPath(ctx context.Context, target model.Target, hello tlsinspect.ClientHello, path model.Path, reason string) (RaceResult, error) {
	if hello.Len() == 0 {
		return RaceResult{}, errors.New("validated ClientHello is required")
	}
	if r.Gate == nil {
		return RaceResult{}, errors.New("TLS readiness gate is required")
	}
	var base CandidateDialer
	switch path {
	case model.PathDirect:
		base = r.Direct
	case model.PathProxy:
		base = r.Proxy
	default:
		return RaceResult{}, fmt.Errorf("unsupported TLS path %q", path)
	}
	if base == nil {
		return RaceResult{}, fmt.Errorf("%s TLS candidate is required", path)
	}
	conn, observation, err := (tlsReadyDialer{base: base, gate: r.Gate, clientHello: hello.WireBytes()}).Dial(ctx, target)
	if observation.Path == "" {
		observation.Path = path
	}
	if observation.Path != path {
		if conn != nil {
			_ = conn.Close()
		}
		observation.Success = false
		observation.FailureClass = "candidate_path_mismatch"
		return RaceResult{}, &TLSPathError{Observation: observation, Err: fmt.Errorf("candidate reported path %q, want %q", observation.Path, path)}
	}
	if err != nil {
		return RaceResult{}, &TLSPathError{Observation: observation, Err: err}
	}
	return RaceResult{Conn: conn, Observation: observation, ReasonCode: reason}, nil
}

type tlsReadyDialer struct {
	base        CandidateDialer
	gate        ReadinessGate
	clientHello []byte
}

func (d tlsReadyDialer) Dial(ctx context.Context, target model.Target) (net.Conn, model.Observation, error) {
	started := time.Now()
	conn, observation, err := d.base.Dial(ctx, target)
	if err != nil {
		return nil, observation, err
	}
	if conn == nil {
		observation.Success = false
		observation.FailureClass = "candidate_returned_nil_connection"
		return nil, observation, errors.New("candidate returned nil connection")
	}
	stopWatcher := closeConnectionOnCancel(ctx, conn)
	defer stopWatcher()

	if err := writeFull(conn, d.clientHello); err != nil {
		_ = conn.Close()
		observation.Success = false
		observation.Latency = time.Since(started)
		observation.FailureClass = classifyClientHelloWriteFailure(ctx, err)
		return nil, observation, fmt.Errorf("write ClientHello: %w", err)
	}
	readiness, err := d.gate.Await(ctx, conn, target)
	observation.Latency = time.Since(started)
	if err != nil {
		_ = conn.Close()
		observation.Success = false
		if readiness.StageReached > observation.StageReached {
			observation.StageReached = readiness.StageReached
		}
		observation.FailureClass = readiness.FailureClass
		if observation.FailureClass == "" {
			observation.FailureClass = "tls_readiness_failed"
		}
		return nil, observation, fmt.Errorf("await TLS readiness: %w", err)
	}
	if readiness.StageReached < model.StageTLS || readiness.StageReached > model.StageApplication || len(readiness.Prefetched) == 0 {
		_ = conn.Close()
		observation.Success = false
		observation.FailureClass = "invalid_tls_readiness_result"
		return nil, observation, errors.New("TLS gate returned an incomplete readiness result")
	}
	observation.Success = true
	observation.StageReached = readiness.StageReached
	observation.FailureClass = ""
	return &prefetchedConn{Conn: conn, reader: io.MultiReader(bytes.NewReader(readiness.Prefetched), conn)}, observation, nil
}

type prefetchedConn struct {
	net.Conn
	reader io.Reader
}

func (c *prefetchedConn) Read(buffer []byte) (int, error) { return c.reader.Read(buffer) }

func writeFull(writer io.Writer, value []byte) error {
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

func closeConnectionOnCancel(ctx context.Context, conn net.Conn) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func classifyTLSReadinessFailure(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return FailureTLSCanceled
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return FailureTLSTimeout
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		return FailureTLSTimeout
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return FailureTLSConnectionClosed
	}
	return tlsinspect.FailureClass(err)
}

func classifyClientHelloWriteFailure(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return FailureTLSCanceled
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return FailureTLSTimeout
	}
	return FailureClientHelloWrite
}
