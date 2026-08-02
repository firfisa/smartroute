package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

const (
	ReasonDurableQueued    = "durable_evidence_queued"
	ReasonDurableQueueFull = "durable_evidence_queue_full"
	ReasonDurableClosed    = "durable_evidence_writer_closed"
)

type EvidenceAppender interface {
	AppendStrongEvidence(context.Context, model.Target, string, model.Observation, *model.Observation, time.Time) (bool, error)
}

type WriteRequest struct {
	Target     model.Target
	Winner     model.Observation
	Other      *model.Observation
	ObservedAt time.Time
}

type WriterStats struct {
	Queued  uint64 `json:"queued"`
	Written uint64 `json:"written"`
	Skipped uint64 `json:"skipped"`
	Dropped uint64 `json:"dropped"`
	Errors  uint64 `json:"errors"`
}

type AsyncWriter struct {
	store     EvidenceAppender
	sessionID string
	queue     chan WriteRequest
	done      chan struct{}
	onError   func(error)

	mu      sync.RWMutex
	closed  bool
	queued  atomic.Uint64
	written atomic.Uint64
	skipped atomic.Uint64
	dropped atomic.Uint64
	errors  atomic.Uint64
}

func NewAsyncWriter(store EvidenceAppender, sessionID string, capacity int, onError func(error)) (*AsyncWriter, error) {
	if store == nil {
		return nil, errors.New("durable evidence store is required")
	}
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	if capacity < 1 || capacity > 65536 {
		return nil, errors.New("durable evidence queue capacity must be between 1 and 65536")
	}
	writer := &AsyncWriter{
		store: store, sessionID: sessionID, queue: make(chan WriteRequest, capacity),
		done: make(chan struct{}), onError: onError,
	}
	go writer.run()
	return writer, nil
}

// Enqueue never blocks. Its reason is safe to expose in a decision event.
func (w *AsyncWriter) Enqueue(request WriteRequest) (accepted bool, reason string) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		w.dropped.Add(1)
		return false, ReasonDurableClosed
	}
	select {
	case w.queue <- request:
		w.queued.Add(1)
		return true, ReasonDurableQueued
	default:
		w.dropped.Add(1)
		return false, ReasonDurableQueueFull
	}
}

func (w *AsyncWriter) Close(ctx context.Context) error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.queue)
	}
	w.mu.Unlock()
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *AsyncWriter) Stats() WriterStats {
	return WriterStats{
		Queued: w.queued.Load(), Written: w.written.Load(), Skipped: w.skipped.Load(),
		Dropped: w.dropped.Load(), Errors: w.errors.Load(),
	}
}

func (w *AsyncWriter) run() {
	defer close(w.done)
	for request := range w.queue {
		written, err := w.store.AppendStrongEvidence(
			context.Background(), request.Target, w.sessionID,
			request.Winner, request.Other, request.ObservedAt,
		)
		if err != nil {
			w.errors.Add(1)
			if w.onError != nil {
				w.onError(err)
			}
			continue
		}
		if written {
			w.written.Add(1)
		} else {
			w.skipped.Add(1)
		}
	}
}
