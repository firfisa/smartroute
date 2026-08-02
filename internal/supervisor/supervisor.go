// Package supervisor independently monitors SmartRoute child services. It
// restarts failed processes but does not replay connections lost at process
// boundaries.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

const EventType = "supervisor"

type Service struct {
	Name       string
	Executable string
	Args       []string
}

type Process interface {
	Wait() error
}

type Starter interface {
	Start(ctx context.Context, service Service) (Process, error)
}

type StartFunc func(ctx context.Context, service Service) (Process, error)

func (f StartFunc) Start(ctx context.Context, service Service) (Process, error) {
	return f(ctx, service)
}

type Event struct {
	EventType    string `json:"event_type"`
	Service      string `json:"service"`
	State        string `json:"state"`
	Attempt      int    `json:"attempt"`
	FailureClass string `json:"failure_class,omitempty"`
	BackoffMS    int64  `json:"backoff_ms,omitempty"`
}

type Supervisor struct {
	Services    []Service
	Starter     Starter
	MinBackoff  time.Duration
	MaxBackoff  time.Duration
	StableAfter time.Duration
	OnEvent     func(Event)

	eventMu sync.Mutex
}

func (s *Supervisor) Run(ctx context.Context) error {
	if err := s.normalizeAndValidate(); err != nil {
		return err
	}
	var wait sync.WaitGroup
	for _, service := range s.Services {
		service := service
		wait.Add(1)
		go func() {
			defer wait.Done()
			s.monitor(ctx, service)
		}()
	}
	<-ctx.Done()
	wait.Wait()
	return nil
}

func (s *Supervisor) normalizeAndValidate() error {
	if len(s.Services) == 0 {
		return errors.New("at least one supervised service is required")
	}
	if s.Starter == nil {
		return errors.New("process starter is required")
	}
	if s.MinBackoff < 0 || s.MaxBackoff < 0 || s.StableAfter < 0 {
		return errors.New("restart durations must not be negative")
	}
	if s.MinBackoff == 0 {
		s.MinBackoff = 100 * time.Millisecond
	}
	if s.MaxBackoff == 0 {
		s.MaxBackoff = 5 * time.Second
	}
	if s.MaxBackoff < s.MinBackoff {
		return errors.New("maximum restart backoff must not be less than minimum")
	}
	if s.StableAfter == 0 {
		s.StableAfter = 30 * time.Second
	}
	names := make(map[string]struct{}, len(s.Services))
	for _, service := range s.Services {
		if service.Name == "" || service.Executable == "" {
			return errors.New("supervised service requires name and executable")
		}
		if _, duplicate := names[service.Name]; duplicate {
			return fmt.Errorf("duplicate supervised service %q", service.Name)
		}
		names[service.Name] = struct{}{}
	}
	return nil
}

func (s *Supervisor) monitor(ctx context.Context, service Service) {
	attempt := 0
	consecutiveFailures := 0
	for ctx.Err() == nil {
		attempt++
		startedAt := time.Now()
		process, err := s.Starter.Start(ctx, service)
		if err != nil || process == nil {
			consecutiveFailures++
			s.emit(Event{Service: service.Name, State: "start_failed", Attempt: attempt, FailureClass: "start_error"})
		} else {
			s.emit(Event{Service: service.Name, State: "started", Attempt: attempt})
			err = process.Wait()
			if ctx.Err() != nil {
				s.emit(Event{Service: service.Name, State: "stopped", Attempt: attempt})
				return
			}
			if time.Since(startedAt) >= s.StableAfter {
				consecutiveFailures = 0
			}
			consecutiveFailures++
			failureClass := "unexpected_exit"
			if err != nil {
				failureClass = "exit_error"
			}
			s.emit(Event{Service: service.Name, State: "exited", Attempt: attempt, FailureClass: failureClass})
		}

		backoff := restartBackoff(s.MinBackoff, s.MaxBackoff, consecutiveFailures)
		s.emit(Event{Service: service.Name, State: "restart_scheduled", Attempt: attempt, BackoffMS: backoff.Milliseconds()})
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func restartBackoff(minimum, maximum time.Duration, failures int) time.Duration {
	if failures <= 1 {
		return minimum
	}
	backoff := minimum
	for range failures - 1 {
		if backoff >= maximum || backoff > maximum/2 {
			return maximum
		}
		backoff *= 2
	}
	if backoff > maximum {
		return maximum
	}
	return backoff
}

func (s *Supervisor) emit(event Event) {
	if s.OnEvent == nil {
		return
	}
	event.EventType = EventType
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	s.OnEvent(event)
}

type CommandStarter struct {
	Stdout        io.Writer
	Stderr        io.Writer
	ShutdownGrace time.Duration
}

func (s CommandStarter) Start(ctx context.Context, service Service) (Process, error) {
	if s.ShutdownGrace < 0 {
		return nil, errors.New("shutdown grace must not be negative")
	}
	command := exec.CommandContext(ctx, service.Executable, service.Args...)
	command.Stdout = s.Stdout
	command.Stderr = s.Stderr
	grace := s.ShutdownGrace
	if grace == 0 {
		grace = 2 * time.Second
	}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return command.Process.Signal(os.Interrupt)
	}
	command.WaitDelay = grace
	if err := command.Start(); err != nil {
		return nil, err
	}
	return commandProcess{command: command}, nil
}

type commandProcess struct {
	command *exec.Cmd
}

func (p commandProcess) Wait() error { return p.command.Wait() }
