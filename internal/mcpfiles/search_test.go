package mcpfiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

type searchRepository struct {
	files.Repository
	snapshots  map[string]files.Snapshot
	seen       []string
	principals []string
}

func (r *searchRepository) CollectionSnapshot(_ context.Context, names []string, _ bool, e ...files.Expectation) (files.Snapshot, error) {
	path := strings.Join(names, "/")
	r.seen = append(r.seen, path)
	if len(e) != 1 {
		return files.Snapshot{}, files.ErrInvalid
	}
	r.principals = append(r.principals, e[0].Principal)
	s, ok := r.snapshots[files.Key(path)]
	if !ok {
		return files.Snapshot{}, files.ErrMissing
	}
	s.Children = append([]files.Node(nil), s.Children...)
	return s, nil
}
func (r *searchRepository) ReadFile(_ context.Context, names []string, e ...files.Expectation) (files.Snapshot, []byte, error) {
	s, ok := r.snapshots[files.Key(strings.Join(names, "/"))]
	if !ok {
		return files.Snapshot{}, nil, files.ErrMissing
	}
	if err := e[0].Check(s.Node, true); err != nil {
		return files.Snapshot{}, nil, err
	}
	return s, []byte("hello"), nil
}
func searchSetup(t *testing.T, scope string) (*Handler, *searchRepository) {
	t.Helper()
	r := &searchRepository{snapshots: map[string]files.Snapshot{}}
	h, err := New(files.New(r), scope, MaxContentBytes)
	if err != nil {
		t.Fatal(err)
	}
	return h, r
}
func (r *searchRepository) directory(path string, children ...files.Node) {
	r.snapshots[files.Key(path)] = files.Snapshot{Path: path, Children: children}
}
func searchFile(name string) files.Node {
	return files.Node{Name: name, File: true, ETag: `"v1"`, Size: 5}
}
func doSearch(t *testing.T, h *Handler, path, query string, limit int) SearchResult {
	t.Helper()
	args := map[string]any{"path": path, "query": query}
	if limit != 0 {
		args["limit"] = limit
	}
	v, err := call(h, "search_files", args)
	if err != nil {
		t.Fatal(err)
	}
	return v.(SearchResult)
}
func searchPaths(r SearchResult) []string {
	p := make([]string, 0, len(r.Entries))
	for _, e := range r.Entries {
		p = append(p, e.Path)
	}
	return p
}
func TestSearchScopeNestedCanonicalAndRead(t *testing.T) {
	h, r := searchSetup(t, "Docs")
	r.directory("Docs", files.Node{Name: "Reports"}, searchFile("z.TXT"), files.Node{Name: "Empty"})
	r.directory("Docs/Reports", searchFile("Plan.TXT"), searchFile("Other.md"))
	r.directory("Docs/Empty")
	r.directory("Outside", searchFile("secret.TXT"))
	f := searchFile("Plan.TXT")
	r.snapshots[files.Key("Docs/Reports/Plan.TXT")] = files.Snapshot{Path: "Docs/Reports/Plan.TXT", Node: f}
	got := doSearch(t, h, "", ".txt", 0)
	if got.Partial || !reflect.DeepEqual(searchPaths(got), []string{"Reports/Plan.TXT", "z.TXT"}) {
		t.Fatalf("%+v", got)
	}
	for _, path := range r.seen {
		if !strings.HasPrefix(path, "Docs") {
			t.Fatalf("escaped scope: %s", path)
		}
	}
	for _, principal := range r.principals {
		if principal != "alice" {
			t.Fatal("lost principal")
		}
	}
	nested := doSearch(t, h, "reports", "REPORTS/PLAN", 0)
	if !reflect.DeepEqual(searchPaths(nested), []string{"Reports/Plan.TXT"}) {
		t.Fatal(nested)
	}
	v, err := call(h, "read_file", map[string]any{"path": nested.Entries[0].Path})
	if err != nil || v.(map[string]any)["content"] != "hello" {
		t.Fatalf("roundtrip: %v %v", v, err)
	}
	empty := doSearch(t, h, "Empty", "x", 0)
	if empty.Partial || len(empty.Entries) != 0 {
		t.Fatal(empty)
	}
	noMatch := doSearch(t, h, "", "secret", 0)
	if len(noMatch.Entries) != 0 {
		t.Fatal(noMatch)
	}
}
func TestSearchUnicodeSimpleFoldNoNormalization(t *testing.T) {
	h, r := searchSetup(t, "K")
	r.directory("K", searchFile("Kelvin.txt"), searchFile("Σ.txt"), searchFile("Straße.txt"), searchFile("Café.txt"), searchFile("Cafe\u0301.txt"))
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"kelvin", []string{"Kelvin.txt"}}, {"ς", []string{"Σ.txt"}}, {"STRASSE", []string{}}, {"CAFÉ", []string{"Café.txt"}}, {"cafe\u0301", []string{"Cafe\u0301.txt"}},
	} {
		got := doSearch(t, h, "", tc.query, 0)
		if !reflect.DeepEqual(searchPaths(got), tc.want) {
			t.Errorf("%q: %v want %v", tc.query, searchPaths(got), tc.want)
		}
	}
}
func TestSearchLiteralAndValidation(t *testing.T) {
	h, r := searchSetup(t, "Docs")
	r.directory("Docs", searchFile("[a].txt"))
	if len(doSearch(t, h, "", "[a]", 0).Entries) != 1 || len(doSearch(t, h, "", ".*", 0).Entries) != 0 || len(doSearch(t, h, "", "*", 0).Entries) != 0 {
		t.Fatal("query is not literal")
	}
	for _, args := range []map[string]any{
		{"path": "", "query": ""}, {"path": "../Outside", "query": "a"}, {"path": "/Docs", "query": "a"}, {"path": "", "query": "a", "limit": 0}, {"path": "", "query": "a", "limit": 101}, {"path": "", "query": "a", "offset": 1}, {"path": "", "query": strings.Repeat("a", MaxSearchQueryBytes+1)},
	} {
		if _, err := call(h, "search_files", args); !errors.Is(err, files.ErrInvalid) {
			t.Errorf("accepted %v: %v", args, err)
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"path":""}`, `{"query":"a"}`, `{"path":"","query":"a"} {}`} {
		if _, err := h.Call(context.Background(), "alice", "search_files", json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	r.snapshots[files.Key("Docs/file")] = files.Snapshot{Path: "Docs/file", Node: searchFile("file")}
	if _, err := call(h, "search_files", map[string]any{"path": "file", "query": "a"}); !errors.Is(err, files.ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Call(ctx, "alice", "search_files", json.RawMessage(`{"path":"","query":"a"}`)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestSearchResultAndScanBounds(t *testing.T) {
	h, r := searchSetup(t, "Docs")
	r.directory("Docs", searchFile("b"), searchFile("a"))
	got := doSearch(t, h, "", "a", 1)
	if !got.Partial || got.StopReason != "result_limit" || !reflect.DeepEqual(searchPaths(got), []string{"a"}) {
		t.Fatal(got)
	}
	children := []files.Node{}
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("d%d", i)
		children = append(children, files.Node{Name: name})
		members := []files.Node{}
		for j := 0; j < files.MaxChildren; j++ {
			members = append(members, searchFile(fmt.Sprintf("f%04d", j)))
		}
		r.directory("Docs/"+name, members...)
	}
	r.directory("Docs", children...)
	got = doSearch(t, h, "", "no-match", 0)
	if !got.Partial || got.StopReason != "scan_limit" || got.ScannedEntries > MaxSearchEntries {
		t.Fatal(got)
	}
	if got.ScannedDirectories != 4 || got.ScannedEntries != 6+3*files.MaxChildren {
		t.Fatalf("unexpected budget use: %+v", got)
	}
	r.snapshots = map[string]files.Snapshot{}
	children = nil
	for i := 0; i < 40; i++ {
		name := fmt.Sprintf("d%02d", i)
		children = append(children, files.Node{Name: name})
		r.directory("Docs/" + name)
	}
	r.directory("Docs", children...)
	got = doSearch(t, h, "", "x", 0)
	if !got.Partial || got.StopReason != "directory_limit" || got.ScannedDirectories != MaxSearchDirectories {
		t.Fatal(got)
	}
}
func TestSearchTreeChangesAndDescriptor(t *testing.T) {
	h, r := searchSetup(t, "Docs")
	r.directory("Docs", files.Node{Name: "gone"})
	got := doSearch(t, h, "", "x", 0)
	if !got.Partial || got.StopReason != "tree_changed" {
		t.Fatal(got)
	}
	found := false
	for _, d := range h.Tools() {
		if d.Name == "search_files" {
			found = true
			if IsWrite(d.Name) {
				t.Fatal("search classified writable")
			}
			if d.InputSchema["additionalProperties"] != false {
				t.Fatal("loose schema")
			}
		}
	}
	if !found {
		t.Fatal("not advertised")
	}
}
