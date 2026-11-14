package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMetadataCreationDate(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	before := time.Now().UTC().Add(-time.Second)
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/created", strings.NewReader("bytes"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	code, _, data := fileRequest(t, c, f, "PROPFIND", "/dav/created", strings.NewReader(`<d:propfind xmlns:d="DAV:"><d:prop><d:creationdate/></d:prop></d:propfind>`), map[string]string{"Depth": "0"})
	var response struct {
		Responses []struct {
			Stats []struct {
				Status string `xml:"DAV: status"`
				Prop   struct {
					Created string `xml:"DAV: creationdate"`
				} `xml:"DAV: prop"`
			} `xml:"DAV: propstat"`
		} `xml:"DAV: response"`
	}
	if code != 207 || xml.Unmarshal(data, &response) != nil || len(response.Responses) != 1 || len(response.Responses[0].Stats) != 1 {
		t.Fatal("creation response invalid")
	}
	stat := response.Responses[0].Stats[0]
	created, err := time.Parse(time.RFC3339, stat.Prop.Created)
	if stat.Status != "HTTP/1.1 200 OK" || err != nil || created.Before(before) || created.After(time.Now()) || created.Format(time.RFC3339) != stat.Prop.Created || created.Nanosecond() != 0 {
		t.Fatalf("live creation date unavailable: %s", stat.Status)
	}
}

func TestMetadataWindowsStringsPersist(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/meta", strings.NewReader("original"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32CreationTime>A &amp; B</z:Win32CreationTime><z:Win32LastModifiedTime>opaque modified</z:Win32LastModifiedTime><z:Win32LastAccessTime></z:Win32LastAccessTime><z:Win32FileAttributes>00000020</z:Win32FileAttributes></d:prop></d:set></d:propertyupdate>`
	code, _, data := metadataPatch(t, c, f, "/dav/meta", strings.NewReader(patch), nil, "Win32CreationTime", "Win32LastModifiedTime", "Win32LastAccessTime", "Win32FileAttributes")
	if code != 207 || !strings.Contains(string(data), "200 OK") {
		p.stop()
		t.Fatalf("supported patch failed: %d", code)
	}
	check := func() {
		assertStoredMetadata(t, c, f, "/dav/meta", map[string]string{"Win32CreationTime": "A & B", "Win32LastModifiedTime": "opaque modified", "Win32LastAccessTime": "", "Win32FileAttributes": "00000020"})
		t.Helper()
		code, _, data := fileRequest(t, c, f, "PROPFIND", "/dav/meta", strings.NewReader(`<d:propfind xmlns:d="DAV:" xmlns:x="urn:schemas-microsoft-com:"><d:prop><x:Win32CreationTime/><x:Win32LastModifiedTime/><x:Win32LastAccessTime/><x:Win32FileAttributes/></d:prop></d:propfind>`), map[string]string{"Depth": "0"})
		if code != 207 || strings.Contains(string(data), "404 Not Found") || !strings.Contains(string(data), "A &amp; B") || !strings.Contains(string(data), "opaque modified") || !strings.Contains(string(data), "00000020") {
			t.Fatal("stored properties unavailable")
		}
	}
	check()
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/META", strings.NewReader("replacement"), nil)
	if code != 204 {
		t.Fatal("replacement failed")
	}
	check()
	p.stop()
	p = startServer(t, bin, f)
	defer p.stop()
	check()
}

func TestMetadataConditionalWrites(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/conditional", strings.NewReader("old"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	tag := h.Get("ETag")
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/conditional", strings.NewReader("lost"), map[string]string{"If-Match": `"stale"`})
	if code != 412 {
		t.Fatalf("stale write got %d", code)
	}
	code, h, data := fileRequest(t, c, f, "GET", "/dav/conditional", nil, nil)
	if code != 200 || string(data) != "old" || h.Get("ETag") != tag {
		t.Fatal("failed condition changed state")
	}
	code, h, _ = fileRequest(t, c, f, "PUT", "/dav/conditional", strings.NewReader("new"), map[string]string{"If-Match": tag})
	if code != 204 || h.Get("ETag") == tag {
		t.Fatal("matching conditional save failed")
	}
}

