package mcpskills

import (
	"context"
	"sync"
	"time"
)

const dataQueueWait = 200 * time.Millisecond

type admissionResult string

const (
	admitted       admissionResult = "admitted"
	queueFull      admissionResult = "full"
	queueTimeout   admissionResult = "timeout"
	queueStopping  admissionResult = "stopping"
	queueCancelled admissionResult = "cancelled"
)

// dataAdmission retains the existing single active data operation and permits
// one pending waiter. Reservation and promotion are atomic; a newly arriving
// request may not bypass a pending waiter. No waiter retains a database permit.
type dataAdmission struct {
	mu       sync.Mutex
	active   bool
	stopping bool
	waiter   chan struct{}
}

func (q *dataAdmission) acquire(ctx context.Context) (admissionResult, bool) {
	q.mu.Lock()
	switch {
	case contextEnded(ctx):
		q.mu.Unlock()
		return queueCancelled, false
	case q.stopping:
		q.mu.Unlock()
		return queueStopping, false
	case q.waiter != nil:
		q.mu.Unlock()
		return queueFull, false
	case !q.active:
		q.active = true
		q.mu.Unlock()
		return admitted, false
	}
	wake := make(chan struct{}, 1)
	q.waiter = wake
	deadline := time.Now().Add(dataQueueWait)
	q.mu.Unlock()

	wait, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	select {
	case <-wake:
	case <-wait.Done():
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.waiter = nil
	// Cancellation, the waiting deadline, and shutdown take priority even
	// when capacity became available at the same time as one of them.
	switch {
	case contextEnded(ctx):
		return queueCancelled, true
	case q.stopping:
		return queueStopping, true
	case !time.Now().Before(deadline):
		return queueTimeout, true
	default:
		q.active = true
		return admitted, true
	}
}

func (q *dataAdmission) notify() {
	if q.waiter != nil {
		select {
		case q.waiter <- struct{}{}:
		default:
		}
	}
}

func (q *dataAdmission) release() {
	q.mu.Lock()
	q.active = false
	q.notify()
	q.mu.Unlock()
}

func (q *dataAdmission) stop() {
	q.mu.Lock()
	q.stopping = true
	q.notify()
	q.mu.Unlock()
}

func (q *dataAdmission) occupancy() (int, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	active, waiting := 0, 0
	if q.active {
		active = 1
	}
	if q.waiter != nil {
		waiting = 1
	}
	return active, waiting
}

func contextEnded(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ok && !time.Now().Before(deadline)
}
