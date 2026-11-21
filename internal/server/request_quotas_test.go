package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/config"
	"github.com/jpconstantineau/GoDav-Prototype/internal/dav"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

func TestGlobalRequestQuotaAndRecovery(t *testing.T) {
	g := requestUsers{limits: config.DefaultConcurrency(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	entered := make(chan struct{}, 100)
	done := make(chan int, 100)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	h := g.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			entered <- struct{}{}
			<-release
		}
		w.WriteHeader(204)
	}))
	request := func(method string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "http://127.0.0.1/dav/", nil))
		return w
	}
	for range 100 {
		go func() { done <- request("GET").Code }()
	}
	for range 100 {
		select {
		case <-entered:
		case code := <-done:
			t.Fatalf("capacity below 100: request returned %d", code)
		}
	}
	w := request("HEAD")
	if w.Code != 503 || w.Header().Get("GoDav-Rejection") != "requests" {
		t.Fatalf("101st request got %d/%s, want global 503", w.Code, w.Header().Get("GoDav-Rejection"))
	}
	close(release)
	for range 100 {
		if code := <-done; code != 204 {
			t.Fatalf("in-flight request got %d", code)
		}
	}
	if w := request("HEAD"); w.Code != 204 {
		t.Fatalf("global quota did not recover: %d", w.Code)
	}
	if len(g.byAccount) != 0 {
		t.Fatal("anonymous requests allocated account capacity")
	}
}

type quotaAccounts map[string]auth.Account

func (a quotaAccounts) LookupEnabled(_ context.Context, name string) (auth.Account, bool, error) {
	account, ok := a[strings.ToLower(name)]
	return account, ok, nil
}

type quotaRepo struct{ files.Repository }

func (quotaRepo) CollectionSnapshot(_ context.Context, _ []string, _ bool, _ ...files.Expectation) (files.Snapshot, error) {
	return files.Snapshot{Node: files.Node{ID: "synthetic-root"}}, nil
}

type heldQuotaResponse struct {
	*httptest.ResponseRecorder
	entering chan<- int
	unblock  <-chan struct{}
}

func (w heldQuotaResponse) WriteHeader(code int) {
	w.ResponseRecorder.WriteHeader(code)
	w.entering <- code
	<-w.unblock
}

func TestSharedAuthenticatedAccountQuota(t *testing.T) {
	owner, pwd, err := auth.Generate("FixtureOwner")
	if err != nil {
		t.Fatal(err)
	}
	reader, readerPwd, err := auth.Generate("FixtureReader")
	if err != nil {
		t.Fatal(err)
	}
	g := requestUsers{limits: config.DefaultConcurrency(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	mcp := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	h := g.guard(dav.HandlerWithAdmission(quotaAccounts{"fixtureowner": owner, "fixturereader": reader}, files.New(quotaRepo{}), "https://localhost:8443", 1, "FixtureOwner", mcp, g.admitAccount))
	run := func(w http.ResponseWriter, path, name, password string) {
		method := "OPTIONS"
		if path == "/mcp" {
			method = "POST"
		}
		r := httptest.NewRequest(method, "https://localhost:8443"+path, nil)
		r.RequestURI = path
		r.URL.Scheme, r.URL.Host = "", ""
		r.SetBasicAuth(name, password)
		h.ServeHTTP(w, r)
	}
	type held struct {
		release chan struct{}
		done    chan struct{}
	}
	pending := make([]held, 0, 10)
	defer func() {
		for _, p := range pending {
			select {
			case <-p.release:
			default:
				close(p.release)
			}
			<-p.done
		}
	}()
	for i := range 10 {
		pendingCall := held{make(chan struct{}), make(chan struct{})}
		pending = append(pending, pendingCall)
		started := make(chan int, 1)
		path, want := "/dav/", 200
		if i%2 == 1 {
			path, want = "/mcp", 204
		}
		go func() {
			defer close(pendingCall.done)
			run(heldQuotaResponse{httptest.NewRecorder(), started, pendingCall.release}, path, "FixtureOwner", pwd)
		}()
		if status := <-started; status != want {
			t.Fatalf("quota admitted fewer than 10 mixed requests: status=%d want=%d", status, want)
		}
	}
	request := func(path, user, pass string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		run(w, path, user, pass)
		return w
	}
	for _, path := range []string{"/dav/", "/mcp"} {
		w := request(path, "FIXTUREOWNER", pwd)
		if w.Code != 429 || w.Header().Get("GoDav-Rejection") != "account-requests" {
			t.Fatalf("eleventh request across DAV and MCP got %d/%s, want account 429", w.Code, w.Header().Get("GoDav-Rejection"))
		}
	}
	if w := request("/dav/", "FixtureOwner", "disposable wrong password"); w.Code != 401 {
		t.Fatalf("failed auth allocated capacity or changed status: %d", w.Code)
	}
	if w := request("/dav/", "FixtureReader", readerPwd); w.Code != 200 {
		t.Fatalf("distinct verified account blocked: %d", w.Code)
	}
	readerReleases := []func(){}
	for range 10 {
		release, ok := g.admitAccount(reader.ID)
		if !ok {
			t.Fatal("reader quota fixture setup failed")
		}
		readerReleases = append(readerReleases, release)
	}
	if w := request("/mcp", "FixtureReader", readerPwd); w.Code != 403 {
		t.Fatalf("nonopted account lost its MCP authorization refusal: %d", w.Code)
	}
	for _, release := range readerReleases {
		release()
	}
	close(pending[0].release)
	<-pending[0].done
	if w := request("/mcp", "FIXTUREOWNER", pwd); w.Code != 204 {
		t.Fatalf("released account capacity did not recover: %d", w.Code)
	}
	for _, p := range pending[1:] {
		close(p.release)
		<-p.done
	}
	if len(g.byAccount) != 0 {
		t.Fatal("account counters were not reclaimed")
	}
}
