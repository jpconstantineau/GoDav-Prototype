// Package dav implements the authenticated, confined DAV transport boundary.
package dav

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"github.com/jpconstantineau/GoDav-Prototype/internal/requestlog"
)

type AccountLookup interface {
	LookupEnabled(context.Context, string) (auth.Account, bool, error)
}

func Handler(accounts AccountLookup, collections *files.Service, origin string, maxBytes int64) http.Handler {
	return HandlerWithMCP(accounts, collections, origin, maxBytes, "", nil)
}

// HandlerWithMCP shares authority checks and the expensive-authentication budget.
// A nil MCP handler leaves the existing DAV transport and namespace unchanged.
func HandlerWithMCP(accounts AccountLookup, collections *files.Service, origin string, maxBytes int64, mcpUsername string, mcp http.Handler) http.Handler {
	u, _ := url.Parse(origin)
	expected, _ := parseAuthority(u.Host)
	slots := make(chan struct{}, 2)
	mux := http.NewServeMux()
	root := discovery(collections, maxBytes)
	mux.Handle("/dav", root)
	mux.Handle("/dav/", root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusUnauthorized
		reject := func() {
			w.Header().Set("WWW-Authenticate", `Basic realm="GoDav"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}
		if code := routeStatusTransport(r, expected, "https", mcp != nil); code != 0 {
			http.Error(w, http.StatusText(code), code)
			return
		}
		headers := r.Header.Values("Authorization")
		if len(headers) != 1 || len(headers[0]) > 2048 {
			reject()
			return
		}
		username, password, ok := r.BasicAuth()
		if !ok || !auth.ValidUsername(username) || len(password) == 0 || len(password) > 1024 {
			reject()
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			status = 503
			w.Header().Set("GoDav-Rejection", "authentication")
			http.Error(w, "Service Unavailable", status)
			return
		}
		// Expensive verification is bounded separately from admitted DAV work.
		// No receiving upload or slow response retains an authentication permit.
		principal := ""
		found, matches, err := func() (bool, bool, error) {
			defer func() { <-slots }()
			account, found, err := accounts.LookupEnabled(r.Context(), username)
			if err != nil {
				return false, false, err
			}
			if !found {
				account = auth.DummyAccount()
			}
			matches := account.VerifyPassword(password)
			if found && matches {
				principal = account.ID
			}
			return found, matches, r.Context().Err()
		}()
		if err != nil {
			http.Error(w, "Service Unavailable", 503)
			return
		}
		if !found || !matches {
			reject()
			return
		}
		r = r.WithContext(auth.WithPrincipal(r.Context(), principal))
		if mcp != nil && r.URL.Path == "/mcp" {
			if !strings.EqualFold(username, mcpUsername) {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
			mcp.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	writeFailed bool
}

func (w *statusWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		w.writeFailed = true
	}
	return n, err
}
func (w *statusWriter) code() int { return w.status }
func (w *statusWriter) outcome(ctx context.Context) string {
	if requestlog.Cancelled(ctx) || ctx.Err() != nil {
		return "cancelled"
	}
	if w.writeFailed {
		return "write-failed"
	}
	if w.status == 0 {
		return "no-response"
	}
	return "response"
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Trace surrounds the complete application route, including admission, authority
// and auth failures. Upstream's separate Logger must stay nil.
func Trace(logger *slog.Logger, next http.Handler) http.Handler {
	var ids atomic.Uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(requestlog.Begin(r.Context(), logger))
		start := time.Now()
		id := ids.Add(1)
		record := &statusWriter{ResponseWriter: w}
		defer func() {
			fields := []any{"method", safeMethod(r.Method), "path", safePath(r), "status", record.code(), "outcome", record.outcome(r.Context()), "duration", time.Since(start), "request_id", id}
			if r.Method == "MOVE" {
				fields = append(fields, "destination", safeMoveDestination(r))
			}
			logger.Info("request", fields...)
		}()
		next.ServeHTTP(record, r)
	})
}
