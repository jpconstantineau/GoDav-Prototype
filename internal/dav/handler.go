// Package dav implements the authenticated, confined DAV transport boundary.
package dav

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
)

type AccountLookup interface {
	LookupEnabled(context.Context, string) (auth.Account, bool, error)
}

func Handler(accounts AccountLookup, origin string) http.Handler {
	u, _ := url.Parse(origin)
	expected, _ := parseAuthority(u.Host)
	slots := make(chan struct{}, 4)
	mux := http.NewServeMux()
	root := discovery()
	mux.Handle("/dav", root)
	mux.Handle("/dav/", root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusUnauthorized
		reject := func() {
			w.Header().Set("WWW-Authenticate", `Basic realm="GoDav"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}
		if code := routeStatus(r, expected); code != 0 {
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
			defer func() { <-slots }()
		default:
			status = 503
			http.Error(w, "Service Unavailable", status)
			return
		}
		account, found, err := accounts.LookupEnabled(r.Context(), username)
		if err != nil {
			status = 503
			http.Error(w, "Service Unavailable", status)
			return
		}
		if !found {
			account = auth.DummyAccount()
		}
		matches := account.VerifyPassword(password)
		if !found || !matches {
			reject()
			return
		}
		mux.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}
func (w *statusWriter) code() int {
	if w.status == 0 {
		return 200
	}
	return w.status
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Trace surrounds the complete application route, including admission, authority
// and auth failures. Upstream's separate Logger must stay nil.
func Trace(logger *slog.Logger, next http.Handler) http.Handler {
	var ids atomic.Uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := ids.Add(1)
		record := &statusWriter{ResponseWriter: w}
		defer func() {
			logger.Info("request", "method", safeMethod(r.Method), "path", safePath(r), "status", record.code(), "duration", time.Since(start), "request_id", id)
		}()
		next.ServeHTTP(record, r)
	})
}
