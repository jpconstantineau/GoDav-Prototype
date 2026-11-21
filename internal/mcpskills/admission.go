package mcpskills

import (
	"context"
	"slices"
	"sync"
	"time"
)

type admissionResult string

const (
	admitted       admissionResult = "admitted"
	queueFull      admissionResult = "full"
	queueTimeout   admissionResult = "timeout"
	queueStopping  admissionResult = "stopping"
	queueCancelled admissionResult = "cancelled"
)

// dataAdmission gives an explicitly bounded number of operations and FIFO
// waiters permits. Reservation and promotion are atomic; a newly arriving
// request may not bypass a pending waiter. No waiter retains a database permit.
type dataAdmission struct {
	mu              sync.Mutex
	active          int
	capacity, depth int
	wait            time.Duration
	stopping        bool
	waiters         []chan struct{}
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
	case len(q.waiters) >= q.depth:
		q.mu.Unlock()
		return queueFull, false
	case q.active < q.capacity && len(q.waiters) == 0:
		q.active++
		q.mu.Unlock()
		return admitted, false
	}
	wake := make(chan struct{}, 1)
	q.waiters = append(q.waiters, wake)
	deadline := time.Now().Add(q.wait)
	q.mu.Unlock()

	wait, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	select {
	case <-wake:
	case <-wait.Done():
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	// The waiter owns its reservation until it exits. A canceled or expired
	// head hands the wakeup to the next reservation; no newcomer can barge.
	defer func() {
		i := slices.Index(q.waiters, wake)
		q.waiters = slices.Delete(q.waiters, i, i+1)
		q.notify()
	}()
	switch {
	case contextEnded(ctx):
		return queueCancelled, true
	case q.stopping:
		return queueStopping, true
	case !time.Now().Before(deadline):
		return queueTimeout, true
	default:
		// Only release/cleanup can signal the queue head while running.
		q.active++
		return admitted, true
	}
}

func signalWaiter(wake chan struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

// notify signals only the head once the active operation has released. The
// head keeps its reservation through the subsequent locked promotion check.
func (q *dataAdmission) notify() {
	if q.active < q.capacity && !q.stopping && len(q.waiters) > 0 {
		signalWaiter(q.waiters[0])
	}
}
func (q *dataAdmission) release() {
	q.mu.Lock()
	q.active--
	q.notify()
	q.mu.Unlock()
}

func (q *dataAdmission) stop() {
	q.mu.Lock()
	q.stopping = true
	for _, wake := range q.waiters {
		signalWaiter(wake)
	}
	q.mu.Unlock()
}

func (q *dataAdmission) occupancy() (int, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.active, len(q.waiters)
}

func contextEnded(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ok && !time.Now().Before(deadline)
}
