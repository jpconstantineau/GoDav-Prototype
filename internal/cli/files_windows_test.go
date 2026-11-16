package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
)

func fileRequest(t *testing.T, client *http.Client, f serverFixture, method, path string, body io.Reader, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, f.origin+path, body)
	if err != nil {
		t.Fatal("file fixture request invalid")
	}
	req.SetBasicAuth("FixtureUser", f.password)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("file fixture HTTPS request failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal("file fixture response incomplete")
	}
	return response.StatusCode, response.Header, data
}

func liveFiles(t *testing.T, maxBytes int) (serverFixture, *http.Client, func()) {
	t.Helper()
	bin := development(t)
	f := newServerFixture(t, bin)
	config, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	config = bytes.ReplaceAll(config, []byte("33554432"), []byte(fmt.Sprint(maxBytes)))
	if os.WriteFile(f.config, config, 0600) != nil {
		t.Fatal("fixture config failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	logs := &safeCapture{}
	go func() { done <- server.Run(ctx, f.config, server.Identity{}, logs) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error("live fixture shutdown failed")
				}
			case <-time.After(12 * time.Second):
				t.Error("live fixture did not stop")
			}
		})
	}
	t.Cleanup(stop)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "msg=ready") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(logs.String(), "msg=ready") {
		t.Fatal("live fixture readiness failed")
	}
	client := fixtureClient(f)
	client.Timeout = 15 * time.Second
	t.Cleanup(client.CloseIdleConnections)
	return f, client, stop
}

func pausedFileUpload(t *testing.T, client *http.Client, f serverFixture, path string) func() {
	t.Helper()
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{Got100Continue: func() { ready <- struct{}{} }})
	req, _ := http.NewRequestWithContext(ctx, "PUT", f.origin+path, reader)
	req.Header.Set("Expect", "100-continue")
	req.SetBasicAuth("FixtureUser", f.password)
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			writer.CloseWithError(io.ErrClosedPipe)
			reader.Close()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("paused fixture request did not stop")
			}
		})
	}
	t.Cleanup(stop)
	select {
	case <-ready:
	case <-done:
		t.Fatal("upload failed before receiving body")
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not accept body")
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal("paused fixture write failed")
	}
	return stop
}

