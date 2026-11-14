package service

import (
	"context"
	"errors"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
	"golang.org/x/sys/windows/svc"
)

// Handler implements the native SCM protocol. Run must not return until its
// resources and request owners have joined. The production runner is
// server.RunObserved; tests drive the same protocol with a fake SCM.
type Handler struct {
	Run func(context.Context, server.Observer) error
}

// Execute returns only after Run finishes. x/sys/svc publishes SERVICE_STOPPED
// using this return value; sending Stopped earlier would release SCM waiters
// before the process has actually released its database ownership.
func (h Handler) Execute(args []string, controls <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	current := svc.Status{State: svc.StartPending, CheckPoint: 1, WaitHint: 10000}
	statuses <- current
	if h.Run == nil || len(args) != 1 || args[0] == "" {
		return true, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// RunObserved emits at most Starting, Ready, Stopping and Stopped. Keep
	// reporting independent of SCM consumption; never block server cleanup.
	events := make(chan server.Event, 4)
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx, func(event server.Event) { events <- event }) }()
	stopping := false
	for {
		select {
		case command, ok := <-controls:
			if !ok {
				controls = nil
				command.Cmd = svc.Stop
			}
			switch command.Cmd {
			case svc.Stop, svc.Shutdown:
				if !stopping {
					stopping = true
					cancel()
					current = svc.Status{State: svc.StopPending, CheckPoint: 1, WaitHint: 35000}
					statuses <- current
				}
			case svc.Interrogate:
				statuses <- current
			}
		case event := <-events:
			switch event.Phase {
			case server.Ready:
				if !stopping {
					current = svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
					statuses <- current
				}
			case server.Stopping:
				stopping = true
				if current.State != svc.StopPending {
					current = svc.Status{State: svc.StopPending, WaitHint: 35000}
				}
				// A checkpoint corresponds to real admission closure, not a
				// timer manufacturing progress during an unresponsive owner.
				current.CheckPoint++
				statuses <- current
			}
		case err := <-done:
			switch {
			case err == nil:
				return false, 0
			case errors.Is(err, server.ErrShutdownFailed):
				return true, 4
			case errors.Is(err, server.ErrShutdownDeadline):
				return true, 3
			case errors.Is(err, server.ErrServing):
				return true, 2
			default:
				return true, 1
			}
		}
	}
}
