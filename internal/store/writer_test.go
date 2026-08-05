package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

type fakeAppender struct {
	started chan struct{}
	release chan struct{}
	err     error
	written bool
	once    sync.Once
}

type fakePolicyRememberer struct {
	started chan struct{}
	release chan struct{}
	err     error
	once    sync.Once

	mu       sync.Mutex
	requests []PolicyWriteRequest
}

func (f *fakePolicyRememberer) RememberDurablePath(_ context.Context, target model.Target, path model.Path, observedAt time.Time, _ int) (DurablePolicyChange, error) {
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	if f.release != nil {
		<-f.release
	}
	f.mu.Lock()
	f.requests = append(f.requests, PolicyWriteRequest{Target: target, Path: path, ObservedAt: observedAt})
	f.mu.Unlock()
	return DurablePolicyChange{Applied: f.err == nil}, f.err
}

func (f *fakeAppender) AppendStrongEvidence(context.Context, model.Target, string, model.Observation, *model.Observation, time.Time) (bool, error) {
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	if f.release != nil {
		<-f.release
	}
	return f.written, f.err
}

func writerRequest() WriteRequest {
	return WriteRequest{
		Target: storeTarget("home", "example.com", 443),
		Winner: storeWinner(model.PathProxy), Other: storeFailure(model.PathDirect, model.StageOutbound, "failed"),
		ObservedAt: time.Now(),
	}
}

func policyWriterRequest(path model.Path) PolicyWriteRequest {
	return PolicyWriteRequest{
		Target: storeTarget("home", "example.com", 443), Path: path, ObservedAt: time.Now(),
	}
}

