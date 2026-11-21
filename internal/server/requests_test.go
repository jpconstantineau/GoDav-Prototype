package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"github.com/jpconstantineau/GoDav-Prototype/internal/mcpskills"
)

func TestRequestUsersStopClosesMCPAdmission(t *testing.T) {
	calls := 0
	mcp, err := mcpskills.NewWithTools(files.New(nil), "Skills", []mcpskills.Tool{{
		Name: "read_file", InputSchema: map[string]any{"type": "object"},
		Call: func(context.Context, json.RawMessage) (any, error) {
			calls++
			return map[string]any{"ok": true}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	users := requestUsers{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), mcp: mcp}
	request := func(h http.Handler) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://localhost/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","arguments":{}}}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", mcpskills.LegacyVersion)
		r = r.WithContext(auth.WithPrincipal(r.Context(), "fixture"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := request(users.guard(mcp))
	if w.Code != 200 || calls != 1 {
		t.Fatal("initial MCP request did not dispatch")
	}
	users.stop()
	// The outer guard owns future requests, and the MCP gate independently
	// stops any data request that already got past the outer guard.
	outer := request(users.guard(mcp))
	if outer.Code != 503 || outer.Header().Get("GoDav-Rejection") != "stopping" {
		t.Fatal("outer admission remained open")
	}
	inner := request(mcp)
	if inner.Code != 503 || inner.Header().Get("GoDav-Rejection") != "mcp-stopping" || calls != 1 {
		t.Fatalf("lifecycle stop did not close MCP admission: status=%d calls=%d", inner.Code, calls)
	}
}
