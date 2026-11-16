package cli_test

import (
	"slices"
	"strings"
	"testing"
)

func TestDeleteFileAndTreeReopen(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	for _, path := range []string{"/dav/Tree/", "/dav/Tree/Nested/"} {
		code, _, _ := fileRequest(t, c, f, "MKCOL", path, nil, nil)
		if code != 201 {
			t.Fatalf("seed collection %s: %d", path, code)
		}
	}
	for _, path := range []string{"/dav/Tree/Nested/leaf", "/dav/Tree/file", "/dav/sibling"} {
		code, _, _ := fileRequest(t, c, f, "PUT", path, strings.NewReader("known bytes"), nil)
		if code != 201 {
			t.Fatalf("seed file %s: %d", path, code)
		}
	}
	code, _, body := fileRequest(t, c, f, "DELETE", "/dav/tree/FILE", nil, nil)
	if code != 204 || len(body) != 0 {
		t.Fatalf("file DELETE: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/tree/file", nil, nil)
	if code != 404 {
		t.Fatalf("missing DELETE: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/TREE/", nil, nil)
	if code != 204 {
		t.Fatalf("tree DELETE: %d", code)
	}
	assertAbsent := func() {
		t.Helper()
		for _, path := range []string{"/dav/Tree/", "/dav/Tree/Nested/", "/dav/Tree/Nested/leaf", "/dav/Tree/file"} {
			for _, method := range []string{"GET", "HEAD", "PROPFIND"} {
				code, _, _ := fileRequest(t, c, f, method, path, nil, map[string]string{"Depth": "0"})
				if code != 404 {
					t.Fatalf("deleted %s %s: %d", method, path, code)
				}
			}
		}
		if got := namespaceListing(t, c, f, "/dav/", "1"); !slices.Equal(got, []string{"/dav/", "/dav/sibling"}) {
			t.Fatalf("post-delete listing: %v", got)
		}
		code, _, data := fileRequest(t, c, f, "GET", "/dav/sibling", nil, nil)
		if code != 200 || string(data) != "known bytes" {
			t.Fatal("unrelated sibling changed")
		}
	}
	assertAbsent()
	c.CloseIdleConnections()
	p.stop()
	p = startServer(t, bin, f)
	defer p.stop()
	assertAbsent()
}

func TestDeleteConditionsDepthAndRoot(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, tag, _ := fileRequest(t, c, f, "PUT", "/dav/kept", strings.NewReader("unchanged"), nil)
	if code != 201 {
		t.Fatal("seed")
	}
	code, _, _ = fileRequest(t, c, f, "MKCOL", "/dav/parent/", nil, nil)
	if code != 201 {
		t.Fatal("seed parent")
	}
	cases := []struct {
		path, body string
		headers    map[string]string
		want       int
	}{
		{"/dav/", "", nil, 405}, {"/dav", "", nil, 405},
		{"/dav/kept/", "", nil, 400},
		{"/dav/kept", "body", nil, 415},
		{"/dav/parent/", "", map[string]string{"Depth": "0"}, 400},
		{"/dav/parent/", "", map[string]string{"Depth": "1"}, 400},
		{"/dav/kept", "", map[string]string{"Depth": "other"}, 400},
		{"/dav/kept", "", map[string]string{"If-Match": `"different"`}, 412},
		{"/dav/kept", "", map[string]string{"If-None-Match": tag.Get("ETag")}, 412},
		{"/dav/kept", "", map[string]string{"If-Unmodified-Since": "Sat, 01 Jan 2000 00:00:00 GMT"}, 412},
		{"/dav/kept", "", map[string]string{"If": "(<urn:uuid:absent>)"}, 412},
		{"/dav/kept", "", map[string]string{"Lock-Token": "<urn:uuid:absent>"}, 400},
	}
	for _, tc := range cases {
		code, _, _ := fileRequest(t, c, f, "DELETE", tc.path, strings.NewReader(tc.body), tc.headers)
		if code != tc.want {
			t.Fatalf("DELETE %s headers%v: got%d want%d", tc.path, tc.headers, code, tc.want)
		}
	}
	code, after, data := fileRequest(t, c, f, "GET", "/dav/kept", nil, nil)
	if code != 200 || string(data) != "unchanged" || after.Get("ETag") != tag.Get("ETag") {
		t.Fatal("refusals changed bytes or validator")
	}
	for _, path := range []string{"/dav/", "/dav/kept"} {
		code, h, _ := fileRequest(t, c, f, "OPTIONS", path, nil, nil)
		if code != 200 || strings.Contains(h.Get("Allow"), "DELETE") != (path != "/dav/") {
			t.Fatalf("Allow at %s: %s", path, h.Get("Allow"))
		}
	}
	for _, depth := range []string{"0", "1", "InFiNiTy"} {
		code, _, _ := fileRequest(t, c, f, "PUT", "/dav/depth", strings.NewReader("x"), nil)
		if code != 201 {
			t.Fatal("depth seed")
		}
		code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/depth", nil, map[string]string{"Depth": depth})
		if code != 204 {
			t.Fatalf("file depth%s: %d", depth, code)
		}
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/kept", nil, map[string]string{"If-Match": tag.Get("ETag")})
	if code != 204 {
		t.Fatalf("matching condition: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/parent/", nil, map[string]string{"Depth": "INFINITY"})
	if code != 204 {
		t.Fatalf("collection infinity: %d", code)
	}
}

func TestDeleteLocksAtomicTreeAndRecreation(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	for _, path := range []string{"/dav/Parent/", "/dav/Parent/Tree/", "/dav/Parent/Tree/Nested/"} {
		code, _, _ := fileRequest(t, c, f, "MKCOL", path, nil, nil)
		if code != 201 {
			t.Fatal("seed collection")
		}
	}
	for _, path := range []string{"/dav/Parent/Tree/Nested/child", "/dav/Parent/sibling"} {
		code, _, _ := fileRequest(t, c, f, "PUT", path, strings.NewReader("original"), nil)
		if code != 201 {
			t.Fatal("seed file")
		}
	}
	patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32FileAttributes>20</z:Win32FileAttributes></d:prop></d:set></d:propertyupdate>`
	code, _, _ := metadataPatch(t, c, f, "/dav/Parent/Tree/Nested/child", strings.NewReader(patch), nil, "Win32FileAttributes")
	if code != 207 {
		t.Fatal("metadata seed")
	}
	parent := acquireFixtureLock(t, c, f, "/dav/Parent/", "0", "Second-60")
	child := acquireFixtureLock(t, c, f, "/dav/Parent/Tree/Nested/child", "0", "Second-60")
	sibling := acquireFixtureLock(t, c, f, "/dav/Parent/sibling", "0", "Second-60")
	code, _, body := fileRequest(t, c, f, "DELETE", "/dav/parent/tree/", nil, map[string]string{"If": "</dav/Parent/> (" + parent + ")"})
	if code != 423 || !strings.Contains(string(body), "/dav/Parent/Tree/Nested/child") {
		t.Fatalf("descendant lock: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/Parent/Tree/", nil, map[string]string{"If": "</dav/Parent/Tree/Nested/child> (" + child + ")"})
	if code != 423 {
		t.Fatalf("parent membership: %d", code)
	}
	assertStoredMetadata(t, c, f, "/dav/Parent/Tree/Nested/child", map[string]string{"Win32FileAttributes": "20"})
	code, _, body = fileRequest(t, c, f, "GET", "/dav/Parent/Tree/Nested/child", nil, nil)
	if code != 200 || string(body) != "original" {
		t.Fatal("locked tree partially removed")
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/parent/tree/", nil, map[string]string{"If": "</dav/Parent/> (" + parent + ") </dav/Parent/Tree/Nested/child> (" + child + ")"})
	if code != 204 {
		t.Fatalf("authorized tree: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "MKCOL", "/dav/Parent/Tree/", nil, nil)
	if code != 423 {
		t.Fatalf("ancestor lease removed: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "MKCOL", "/dav/Parent/Tree/", nil, map[string]string{"If": "</dav/Parent/> (" + parent + ")"})
	if code != 201 {
		t.Fatal("authorized recreation")
	}
	fileRequest(t, c, f, "MKCOL", "/dav/Parent/Tree/Nested/", nil, nil)
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/Parent/Tree/Nested/child", strings.NewReader("new"), nil)
	if code != 201 {
		t.Fatalf("deleted lease still blocks path: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/Parent/Tree/Nested/child", nil, map[string]string{"If": "(" + child + ")"})
	if code != 412 {
		t.Fatalf("deleted lease became true: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/Parent/sibling", strings.NewReader("bad"), nil)
	if code != 423 {
		t.Fatalf("sibling lease removed: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "UNLOCK", "/dav/Parent/sibling", nil, map[string]string{"Lock-Token": sibling})
	if code != 204 {
		t.Fatal("sibling authority changed")
	}
}

func TestDeleteFailedPublicationPreservesTreeAndLease(t *testing.T) {
	normal := development(t)
	failing := faultBuild(t, "godav_test_fail_delete_commit")
	f := newServerFixture(t, normal)
	p := startServer(t, failing, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	fileRequest(t, c, f, "MKCOL", "/dav/tree/", nil, nil)
	code, head, _ := fileRequest(t, c, f, "PUT", "/dav/tree/file", strings.NewReader("prior content"), nil)
	if code != 201 {
		t.Fatal("seed")
	}
	token := acquireFixtureLock(t, c, f, "/dav/tree/", "infinity", "Second-60")
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/tree/", nil, map[string]string{"If": "(" + token + ")"})
	if code != 500 {
		t.Fatalf("synthetic publication failure: %d", code)
	}
	code, after, data := fileRequest(t, c, f, "GET", "/dav/tree/file", nil, nil)
	if code != 200 || string(data) != "prior content" || after.Get("ETag") != head.Get("ETag") {
		t.Fatal("failed deletion changed tree")
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/tree/file", strings.NewReader("bad"), nil)
	if code != 423 {
		t.Fatalf("failure removed lease: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/unrelated", strings.NewReader("healthy"), nil)
	if code != 201 {
		t.Fatalf("failure poisoned store: %d", code)
	}
	c.CloseIdleConnections()
	p.stop()
	assertLifecycleLockLogs(t, p, token)
	p = startServer(t, normal, f)
	defer p.stop()
	code, after, data = fileRequest(t, c, f, "GET", "/dav/tree/file", nil, nil)
	if code != 200 || string(data) != "prior content" || after.Get("ETag") != head.Get("ETag") {
		t.Fatal("rollback not preserved on reopen")
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/tree/", nil, nil)
	if code != 204 {
		t.Fatalf("healthy deletion after reopen: %d", code)
	}
}