func TestMetadataAtomicPatchConditions(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/meta", strings.NewReader("unchanged"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	tag := h.Get("ETag")
	patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32FileAttributes>20</z:Win32FileAttributes></d:prop></d:set></d:propertyupdate>`
	code, h, _ = fileRequest(t, c, f, "PROPPATCH", "/dav/meta", strings.NewReader(patch), map[string]string{"If-Match": `"stale"`})
	if code != 412 {
		t.Fatalf("stale patch got %d", code)
	}
	code, h, _ = fileRequest(t, c, f, "HEAD", "/dav/meta", nil, nil)
	if code != 200 || h.Get("ETag") != tag {
		t.Fatal("stale patch changed tag")
	}
	code, h, _ = metadataPatch(t, c, f, "/dav/meta", strings.NewReader(patch), map[string]string{"If-Match": tag}, "Win32FileAttributes")
	if code != 207 || h.Get("ETag") == tag {
		t.Fatal("matching patch failed to rotate tag")
	}
	tag = h.Get("ETag")
	code, h, _ = metadataPatch(t, c, f, "/dav/meta", strings.NewReader(patch), nil, "Win32FileAttributes")
	if code != 207 || h.Get("ETag") != tag {
		t.Fatal("no-op patch rotated tag")
	}
	bad := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32FileAttributes>bad</z:Win32FileAttributes><d:creationdate>bad</d:creationdate></d:prop></d:set></d:propertyupdate>`
	code, h, data := fileRequest(t, c, f, "PROPPATCH", "/dav/meta", strings.NewReader(bad), nil)
	if code != 207 || h.Get("ETag") != tag || strings.Contains(string(data), "200 OK") || !strings.Contains(string(data), "424 Failed Dependency") || !strings.Contains(string(data), "403 Forbidden") || !strings.Contains(string(data), "cannot-modify-protected-property") {
		t.Fatal("atomic protected failure response invalid")
	}
	code, h, data = fileRequest(t, c, f, "GET", "/dav/meta", nil, nil)
	if code != 200 || h.Get("ETag") != tag || string(data) != "unchanged" {
		t.Fatal("metadata changed bytes or failed-patch tag")
	}
}

func TestMetadataReadTagFields(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/read", strings.NewReader("abcdef"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	tag := h.Get("ETag")
	req, _ := http.NewRequest("GET", f.origin+"/dav/read", nil)
	req.SetBasicAuth("FixtureUser", f.password)
	req.Header.Add("If-None-Match", `"other,tag"`)
	req.Header.Add("If-None-Match", "W/"+tag)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 304 {
		t.Fatalf("later tag field ignored: %d", resp.StatusCode)
	}
	for _, method := range []string{"GET", "HEAD"} {
		code, _, _ = fileRequest(t, c, f, method, "/dav/read", nil, map[string]string{"If-Match": tag + " garbage"})
		if code != 400 {
			t.Fatalf("malformed matching field accepted: %d", code)
		}
		code, _, _ = fileRequest(t, c, f, method, "/dav/read", nil, map[string]string{"If-Match": "W/" + tag})
		if code != 412 {
			t.Fatal("weak match accepted")
		}
	}
	code, _, data := fileRequest(t, c, f, "GET", "/dav/read", nil, map[string]string{"Range": "bytes=0-1", "If-Range": tag + " garbage"})
	if code != 200 || string(data) != "abcdef" {
		t.Fatal("malformed If-Range accepted")
	}
}

