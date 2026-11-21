package indexing

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"
	"unicode/utf8"
)

type State string

const (
	Queued    State = "queued"
	Running   State = "running"
	Paused    State = "paused"
	Completed State = "completed"
	Cancelled State = "cancelled"
	Failed    State = "failed"
)

type Pressure string

const (
	Idle        Pressure = "idle"
	Manual      Pressure = "manual"
	MCPQueue    Pressure = "mcp_queue"
	Foreground  Pressure = "foreground"
	QuietPeriod Pressure = "quiet_period"
)

type Status struct {
	ID                                          string
	State                                       State
	Total, Processed, Indexed, Skipped, Backlog int
	PauseReason, ErrorCode                      string
}

var (
	ErrNotFound     = errors.New("index job not found")
	ErrBusy         = errors.New("index operation busy")
	ErrInvalidState = errors.New("index job cannot make that transition")
	ErrUnavailable  = errors.New("index unavailable")
	ErrInvalid      = errors.New("invalid indexing request")
)

type Options struct {
	QuietPeriod, PollInterval, StepTimeout time.Duration
	MaxFileBytes                           int64
	MCPWaiting                             func() int
	ObserveStoreHold                       func(string, time.Duration)
	OwnerAllowed                           func(string) bool
}

func DefaultOptions() Options {
	return Options{QuietPeriod: 100 * time.Millisecond, PollInterval: 25 * time.Millisecond, StepTimeout: 750 * time.Millisecond, MaxFileBytes: 256 << 10}
}

type Action string

const (
	StartAction  Action = "start"
	StatusAction Action = "status"
	PauseAction  Action = "pause"
	ResumeAction Action = "resume"
	CancelAction Action = "cancel"
)

// Work never crosses a network or authorization boundary. Meta is comparable so
// publication can reject stale bytes even when a move leaves the ETag unchanged.
type Work struct {
	JobID              string
	Meta               FileMeta
	Body               []byte
	Oversized, Missing bool
}
type FileMeta struct {
	ID, ETag, Path, ContentType string
	Size, Modified              int64
}

type Budget struct {
	QuietPeriod      time.Duration
	MaxFileBytes     int64
	MCPWaiting       func() int
	ObserveStoreHold func(string, time.Duration)
	OwnerAllowed     func(string) bool
}

// Store binds a single configured logical collection. A Step is either one
// short durable metadata/checkpoint operation, a one-file snapshot, or idle.
type Store interface {
	Control(context.Context, Action, string, string) (Status, error)
	Step(context.Context, Budget) (*Work, Pressure, error)
	Publish(context.Context, Budget, Work, bool) (Pressure, error)
}

// Engine has a single in-memory prepared slot. Persisted pending work, not
// this slot or process lifetime, is the source of truth after cancellation.
type Engine struct {
	store    Store
	opts     Options
	mu       sync.Mutex
	running  bool
	pressure Pressure
	failed   bool
	wake     chan struct{}
}

func New(store Store, o Options) (*Engine, error) {
	d := DefaultOptions()
	if o.QuietPeriod == 0 {
		o.QuietPeriod = d.QuietPeriod
	}
	if o.PollInterval == 0 {
		o.PollInterval = d.PollInterval
	}
	if o.StepTimeout == 0 {
		o.StepTimeout = d.StepTimeout
	}
	if o.MaxFileBytes == 0 {
		o.MaxFileBytes = d.MaxFileBytes
	}
	if store == nil || o.QuietPeriod < 10*time.Millisecond || o.QuietPeriod > 5*time.Second ||
		o.PollInterval < 10*time.Millisecond || o.PollInterval > time.Second ||
		o.StepTimeout < 50*time.Millisecond || o.StepTimeout > 2*time.Second ||
		o.MaxFileBytes < 1 || o.MaxFileBytes > 1<<20 {
		return nil, ErrInvalid
	}
	return &Engine{store: store, opts: o, wake: make(chan struct{}, 1)}, nil
}

