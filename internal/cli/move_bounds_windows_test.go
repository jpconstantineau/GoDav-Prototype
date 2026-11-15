package cli_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMoveLastModifiedRepresentationTransitions(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	for path, data := range map[string]string{"/dav/source": "source bytes", "/dav/destination": "old destination bytes"} {
		code, _, _ := fileRequest(t, c, f, "PUT", path, strings.NewReader(data), nil)
		if code != 201 {
			t.Fatal("timestamp seed failed")
		}
	}
	c.CloseIdleConnections()
	p.stop()
	// A valid stopped-fixture timestamp creates an independent old-state
	// premise. No sleep, same-second race, private acceptance query or clock edit.
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "godav.sqlite"))
	if err != nil {
		t.Fatal("timestamp fixture open")
	}
	_, err = db.Exec(`UPDATE nodes SET modified_at=946684800 WHERE name IN ('source','destination')`)
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatal("timestamp fixture write")
	}
	p = startServer(t, bin, f)
	defer p.stop()
	for _, path := range []string{"/dav/source", "/dav/destination"} {
		code, h, _ := fileRequest(t, c, f, "HEAD", path, nil, nil)
		if code != 200 || h.Get("Last-Modified") != "Sat, 01 Jan 2000 00:00:00 GMT" {
			t.Fatal("public old timestamp premise differs")
		}
	}
	code, _, _ := moveRequest(t, c, f, "/dav/source", "/dav/Source", nil)
	if code != 204 {
		t.Fatal("case rename failed")
	}
	_, h, _ := fileRequest(t, c, f, "HEAD", "/dav/Source", nil, nil)
	if h.Get("Last-Modified") != "Sat, 01 Jan 2000 00:00:00 GMT" {
		t.Fatal("case rename changed representation timestamp")
	}
	code, _, _ = moveRequest(t, c, f, "/dav/Source", "/dav/destination", nil)
	if code != 204 {
		t.Fatal("replacement MOVE failed")
	}
	code, h, body := fileRequest(t, c, f, "GET", "/dav/destination", nil, nil)
	stamp, err := time.Parse(http.TimeFormat, h.Get("Last-Modified"))
	if code != 200 || string(body) != "source bytes" || err != nil || !stamp.After(time.Unix(946684800, 0)) {
		t.Fatal("changed destination representation did not advance timestamp")
	}
}

func TestMoveCombinedTreeBoundsAndReopen(t *testing.T) {
	for _, kind := range []string{"resources", "logical-bytes"} {
		t.Run(kind, func(t *testing.T) {
			bin := development(t)
			f := newServerFixture(t, bin)
			p := startServer(t, bin, f)
			c := fixtureClient(f)
			defer c.CloseIdleConnections()
			fileRequest(t, c, f, "MKCOL", "/dav/Bounded", nil, nil)
			if kind == "resources" {
				fileRequest(t, c, f, "MKCOL", "/dav/destination", nil, nil)
			} else {
				fileRequest(t, c, f, "PUT", "/dav/destination", strings.NewReader("x"), nil)
			}
			c.CloseIdleConnections()
			p.stop()
			seedMoveBound(t, f, kind)
			p = startServer(t, bin, f)
			code, _, _ := moveRequest(t, c, f, "/dav/Bounded", "/dav/new", nil)
			if code != 507 {
				t.Fatalf("tree excess:%d", code)
			}
			leaf := "/dav/Bounded/b0/n0000"
			if kind == "logical-bytes" {
				leaf = "/dav/Bounded/extra"
			}
			code, _, _ = moveRequest(t, c, f, leaf, "/dav/removed-one", nil)
			if code != 201 {
				t.Fatalf("smaller explicit move:%d", code)
			}
			code, _, _ = moveRequest(t, c, f, "/dav/Bounded", "/dav/destination", nil)
			if code != 507 {
				t.Fatalf("combined excess:%d", code)
			}
			c.CloseIdleConnections()
			p.stop()
			p = startServer(t, bin, f)
			code, _, _ = fileRequest(t, c, f, "PROPFIND", "/dav/Bounded", nil, map[string]string{"Depth": "0"})
			if code != 207 {
				t.Fatal("bounded refusal lost source on reopen")
			}
			if kind == "resources" {
				code, _, _ = moveRequest(t, c, f, "/dav/Bounded/b0/n0001", "/dav/removed-two", nil)
				if code != 201 {
					t.Fatal("second smaller move failed")
				}
			} else {
				code, _, _ = moveRequest(t, c, f, "/dav/destination", "/dav/removed-two", nil)
				if code != 201 {
					t.Fatal("destination relocation failed")
				}
			}
			want := 204
			if kind == "logical-bytes" {
				want = 201
			}
			code, _, _ = moveRequest(t, c, f, "/dav/Bounded", "/dav/destination", nil)
			if code != want {
				t.Fatalf("exact boundary:%d want%d", code, want)
			}
			c.CloseIdleConnections()
			p.stop()
			p = startServer(t, bin, f)
			defer p.stop()
			code, _, _ = fileRequest(t, c, f, "PROPFIND", "/dav/Bounded", nil, map[string]string{"Depth": "0"})
			if code != 404 {
				t.Fatal("old mapping returned")
			}
			code, _, _ = fileRequest(t, c, f, "PROPFIND", "/dav/destination", nil, map[string]string{"Depth": "0"})
			if code != 207 {
				t.Fatal("boundary move lost destination")
			}
		})
	}
}

// Fixture construction only, while the disposable server is stopped. Startup
// validates production-shaped state. All assertions use HTTPS and executable
// reopen; no test reads private tables to decide whether MOVE succeeded.
func seedMoveBound(t *testing.T, f serverFixture, kind string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "godav.sqlite"))
	if err != nil {
		t.Fatal("bound fixture open")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal("fixture foreign keys")
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal("fixture transaction")
	}
	defer tx.Rollback()
	var root string
	if tx.QueryRow(`SELECT id FROM nodes WHERE name='Bounded'`).Scan(&root) != nil {
		t.Fatal("fixture root")
	}
	serial := 1
	add := func(parent, name string) string {
		id := fmt.Sprintf("%032x", serial)
		serial++
		if _, err := tx.Exec(`INSERT INTO nodes VALUES(?,?,?,?,946684800,946684800)`, id, parent, name, strings.ToUpper(name)); err != nil {
			t.Fatal("fixture node")
		}
		return id
	}
	if kind == "resources" {
		for b := 0; b < 4; b++ {
			parent := add(root, fmt.Sprintf("b%d", b))
			for n := 0; n < 1023; n++ {
				add(parent, fmt.Sprintf("n%04d", n))
			}
		}
	} else {
		for _, file := range []struct {
			name string
			size int64
		}{{"a", 64 << 20}, {"b", 64 << 20}, {"extra", 1}} {
			id := add(root, file.name)
			if _, err := tx.Exec(`INSERT INTO file_contents VALUES(?,zeroblob(?),?,'application/octet-stream',?)`, id, file.size, file.size, `"`+id+`"`); err != nil {
				t.Fatal("fixture content")
			}
		}
	}
	if tx.Commit() != nil {
		t.Fatal("fixture commit")
	}
}
