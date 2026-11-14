package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func awaitLifecycle(t *testing.T, logs func() string, marker string) {
	t.Helper()
	end := time.Now().Add(8 * time.Second)
	for time.Now().Before(end) {
		if strings.Contains(logs(), marker) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("lifecycle marker absent")
}

// The callback controls a real receiving request using HTTP 100 Continue, not
// private service state. Returned channels never carry credentials or content.
func finishingUpload(t *testing.T, client *http.Client, f serverFixture, path string) (*io.PipeWriter, <-chan int) {
	return receivingRequest(t, client, f, "PUT", path)
}
func receivingRequest(t *testing.T, client *http.Client, f serverFixture, method, path string) (*io.PipeWriter, <-chan int) {
	t.Helper()
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{Got100Continue: func() { ready <- struct{}{} }})
	req, _ := http.NewRequestWithContext(ctx, method, f.origin+path, reader)
	req.Header.Set("Expect", "100-continue")
	req.Header.Set("Depth", "0")
	req.SetBasicAuth("FixtureUser", f.password)
	done := make(chan int, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		resp, err := client.Do(req)
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
			t.Error("request fixture did not join")
		}
	})
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not start")
	}
	if _, err := writer.Write([]byte("complete")); err != nil {
		t.Fatal("upload fixture write failed")
	}
	return writer, done
}

func TestBoundsProcessControlCDrainsAcceptedUpload(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	client := fixtureClient(f)
	client.Timeout = 15 * time.Second
	defer client.CloseIdleConnections()
	_, stdout, logs, code := privateConsoleInteraction(t, t.TempDir(), bin, func(input *os.File, logs func() string) {
		awaitLifecycle(t, logs, "msg=ready")
		body, done := finishingUpload(t, client, f, "/dav/drained")
		if _, err := input.Write([]byte{3}); err != nil {
			t.Fatal("isolated Ctrl+C delivery failed")
		}
		awaitLifecycle(t, logs, "phase=stop-admission")
		req, _ := http.NewRequest("PUT", f.origin+"/dav/too-late", strings.NewReader("late"))
		req.SetBasicAuth("FixtureUser", f.password)
		if response, err := client.Do(req); err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				t.Fatal("stop admitted new work")
			}
		}
		if body.Close() != nil {
			t.Fatal("upload completion failed")
		}
		select {
		case status := <-done:
			if status != 201 {
				t.Fatalf("drained upload status %d", status)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("drained upload did not finish")
		}
	}, "run", "server", "--config", f.config)
	if code != 0 || stdout != "" || !strings.Contains(logs, "result=clean") {
		t.Fatal("Ctrl+C did not produce clean lifecycle result")
	}
	p := startServer(t, bin, f)
	defer p.stop()
	status, _, data := fileRequest(t, client, f, "GET", "/dav/drained", nil, nil)
	if status != 200 || !bytes.Equal(data, []byte("complete")) {
		t.Fatal("drained upload did not survive restart")
	}
}

