package cli_test

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
)

const boundedPatchStart = `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32CreationTime>`
const boundedPatchEnd = `</z:Win32CreationTime><z:Win32FileAttributes>20</z:Win32FileAttributes></d:prop></d:set></d:propertyupdate>`

type metadataHTTPResult struct {
	code    int
	headers http.Header
	body    []byte
}

// HTTP 100 Continue proves admission and authentication before the test delays
// the remaining XML. Only the public request body controls the server timing.
func receivingMetadataPatch(t *testing.T, client *http.Client, f serverFixture, tag string) (*io.PipeWriter, <-chan metadataHTTPResult) {
	t.Helper()
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{Got100Continue: func() { ready <- struct{}{} }})
	req, err := http.NewRequestWithContext(ctx, "PROPPATCH", f.origin+"/dav/metadata-stop", reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Expect", "100-continue")
	req.Header.Set("If-Match", tag)
	req.SetBasicAuth("FixtureUser", f.password)
	done := make(chan metadataHTTPResult, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		response, err := client.Do(req)
		if err != nil {
			done <- metadataHTTPResult{}
			return
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			done <- metadataHTTPResult{}
			return
		}
		done <- metadataHTTPResult{response.StatusCode, response.Header, data}
	}()
	t.Cleanup(func() {
		cancel()
		writer.CloseWithError(io.ErrClosedPipe)
		reader.Close()
		select {
		case <-joined:
		case <-time.After(3 * time.Second):
			t.Error("metadata request fixture did not join")
		}
	})
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("metadata request was not admitted")
	}
	if _, err := io.WriteString(writer, boundedPatchStart); err != nil {
		t.Fatal("metadata XML prefix write failed")
	}
	return writer, done
}

func seedBoundedMetadata(t *testing.T, client *http.Client, f serverFixture) string {
	t.Helper()
	code, _, _ := fileRequest(t, client, f, "PUT", "/dav/metadata-stop", strings.NewReader("original"), nil)
	if code != 201 {
		t.Fatal("metadata lifecycle seed failed")
	}
	_, headers, _ := metadataPatch(t, client, f, "/dav/metadata-stop", strings.NewReader(boundedPatchStart+"kept"+boundedPatchEnd), nil, "Win32CreationTime", "Win32FileAttributes")
	return headers.Get("ETag")
}

func assertBoundedMetadataSnapshot(t *testing.T, client *http.Client, f serverFixture, tag, creation string) {
	t.Helper()
	code, headers, data := fileRequest(t, client, f, "GET", "/dav/metadata-stop", nil, nil)
	if code != 200 || headers.Get("ETag") != tag || string(data) != "original" {
		t.Fatal("metadata lifecycle changed content or lost the expected validator")
	}
	assertStoredMetadata(t, client, f, "/dav/metadata-stop", map[string]string{"Win32CreationTime": creation, "Win32FileAttributes": "20"})
}

func TestMetadataBoundsProcessControlCDrainsPatch(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	client := fixtureClient(f)
	client.Timeout = 12 * time.Second
	defer client.CloseIdleConnections()
	var committedTag string
	_, stdout, logs, code := privateConsoleInteraction(t, t.TempDir(), bin, func(input *os.File, logs func() string) {
		awaitLifecycle(t, logs, "msg=ready")
		tag := seedBoundedMetadata(t, client, f)
		body, done := receivingMetadataPatch(t, client, f, tag)
		if _, err := input.Write([]byte{3}); err != nil {
			t.Fatal("isolated Ctrl+C delivery failed")
		}
		awaitLifecycle(t, logs, "phase=stop-admission")
		if _, err := io.WriteString(body, "drained"+boundedPatchEnd); err != nil || body.Close() != nil {
			t.Fatal("accepted metadata body did not drain")
		}
		select {
		case result := <-done:
			assertMetadataPatchOK(t, result.code, result.body, "Win32CreationTime", "Win32FileAttributes")
			committedTag = result.headers.Get("ETag")
			if committedTag == "" || committedTag == tag {
				t.Fatal("drained metadata did not rotate the validator")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("drained metadata did not finish")
		}
	}, "run", "server", "--config", f.config)
	if code != 0 || stdout != "" || !strings.Contains(logs, "result=clean") {
		t.Fatal("metadata drain did not stop cleanly")
	}
	p := startServer(t, bin, f)
	defer p.stop()
	assertBoundedMetadataSnapshot(t, client, f, committedTag, "drained")
}

func metadataShutdownGrace(t *testing.T, f serverFixture) {
	t.Helper()
	config, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal("metadata fixture settings read failed")
	}
	config = append(config, []byte("  shutdown_grace_ms: 100\n")...)
	if err := os.WriteFile(f.config, config, 0600); err != nil {
		t.Fatal("metadata fixture grace setup failed")
	}
}