func (e *Engine) Start(ctx context.Context, ownerID string) (Status, error) {
	return e.control(ctx, StartAction, ownerID, "")
}
func (e *Engine) Status(ctx context.Context, ownerID, jobID string) (Status, error) {
	return e.control(ctx, StatusAction, ownerID, jobID)
}
func (e *Engine) Pause(ctx context.Context, ownerID, jobID string) (Status, error) {
	return e.control(ctx, PauseAction, ownerID, jobID)
}
func (e *Engine) Resume(ctx context.Context, ownerID, jobID string) (Status, error) {
	return e.control(ctx, ResumeAction, ownerID, jobID)
}
func (e *Engine) Cancel(ctx context.Context, ownerID, jobID string) (Status, error) {
	return e.control(ctx, CancelAction, ownerID, jobID)
}
func (e *Engine) control(ctx context.Context, action Action, owner, jobID string) (Status, error) {
	if !validID(owner) || (jobID != "" && !validID(jobID)) ||
		(jobID == "" && action != StartAction && action != StatusAction) {
		return Status{}, ErrInvalid
	}
	if e.opts.OwnerAllowed != nil && !e.opts.OwnerAllowed(owner) {
		return Status{}, ErrNotFound
	}
	result, err := e.store.Control(ctx, action, owner, jobID)
	if err == nil && action != StatusAction {
		select {
		case e.wake <- struct{}{}:
		default:
		}
	}
	if err == nil && (result.State == Queued || result.State == Running) {
		e.mu.Lock()
		result.PauseReason = string(e.pressure)
		if e.failed {
			result.ErrorCode = "storage_unavailable"
		}
		e.mu.Unlock()
	}
	return result, err
}
func validID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, b := range []byte(s) {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

// Run starts one worker until cancellation. The owning server must cancel and
// join this worker before closing the retained SQLite connection.
func (e *Engine) Run(ctx context.Context) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return ErrBusy
	}
	e.running = true
	e.failed = false
	e.mu.Unlock()
	var pending *Work
	defer func() {
		if pending != nil {
			clear(pending.Body)
		}
		e.mu.Lock()
		e.running = false
		e.pressure = ""
		e.mu.Unlock()
	}()
	budget := Budget{QuietPeriod: e.opts.QuietPeriod, MaxFileBytes: e.opts.MaxFileBytes, MCPWaiting: e.opts.MCPWaiting, ObserveStoreHold: e.opts.ObserveStoreHold, OwnerAllowed: e.opts.OwnerAllowed}
	var eligible bool
	timeouts := 0
	for ctx.Err() == nil {
		var reason Pressure
		var err error
		step, cancel := context.WithTimeout(ctx, e.opts.StepTimeout)
		if pending == nil {
			pending, reason, err = e.store.Step(step, budget)
			cancel()
			// UTF-8 validation and initial text inspection run after SQLite
			// returned and released its connection permit.
			if pending != nil {
				eligible = !pending.Missing && !pending.Oversized && int64(len(pending.Body)) == pending.Meta.Size && pending.Meta.Size <= budget.MaxFileBytes &&
					utf8.Valid(pending.Body) && bytes.IndexByte(pending.Body, 0) < 0
			}
		} else {
			reason, err = e.store.Publish(step, budget, *pending, eligible)
			cancel()
			if reason == "" {
				clear(pending.Body)
				pending = nil
			}
		}
		e.mu.Lock()
		e.pressure = reason
		if reason == Idle {
			e.pressure = ""
		}
		e.mu.Unlock()
		if errors.Is(err, ErrBusy) {
			timeouts++
		} else {
			timeouts = 0
		}
		if ctx.Err() == nil && (err != nil && !errors.Is(err, ErrBusy) || timeouts >= 5) {
			e.mu.Lock()
			e.failed = true
			e.mu.Unlock()
			return ErrUnavailable
		}
		if reason == Idle {
			select {
			case <-ctx.Done():
			case <-e.wake:
			}
			continue
		}
		timer := time.NewTimer(e.opts.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		case <-e.wake:
			timer.Stop()
		}
	}
	return nil
}