func TestAsyncPolicyWriterPersistsOnlyPathAndIsBounded(t *testing.T) {
	rememberer := &fakePolicyRememberer{started: make(chan struct{}), release: make(chan struct{})}
	writer, err := NewAsyncPolicyWriter(rememberer, 10, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if accepted, reason := writer.Enqueue(policyWriterRequest(model.PathDirect)); !accepted || reason != ReasonPolicyQueued {
		t.Fatalf("first enqueue accepted=%v reason=%q", accepted, reason)
	}
	<-rememberer.started
	if accepted, _ := writer.Enqueue(policyWriterRequest(model.PathProxy)); !accepted {
		t.Fatal("buffered policy request not accepted")
	}
	if accepted, reason := writer.Enqueue(policyWriterRequest(model.PathDirect)); accepted || reason != ReasonPolicyQueueFull {
		t.Fatalf("third enqueue accepted=%v reason=%q", accepted, reason)
	}
	close(rememberer.release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	stats := writer.Stats()
	if stats.Queued != 2 || stats.Written != 2 || stats.Dropped != 1 || stats.Skipped != 0 {
		t.Fatalf("stats=%+v", stats)
	}
	rememberer.mu.Lock()
	defer rememberer.mu.Unlock()
	if len(rememberer.requests) != 2 || rememberer.requests[0].Path != model.PathDirect || rememberer.requests[1].Path != model.PathProxy {
		t.Fatalf("requests=%+v", rememberer.requests)
	}
	if accepted, reason := writer.Enqueue(policyWriterRequest(model.PathDirect)); accepted || reason != ReasonPolicyClosed {
		t.Fatalf("closed enqueue accepted=%v reason=%q", accepted, reason)
	}
}

func TestAsyncPolicyWriterErrorDoesNotStopLaterWrites(t *testing.T) {
	rememberer := &sequencePolicyRememberer{errors: []error{errors.New("failed"), nil}}
	var reported atomic.Int32
	writer, err := NewAsyncPolicyWriter(rememberer, 10, 2, func(error) { reported.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	writer.Enqueue(policyWriterRequest(model.PathDirect))
	writer.Enqueue(policyWriterRequest(model.PathProxy))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if stats := writer.Stats(); stats.Errors != 1 || stats.Written != 1 || reported.Load() != 1 {
		t.Fatalf("stats=%+v reported=%d", stats, reported.Load())
	}
}

type sequencePolicyRememberer struct {
	mu     sync.Mutex
	errors []error
}

func (s *sequencePolicyRememberer) RememberDurablePath(context.Context, model.Target, model.Path, time.Time, int) (DurablePolicyChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if len(s.errors) > 0 {
		err, s.errors = s.errors[0], s.errors[1:]
	}
	return DurablePolicyChange{Applied: err == nil}, err
}

func TestAsyncWriterQueueIsNonBlockingAndBounded(t *testing.T) {
	appender := &fakeAppender{started: make(chan struct{}), release: make(chan struct{}), written: true}
	writer, err := NewAsyncWriter(appender, "session", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if accepted, _ := writer.Enqueue(writerRequest()); !accepted {
		t.Fatal("first request not accepted")
	}
	<-appender.started
	if accepted, _ := writer.Enqueue(writerRequest()); !accepted {
		t.Fatal("buffered request not accepted")
	}
	started := time.Now()
	if accepted, reason := writer.Enqueue(writerRequest()); accepted || reason != ReasonDurableQueueFull {
		t.Fatalf("third enqueue accepted=%v reason=%q", accepted, reason)
	}
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("full enqueue blocked for %s", elapsed)
	}
	close(appender.release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	stats := writer.Stats()
	if stats.Queued != 2 || stats.Written != 2 || stats.Dropped != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if accepted, reason := writer.Enqueue(writerRequest()); accepted || reason != ReasonDurableClosed {
		t.Fatalf("closed enqueue accepted=%v reason=%q", accepted, reason)
	}
}

func TestAsyncWriterErrorDoesNotStopLaterWrites(t *testing.T) {
	var errorCount int
	var mu sync.Mutex
	appender := &sequenceAppender{results: []appendResult{{err: errors.New("failed")}, {written: true}}}
	writer, err := NewAsyncWriter(appender, "session", 2, func(error) {
		mu.Lock()
		errorCount++
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	writer.Enqueue(writerRequest())
	writer.Enqueue(writerRequest())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	stats := writer.Stats()
	mu.Lock()
	defer mu.Unlock()
	if stats.Errors != 1 || stats.Written != 1 || errorCount != 1 {
		t.Fatalf("stats=%+v errorCount=%d", stats, errorCount)
	}
}

func TestAsyncWriterCountsSkippedEvidence(t *testing.T) {
	writer, err := NewAsyncWriter(&fakeAppender{}, "session", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	writer.Enqueue(writerRequest())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if stats := writer.Stats(); stats.Skipped != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestAsyncWriterCallsOnWrittenOnlyForPersistedEvidence(t *testing.T) {
	appender := &sequenceAppender{results: []appendResult{{written: true}, {written: false}, {err: errors.New("failed")}}}
	var written []WriteRequest
	var mu sync.Mutex
	writer, err := NewAsyncWriterWithOptions(appender, "session", WriterOptions{
		Capacity: 3,
		OnWritten: func(request WriteRequest) error {
			mu.Lock()
			written = append(written, request)
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		writer.Enqueue(writerRequest())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(written) != 1 {
		t.Fatalf("written callbacks = %d", len(written))
	}
}

func TestAsyncWriterCallsOnProcessedForWinnerOnlyResult(t *testing.T) {
	var callbacks atomic.Int32
	var observedWritten atomic.Bool
	writer, err := NewAsyncWriterWithOptions(&fakeAppender{written: false}, "session", WriterOptions{
		Capacity: 1,
		OnProcessed: func(_ WriteRequest, written bool) error {
			callbacks.Add(1)
			observedWritten.Store(written)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writer.Enqueue(writerRequest())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if callbacks.Load() != 1 || observedWritten.Load() || writer.Stats().Skipped != 1 {
		t.Fatalf("callbacks=%d written=%v stats=%+v", callbacks.Load(), observedWritten.Load(), writer.Stats())
	}
}

func TestAsyncWriterRecoversPostProcessPanicAndContinues(t *testing.T) {
	var callbacks atomic.Int32
	writer, err := NewAsyncWriterWithOptions(&fakeAppender{written: false}, "session", WriterOptions{
		Capacity: 2,
		OnProcessed: func(WriteRequest, bool) error {
			if callbacks.Add(1) == 1 {
				panic("synthetic panic")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writer.Enqueue(writerRequest())
	writer.Enqueue(writerRequest())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if stats := writer.Stats(); stats.Skipped != 2 || stats.Errors != 1 || callbacks.Load() != 2 {
		t.Fatalf("stats=%+v callbacks=%d", stats, callbacks.Load())
	}
}

func TestAsyncWriterAssessmentErrorDoesNotUndoWriteOrStopWorker(t *testing.T) {
	appender := &fakeAppender{written: true}
	var callbacks atomic.Int32
	var reported atomic.Int32
	writer, err := NewAsyncWriterWithOptions(appender, "session", WriterOptions{
		Capacity: 2,
		OnWritten: func(WriteRequest) error {
			if callbacks.Add(1) == 1 {
				return errors.New("assessment failed")
			}
			return nil
		},
		OnError: func(error) { reported.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	writer.Enqueue(writerRequest())
	writer.Enqueue(writerRequest())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	stats := writer.Stats()
	if stats.Written != 2 || stats.Errors != 1 || callbacks.Load() != 2 || reported.Load() != 1 {
		t.Fatalf("stats=%+v callbacks=%d reported=%d", stats, callbacks.Load(), reported.Load())
	}
}

func TestAsyncWriterRecoversAssessmentPanicAndContinues(t *testing.T) {
	appender := &fakeAppender{written: true}
	var callbacks atomic.Int32
	writer, err := NewAsyncWriterWithOptions(appender, "session", WriterOptions{
		Capacity: 2,
		OnWritten: func(WriteRequest) error {
			if callbacks.Add(1) == 1 {
				panic("synthetic panic")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writer.Enqueue(writerRequest())
	writer.Enqueue(writerRequest())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if stats := writer.Stats(); stats.Written != 2 || stats.Errors != 1 || callbacks.Load() != 2 {
		t.Fatalf("stats=%+v callbacks=%d", stats, callbacks.Load())
	}
}

func TestAsyncWriterCloseHonorsContext(t *testing.T) {
	appender := &fakeAppender{started: make(chan struct{}), release: make(chan struct{})}
	writer, err := NewAsyncWriter(appender, "session", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	writer.Enqueue(writerRequest())
	<-appender.started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writer.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close() error = %v", err)
	}
	close(appender.release)
}

func TestAsyncWriterConcurrentEnqueueAndClose(t *testing.T) {
	writer, err := NewAsyncWriter(&fakeAppender{written: true}, "session", 32, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var producers sync.WaitGroup
	for range 16 {
		producers.Add(1)
		go func() {
			defer producers.Done()
			<-start
			for range 100 {
				writer.Enqueue(writerRequest())
			}
		}()
	}
	close(start)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	producers.Wait()
	stats := writer.Stats()
	if stats.Queued != stats.Written+stats.Skipped+stats.Errors {
		t.Fatalf("accepted requests were not drained: %+v", stats)
	}
}

func TestNewAsyncWriterRejectsInvalidInputs(t *testing.T) {
	if _, err := NewAsyncWriter(nil, "session", 1, nil); err == nil {
		t.Fatal("nil store error = nil")
	}
	if _, err := NewAsyncWriter(&fakeAppender{}, "bad session", 1, nil); err == nil {
		t.Fatal("invalid session error = nil")
	}
	if _, err := NewAsyncWriter(&fakeAppender{}, "session", 0, nil); err == nil {
		t.Fatal("invalid capacity error = nil")
	}
}

type appendResult struct {
	written bool
	err     error
}

type sequenceAppender struct {
	mu      sync.Mutex
	results []appendResult
}

func (s *sequenceAppender) AppendStrongEvidence(context.Context, model.Target, string, model.Observation, *model.Observation, time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.results[0]
	s.results = s.results[1:]
	return result.written, result.err
}