func TestFilesInProcessPayloadBudgetAndUnrelatedProgress(t *testing.T) {
	f, client, _ := liveFiles(t, 64<<20)
	code, _, _ := fileRequest(t, client, f, "PUT", "/dav/old", bytes.NewReader([]byte("old")), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	first := pausedFileUpload(t, client, f, "/dav/one")
	start := time.Now()
	code, _, _ = fileRequest(t, client, f, "PUT", "/dav/small", bytes.NewReader([]byte("ok")), nil)
	if code != 201 || time.Since(start) > 2*time.Second {
		t.Fatal("slow body blocked unrelated save")
	}
	second := pausedFileUpload(t, client, f, "/dav/two")
	code, _, _ = fileRequest(t, client, f, "GET", "/dav/old", nil, nil)
	if code != 503 {
		t.Fatalf("read bypassed combined payload budget: %d", code)
	}
	code, _, _ = fileRequest(t, client, f, "PUT", "/dav/excess", bytes.NewReader([]byte("new")), nil)
	if code != 503 {
		t.Fatal("upload bypassed combined payload budget")
	}
	code, h, data := fileRequest(t, client, f, "HEAD", "/dav/old", nil, nil)
	if code != 200 || len(data) != 0 || h.Get("Content-Length") != "3" {
		t.Fatal("metadata unnecessarily reserved payload")
	}
	first()
	second()
	deadline := time.Now().Add(3 * time.Second)
	for {
		code, _, _ = fileRequest(t, client, f, "PUT", "/dav/after-cancel", bytes.NewReader([]byte("ok")), nil)
		if code == 201 {
			break
		}
		if code != 503 || time.Now().After(deadline) {
			t.Fatal("cancellation leaked payload/admission capacity")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, path := range []string{"/dav/one", "/dav/two", "/dav/excess"} {
		code, _, _ := fileRequest(t, client, f, "GET", path, nil, nil)
		if code != 404 {
			t.Fatal("incomplete upload became visible")
		}
	}
}

func TestFilesConfiguredAbsoluteLimit(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	config, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	config = bytes.ReplaceAll(config, []byte("33554432"), []byte("67108865"))
	if os.WriteFile(f.config, config, 0600) != nil {
		t.Fatal("fixture config failed")
	}
	namespaceServerRefusal(t, bin, f)
}

func TestFilesActualSizeBoundaries(t *testing.T) {
	bin := development(t)
	for _, capBytes := range []int{7, 33554432, 67108864} {
		t.Run(fmt.Sprint(capBytes), func(t *testing.T) {
			f := newServerFixture(t, bin)
			config, err := os.ReadFile(f.config)
			if err != nil {
				t.Fatal(err)
			}
			config = bytes.ReplaceAll(config, []byte("33554432"), []byte(fmt.Sprint(capBytes)))
			if os.WriteFile(f.config, config, 0600) != nil {
				t.Fatal("fixture config failed")
			}
			p := startServer(t, bin, f)
			defer p.stop()
			client := fixtureClient(f)
			client.Timeout = 10 * time.Second
			defer client.CloseIdleConnections()
			payload := bytes.Repeat([]byte{0xA5}, capBytes+1)
			for _, unknown := range []bool{false, true} {
				for _, size := range []int{capBytes - 1, capBytes, capBytes + 1} {
					for _, existing := range []bool{false, true} {
						path := fmt.Sprintf("/dav/bound-%t-%d-%t", unknown, size, existing)
						oldTag := ""
						if existing {
							code, h, _ := fileRequest(t, client, f, "PUT", path, bytes.NewReader([]byte("old")), nil)
							if code != 201 {
								t.Fatal("seed failed")
							}
							oldTag = h.Get("ETag")
						}
						var body io.Reader = bytes.NewReader(payload[:size])
						if unknown {
							body = io.LimitReader(body, int64(size))
						}
						code, _, _ := fileRequest(t, client, f, "PUT", path, body, nil)
						if size > capBytes {
							if code != 413 {
								t.Fatalf("over-limit %t %t status %d", unknown, existing, code)
							}
							got, h, data := fileRequest(t, client, f, "GET", path, nil, nil)
							if existing {
								if got != 200 || string(data) != "old" || h.Get("ETag") != oldTag {
									t.Fatal("failed replacement changed committed state")
								}
							} else if got != 404 {
								t.Fatal("failed creation became visible")
							}
						} else {
							want := 201
							if existing {
								want = 204
							}
							if code != want {
								t.Fatalf("within-limit status %d", code)
							}
							got, h, data := fileRequest(t, client, f, "GET", path, nil, nil)
							if got != 200 || !bytes.Equal(data, payload[:size]) || h.Get("Content-Length") != fmt.Sprint(size) {
								t.Fatal("boundary readback differs")
							}
						}
					}
				}
			}
			code, _, _ := fileRequest(t, client, f, "PUT", "/dav/after-bounds", bytes.NewReader([]byte("ok")), nil)
			if code != 201 {
				t.Fatal("capacity leaked after bound checks")
			}
		})
	}
}

func TestFilesReplaceBytesAndListMetadata(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	namespaceStatus(t, client, f, "MKCOL", "/dav/Parent/", nil, 201)
	fixtures := [][]byte{[]byte("alpha"), {0, 255, 128, 0, 12}, []byte("bravo")}
	var previous string
	for i, data := range fixtures {
		want := 204
		if i == 0 {
			want = 201
		}
		code, h, _ := fileRequest(t, client, f, "PUT", "/dav/parent/Readme.bin", bytes.NewReader(data), map[string]string{"Content-Type": "application/x-fixture"})
		if code != want || h.Get("ETag") == previous {
			t.Fatal("complete replacement did not get fresh validator/status")
		}
		previous = h.Get("ETag")
		code, h, got := fileRequest(t, client, f, "GET", "/dav/PARENT/README.BIN", nil, nil)
		if code != 200 || !bytes.Equal(got, data) || h.Get("Content-Length") != "5" || h.Get("Content-Type") != "application/x-fixture" || h.Get("ETag") != previous {
			t.Fatal("replacement bytes or metadata differ")
		}
		_, _, payload := fileRequest(t, client, f, "PROPFIND", "/dav/Parent/", nil, map[string]string{"Depth": "1"})
		var listing struct {
			Responses []struct {
				Href  string `xml:"DAV: href"`
				Props []struct {
					Prop struct {
						Length string `xml:"DAV: getcontentlength"`
						Type   string `xml:"DAV: getcontenttype"`
						ETag   string `xml:"DAV: getetag"`
					} `xml:"DAV: prop"`
				} `xml:"DAV: propstat"`
			} `xml:"DAV: response"`
		}
		if xml.Unmarshal(payload, &listing) != nil {
			t.Fatal("file listing XML invalid")
		}
		found := false
		for _, entry := range listing.Responses {
			if entry.Href == "/dav/Parent/Readme.bin" {
				for _, props := range entry.Props {
					if props.Prop.Length == "5" && props.Prop.Type == "application/x-fixture" && props.Prop.ETag == previous {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatal("file metadata absent or inconsistent in collection listing")
		}
	}
	if got := namespaceListing(t, client, f, "/dav/Parent/", "1"); !slices.Equal(got, []string{"/dav/Parent/", "/dav/Parent/Readme.bin"}) {
		t.Fatal("replacement changed display spelling or made duplicate")
	}
	p.stop()
	client.CloseIdleConnections()
	p = startServer(t, bin, f)
	defer p.stop()
	code, h, data := fileRequest(t, client, f, "GET", "/dav/parent/readme.bin", nil, nil)
	if code != 200 || string(data) != "bravo" || h.Get("ETag") != previous {
		t.Fatal("replacement did not survive reopen")
	}
}

func TestFilesEmptySaveReadAndReopen(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	code, headers, _ := fileRequest(t, client, f, "PUT", "/dav/Empty.txt", bytes.NewReader(nil), nil)
	if code != 201 {
		t.Fatalf("empty PUT status %d, want 201", code)
	}
	etag := headers.Get("ETag")
	if len(etag) < 3 || etag[0] != '"' || etag[len(etag)-1] != '"' {
		t.Fatal("empty PUT has no strong validator")
	}
	for _, method := range []string{"GET", "HEAD"} {
		code, h, data := fileRequest(t, client, f, method, "/dav/empty.TXT", nil, nil)
		if code != 200 || len(data) != 0 || h.Get("Content-Length") != "0" || h.Get("ETag") != etag || h.Get("Content-Type") != "application/octet-stream" {
			t.Fatal("empty file metadata/readback differs")
		}
	}
	p.stop()
	client.CloseIdleConnections()
	p = startServer(t, bin, f)
	defer p.stop()
	code, h, data := fileRequest(t, client, f, "GET", "/dav/Empty.txt", nil, nil)
	if code != 200 || len(data) != 0 || h.Get("ETag") != etag {
		t.Fatal("committed empty file did not survive reopen")
	}
}

func TestFilesRangesConditionsAndUnsupportedWrites(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	code, h, _ := fileRequest(t, client, f, "PUT", "/dav/data", bytes.NewReader([]byte("0123456789")), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	etag := h.Get("ETag")
	for _, tc := range []struct {
		method, rangeValue, ifRange, body, length, contentRange string
		status                                                  int
	}{
		{"GET", "bytes=2-4", "", "234", "3", "bytes 2-4/10", 206},
		{"GET", "bytes=7-", "", "789", "3", "bytes 7-9/10", 206},
		{"GET", "bytes=-2", "", "89", "2", "bytes 8-9/10", 206},
		{"GET", "bytes=2-4", etag, "234", "3", "bytes 2-4/10", 206},
		{"GET", "bytes=2-4", `"other"`, "0123456789", "10", "", 200},
		{"GET", "bytes=2-4", "W/" + etag, "0123456789", "10", "", 200},
		{"GET", "bytes=0-0,2-2", "", "0123456789", "10", "", 200},
		{"GET", "items=0-0", "", "0123456789", "10", "", 200},
		{"HEAD", "bytes=2-4", "", "", "10", "", 200},
	} {
		code, h, data := fileRequest(t, client, f, tc.method, "/dav/data", nil, map[string]string{"Range": tc.rangeValue, "If-Range": tc.ifRange})
		if code != tc.status || string(data) != tc.body || h.Get("Content-Length") != tc.length || h.Get("Content-Range") != tc.contentRange {
			t.Fatalf("range %s %s returned %d length %s", tc.method, tc.rangeValue, code, h.Get("Content-Length"))
		}
	}
	for _, rangeValue := range []string{"bytes=99-100", "bytes=garbage", "bytes=-0", "bytes=-000", "bytes="} {
		code, _, _ := fileRequest(t, client, f, "GET", "/dav/data", nil, map[string]string{"Range": rangeValue})
		if code != 416 {
			t.Fatalf("invalid single range %q returned %d", rangeValue, code)
		}
	}
	for _, method := range []string{"GET", "HEAD"} {
		for _, tc := range []struct {
			header, value string
			status        int
		}{{"If-None-Match", etag, 304}, {"If-None-Match", "W/" + etag, 304}, {"If-Match", `"other"`, 412}, {"If-Match", "W/" + etag, 412}, {"If-Match", etag, 200}} {
			code, _, data := fileRequest(t, client, f, method, "/dav/data", nil, map[string]string{tc.header: tc.value})
			if code != tc.status || (method == "HEAD" && len(data) != 0) {
				t.Fatal("conditional read outcome differs")
			}
		}
	}
	for _, header := range []string{"If", "Lock-Token"} {
		code, _, _ := fileRequest(t, client, f, "PUT", "/dav/data", bytes.NewReader([]byte("changed")), map[string]string{header: "unsupported"})
		if code != 400 {
			t.Fatalf("invalid %s was accepted: %d", header, code)
		}
	}
	for _, tc := range []struct {
		header, value string
		status        int
	}{{"Content-Range", "bytes 0-1/10", 400}, {"Content-Encoding", "gzip", 415}, {"Content-Type", "not a media type", 400}} {
		code, _, _ := fileRequest(t, client, f, "PUT", "/dav/data", bytes.NewReader([]byte("changed")), map[string]string{tc.header: tc.value})
		if code != tc.status {
			t.Fatal("unsupported representation accepted")
		}
	}
	code, h, data := fileRequest(t, client, f, "GET", "/dav/data", nil, nil)
	if code != 200 || string(data) != "0123456789" || h.Get("ETag") != etag {
		t.Fatal("rejected writes changed state")
	}
	code, _, _ = fileRequest(t, client, f, "MKCOL", "/dav/conditional/", nil, map[string]string{"If": "unsupported"})
	if code != 400 {
		t.Fatal("collection ignored write condition")
	}
}

func TestFilesTaggedFailuresRetainCommittedSnapshot(t *testing.T) {
	bin := development(t)
	for _, tag := range []string{"godav_test_fail_file_stage", "godav_test_fail_file_commit"} {
		t.Run(tag, func(t *testing.T) {
			faulty := faultBuild(t, tag)
			f := newServerFixture(t, bin)
			p := startServer(t, bin, f)
			client := fixtureClient(f)
			defer client.CloseIdleConnections()
			for path, body := range map[string]string{"/dav/old": "original", "/dav/empty": ""} {
				code, _, _ := fileRequest(t, client, f, "PUT", path, strings.NewReader(body), nil)
				if code != 201 {
					t.Fatal("fault seed failed")
				}
			}
			_, before, _ := fileRequest(t, client, f, "HEAD", "/dav/old", nil, nil)
			_, _, listing := fileRequest(t, client, f, "PROPFIND", "/dav/", nil, map[string]string{"Depth": "1"})
			p.stop()
			client.CloseIdleConnections()
			p = startServer(t, faulty, f)
			for _, path := range []string{"/dav/old", "/dav/empty", "/dav/new", "/dav/new", "/dav/new"} {
				code, _, _ := fileRequest(t, client, f, "PUT", path, io.LimitReader(strings.NewReader("replacement"), 11), nil)
				if code != 500 {
					t.Fatalf("tagged failure status %d, want 500", code)
				}
			}
			_, after, _ := fileRequest(t, client, f, "HEAD", "/dav/old", nil, nil)
			if before.Get("ETag") != after.Get("ETag") || before.Get("Last-Modified") != after.Get("Last-Modified") || before.Get("Content-Length") != after.Get("Content-Length") {
				t.Fatal("failed publication changed metadata")
			}
			_, _, gotListing := fileRequest(t, client, f, "PROPFIND", "/dav/", nil, map[string]string{"Depth": "1"})
			if !maps.Equal(fileListingMetadata(t, listing), fileListingMetadata(t, gotListing)) {
				t.Fatal("failed publication changed namespace metadata")
			}
			p.stop()
			client.CloseIdleConnections()
			p = startServer(t, bin, f)
			defer p.stop()
			for path, want := range map[string]string{"/dav/old": "original", "/dav/empty": ""} {
				code, _, body := fileRequest(t, client, f, "GET", path, nil, nil)
				if code != 200 || string(body) != want {
					t.Fatal("failed save lost prior bytes on reopen")
				}
			}
			code, _, _ := fileRequest(t, client, f, "GET", "/dav/new", nil, nil)
			if code != 404 {
				t.Fatal("failed creation persisted")
			}
			code, _, _ = fileRequest(t, client, f, "PUT", "/dav/recovered", strings.NewReader("ok"), nil)
			if code != 201 {
				t.Fatal("failure left store unusable")
			}
		})
	}
}

func fileListingMetadata(t *testing.T, payload []byte) map[string][6]string {
	t.Helper()
	var listing struct {
		Responses []struct {
			Href      string `xml:"DAV: href"`
			Propstats []struct {
				Status string `xml:"DAV: status"`
				Prop   struct {
					Name     string `xml:"DAV: displayname"`
					Size     string `xml:"DAV: getcontentlength"`
					Type     string `xml:"DAV: getcontenttype"`
					ETag     string `xml:"DAV: getetag"`
					Modified string `xml:"DAV: getlastmodified"`
					Resource struct {
						Collection *struct{} `xml:"DAV: collection"`
					} `xml:"DAV: resourcetype"`
				} `xml:"DAV: prop"`
			} `xml:"DAV: propstat"`
		} `xml:"DAV: response"`
	}
	if xml.Unmarshal(payload, &listing) != nil || len(listing.Responses) == 0 {
		t.Fatal("fixture listing invalid")
	}
	result := map[string][6]string{}
	for _, response := range listing.Responses {
		for _, props := range response.Propstats {
			if props.Status == "HTTP/1.1 200 OK" {
				p := props.Prop
				result[response.Href] = [6]string{p.Name, p.Size, p.Type, p.ETag, p.Modified, fmt.Sprint(p.Resource.Collection != nil)}
			}
		}
	}
	return result
}

func TestFilesMetadataDoesNotLoadPayload(t *testing.T) {
	bin := development(t)
	faulty := faultBuild(t, "godav_test_fail_file_read")
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	code, headers, _ := fileRequest(t, client, f, "PUT", "/dav/content", strings.NewReader("known fixture"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	p.stop()
	client.CloseIdleConnections()
	p = startServer(t, faulty, f)
	defer p.stop()
	code, h, body := fileRequest(t, client, f, "HEAD", "/dav/content", nil, nil)
	if code != 200 || len(body) != 0 || h.Get("Content-Length") != "13" || h.Get("ETag") != headers.Get("ETag") {
		t.Fatal("HEAD required content load")
	}
	code, _, body = fileRequest(t, client, f, "PROPFIND", "/dav/", nil, map[string]string{"Depth": "1"})
	if code != 207 || fileListingMetadata(t, body)["/dav/content"][1] != "13" {
		t.Fatal("PROPFIND required content load or lost length")
	}
	code, _, _ = fileRequest(t, client, f, "GET", "/dav/content", nil, nil)
	if code != 500 {
		t.Fatal("payload failure fixture did not exercise GET")
	}
	code, _, body = fileRequest(t, client, f, "GET", "/dav/content", nil, map[string]string{"If-None-Match": headers.Get("ETag")})
	if code != 304 || len(body) != 0 {
		t.Fatal("conditional metadata response required payload load")
	}
}

func TestFilesWriterAcquisitionHasDeadline(t *testing.T) {
	bin := development(t)
	paused := faultBuild(t, "godav_test_pause_file_commit")
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	client.Timeout = 12 * time.Second
	defer client.CloseIdleConnections()
	code, _, _ := fileRequest(t, client, f, "PUT", "/dav/old", strings.NewReader("old"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	p.stop()
	client.CloseIdleConnections()
	p = startServer(t, paused, f)
	defer p.stop()
	done := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest("PUT", f.origin+"/dav/old", strings.NewReader("new"))
		req.SetBasicAuth("FixtureUser", f.password)
		response, err := client.Do(req)
		if err != nil {
			done <- 0
			return
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		done <- response.StatusCode
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(p.logs.String(), "test-only file publication pause") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(p.logs.String(), "test-only file publication pause") {
		t.Fatal("publication pause fixture not reached")
	}
	start := time.Now()
	code, _, _ = fileRequest(t, client, f, "HEAD", "/dav/old", nil, nil)
	elapsed := time.Since(start)
	if code != 503 || elapsed < 1500*time.Millisecond || elapsed > 3500*time.Millisecond {
		t.Fatalf("store acquisition was not bounded: %d after %s", code, elapsed)
	}
	select {
	case code := <-done:
		if code != 500 {
			t.Fatal("paused publication did not fail")
		}
	case <-time.After(7 * time.Second):
		t.Fatal("paused publication did not release")
	}
	code, _, data := fileRequest(t, client, f, "GET", "/dav/old", nil, nil)
	if code != 200 || string(data) != "old" {
		t.Fatal("paused failed transaction leaked or changed content")
	}
}

func TestFilesInterruptedInputPreservesPriorState(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	for path, body := range map[string]string{"/dav/old": "original", "/dav/empty": ""} {
		code, _, _ := fileRequest(t, client, f, "PUT", path, strings.NewReader(body), nil)
		if code != 201 {
			t.Fatal("seed failed")
		}
	}
	_, _, listing := fileRequest(t, client, f, "PROPFIND", "/dav/", nil, map[string]string{"Depth": "1"})
	for _, path := range []string{"/dav/old", "/dav/empty", "/dav/new"} {
		for _, chunked := range []bool{false, true} {
			conn, err := fixtureTLSDial(f, &tls.Config{RootCAs: f.roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})
			if err != nil {
				t.Fatal("truncation fixture TLS failed")
			}
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			framing := "Content-Length: 100\r\n\r\nabc"
			if chunked {
				framing = "Transfer-Encoding: chunked\r\n\r\n8\r\nabc"
			}
			wire := "PUT " + path + " HTTP/1.1\r\nHost: " + strings.TrimPrefix(f.origin, "https://") + "\r\nAuthorization: Basic " + base64.StdEncoding.EncodeToString([]byte("FixtureUser:"+f.password)) + "\r\n" + framing
			if _, err = io.WriteString(conn, wire); err != nil {
				conn.Close()
				t.Fatal("truncation fixture write failed")
			}
			conn.Close()
		}
	}
	// Subsequent complete requests are the public oracle; no status is expected
	// from a client that deliberately closed its disposable connection.
	var after []byte
	deadline := time.Now().Add(3 * time.Second)
	for {
		code, _, data := fileRequest(t, client, f, "PROPFIND", "/dav/", nil, map[string]string{"Depth": "1"})
		if code == 207 {
			after = data
			break
		}
		if code != 503 || time.Now().After(deadline) {
			t.Fatal("disconnected requests did not release admission")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !maps.Equal(fileListingMetadata(t, listing), fileListingMetadata(t, after)) {
		t.Fatal("interrupted body changed metadata")
	}
	p.stop()
	client.CloseIdleConnections()
	p = startServer(t, bin, f)
	defer p.stop()
	for path, want := range map[string]string{"/dav/old": "original", "/dav/empty": ""} {
		code, _, data := fileRequest(t, client, f, "GET", path, nil, nil)
		if code != 200 || string(data) != want {
			t.Fatal("interrupted save lost prior bytes")
		}
	}
	code, _, _ := fileRequest(t, client, f, "GET", "/dav/new", nil, nil)
	if code != 404 {
		t.Fatal("interrupted new file persisted")
	}
	code, _, _ = fileRequest(t, client, f, "PUT", "/dav/after", strings.NewReader("ok"), nil)
	if code != 201 {
		t.Fatal("interrupted requests leaked resources")
	}
}

func TestFilesRejectResourceKindsAndTrailingSlash(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	namespaceStatus(t, client, f, "MKCOL", "/dav/Folder/", nil, 201)
	code, _, _ := fileRequest(t, client, f, "PUT", "/dav/file", strings.NewReader("old"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"PUT", "/dav/", 405}, {"PUT", "/dav/Folder/", 405}, {"PUT", "/dav/Folder", 405}, {"PUT", "/dav/file/", 400}, {"GET", "/dav/file/", 400}, {"HEAD", "/dav/file/", 400}, {"PUT", "/dav/missing/child", 409}, {"PUT", "/dav/file/child", 409}, {"MKCOL", "/dav/file/child", 409}, {"GET", "/dav/file/child", 404}} {
		code, _, _ := fileRequest(t, client, f, tc.method, tc.path, nil, nil)
		if code != tc.status {
			t.Fatalf("%s %s returned %d want %d", tc.method, tc.path, code, tc.status)
		}
	}
	code, _, data := fileRequest(t, client, f, "GET", "/dav/file", nil, nil)
	if code != 200 || string(data) != "old" {
		t.Fatal("kind conflict changed file")
	}
}

func TestFilesMigrationConflictPreservesV1(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	path := filepath.Join(f.dir, "godav.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal("fixture open failed")
	}
	if _, err = db.Exec(`CREATE TABLE file_contents(unrelated TEXT); INSERT INTO file_contents VALUES('fixture')`); err != nil {
		db.Close()
		t.Fatal("fixture creation failed")
	}
	if db.Close() != nil {
		t.Fatal("fixture close failed")
	}
	before := fixtureBytes(t, path)
	namespaceServerRefusal(t, bin, f)
	if !bytes.Equal(before, fixtureBytes(t, path)) {
		t.Fatal("file migration conflict modified v1 state")
	}
}

func TestFilesRepresentationHeaderProfile(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	code, _, _ := fileRequest(t, client, f, "PUT", "/dav/file", strings.NewReader("0123456789"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	_, metadata, _ := fileRequest(t, client, f, "HEAD", "/dav/file", nil, nil)
	if _, err := http.ParseTime(metadata.Get("Last-Modified")); err != nil {
		t.Fatal("modification date invalid")
	}
	code, _, data := fileRequest(t, client, f, "GET", "/dav/file", nil, map[string]string{"Range": "bytes=0-1", "If-Range": metadata.Get("Last-Modified")})
	if code != 200 || string(data) != "0123456789" {
		t.Fatal("second-resolution date incorrectly treated as strong range validator")
	}
	for _, kind := range []string{"text/plain; x=\"\xff\"", "text/plain; x=\"é\""} {
		code, _, _ := fileRequest(t, client, f, "PUT", "/dav/file", strings.NewReader("new"), map[string]string{"Content-Type": kind})
		if code != 400 {
			t.Fatal("non-ASCII content type accepted")
		}
	}
	for _, method := range []string{"PROPFIND", "OPTIONS"} {
		code, _, _ := fileRequest(t, client, f, method, "/dav/file/", nil, map[string]string{"Depth": "0"})
		if code != 400 {
			t.Fatal("file trailing slash accepted")
		}
	}
}

func TestFilesV2MigrationAndAccountMaintenance(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "godav.sqlite"))
	if err != nil {
		t.Fatal("v2 fixture open failed")
	}
	_, err = db.Exec(`CREATE TABLE namespace_policy(id INTEGER PRIMARY KEY NOT NULL CHECK(id=1),algorithm TEXT NOT NULL,unicode_version TEXT NOT NULL) STRICT;
 CREATE TABLE nodes(id TEXT PRIMARY KEY NOT NULL,parent_id TEXT REFERENCES nodes(id),name TEXT NOT NULL,name_key TEXT NOT NULL,created_at INTEGER NOT NULL,modified_at INTEGER NOT NULL,
 CHECK((id='root' AND parent_id IS NULL AND name='' AND name_key='') OR (id<>'root' AND parent_id IS NOT NULL AND octet_length(name) BETWEEN 1 AND 255 AND octet_length(name_key) BETWEEN 1 AND 255)),UNIQUE(parent_id,name_key)) STRICT;
 INSERT INTO namespace_policy VALUES(1,'simplefold-min/no-normalization/v1','17.0.0');
 INSERT INTO nodes VALUES('root',NULL,'','',946684800,946684800),('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','root','Kept','KEPT',946684800,946684800);
 PRAGMA user_version=2;`)
	if err != nil || db.Close() != nil {
		t.Fatal("v2 fixture creation failed")
	}
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	code, _, listing := fileRequest(t, client, f, "PROPFIND", "/dav/Kept/", nil, map[string]string{"Depth": "0"})
	props := fileListingMetadata(t, listing)["/dav/Kept/"]
	if code != 207 || props[0] != "Kept" || props[4] != "Sat, 01 Jan 2000 00:00:00 GMT" || props[5] != "true" {
		t.Fatal("v2 folder migration changed public metadata")
	}
	code, h, _ := fileRequest(t, client, f, "PUT", "/dav/kept/Proof.bin", bytes.NewReader([]byte{0, 1, 255, 2}), nil)
	if code != 201 {
		t.Fatal("v2 migration did not enable file writes")
	}
	etag := h.Get("ETag")
	p.stop()
	client.CloseIdleConnections()
	console, out, stderr, exit := privateConsoleCommand(t, t.TempDir(), bin, "add", "user", "--config", f.config, "--username", "Second.User")
	if exit != 0 || out != "godav: user added\n" {
		t.Fatal("v3 offline account add failed")
	}
	secondPassword := accountSecret(t, console, out, stderr)
	console, out, stderr, exit = privateConsoleCommand(t, t.TempDir(), bin, "reset", "password", "--config", f.config, "--username", "FixtureUser")
	if exit != 0 || out != "godav: password reset\n" {
		t.Fatal("v3 offline account reset failed")
	}
	newPassword := accountSecret(t, console, out, stderr)
	oldPassword := f.password
	f.password = newPassword
	p = startServer(t, bin, f)
	defer p.stop()
	assertAccountHTTPS(t, client, f, "FixtureUser", oldPassword, 401)
	assertAccountHTTPS(t, client, f, "Second.User", secondPassword, 200)
	code, h, data := fileRequest(t, client, f, "GET", "/dav/KEPT/proof.BIN", nil, nil)
	if code != 200 || !bytes.Equal(data, []byte{0, 1, 255, 2}) || h.Get("ETag") != etag {
		t.Fatal("account maintenance lost file bytes/validator")
	}
	if got := namespaceListing(t, client, f, "/dav/Kept/", "1"); !slices.Equal(got, []string{"/dav/Kept/", "/dav/Kept/Proof.bin"}) {
		t.Fatal("migration/account maintenance changed spelling")
	}
}

func TestFilesRefuseMalformedContentWithoutRepair(t *testing.T) {
	bin := development(t)
	for _, statement := range []string{
		`PRAGMA user_version=5`,
		`DROP TABLE file_contents`,
		`PRAGMA ignore_check_constraints=ON; UPDATE file_contents SET size=size+1`,
		`UPDATE file_contents SET content_type='not a media type'`,
		`UPDATE file_contents SET content_type='attachment'`,
		`UPDATE file_contents SET etag='"gggggggggggggggggggggggggggggggg"'`,
		`INSERT INTO nodes SELECT 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',id,'Child','CHILD',1,1 FROM nodes WHERE name='data'`,
		`PRAGMA foreign_keys=OFF; DELETE FROM nodes WHERE name='data'`,
		`CREATE TRIGGER fixture_noop AFTER UPDATE ON file_contents BEGIN SELECT 1; END`,
	} {
		t.Run(fmt.Sprint(len(statement)), func(t *testing.T) {
			f := newServerFixture(t, bin)
			p := startServer(t, bin, f)
			client := fixtureClient(f)
			code, _, _ := fileRequest(t, client, f, "PUT", "/dav/data", strings.NewReader("known fixture"), nil)
			if code != 201 {
				t.Fatal("seed failed")
			}
			client.CloseIdleConnections()
			p.stop()
			path := filepath.Join(f.dir, "godav.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal("fixture open failed")
			}
			_, err = db.Exec(statement)
			closeErr := db.Close()
			if err != nil || closeErr != nil {
				t.Fatal("malformed fixture could not be prepared")
			}
			before := fixtureBytes(t, path)
			out, stderr, exit := command(t, f.dir, bin, "init", "config", "--config", f.config, "--username", "Other")
			if exit != 1 || out != "" || stderr == "" {
				t.Fatal("malformed file state accepted")
			}
			namespaceServerRefusal(t, bin, f)
			if !bytes.Equal(before, fixtureBytes(t, path)) {
				t.Fatal("malformed file state changed during refusal")
			}
		})
	}
}

func TestFilesInProcessConcurrentSnapshots(t *testing.T) {
	f, client, _ := liveFiles(t, 32<<20)
	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	a := bytes.Repeat([]byte{0x17}, 8192)
	b := bytes.Repeat([]byte{0xE5}, 8192)
	for i, payload := range [][]byte{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			path := "/dav/Race"
			if i == 1 {
				path = "/dav/rACE"
			}
			req, _ := http.NewRequest("PUT", f.origin+path, bytes.NewReader(payload))
			req.SetBasicAuth("FixtureUser", f.password)
			response, err := retryAdmission(client, req)
			if err != nil {
				results <- 0
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			results <- response.StatusCode
		}()
	}
	close(start)
	for range 10 {
		code, headers, data := fileRequest(t, client, f, "GET", "/dav/RACE", nil, nil)
		if code == 503 && admissionRefusal(headers) {
			continue
		}
		if code != 404 && (code != 200 || (!bytes.Equal(data, a) && !bytes.Equal(data, b))) {
			t.Fatal("reader saw a torn or invalid concurrent snapshot")
		}
	}
	wg.Wait()
	code, _, data := fileRequest(t, client, f, "GET", "/dav/RACE", nil, nil)
	if code != 200 || (!bytes.Equal(data, a) && !bytes.Equal(data, b)) {
		t.Fatal("final concurrent snapshot invalid")
	}
	close(results)
	codes := []int{}
	for code := range results {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	if !slices.Equal(codes, []int{201, 204}) {
		t.Fatalf("racing PUT outcomes %v", codes)
	}
	if got := namespaceListing(t, client, f, "/dav/", "1"); len(got) != 2 {
		t.Fatal("racing saves duplicated resource")
	}
}

func TestFilesReviewedRepresentationEdges(t *testing.T) {
	f, client, _ := liveFiles(t, 32<<20)
	for path, body := range map[string]string{"/dav/full": "0123456789", "/dav/empty": ""} {
		if code, _, _ := fileRequest(t, client, f, "PUT", path, strings.NewReader(body), nil); code != 201 {
			t.Fatal("seed failed")
		}
	}
	_, before, _ := fileRequest(t, client, f, "HEAD", "/dav/full", nil, nil)
	for _, kind := range []string{"attachment", "text", "application; charset=utf-8"} {
		t.Run(kind, func(t *testing.T) {
			code, _, _ := fileRequest(t, client, f, "PUT", "/dav/full", strings.NewReader("changed"), map[string]string{"Content-Type": kind})
			if code != 400 {
				t.Fatalf("invalid media type accepted: %d", code)
			}
			code, h, body := fileRequest(t, client, f, "GET", "/dav/full", nil, nil)
			if code != 200 || string(body) != "0123456789" || h.Get("ETag") != before.Get("ETag") {
				t.Fatal("invalid media type changed state")
			}
		})
	}
	for _, path := range []string{"/dav/full", "/dav/empty"} {
		for _, value := range []string{"bytes=", "bytes=-0", "bytes=-000", "bytes=-+0", "bytes=+0-1", "bytes=-2", "bytes=0-1", "bytes=garbage"} {
			t.Run(path+value, func(t *testing.T) {
				want := 416
				if path == "/dav/empty" {
					want = 200
				} else if value == "bytes=-2" || value == "bytes=0-1" {
					want = 206
				}
				code, h, body := fileRequest(t, client, f, "GET", path, nil, map[string]string{"Range": value})
				if code != want || (path == "/dav/empty" && (len(body) != 0 || h.Get("Content-Range") != "")) {
					t.Fatalf("range returned %d, want %d", code, want)
				}
			})
		}
	}
	// Read preconditions have precedence over range syntax, including the edge profile.
	for _, value := range []string{"bytes=", "bytes=-0"} {
		for _, condition := range []struct {
			header, value string
			status        int
		}{{"If-None-Match", before.Get("ETag"), 304}, {"If-Match", `"missing"`, 412}, {"If-Range", `"missing"`, 200}} {
			code, _, _ := fileRequest(t, client, f, "GET", "/dav/full", nil, map[string]string{"Range": value, condition.header: condition.value})
			if code != condition.status {
				t.Errorf("range changed condition precedence: %d", code)
			}
		}
	}
}

func TestFilesInProcessPausedReaderAllowsSave(t *testing.T) {
	f, client, _ := liveFiles(t, 64<<20)
	original := bytes.Repeat([]byte("read snapshot fixture\x00"), (32<<20)/22)
	code, first, _ := fileRequest(t, client, f, "PUT", "/dav/large", bytes.NewReader(original), nil)
	if code != 201 {
		t.Fatal("reader seed failed")
	}
	// A small socket receive buffer and an undrained large body keep HTTP output
	// pending while public requests prove the database is available elsewhere.
	conn, err := fixtureTLSDial(f, &tls.Config{RootCAs: f.roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal("reader fixture TLS failed")
	}
	defer conn.Close()
	if err := conn.NetConn().(*net.TCPConn).SetReadBuffer(128 << 10); err != nil {
		t.Fatal("reader fixture buffer failed")
	}
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	req, _ := http.NewRequest("GET", f.origin+"/dav/large", nil)
	req.SetBasicAuth("FixtureUser", f.password)
	if req.Write(conn) != nil {
		t.Fatal("reader fixture request failed")
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil || response.StatusCode != 200 || response.Header.Get("ETag") != first.Get("ETag") {
		t.Fatal("reader snapshot headers failed")
	}
	defer response.Body.Close()
	start := time.Now()
	code, _, _ = fileRequest(t, client, f, "PUT", "/dav/unrelated", strings.NewReader("ok"), nil)
	if code != 201 || time.Since(start) > 2*time.Second {
		t.Fatal("slow reader monopolized unrelated save")
	}
	code, replacement, _ := fileRequest(t, client, f, "PUT", "/dav/large", strings.NewReader("new snapshot"), nil)
	if code != 204 || replacement.Get("ETag") == first.Get("ETag") {
		t.Fatal("snapshot replacement failed")
	}
	data, err := io.ReadAll(response.Body)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("paused reader did not retain its whole committed version")
	}
	code, h, data := fileRequest(t, client, f, "GET", "/dav/large", nil, nil)
	if code != 200 || string(data) != "new snapshot" || h.Get("ETag") != replacement.Get("ETag") {
		t.Fatal("fresh snapshot differs")
	}
	// Both maximum reservations must be available again after network completion.
	one := pausedFileUpload(t, client, f, "/dav/budget-one")
	two := pausedFileUpload(t, client, f, "/dav/budget-two")
	one()
	two()
}

func TestFilesCancelledPrecommitPreservesSnapshot(t *testing.T) {
	bin := development(t)
	paused := faultBuild(t, "godav_test_pause_file_commit")
	f := newServerFixture(t, bin)
	config, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal("cancel fixture config failed")
	}
	config = bytes.ReplaceAll(config, []byte("33554432"), []byte("67108864"))
	if os.WriteFile(f.config, config, 0600) != nil {
		t.Fatal("cancel fixture config failed")
	}
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	code, _, _ := fileRequest(t, client, f, "PUT", "/dav/old", strings.NewReader("old committed"), nil)
	if code != 201 {
		t.Fatal("cancel seed failed")
	}
	_, _, listing := fileRequest(t, client, f, "PROPFIND", "/dav/", nil, map[string]string{"Depth": "1"})
	p.stop()
	client.CloseIdleConnections()
	p = startServer(t, paused, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "PUT", f.origin+"/dav/old", strings.NewReader("fully staged replacement"))
	req.SetBasicAuth("FixtureUser", f.password)
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(p.logs.String(), "test-only file publication pause") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(p.logs.String(), "test-only file publication pause") {
		t.Fatal("fully staged precommit pause not reached")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled request did not stop")
	}
	code, _, after := fileRequest(t, client, f, "PROPFIND", "/dav/", nil, map[string]string{"Depth": "1"})
	if code != 207 || !maps.Equal(fileListingMetadata(t, listing), fileListingMetadata(t, after)) {
		t.Fatal("cancelled precommit changed metadata")
	}
	code, _, data := fileRequest(t, client, f, "GET", "/dav/old", nil, nil)
	if code != 200 || string(data) != "old committed" {
		t.Fatal("cancelled precommit changed bytes")
	}
	one := pausedFileUpload(t, client, f, "/dav/one")
	two := pausedFileUpload(t, client, f, "/dav/two")
	one()
	two()
	p.stop()
	client.CloseIdleConnections()
	p = startServer(t, bin, f)
	defer p.stop()
	code, _, data = fileRequest(t, client, f, "GET", "/dav/old", nil, nil)
	if code != 200 || string(data) != "old committed" {
		t.Fatal("cancelled save survived reopen")
	}
	code, _, _ = fileRequest(t, client, f, "PUT", "/dav/after", strings.NewReader("ok"), nil)
	if code != 201 {
		t.Fatal("cancelled precommit left store unusable")
	}
}
