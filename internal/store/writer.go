package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

const (
	ReasonDurableQueued    = "durable_evidence_queued"
	ReasonDurableQueueFull = "durable_evidence_queue_full"
	ReasonDurableClosed    = "durable_evidence_writer_closed"

	ReasonPolicyQueued    = "durable_policy_queued"
	ReasonPolicyQueueFull = "durable_policy_queue_full"
	ReasonPolicyClosed    = "durable_policy_writer_closed"
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

type WriterOptions struct {
	Capacity    int
	OnError     func(error)
	OnWritten   func(WriteRequest) error
	OnProcessed func(WriteRequest, bool) error
}

type PolicyRememberer interface {
	RememberDurablePath(context.Context, model.Target, model.Path, time.Time, int) (DurablePolicyChange, error)
}

type PolicyWriteRequest struct {
	Target     model.Target
	Path       model.Path
	ObservedAt time.Time
}

// AsyncPolicyWriter is the minimal persistence path used by automatic mode.
// It stores only the last ready path; it does not create evidence or session rows.
type AsyncPolicyWriter struct {
	store      PolicyRememberer
	maxEntries int
	queue      chan PolicyWriteRequest
	done       chan struct{}
	onError    func(error)

	mu      sync.RWMutex
	closed  bool
	queued  atomic.Uint64
	written atomic.Uint64
	dropped atomic.Uint64
	errors  atomic.Uint64
}

func NewAsyncPolicyWriter(store PolicyRememberer, maxEntries, capacity int, onError func(error)) (*AsyncPolicyWriter, error) {
	if store == nil {
		return nil, errors.New("durable policy store is required")
	}
	if maxEntries < 1 || maxEntries > 1000000 {
		return nil, errors.New("durable policy capacity must be between 1 and 1000000")
	}
	if capacity < 1 || capacity > 65536 {
		return nil, errors.New("durable policy queue capacity must be between 1 and 65536")
	}
	writer := &AsyncPolicyWriter{
		store: store, maxEntries: maxEntries, queue: make(chan PolicyWriteRequest, capacity),
		done: make(chan struct{}), onError: onError,
	}
	go writer.run()
	return writer, nil
}

// Enqueue never blocks. A full queue drops persistence work only; the in-memory
// choice has already been applied and the current connection is unaffected.
func (w *AsyncPolicyWriter) Enqueue(request PolicyWriteRequest) (accepted bool, reason string) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		w.dropped.Add(1)
		return false, ReasonPolicyClosed
	}
	select {
	case w.queue <- request:
		w.queued.Add(1)
		return true, ReasonPolicyQueued
	default:
		w.dropped.Add(1)
		return false, ReasonPolicyQueueFull
	}
}

func (w *AsyncPolicyWriter) Close(ctx context.Context) error {
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

func (w *AsyncPolicyWriter) Stats() WriterStats {
	return WriterStats{Queued: w.queued.Load(), Written: w.written.Load(), Dropped: w.dropped.Load(), Errors: w.errors.Load()}
}

func (w *AsyncPolicyWriter) run() {
	defer close(w.done)
	for request := range w.queue {
		if _, err := w.store.RememberDurablePath(
			context.Background(), request.Target, request.Path, request.ObservedAt, w.maxEntries,
		); err != nil {
			w.errors.Add(1)
			if w.onError != nil {
				w.onError(err)
			}
			continue
		}
		w.written.Add(1)
	}
}

type AsyncWriter struct {
	store       EvidenceAppender
	sessionID   string
	queue       chan WriteRequest
	done        chan struct{}
	onError     func(error)
	onWritten   func(WriteRequest) error
	onProcessed func(WriteRequest, bool) error

	mu      sync.RWMutex
	closed  bool
	queued  atomic.Uint64
	written atomic.Uint64
	skipped atomic.Uint64
	dropped atomic.Uint64
	errors  atomic.Uint64
}

func NewAsyncWriter(store EvidenceAppender, sessionID string, capacity int, onError func(error)) (*AsyncWriter, error) {
	return NewAsyncWriterWithOptions(store, sessionID, WriterOptions{Capacity: capacity, OnError: onError})
}

func NewAsyncWriterWithOptions(store EvidenceAppender, sessionID string, options WriterOptions) (*AsyncWriter, error) {
	if store == nil {
		return nil, errors.New("durable evidence store is required")
	}
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	if options.Capacity < 1 || options.Capacity > 65536 {
		return nil, errors.New("durable evidence queue capacity must be between 1 and 65536")
	}
	writer := &AsyncWriter{
		store: store, sessionID: sessionID, queue: make(chan WriteRequest, options.Capacity),
		done: make(chan struct{}), onError: options.OnError, onWritten: options.OnWritten, onProcessed: options.OnProcessed,
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
			if w.onWritten != nil {
				if err := invokeOnWritten(w.onWritten, request); err != nil {
					w.errors.Add(1)
					if w.onError != nil {
						w.onError(err)
					}
				}
			}
		} else {
			w.skipped.Add(1)
		}
		if w.onProcessed != nil {
			if err := invokeOnProcessed(w.onProcessed, request, written); err != nil {
				w.errors.Add(1)
				if w.onError != nil {
					w.onError(err)
				}
			}
		}
	}
}

func invokeOnProcessed(callback func(WriteRequest, bool) error, request WriteRequest, written bool) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("durable post-process callback panicked")
		}
	}()
	return callback(request, written)
}

func invokeOnWritten(callback func(WriteRequest) error, request WriteRequest) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("durable post-write callback panicked")
		}
	}()
	return callback(request)
}
