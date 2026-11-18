package cli_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
)

func startHTTPTest(t *testing.T) (string, *http.Client, func()) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	logs := &safeCapture{}
	done := make(chan error, 1)
	go func() { done <- server.RunHTTPTest(ctx, port, server.Identity{}, logs) }()
	origin := "http://127.0.0.1:" + strconv.Itoa(port)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("test server stop: %v; %s", err, logs.String())
			}
		case <-time.After(10 * time.Second):
			t.Error("test server did not stop")
		}
		client.CloseIdleConnections()
	}
	t.Cleanup(stop)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), "msg=ready") {
			return origin, client, stop
		}
		select {
		case err := <-done:
			stopped = true
			cancel()
			t.Fatalf("test server startup: %v", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("test server not ready")
	return "", nil, nil
}

func httpTestRequest(t *testing.T, client *http.Client, origin, method, path, body string, headers map[string]string, want int) (string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, origin+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, res.StatusCode, want, data)
	}
	return string(data), res.Header
}

func TestHTTPTestAnonymousWorkflowAndIsolation(t *testing.T) {
	origin, client, stop := startHTTPTest(t)
	httpTestRequest(t, client, origin, "OPTIONS", "/dav/", "", nil, 200)
	httpTestRequest(t, client, origin, "PROPFIND", "/dav/", "", map[string]string{"Depth": "1"}, 207)
	httpTestRequest(t, client, origin, "GET", "/dav/", "", nil, 405)
	httpTestRequest(t, client, origin, "MKCOL", "/dav/folder", "", nil, 201)
	httpTestRequest(t, client, origin, "PUT", "/dav/folder/test%20file.txt", "first", nil, 201)
	httpTestRequest(t, client, origin, "PUT", "/dav/folder/test%20file.txt", "saved", nil, 204)
	got, h := httpTestRequest(t, client, origin, "GET", "/dav/folder/test%20file.txt", "", nil, 200)
	if got != "saved" || h.Get("ETag") == "" {
		t.Fatal("saved content/etag missing")
	}
	httpTestRequest(t, client, origin, "PUT", "/dav/folder/test%20file.txt", "bad", map[string]string{"If-Match": "\"not-current\""}, 412)
	httpTestRequest(t, client, origin, "MOVE", "/dav/folder/test%20file.txt", "", map[string]string{"Destination": origin + "/dav/moved.txt", "Overwrite": "F", "Depth": "0"}, 201)
	got, _ = httpTestRequest(t, client, origin, "GET", "/dav/moved.txt", "", nil, 200)
	if got != "saved" {
		t.Fatal("move changed bytes")
	}
	httpTestRequest(t, client, origin, "GET", "/dav/folder/test%20file.txt", "", nil, 404)
	httpTestRequest(t, client, origin, "PUT", "/dav/collision.txt", "keep", nil, 201)
	httpTestRequest(t, client, origin, "MOVE", "/dav/moved.txt", "", map[string]string{"Destination": origin + "/dav/collision.txt", "Overwrite": "F"}, 412)
	httpTestRequest(t, client, origin, "DELETE", "/dav/folder", "", nil, 204)
	stop()
	origin2, client2, _ := startHTTPTest(t)
	httpTestRequest(t, client2, origin2, "GET", "/dav/moved.txt", "", nil, 404)
	httpTestRequest(t, client2, origin2, "GET", "/dav/collision.txt", "", nil, 404)
}

func TestHTTPTestRejectsCredentialsForeignAuthoritiesAndOrigins(t *testing.T) {
	origin, client, _ := startHTTPTest(t)
	for _, tc := range []struct {
		name    string
		headers map[string]string
		status  int
	}{
		{"credentials", map[string]string{"Authorization": "Basic Zm9vOmJhcg=="}, 400},
		{"proxy credentials", map[string]string{"Proxy-Authorization": "Basic Zm9vOmJhcg=="}, 400},
		{"cookies", map[string]string{"Cookie": "session=dummy"}, 400},
		{"foreign host", map[string]string{"Host": "example.test:8080"}, 400},
		{"host alias", map[string]string{"Host": "localhost:" + strings.Split(origin, ":")[2]}, 400},
		{"foreign origin", map[string]string{"Origin": "https://example.test"}, 400},
		{"null origin", map[string]string{"Origin": "null"}, 400},
		{"foreign fetch", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpTestRequest(t, client, origin, "PUT", "/dav/forbidden.txt", "must not save", tc.headers, tc.status)
		})
	}
	httpTestRequest(t, client, origin, "GET", "/dav/forbidden.txt", "", nil, 404)
	httpTestRequest(t, client, origin, "OPTIONS", "/dav/", "", map[string]string{"Origin": origin}, 200)
	httpTestRequest(t, client, origin, "PUT", "/dav/source.txt", "content", nil, 201)
	httpTestRequest(t, client, origin, "MOVE", "/dav/source.txt", "", map[string]string{"Destination": "http://example.test/dav/dest.txt"}, 502)
	httpTestRequest(t, client, origin, "MOVE", "/dav/source.txt", "", map[string]string{"Destination": strings.Replace(origin, "http:", "https:", 1) + "/dav/dest.txt"}, 502)
	httpTestRequest(t, client, origin, "GET", "/dav/%2e%2e/nope", "", nil, 400)
}