func TestBoundsReceivingUploadsLeaveIndependentCapacity(t *testing.T) {
	f, client, _ := liveFiles(t, 32<<20)
	status, _, _ := fileRequest(t, client, f, "PUT", "/dav/prior", bytes.NewReader([]byte("prior")), nil)
	if status != 201 {
		t.Fatal("seed failed")
	}
	first := pausedFileUpload(t, client, f, "/dav/one")
	second := pausedFileUpload(t, client, f, "/dav/two")
	status, headers, _ := fileRequest(t, client, f, "PUT", "/dav/third", bytes.NewReader([]byte("third")), nil)
	if status != 503 || headers.Get("GoDav-Rejection") != "receiving" {
		t.Fatalf("third receiving upload status %d", status)
	}
	start := time.Now()
	status, _, data := fileRequest(t, client, f, "GET", "/dav/prior", nil, nil)
	if status != 200 || string(data) != "prior" || time.Since(start) > time.Second {
		t.Fatal("receiving uploads blocked independent read")
	}
	first()
	second()
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, retryHeaders, _ := fileRequest(t, client, f, "PUT", "/dav/after", bytes.NewReader([]byte("after")), nil)
		if status == 201 {
			break
		}
		if status != 503 || !admissionRefusal(retryHeaders) {
			t.Fatal("non-admission failure cannot be retried")
		}
		if time.Now().After(deadline) {
			t.Fatal("upload slots were not released")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBoundsProcessDeadlinePreservesReplacement(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	config, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal("fixture settings read failed")
	}
	config = append(config, []byte("  shutdown_grace_ms: 200\n")...)
	if os.WriteFile(f.config, config, 0600) != nil {
		t.Fatal("fixture grace setup failed")
	}
	client := fixtureClient(f)
	client.Timeout = 15 * time.Second
	defer client.CloseIdleConnections()
	var elapsed time.Duration
	_, stdout, logs, code := privateConsoleInteraction(t, t.TempDir(), bin, func(input *os.File, logs func() string) {
		awaitLifecycle(t, logs, "msg=ready")
		status, _, _ := fileRequest(t, client, f, "PUT", "/dav/prior", bytes.NewReader([]byte("prior")), nil)
		if status != 201 {
			t.Fatal("seed failed")
		}
		body, done := finishingUpload(t, client, f, "/dav/prior")
		defer body.CloseWithError(io.ErrClosedPipe)
		fresh, freshDone := finishingUpload(t, client, f, "/dav/absent")
		defer fresh.CloseWithError(io.ErrClosedPipe)
		start := time.Now()
		if _, err := input.Write([]byte{3}); err != nil {
			t.Fatal("isolated Ctrl+C delivery failed")
		}
		awaitLifecycle(t, logs, "phase=cancelling")
		body.CloseWithError(io.ErrClosedPipe)
		fresh.CloseWithError(io.ErrClosedPipe)
		for _, result := range []<-chan int{done, freshDone} {
			select {
			case status := <-result:
				if status >= 200 && status < 300 {
					t.Fatal("cancelled upload reported success")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled upload did not exit")
			}
		}
		elapsed = time.Since(start)
	}, "run", "server", "--config", f.config)
	if code != 1 || stdout != "" || !strings.Contains(logs, "result=cancelled") || !strings.Contains(logs, "shutdown deadline expired") || strings.Contains(logs, "startup failed") || elapsed > 3*time.Second {
		t.Fatal("deadline cancellation result incorrect")
	}
	p := startServer(t, bin, f)
	defer p.stop()
	status, _, data := fileRequest(t, client, f, "GET", "/dav/prior", nil, nil)
	if status != 200 || string(data) != "prior" {
		t.Fatal("cancelled replacement changed committed bytes")
	}
	status, _, _ = fileRequest(t, client, f, "HEAD", "/dav/absent", nil, nil)
	if status != 404 {
		t.Fatal("cancelled creation published")
	}
	status, _, _ = fileRequest(t, client, f, "PUT", "/dav/after", bytes.NewReader([]byte("after")), nil)
	if status != 201 {
		t.Fatal("restart did not release limits and ownership")
	}
}

func TestBoundsAcceptedConnectionsAndReuse(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	endpoint, err := url.Parse(f.origin)
	if err != nil {
		t.Fatal("fixture origin invalid")
	}
	address := "127.0.0.1:" + endpoint.Port()
	var held []net.Conn
	defer func() {
		for _, conn := range held {
			conn.Close()
		}
	}()
	for range 16 {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			t.Fatal("fixture transport connection failed")
		}
		held = append(held, conn)
	}
	extra, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal("fixture excess connection failed")
	}
	defer extra.Close()
	extra.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	_, err = extra.Read(b[:])
	if err == nil {
		t.Fatal("excess connection unexpectedly served")
	}
	if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("connection above capacity remained open")
	}
	held[0].Close()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(2 * time.Second)
	for {
		req, _ := http.NewRequest("OPTIONS", f.origin+"/dav/", nil)
		req.SetBasicAuth("FixtureUser", f.password)
		resp, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("connection capacity did not recover")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBoundsFourRequestsAndAuthenticationPermitRelease(t *testing.T) {
	f, client, _ := liveFiles(t, 32<<20)
	first, _ := receivingRequest(t, client, f, "PUT", "/dav/one")
	second, _ := receivingRequest(t, client, f, "PUT", "/dav/two")
	third, thirdDone := receivingRequest(t, client, f, "PROPFIND", "/dav/")
	fourth, fourthDone := receivingRequest(t, client, f, "PROPFIND", "/dav/")
	defer first.CloseWithError(io.ErrClosedPipe)
	defer second.CloseWithError(io.ErrClosedPipe)
	start := time.Now()
	status, _, _ := fileRequest(t, client, f, "OPTIONS", "/dav/", nil, nil)
	if status != 503 || time.Since(start) > time.Second {
		t.Fatal("application admission is not bounded")
	}
	third.CloseWithError(io.ErrClosedPipe)
	fourth.CloseWithError(io.ErrClosedPipe)
	for _, done := range []<-chan int{thirdDone, fourthDone} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("cancelled metadata read did not finish")
		}
	}
	deadline := time.Now().Add(time.Second)
	for {
		status, _, _ = fileRequest(t, client, f, "OPTIONS", "/dav/", nil, nil)
		if status == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow uploads retained authentication permits or leaked admission")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBoundsAuthenticationWaitDoesNotQueue(t *testing.T) {
	bin := development(t)
	paused := faultBuild(t, "godav_test_pause_file_commit")
	f := newServerFixture(t, bin)
	p := startServer(t, paused, f)
	defer p.stop()
	client := fixtureClient(f)
	client.Timeout = 10 * time.Second
	defer client.CloseIdleConnections()
	publication := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest("PUT", f.origin+"/dav/paused", strings.NewReader("staged"))
		req.SetBasicAuth("FixtureUser", f.password)
		resp, err := client.Do(req)
		if err != nil {
			publication <- 0
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		publication <- resp.StatusCode
	}()
	awaitLifecycle(t, p.logs.String, "test-only file publication pause")
	type result struct {
		status    int
		duration  time.Duration
		rejection string
	}
	results := make(chan result, 3)
	start := make(chan struct{})
	for range 3 {
		go func() {
			<-start
			began := time.Now()
			req, _ := http.NewRequest("HEAD", f.origin+"/dav/missing", nil)
			req.SetBasicAuth("FixtureUser", f.password)
			resp, err := client.Do(req)
			if err != nil {
				results <- result{}
				return
			}
			resp.Body.Close()
			results <- result{resp.StatusCode, time.Since(began), resp.Header.Get("GoDav-Rejection")}
		}()
	}
	close(start)
	immediate, waited := 0, 0
	for range 3 {
		r := <-results
		if r.status != 503 {
			t.Fatalf("auth contention status %d", r.status)
		}
		if r.duration < time.Second {
			if r.rejection != "authentication" {
				t.Fatal("immediate refusal lacks exact admission category")
			}
			immediate++
		} else if r.duration >= 1500*time.Millisecond && r.duration < 3500*time.Millisecond {
			if r.rejection != "" {
				t.Fatal("operation timeout incorrectly authorizes retry")
			}
			waited++
		}
	}
	if immediate != 1 || waited != 2 {
		t.Fatalf("auth admission got %d immediate and %d bounded waits", immediate, waited)
	}
	if !strings.Contains(p.logs.String(), "msg=store-gate result=timeout") || !strings.Contains(p.logs.String(), "wait=") {
		t.Fatal("bounded acquisition lacks safe wait diagnostics")
	}
	if <-publication != 500 {
		t.Fatal("publication fault outcome incorrect")
	}
	status, _, _ := fileRequest(t, client, f, "OPTIONS", "/dav/", nil, nil)
	if status != 200 {
		t.Fatal("authentication permits leaked")
	}
}

