package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"github.com/jpconstantineau/GoDav-Prototype/internal/config"
	"github.com/jpconstantineau/GoDav-Prototype/internal/mcpskills"
	"github.com/jpconstantineau/GoDav-Prototype/internal/requestlog"
)

// Admission and joining share the same mutex, so no owner can be added after
// stop. Receiving permits are shorter-lived than request ownership: complete
// staging returns them before waiting for SQLite publication.
type requestUsers struct {
	mu                sync.Mutex
	stopping          bool
	active, receiving int
	owners            sync.WaitGroup
	logger            *slog.Logger
	byAccount         map[string]int
	limits            config.Concurrency
	mcp               http.Handler
}

func (g *requestUsers) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upload := r.Method == http.MethodPut
		g.mu.Lock()
		reason := ""
		if g.stopping {
			reason = "stopping"
		} else if g.active >= g.limits.MaxRequests {
			reason = "requests"
		} else if upload && g.receiving >= g.limits.ReceivingUploads {
			reason = "receiving"
		}
		if reason != "" {
			active, receiving := g.active, g.receiving
			g.mu.Unlock()
			g.logger.Info("admission", "result", "rejected", "reason", reason, "active", active, "receiving", receiving)
			w.Header().Set("Connection", "close")
			w.Header().Set("GoDav-Rejection", reason)
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}
		g.active++
		g.owners.Add(1)
		if upload {
			g.receiving++
		}
		g.mu.Unlock()
		defer func() { g.mu.Lock(); g.active--; g.mu.Unlock(); g.owners.Done() }()
		ctx, cancel := context.WithTimeout(r.Context(), g.limits.RequestTimeout)
		defer cancel()
		defer requestlog.Complete(ctx)
		r = r.WithContext(ctx)
		if upload {
			var once sync.Once
			release := func() { once.Do(func() { g.mu.Lock(); g.receiving--; g.mu.Unlock() }) }
			defer release()
			r.Body = &receivingBody{ReadCloser: r.Body, release: release}
		}
		next.ServeHTTP(w, r)
	})
}

// admitAccount is called only for successfully verified stable principals,
// while the request already owns global admission. It never waits or retains
// an authentication permit. Existing outer owners can finish during drain.
func (g *requestUsers) admitAccount(principal string) (func(), bool) {
	g.mu.Lock()
	count := g.byAccount[principal]
	if principal == "" || count >= g.limits.MaxRequestsPerAccount {
		g.mu.Unlock()
		return nil, false
	}
	if g.byAccount == nil {
		g.byAccount = make(map[string]int)
	}
	g.byAccount[principal] = count + 1
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		if g.byAccount[principal] == 1 {
			delete(g.byAccount, principal)
		} else {
			g.byAccount[principal]--
		}
		g.mu.Unlock()
	}, true
}

// Body EOF/error releases only the receiving permit, never request ownership.
type receivingBody struct {
	io.ReadCloser
	release func()
}

func (b *receivingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.release()
	}
	return n, err
}
func (g *requestUsers) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.stopping {
		g.stopping = true
		mcpskills.StopAdmission(g.mcp)
	}
}
func (g *requestUsers) wait() { g.owners.Wait() }
