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

func TestLocksLifecycleRefreshAndExpiry(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/lifetime", strings.NewReader("committed"), nil)
	if code != 201 {
		t.Fatalf("seed: %d", code)
	}
	token := acquireFixtureLock(t, c, f, "/dav/lifetime", "0", "Second-2")
	time.Sleep(500 * time.Millisecond)
	code, h, body := fileRequest(t, c, f, "LOCK", "/dav/lifetime", nil, map[string]string{"If": "(" + token + ")", "Timeout": "Second-3"})
	if code != 200 || h.Get("Lock-Token") != "" || !strings.Contains(string(body), "Second-3") || !strings.Contains(string(body), strings.Trim(token, "<>")) {
		t.Fatalf("refresh must retain token, report requested lifetime, and omit Lock-Token: %d", code)
	}
	refreshed := time.Now()
	time.Sleep(1700 * time.Millisecond)
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/lifetime", strings.NewReader("must not publish"), nil)
	if code != 423 {
		t.Fatalf("refresh did not extend lifetime beyond original expiry: %d", code)
	}
	time.Sleep(time.Until(refreshed.Add(3200 * time.Millisecond)))
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/lifetime", strings.NewReader("stale token"), map[string]string{"If": "(" + token + ")"})
	if code != 412 {
		t.Fatalf("expired token remains true: %d", code)
	}
	code, _, body = fileRequest(t, c, f, "GET", "/dav/lifetime", nil, nil)
	if code != 200 || string(body) != "committed" {
		t.Fatal("refused writes changed committed bytes")
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/lifetime", strings.NewReader("after expiry"), nil)
	if code != http.StatusNoContent {
		t.Fatalf("expired lock still blocks save: %d", code)
	}
}

func TestLocksLifecycleRestartAndSafeLogs(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/persistent", strings.NewReader("committed bytes"), nil)
	if code != 201 {
		t.Fatalf("seed: %d", code)
	}
	token := acquireFixtureLock(t, c, f, "/dav/persistent", "0", "Second-60")
	patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32FileAttributes>00000020</z:Win32FileAttributes></d:prop></d:set></d:propertyupdate>`
	code, _, body := metadataPatch(t, c, f, "/dav/persistent", strings.NewReader(patch), map[string]string{"If": "(" + token + ")"}, "Win32FileAttributes")
	if code != 207 || !strings.Contains(string(body), "200 OK") {
		t.Fatal("locked metadata publication failed")
	}
	code, before, _ := fileRequest(t, c, f, "HEAD", "/dav/persistent", nil, nil)
	if code != 200 || before.Get("ETag") == "" {
		t.Fatal("committed metadata missing")
	}
	code, _, _ = fileRequest(t, c, f, "UNLOCK", "/dav/persistent", nil, map[string]string{"Lock-Token": token})
	if code != 204 {
		t.Fatalf("unlock: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/persistent", strings.NewReader("must not publish"), map[string]string{"If": "(" + token + ")"})
	if code != 412 {
		t.Fatalf("unlocked token still true: %d", code)
	}
	active := acquireFixtureLock(t, c, f, "/dav/persistent", "0", "Second-60")
	c.CloseIdleConnections()
	p.stop() // Actual CLI termination/reopen, not graceful shutdown evidence.
	assertLifecycleLockLogs(t, p, token, active)
	p = startServer(t, bin, f)
	defer p.stop()
	code, after, body := fileRequest(t, c, f, "GET", "/dav/persistent", nil, nil)
	if code != 200 || string(body) != "committed bytes" || after.Get("ETag") != before.Get("ETag") || after.Get("Last-Modified") != before.Get("Last-Modified") {
		t.Fatal("restart changed committed representation")
	}
	assertStoredMetadata(t, c, f, "/dav/persistent", map[string]string{"Win32FileAttributes": "00000020"})
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/persistent", strings.NewReader("must not publish"), map[string]string{"If": "(" + active + ")"})
	if code != 412 {
		t.Fatalf("pre-restart lease token still true: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/persistent", strings.NewReader("fresh save"), nil)
	if code != 204 {
		t.Fatalf("pre-restart lock still blocks save: %d", code)
	}
	c.CloseIdleConnections()
	p.stop()
	assertLifecycleLockLogs(t, p, token, active)
}

func assertLifecycleLockLogs(t *testing.T, p *serverProcess, tokens ...string) {
	t.Helper()
	logs := p.logs.String() + p.out.String()
	for _, token := range tokens {
		if strings.Contains(logs, strings.Trim(token, "<>")) {
			t.Fatal("lock token entered process output")
		}
	}
	for _, secret := range []string{"synthetic-owner", "<d:lockinfo", "<d:owner", "<D:owner"} {
		if strings.Contains(logs, secret) {
			t.Fatal("lock owner XML entered process output")
		}
	}
}

func TestLocksLifecyclePrincipalIsolation(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	console, out, stderr, code := privateConsoleCommand(t, t.TempDir(), bin, "add", "user", "--config", f.config, "--username", "Other.User")
	if code != 0 || out != "godav: user added\n" {
		t.Fatal("second principal fixture failed")
	}
	otherPassword := accountSecret(t, console, out, stderr)
	p := startServer(t, bin, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/principal", strings.NewReader("original"), nil)
	if code != 201 {
		t.Fatalf("seed: %d", code)
	}
	token := acquireFixtureLock(t, c, f, "/dav/principal", "0", "Second-60")
	for _, tc := range []struct {
		method, body string
		headers      map[string]string
		want         int
	}{
		{"PUT", "unauthorized", nil, 423},
		{"PUT", "unauthorized", map[string]string{"If": "(" + token + ")"}, 403},
		{"LOCK", "", map[string]string{"If": "(" + token + ")", "Timeout": "Second-60"}, 403},
		{"UNLOCK", "", map[string]string{"Lock-Token": token}, 403},
	} {
		req, err := http.NewRequest(tc.method, f.origin+"/dav/principal", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal("principal request construction failed")
		}
		req.SetBasicAuth("Other.User", otherPassword)
		for name, value := range tc.headers {
			req.Header.Set(name, value)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal("principal HTTPS request failed")
		}
		_, err = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != tc.want {
			t.Fatalf("other principal %s: got %d, want %d", tc.method, res.StatusCode, tc.want)
		}
	}
	code, _, body := fileRequest(t, c, f, "GET", "/dav/principal", nil, nil)
	if code != 200 || string(body) != "original" {
		t.Fatal("other principal changed committed bytes")
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/principal", strings.NewReader("owner save"), map[string]string{"If": "(" + token + ")"})
	if code != 204 {
		t.Fatalf("original owner lost lock authority: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "UNLOCK", "/dav/principal", nil, map[string]string{"Lock-Token": token})
	if code != 204 {
		t.Fatalf("owner unlock: %d", code)
	}
	c.CloseIdleConnections()
	p.stop()
	assertLifecycleLockLogs(t, p, token)
	assertAccountServerLogs(t, p, [2]string{"FixtureUser", f.password}, [2]string{"Other.User", otherPassword})
}

func TestLocksLifecycleGrantedDuringUpload(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/staging", strings.NewReader("original"), nil)
	if code != 201 {
		t.Fatalf("seed: %d", code)
	}
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{Got100Continue: func() {
		select {
		case ready <- struct{}{}:
		default:
		}
	}})
	req, err := http.NewRequestWithContext(ctx, "PUT", f.origin+"/dav/staging", reader)
	if err != nil {
		t.Fatal("paused upload request failed")
	}
	req.Header.Set("Expect", "100-continue")
	req.SetBasicAuth("FixtureUser", f.password)
	type outcome struct {
		code int
		err  error
	}
	done := make(chan outcome, 1)
	joined := false
	go func() {
		res, err := c.Do(req)
		if err != nil {
			done <- outcome{err: err}
			return
		}
		_, err = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		done <- outcome{code: res.StatusCode, err: err}
	}()
	defer func() {
		cancel()
		writer.CloseWithError(io.ErrClosedPipe)
		reader.Close()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("paused upload did not join")
			}
		}
	}()
	select {
	case <-ready:
	case result := <-done:
		joined = true
		t.Fatalf("upload refused before staging: %d", result.code)
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not enter receiving")
	}
	if _, err := writer.Write([]byte("replacement")); err != nil {
		t.Fatal("partial upload failed")
	}
	// EOF has not arrived: granting a lock must proceed without owning the
	// upload's staging buffer and be observed again at publication time.
	token := acquireFixtureLock(t, c, f, "/dav/staging", "0", "Second-60")
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/unrelated", strings.NewReader("independent"), nil)
	if code != 201 {
		t.Fatalf("paused body and lock blocked unrelated save: %d", code)
	}
	if writer.Close() != nil {
		t.Fatal("upload EOF failed")
	}
	select {
	case result := <-done:
		joined = true
		if result.err != nil || result.code != 423 {
			t.Fatalf("upload published despite intervening lock: %d", result.code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("staged upload did not finish")
	}
	code, _, body := fileRequest(t, c, f, "GET", "/dav/staging", nil, nil)
	if code != 200 || string(body) != "original" {
		t.Fatal("intervening lock did not preserve committed bytes")
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/staging", strings.NewReader("authorized"), map[string]string{"If": "(" + token + ")"})
	if code != 204 {
		t.Fatalf("publication rejection leaked upload capacity or lock authority: %d", code)
	}
	code, _, body = fileRequest(t, c, f, "GET", "/dav/unrelated", nil, nil)
	if code != 200 || string(body) != "independent" {
		t.Fatal("unrelated save not committed")
	}
}

func TestLocksLifecyclePlaceholderPublicationFailure(t *testing.T) {
	normal := development(t)
	faulty := faultBuild(t, "godav_test_fail_file_commit")
	f := newServerFixture(t, normal)
	p := startServer(t, faulty, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	// A second failed attempt must reach publication again, not conflict with
	// a lease left behind by the first failed atomic placeholder creation.
	for attempt := 0; attempt < 2; attempt++ {
		code, h, _ := fileRequest(t, c, f, "LOCK", "/dav/placeholder", strings.NewReader(lockRequestXML), map[string]string{"Depth": "0", "Timeout": "Second-60"})
		if code != 500 || h.Get("Lock-Token") != "" {
			t.Fatalf("failed placeholder LOCK status %d, want500 without token", code)
		}
		code, _, _ = fileRequest(t, c, f, "HEAD", "/dav/placeholder", nil, nil)
		if code != 404 {
			t.Fatalf("failed placeholder was published: %d", code)
		}
	}
	// An infinite root lock would conflict with a leaked descendant lease.
	rootToken := acquireFixtureLock(t, c, f, "/dav/", "infinity", "Second-60")
	code, _, _ := fileRequest(t, c, f, "UNLOCK", "/dav/", nil, map[string]string{"Lock-Token": rootToken})
	if code != 204 {
		t.Fatalf("root lease cleanup: %d", code)
	}
	c.CloseIdleConnections()
	p.stop()
	assertLifecycleLockLogs(t, p, rootToken)
	p = startServer(t, normal, f)
	defer p.stop()
	code, _, _ = fileRequest(t, c, f, "HEAD", "/dav/placeholder", nil, nil)
	if code != 404 {
		t.Fatalf("failed placeholder survived reopen: %d", code)
	}
	code, h, _ := fileRequest(t, c, f, "LOCK", "/dav/placeholder", strings.NewReader(lockRequestXML), map[string]string{"Depth": "0", "Timeout": "Second-60"})
	if code != 201 || !strings.HasPrefix(h.Get("Lock-Token"), "<urn:uuid:") {
		t.Fatalf("ordinary restart cannot create fresh locked placeholder: %d", code)
	}
	code, _, body := fileRequest(t, c, f, "GET", "/dav/placeholder", nil, nil)
	if code != 200 || len(body) != 0 {
		t.Fatal("fresh locked placeholder is not an empty committed file")
	}
}

// Add the public conditional header to the existing receiving-upload fixture;
// all traffic still uses the fixture's real authenticated HTTPS transport.
type lifecycleConditionalTransport struct {
	base  http.RoundTripper
	token string
}

func (c lifecycleConditionalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header.Set("If", "("+c.token+")")
	return c.base.RoundTrip(copy)
}

func TestLocksLifecycleControlCDrainsConditionalUpload(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	c := fixtureClient(f)
	c.Timeout = 15 * time.Second
	defer c.CloseIdleConnections()
	var token string
	_, stdout, logs, code := privateConsoleInteraction(t, t.TempDir(), bin, func(input *os.File, logs func() string) {
		awaitLifecycle(t, logs, "msg=ready")
		status, _, _ := fileRequest(t, c, f, "PUT", "/dav/drain-locked", strings.NewReader("prior"), nil)
		if status != 201 {
			t.Fatalf("drain seed: %d", status)
		}
		token = acquireFixtureLock(t, c, f, "/dav/drain-locked", "0", "Second-60")
		conditional := *c
		conditional.Transport = lifecycleConditionalTransport{base: c.Transport, token: token}
		body, done := finishingUpload(t, &conditional, f, "/dav/drain-locked")
		if _, err := input.Write([]byte{3}); err != nil {
			t.Fatal("isolated Ctrl+C delivery failed")
		}
		awaitLifecycle(t, logs, "phase=stop-admission")
		if body.Close() != nil {
			t.Fatal("conditional upload EOF failed")
		}
		select {
		case status := <-done:
			if status != 204 {
				t.Fatalf("drained conditional upload: %d", status)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("conditional upload did not drain")
		}
	}, "run", "server", "--config", f.config)
	if code != 0 || stdout != "" || !strings.Contains(logs, "result=clean") {
		t.Fatal("locked accepted work did not drain cleanly")
	}
	if strings.Contains(logs, strings.Trim(token, "<>")) || strings.Contains(logs, "synthetic-owner") || strings.Contains(logs, "<d:lockinfo") {
		t.Fatal("lock data entered shutdown logs")
	}
	c.CloseIdleConnections()
	p := startServer(t, bin, f)
	defer p.stop()
	status, _, body := fileRequest(t, c, f, "GET", "/dav/drain-locked", nil, nil)
	if status != 200 || string(body) != "complete" {
		t.Fatal("drained conditional save did not survive restart")
	}
	status, _, _ = fileRequest(t, c, f, "PUT", "/dav/drain-locked", strings.NewReader("stale"), map[string]string{"If": "(" + token + ")"})
	if status != 412 {
		t.Fatalf("clean shutdown retained old token: %d", status)
	}
	status, _, body = fileRequest(t, c, f, "GET", "/dav/drain-locked", nil, nil)
	if status != 200 || string(body) != "complete" {
		t.Fatal("stale token changed drained bytes")
	}
	fresh := acquireFixtureLock(t, c, f, "/dav/drain-locked", "0", "Second-60")
	if fresh == token {
		t.Fatal("restart reused expired process token")
	}
	status, _, _ = fileRequest(t, c, f, "PUT", "/dav/drain-locked", strings.NewReader("fresh"), map[string]string{"If": "(" + fresh + ")"})
	if status != 204 {
		t.Fatalf("clean shutdown did not release ownership/capacity: %d", status)
	}
}
