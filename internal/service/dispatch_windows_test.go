package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
	"golang.org/x/sys/windows/svc"
)

type fakeDispatcher struct {
	isService bool
	checkErr  error
	runCalls  int
	execute   func(string, svc.Handler) error
}

func (d *fakeDispatcher) IsService() (bool, error) { return d.isService, d.checkErr }
func (d *fakeDispatcher) Run(name string, h svc.Handler) error {
	d.runCalls++
	return d.execute(name, h)
}
func TestDispatchRefusesNonServiceAndDetectionFailure(t *testing.T) {
	detectionFailure := errors.New("detection failed")
	for _, d := range []*fakeDispatcher{{isService: false}, {checkErr: detectionFailure}} {
		called := false
		err := Dispatch(d, "GoDav-test", func(context.Context, server.Observer) error { called = true; return nil })
		if err == nil || d.runCalls != 0 || called {
			t.Fatalf("nonservice entered dispatcher: %v", err)
		}
		if d.checkErr != nil && !errors.Is(err, detectionFailure) {
			t.Fatal("lost detection error")
		}
	}
}
func TestDispatchPropagatesDispatchFailure(t *testing.T) {
	failure := errors.New("dispatcher failed")
	d := &fakeDispatcher{isService: true, execute: func(string, svc.Handler) error { return failure }}
	err := Dispatch(d, "GoDav-test", func(context.Context, server.Observer) error { t.Fatal("unexpected runner"); return nil })
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
func TestDispatchRetainsRunnerFailureEvenWhenSCMReturnsNil(t *testing.T) {
	failure := errors.New("server startup failed")
	d := &fakeDispatcher{isService: true, execute: func(name string, h svc.Handler) error {
		statuses := make(chan svc.Status, 8)
		specific, code := h.Execute([]string{name}, make(chan svc.ChangeRequest), statuses)
		if !specific || code != 1 {
			t.Fatalf("handler exit %v %d", specific, code)
		}
		return nil
	}}
	if err := Dispatch(d, "GoDav-test", func(context.Context, server.Observer) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("lost runner failure: %v", err)
	}
}
func TestDispatchRunsObservedLifecycleThroughHandler(t *testing.T) {
	d := &fakeDispatcher{isService: true, execute: func(name string, h svc.Handler) error {
		controls := make(chan svc.ChangeRequest, 1)
		statuses := make(chan svc.Status, 8)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for status := range statuses {
				if status.State == svc.Running {
					controls <- svc.ChangeRequest{Cmd: svc.Stop}
				}
			}
		}()
		specific, code := h.Execute([]string{name}, controls, statuses)
		close(statuses)
		<-done
		if specific || code != 0 {
			t.Fatalf("handler exit %v %d", specific, code)
		}
		return nil
	}}
	err := Dispatch(d, "GoDav-test", func(ctx context.Context, observe server.Observer) error {
		observe(server.Event{Phase: server.Starting})
		observe(server.Event{Phase: server.Ready})
		<-ctx.Done()
		observe(server.Event{Phase: server.Stopping})
		observe(server.Event{Phase: server.Stopped})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestDispatchRejectsMissingRunner(t *testing.T) {
	d := &fakeDispatcher{isService: true, execute: func(string, svc.Handler) error { return nil }}
	if err := Dispatch(d, "GoDav-test", func(context.Context, server.Observer) error { return nil }); err == nil {
		t.Fatal("missing runner succeeded")
	}
}
func TestDispatchDoesNotWaitForIncompleteRunner(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	d := &fakeDispatcher{isService: true, execute: func(name string, h svc.Handler) error {
		go func() {
			defer close(finished)
			h.Execute([]string{name}, make(chan svc.ChangeRequest), make(chan svc.Status, 8))
		}()
		<-started
		return nil
	}}
	err := Dispatch(d, "GoDav-test", func(ctx context.Context, _ server.Observer) error { close(started); <-ctx.Done(); return ctx.Err() })
	if err == nil {
		t.Fatal("incomplete runner succeeded")
	}
	<-finished // Dispatch cancels the incomplete runner, without blocking on it.
}
