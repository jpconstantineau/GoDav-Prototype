package mcpfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

type repository struct {
	files.Repository
	node        files.Node
	data        []byte
	path        string
	expectation files.Expectation
	deletion    files.DeleteRequest
	children    []files.Node
	fail        error
	readCalled  bool
}

func (r *repository) CollectionSnapshot(_ context.Context, names []string, children bool, e ...files.Expectation) (files.Snapshot, error) {
	r.path = strings.Join(names, "/")
	if r.fail != nil {
		return files.Snapshot{}, r.fail
	}
	return files.Snapshot{Node: r.node, Children: r.children}, nil
}
func (r *repository) ReadFile(_ context.Context, names []string, e ...files.Expectation) (files.Snapshot, []byte, error) {
	r.readCalled = true
	r.expectation = e[0]
	if err := e[0].Check(r.node, true); err != nil {
		return files.Snapshot{}, nil, err
	}
	return files.Snapshot{Node: r.node}, append([]byte(nil), r.data...), r.fail
}
func (r *repository) PutFile(_ context.Context, names []string, data []byte, contentType string, e files.Expectation) (files.Node, bool, error) {
	r.path = strings.Join(names, "/")
	r.expectation = e
	if r.fail != nil {
		return files.Node{}, false, r.fail
	}
	exists := r.node.ETag != ""
	if err := e.CheckWrite(r.node, exists); err != nil {
		return files.Node{}, false, err
	}
	r.data = append([]byte(nil), data...)
	r.node = files.Node{Name: names[len(names)-1], File: true, Size: int64(len(data)), ContentType: contentType, ETag: `"new"`}
	return r.node, !exists, nil
}
func (r *repository) DeleteResource(_ context.Context, names []string, d files.DeleteRequest, e files.Expectation) error {
	r.path = strings.Join(names, "/")
	r.expectation = e
	r.deletion = d
	if r.fail != nil {
		return r.fail
	}
	if !r.node.File && d.FiniteDepth {
		return files.ErrInvalid
	}
	return e.CheckWrite(r.node, r.node.ETag != "")
}
func setup(t *testing.T) (*Handler, *repository) {
	t.Helper()
	r := &repository{}
	h, err := New(files.New(r), "safe", MaxContentBytes)
	if err != nil {
		t.Fatal(err)
	}
	return h, r
}
func call(h *Handler, name string, a map[string]any) (any, error) {
	raw, _ := json.Marshal(a)
	return h.Call(context.Background(), "alice", name, raw)
}
func TestCreateNoOverwriteAndScope(t *testing.T) {
	h, r := setup(t)
	_, err := call(h, "create_file", map[string]any{"path": "a.txt", "content": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if r.path != "safe/a.txt" || !r.expectation.NotMatch.Any || r.expectation.Principal != "alice" {
		t.Fatalf("unsafe expectation: %+v %s", r.expectation, r.path)
	}
	_, err = call(h, "create_file", map[string]any{"path": "a.txt", "content": "overwrite"})
	if !errors.Is(err, files.ErrPrecondition) {
		t.Fatalf("overwrite: %v", err)
	}
	if string(r.data) != "hello" {
		t.Fatal("overwritten")
	}
}
func TestMutationExpectations(t *testing.T) {
	h, r := setup(t)
	r.node = files.Node{File: true, ETag: `"current"`, ContentType: "text/plain"}
	for _, name := range []string{"edit_file", "delete_file"} {
		args := map[string]any{"path": "a", "expected_etag": `"stale"`}
		if name == "edit_file" {
			args["content"] = "changed"
		}
		_, err := call(h, name, args)
		if !errors.Is(err, files.ErrPrecondition) {
			t.Fatalf("%s stale: %v", name, err)
		}
		args["expected_etag"] = `"current"`
		r.fail = files.ErrLocked
		_, err = call(h, name, args)
		if !errors.Is(err, files.ErrLocked) {
			t.Fatalf("%s lock: %v", name, err)
		}
		r.fail = nil
	}
	_, err := call(h, "delete_file", map[string]any{"path": "a", "expected_etag": `"current"`})
	if err != nil || !r.deletion.FiniteDepth || r.expectation.Principal != "alice" {
		t.Fatalf("delete guard: %v %+v", err, r.deletion)
	}
	r.node.File = false
	_, err = call(h, "delete_file", map[string]any{"path": "dir", "expected_etag": `"current"`})
	if !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("directory delete: %v", err)
	}
}
func TestReadBoundsAndPin(t *testing.T) {
	h, r := setup(t)
	r.node = files.Node{File: true, ETag: `"version"`, Size: MaxContentBytes + 1}
	_, err := call(h, "read_file", map[string]any{"path": "a"})
	if !errors.Is(err, files.ErrTooLarge) || r.readCalled {
		t.Fatalf("oversize allocated: %v", err)
	}
	r.node.Size = 5
	r.data = []byte("hello")
	v, err := call(h, "read_file", map[string]any{"path": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["content"] != "hello" || r.expectation.Match.Tags[0].Value != r.node.ETag {
		t.Fatal("read not pinned")
	}
	r.data = []byte{0xff}
	_, err = call(h, "read_file", map[string]any{"path": "a"})
	if !errors.Is(err, files.ErrInvalid) {
		t.Fatal("binary accepted")
	}
}
func TestListBounded(t *testing.T) {
	h, r := setup(t)
	for i := 0; i < 105; i++ {
		r.children = append(r.children, files.Node{Name: "a"})
	}
	v, err := call(h, "list_files", map[string]any{"path": ""})
	if err != nil {
		t.Fatal(err)
	}
	result := v.(map[string]any)
	if len(result["entries"].([]Entry)) != 100 || result["next_offset"] != 100 {
		t.Fatal(result)
	}
}
func TestInvalidArguments(t *testing.T) {
	h, _ := setup(t)
	for _, path := range []string{"../outside", "/absolute", `C:\file`, `a\b`, "a/../b", "a//b", "CON", "a.", ""} {
		if _, err := call(h, "read_file", map[string]any{"path": path}); err == nil {
			t.Errorf("accepted path %q", path)
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"path":"a","extra":1}`, `{"path":"a"} {}`, `{"path":"a","offset":1}`, `{"path":"a","content":"x"}`} {
		if _, err := h.Call(context.Background(), "alice", "read_file", json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, err := h.Call(context.Background(), "", "read_file", json.RawMessage(`{"path":"a"}`)); err == nil {
		t.Fatal("anonymous")
	}
	for _, tag := range []string{"", "*", `W/"a"`, `"a","b"`} {
		if _, err := call(h, "delete_file", map[string]any{"path": "a", "expected_etag": tag}); err == nil {
			t.Errorf("accepted tag %q", tag)
		}
	}
	if _, err := call(h, "create_file", map[string]any{"path": "a", "content": strings.Repeat("a", MaxContentBytes+1)}); !errors.Is(err, files.ErrTooLarge) {
		t.Fatal(err)
	}
}

func TestConfiguredWriteLimitAndTextOnlyEdit(t *testing.T) {
	r := &repository{}
	h, err := New(files.New(r), "safe", 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = call(h, "create_file", map[string]any{"path": "a", "content": "12345"}); !errors.Is(err, files.ErrTooLarge) {
		t.Fatalf("configured limit ignored: %v", err)
	}
	if _, err = call(h, "create_file", map[string]any{"path": "a", "content": "1234"}); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{{0xff}, {'a', 0, 'b'}} {
		r.data = data
		r.node.Size = int64(len(data))
		if _, err = call(h, "edit_file", map[string]any{"path": "a", "content": "text", "expected_etag": `"new"`}); !errors.Is(err, files.ErrInvalid) {
			t.Fatalf("binary edit accepted: %v", err)
		}
		if !bytes.Equal(r.data, data) {
			t.Fatal("binary changed")
		}
	}
	r.data = []byte("text")
	r.node.Size = 4
	if _, err = call(h, "edit_file", map[string]any{"path": "a", "content": "more!", "expected_etag": `"new"`}); !errors.Is(err, files.ErrTooLarge) {
		t.Fatalf("edit limit ignored: %v", err)
	}
	if _, err = call(h, "edit_file", map[string]any{"path": "a", "content": "next", "expected_etag": `"new"`}); err != nil {
		t.Fatal(err)
	}
	if !r.readCalled || string(r.data) != "next" {
		t.Fatal("edit validation read missing")
	}
	for _, n := range []int64{0, -1, files.AbsoluteMaxFileBytes + 1} {
		if _, err = New(files.New(r), "safe", n); err == nil {
			t.Fatalf("invalid max accepted: %d", n)
		}
	}
	for _, tool := range h.Tools() {
		if tool.Name == "create_file" || tool.Name == "edit_file" {
			if tool.InputSchema["properties"].(map[string]any)["content"].(map[string]any)["maxLength"] != int64(4) {
				t.Fatal("schema limit")
			}
		}
	}
}
