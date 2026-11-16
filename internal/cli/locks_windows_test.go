package cli_test

import (
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLocksConcurrentCreation(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	ready := make(chan struct{})
	results := make(chan int, 2)
	for _, path := range []string{"/dav/concurrent", "/dav/CONCURRENT"} {
		req, _ := http.NewRequest("LOCK", f.origin+path, strings.NewReader(lockRequestXML))
		req.SetBasicAuth("FixtureUser", f.password)
		req.Header.Set("Depth", "0")
		go func() {
			<-ready
			resp, err := c.Do(req)
			if err != nil {
				results <- 0
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			results <- resp.StatusCode
		}()
	}
	close(ready)
	a, b := <-results, <-results
	if !(a == 201 && b == 423 || a == 423 && b == 201) {
		t.Fatalf("competing creates: %d %d", a, b)
	}
	code, _, data := fileRequest(t, c, f, "GET", "/dav/concurrent", nil, nil)
	if code != 200 || len(data) != 0 {
		t.Fatal("concurrent lock placeholder differs")
	}
}

const lockRequestXML = `<d:lockinfo xmlns:d="DAV:"><d:lockscope><d:exclusive/></d:lockscope><d:locktype><d:write/></d:locktype><d:owner><d:href>synthetic-owner</d:href></d:owner></d:lockinfo>`

func acquireFixtureLock(t *testing.T, c *http.Client, f serverFixture, path, depth, timeout string) string {
	t.Helper()
	code, h, _ := fileRequest(t, c, f, "LOCK", path, strings.NewReader(lockRequestXML), map[string]string{"Depth": depth, "Timeout": timeout})
	if code != 200 && code != 201 {
		t.Fatalf("fixture LOCK %s: %d", path, code)
	}
	return h.Get("Lock-Token")
}

func TestLocksCollectionScopeAndDiscovery(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	fileRequest(t, c, f, "MKCOL", "/dav/folder/", nil, nil)
	fileRequest(t, c, f, "PUT", "/dav/folder/child", strings.NewReader("old"), nil)
	child := acquireFixtureLock(t, c, f, "/dav/folder/child", "0", "Second-60")
	code, _, body := fileRequest(t, c, f, "LOCK", "/dav/folder/", strings.NewReader(lockRequestXML), map[string]string{"Depth": "infinity"})
	if code != 207 || !strings.Contains(string(body), "423 Locked") || !strings.Contains(string(body), "/dav/folder/child") {
		t.Fatalf("descendant conflict response: %d", code)
	}
	fileRequest(t, c, f, "UNLOCK", "/dav/folder/child", nil, map[string]string{"Lock-Token": child})
	parent := acquireFixtureLock(t, c, f, "/dav/folder/", "0", "Second-60")
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/folder/child", strings.NewReader("replacement"), nil)
	if code != 204 {
		t.Fatalf("depth-zero blocked existing child content: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/folder/new", strings.NewReader("new"), nil)
	if code != 423 {
		t.Fatalf("locked membership accepted: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/folder/new", strings.NewReader("new"), map[string]string{"If": "</dav/folder/> (" + parent + ")"})
	if code != 201 {
		t.Fatalf("authorized membership failed: %d", code)
	}
	code, _, body = fileRequest(t, c, f, "PROPFIND", "/dav/folder/", strings.NewReader(`<d:propfind xmlns:d="DAV:"><d:prop><d:supportedlock/><d:lockdiscovery/></d:prop></d:propfind>`), map[string]string{"Depth": "0"})
	if code != 207 || !strings.Contains(string(body), "lockentry") || !strings.Contains(string(body), strings.Trim(parent, "<>")) || strings.Contains(string(body), "404 Not Found") {
		t.Fatal("active discovery missing")
	}
	fileRequest(t, c, f, "UNLOCK", "/dav/folder/", nil, map[string]string{"Lock-Token": parent})
	parent = acquireFixtureLock(t, c, f, "/dav/folder/", "infinity", "Second-60")
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/folder/child", strings.NewReader("conflict"), nil)
	if code != 423 {
		t.Fatalf("infinite scope lost: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/folder/child", strings.NewReader("authorized"), map[string]string{"If": "(" + parent + ")"})
	if code != 204 {
		t.Fatalf("inherited token failed: %d", code)
	}
}

func TestLocksExplorerSequence(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/copy.txt", strings.NewReader(""), nil)
	if code != 201 {
		t.Fatalf("empty PUT: %d", code)
	}
	code, h, body := fileRequest(t, c, f, "LOCK", "/dav/copy.txt", strings.NewReader(lockRequestXML), map[string]string{"Depth": "0", "Timeout": "Second-60"})
	if code != 200 {
		t.Fatalf("LOCK: %d", code)
	}
	token := h.Get("Lock-Token")
	var response struct {
		Discovery struct {
			Lock struct {
				Token struct {
					Href string `xml:"DAV: href"`
				} `xml:"DAV: locktoken"`
			} `xml:"DAV: activelock"`
		} `xml:"DAV: lockdiscovery"`
	}
	if xml.Unmarshal(body, &response) != nil || token != "<"+response.Discovery.Lock.Token.Href+">" || !strings.HasPrefix(token, "<urn:uuid:") {
		t.Fatal("invalid lock token or XML")
	}
	condition := map[string]string{"If": "(" + token + ")"}
	patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32FileAttributes>20</z:Win32FileAttributes></d:prop></d:set></d:propertyupdate>`
	code, _, _ = metadataPatch(t, c, f, "/dav/copy.txt", strings.NewReader(patch), condition, "Win32FileAttributes")
	if code != 207 {
		t.Fatalf("metadata: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/copy.txt", strings.NewReader("complete bytes"), condition)
	if code != 204 {
		t.Fatalf("body PUT: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/COPY.txt", strings.NewReader("conflict"), nil)
	if code != 423 {
		t.Fatalf("conflicting writer: %d", code)
	}
	code, _, body = fileRequest(t, c, f, "GET", "/dav/copy.txt", nil, nil)
	if code != 200 || string(body) != "complete bytes" {
		t.Fatal("readback mismatch")
	}
	assertStoredMetadata(t, c, f, "/dav/copy.txt", map[string]string{"Win32FileAttributes": "20"})
	code, _, _ = fileRequest(t, c, f, "UNLOCK", "/dav/copy.txt", nil, map[string]string{"Lock-Token": token})
	if code != 204 {
		t.Fatalf("UNLOCK: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/copy.txt", strings.NewReader("direct save"), nil)
	if code != 204 {
		t.Fatalf("direct save: %d", code)
	}
}
