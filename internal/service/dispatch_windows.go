package service

import (
	"context"
	"errors"
	"sync/atomic"

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
// returns early is an error, never evidence that the server stopped successfully.
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
	var started, repeated atomic.Bool
	handler := Handler{Run: func(ctx context.Context, observer server.Observer) error {
		if !started.CompareAndSwap(false, true) {
			repeated.Store(true)
			return errors.New("service runner invoked more than once")
		}
		ctx, cancelRun := context.WithCancel(ctx)
		stopCancellation := context.AfterFunc(dispatchContext, cancelRun)
		defer stopCancellation()
		defer cancelRun()
		result := run(ctx, observer)
		completed <- result
		return result
	}}
	dispatchErr := dispatcher.Run(name, handler)
	if repeated.Load() {
		dispatchErr = errors.Join(dispatchErr, errors.New("service runner invoked more than once"))
	}
	select {
	case result := <-completed:
		return errors.Join(dispatchErr, result)
	default:
		return errors.Join(dispatchErr, errors.New("service dispatcher returned without a completed server lifecycle"))
	}
}
