package cli_test

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"
)

func moveCreated(t *testing.T, c *http.Client, f serverFixture, path string) string {
	t.Helper()
	code, _, data := fileRequest(t, c, f, "PROPFIND", path, strings.NewReader(`<d:propfind xmlns:d="DAV:"><d:prop><d:creationdate/></d:prop></d:propfind>`), map[string]string{"Depth": "0"})
	var result struct {
		Responses []struct {
			Stats []struct {
				Status string `xml:"DAV: status"`
				Prop   struct {
					Created string `xml:"DAV: creationdate"`
				} `xml:"DAV: prop"`
			} `xml:"DAV: propstat"`
		} `xml:"DAV: response"`
	}
	if code != 207 || xml.Unmarshal(data, &result) != nil || len(result.Responses) != 1 || len(result.Responses[0].Stats) != 1 || result.Responses[0].Stats[0].Status != "HTTP/1.1 200 OK" || result.Responses[0].Stats[0].Prop.Created == "" {
		t.Fatal("creation metadata unavailable")
	}
	return result.Responses[0].Stats[0].Prop.Created
}

func TestMoveDuringConditionalUpload(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/source", strings.NewReader("committed"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ready := make(chan struct{}, 1)
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{Got100Continue: func() { ready <- struct{}{} }})
	req, _ := http.NewRequestWithContext(ctx, "PUT", f.origin+"/dav/source", reader)
	req.SetBasicAuth("FixtureUser", f.password)
	req.Header.Set("If-Match", h.Get("ETag"))
	req.Header.Set("Expect", "100-continue")
	done := make(chan int, 1)
	go func() {
		resp, err := c.Do(req)
		if err != nil {
			done <- 0
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not begin")
	}
	if _, err := writer.Write([]byte("staged old-path bytes")); err != nil {
		t.Fatal("staging failed")
	}
	code, _, _ = moveRequest(t, c, f, "/dav/source", "/dav/destination", map[string]string{"If-Match": h.Get("ETag")})
	if code != 201 {
		t.Fatalf("staging blocked MOVE:%d", code)
	}
	writer.Close()
	select {
	case code = <-done:
		if code != 412 {
			t.Fatalf("old-path conditional publication:%d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not finish")
	}
	code, _, body := fileRequest(t, c, f, "GET", "/dav/destination", nil, nil)
	if code != 200 || string(body) != "committed" {
		t.Fatal("stale upload changed moved bytes")
	}
	code, _, _ = fileRequest(t, c, f, "HEAD", "/dav/source", nil, nil)
	if code != 404 {
		t.Fatal("stale upload recreated old mapping")
	}
}

func TestMoveParentMembershipAndDestinationChildLocks(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	for _, name := range []string{"old", "new", "from", "to"} {
		fileRequest(t, c, f, "MKCOL", "/dav/"+name, nil, nil)
	}
	fileRequest(t, c, f, "PUT", "/dav/old/file", strings.NewReader("child"), nil)
	old := acquireFixtureLock(t, c, f, "/dav/old", "0", "Second-60")
	next := acquireFixtureLock(t, c, f, "/dav/new", "infinity", "Second-60")
	code, _, _ := moveRequest(t, c, f, "/dav/old/file", "/dav/new/file", nil)
	if code != 423 {
		t.Fatalf("parent membership:%d", code)
	}
	condition := map[string]string{"If": "<" + f.origin + "/dav/old> (" + old + ") <" + f.origin + "/dav/new> (" + next + ")"}
	code, _, _ = moveRequest(t, c, f, "/dav/old/file", "/dav/new/file", condition)
	if code != 201 {
		t.Fatalf("parent tokens:%d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/new/file", strings.NewReader("forbidden"), nil)
	if code != 423 {
		t.Fatal("destination ancestor not inherited")
	}
	fileRequest(t, c, f, "PUT", "/dav/from/Keep", strings.NewReader("incoming"), nil)
	fileRequest(t, c, f, "PUT", "/dav/to/keep", strings.NewReader("old"), nil)
	fileRequest(t, c, f, "PUT", "/dav/to/gone", strings.NewReader("remove"), nil)
	keep := acquireFixtureLock(t, c, f, "/dav/to/keep", "0", "Second-60")
	gone := acquireFixtureLock(t, c, f, "/dav/to/gone", "0", "Second-60")
	condition = map[string]string{"If": "<" + f.origin + "/dav/to/keep> (" + keep + ") <" + f.origin + "/dav/to/gone> (" + gone + ")"}
	code, _, _ = moveRequest(t, c, f, "/dav/from", "/dav/to", condition)
	if code != 204 {
		t.Fatalf("child-token overwrite:%d", code)
	}
	code, _, body := fileRequest(t, c, f, "PROPFIND", "/dav/to/Keep", nil, map[string]string{"Depth": "0"})
	if code != 207 || !strings.Contains(string(body), "/dav/to/Keep") || strings.Contains(string(body), "/dav/to/keep") {
		t.Fatal("retained lease not canonical")
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/to/Keep", strings.NewReader("denied"), nil)
	if code != 423 {
		t.Fatal("surviving destination child lease lost")
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/to/gone", strings.NewReader("fresh"), nil)
	if code != 201 {
		t.Fatal("removed destination lease survived")
	}
}

func TestMoveReopenMetadataAndNoPayloadRead(t *testing.T) {
	bin := development(t)
	readFault := faultBuild(t, "godav_test_fail_file_read")
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	fileRequest(t, c, f, "MKCOL", "/dav/Parent", nil, nil)
	fileRequest(t, c, f, "MKCOL", "/dav/Destination", nil, nil)
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/Parent/file", strings.NewReader("persistent bytes"), map[string]string{"Content-Type": "text/plain"})
	if code != 201 {
		t.Fatal("seed failed")
	}
	patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32CreationTime>preserved creation</z:Win32CreationTime><z:Win32FileAttributes>20</z:Win32FileAttributes></d:prop></d:set></d:propertyupdate>`
	code, _, _ = metadataPatch(t, c, f, "/dav/Parent/file", strings.NewReader(patch), nil, "Win32CreationTime", "Win32FileAttributes")
	if code != 207 {
		t.Fatal("metadata seed failed")
	}
	created := moveCreated(t, c, f, "/dav/Parent/file")
	_, before, _ := fileRequest(t, c, f, "HEAD", "/dav/Parent/file", nil, nil)
	c.CloseIdleConnections()
	p.stop()
	p = startServer(t, readFault, f)
	code, _, _ = moveRequest(t, c, f, "/dav/Parent", "/dav/Destination/Folder", nil)
	if code != 201 {
		t.Fatalf("metadata-only collection MOVE:%d", code)
	}
	code, after, _ := fileRequest(t, c, f, "HEAD", "/dav/Destination/Folder/file", nil, nil)
	if code != 200 || after.Get("ETag") != before.Get("ETag") || after.Get("Content-Type") != "text/plain" {
		t.Fatal("metadata differs after move")
	}
	c.CloseIdleConnections()
	p.stop()
	p = startServer(t, bin, f)
	defer p.stop()
	code, _, body := fileRequest(t, c, f, "GET", "/dav/Destination/Folder/file", nil, nil)
	if code != 200 || string(body) != "persistent bytes" || moveCreated(t, c, f, "/dav/Destination/Folder/file") != created {
		t.Fatal("reopen changed moved identity metadata/content")
	}
	assertStoredMetadata(t, c, f, "/dav/Destination/Folder/file", map[string]string{"Win32CreationTime": "preserved creation", "Win32FileAttributes": "20"})
	code, _, _ = fileRequest(t, c, f, "PROPFIND", "/dav/Parent", nil, map[string]string{"Depth": "0"})
	if code != 404 {
		t.Fatal("old mapping returned after reopen")
	}
}

func TestMovePrecommitFailurePreservesBothSidesAndLocks(t *testing.T) {
	bin := development(t)
	faulty := faultBuild(t, "godav_test_fail_move_commit")
	f := newServerFixture(t, bin)
	p := startServer(t, faulty, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	for path, data := range map[string]string{"/dav/source": "original source", "/dav/destination": "original destination"} {
		code, _, _ := fileRequest(t, c, f, "PUT", path, strings.NewReader(data), nil)
		if code != 201 {
			t.Fatal("seed failed")
		}
	}
	source := acquireFixtureLock(t, c, f, "/dav/source", "0", "Second-60")
	destination := acquireFixtureLock(t, c, f, "/dav/destination", "0", "Second-60")
	_, _, before := fileRequest(t, c, f, "PROPFIND", "/dav/", nil, map[string]string{"Depth": "1"})
	condition := map[string]string{"If": "<" + f.origin + "/dav/source> (" + source + ") <" + f.origin + "/dav/destination> (" + destination + ")"}
	for attempt := 0; attempt < 2; attempt++ {
		code, _, _ := moveRequest(t, c, f, "/dav/source", "/dav/destination", condition)
		if code != 500 {
			t.Fatalf("precommit:%d", code)
		}
	}
	_, _, after := fileRequest(t, c, f, "PROPFIND", "/dav/", nil, map[string]string{"Depth": "1"})
	// Lock discovery timeout values decrease, so compare stable file metadata.
	if !equalMoveListing(t, before, after) {
		t.Fatal("failed move changed metadata")
	}
	for path, token := range map[string]string{"/dav/source": source, "/dav/destination": destination} {
		code, _, _ := fileRequest(t, c, f, "HEAD", path, nil, map[string]string{"If": "(" + token + ")"})
		if code != 200 {
			t.Fatal("failed move lost lease")
		}
	}
	c.CloseIdleConnections()
	p.stop()
	p = startServer(t, bin, f)
	defer p.stop()
	for path, data := range map[string]string{"/dav/source": "original source", "/dav/destination": "original destination"} {
		code, _, body := fileRequest(t, c, f, "GET", path, nil, nil)
		if code != 200 || string(body) != data {
			t.Fatal("rollback did not survive reopen")
		}
	}
	code, _, _ := moveRequest(t, c, f, "/dav/source", "/dav/destination", nil)
	if code != 204 {
		t.Fatalf("store not reusable:%d", code)
	}
}

func equalMoveListing(t *testing.T, a, b []byte) bool {
	t.Helper()
	x, y := fileListingMetadata(t, a), fileListingMetadata(t, b)
	if len(x) != len(y) {
		return false
	}
	for path, value := range x {
		if y[path] != value {
			return false
		}
	}
	return true
}

func TestMoveCancelledPrecommitAndReuse(t *testing.T) {
	bin := development(t)
	paused := faultBuild(t, "godav_test_pause_move_commit")
	f := newServerFixture(t, bin)
	p := startServer(t, paused, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	for path, data := range map[string]string{"/dav/source": "source", "/dav/destination": "destination"} {
		fileRequest(t, c, f, "PUT", path, strings.NewReader(data), nil)
	}
	_, _, before := fileRequest(t, c, f, "PROPFIND", "/dav/", nil, map[string]string{"Depth": "1"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "MOVE", f.origin+"/dav/source", nil)
	req.SetBasicAuth("FixtureUser", f.password)
	req.Header.Set("Destination", f.origin+"/dav/destination")
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := c.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	deadline := time.Now().Add(4 * time.Second)
	for !strings.Contains(p.logs.String(), "test-only move publication pause") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(p.logs.String(), "test-only move publication pause") {
		t.Fatal("MOVE did not reach precommit")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not finish")
	}
	code, _, after := fileRequest(t, c, f, "PROPFIND", "/dav/", nil, map[string]string{"Depth": "1"})
	if code != 207 || !equalMoveListing(t, before, after) {
		t.Fatal("cancel changed snapshot")
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/independent", bytes.NewBufferString("ok"), nil)
	if code != 201 {
		t.Fatal("cancel leaked operation gate")
	}
	c.CloseIdleConnections()
	p.stop()
	p = startServer(t, bin, f)
	defer p.stop()
	for path, data := range map[string]string{"/dav/source": "source", "/dav/destination": "destination"} {
		code, _, body := fileRequest(t, c, f, "GET", path, nil, nil)
		if code != 200 || string(body) != data {
			t.Fatal("cancel did not persist rollback")
		}
	}
}

func TestMoveSafeDestinationTrace(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	fileRequest(t, c, f, "PUT", "/dav/private-source-name", strings.NewReader("private-file-content"), nil)
	code, _, _ := moveRequest(t, c, f, "/dav/private-source-name", "/dav/private-destination-name", nil)
	if code != 201 {
		t.Fatal("move failed")
	}
	code, _, _ = moveRequest(t, c, f, "/dav/private-destination-name", "/dav/ignored?private-query-marker", nil)
	if code != 400 {
		t.Fatal("query rejected incorrectly")
	}
	c.CloseIdleConnections()
	p.stop()
	logs := p.logs.String()
	if strings.Count(logs, "method=MOVE") != 2 || strings.Count(logs, "destination=") != 2 || !strings.Contains(logs, "destination=<redacted>") {
		t.Fatal("MOVE trace missing sanitized destination or duplicated completion")
	}
	for _, secret := range []string{f.password, "private-source-name", "private-destination-name", "private-file-content", "private-query-marker", "Authorization", "Destination:"} {
		if strings.Contains(logs, secret) {
			t.Fatal("private request data entered trace")
		}
	}
}
