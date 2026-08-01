package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/socks5"
)

// SOCKS5Dialer opens one candidate through a loopback SOCKS5 endpoint.
type SOCKS5Dialer struct {
	Path     model.Path
	Endpoint string
}

func (d SOCKS5Dialer) Dial(ctx context.Context, target model.Target) (net.Conn, model.Observation, error) {
	started := time.Now()
	observation := model.Observation{Path: d.Path, StageReached: model.StageOutbound}
	if d.Path != model.PathDirect && d.Path != model.PathProxy {
		observation.FailureClass = "invalid_dialer"
		return nil, observation, fmt.Errorf("candidate path must be direct or proxy")
	}
	if target.Transport != model.TransportTCP {
		observation.FailureClass = "unsupported_transport"
		return nil, observation, fmt.Errorf("SOCKS5 candidate supports TCP only")
	}

	conn, err := socks5.DialContext(ctx, d.Endpoint, socks5.Request{Host: target.Hostname, Port: target.Port})
	observation.Latency = time.Since(started)
	if err != nil {
		observation.FailureClass = classifyDialFailure(ctx, err)
		return nil, observation, err
	}
	observation.Success = true
	observation.StageReached = model.StageTCP
	return conn, observation, nil
}

func classifyDialFailure(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		return "timeout"
	}
	return "socks_connect_failed"
}