func TestHTTPTestLockTaggedConditionsAndMetadata(t *testing.T) {
	origin, client, _ := startHTTPTest(t)
	httpTestRequest(t, client, origin, "PUT", "/dav/locked.txt", "before", nil, 201)
	lockBody := "<?xml version=\"1.0\"?><d:lockinfo xmlns:d=\"DAV:\"><d:lockscope><d:exclusive/></d:lockscope><d:locktype><d:write/></d:locktype><d:owner>test</d:owner></d:lockinfo>"
	_, h := httpTestRequest(t, client, origin, "LOCK", "/dav/locked.txt", lockBody, map[string]string{"Depth": "0", "Timeout": "Second-60", "Content-Type": "application/xml"}, 200)
	token := h.Get("Lock-Token")
	if token == "" {
		t.Fatal("no lock token")
	}
	httpTestRequest(t, client, origin, "PUT", "/dav/locked.txt", "blocked", nil, 423)
	httpTestRequest(t, client, origin, "PUT", "/dav/locked.txt", "after", map[string]string{"If": "<" + origin + "/dav/locked.txt> (" + token + ")"}, 204)
	httpTestRequest(t, client, origin, "UNLOCK", "/dav/locked.txt", "", map[string]string{"Lock-Token": token}, 204)
	body := "<d:propertyupdate xmlns:d=\"DAV:\" xmlns:t=\"urn:schemas-microsoft-com:\"><d:set><d:prop><t:Win32CreationTime>blue</t:Win32CreationTime></d:prop></d:set></d:propertyupdate>"
	httpTestRequest(t, client, origin, "PROPPATCH", "/dav/locked.txt", body, map[string]string{"Content-Type": "application/xml"}, 207)
	got, _ := httpTestRequest(t, client, origin, "PROPFIND", "/dav/locked.txt", "", map[string]string{"Depth": "0"}, 207)
	if !strings.Contains(got, "blue") {
		t.Fatal("metadata not retained")
	}
	httpTestRequest(t, client, origin, "DELETE", "/dav/locked.txt", "", nil, 204)
}

// Separate client pools reproduce native WebClient's idle-connection behavior.
// Responses must release slots rather than starve later sequential operations.
func TestHTTPTestSequentialClientPoolsDoNotExhaustConnections(t *testing.T) {
	origin, _, _ := startHTTPTest(t)
	for range 40 {
		c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
		defer c.CloseIdleConnections()
		req, _ := http.NewRequest("OPTIONS", origin+"/dav/", nil)
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !res.Close {
			t.Fatalf("test mode should close bounded idle connections: %d close=%v", res.StatusCode, res.Close)
		}
	}
}

func TestHTTPTestCleanupOnStopAndBindFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	_, _, stop := startHTTPTest(t)
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal("expected one isolated temporary state directory")
	}
	if _, err := os.Stat(filepath.Join(root, entries[0].Name(), "disposable.sqlite")); err != nil {
		t.Fatal(err)
	}
	stop()
	entries, err = os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("clean stop did not remove disposable state")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := server.RunHTTPTest(context.Background(), port, server.Identity{}, io.Discard); err == nil {
		t.Fatal("occupied port accepted")
	}
	entries, err = os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed startup leaked temporary state")
	}
}

func TestHTTPTestInvalidArgumentsDoNotCreateState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	for _, port := range []int{-1, 0, 65536} {
		if err := server.RunHTTPTest(context.Background(), port, server.Identity{}, io.Discard); err == nil {
			t.Fatal("invalid port accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := server.RunHTTPTest(ctx, 8080, server.Identity{}, io.Discard); err == nil {
		t.Fatal("cancelled startup accepted")
	}
	t.Setenv("GODEBUG", "httpmuxgo121=1")
	if err := server.RunHTTPTest(context.Background(), 8080, server.Identity{}, io.Discard); err == nil {
		t.Fatal("legacy routing accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("invalid startup created state")
	}
}

func TestHTTPTestRejectsOversizedAndIncompleteUploads(t *testing.T) {
	origin, client, _ := startHTTPTest(t)
	httpTestRequest(t, client, origin, "PUT", "/dav/kept.txt", "original", nil, 201)
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(origin, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	// An incomplete overwrite must not publish partial data.
	fmt.Fprintf(conn, "PUT /dav/kept.txt HTTP/1.1\r\nHost: %s\r\nContent-Length: 50\r\n\r\npartial", strings.TrimPrefix(origin, "http://"))
	conn.Close()
	got, _ := httpTestRequest(t, client, origin, "GET", "/dav/kept.txt", "", nil, 200)
	if got != "original" {
		t.Fatal("incomplete upload replaced committed data")
	}
	conn, err = net.DialTimeout("tcp", strings.TrimPrefix(origin, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(conn, "PUT /dav/too-big HTTP/1.1\r\nHost: %s\r\nContent-Length: 33554433\r\nExpect: 100-continue\r\n\r\n", strings.TrimPrefix(origin, "http://"))
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 413 {
		t.Fatalf("oversized upload: %d", res.StatusCode)
	}
	httpTestRequest(t, client, origin, "GET", "/dav/too-big", "", nil, 404)
}
