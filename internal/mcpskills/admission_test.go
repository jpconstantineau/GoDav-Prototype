package mcpskills

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"github.com/jpconstantineau/GoDav-Prototype/internal/requestlog"
)

func queueHandler(t *testing.T, dispatch func(context.Context, json.RawMessage) (any, error)) http.Handler {
	t.Helper()
	extra := []Tool{}
	for _, name := range []string{"read_file", "edit_file"} {
		extra = append(extra, Tool{Name: name, InputSchema: map[string]any{"type": "object"}, Call: dispatch})
	}
	h, err := NewWithTools(files.New(&memoryRepo{}), "Skills", extra)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func queueRequest(ctx context.Context, h http.Handler, method, tool string, hold bool) *httptest.ResponseRecorder {
	p := map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": ProtocolVersion, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}}
	if tool != "" {
		p["name"] = tool
		p["arguments"] = map[string]any{"hold": hold}
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": p})
	r := httptest.NewRequest("POST", "https://localhost/mcp", bytes.NewReader(body)).WithContext(auth.WithPrincipal(ctx, "fixture"))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	r.Header.Set("Mcp-Method", method)
	if tool != "" {
		r.Header.Set("Mcp-Name", tool)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func startQueueCall(ctx context.Context, h http.Handler, tool string, hold bool) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- queueRequest(ctx, h, "tools/call", tool, hold) }()
	return done
}

func queueStatus(t *testing.T, w *httptest.ResponseRecorder, status int, reason, retry string) {
	t.Helper()
	if w.Code != status || w.Header().Get("GoDav-Rejection") != reason || w.Header().Get("Retry-After") != retry {
		t.Fatalf("status=%d rejection=%q retry=%q; want %d %q %q", w.Code, w.Header().Get("GoDav-Rejection"), w.Header().Get("Retry-After"), status, reason, retry)
	}
}

func heldQueue(t *testing.T, fn func()) (http.Handler, <-chan struct{}, chan struct{}) {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	h := queueHandler(t, func(_ context.Context, args json.RawMessage) (any, error) {
		var p struct{ Hold bool }
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		if p.Hold {
			close(started)
			<-release
		} else if fn != nil {
			fn()
		}
		return map[string]any{"ok": true}, nil
	})
	return h, started, release
}

func TestMCPAdmissionWaitFullAndRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, started, release := heldQueue(t, nil)
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		var logs bytes.Buffer
		ctx := requestlog.Begin(t.Context(), slog.New(slog.NewTextHandler(&logs, nil)), 42)
		first := startQueueCall(ctx, h, "read_file", true)
		<-started
		waiter := startQueueCall(ctx, h, "read_file", false)
		synctest.Wait()
		select {
		case <-waiter:
			t.Fatal("earlier overlapping call did not wait")
		default:
		}
		queueStatus(t, queueRequest(ctx, h, "tools/call", "read_file", false), 503, "mcp-full", "1")
		close(release)
		queueStatus(t, <-first, 200, "", "")
		queueStatus(t, <-waiter, 200, "", "")
		queueStatus(t, queueRequest(ctx, h, "tools/call", "read_file", false), 200, "", "")
		out := logs.String()
		if !strings.Contains(out, "msg=mcp-admission class=file-read result=full") ||
			!strings.Contains(out, "queued=true") || !strings.Contains(out, "waiting=1") ||
			!strings.Contains(out, "msg=mcp-execution class=file-read result=complete") ||
			!strings.Contains(out, "request_id=42") {
			t.Fatal("missing coarse correlated queue diagnostics")
		}
	})
}

func TestMCPAdmissionControlsBypassQueuedData(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, started, release := heldQueue(t, nil)
		defer close(release)
		first := startQueueCall(t.Context(), h, "read_file", true)
		<-started
		queued := startQueueCall(t.Context(), h, "read_file", false)
		synctest.Wait()
		for _, method := range []string{"server/discover", "tools/list", "ping"} {
			w := queueRequest(t.Context(), h, method, "", false)
			if w.Code != 200 {
				t.Errorf("control %s while data queue full: got %d, want 200", method, w.Code)
			}
		}
		select {
		case <-first:
			t.Fatal("running fixture unexpectedly released")
		case <-queued:
			t.Fatal("control dispatched or rejected queued work")
		default:
		}
	})
}

func TestMCPAdmissionTimeoutNeverDispatchesWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var writes atomic.Int32
		h, started, release := heldQueue(t, func() { writes.Add(1) })
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		first := startQueueCall(t.Context(), h, "read_file", true)
		<-started
		start := time.Now()
		waiter := startQueueCall(t.Context(), h, "edit_file", false)
		synctest.Wait()
		w := <-waiter // the bubble's clock advances, not the wall clock
		if time.Since(start) != dataQueueWait {
			t.Fatalf("wrong queue deadline: %s", time.Since(start))
		}
		queueStatus(t, w, 503, "mcp-timeout", "1")
		close(release)
		queueStatus(t, <-first, 200, "", "")
		if writes.Load() != 0 {
			t.Fatalf("timed-out write dispatched, calls=%d", writes.Load())
		}
		// The callback is available to a fresh request after the timed-out one.
		queueStatus(t, queueRequest(t.Context(), h, "tools/call", "edit_file", false), 200, "", "")
		if writes.Load() != 1 {
			t.Fatal("post-timeout dispatch did not recover")
		}
	})
}

func TestMCPAdmissionCancellationWinsRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var gate dataAdmission
		if got, _ := gate.acquire(t.Context()); got != admitted {
			t.Fatal("cannot occupy gate")
		}
		done := make(chan admissionResult, 1)
		go func() { outcome, _ := gate.acquire(ctx); done <- outcome }()
		synctest.Wait()
		gate.mu.Lock()
		gate.active = false
		gate.notify()
		cancel()
		gate.mu.Unlock()
		if got := <-done; got != queueCancelled {
			t.Fatalf("simultaneous release/cancel granted pending request: %q", got)
		}
		if got, _ := gate.acquire(t.Context()); got != admitted {
			t.Fatalf("cancellation leaked admission capacity: %q", got)
		}
		gate.release()
	})
}

func TestMCPAdmissionShutdownWakesQueuedWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var writes atomic.Int32
		h, started, release := heldQueue(t, func() { writes.Add(1) })
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		first := startQueueCall(t.Context(), h, "read_file", true)
		<-started
		waiter := startQueueCall(t.Context(), h, "edit_file", false)
		synctest.Wait()
		startedAt := time.Now()
		StopAdmission(h)
		queueStatus(t, <-waiter, 503, "mcp-stopping", "1")
		if time.Since(startedAt) != 0 {
			t.Fatal("shutdown did not wake the pending request immediately")
		}
		close(release)
		queueStatus(t, <-first, 200, "", "")
		queueStatus(t, queueRequest(t.Context(), h, "tools/call", "edit_file", false), 503, "mcp-stopping", "1")
		if writes.Load() != 0 {
			t.Fatal("stopped queue dispatched a write")
		}
	})
}
