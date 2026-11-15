package service

import (
	"context"
	"errors"
	"testing"
	"time"

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
func TestDispatchJoinsCancelledRunnerCleanup(t *testing.T) {
	started := make(chan struct{})
	cleaning := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	returned := make(chan error, 1)
	dispatchFailure := errors.New("status publication failed")
	cleanupFailure := errors.New("runner cleanup failed")
	d := &fakeDispatcher{isService: true, execute: func(name string, h svc.Handler) error {
		go func() {
			defer close(finished)
			h.Execute([]string{name}, make(chan svc.ChangeRequest), make(chan svc.Status, 8))
		}()
		<-started
		return dispatchFailure
	}}
	go func() {
		returned <- Dispatch(d, "GoDav-test", func(ctx context.Context, observe server.Observer) error {
			observe(server.Event{Phase: server.Starting})
			close(started)
			<-ctx.Done()
			observe(server.Event{Phase: server.Stopping})
			close(cleaning)
			<-release
			observe(server.Event{Phase: server.Stopped, Err: cleanupFailure})
			return cleanupFailure
		})
	}()
	<-cleaning
	var early error
	earlyReturned := false
	select {
	case early = <-returned:
		earlyReturned = true
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-finished
	if earlyReturned {
		t.Fatalf("Dispatch returned before runner released ownership: %v", early)
	}
	err := <-returned
	if !errors.Is(err, dispatchFailure) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("lost dispatcher or cleanup error: %v", err)
	}
}

func TestDispatchRejectsLateRunnerBeforeApplicationWork(t *testing.T) {
	var captured svc.Handler
	d := &fakeDispatcher{isService: true, execute: func(_ string, h svc.Handler) error { captured = h; return nil }}
	called := false
	err := Dispatch(d, "GoDav-test", func(context.Context, server.Observer) error { called = true; return nil })
	if err == nil {
		t.Fatal("never-started dispatcher succeeded")
	}
	specific, code := captured.Execute([]string{"GoDav-test"}, make(chan svc.ChangeRequest), make(chan svc.Status, 8))
	if called {
		t.Fatal("application work began after dispatcher completion")
	}
	if !specific || code == 0 {
		t.Fatalf("late runner reported success: %v %d", specific, code)
	}
}

// Repeated channel-synchronized starts exercise prompt cancellation racing with
// dispatcher completion. No sleep or external SCM determines the ordering.
func TestDispatchRejectsEarlyNilReturnWithPromptSuccessfulCleanup(t *testing.T) {
	for attempt := 0; attempt < 4096; attempt++ {
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
		err := Dispatch(d, "GoDav-test", func(ctx context.Context, _ server.Observer) error {
			close(started)
			<-ctx.Done()
			return nil
		})
		<-finished
		if err == nil {
			t.Fatalf("early nil dispatcher return lost at prompt cleanup attempt %d", attempt)
		}
	}
}
