package cli_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func moveRequest(t *testing.T, c *http.Client, f serverFixture, source, destination string, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	h := map[string]string{"Destination": f.origin + destination}
	for k, v := range headers {
		h[k] = v
	}
	return fileRequest(t, c, f, "MOVE", source, nil, h)
}

func TestMoveConcurrentCollisionAndIndependentSiblings(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	for _, name := range []string{"a", "b", "c", "d"} {
		fileRequest(t, c, f, "PUT", "/dav/"+name, strings.NewReader(name), nil)
	}
	concurrent := func(sources, destinations []string) []int {
		start := make(chan struct{})
		statuses := make(chan int, len(sources))
		for i, source := range sources {
			req, _ := http.NewRequest("MOVE", f.origin+"/dav/"+source, nil)
			req.SetBasicAuth("FixtureUser", f.password)
			req.Header.Set("Destination", f.origin+"/dav/"+destinations[i])
			req.Header.Set("Overwrite", "F")
			go func() {
				<-start
				resp, err := retryAdmission(c, req)
				if err != nil {
					statuses <- 0
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				statuses <- resp.StatusCode
			}()
		}
		close(start)
		result := make([]int, 0, len(sources))
		for range sources {
			result = append(result, <-statuses)
		}
		slices.Sort(result)
		return result
	}
	if got := concurrent([]string{"a", "b"}, []string{"Same", "SAME"}); !slices.Equal(got, []int{201, 412}) {
		t.Fatalf("collision outcomes:%v", got)
	}
	if got := concurrent([]string{"c", "d"}, []string{"independent-c", "independent-d"}); !slices.Equal(got, []int{201, 201}) {
		t.Fatalf("sibling outcomes:%v", got)
	}
	code, _, winner := fileRequest(t, c, f, "GET", "/dav/same", nil, nil)
	if code != 200 || string(winner) != "a" && string(winner) != "b" {
		t.Fatal("race destination incomplete")
	}
	loser := "a"
	if string(winner) == "a" {
		loser = "b"
	}
	code, _, body := fileRequest(t, c, f, "GET", "/dav/"+loser, nil, nil)
	if code != 200 || string(body) != loser {
		t.Fatal("race lost losing source")
	}
	for _, name := range []string{"c", "d"} {
		code, _, body := fileRequest(t, c, f, "GET", "/dav/independent-"+name, nil, nil)
		if code != 200 || string(body) != name {
			t.Fatal("sibling content differs")
		}
	}
}

func TestMoveDescendantNamespaceBoundsAndKinds(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	path := "/dav"
	for i := 0; i < 31; i++ {
		path += "/n"
		code, _, _ := fileRequest(t, c, f, "MKCOL", path, nil, nil)
		if code != 201 {
			t.Fatalf("deep fixture:%d", code)
		}
	}
	fileRequest(t, c, f, "MKCOL", "/dav/source", nil, nil)
	fileRequest(t, c, f, "PUT", "/dav/source/child", strings.NewReader("deep child"), nil)
	code, _, body := moveRequest(t, c, f, "/dav/source", path+"/moved", nil)
	if code != 207 || !strings.Contains(string(body), "400 Bad Request") || !strings.Contains(string(body), "/moved/child") {
		t.Fatalf("descendant depth bound:%d", code)
	}
	code, _, body = fileRequest(t, c, f, "GET", "/dav/source/child", nil, nil)
	if code != 200 || string(body) != "deep child" {
		t.Fatal("depth rejection changed source")
	}
	for _, parent := range []string{"First", "Second"} {
		fileRequest(t, c, f, "MKCOL", "/dav/"+parent, nil, nil)
	}
	fileRequest(t, c, f, "PUT", "/dav/file", strings.NewReader("file replaces folder"), nil)
	code, _, _ = moveRequest(t, c, f, "/dav/file", "/dav/First", nil)
	if code != 204 {
		t.Fatalf("file replaces collection:%d", code)
	}
	fileRequest(t, c, f, "PUT", "/dav/other", strings.NewReader("old file"), nil)
	code, _, _ = moveRequest(t, c, f, "/dav/Second", "/dav/other/", nil)
	if code != 400 {
		t.Fatalf("file destination slash:%d", code)
	}
	code, _, _ = moveRequest(t, c, f, "/dav/Second", "/dav/other", nil)
	if code != 204 {
		t.Fatalf("collection replaces file:%d", code)
	}
	if got := namespaceListing(t, c, f, "/dav/other", "0"); !slices.Equal(got, []string{"/dav/other/"}) {
		t.Fatal("replacement collection kind differs")
	}
	// A new longer spelling can invalidate descendants even at the same depth.
	long := strings.Repeat("a", 250)
	prefix := "/dav"
	for i := 0; i < 15; i++ {
		prefix += "/" + fmt.Sprintf("%02d", i) + long
		code, _, _ := fileRequest(t, c, f, "MKCOL", prefix, nil, nil)
		if code != 201 {
			t.Fatalf("long fixture:%d", code)
		}
	}
	fileRequest(t, c, f, "MKCOL", prefix+"/s", nil, nil)
	fileRequest(t, c, f, "PUT", prefix+"/s/"+strings.Repeat("b", 200), strings.NewReader("preserved"), nil)
	code, _, _ = moveRequest(t, c, f, prefix+"/s", prefix+"/"+strings.Repeat("c", 101), nil)
	if code != 207 {
		t.Fatalf("descendant byte bound:%d", code)
	}
}

func TestMoveProtocolValidationPreservesState(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	fileRequest(t, c, f, "MKCOL", "/dav/folder", nil, nil)
	fileRequest(t, c, f, "MKCOL", "/dav/folder/child", nil, nil)
	fileRequest(t, c, f, "PUT", "/dav/file", strings.NewReader("unchanged"), nil)
	for _, tc := range []struct {
		name, source, destination string
		headers                   map[string]string
		status                    int
	}{
		{"exact noop", "/dav/file", "/dav/file", nil, 403},
		{"case collision F", "/dav/file", "/dav/FILE", map[string]string{"Overwrite": "F"}, 412},
		{"collection depth0", "/dav/folder", "/dav/new", map[string]string{"Depth": "0"}, 400},
		{"root source", "/dav/", "/dav/new", nil, 403},
		{"root destination", "/dav/file", "/dav/", nil, 403},
		{"descendant", "/dav/folder", "/dav/folder/child/moved", nil, 403},
		{"ancestor", "/dav/folder/child", "/dav/folder", nil, 403},
		{"missing parent", "/dav/file", "/dav/missing/file", nil, 409},
		{"file parent", "/dav/folder", "/dav/file/child", nil, 409},
		{"missing source", "/dav/missing", "/dav/new", nil, 404},
		{"source slash", "/dav/file/", "/dav/new", nil, 400},
		{"destination slash", "/dav/file", "/dav/new/", nil, 400},
		{"bad depth", "/dav/file", "/dav/new", map[string]string{"Depth": "1"}, 400},
		{"empty depth", "/dav/file", "/dav/new", map[string]string{"Depth": ""}, 400},
		{"bad overwrite", "/dav/file", "/dav/new", map[string]string{"Overwrite": "yes"}, 400},
		{"empty overwrite", "/dav/file", "/dav/new", map[string]string{"Overwrite": ""}, 400},
		{"outside mount", "/dav/file", "/other/new", nil, 403},
		{"query destination", "/dav/file", "/dav/new?secret=redact", nil, 400},
		{"false validator", "/dav/file", "/dav/new", map[string]string{"If-Match": "\"different\""}, 412},
		{"wrong lock field", "/dav/file", "/dav/new", map[string]string{"Lock-Token": "<urn:uuid:ordinary-fixture>"}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, _ := moveRequest(t, c, f, tc.source, tc.destination, tc.headers)
			if code != tc.status {
				t.Fatalf("got%d want%d", code, tc.status)
			}
		})
	}
	for _, tc := range []struct {
		headers http.Header
		body    string
		status  int
	}{
		{http.Header{}, "", 400},
		{http.Header{"Destination": {f.origin + "/dav/a", f.origin + "/dav/b"}}, "", 400},
		{http.Header{"Destination": {f.origin + "/dav/new"}, "Depth": {"0", "0"}}, "", 400},
		{http.Header{"Destination": {f.origin + "/dav/new"}, "Overwrite": {"T", "T"}}, "", 400},
		{http.Header{"Destination": {"/dav/new"}}, "", 400},
		{http.Header{"Destination": {"https://localhost:1/dav/new"}}, "", 502},
		{http.Header{"Destination": {f.origin + "/dav/new"}}, "unsupported body", 415},
	} {
		req, _ := http.NewRequest("MOVE", f.origin+"/dav/file", strings.NewReader(tc.body))
		req.Header = tc.headers
		req.SetBasicAuth("FixtureUser", f.password)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal("request failed")
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("header/body status%d want%d", resp.StatusCode, tc.status)
		}
	}
	code, _, body := fileRequest(t, c, f, "GET", "/dav/file", nil, nil)
	if code != 200 || string(body) != "unchanged" {
		t.Fatal("validation changed source")
	}
	if got := namespaceListing(t, c, f, "/dav/", "1"); !slices.Equal(got, []string{"/dav/", "/dav/file", "/dav/folder/"}) {
		t.Fatalf("validation changed namespace:%v", got)
	}
	code, _, _ = moveRequest(t, c, f, "/dav/file", "/dav/"+url.PathEscape("Résumé %file"), map[string]string{"Depth": "InFiNiTy", "Overwrite": "t"})
	if code != 201 {
		t.Fatalf("encoded destination:%d", code)
	}
	code, _, body = fileRequest(t, c, f, "GET", "/dav/"+url.PathEscape("Résumé %file"), nil, nil)
	if code != 200 || string(body) != "unchanged" {
		t.Fatal("encoded readback differs")
	}
}

