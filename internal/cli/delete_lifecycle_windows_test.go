package cli_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"strings"
	"testing"
	"time"
)

func beginEmptyDelete(t *testing.T, c *http.Client, f serverFixture, path string) (*io.PipeWriter, <-chan int) {
	t.Helper()
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{Got100Continue: func() {
		select {
		case ready <- struct{}{}:
		default:
		}
	}})
	req, _ := http.NewRequestWithContext(ctx, "DELETE", f.origin+path, reader)
	req.Header.Set("Expect", "100-continue")
	req.SetBasicAuth("FixtureUser", f.password)
	done := make(chan int, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		resp, err := c.Do(req)
		if err != nil {
			done <- 0
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	t.Cleanup(func() {
		cancel()
		writer.CloseWithError(io.ErrClosedPipe)
		reader.Close()
		select {
		case <-joined:
		case <-time.After(3 * time.Second):
			t.Error("empty DELETE did not join")
		}
	})
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("DELETE did not receive body")
	}
	return writer, done
}

func TestDeleteReceivingRechecksInterveningLock(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/pending", strings.NewReader("prior"), nil)
	if code != 201 {
		t.Fatal("seed")
	}
	body, done := beginEmptyDelete(t, c, f, "/dav/pending")
	token := acquireFixtureLock(t, c, f, "/dav/pending", "0", "Second-60")
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/unrelated", strings.NewReader("healthy"), nil)
	if code != 201 {
		t.Fatal("receiving DELETE blocked unrelated work")
	}
	if body.Close() != nil {
		t.Fatal("DELETE EOF")
	}
	select {
	case code := <-done:
		if code != 423 {
			t.Fatalf("intervening lock: %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("DELETE did not finish")
	}
	code, _, data := fileRequest(t, c, f, "GET", "/dav/pending", nil, nil)
	if code != 200 || string(data) != "prior" {
		t.Fatal("intervening lock did not preserve content")
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/pending", nil, map[string]string{"If": "(" + token + ")"})
	if code != 204 {
		t.Fatal("authorized deletion")
	}
}

func TestDeleteConcurrentConditionalPublication(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	for range 8 {
		code, h, _ := fileRequest(t, c, f, "PUT", "/dav/race", strings.NewReader("prior"), nil)
		if code != 201 {
			t.Fatal("race seed")
		}
		ready := make(chan struct{})
		result := make(chan int, 2)
		for _, method := range []string{"DELETE", "PUT"} {
			var body io.Reader
			if method == "PUT" {
				body = strings.NewReader("replacement")
			}
			req, _ := http.NewRequest(method, f.origin+"/dav/race", body)
			req.SetBasicAuth("FixtureUser", f.password)
			req.Header.Set("If-Match", h.Get("ETag"))
			go func() {
				<-ready
				resp, err := c.Do(req)
				if err != nil {
					result <- 0
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				result <- resp.StatusCode
			}()
		}
		close(ready)
		a, b := <-result, <-result
		if !(a == 204 && b == 412 || a == 412 && b == 204) {
			t.Fatalf("conditional competitors: %d,%d", a, b)
		}
		code, _, data := fileRequest(t, c, f, "GET", "/dav/race", nil, nil)
		if code == 200 {
			if string(data) != "replacement" {
				t.Fatal("partial publication")
			}
			code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/race", nil, nil)
			if code != 204 {
				t.Fatal("race cleanup")
			}
		} else if code != 404 {
			t.Fatalf("race result: %d", code)
		}
	}
}

func TestDeleteControlCDrainsAcceptedRequest(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	_, _, logs, exit := privateConsoleInteraction(t, t.TempDir(), bin, func(input *os.File, logs func() string) {
		awaitLifecycle(t, logs, "msg=ready")
		code, _, _ := fileRequest(t, c, f, "PUT", "/dav/drain", strings.NewReader("prior"), nil)
		if code != 201 {
			t.Fatal("seed")
		}
		body, done := beginEmptyDelete(t, c, f, "/dav/drain")
		if _, err := input.Write([]byte{3}); err != nil {
			t.Fatal("isolated Ctrl+C")
		}
		awaitLifecycle(t, logs, "phase=stop-admission")
		if body.Close() != nil {
			t.Fatal("draining DELETE EOF")
		}
		select {
		case code := <-done:
			if code != 204 {
				t.Fatalf("drained DELETE: %d", code)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("DELETE did not drain")
		}
	}, "run", "server", "--config", f.config)
	if exit != 0 || !strings.Contains(logs, "result=clean") {
		t.Fatal("DELETE drain lifecycle")
	}
	c.CloseIdleConnections()
	p := startServer(t, bin, f)
	defer p.stop()
	code, _, _ := fileRequest(t, c, f, "HEAD", "/dav/drain", nil, nil)
	if code != 404 {
		t.Fatal("drained deletion did not persist")
	}
}

func TestDeleteControlCCancelsOwnedTransaction(t *testing.T) {
	normal := development(t)
	paused := faultBuild(t, "godav_test_pause_delete_commit")
	f := newServerFixture(t, normal)
	config, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal("fixture config")
	}
	if os.WriteFile(f.config, append(config, []byte("  shutdown_grace_ms: 100\n")...), 0600) != nil {
		t.Fatal("fixture grace")
	}
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	p := startServer(t, normal, f)
	fileRequest(t, c, f, "MKCOL", "/dav/tree/", nil, nil)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/tree/file", strings.NewReader("prior"), nil)
	if code != 201 {
		t.Fatal("seed")
	}
	c.CloseIdleConnections()
	p.stop()
	_, _, logs, exit := privateConsoleInteraction(t, t.TempDir(), paused, func(input *os.File, logs func() string) {
		awaitLifecycle(t, logs, "msg=ready")
		done := make(chan int, 1)
		go func() {
			req, _ := http.NewRequest("DELETE", f.origin+"/dav/tree/", nil)
			req.SetBasicAuth("FixtureUser", f.password)
			resp, err := c.Do(req)
			if err != nil {
				done <- 0
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			done <- resp.StatusCode
		}()
		awaitLifecycle(t, logs, "test-only delete publication pause")
		if _, err := input.Write([]byte{3}); err != nil {
			t.Fatal("isolated Ctrl+C")
		}
		select {
		case code := <-done:
			if code >= 200 && code < 300 {
				t.Fatal("cancelled deletion reported success")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("cancelled deletion did not join")
		}
	}, "run", "server", "--config", f.config)
	if exit != 1 || !strings.Contains(logs, "result=cancelled") {
		t.Fatal("DELETE cancellation lifecycle")
	}
	c.CloseIdleConnections()
	p = startServer(t, normal, f)
	defer p.stop()
	code, after, data := fileRequest(t, c, f, "GET", "/dav/tree/file", nil, nil)
	if code != 200 || string(data) != "prior" || after.Get("ETag") != h.Get("ETag") {
		t.Fatal("cancelled deletion changed reopened tree")
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/tree/", nil, nil)
	if code != 204 {
		t.Fatal("cancelled deletion leaked ownership")
	}
}

func TestDeletePrincipalIsolation(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	console, out, stderr, exit := privateConsoleCommand(t, t.TempDir(), bin, "add", "user", "--config", f.config, "--username", "Other.User")
	if exit != 0 {
		t.Fatal("second principal fixture")
	}
	other := accountSecret(t, console, out, stderr)
	p := startServer(t, bin, f)
	defer p.stop()
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	fileRequest(t, c, f, "MKCOL", "/dav/tree/", nil, nil)
	fileRequest(t, c, f, "PUT", "/dav/tree/file", strings.NewReader("prior"), nil)
	token := acquireFixtureLock(t, c, f, "/dav/tree/file", "0", "Second-60")
	for _, path := range []string{"/dav/tree/", "/dav/tree/file"} {
		req, _ := http.NewRequest("DELETE", f.origin+path, nil)
		req.SetBasicAuth("Other.User", other)
		req.Header.Set("If", "</dav/tree/file> ("+token+")")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal("principal request")
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatalf("other principal DELETE %s: %d", path, resp.StatusCode)
		}
	}
	code, _, data := fileRequest(t, c, f, "GET", "/dav/tree/file", nil, nil)
	if code != 200 || string(data) != "prior" {
		t.Fatal("principal refusal changed tree")
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/tree/", nil, map[string]string{"If": "</dav/tree/file> (" + token + ")"})
	if code != 204 {
		t.Fatalf("owner DELETE: %d", code)
	}
}