func TestMetadataCollectionConditions(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, _, _ := fileRequest(t, c, f, "MKCOL", "/dav/absent/", nil, map[string]string{"If-Match": "*"})
	if code != 412 {
		t.Fatalf("absent collection expectation got %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "MKCOL", "/dav/new/", nil, map[string]string{"If-None-Match": "*"})
	if code != 201 {
		t.Fatal("absent collection creation rejected")
	}
	patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32FileAttributes>10</z:Win32FileAttributes></d:prop></d:set></d:propertyupdate>`
	code, h, _ := metadataPatch(t, c, f, "/dav/new/", strings.NewReader(patch), map[string]string{"If-Match": "*"}, "Win32FileAttributes")
	if code != 207 || h.Get("ETag") != "" {
		t.Fatal("collection wildcard/etag contract failed")
	}
	assertStoredMetadata(t, c, f, "/dav/new/", map[string]string{"Win32FileAttributes": "10"})
	code, _, _ = fileRequest(t, c, f, "PROPPATCH", "/dav/new/", strings.NewReader(patch), map[string]string{"If-Match": `"token"`})
	if code != 412 {
		t.Fatal("collection matched file tag")
	}
	code, _, _ = fileRequest(t, c, f, "MKCOL", "/dav/new/", nil, map[string]string{"If-Match": `"stale"`})
	if code != 405 {
		t.Fatal("condition masked method error")
	}
}

func TestMetadataExtensionsAndTrailingSlash(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/meta", strings.NewReader("old"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:" xmlns:x="urn:extension" x:hint="ignore"><x:ignored><d:remove><d:prop><z:Win32CreationTime/></d:prop></d:remove></x:ignored><d:set x:hint="ignore"><x:ignored/><d:prop><z:Win32CreationTime>kept</z:Win32CreationTime></d:prop></d:set></d:propertyupdate>`
	code, _, data := metadataPatch(t, c, f, "/dav/meta", strings.NewReader(patch), nil, "Win32CreationTime")
	if code != 207 || strings.Count(string(data), "200 OK") != 1 {
		t.Fatalf("extension subtree not safely ignored: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PROPPATCH", "/dav/meta/", strings.NewReader(patch), nil)
	if code != 400 {
		t.Fatalf("file trailing slash accepted: %d", code)
	}
}

func TestMetadataOrderedOperationsAndRejectionBounds(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/order", strings.NewReader("bytes"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	tag := h.Get("ETag")
	wrap := func(s string) string {
		return `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:">` + s + `</d:propertyupdate>`
	}
	set := `<d:set><d:prop><z:Win32CreationTime>temporary</z:Win32CreationTime></d:prop></d:set>`
	remove := `<d:remove><d:prop><z:Win32CreationTime/></d:prop></d:remove>`
	for i, body := range []string{wrap(remove), wrap(set + remove)} {
		names := []string{"Win32CreationTime"}
		if i == 1 {
			names = append(names, "Win32CreationTime")
		}
		code, h, _ = metadataPatch(t, c, f, "/dav/order", strings.NewReader(body), nil, names...)
		if code != 207 || h.Get("ETag") != tag {
			t.Fatal("final-state no-op changed tag")
		}
	}
	code, h, _ = metadataPatch(t, c, f, "/dav/order", strings.NewReader(wrap(remove+set)), nil, "Win32CreationTime", "Win32CreationTime")
	if code != 207 || h.Get("ETag") == tag {
		t.Fatal("operation order lost")
	}
	tag = h.Get("ETag")
	cases := []struct {
		body   string
		outer  int
		status string
	}{
		{"", 400, ""}, {wrap(""), 400, ""}, {wrap(set) + "junk", 400, ""}, {wrap(set) + wrap(set), 400, ""}, {`<!DOCTYPE d [<!ENTITY x "hi">]>` + wrap(set), 400, ""},
		{wrap(`<d:set><d:prop><z:Win32CreationTime><d:nested/></z:Win32CreationTime></d:prop></d:set>`), 207, "409 Conflict"},
		{wrap(`<d:remove><d:prop><z:win32CreationTime/></d:prop></d:remove>`), 207, "403 Forbidden"},
		{wrap(`<d:set><d:prop><z:Win32CreationTime xmlns:z="urn:wrong">no</z:Win32CreationTime></d:prop></d:set>`), 207, "403 Forbidden"},
		{wrap(`<d:set><d:prop><z:Win32CreationTime>` + strings.Repeat("x", 4097) + `</z:Win32CreationTime></d:prop></d:set>`), 413, ""},
		{wrap(strings.Repeat(set, 65)), 413, ""}, {strings.Repeat(" ", 65537), 413, ""},
		{wrap(`<d:set><d:prop><z:Win32CreationTime>` + strings.Repeat(`<d:n>`, 30) + strings.Repeat(`</d:n>`, 30) + `</z:Win32CreationTime></d:prop></d:set>`), 413, ""},
	}
	for i, tc := range cases {
		code, _, data := fileRequest(t, c, f, "PROPPATCH", "/dav/order", strings.NewReader(tc.body), nil)
		if code != tc.outer || tc.status != "" && !strings.Contains(string(data), tc.status) {
			t.Fatalf("rejection %d got %d", i, code)
		}
		code, h, _ = fileRequest(t, c, f, "HEAD", "/dav/order", nil, nil)
		if code != 200 || h.Get("ETag") != tag {
			t.Fatalf("rejection %d changed state", i)
		}
	}
}

func TestMetadataConditionsAtPublication(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/slow", strings.NewReader("original"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	tag := h.Get("ETag")
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ready := make(chan struct{}, 1)
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{Got100Continue: func() { ready <- struct{}{} }})
	req, _ := http.NewRequestWithContext(ctx, "PUT", f.origin+"/dav/slow", reader)
	req.SetBasicAuth("FixtureUser", f.password)
	req.Header.Set("If-Match", tag)
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
		t.Fatal("staging did not start")
	}
	if _, err := writer.Write([]byte("staged")); err != nil {
		t.Fatal("staging failed")
	}
	patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32CreationTime>new version</z:Win32CreationTime></d:prop></d:set></d:propertyupdate>`
	code, h, _ = metadataPatch(t, c, f, "/dav/slow", strings.NewReader(patch), map[string]string{"If-Match": tag}, "Win32CreationTime")
	if code != 207 {
		t.Fatalf("concurrent metadata patch got %d", code)
	}
	newTag := h.Get("ETag")
	writer.Close()
	select {
	case code = <-done:
		if code != 412 {
			t.Fatalf("staged stale publication got %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("staged request did not finish")
	}
	code, h, data := fileRequest(t, c, f, "GET", "/dav/slow", nil, nil)
	if code != 200 || h.Get("ETag") != newTag || string(data) != "original" {
		t.Fatal("stale staged upload damaged current state")
	}
}

func TestMetadataDiscoveryConditions(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/probe", strings.NewReader("bytes"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	tag := h.Get("ETag")
	for _, method := range []string{"PROPFIND", "OPTIONS"} {
		code, _, _ := fileRequest(t, c, f, method, "/dav/probe", nil, map[string]string{"Depth": "0", "If-Match": `"stale"`})
		if code != 412 {
			t.Fatalf("%s stale condition got %d", method, code)
		}
		code, _, _ = fileRequest(t, c, f, method, "/dav/probe", nil, map[string]string{"Depth": "0", "If-None-Match": "W/" + tag})
		if code != 412 {
			t.Fatal("non-GET condition returned success or304")
		}
	}
}

func TestMetadataCommitFailurePreservesSnapshot(t *testing.T) {
	bin := development(t)
	faulty := faultBuild(t, "godav_test_fail_metadata_commit")
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/fault", strings.NewReader("old"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	tag := h.Get("ETag")
	p.stop()
	p = startServer(t, faulty, f)
	patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32CreationTime>must rollback</z:Win32CreationTime></d:prop></d:set></d:propertyupdate>`
	code, _, failure := fileRequest(t, c, f, "PROPPATCH", "/dav/fault", strings.NewReader(patch), nil)
	p.stop()
	if code != 207 || !strings.Contains(string(failure), "500 Internal Server Error") || strings.Contains(string(failure), "200 OK") {
		t.Fatalf("failed commit returned %d", code)
	}
	p = startServer(t, bin, f)
	defer p.stop()
	code, h, data := fileRequest(t, c, f, "GET", "/dav/fault", nil, nil)
	if code != 200 || string(data) != "old" || h.Get("ETag") != tag {
		t.Fatal("failed commit changed bytes/tag")
	}
	code, _, data = fileRequest(t, c, f, "PROPFIND", "/dav/fault", strings.NewReader(`<d:propfind xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:prop><z:Win32CreationTime/></d:prop></d:propfind>`), map[string]string{"Depth": "0"})
	if code != 207 || !strings.Contains(string(data), "404 Not Found") {
		t.Fatal("rolled-back property persisted")
	}
}

func TestMetadataRefusesMalformedV4(t *testing.T) {
	bin := development(t)
	for _, statement := range []string{
		`UPDATE node_metadata SET creation=cast(x'ff' AS TEXT)`,
		`UPDATE node_metadata SET creation=char(0)`,
		`PRAGMA ignore_check_constraints=ON; UPDATE node_metadata SET creation=char(0)||replace(hex(zeroblob(3000)),'0','x')`,
		`CREATE TRIGGER metadata_side_effect AFTER UPDATE ON node_metadata BEGIN SELECT 1; END`,
		`DROP TABLE node_metadata`,
		`PRAGMA foreign_keys=OFF; UPDATE node_metadata SET node_id='orphan'`,
		`ALTER TABLE node_metadata RENAME TO old_metadata; CREATE TABLE node_metadata(node_id TEXT PRIMARY KEY NOT NULL REFERENCES nodes(id),creation TEXT,modified TEXT,accessed TEXT,attributes TEXT) STRICT; INSERT INTO node_metadata SELECT * FROM old_metadata; DROP TABLE old_metadata`,
	} {
		t.Run(fmt.Sprint(len(statement)), func(t *testing.T) {
			f := newServerFixture(t, bin)
			p := startServer(t, bin, f)
			c := fixtureClient(f)
			patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32CreationTime>seed</z:Win32CreationTime></d:prop></d:set></d:propertyupdate>`
			code, _, _ := metadataPatch(t, c, f, "/dav/", strings.NewReader(patch), nil, "Win32CreationTime")
			if code != 207 {
				t.Fatal("metadata seed failed")
			}
			c.CloseIdleConnections()
			p.stop()
			path := filepath.Join(f.dir, "godav.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal("fixture open failed")
			}
			_, err = db.Exec(statement)
			closeErr := db.Close()
			if err != nil || closeErr != nil {
				t.Fatal("malformed fixture setup failed")
			}
			before := fixtureBytes(t, path)
			out, stderr, exit := command(t, f.dir, bin, "init", "config", "--config", f.config, "--username", "Other")
			if exit != 1 || out != "" || stderr == "" {
				t.Fatal("malformed metadata accepted")
			}
			namespaceServerRefusal(t, bin, f)
			if !bytes.Equal(before, fixtureBytes(t, path)) {
				t.Fatal("malformed metadata repaired or changed")
			}
		})
	}
}

func TestMetadataConcurrentPatchExpectation(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/race", strings.NewReader("bytes"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	tag := h.Get("ETag")
	start := make(chan struct{})
	type outcome struct {
		code  int
		data  []byte
		value string
	}
	results := make(chan outcome, 2)
	for _, value := range []string{"first", "second"} {
		go func() {
			<-start
			deadline := time.Now().Add(5 * time.Second)
			for {
				body := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32CreationTime>` + value + `</z:Win32CreationTime></d:prop></d:set></d:propertyupdate>`
				req, _ := http.NewRequest("PROPPATCH", f.origin+"/dav/race", strings.NewReader(body))
				req.SetBasicAuth("FixtureUser", f.password)
				req.Header.Set("If-Match", tag)
				resp, err := c.Do(req)
				if err != nil {
					results <- outcome{}
					return
				}
				code := resp.StatusCode
				payload, readErr := io.ReadAll(resp.Body)
				if readErr != nil {
					code = 0
				}
				resp.Body.Close()
				if code == 503 && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				results <- outcome{code: code, data: payload, value: value}
				return
			}
		}()
	}
	close(start)
	a, b := <-results, <-results
	if !(a.code == 207 && b.code == 412 || a.code == 412 && b.code == 207) {
		t.Fatalf("competing expectations got %d/%d", a.code, b.code)
	}
	for _, result := range []outcome{a, b} {
		if result.code == 207 {
			assertMetadataPatchOK(t, result.code, result.data, "Win32CreationTime")
			assertStoredMetadata(t, c, f, "/dav/race", map[string]string{"Win32CreationTime": result.value})
		}
	}
	code, h, data := fileRequest(t, c, f, "GET", "/dav/race", nil, nil)
	if code != 200 || h.Get("ETag") == tag || string(data) != "bytes" {
		t.Fatal("conditional race changed bytes or lost metadata tag")
	}
}

func TestMetadataRejectsDuplicateXMLAttributes(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	body := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:" hint="a" hint="b"><d:set><d:prop><z:Win32CreationTime>bad</z:Win32CreationTime></d:prop></d:set></d:propertyupdate>`
	code, _, _ := fileRequest(t, c, f, "PROPPATCH", "/dav/", strings.NewReader(body), nil)
	if code != 400 {
		t.Fatalf("duplicate XML attributes got %d", code)
	}
}

func TestMetadataCombinedConditionBudget(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	tags := strings.TrimSuffix(strings.Repeat(`"x",`, 65), ",")
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/budget", strings.NewReader("bad"), map[string]string{"If-Match": tags, "If-None-Match": tags})
	if code != 431 {
		t.Fatalf("combined tag budget got %d", code)
	}
}

func TestMetadataConditionPrecedenceAndGrammar(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/precedence", strings.NewReader("original"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	tag := h.Get("ETag")
	old := "Sat, 01 Jan 2000 00:00:00 GMT"
	future := "Fri, 01 Jan 2100 00:00:00 GMT"
	cases := []struct {
		headers map[string]string
		want    int
	}{
		{map[string]string{"If-Match": tag, "If-Unmodified-Since": old}, 200},
		{map[string]string{"If-None-Match": `"other"`, "If-Modified-Since": future}, 200},
		{map[string]string{"If-Match": `"stale"`, "If-None-Match": tag}, 412},
		{map[string]string{"If-Match": tag, "If-None-Match": "W/" + tag}, 304},
		{map[string]string{"If-Unmodified-Since": old}, 412},
		{map[string]string{"If-Unmodified-Since": "invalid"}, 200},
		{map[string]string{"If-Modified-Since": future}, 304},
		{map[string]string{"If-Match": `"a,b", ` + tag}, 200},
		{map[string]string{"If-Match": tag + `, W/"other"`}, 200},
		{map[string]string{"If-Match": `*, ` + tag}, 400},
		{map[string]string{"If-None-Match": tag + ` broken`}, 400},
		{map[string]string{"If-Match": `"` + strings.Repeat("x", 8190) + `"`}, 412},
		{map[string]string{"If-Match": `"` + strings.Repeat("x", 8191) + `"`}, 431},
	}
	for i, tc := range cases {
		code, _, _ := fileRequest(t, c, f, "GET", "/dav/precedence", nil, tc.headers)
		if code != tc.want {
			t.Fatalf("read condition %d got %d want %d", i, code, tc.want)
		}
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/precedence", strings.NewReader("rejected"), map[string]string{"If-None-Match": "W/" + tag})
	if code != 412 {
		t.Fatal("weak write exclusion not applied")
	}
	code, h, _ = fileRequest(t, c, f, "PUT", "/dav/precedence", strings.NewReader("updated"), map[string]string{"If-Match": tag, "If-Unmodified-Since": old, "If-Modified-Since": future, "If-Range": "invalid"})
	if code != 204 {
		t.Fatal("write precedence/inapplicable conditions failed")
	}
	tag = h.Get("ETag")
	for _, method := range []string{"GET", "HEAD", "PROPFIND", "PROPPATCH"} {
		body := io.Reader(nil)
		if method == "PROPPATCH" {
			body = strings.NewReader(`<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:remove><d:prop><z:Win32CreationTime/></d:prop></d:remove></d:propertyupdate>`)
		}
		code, _, _ = fileRequest(t, c, f, method, "/dav/missing", body, map[string]string{"Depth": "0", "If-Match": tag})
		if code != 404 {
			t.Fatalf("missing %s masked by condition: %d", method, code)
		}
	}
}

func TestMetadataV3UpgradeAndOfflineAccounts(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "godav.sqlite"))
	if err != nil {
		t.Fatal("legacy fixture open failed")
	}
	_, err = db.Exec(`CREATE TABLE namespace_policy(id INTEGER PRIMARY KEY NOT NULL CHECK(id=1),algorithm TEXT NOT NULL,unicode_version TEXT NOT NULL) STRICT;
 CREATE TABLE nodes(id TEXT PRIMARY KEY NOT NULL,parent_id TEXT REFERENCES nodes(id),name TEXT NOT NULL,name_key TEXT NOT NULL,created_at INTEGER NOT NULL,modified_at INTEGER NOT NULL,
 CHECK((id='root' AND parent_id IS NULL AND name='' AND name_key='') OR (id<>'root' AND parent_id IS NOT NULL AND octet_length(name) BETWEEN 1 AND 255 AND octet_length(name_key) BETWEEN 1 AND 255)),UNIQUE(parent_id,name_key)) STRICT;
 INSERT INTO namespace_policy VALUES(1,'simplefold-min/no-normalization/v1','17.0.0');
 INSERT INTO nodes VALUES('root',NULL,'','',946684800,946684800),('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','root','Kept','KEPT',946684800,946684800);
 CREATE TABLE file_contents(node_id TEXT PRIMARY KEY NOT NULL REFERENCES nodes(id),content BLOB NOT NULL,size INTEGER NOT NULL,content_type TEXT NOT NULL,etag TEXT NOT NULL,CHECK(node_id<>'root'),CHECK(size BETWEEN 0 AND 67108864 AND length(content)=size),CHECK(octet_length(content_type) BETWEEN 1 AND 255),CHECK(octet_length(etag)=34)) STRICT;
 INSERT INTO file_contents VALUES('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',x'0001ff',3,'application/octet-stream','"00000000000000000000000000000000"');
 PRAGMA user_version=3;`)
	if err != nil || db.Close() != nil {
		t.Fatal("legacy fixture creation failed")
	}
	p := startServer(t, bin, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	code, h, data := fileRequest(t, c, f, "GET", "/dav/kept", nil, nil)
	if code != 200 || !bytes.Equal(data, []byte{0, 1, 255}) || h.Get("ETag") != `"00000000000000000000000000000000"` || h.Get("Last-Modified") != "Sat, 01 Jan 2000 00:00:00 GMT" {
		t.Fatal("v3 upgrade changed representation")
	}
	patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32CreationTime>offline preserved</z:Win32CreationTime></d:prop></d:set></d:propertyupdate>`
	code, h, _ = metadataPatch(t, c, f, "/dav/kept", strings.NewReader(patch), nil, "Win32CreationTime")
	if code != 207 {
		t.Fatal("v4 metadata unavailable")
	}
	tag := h.Get("ETag")
	p.stop()
	c.CloseIdleConnections()
	console, out, stderr, exit := privateConsoleCommand(t, t.TempDir(), bin, "reset", "password", "--config", f.config, "--username", "FixtureUser")
	if exit != 0 || out != "godav: password reset\n" {
		t.Fatal("v4 offline reset failed")
	}
	f.password = accountSecret(t, console, out, stderr)
	p = startServer(t, bin, f)
	defer p.stop()
	code, h, data = fileRequest(t, c, f, "GET", "/dav/Kept", nil, nil)
	if code != 200 || !bytes.Equal(data, []byte{0, 1, 255}) || h.Get("ETag") != tag {
		t.Fatal("offline account maintenance changed file")
	}
	code, _, data = fileRequest(t, c, f, "PROPFIND", "/dav/Kept", nil, map[string]string{"Depth": "0"})
	if code != 207 || !strings.Contains(string(data), "offline preserved") || !strings.Contains(string(data), "2000-01-01T00:00:00Z") {
		t.Fatal("offline maintenance lost metadata/creation date")
	}
}

func TestMetadataReservedMigrationConflictPreservesV1(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	path := filepath.Join(f.dir, "godav.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal("fixture open failed")
	}
	_, err = db.Exec(`CREATE TABLE node_metadata(unrelated TEXT); INSERT INTO node_metadata VALUES('fixture')`)
	if err != nil || db.Close() != nil {
		t.Fatal("fixture setup failed")
	}
	before := fixtureBytes(t, path)
	namespaceServerRefusal(t, bin, f)
	if !bytes.Equal(before, fixtureBytes(t, path)) {
		t.Fatal("metadata conflict partially migrated old state")
	}
}

func TestMetadataMigrationFailureRollsBackV3(t *testing.T) {
	bin := development(t)
	faulty := faultBuild(t, "godav_test_fail_metadata_migration")
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	c := fixtureClient(f)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/migration", strings.NewReader("preserved"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	tag := h.Get("ETag")
	c.CloseIdleConnections()
	p.stop()
	path := filepath.Join(f.dir, "godav.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal("fixture open failed")
	}
	_, err = db.Exec(`DROP TABLE node_metadata; PRAGMA user_version=3`)
	if err != nil || db.Close() != nil {
		t.Fatal("v3 fixture setup failed")
	}
	before := fixtureBytes(t, path)
	namespaceServerRefusal(t, faulty, f)
	if !bytes.Equal(before, fixtureBytes(t, path)) {
		t.Fatal("failed v3 migration changed state")
	}
	p = startServer(t, bin, f)
	defer p.stop()
	defer c.CloseIdleConnections()
	code, h, data := fileRequest(t, c, f, "GET", "/dav/migration", nil, nil)
	if code != 200 || string(data) != "preserved" || h.Get("ETag") != tag {
		t.Fatal("migration recovery changed bytes/tag")
	}
}

func TestMetadataXMLDeclarations(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	body := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:remove><d:prop><z:Win32CreationTime/></d:prop></d:remove></d:propertyupdate>`
	for _, prefix := range []string{`<?xml version="1.0"?><?xml version="1.0"?>`, ` <?xml version="1.0"?>`, `<?xml?>`, `<?xml version=1.0?>`} {
		code, _, _ := fileRequest(t, c, f, "PROPPATCH", "/dav/", strings.NewReader(prefix+body), nil)
		if code != 400 {
			t.Fatalf("invalid XML declaration got %d", code)
		}
	}
	code, _, _ := metadataPatch(t, c, f, "/dav/", strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>`+body), nil, "Win32CreationTime")
	if code != 207 {
		t.Fatal("valid declaration rejected")
	}
}

func TestMetadataRejectsUnboundNamespacesAtomically(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/namespaces", strings.NewReader("bytes"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	wrap := func(attrs, inside string) string {
		return `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:" ` + attrs + `>` + inside + `</d:propertyupdate>`
	}
	set := func(value string) string {
		return `<d:set><d:prop><z:Win32CreationTime>` + value + `</z:Win32CreationTime></d:prop></d:set>`
	}
	code, h, data := metadataPatch(t, c, f, "/dav/namespaces", strings.NewReader(wrap("", set("kept"))), nil, "Win32CreationTime")
	if code != 207 || !strings.Contains(string(data), "200 OK") {
		t.Fatal("metadata seed failed")
	}
	tag := h.Get("ETag")
	invalid := []string{
		wrap("", set("changed")+`<x:ignored/>`),
		wrap(`x:hint="bad"`, set("changed")),
		wrap(`xmlns:e="urn:extension"`, set("changed")+`<e:ignored><x:nested/></e:ignored>`),
		wrap(`xmlns:e="urn:extension"`, set("changed")+`<e:ignored><e:nested x:hint="bad"/></e:ignored>`),
		wrap(`xmlns:xml="urn:wrong"`, set("changed")),
		wrap(`xmlns:e="http://www.w3.org/XML/1998/namespace"`, set("changed")),
		wrap(`xmlns:xmlns="http://www.w3.org/2000/xmlns/"`, set("changed")),
		wrap(`xmlns:e="http://www.w3.org/2000/xmlns/"`, set("changed")),
		wrap(`xmlns:e=""`, set("changed")),
		wrap(`xmlns="http://www.w3.org/XML/1998/namespace"`, set("changed")),
		wrap(`xmlns:e="urn:extension"`, set("changed")+`<e:1invalid/>`),
	}
	for i, body := range invalid {
		code, _, _ = fileRequest(t, c, f, "PROPPATCH", "/dav/namespaces", strings.NewReader(body), nil)
		if code != 400 {
			t.Fatalf("namespace case %d got %d", i, code)
		}
		code, h, _ = fileRequest(t, c, f, "HEAD", "/dav/namespaces", nil, nil)
		if code != 200 || h.Get("ETag") != tag {
			t.Fatal("malformed namespace changed tag")
		}
		code, _, data = fileRequest(t, c, f, "PROPFIND", "/dav/namespaces", nil, map[string]string{"Depth": "0"})
		if code != 207 || !strings.Contains(string(data), ">kept<") {
			t.Fatal("malformed namespace changed property")
		}
	}
}

// Success means every requested expanded property succeeded, not just outer207.
func assertMetadataPatchOK(t *testing.T, code int, data []byte, names ...string) {
	t.Helper()
	if code != 207 {
		t.Fatalf("metadata patch status %d", code)
	}
	var doc metadataReply
	if xml.Unmarshal(data, &doc) != nil || len(doc.Responses) != 1 {
		t.Fatal("patch multistatus invalid")
	}
	want := map[xml.Name]int{}
	for _, name := range names {
		want[xml.Name{Space: "urn:schemas-microsoft-com:", Local: name}]++
	}
	for _, stat := range doc.Responses[0].Stats {
		if stat.Status != "HTTP/1.1 200 OK" || stat.Error != nil || len(stat.Prop.Values) == 0 {
			t.Fatal("patch contained a property failure")
		}
		for _, p := range stat.Prop.Values {
			if want[p.XMLName] == 0 || p.Text != "" {
				t.Fatal("patch property namespace/count/value differs")
			}
			want[p.XMLName]--
		}
	}
	for _, count := range want {
		if count != 0 {
			t.Fatal("patch omitted requested property")
		}
	}
}

type metadataReply struct {
	Responses []struct {
		Stats []struct {
			Status string    `xml:"DAV: status"`
			Error  *struct{} `xml:"DAV: error"`
			Prop   struct {
				Values []struct {
					XMLName xml.Name
					Text    string `xml:",chardata"`
				} `xml:",any"`
			} `xml:"DAV: prop"`
		} `xml:"DAV: propstat"`
	} `xml:"DAV: response"`
}

func metadataPatch(t *testing.T, c *http.Client, f serverFixture, path string, body io.Reader, headers map[string]string, names ...string) (int, http.Header, []byte) {
	t.Helper()
	code, h, data := fileRequest(t, c, f, "PROPPATCH", path, body, headers)
	assertMetadataPatchOK(t, code, data, names...)
	return code, h, data
}
func assertStoredMetadata(t *testing.T, c *http.Client, f serverFixture, path string, want map[string]string) {
	t.Helper()
	var body strings.Builder
	body.WriteString(`<d:propfind xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:prop>`)
	for name := range want {
		body.WriteString("<z:" + name + "/>")
	}
	body.WriteString(`</d:prop></d:propfind>`)
	code, _, data := fileRequest(t, c, f, "PROPFIND", path, strings.NewReader(body.String()), map[string]string{"Depth": "0"})
	var doc metadataReply
	if code != 207 || xml.Unmarshal(data, &doc) != nil || len(doc.Responses) != 1 {
		t.Fatal("metadata readback invalid")
	}
	seen := map[string]bool{}
	for _, stat := range doc.Responses[0].Stats {
		if stat.Status != "HTTP/1.1 200 OK" || stat.Error != nil {
			t.Fatal("stored metadata property failed")
		}
		for _, p := range stat.Prop.Values {
			value, ok := want[p.XMLName.Local]
			if p.XMLName.Space != "urn:schemas-microsoft-com:" || !ok || seen[p.XMLName.Local] || value != p.Text {
				t.Fatal("stored metadata QName/value differs")
			}
			seen[p.XMLName.Local] = true
		}
	}
	if len(seen) != len(want) {
		t.Fatal("stored metadata omitted property")
	}
}

func TestMetadataValidNamespaceScopesAndBounds(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	value := strings.Repeat("v", 4096)
	names := []string{"Win32CreationTime", "Win32LastModifiedTime", "Win32LastAccessTime", "Win32FileAttributes"}
	want := map[string]string{}
	var props strings.Builder
	for _, name := range names {
		props.WriteString("<z:" + name + ">" + value + "</z:" + name + ">")
		want[name] = value
	}
	prefix := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:" xmlns:e="urn:extension" xmlns:xml="http://www.w3.org/XML/1998/namespace"><e:ignored xmlns:z="urn:shadow"><z:bound xml:lang="en"/></e:ignored>`
	body := prefix + `<d:set><d:prop>` + props.String() + `</d:prop></d:set></d:propertyupdate>`
	metadataPatch(t, c, f, "/dav/", strings.NewReader(body), nil, names...)
	assertStoredMetadata(t, c, f, "/dav/", want)
	body = prefix + `<d:set><d:prop>` + props.String() + `<z:Win32CreationTime>x</z:Win32CreationTime></d:prop></d:set></d:propertyupdate>`
	code, _, _ := fileRequest(t, c, f, "PROPPATCH", "/dav/", strings.NewReader(body), nil)
	if code != 413 {
		t.Fatal("aggregate decoded value limit not enforced")
	}
	assertStoredMetadata(t, c, f, "/dav/", want)
	var operations strings.Builder
	var repeated []string
	for range 64 {
		operations.WriteString(`<d:remove><d:prop><z:Win32CreationTime/></d:prop></d:remove>`)
		repeated = append(repeated, "Win32CreationTime")
	}
	metadataPatch(t, c, f, "/dav/", strings.NewReader(prefix+operations.String()+`</d:propertyupdate>`), nil, repeated...)
}

func TestMetadataRemovePersists(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/remove", strings.NewReader("bytes"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	set := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32CreationTime>remove me</z:Win32CreationTime></d:prop></d:set></d:propertyupdate>`
	_, h, _ := metadataPatch(t, c, f, "/dav/remove", strings.NewReader(set), nil, "Win32CreationTime")
	oldTag := h.Get("ETag")
	remove := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:remove><d:prop><z:Win32CreationTime/></d:prop></d:remove></d:propertyupdate>`
	_, h, _ = metadataPatch(t, c, f, "/dav/remove", strings.NewReader(remove), nil, "Win32CreationTime")
	tag := h.Get("ETag")
	if tag == oldTag {
		t.Fatal("effective removal did not change tag")
	}
	p.stop()
	p = startServer(t, bin, f)
	defer p.stop()
	code, h, data := fileRequest(t, c, f, "GET", "/dav/remove", nil, nil)
	if code != 200 || h.Get("ETag") != tag || string(data) != "bytes" {
		t.Fatal("removed metadata changed representation after restart")
	}
	body := `<d:propfind xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:prop><z:Win32CreationTime/></d:prop></d:propfind>`
	code, _, data = fileRequest(t, c, f, "PROPFIND", "/dav/remove", strings.NewReader(body), map[string]string{"Depth": "0"})
	var doc metadataReply
	if code != 207 || xml.Unmarshal(data, &doc) != nil || len(doc.Responses) != 1 || len(doc.Responses[0].Stats) != 1 {
		t.Fatal("removed property response invalid")
	}
	stat := doc.Responses[0].Stats[0]
	if stat.Status != "HTTP/1.1 404 Not Found" || len(stat.Prop.Values) != 1 || stat.Prop.Values[0].XMLName != (xml.Name{Space: "urn:schemas-microsoft-com:", Local: "Win32CreationTime"}) {
		t.Fatal("removed property was synthesized or reappeared")
	}
}
