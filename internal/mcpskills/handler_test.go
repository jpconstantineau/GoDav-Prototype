package mcpskills

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

type memoryRepo struct {
	files.Repository
	data       map[string][]byte
	reads      []string
	onSnapshot func(string)
}

func (m *memoryRepo) CollectionSnapshot(_ context.Context, names []string, children bool, _ ...files.Expectation) (files.Snapshot, error) {
	path := strings.Join(names, "/")
	if m.onSnapshot != nil {
		m.onSnapshot(path)
	}
	out := files.Snapshot{Path: path, Node: files.Node{ID: path, Name: path}}
	if b, ok := m.data[path]; ok {
		out.Node.File = true
		out.Node.Size = int64(len(b))
		out.Node.ETag = digest(b)
		return out, nil
	}
	prefix := path + "/"
	seen := map[string]bool{}
	for p, b := range m.data {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(p, prefix)
		part := strings.Split(suffix, "/")[0]
		if seen[part] {
			continue
		}
		seen[part] = true
		file := !strings.Contains(suffix, "/")
		n := files.Node{ID: prefix + part, Name: part, File: file}
		if file {
			n.Size = int64(len(b))
			n.ETag = digest(b)
		}
		out.Children = append(out.Children, n)
	}
	if len(seen) == 0 {
		return files.Snapshot{}, files.ErrMissing
	}
	return out, nil
}
func (m *memoryRepo) ReadFile(_ context.Context, names []string, e ...files.Expectation) (files.Snapshot, []byte, error) {
	p := strings.Join(names, "/")
	m.reads = append(m.reads, p)
	b, ok := m.data[p]
	if !ok {
		return files.Snapshot{}, nil, files.ErrMissing
	}
	node := files.Node{ID: p, File: true, ETag: digest(b), ContentType: "application/octet-stream", Size: int64(len(b))}
	for _, x := range e {
		if err := x.Check(node, true); err != nil {
			return files.Snapshot{}, nil, err
		}
	}
	return files.Snapshot{Path: p, Node: node}, bytes.Clone(b), nil
}
func fixture(t *testing.T) (http.Handler, *memoryRepo) {
	t.Helper()
	repo := &memoryRepo{data: map[string][]byte{"Skills/demo/SKILL.md": []byte("---\nname: demo\ndescription: Test skill\ncustom:\n  enabled: true\n  tags: [one, two]\n---\nRead references/guide.txt.\n"), "Skills/demo/references/guide.txt": []byte("hello\r\n"), "Skills/demo/asset.bin": {0xff, 0, 1}, "Private/secret.txt": []byte("private")}}
	h, e := New(files.New(repo), "Skills")
	if e != nil {
		t.Fatal(e)
	}
	return h, repo
}
func rpc(t *testing.T, h http.Handler, method string, p map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	if p == nil {
		p = map[string]any{}
	}
	p["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": ProtocolVersion, "io.modelcontextprotocol/clientInfo": map[string]any{"name": "test", "version": "1"}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": p})
	r := httptest.NewRequest("POST", "https://localhost/mcp", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	r.Header.Set("Mcp-Method", method)
	if method == "tools/call" {
		r.Header.Set("Mcp-Name", p["name"].(string))
	}
	if method == "resources/read" {
		r.Header.Set("Mcp-Name", p["uri"].(string))
	}
	r = r.WithContext(auth.WithPrincipal(r.Context(), "alice"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result map[string]any
	if json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	return w, result
}
func TestCatalogNativeFallbackParity(t *testing.T) {
	h, repo := fixture(t)
	w, v := rpc(t, h, "skills/list", nil)
	if w.Code != 200 || v["error"] != nil {
		t.Fatalf("%v", v)
	}
	result := v["result"].(map[string]any)
	entries := result["skills"].([]any)
	if len(entries) != 1 {
		t.Fatal(entries)
	}
	entry := entries[0].(map[string]any)
	fm := entry["frontmatter"].(map[string]any)
	if fm["custom"] == nil {
		t.Fatal("lost author field")
	}
	resources := entry["resources"].([]any)
	if len(resources) != 3 {
		t.Fatal(resources)
	}
	revision := entry["_meta"].(map[string]any)["io.godav/revision"].(string)
	_, got := rpc(t, h, "skills/get", map[string]any{"uri": "skill://demo/SKILL.md"})
	a, _ := json.Marshal(entry)
	b, _ := json.Marshal(got["result"].(map[string]any)["skill"])
	if !bytes.Equal(a, b) {
		t.Fatalf("list/get differ %s %s", a, b)
	}
	for _, raw := range resources {
		res := raw.(map[string]any)
		uri := res["uri"].(string)
		_, v := rpc(t, h, "resources/read", map[string]any{"uri": uri, "expectedRevision": revision})
		items := v["result"].(map[string]any)["contents"].([]any)
		resource := items[0].(map[string]any)
		var data []byte
		if txt, ok := resource["text"].(string); ok {
			data = []byte(txt)
		} else {
			var e error
			data, e = base64.StdEncoding.DecodeString(resource["blob"].(string))
			if e != nil {
				t.Fatal(e)
			}
		}
		if digest(data) != res["digest"] || float64(len(data)) != res["size"] {
			t.Fatal("digest mismatch")
		}
		_, fallback := rpc(t, h, "tools/call", map[string]any{"name": "get_skill", "arguments": map[string]any{"uri": uri, "expectedRevision": revision}})
		structured := fallback["result"].(map[string]any)["structuredContent"].(map[string]any)
		j, _ := json.Marshal(structured["contents"])
		k, _ := json.Marshal(items)
		if !bytes.Equal(j, k) {
			t.Fatal("native/fallback bytes differ")
		}
	}
	repo.data["Skills/demo/references/guide.txt"] = []byte("changed")
	_, stale := rpc(t, h, "resources/read", map[string]any{"uri": "skill://demo/SKILL.md", "expectedRevision": revision})
	if stale["error"] == nil {
		t.Fatal("accepted old package revision")
	}
	for _, p := range repo.reads {
		if !strings.HasPrefix(p, "Skills/") {
			t.Fatalf("cross scope %s", p)
		}
	}
}
func TestURIConfinement(t *testing.T) {
	for _, uri := range []string{"file:///Private/secret.txt", "skill://../secret", "skill://demo/%2e%2e/secret", "skill://demo/references%2Fguide.txt", "skill://demo/%53KILL.md", "skill://demo/SKILL.md?x=1", "skill://Demo/SKILL.md", "skill://demo/SKILL.md/", "skill://demo//SKILL.md"} {
		if _, e := canonicalURI(uri); e == nil {
			t.Fatalf("accepted %s", uri)
		}
	}
	h, _ := fixture(t)
	_, v := rpc(t, h, "resources/read", map[string]any{"uri": "skill://private/secret.txt"})
	if v["error"] == nil {
		t.Fatal("unknown URI accepted")
	}
}
func TestFrontmatterValidation(t *testing.T) {
	for _, s := range []string{"name: demo", "---\nname: other\ndescription: test\n---\n", "---\nname: demo\ndescription: test\nname: demo\n---\n", "---\nname: demo\ndescription: &x test\nother: *x\n---\n", "---\nname: demo\ndescription: test\nwhen: 2026-01-01\n---\n"} {
		if _, e := frontmatter([]byte(s), "demo"); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestBoundsAndFailClosed(t *testing.T) {
	h, repo := fixture(t)
	repo.data["Skills/demo/large.bin"] = make([]byte, MaxFileBytes+1)
	_, v := rpc(t, h, "skills/list", nil)
	if v["error"] == nil {
		t.Fatal("large file accepted")
	}
	for _, scope := range []string{"", "/Skills", "Skills/", "../Skills", "Skills/../Private"} {
		if _, e := New(files.New(repo), scope); e == nil {
			t.Fatalf("accepted scope %q", scope)
		}
	}
}
func TestAuthAndHTTPBoundaries(t *testing.T) {
	h, _ := fixture(t)
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	r = r.WithContext(auth.WithPrincipal(r.Context(), "alice"))
	r.Method = "GET"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
	if got := w.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q, want POST", got)
	}
	w, v := rpc(t, h, "server/discover", nil)
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("discovery Content-Type = %q, want application/json", got)
	}
	result := v["result"].(map[string]any)
	if result["capabilities"].(map[string]any)["extensions"] == nil {
		t.Fatal("no native capability")
	}
	w, v = rpc(t, h, "execute", nil)
	if w.Code != 404 || v["error"] == nil {
		t.Fatal("unexpected method")
	}
}
func TestJSONBoundaries(t *testing.T) {
	for _, v := range []string{`{"x":1,"x":2}`, `{} {}`, `[`, strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40)} {
		if validJSON([]byte(v)) {
			t.Fatalf("accepted %s", v)
		}
	}
	if !validJSON([]byte(`{"x":[1,true,null,"s"]}`)) {
		t.Fatal("valid rejected")
	}
}
func TestExtraToolPrincipalAndRedaction(t *testing.T) {
	_, repo := fixture(t)
	h, e := NewWithTools(files.New(repo), "Skills", []Tool{{Name: "extra", Description: "test", InputSchema: map[string]any{"type": "object"}, Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
		if auth.Principal(ctx) != "alice" {
			t.Fatal("principal lost")
		}
		return nil, errors.New("secret detail")
	}}})
	if e != nil {
		t.Fatal(e)
	}
	_, v := rpc(t, h, "tools/call", map[string]any{"name": "extra", "arguments": map[string]any{}})
	b, _ := json.Marshal(v)
	if bytes.Contains(b, []byte("secret detail")) {
		t.Fatal("error leaked")
	}
}

func TestTransportMetadataAndLegacy(t *testing.T) {
	h, _ := fixture(t)
	for _, version := range []string{LegacyVersion, EarlierLegacyVersion} {
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + version + `","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r = r.WithContext(auth.WithPrincipal(r.Context(), "alice"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"protocolVersion":"`+version+`"`) {
			t.Fatalf("legacy %s: %d %s", version, w.Code, w.Body.String())
		}
	}
	meta := map[string]any{"io.modelcontextprotocol/protocolVersion": ProtocolVersion, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{"_meta": meta}})
	for _, tc := range []struct {
		name, method, accept string
		want                 int
	}{{"optional-client-info", "tools/list", "application/json, text/event-stream", 200}, {"method-mismatch", "ping", "application/json, text/event-stream", 400}, {"missing-method", "", "application/json, text/event-stream", 400}, {"zero-quality", "tools/list", "application/json;q=0.0, text/event-stream", 406}} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", tc.accept)
			r.Header.Set("MCP-Protocol-Version", ProtocolVersion)
			r.Header.Set("Mcp-Method", tc.method)
			r = r.WithContext(auth.WithPrincipal(r.Context(), "alice"))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestInvalidDraftDoesNotHideValidSkill(t *testing.T) {
	h, repo := fixture(t)
	repo.data["Skills/draft/readme.txt"] = []byte("not a published skill")
	repo.data["Skills/Invalid/SKILL.md"] = []byte("invalid name")
	repo.data["Skills/broken/SKILL.md"] = []byte("---\nname: broken\ndescription: [not, text]\n---\n")
	_, v := rpc(t, h, "skills/list", nil)
	if v["error"] != nil {
		t.Fatal(v)
	}
	entries := v["result"].(map[string]any)["skills"].([]any)
	if len(entries) != 1 {
		t.Fatalf("%v", entries)
	}
	for _, uri := range []string{"skill://draft/readme.txt", "skill://broken/SKILL.md"} {
		_, v := rpc(t, h, "resources/read", map[string]any{"uri": uri})
		if v["error"] == nil {
			t.Fatalf("served unpublished %s", uri)
		}
	}
}

func TestMutationDuringCatalogFailsClosed(t *testing.T) {
	h, repo := fixture(t)
	calls := 0
	repo.onSnapshot = func(path string) {
		if path == "Skills/demo" {
			calls++
			if calls == 2 {
				repo.data["Skills/demo/new.txt"] = []byte("new member")
			}
		}
	}
	_, v := rpc(t, h, "skills/list", nil)
	if v["error"] == nil {
		t.Fatal("published known stale catalog")
	}
}

// Use a literal SHA-256 test vector, not the production digest helper: native
// and fallback parity alone cannot detect the same digest-format bug in both.
func TestSkillManifestDigestKnownVector(t *testing.T) {
	h, _ := fixture(t)
	_, response := rpc(t, h, "skills/get", map[string]any{"uri": "skill://demo/SKILL.md"})
	if response["error"] != nil {
		t.Fatal(response)
	}
	skill := response["result"].(map[string]any)["skill"].(map[string]any)
	for _, value := range skill["resources"].([]any) {
		resource := value.(map[string]any)
		if resource["uri"] != "skill://demo/references/guide.txt" {
			continue
		}
		// Fixture bytes are exactly "hello\r\n", seven bytes, without BOM.
		const want = "sha256:cd2eca3535741f27a8ae40c31b0c41d4057a7a7b912b33b9aed86485d1c84676"
		if resource["digest"] != want || resource["size"] != float64(7) {
			t.Fatalf("guide digest/size = %v/%v, want %s/7", resource["digest"], resource["size"], want)
		}
		return
	}
	t.Fatal("guide resource missing from manifest")
}

func TestSkillEntryMIMEAcrossReadSurfaces(t *testing.T) {
	h, _ := fixture(t)
	for _, method := range []string{"resources/read", "tools/call"} {
		t.Run(method, func(t *testing.T) {
			params := map[string]any{"uri": "skill://demo/SKILL.md"}
			if method == "tools/call" {
				params = map[string]any{"name": "get_skill", "arguments": params}
			}
			_, response := rpc(t, h, method, params)
			if response["error"] != nil {
				t.Fatal(response)
			}
			result := response["result"].(map[string]any)
			if method == "tools/call" {
				if result["isError"] == true {
					t.Fatal(result)
				}
				result = result["structuredContent"].(map[string]any)
			}
			content := result["contents"].([]any)[0].(map[string]any)
			if content["mimeType"] != "text/markdown" {
				t.Fatalf("SKILL.md mimeType = %v, want text/markdown", content["mimeType"])
			}
		})
	}
}
