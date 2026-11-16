package cli_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteBoundedTree(t *testing.T) {
	for _, kind := range []string{"resources", "logical-bytes"} {
		t.Run(kind, func(t *testing.T) {
			bin := development(t)
			f := newServerFixture(t, bin)
			p := startServer(t, bin, f)
			c := fixtureClient(f)
			defer c.CloseIdleConnections()
			code, _, _ := fileRequest(t, c, f, "MKCOL", "/dav/Bounded/", nil, nil)
			if code != 201 {
				t.Fatal("seed root")
			}
			code, _, _ = fileRequest(t, c, f, "PUT", "/dav/sibling", strings.NewReader("retained"), nil)
			if code != 201 {
				t.Fatal("seed sibling")
			}
			c.CloseIdleConnections()
			p.stop()
			// Setup shortcut only: build ordinary production-shaped records in this
			// stopped disposable SQLite fixture. Startup validates them; every acceptance
			// assertion below uses HTTPS and reopen, never a private SQL inspection.
			seedDeleteBound(t, f, kind)
			p = startServer(t, bin, f)
			code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/Bounded/", nil, nil)
			if code != 507 {
				t.Fatalf("%s upper bound: %d", kind, code)
			}
			leaf := "/dav/Bounded/b0/n0000/"
			if kind == "logical-bytes" {
				leaf = "/dav/Bounded/extra"
			}
			code, _, _ = fileRequest(t, c, f, "PROPFIND", leaf, nil, map[string]string{"Depth": "0"})
			if code != 207 {
				t.Fatalf("refusal lost child: %d", code)
			}
			if len(namespaceListing(t, c, f, "/dav/Bounded/", "1")) < 4 {
				t.Fatal("refusal partially removed tree")
			}
			c.CloseIdleConnections()
			p.stop()
			p = startServer(t, bin, f)
			code, _, _ = fileRequest(t, c, f, "PROPFIND", leaf, nil, map[string]string{"Depth": "0"})
			if code != 207 {
				t.Fatal("bounded refusal did not survive reopen")
			}
			code, _, _ = fileRequest(t, c, f, "DELETE", leaf, nil, nil)
			if code != 204 {
				t.Fatalf("explicit smaller operation: %d", code)
			}
			code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/Bounded/", nil, nil)
			if code != 204 {
				t.Fatalf("exact %s boundary: %d", kind, code)
			}
			code, _, data := fileRequest(t, c, f, "GET", "/dav/sibling", nil, nil)
			if code != 200 || string(data) != "retained" {
				t.Fatal("bound operation changed sibling")
			}
			c.CloseIdleConnections()
			p.stop()
			p = startServer(t, bin, f)
			defer p.stop()
			code, _, _ = fileRequest(t, c, f, "PROPFIND", "/dav/Bounded/", nil, map[string]string{"Depth": "0"})
			if code != 404 {
				t.Fatal("bounded deletion resurrected")
			}
		})
	}
}

func seedDeleteBound(t *testing.T, f serverFixture, kind string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "godav.sqlite"))
	if err != nil {
		t.Fatal("bound fixture open")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal("bound fixture foreign keys")
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal("bound fixture transaction")
	}
	defer tx.Rollback()
	var root string
	if tx.QueryRow(`SELECT id FROM nodes WHERE name='Bounded'`).Scan(&root) != nil {
		t.Fatal("bound fixture root")
	}
	serial := 1
	add := func(parent, name string) string {
		id := fmt.Sprintf("%032x", serial)
		serial++
		if _, err := tx.Exec(`INSERT INTO nodes VALUES(?,?,?,?,946684800,946684800)`, id, parent, name, strings.ToUpper(name)); err != nil {
			t.Fatal("bound fixture node")
		}
		return id
	}
	if kind == "resources" {
		for branch := 0; branch < 4; branch++ {
			parent := add(root, fmt.Sprintf("b%d", branch))
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
			if _, err := tx.Exec(`INSERT INTO file_contents VALUES(?,zeroblob(?),?,'application/octet-stream','"0123456789abcdef0123456789abcdef"')`, id, file.size, file.size); err != nil {
				t.Fatal("bound fixture content")
			}
		}
	}
	if tx.Commit() != nil {
		t.Fatal("bound fixture commit")
	}
}

func TestDeleteStorageNamesAreOnlyDAVResources(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	yaml, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal("fixture config read")
	}
	dbInfo, err := os.Stat(filepath.Join(f.dir, "godav.sqlite"))
	if err != nil {
		t.Fatal("fixture DB info")
	}
	sentinel := filepath.Join(f.dir, "unrelated.txt")
	if os.WriteFile(sentinel, []byte("host sentinel"), 0600) != nil {
		t.Fatal("fixture sentinel")
	}
	p := startServer(t, bin, f)
	c := fixtureClient(f)
	defer c.CloseIdleConnections()
	for _, name := range []string{"godav.yaml", "godav.sqlite", "unrelated.txt"} {
		code, _, _ := fileRequest(t, c, f, "PUT", "/dav/"+name, strings.NewReader("DAV only"), nil)
		if code != 201 {
			t.Fatal("logical seed")
		}
		code, _, _ = fileRequest(t, c, f, "DELETE", "/dav/"+name, nil, nil)
		if code != 204 {
			t.Fatalf("logical DELETE: %d", code)
		}
	}
	c.CloseIdleConnections()
	p.stop()
	after, err := os.ReadFile(f.config)
	if err != nil || !bytes.Equal(yaml, after) {
		t.Fatal("DELETE changed settings")
	}
	afterInfo, err := os.Stat(filepath.Join(f.dir, "godav.sqlite"))
	if err != nil || !os.SameFile(dbInfo, afterInfo) {
		t.Fatal("DELETE changed DB identity")
	}
	contents, err := os.ReadFile(sentinel)
	if err != nil || string(contents) != "host sentinel" {
		t.Fatal("DELETE touched host sibling")
	}
	p = startServer(t, bin, f)
	defer p.stop()
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/account-and-store-healthy", strings.NewReader("after reopen"), nil)
	if code != 201 {
		t.Fatalf("DELETE damaged accounts or reopened state: %d", code)
	}
}
