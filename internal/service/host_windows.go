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
	// A single FIFO orders lifecycle transitions before the terminal result.
	// Buffer all four RunObserved events plus completion so delayed SCM status
	// consumption cannot prevent server resource cleanup.
	type update struct {
		event    server.Event
		finished bool
		result   error
	}
	updates := make(chan update, 5)
	go func() {
		err := h.Run(ctx, func(event server.Event) { updates <- update{event: event} })
		updates <- update{finished: true, result: err}
	}()
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
		case u := <-updates:
			if u.finished {
				switch {
				case u.result == nil:
					return false, 0
				case errors.Is(u.result, server.ErrShutdownFailed):
					return true, 4
				case errors.Is(u.result, server.ErrShutdownDeadline):
					return true, 3
				case errors.Is(u.result, server.ErrServing):
					return true, 2
				default:
					return true, 1
				}
			}
			switch u.event.Phase {
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

		}
	}
}