func TestMoveFileDepthZeroAndCaseRename(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/original", strings.NewReader("stable content"), nil)
	if code != 201 {
		t.Fatalf("seed: %d", code)
	}
	_, before, _ := fileRequest(t, c, f, "HEAD", "/dav/original", nil, nil)
	code, _, _ = moveRequest(t, c, f, "/dav/original", "/dav/renamed", map[string]string{"Depth": "0"})
	if code != 201 {
		t.Fatalf("file MOVE Depth 0: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "GET", "/dav/original", nil, nil)
	if code != 404 {
		t.Fatalf("old mapping: %d", code)
	}
	code, after, body := fileRequest(t, c, f, "GET", "/dav/renamed", nil, nil)
	if code != 200 || string(body) != "stable content" || before.Get("ETag") != after.Get("ETag") {
		t.Fatal("move changed content or validator")
	}
	code, _, _ = moveRequest(t, c, f, "/dav/renamed", "/dav/Renamed", map[string]string{"Depth": "0"})
	if code != 204 {
		t.Fatalf("case rename: %d", code)
	}
	hrefs := namespaceListing(t, c, f, "/dav/", "1")
	if len(hrefs) != 2 || hrefs[1] != "/dav/Renamed" {
		t.Fatalf("case spelling: %v", hrefs)
	}
}

func TestMoveAtomicCollectionReplacement(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	for _, path := range []string{"/dav/Source", "/dav/Source/Sub", "/dav/Destination"} {
		code, _, _ := fileRequest(t, c, f, "MKCOL", path, nil, nil)
		if code != 201 {
			t.Fatalf("seed collection: %d", code)
		}
	}
	for path, data := range map[string]string{"/dav/Source/Sub/file": "source bytes", "/dav/Destination/old": "destination bytes"} {
		code, _, _ := fileRequest(t, c, f, "PUT", path, strings.NewReader(data), nil)
		if code != 201 {
			t.Fatalf("seed file: %d", code)
		}
	}
	code, _, _ := moveRequest(t, c, f, "/dav/source", "/dav/destination", map[string]string{"Overwrite": "F"})
	if code != 412 {
		t.Fatalf("Overwrite F: %d", code)
	}
	for path, data := range map[string]string{"/dav/Source/Sub/file": "source bytes", "/dav/Destination/old": "destination bytes"} {
		code, _, body := fileRequest(t, c, f, "GET", path, nil, nil)
		if code != 200 || string(body) != data {
			t.Fatal("collision changed a side")
		}
	}
	code, _, _ = moveRequest(t, c, f, "/dav/source", "/dav/destination", map[string]string{"Depth": "infinity"})
	if code != 204 {
		t.Fatalf("collection replace: %d", code)
	}
	code, _, body := fileRequest(t, c, f, "GET", "/dav/destination/Sub/file", nil, nil)
	if code != 200 || string(body) != "source bytes" {
		t.Fatal("moved subtree differs")
	}
	for _, path := range []string{"/dav/Source", "/dav/destination/old"} {
		code, _, _ := fileRequest(t, c, f, "PROPFIND", path, nil, map[string]string{"Depth": "0"})
		if code != 404 {
			t.Fatalf("removed mapping %s: %d", path, code)
		}
	}
}

func TestMoveLocksSubtreeAndDestinationScope(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	fileRequest(t, c, f, "MKCOL", "/dav/Source", nil, nil)
	fileRequest(t, c, f, "PUT", "/dav/Source/child", strings.NewReader("child"), nil)
	token := acquireFixtureLock(t, c, f, "/dav/Source/child", "0", "Second-60")
	code, _, body := moveRequest(t, c, f, "/dav/Source", "/dav/Renamed", nil)
	if code != 207 || !strings.Contains(string(body), "423 Locked") || !strings.Contains(string(body), "/dav/Source/child") || strings.Contains(string(body), "424") {
		t.Fatalf("descendant lock response:%d %s", code, body)
	}
	condition := map[string]string{"If": "<" + f.origin + "/dav/Source/child> (" + token + ")"}
	code, _, _ = moveRequest(t, c, f, "/dav/Source", "/dav/Renamed", condition)
	if code != 201 {
		t.Fatalf("authorized subtree move:%d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/Renamed/child", strings.NewReader("new bytes"), nil)
	if code != 204 {
		t.Fatalf("source lease followed MOVE:%d", code)
	}
	code, _, _ = fileRequest(t, c, f, "GET", "/dav/Renamed/child", nil, map[string]string{"If": "(" + token + ")"})
	if code != 412 {
		t.Fatalf("old token still true:%d", code)
	}
	fileRequest(t, c, f, "PUT", "/dav/from", strings.NewReader("source"), nil)
	fileRequest(t, c, f, "PUT", "/dav/to", strings.NewReader("destination"), nil)
	sourceLock := acquireFixtureLock(t, c, f, "/dav/from", "0", "Second-60")
	destLock := acquireFixtureLock(t, c, f, "/dav/to", "0", "Second-60")
	condition = map[string]string{"If": "<" + f.origin + "/dav/from> (" + sourceLock + ") <" + f.origin + "/dav/to> (" + destLock + ")"}
	code, _, _ = moveRequest(t, c, f, "/dav/from", "/dav/to", condition)
	if code != 204 {
		t.Fatalf("two-token replacement:%d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/to", strings.NewReader("must not publish"), nil)
	if code != 423 {
		t.Fatalf("destination lease disappeared:%d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/to", strings.NewReader("authorized"), map[string]string{"If": "(" + destLock + ")"})
	if code != 204 {
		t.Fatalf("destination scope not inherited:%d", code)
	}
	code, _, _ = moveRequest(t, c, f, "/dav/to", "/dav/TO", map[string]string{"If": "(" + destLock + ")"})
	if code != 204 {
		t.Fatalf("locked case rename:%d", code)
	}
	code, _, body = fileRequest(t, c, f, "PROPFIND", "/dav/TO", nil, map[string]string{"Depth": "0"})
	if code != 207 || !strings.Contains(string(body), "/dav/TO") || !strings.Contains(string(body), strings.Trim(destLock, "<>")) {
		t.Fatal("case rename lost lease spelling")
	}
	code, _, _ = fileRequest(t, c, f, "UNLOCK", "/dav/TO", nil, map[string]string{"Lock-Token": destLock})
	if code != 204 {
		t.Fatalf("renamed unlock:%d", code)
	}
}
