package server

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/config"
	"github.com/jpconstantineau/GoDav-Prototype/internal/indexing"
	"github.com/jpconstantineau/GoDav-Prototype/internal/sqlite"
)

type indexingRuntime struct {
	controller *indexing.Engine
	run        func(context.Context) error
	available  atomic.Bool
	cancel     context.CancelFunc
	done       chan struct{}
}

func prepareIndexing(ctx context.Context, accounts *sqlite.AccountStore, cfg config.IndexingSettings, waiting func() int, logger *slog.Logger) (*indexingRuntime, error) {
	if cfg.Root == "" {
		return nil, nil
	}
	admins := cfg.AdminUsers()
	ownerIDs := make(map[string]bool, len(admins))
	for _, admin := range admins {
		account, enabled, err := accounts.LookupEnabled(ctx, admin)
		if err != nil || !enabled {
			return nil, errors.New("indexing administrator account unavailable")
		}
		ownerIDs[account.ID] = true
	}
	store, err := accounts.PrepareIndexing(ctx, cfg.Root)
	if err != nil {
		return nil, errors.New("indexing storage preparation failed")
	}
	engine, err := indexing.New(store, indexing.Options{
		QuietPeriod: cfg.QuietPeriod, PollInterval: cfg.PollInterval, StepTimeout: cfg.StepTimeout,
		MaxFileBytes: cfg.MaxFileBytes, MCPWaiting: waiting,
		OwnerAllowed: func(ownerID string) bool { return ownerIDs[ownerID] },
		ObserveStoreHold: func(stage string, elapsed time.Duration) {
			// The worker also acquires the gate to discover that it is idle.
			// Avoid an unbounded stream of idle-loop INFO records.
			if elapsed < 5*time.Millisecond {
				return
			}
			switch stage {
			case "snapshot", "publish":
				logger.Info("indexing-store", "stage", stage, "hold", elapsed)
			}
		},
	})
	if err != nil {
		return nil, errors.New("indexing configuration invalid")
	}
	return &indexingRuntime{controller: engine, run: engine.Run}, nil
}

func (r *indexingRuntime) start(ctx context.Context, logger *slog.Logger) {
	if r == nil {
		return
	}
	workerCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.done = make(chan struct{})
	r.available.Store(true)
	go func() {
		defer close(r.done)
		err := r.run(workerCtx)
		r.available.Store(false)
		if err != nil && workerCtx.Err() == nil {
			logger.Warn("indexing-worker", "result", "failed")
		}
	}()
}

func (r *indexingRuntime) stop() {
	if r != nil && r.cancel != nil {
		r.available.Store(false)
		r.cancel()
	}
}

func (r *indexingRuntime) join() {
	if r != nil && r.done != nil {
		<-r.done
	}
}
