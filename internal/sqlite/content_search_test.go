package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

func contentSearchStore(t *testing.T) *AccountStore {
	t.Helper()
	db, e := sql.Open("sqlite", ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	conn, e := db.Conn(context.Background())
	if e != nil {
		db.Close()
		t.Fatal(e)
	}
	s := &AccountStore{db: db, conn: conn, writable: true, operation: make(chan struct{}, 1)}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	})
	if _, e = conn.ExecContext(context.Background(), `PRAGMA trusted_schema=OFF; PRAGMA foreign_keys=ON; PRAGMA user_version=1`); e != nil {
		t.Fatal(e)
	}
	if e = s.PrepareNamespace(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.PrepareFiles(context.Background()); e != nil {
		t.Fatal(e)
	}
	searchMkdir(t, s, "Documents")
	return s
}
func searchMkdir(t *testing.T, s *AccountStore, p string) {
	t.Helper()
	if e := s.CreateCollection(context.Background(), strings.Split(p, "/"), files.Expectation{}); e != nil {
		t.Fatal(p, e)
	}
}
func searchPut(t *testing.T, s *AccountStore, p, text, kind string) {
	t.Helper()
	if _, _, e := s.PutFile(context.Background(), strings.Split(p, "/"), []byte(text), kind, files.Expectation{}); e != nil {
		t.Fatal(p, e)
	}
}
func searchOptions(query string) files.ContentSearchOptions {
	return files.ContentSearchOptions{Query: query, Limit: 100}
}
func contentSearch(t *testing.T, s *AccountStore, p string, o files.ContentSearchOptions) files.ContentSearchResult {
	t.Helper()
	v, e := s.SearchContent(context.Background(), strings.Split(p, "/"), o)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func hitPaths(v files.ContentSearchResult) []string {
	out := []string{}
	for _, h := range v.Entries {
		out = append(out, h.Path)
	}
	return out
}
func requirePaths(t *testing.T, v files.ContentSearchResult, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(hitPaths(v), want) {
		t.Fatalf("paths=%v want=%v", hitPaths(v), want)
	}
}

func TestContentSearchFullBodyPhraseAndRanking(t *testing.T) {
	s := contentSearchStore(t)
	searchPut(t, s, "Documents/deep.txt", strings.Repeat("padding ", 10000)+"deep needle", "text/plain")
	searchPut(t, s, "Documents/deep needle.txt", "unrelated words", "text/plain")
	searchPut(t, s, "Documents/separate.txt", "deep other needle", "text/plain")
	searchPut(t, s, "Documents/binary.bin", "deep needle\x00more", "application/octet-stream")
	searchPut(t, s, "Documents/invalid.bin", "deep needle\xff", "application/octet-stream")
	requirePaths(t, contentSearch(t, s, "documents", searchOptions("deep needle")), "Documents/deep.txt")
	searchPut(t, s, "Documents/a.txt", "alpha beta", "text/plain")
	searchPut(t, s, "Documents/b.txt", "alpha beta", "text/plain")
	searchPut(t, s, "Documents/z.txt", "alpha beta "+strings.Repeat("filler ", 1000), "text/plain")
	requirePaths(t, contentSearch(t, s, "Documents", searchOptions("alpha beta")), "Documents/a.txt", "Documents/b.txt", "Documents/z.txt")
	if v := contentSearch(t, s, "Documents", searchOptions("alpha OR beta")); len(v.Entries) != 0 {
		t.Fatalf("query operators interpreted: %+v", v)
	}
	// Repeating a query proves TEMP teardown and complete backfill are usable twice.
	requirePaths(t, contentSearch(t, s, "Documents", searchOptions("deep needle")), "Documents/deep.txt")
}

func TestContentSearchRefreshAndScopedIdentity(t *testing.T) {
	s := contentSearchStore(t)
	searchMkdir(t, s, "Documents/Sub")
	searchMkdir(t, s, "DocumentsTwo")
	searchPut(t, s, "Documents/Sub/one.txt", "before marker", "text/plain")
	searchPut(t, s, "DocumentsTwo/outside.txt", "before marker", "text/plain")
	requirePaths(t, contentSearch(t, s, "documents/sub", searchOptions("before marker")), "Documents/Sub/one.txt")
	searchPut(t, s, "Documents/Sub/one.txt", "after marker", "text/plain")
	if v := contentSearch(t, s, "Documents", searchOptions("before marker")); len(v.Entries) != 0 {
		t.Fatal("stale or sibling content", v)
	}
	requirePaths(t, contentSearch(t, s, "Documents", searchOptions("after marker")), "Documents/Sub/one.txt")
	if _, e := s.Move(context.Background(), []string{"Documents", "Sub", "one.txt"}, []string{"Documents", "moved.txt"}, files.MoveOptions{}, files.Expectation{}); e != nil {
		t.Fatal(e)
	}
	requirePaths(t, contentSearch(t, s, "Documents", searchOptions("after marker")), "Documents/moved.txt")
	if v := contentSearch(t, s, "Documents/Sub", searchOptions("after marker")); len(v.Entries) != 0 {
		t.Fatal("moved file remains", v)
	}
	if e := s.DeleteResource(context.Background(), []string{"Documents", "moved.txt"}, files.DeleteRequest{}, files.Expectation{}); e != nil {
		t.Fatal(e)
	}
	if v := contentSearch(t, s, "Documents", searchOptions("after marker")); len(v.Entries) != 0 {
		t.Fatal("deleted file remains", v)
	}
	searchPut(t, s, "Documents/new.txt", "after marker", "text/plain")
	requirePaths(t, contentSearch(t, s, "Documents", searchOptions("after marker")), "Documents/new.txt")
}

func TestContentSearchFiltersAndAccurateLimit(t *testing.T) {
	s := contentSearchStore(t)
	for _, n := range []string{"A.TXT", "B.md", "C.txt"} {
		searchPut(t, s, "Documents/"+n, "find me", "text/plain; charset=utf-8")
	}
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i, n := range []string{"A.TXT", "B.md", "C.txt"} {
		if _, e := s.conn.ExecContext(context.Background(), `UPDATE nodes SET modified_at=? WHERE name=?`, base.Unix()+int64(i), n); e != nil {
			t.Fatal(e)
		}
	}
	o := searchOptions("find me")
	o.Limit = 1
	v := contentSearch(t, s, "Documents", o)
	requirePaths(t, v, "Documents/A.TXT")
	if !v.Partial {
		t.Fatal("missing accurate partial flag")
	}
	o.Limit = 3
	v = contentSearch(t, s, "Documents", o)
	if v.Partial || len(v.Entries) != 3 {
		t.Fatal("exact limit is not partial", v)
	}
	o.Extension = ".tXt"
	requirePaths(t, contentSearch(t, s, "Documents", o), "Documents/A.TXT", "Documents/C.txt")
	o.ContentType = "TEXT/PLAIN"
	requirePaths(t, contentSearch(t, s, "Documents", o), "Documents/A.TXT", "Documents/C.txt")
	o.ContentType = "text/markdown"
	if v = contentSearch(t, s, "Documents", o); len(v.Entries) != 0 {
		t.Fatal("MIME filter ignored")
	}
	o = searchOptions("find me")
	seven := int64(7)
	o.MinSize = &seven
	o.MaxSize = &seven
	requirePaths(t, contentSearch(t, s, "Documents", o), "Documents/A.TXT", "Documents/B.md", "Documents/C.txt")
	eight := int64(8)
	o.MinSize = &eight
	o.MaxSize = nil
	if v = contentSearch(t, s, "Documents", o); len(v.Entries) != 0 {
		t.Fatal("size filter ignored")
	}
	o = searchOptions("find me")
	o.ModifiedAfter = base.Add(500 * time.Millisecond)
	o.ModifiedBefore = base.Add(2 * time.Second)
	requirePaths(t, contentSearch(t, s, "Documents", o), "Documents/B.md")
	o.ModifiedAfter = base
	o.ModifiedBefore = base.Add(time.Second)
	requirePaths(t, contentSearch(t, s, "Documents", o), "Documents/A.TXT")
	o.ModifiedAfter = time.Time{}
	o.ModifiedBefore = base.Add(500 * time.Millisecond)
	requirePaths(t, contentSearch(t, s, "Documents", o), "Documents/A.TXT")
	searchPut(t, s, "Documents/unicode.\u212a", "unicode marker", "text/plain")
	o = searchOptions("unicode marker")
	o.Extension = ".k"
	if v = contentSearch(t, s, "Documents", o); len(v.Entries) != 0 {
		t.Fatal("extension matching must fold ASCII only", v)
	}
}

func TestContentSearchCapacityAndTemporaryState(t *testing.T) {
	s := contentSearchStore(t)
	ctx := context.Background()
	searchPut(t, s, "Documents/a.txt", "test phrase", "text/plain")
	var before string
	if e := s.conn.QueryRowContext(ctx, `SELECT group_concat(sql,';') FROM main.sqlite_schema ORDER BY name`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	s.writable = false
	requirePaths(t, contentSearch(t, s, "Documents", searchOptions("test phrase")), "Documents/a.txt")
	s.writable = true
	var after string
	_ = s.conn.QueryRowContext(ctx, `SELECT group_concat(sql,';') FROM main.sqlite_schema ORDER BY name`).Scan(&after)
	if before != after {
		t.Fatal("persisted schema changed")
	}
	var count, mode int
	if e := s.conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_temp_schema WHERE name LIKE 'godav_content_fts%'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("TEMP index retained", count, e)
	}
	if e := s.conn.QueryRowContext(ctx, `PRAGMA temp_store`).Scan(&mode); e != nil || mode != 2 {
		t.Fatal("index not memory-only", mode, e)
	}
	searchMkdir(t, s, "Big")
	searchMkdir(t, s, "Big/Left")
	searchMkdir(t, s, "Big/Right")
	// Valid namespace layout: two groups each have at most 1023 children. There
	// are exactly 2048 scoped nodes, including Big and its two group collections.
	tx, e := s.conn.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	left, _, e := lookupNode(ctx, tx, []string{"Big", "Left"})
	if e != nil {
		t.Fatal(e)
	}
	right, _, e := lookupNode(ctx, tx, []string{"Big", "Right"})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2045; i++ {
		parent := left.ID
		if i >= 1023 {
			parent = right.ID
		}
		name := fmt.Sprintf("n%04d", i)
		if _, e = tx.ExecContext(ctx, `INSERT INTO nodes VALUES(?,?,?,?,?,?)`, fmt.Sprintf("%032x", i+1), parent, name, files.Key(name), 1, 1); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if v := contentSearch(t, s, "Big", searchOptions("none")); len(v.Entries) != 0 || v.Partial {
		t.Fatal(v)
	}
	searchMkdir(t, s, "Big/Right/extra")
	v, e := s.SearchContent(ctx, []string{"Big"}, searchOptions("none"))
	if !errors.Is(e, files.ErrCapacity) || len(v.Entries) != 0 || v.Partial {
		t.Fatalf("capacity returned misleading result: %+v %v", v, e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = s.SearchContent(cancelled, []string{"Documents"}, searchOptions("test")); e == nil {
		t.Fatal("cancelled search succeeded")
	}
	requirePaths(t, contentSearch(t, s, "Documents", searchOptions("test phrase")), "Documents/a.txt")
}
