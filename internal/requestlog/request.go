// Package requestlog carries bounded request observations across HTTP, lifecycle
// and storage. It never logs caller-provided paths, identities or error text.
package requestlog

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

type key struct{}
type observation struct {
	logger    *slog.Logger
	id        uint64
	cancelled atomic.Bool
}

// Begin is called once around application handling, including early rejection.
func Begin(ctx context.Context, logger *slog.Logger, id uint64) context.Context {
	return context.WithValue(ctx, key{}, &observation{logger: logger, id: id})
}

// Complete must run before the caller's normal deferred cancel. It records only
// cancellation that actually occurred while the admitted operation was running.
func Complete(ctx context.Context) {
	if state, ok := ctx.Value(key{}).(*observation); ok && ctx.Err() != nil {
		state.cancelled.Store(true)
	}
}
func Cancelled(ctx context.Context) bool {
	state, ok := ctx.Value(key{}).(*observation)
	return ok && state.cancelled.Load()
}

// StoreWait observes the existing single operation gate, which includes readers
// and authentication lookups. It is not a ready-writer metric or transaction timer.
// Only fixed categories and a duration are emitted; errors are never rendered.
func StoreWait(ctx context.Context, elapsed time.Duration, err error) {
	state, ok := ctx.Value(key{}).(*observation)
	if !ok {
		return
	}
	result := "acquired"
	if err != nil {
		result = "timeout"
		if ctx.Err() != nil {
			result = "cancelled"
		}
	}
	state.logger.Info("store-gate", "result", result, "wait", elapsed, "request_id", state.id)
}

func MCPAdmission(ctx context.Context, class, result string, wait time.Duration, queued bool, active, waiting int) {
	if state, ok := ctx.Value(key{}).(*observation); ok {
		state.logger.Info("mcp-admission", "class", class, "result", result, "wait", wait, "queued", queued, "active", active, "waiting", waiting, "request_id", state.id)
	}
}

func MCPExecution(ctx context.Context, class string, elapsed time.Duration, failed bool) {
	if state, ok := ctx.Value(key{}).(*observation); ok {
		result := "complete"
		if failed {
			result = "error"
		}
		state.logger.Info("mcp-execution", "class", class, "result", result, "duration", elapsed, "request_id", state.id)
	}
}
