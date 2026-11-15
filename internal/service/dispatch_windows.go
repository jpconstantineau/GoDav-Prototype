package service

import (
	"context"
	"errors"
	"sync"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
	"golang.org/x/sys/windows/svc"
)

// Dispatcher is the native service-host boundary. Tests supply a fake dispatcher.
type Dispatcher interface {
	IsService() (bool, error)
	Run(string, svc.Handler) error
}

type NativeDispatcher struct{}

func (NativeDispatcher) IsService() (bool, error)                   { return svc.IsWindowsService() }
func (NativeDispatcher) Run(name string, handler svc.Handler) error { return svc.Run(name, handler) }

// Dispatch enters SCM hosting only from a verified service process. The native
// dispatcher normally waits for Handler.Execute to finish. A dispatcher that
// returns early cancels and joins any started runner before relinquishing process
// ownership. Cleanup has no forced timeout; late runner starts are rejected.
func Dispatch(dispatcher Dispatcher, name string, run func(context.Context, server.Observer) error) error {
	if dispatcher == nil || run == nil || !serviceName.MatchString(name) {
		return errors.New("invalid service dispatcher request")
	}
	isService, err := dispatcher.IsService()
	if err != nil {
		return err
	}
	if !isService {
		return errors.New("SCM hosting requires a Windows service process")
	}
	dispatchContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan error, 1)
	var gate sync.Mutex
	var started, runnerFinished, dispatchFinished, repeated bool
	handler := Handler{Run: func(ctx context.Context, observer server.Observer) error {
		gate.Lock()
		if dispatchFinished {
			gate.Unlock()
			return errors.New("service runner invoked after dispatcher completion")
		}
		if started {
			repeated = true
			gate.Unlock()
			return errors.New("service runner invoked more than once")
		}
		started = true
		gate.Unlock()
		ctx, cancelRun := context.WithCancel(ctx)
		stopCancellation := context.AfterFunc(dispatchContext, cancelRun)
		defer stopCancellation()
		defer cancelRun()
		result := run(ctx, observer)
		gate.Lock()
		runnerFinished = true
		gate.Unlock()
		completed <- result
		return result
	}}
	dispatchErr := dispatcher.Run(name, handler)
	// Closing this gate and observing started are one operation: a racing runner
	// either owns work that we join below, or is refused before application work.
	gate.Lock()
	dispatchFinished = true
	didStart, didFinish, didRepeat := started, runnerFinished, repeated
	gate.Unlock()
	cancel()
	if didRepeat {
		dispatchErr = errors.Join(dispatchErr, errors.New("service runner invoked more than once"))
	}
	if !didStart {
		return errors.Join(dispatchErr, errors.New("service dispatcher returned without starting the server lifecycle"))
	}
	if !didFinish {
		// Record the pre-cancellation state. Prompt successful cleanup must not turn
		// a premature nil dispatcher return into apparent lifecycle success.
		dispatchErr = errors.Join(dispatchErr, errors.New("service dispatcher returned before the server lifecycle completed"))
	}
	// svc.Run can return after a status publication failure without joining
	// Handler.Execute. Preserve runner-owned cleanup even on that error path.
	return errors.Join(dispatchErr, <-completed)
}
