package supervisor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeProcess struct {
	ctx       context.Context
	immediate bool
	delay     time.Duration
}

func (p fakeProcess) Wait() error {
	if p.immediate {
		return errors.New("synthetic child failure")
	}
	if p.delay > 0 {
		timer := time.NewTimer(p.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			return errors.New("synthetic delayed child failure")
		case <-p.ctx.Done():
			return p.ctx.Err()
		}
	}
	<-p.ctx.Done()
	return p.ctx.Err()
}

func TestSupervisorRestartsFailedServiceAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	starts := 0
	thirdStart := make(chan struct{})
	starter := StartFunc(func(ctx context.Context, _ Service) (Process, error) {
		mu.Lock()
		defer mu.Unlock()
		starts++
		if starts == 3 {
			close(thirdStart)
		}
		return fakeProcess{ctx: ctx, immediate: starts < 3}, nil
	})
	events := make([]Event, 0, 8)
	supervisor := Supervisor{
		Services: []Service{{Name: "guard", Executable: "/synthetic"}}, Starter: starter,
		MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, StableAfter: time.Second,
		OnEvent: func(event Event) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		},
	}
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-thirdStart:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not reach third start")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if starts != 3 || !containsState(events, "started", 3) || !containsState(events, "restart_scheduled", 2) || !containsState(events, "stopped", 3) {
		t.Fatalf("starts=%d events=%+v", starts, events)
	}
}

func TestSupervisorRetriesStartErrorsWithoutStoppingOtherServices(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	starts := map[string]int{}
	bothObserved := make(chan struct{})
	once := sync.Once{}
	starter := StartFunc(func(ctx context.Context, service Service) (Process, error) {
		mu.Lock()
		starts[service.Name]++
		guardStarts := starts["guard"]
		engineStarts := starts["engine"]
		mu.Unlock()
		if guardStarts >= 2 && engineStarts >= 1 {
			once.Do(func() { close(bothObserved) })
		}
		if service.Name == "guard" && guardStarts == 1 {
			return nil, errors.New("synthetic start error")
		}
		return fakeProcess{ctx: ctx}, nil
	})
	supervisor := Supervisor{
		Services: []Service{{Name: "engine", Executable: "/engine"}, {Name: "guard", Executable: "/guard"}},
		Starter:  starter, MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
	}
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-bothObserved:
	case <-time.After(time.Second):
		t.Fatal("services did not start independently")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if starts["engine"] != 1 || starts["guard"] != 2 {
		t.Fatalf("starts = %+v", starts)
	}
}

func TestSupervisorRejectsInvalidServiceSet(t *testing.T) {
	supervisor := Supervisor{
		Services: []Service{{Name: "same", Executable: "/one"}, {Name: "same", Executable: "/two"}},
		Starter:  StartFunc(func(context.Context, Service) (Process, error) { return nil, nil }),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := supervisor.Run(ctx); err == nil {
		t.Fatal("Run() error = nil")
	}
}

func TestSupervisorRejectsNegativeDurations(t *testing.T) {
	supervisor := Supervisor{
		Services:   []Service{{Name: "guard", Executable: "/guard"}},
		Starter:    StartFunc(func(context.Context, Service) (Process, error) { return nil, nil }),
		MinBackoff: -time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := supervisor.Run(ctx); err == nil {
		t.Fatal("Run() error = nil")
	}
}

func TestRestartBackoffCapsWithoutOverflow(t *testing.T) {
	minimum := 100 * time.Millisecond
	maximum := 750 * time.Millisecond
	want := []time.Duration{minimum, 200 * time.Millisecond, 400 * time.Millisecond, maximum, maximum}
	for index, expected := range want {
		if got := restartBackoff(minimum, maximum, index+1); got != expected {
			t.Fatalf("restartBackoff(failures=%d) = %s, want %s", index+1, got, expected)
		}
	}
}

func TestStableRuntimeResetsFailureBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	backoffs := []int64{}
	supervisor := Supervisor{
		Services: []Service{{Name: "engine", Executable: "/engine"}},
		Starter: StartFunc(func(ctx context.Context, _ Service) (Process, error) {
			return fakeProcess{ctx: ctx, delay: 20 * time.Millisecond}, nil
		}),
		MinBackoff: time.Millisecond, MaxBackoff: 8 * time.Millisecond, StableAfter: 5 * time.Millisecond,
		OnEvent: func(event Event) {
			if event.State != "restart_scheduled" {
				return
			}
			mu.Lock()
			backoffs = append(backoffs, event.BackoffMS)
			count := len(backoffs)
			mu.Unlock()
			if count == 2 {
				cancel()
			}
		},
	}
	if err := supervisor.Run(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(backoffs) != 2 || backoffs[0] != 1 || backoffs[1] != 1 {
		t.Fatalf("backoffs = %v", backoffs)
	}
}

func containsState(events []Event, state string, attempt int) bool {
	for _, event := range events {
		if event.EventType == EventType && event.State == state && event.Attempt == attempt {
			return true
		}
	}
	return false
}
