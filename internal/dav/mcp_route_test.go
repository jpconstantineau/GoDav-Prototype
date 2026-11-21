package dav

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

type mcpRouteAccounts struct{}

func (mcpRouteAccounts) LookupEnabled(_ context.Context, name string) (auth.Account, bool, error) {
	a := auth.DummyAccount()
	a.Name = name
	a.Key = strings.ToLower(name)
	return a, true, nil
}

func TestMCPOnlyOnExactOptInHTTPSRoute(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unauthenticated call reached MCP")
		w.WriteHeader(204)
	})
	enabled := HandlerWithMCP(mcpRouteAccounts{}, files.New(nil), "https://localhost:8443", 1, "fixture", next)
	disabled := Handler(mcpRouteAccounts{}, files.New(nil), "https://localhost:8443", 1)
	anonymous := HTTPTestHandler(files.New(nil), "http://127.0.0.1:8443", 1)
	cases := []struct {
		name, url, origin, site string
		h                       http.Handler
		status                  int
	}{
		{"disabled", "https://localhost:8443/mcp", "", "", disabled, 404},
		{"anonymous", "http://127.0.0.1:8443/mcp", "", "", anonymous, 404},
		{"missing auth", "https://localhost:8443/mcp", "", "", enabled, 401},
		{"wrong host", "https://127.0.0.1:8443/mcp", "", "", enabled, 400},
		{"wrong origin", "https://localhost:8443/mcp", "https://localhost:8444", "", enabled, 400},
		{"cross site", "https://localhost:8443/mcp", "", "cross-site", enabled, 400},
		{"query", "https://localhost:8443/mcp?x=1", "", "", enabled, 400},
		{"encoded alias", "https://localhost:8443/%6dcp", "", "", enabled, 400},
		{"subpath", "https://localhost:8443/mcp/", "", "", enabled, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, tc.url, nil)
			r.RequestURI = r.URL.RequestURI()
			r.URL.Scheme, r.URL.Host = "", ""
			r.RemoteAddr = "127.0.0.1:12345"
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
			}
			w := httptest.NewRecorder()
			tc.h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d, want %d", w.Code, tc.status)
			}
		})
	}
}
