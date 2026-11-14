package server

import (
	"net/http"
	"sync"
)

// requestUsers prevents new account users before waiting for old users. Closing
// network connections alone does not imply their handler goroutines have exited.
// This is session lifetime safety, not the later configurable workload scheduler.
type requestUsers struct {
	mu       sync.Mutex
	stopping bool
	active   sync.WaitGroup
}

func (g *requestUsers) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		if g.stopping {
			g.mu.Unlock()
			http.Error(w, "Service Unavailable", 503)
			return
		}
		g.active.Add(1)
		g.mu.Unlock()
		defer g.active.Done()
		next.ServeHTTP(w, r)
	})
}
func (g *requestUsers) stop() { g.mu.Lock(); g.stopping = true; g.mu.Unlock() }
func (g *requestUsers) wait() { g.active.Wait() }
