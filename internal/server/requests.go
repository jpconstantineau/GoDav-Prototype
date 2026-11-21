package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/mcpskills"
	"github.com/jpconstantineau/GoDav-Prototype/internal/requestlog"
)

const maxRequests = 4
const maxReceivingUploads = 2

// Admission and joining share the same mutex, so no owner can be added after
// stop. Receiving permits are shorter-lived than request ownership: complete
// staging returns them before waiting for SQLite publication.
type requestUsers struct {
	mu                sync.Mutex
	stopping          bool
	active, receiving int
	owners            sync.WaitGroup
	logger            *slog.Logger
	mcp               http.Handler
}

func (g *requestUsers) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upload := r.Method == http.MethodPut
		g.mu.Lock()
		reason := ""
		if g.stopping {
			reason = "stopping"
		} else if g.active >= maxRequests {
			reason = "requests"
		} else if upload && g.receiving >= maxReceivingUploads {
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
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
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
