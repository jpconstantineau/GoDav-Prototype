package server

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"testing/synctest"
)

func TestIndexWorkerStopsAndJoinsBeforeRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, cancelled, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
		worker := &indexingRuntime{run: func(ctx context.Context) error {
			close(entered)
			select {
			case <-ctx.Done():
				close(cancelled)
			case <-release:
				return nil
			}
			<-release
			return nil
		}}
		defer close(release)
		worker.start(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)))
		<-entered
		if !worker.available.Load() {
			t.Fatal("worker is not available while running")
		}
		worker.stop()
		synctest.Wait()
		select {
		case <-cancelled:
		default:
			t.Fatal("shutdown failed to cancel the indexing worker")
		}
		if worker.available.Load() {
			t.Fatal("worker still accepts commands during shutdown")
		}
		go func() { worker.join(); close(joined) }()
		synctest.Wait()
		select {
		case <-joined:
			t.Fatal("storage could close before indexing worker finished")
		default:
		}
	})
}
