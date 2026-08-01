package transport

import (
	"context"
	"net"

	"github.com/firfisa/smartroute/internal/model"
)

// CandidateDialer owns one concrete outbound candidate. Implementations must
// honor cancellation and return structured evidence rather than log-derived state.
type CandidateDialer interface {
	Dial(ctx context.Context, target model.Target) (net.Conn, model.Observation, error)
}

// ReadinessGate decides when a connected candidate is safe to commit. A gate
// must never consume bytes that it cannot replay exactly to the selected path.
type ReadinessGate interface {
	Await(ctx context.Context, conn net.Conn, target model.Target) (model.Observation, error)
}
