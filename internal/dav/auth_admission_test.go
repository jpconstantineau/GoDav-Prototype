package dav

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

type heldAuthLookup struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (l heldAuthLookup) LookupEnabled(ctx context.Context, _ string) (auth.Account, bool, error) {
	l.entered <- struct{}{}
	select {
	case <-ctx.Done():
	case <-l.release:
	}
	return auth.Account{}, false, errors.New("disposable lookup failure")
}

func TestAuthAdmissionTenSharedFailFast(t *testing.T) {
	entered, release := make(chan struct{}, 12), make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 11)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	never := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	g := HandlerWithAdmission(heldAuthLookup{entered, release}, files.New(nil), "https://localhost:8443", 1, "fixture", never, func(string) (func(), bool) {
		t.Error("failed lookup reached authenticated admission")
		return func() {}, true
	})
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://localhost:8443"+path, nil)
		r.RequestURI = path
		r.URL.Scheme, r.URL.Host = "", ""
		r.SetBasicAuth("fixture", "disposable password")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	for i := range 10 {
		path := "/dav/"
		if i%2 == 1 {
			path = "/mcp"
		}
		go func() { done <- request(path) }()
		select {
		case <-entered:
		case w := <-done:
			t.Fatalf("authentication admitted fewer than ten requests: HTTP %d", w.Code)
		}
	}
	go func() { done <- request("/mcp") }()
	select {
	case <-entered:
		t.Fatal("eleventh authentication entered the occupied lookup")
	case w := <-done:
		if w.Code != 503 || w.Header().Get("GoDav-Rejection") != "authentication" {
			t.Fatalf("eleventh authentication got %d/%s, want fail-fast marker", w.Code, w.Header().Get("GoDav-Rejection"))
		}
	}
	close(release)
	for range 10 {
		w := <-done
		if w.Code != 503 || w.Header().Get("GoDav-Rejection") != "" {
			t.Fatalf("owned lookup failure got HTTP %d with retry marker %q", w.Code, w.Header().Get("GoDav-Rejection"))
		}
	}
	if w := request("/dav/"); w.Code != 503 || w.Header().Get("GoDav-Rejection") != "" {
		t.Fatalf("authentication permits did not recover: HTTP %d", w.Code)
	}
	select {
	case <-entered:
	default:
		t.Fatal("recovery request never reached account lookup")
	}
}