func finishRejectedMetadata(t *testing.T, done <-chan metadataHTTPResult) {
	t.Helper()
	select {
	case result := <-done:
		if result.code == 0 {
			return // The forced close can prevent a complete response.
		}
		if result.headers.Get("GoDav-Rejection") != "" {
			t.Fatal("owned metadata cancellation incorrectly authorizes retry")
		}
		if result.code == 503 {
			return
		}
		var doc metadataReply
		if result.code != 207 || xml.Unmarshal(result.body, &doc) != nil || len(doc.Responses) != 1 || len(doc.Responses[0].Stats) != 2 {
			t.Fatalf("cancelled metadata response invalid: %d", result.code)
		}
		for i, stat := range doc.Responses[0].Stats {
			status := "HTTP/1.1 424 Failed Dependency"
			if i == 0 {
				status = "HTTP/1.1 500 Internal Server Error"
			}
			if stat.Status != status {
				t.Fatal("cancelled metadata advertised a property success")
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled metadata request did not join")
	}
}

func TestMetadataBoundsProcessCancelsOwnedPatch(t *testing.T) {
	bin := development(t)
	paused := faultBuild(t, "godav_test_pause_metadata_commit")
	f := newServerFixture(t, bin)
	metadataShutdownGrace(t, f)
	client := fixtureClient(f)
	client.Timeout = 12 * time.Second
	defer client.CloseIdleConnections()
	p := startServer(t, bin, f)
	tag := seedBoundedMetadata(t, client, f)
	p.stop()
	client.CloseIdleConnections()
	_, stdout, logs, code := privateConsoleInteraction(t, t.TempDir(), paused, func(input *os.File, logs func() string) {
		awaitLifecycle(t, logs, "msg=ready")
		body, done := receivingMetadataPatch(t, client, f, tag)
		end := strings.Replace(boundedPatchEnd, ">20<", ">40<", 1)
		if _, err := io.WriteString(body, "must-rollback"+end); err != nil || body.Close() != nil {
			t.Fatal("metadata cancellation fixture could not finish its body")
		}
		awaitLifecycle(t, logs, "test-only metadata publication pause")
		if _, err := input.Write([]byte{3}); err != nil {
			t.Fatal("isolated Ctrl+C delivery failed")
		}
		finishRejectedMetadata(t, done)
	}, "run", "server", "--config", f.config)
	if code != 1 || stdout != "" || !strings.Contains(logs, "result=cancelled") || !strings.Contains(logs, "shutdown deadline expired") || strings.Contains(logs, "must-rollback") {
		t.Fatal("owned metadata cancellation did not report a safe deadline failure")
	}
	p = startServer(t, bin, f)
	defer p.stop()
	assertBoundedMetadataSnapshot(t, client, f, tag, "kept")
	_, headers, _ := metadataPatch(t, client, f, "/dav/metadata-stop", strings.NewReader(boundedPatchStart+"recovered"+boundedPatchEnd), map[string]string{"If-Match": tag}, "Win32CreationTime", "Win32FileAttributes")
	if headers.Get("ETag") == tag {
		t.Fatal("metadata did not recover after cancellation and restart")
	}
}

func TestMetadataBoundsInProcessStop(t *testing.T) {
	bin := development(t)
	for _, deadline := range []bool{false, true} {
		name := "drain"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			f := newServerFixture(t, bin)
			if deadline {
				metadataShutdownGrace(t, f)
			}
			client := fixtureClient(f)
			client.Timeout = 12 * time.Second
			defer client.CloseIdleConnections()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			logs := &safeCapture{}
			go func() { done <- server.Run(ctx, f.config, server.Identity{}, logs) }()
			joined := false
			t.Cleanup(func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(12 * time.Second):
						t.Error("metadata server fixture did not join")
					}
				}
			})
			awaitLifecycle(t, logs.String, "msg=ready")
			tag := seedBoundedMetadata(t, client, f)
			body, requestDone := receivingMetadataPatch(t, client, f, tag)
			started := time.Now()
			cancel()
			awaitLifecycle(t, logs.String, "phase=stop-admission")
			creation := "kept"
			if deadline {
				awaitLifecycle(t, logs.String, "phase=cancelling")
				body.CloseWithError(io.ErrClosedPipe)
				select {
				case result := <-requestDone:
					if result.code != 0 && result.code != 400 && result.code != 503 {
						t.Fatalf("incomplete cancelled XML returned %d", result.code)
					}
					if result.headers.Get("GoDav-Rejection") != "" {
						t.Fatal("cancelled metadata body incorrectly authorizes retry")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("cancelled metadata body did not join")
				}
			} else {
				creation = "drained"
				if _, err := io.WriteString(body, creation+boundedPatchEnd); err != nil || body.Close() != nil {
					t.Fatal("in-process metadata body did not drain")
				}
				select {
				case result := <-requestDone:
					assertMetadataPatchOK(t, result.code, result.body, "Win32CreationTime", "Win32FileAttributes")
					next := result.headers.Get("ETag")
					if next == "" || next == tag {
						t.Fatal("in-process drain did not commit a new validator")
					}
					tag = next
				case <-time.After(3 * time.Second):
					t.Fatal("in-process metadata response did not finish")
				}
			}
			select {
			case err := <-done:
				joined = true
				if deadline {
					if !errors.Is(err, server.ErrShutdownDeadline) || !strings.Contains(logs.String(), "result=cancelled") || time.Since(started) > 3*time.Second {
						t.Fatal("in-process metadata cancellation missed its bounded deadline outcome")
					}
				} else if err != nil || !strings.Contains(logs.String(), "result=clean") {
					t.Fatal("in-process metadata drain was not clean")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("in-process metadata stop did not join")
			}
			p := startServer(t, bin, f)
			defer p.stop()
			assertBoundedMetadataSnapshot(t, client, f, tag, creation)
		})
	}
}
