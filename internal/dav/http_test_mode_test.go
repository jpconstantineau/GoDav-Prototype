package dav_test

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/dav"
)

// Outside-prefix requests exercise transport validation without a storage fixture.
func TestHTTPTestHandlerDefaultPortAndTransportBoundary(t *testing.T) {
	handler := dav.HTTPTestHandler(nil, "http://127.0.0.1:80", 1024)
	for _, tc := range []struct {
		name, host, remote string
		tls                bool
		status             int
	}{
		{"default80", "127.0.0.1", "127.0.0.1:1234", false, 404},
		{"explicit80", "127.0.0.1:80", "127.0.0.1:1234", false, 404},
		{"foreign", "example.test", "127.0.0.1:1234", false, 400},
		{"not-local", "127.0.0.1", "192.0.2.1:1234", false, 403},
		{"TLS", "127.0.0.1", "127.0.0.1:1234", true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("OPTIONS", "/outside", nil)
			req.RequestURI = "/outside"
			req.Host = tc.host
			req.RemoteAddr = tc.remote
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d", w.Code, tc.status)
			}
		})
	}
}

// Credential rejection avoids store operations after successful route validation.
func TestHTTPTestHandlerDefaultPortOriginAndDestination(t *testing.T) {
	// Route validation precedes authentication rejection, which is a public observable seam.
	handler := dav.HTTPTestHandler(nil, "http://127.0.0.1:80", 1024)
	for _, tc := range []struct {
		name, origin, destination string
		status                    int
	}{
		{"matching origin", "http://127.0.0.1", "", 400},
		{"matching destination", "", "http://127.0.0.1/dav/file", 400},
		{"wrong scheme", "", "https://127.0.0.1:80/dav/file", 502},
		{"wrong port", "", "http://127.0.0.1:443/dav/file", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("MOVE", "/dav/file", nil)
			req.RequestURI = "/dav/file"
			req.Host = "127.0.0.1"
			req.RemoteAddr = "127.0.0.1:1234"
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.destination != "" {
				req.Header.Set("Destination", tc.destination)
			}
			req.Header.Set("Authorization", "Basic ignored")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d", w.Code, tc.status)
			}
			if tc.status == 400 && w.Body.String() != "Credentials are not accepted in test mode\n" {
				t.Fatalf("route failed before credential guard: %s", w.Body.String())
			}
		})
	}
}
