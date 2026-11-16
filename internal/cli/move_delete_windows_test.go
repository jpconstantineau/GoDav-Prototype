package cli_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMutationsMoveThenDeleteWithInheritedLock(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	fileRequest(t, c, f, "PUT", "/dav/source", strings.NewReader("committed"), nil)
	fileRequest(t, c, f, "MKCOL", "/dav/parent", nil, nil)
	source := acquireFixtureLock(t, c, f, "/dav/source", "0", "Second-60")
	parent := acquireFixtureLock(t, c, f, "/dav/parent", "infinity", "Second-60")
	condition := map[string]string{"If": "<" + f.origin + "/dav/source> (" + source + ") <" + f.origin + "/dav/parent> (" + parent + ")"}
	code, _, _ := moveRequest(t, c, f, "/dav/source", "/dav/parent/moved", condition)
	if code != 201 {
		t.Fatalf("MOVE:%d", code)
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/source", nil, nil)
	if code != 404 {
		t.Fatalf("old mapping DELETE:%d", code)
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/parent/moved", nil, nil)
	if code != 423 {
		t.Fatalf("inherited lock DELETE:%d", code)
	}
	code, _, body := fileRequest(t, c, f, "GET", "/dav/parent/moved", nil, nil)
	if code != 200 || string(body) != "committed" {
		t.Fatal("refused DELETE changed moved bytes")
	}
	code, _, _ = fileRequest(t, c, f, "COPY", "/dav/parent/moved", nil, map[string]string{"Destination": f.origin + "/dav/copy"})
	if code != 405 {
		t.Fatalf("COPY enabled:%d", code)
	}
	code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/parent", nil, map[string]string{"If": "(" + parent + ")"})
	if code != 204 {
		t.Fatalf("authorized subtree DELETE:%d", code)
	}
	code, _, _ = fileRequest(t, c, f, "MKCOL", "/dav/parent", nil, nil)
	if code != 201 {
		t.Fatal("deleted lease prevented recreation")
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/parent/fresh", strings.NewReader("fresh"), nil)
	if code != 201 {
		t.Fatal("combined operations leaked lock or store ownership")
	}
}

func TestMutationsMoveDeleteCompeteAtomically(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/source", strings.NewReader("whole source"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	start := make(chan struct{})
	results := make(chan struct {
		method string
		status int
	}, 2)
	for _, method := range []string{"MOVE", "DELETE"} {
		req, _ := http.NewRequest(method, f.origin+"/dav/source", nil)
		req.SetBasicAuth("FixtureUser", f.password)
		req.Header.Set("If-Match", h.Get("ETag"))
		if method == "MOVE" {
			req.Header.Set("Destination", f.origin+"/dav/moved")
		}
		go func() {
			<-start
			resp, err := retryAdmission(c, req)
			status := 0
			if err == nil {
				status = resp.StatusCode
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			results <- struct {
				method string
				status int
			}{method, status}
		}()
	}
	close(start)
	statuses := map[string]int{}
	for range 2 {
		r := <-results
		statuses[r.method] = r.status
	}
	if !(statuses["MOVE"] == 201 && statuses["DELETE"] == 404 || statuses["MOVE"] == 404 && statuses["DELETE"] == 204) {
		t.Fatalf("competing outcomes:%v", statuses)
	}
	code, _, _ = fileRequest(t, c, f, "HEAD", "/dav/source", nil, nil)
	if code != 404 {
		t.Fatal("source mapping survived")
	}
	code, _, body := fileRequest(t, c, f, "GET", "/dav/moved", nil, nil)
	if statuses["MOVE"] == 201 {
		if code != 200 || string(body) != "whole source" {
			t.Fatal("winning MOVE incomplete")
		}
	} else if code != 404 {
		t.Fatal("losing MOVE published")
	}
}
