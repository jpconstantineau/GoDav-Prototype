package cli_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Both replacement requests finish authentication before either starts
// publishing. This distinguishes the waiting writer from a request stalled
// in its account lookup on the same SQLite operation gate.
func TestBoundsFullyStagedWriterWaitTimesOut(t *testing.T) {
	normal := development(t)
	paused := faultBuild(t, "godav_test_pause_file_commit")
	f := newServerFixture(t, normal)
	client := fixtureClient(f)
	client.Timeout = 12 * time.Second
	defer client.CloseIdleConnections()

	type committed struct {
		path, body, etag string
	}
	originals := []committed{
		{path: "/dav/writer-holder.txt", body: "holder committed bytes"},
		{path: "/dav/writer-waiter.txt", body: "waiter committed bytes"},
	}
	p := startServer(t, normal, f)
	for i := range originals {
		s := &originals[i]
		status, headers, _ := fileRequest(t, client, f, "PUT", s.path, strings.NewReader(s.body), nil)
		if status != http.StatusCreated || headers.Get("ETag") == "" {
			t.Fatalf("seed %d did not publish with an ETag: HTTP %d", i, status)
		}
		s.etag = headers.Get("ETag")
	}
	client.CloseIdleConnections()
	p.stop()

	p = startServer(t, paused, f)
	defer p.stop()
	holder, holderResult := finishingUpload(t, client, f, originals[0].path)
	waiter, waiterResult := finishingUpload(t, client, f, originals[1].path)
	// Release pipes even when an assertion fails, before stopping the process.
	defer holder.CloseWithError(io.ErrClosedPipe)
	defer waiter.CloseWithError(io.ErrClosedPipe)
	if err := holder.Close(); err != nil {
		t.Fatal("holder EOF failed")
	}
	awaitLifecycle(t, p.logs.String, "test-only file publication pause")

	// Only now fully stage the second writer. The existing test-only holder
	// cannot release the operation gate until its five-second fault finishes.
	started := time.Now()
	if err := waiter.Close(); err != nil {
		t.Fatal("waiter EOF failed")
	}
	select {
	case status := <-waiterResult:
		wait := time.Since(started)
		if status != http.StatusServiceUnavailable || wait < 1500*time.Millisecond {
			t.Fatalf("staged writer did not wait and return HTTP 503: got %d after %s", status, wait)
		}
	case status := <-holderResult:
		t.Fatalf("holder released before the staged writer timed out: HTTP %d", status)
	case <-time.After(3500 * time.Millisecond):
		t.Fatal("staged writer exceeded the 2s gate wait budget")
	}
	// The trace ties the 503 to a store timeout on a previously authenticated
	// request, with no admission marker, lookup failure, or cancellation.
	awaitLifecycle(t, p.logs.String, "status=503 outcome=response")
	logs := p.logs.String()
	_, completion, ok := strings.Cut(logs, "status=503 outcome=response")
	if !ok {
		t.Fatal("waiter response trace missing")
	}
	completion, _, _ = strings.Cut(completion, "\n")
	_, id, ok := strings.Cut(completion, "request_id=")
	if !ok || len(strings.Fields(id)) != 1 {
		t.Fatal("waiter completion lacks a request ID or carries an admission tag")
	}
	id = strings.TrimSpace(id)
	var acquired, timedOut, authFailed bool
	for _, line := range strings.Split(logs, "\n") {
		if !strings.HasSuffix(strings.TrimSpace(line), " request_id="+id) {
			continue
		}
		acquired = acquired || strings.Contains(line, "msg=store-gate result=acquired wait=")
		timedOut = timedOut || strings.Contains(line, "msg=store-gate result=timeout wait=")
		authFailed = authFailed || strings.Contains(line, "msg=authentication-lookup")
	}
	if !acquired || !timedOut || authFailed || strings.Count(logs, "test-only file publication pause") != 1 {
		t.Fatal("waiter did not time out after authentication and before publication")
	}
	select {
	case status := <-holderResult:
		if status != http.StatusInternalServerError {
			t.Fatalf("test-only holder did not roll back: HTTP %d", status)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("publication holder did not release")
	}
	client.CloseIdleConnections()
	p.stop()

	p = startServer(t, normal, f)
	defer p.stop()
	for i, want := range originals {
		status, headers, data := fileRequest(t, client, f, "GET", want.path, nil, nil)
		if status != http.StatusOK || string(data) != want.body || headers.Get("ETag") != want.etag {
			t.Fatalf("restart changed seed %d bytes or ETag: HTTP %d", i, status)
		}
	}
	status, _, _ := fileRequest(t, client, f, "PUT", "/dav/writer-recovery.txt", strings.NewReader("recovered"), nil)
	if status != http.StatusCreated {
		t.Fatalf("reopened store did not accept a fresh write: HTTP %d", status)
	}
}