// Only the explicit pre-operation admission marker authorizes retry. An
// arbitrary 503, lost response or transport error could have an uncertain
// mutation outcome and is never retried here.
func retryAdmission(client *http.Client, req *http.Request) (*http.Response, error) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		response, err := client.Do(req)
		if err != nil || response.StatusCode != 503 || !admissionRefusal(response.Header) || time.Now().After(deadline) {
			return response, err
		}
		if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
			return response, nil
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		next := req.Clone(req.Context())
		if req.GetBody != nil {
			next.Body, err = req.GetBody()
			if err != nil {
				return nil, err
			}
		}
		req = next
		time.Sleep(20 * time.Millisecond)
	}
}
func admissionRefusal(headers http.Header) bool {
	switch headers.Get("GoDav-Rejection") {
	case "requests", "receiving", "authentication":
		return true
	}
	return false
}

func TestBoundsProcessCancelsOwnedTransaction(t *testing.T) {
	bin := development(t)
	paused := faultBuild(t, "godav_test_pause_file_commit")
	f := newServerFixture(t, bin)
	config, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal("fixture config failed")
	}
	config = append(config, []byte("  shutdown_grace_ms: 100\n")...)
	if os.WriteFile(f.config, config, 0600) != nil {
		t.Fatal("fixture grace failed")
	}
	client := fixtureClient(f)
	client.Timeout = 12 * time.Second
	defer client.CloseIdleConnections()
	p := startServer(t, bin, f)
	status, _, _ := fileRequest(t, client, f, "PUT", "/dav/prior", strings.NewReader("original"), nil)
	if status != 201 {
		t.Fatal("seed failed")
	}
	p.stop()
	client.CloseIdleConnections()
	_, _, logs, code := privateConsoleInteraction(t, t.TempDir(), paused, func(input *os.File, logs func() string) {
		awaitLifecycle(t, logs, "msg=ready")
		done := make(chan int, 1)
		go func() {
			req, _ := http.NewRequest("PUT", f.origin+"/dav/prior", strings.NewReader("replacement"))
			req.SetBasicAuth("FixtureUser", f.password)
			resp, err := client.Do(req)
			if err != nil {
				done <- 0
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			done <- resp.StatusCode
		}()
		awaitLifecycle(t, logs, "test-only file publication pause")
		if _, err := input.Write([]byte{3}); err != nil {
			t.Fatal("isolated Ctrl+C failed")
		}
		select {
		case status := <-done:
			if status >= 200 && status < 300 {
				t.Fatal("uncommitted transaction reported success")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("cancelled transaction did not exit")
		}
	}, "run", "server", "--config", f.config)
	if code != 1 || !strings.Contains(logs, "result=cancelled") {
		t.Fatal("transaction cancellation lifecycle incorrect")
	}
	p = startServer(t, bin, f)
	defer p.stop()
	status, _, data := fileRequest(t, client, f, "GET", "/dav/prior", nil, nil)
	if status != 200 || string(data) != "original" {
		t.Fatal("shutdown published partial replacement or closed database underneath owner")
	}
	status, _, _ = fileRequest(t, client, f, "PUT", "/dav/after", strings.NewReader("ok"), nil)
	if status != 201 {
		t.Fatal("transaction ownership was not released")
	}
}

func TestBoundsCommittedUnreadResponseSurvivesStop(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	_, _, logs, code := privateConsoleInteraction(t, t.TempDir(), bin, func(input *os.File, logs func() string) {
		awaitLifecycle(t, logs, "msg=ready")
		u, _ := url.Parse(f.origin)
		conn, err := tls.Dial("tcp", "127.0.0.1:"+u.Port(), &tls.Config{RootCAs: f.roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatal("trusted fixture connection failed")
		}
		defer conn.Close()
		req, _ := http.NewRequest("PUT", f.origin+"/dav/unread", strings.NewReader("committed"))
		req.SetBasicAuth("FixtureUser", f.password)
		if req.Write(conn) != nil {
			t.Fatal("fixture request transmission failed")
		}
		awaitLifecycle(t, logs, "status=201")
		conn.Close() // Deliberately never read the successful response.
		if _, err := input.Write([]byte{3}); err != nil {
			t.Fatal("isolated Ctrl+C failed")
		}
	}, "run", "server", "--config", f.config)
	if code != 0 || !strings.Contains(logs, "result=clean") {
		t.Fatal("committed-response stop failed")
	}
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	status, _, data := fileRequest(t, client, f, "GET", "/dav/unread", nil, nil)
	if status != 200 || string(data) != "committed" {
		t.Fatal("lost response rolled back committed outcome")
	}
}

func TestBoundsInvalidGraceRefusesWithoutMutation(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	original, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal("fixture config failed")
	}
	for _, value := range []string{"0", "-1", "30001", "\"200\"", "true", "1.5", "200\n  shutdown_grace_ms: 200", "200\n  unknown_limit: 1"} {
		candidate := append(bytes.Clone(original), []byte("  shutdown_grace_ms: "+value+"\n")...)
		if os.WriteFile(f.config, candidate, 0600) != nil {
			t.Fatal("invalid fixture write failed")
		}
		before, err := os.ReadFile(filepath.Join(f.dir, "godav.sqlite"))
		if err != nil {
			t.Fatal("fixture database read failed")
		}
		out, stderr, code := command(t, t.TempDir(), bin, "run", "server", "--config", f.config)
		after, err := os.ReadFile(filepath.Join(f.dir, "godav.sqlite"))
		if err != nil {
			t.Fatal("fixture database read failed")
		}
		if code != 1 || out != "" || stderr != "godav: server startup failed\n" || !bytes.Equal(before, after) {
			t.Fatal("invalid grace changed persistent state or started")
		}
	}
}

func TestBoundsSlowBodyTimeoutReleasesCapacity(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	status, _, _ := fileRequest(t, client, f, "PUT", "/dav/prior", strings.NewReader("prior"), nil)
	if status != 201 {
		t.Fatal("seed failed")
	}
	u, _ := url.Parse(f.origin)
	conn, err := tls.Dial("tcp", "127.0.0.1:"+u.Port(), &tls.Config{RootCAs: f.roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal("trusted fixture connection failed")
	}
	defer conn.Close()
	req, _ := http.NewRequest("PUT", f.origin+"/dav/prior", strings.NewReader("x"))
	req.SetBasicAuth("FixtureUser", f.password)
	// Declare a fixed length but send a prefix. The raw client can observe server
	// closure independently of an HTTP Transport's blocked request-body producer.
	req.ContentLength = 100
	var wire bytes.Buffer
	if err := req.Write(&wire); err == nil {
		t.Fatal("fixture should have an incomplete body")
	}
	conn.SetDeadline(time.Now().Add(13 * time.Second))
	if _, err := conn.Write(wire.Bytes()); err != nil {
		t.Fatal("slow fixture transmission failed")
	}
	start := time.Now()
	response, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err == nil {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			t.Fatal("incomplete body reported success")
		}
	}
	if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("only the client deadline stopped the body")
	}
	elapsed := time.Since(start)
	if elapsed < 8*time.Second || elapsed > 12*time.Second {
		t.Fatal("server receive timeout was not bounded")
	}
	conn.Close()
	status, _, data := fileRequest(t, client, f, "GET", "/dav/prior", nil, nil)
	if status != 200 || string(data) != "prior" {
		t.Fatal("receive timeout changed content")
	}
	status, _, _ = fileRequest(t, client, f, "PUT", "/dav/after-timeout", strings.NewReader("ok"), nil)
	if status != 201 {
		t.Fatal("receive timeout leaked capacity")
	}
}
func TestBoundsDelayedCleanupRetainsDatabaseOwnership(t *testing.T) {
	bin := development(t)
	stuck := faultBuild(t, "godav_test_stuck_file_commit")
	f := newServerFixture(t, bin)
	config, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal("fixture config failed")
	}
	config = append(config, []byte("  shutdown_grace_ms: 100\n")...)
	if os.WriteFile(f.config, config, 0600) != nil {
		t.Fatal("fixture grace failed")
	}
	client := fixtureClient(f)
	client.Timeout = 12 * time.Second
	defer client.CloseIdleConnections()
	_, _, logs, code := privateConsoleInteraction(t, t.TempDir(), stuck, func(input *os.File, logs func() string) {
		awaitLifecycle(t, logs, "msg=ready")
		done := make(chan int, 1)
		go func() {
			req, _ := http.NewRequest("PUT", f.origin+"/dav/uncommitted", strings.NewReader("uncommitted"))
			req.SetBasicAuth("FixtureUser", f.password)
			resp, err := client.Do(req)
			if err != nil {
				done <- 0
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			done <- resp.StatusCode
		}()
		awaitLifecycle(t, logs, "test-only unresponsive file publication pause")
		if _, err := input.Write([]byte{3}); err != nil {
			t.Fatal("isolated Ctrl+C failed")
		}
		awaitLifecycle(t, logs, "phase=cleanup-delayed")
		if strings.Contains(logs(), "msg=stopped") || strings.Contains(logs(), "phase=closing-state") {
			t.Fatal("database closed while an owner remained")
		}
		out, _, code := command(t, t.TempDir(), bin, "init", "config", "--config", f.config, "--username", "FixtureUser")
		if code != 1 || out != "" {
			t.Fatal("delayed owner released exclusive database ownership early")
		}
		select {
		case status := <-done:
			if status >= 200 && status < 300 {
				t.Fatal("unresponsive transaction reported success")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("forced connection close did not release client")
		}
	}, "run", "server", "--config", f.config)
	if code != 1 || !strings.Contains(logs, "result=failure") || !strings.Contains(logs, "server shutdown failed") {
		t.Fatal("cleanup delay was hidden as clean stop")
	}
	p := startServer(t, bin, f)
	defer p.stop()
	status, _, _ := fileRequest(t, client, f, "HEAD", "/dav/uncommitted", nil, nil)
	if status != 404 {
		t.Fatal("unresponsive transaction published")
	}
	status, _, _ = fileRequest(t, client, f, "PUT", "/dav/recovered", strings.NewReader("ok"), nil)
	if status != 201 {
		t.Fatal("database was not released coherently after delayed owner")
	}
}

func TestBoundsApplicationDeadlineIsTraced(t *testing.T) {
	bin := development(t)
	paused := faultBuild(t, "godav_test_pause_file_commit")
	f := newServerFixture(t, bin)
	p := startServer(t, paused, f)
	defer p.stop()
	client := fixtureClient(f)
	client.Timeout = 16 * time.Second
	defer client.CloseIdleConnections()
	body, done := finishingUpload(t, client, f, "/dav/deadline")
	defer body.CloseWithError(io.ErrClosedPipe)
	time.Sleep(8 * time.Second)
	if body.Close() != nil {
		t.Fatal("staged deadline fixture could not finish")
	}
	awaitLifecycle(t, p.logs.String, "test-only file publication pause")
	select {
	case status := <-done:
		if status >= 200 && status < 300 {
			t.Fatal("deadline returned success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("application deadline did not stop transaction")
	}
	awaitLifecycle(t, p.logs.String, "outcome=cancelled")
	status, _, _ := fileRequest(t, client, f, "HEAD", "/dav/deadline", nil, nil)
	if status != 404 {
		t.Fatal("application deadline published content")
	}
}
