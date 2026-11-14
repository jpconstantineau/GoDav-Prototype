package server

// Phase is a factual lifecycle transition, independent of logging or SCM.
type Phase uint8

const (
	Starting Phase = iota + 1
	Ready
	Stopping
	Stopped
)

// Event reports a transition. Err is populated only for Stopped, after all
// server-owned resources have been closed and exclusive ownership released.
type Event struct {
	Phase Phase
	Err   error
}

// Observer runs synchronously on the lifecycle owner. It must return promptly,
// must not panic, and must not wait for another server operation to complete.
// No observer is required by foreground callers.
type Observer func(Event)

func (o Observer) report(phase Phase, err error) {
	if o != nil {
		o(Event{Phase: phase, Err: err})
	}
}
