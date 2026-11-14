package cli_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
)

func TestServiceObserverReleasesStateBeforeStopped(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan server.Event, 8)
	done := make(chan error, 1)
	finished := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(8 * time.Second):
			t.Error("observer fixture did not join")
		}
	})
	go func() {
		defer close(finished)
		done <- server.RunObserved(ctx, f.config, server.Identity{}, io.Discard, func(e server.Event) { events <- e })
	}()
	next := func(want server.Phase) {
		t.Helper()
		select {
		case event := <-events:
			if event.Phase != want || event.Err != nil {
				t.Fatalf("unexpected lifecycle event: %v", event.Phase)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("missing lifecycle event")
		}
	}
	next(server.Starting)
	next(server.Ready)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	response := davRequest(t, client, f, "OPTIONS", "/dav/", true)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("ready did not serve authenticated request")
	}
	cancel()
	next(server.Stopping)
	next(server.Stopped)
	if err := <-done; err != nil {
		t.Fatal("observed stop failed")
	}
	// The real executable is an independent ownership contender. No internal
	// DB query or ownership probe is substituted for restart behavior.
	restarted := startServer(t, bin, f)
	restarted.stop()
}
