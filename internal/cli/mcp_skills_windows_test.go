package cli_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// All inputs are pinned, inert upstream data. No network acquisition or skill
// execution takes place during tests; only disposable HTTPS/WebDAV state changes.
func mcpPrototypeFixture(t *testing.T) map[string][]byte {
	t.Helper()
	hashes := map[string]string{
		"SKILL.md": "714de632d116bb73f65cdb5a882db15b9369a6713b9a47c0fad827848f0bfbe3",
		"LOGIC.md": "f61c7d249e786a79ef289018901c348271e1798dd0b0bc5607b5c6f4d4a01ab9",
		"UI.md":    "723211e878acbc7b6ff09755263f3295cde724ba902ff0064da41eed51d45ad3",
	}
	hashes["agents/openai.yaml"] = "5af65e43ab41a350436697b81e27b7f848d36782043b73c322bb2c9fa9cc55dc"
	files := make(map[string][]byte)
	for name, hash := range hashes {
		data, err := os.ReadFile(filepath.Join("testdata", "mattpocock-prototype", name))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != hash {
			t.Fatalf("pinned fixture %s changed", name)
		}
		files[name] = data
	}
	return files
}

func TestMCPSkillsAnonymousHTTPRemainsWebDAVOnly(t *testing.T) {
	origin, client, _ := startHTTPTest(t)
	httpTestRequest(t, client, origin, "OPTIONS", "/dav/", "", nil, 200)
	for _, method := range []string{"GET", "POST"} {
		httpTestRequest(t, client, origin, method, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"skills/list"}`, map[string]string{"Content-Type": "application/json"}, 404)
	}
}

type skillsWireReply struct {
	CacheControl string          `json:"-"`
	Result       json.RawMessage `json:"result"`
	Error        *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type skillsWireEntry struct {
	URI         string         `json:"uri"`
	Frontmatter map[string]any `json:"frontmatter"`
	Resources   []struct {
		URI    string `json:"uri"`
		Digest string `json:"digest"`
		Size   int    `json:"size"`
	} `json:"resources"`
	Meta map[string]any `json:"_meta"`
}

func mcpWire(t *testing.T, client *http.Client, f serverFixture, method string, params map[string]any, version, user, password string) (int, skillsWireReply) {
	return mcpWireAt(t, client, f, "/mcp", method, params, version, user, password)
}

func mcpWireAt(t *testing.T, client *http.Client, f serverFixture, endpoint, method string, params map[string]any, version, user, password string) (int, skillsWireReply) {
	t.Helper()
	if version == "2026-07-28" {
		params["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": version, "io.modelcontextprotocol/clientInfo": map[string]any{"name": "skills-fixture", "version": "1"}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", f.origin+endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", version)
	req.Header.Set("Mcp-Method", method)
	if name, ok := params["name"].(string); ok {
		req.Header.Set("Mcp-Name", name)
	}
	if uri, ok := params["uri"].(string); ok && method == "resources/read" {
		req.Header.Set("Mcp-Name", uri)
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	var reply skillsWireReply
	if response.StatusCode == http.StatusOK || json.Valid(data) {
		if err = json.Unmarshal(data, &reply); err != nil {
			t.Fatalf("%s JSON response: %v", method, err)
		}
	}
	reply.CacheControl = response.Header.Get("Cache-Control")
	return response.StatusCode, reply
}

func mcpResult(t *testing.T, client *http.Client, f serverFixture, method string, params map[string]any, version string, dest any) {
	t.Helper()
	status, reply := mcpWire(t, client, f, method, params, version, "FixtureUser", f.password)
	if status != 200 || reply.Error != nil {
		t.Fatalf("%s failed: status=%d error=%+v", method, status, reply.Error)
	}
	if err := json.Unmarshal(reply.Result, dest); err != nil {
		t.Fatalf("%s result: %v", method, err)
	}
}

func assertMCPResource(t *testing.T, client *http.Client, f serverFixture, version, uri string, want []byte) {
	t.Helper()
	var result struct {
		Contents []struct {
			URI  string  `json:"uri"`
			Text *string `json:"text"`
			Blob string  `json:"blob"`
		} `json:"contents"`
	}
	mcpResult(t, client, f, "resources/read", map[string]any{"uri": uri}, version, &result)
	if len(result.Contents) != 1 || result.Contents[0].URI != uri {
		t.Fatal("resource identity mismatch")
	}
	item := result.Contents[0]
	var got []byte
	if item.Text != nil {
		got = []byte(*item.Text)
	} else {
		var err error
		got, err = base64.StdEncoding.DecodeString(item.Blob)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("resource bytes differ: %s", uri)
	}
}

func TestMCPSkillsPinnedPrototypeEndToEnd(t *testing.T) {
	const modern = "2026-07-28"
	const legacy = "2025-11-25"
	files := mcpPrototypeFixture(t)
	bin := development(t)
	f := newServerFixture(t, bin)
	config := fixtureBytes(t, f.config)
	config = append(config, []byte("\nmcp_enabled: true\nmcp_skills_root: /Skills\nmcp_username: FixtureUser\n")...)
	if err := os.WriteFile(f.config, config, 0600); err != nil {
		t.Fatal(err)
	}
	// A second valid account must not inherit the dedicated MCP account's access.
	console, out, stderr, code := privateConsoleCommand(t, t.TempDir(), bin, "add", "user", "--config", f.config, "--username", "OtherUser")
	if code != 0 {
		t.Fatal("second account setup failed")
	}
	otherPassword := accountSecret(t, console, out, stderr)
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	for _, path := range []string{"/dav/Skills", "/dav/Skills/prototype", "/dav/Skills/prototype/agents"} {
		status, _, _ := fileRequest(t, client, f, "MKCOL", path, nil, nil)
		if status != 201 {
			t.Fatalf("MKCOL %s: %d", path, status)
		}
	}
	for name, data := range files {
		status, _, _ := fileRequest(t, client, f, "PUT", "/dav/Skills/prototype/"+name, bytes.NewReader(data), nil)
		if status != 201 {
			t.Fatalf("PUT %s: %d", name, status)
		}
	}
	ingestMCPCollectionExtras(t, client, f)
	for _, tc := range []struct {
		user, password string
		status         int
	}{{"", "", 401}, {"FixtureUser", "wrong", 401}, {"OtherUser", otherPassword, 403}} {
		status, _ := mcpWire(t, client, f, "skills/list", map[string]any{}, modern, tc.user, tc.password)
		if status != tc.status {
			t.Fatalf("MCP auth status=%d want=%d", status, tc.status)
		}
	}
	var discover map[string]any
	mcpResult(t, client, f, "server/discover", map[string]any{}, modern, &discover)
	var list struct {
		Skills []skillsWireEntry `json:"skills"`
	}
	mcpResult(t, client, f, "skills/list", map[string]any{}, modern, &list)
	if len(list.Skills) != 3 {
		t.Fatalf("catalog skills=%d want=3", len(list.Skills))
	}
	var original skillsWireEntry
	seenPackages := map[string]bool{}
	for _, entry := range list.Skills {
		name, ok := entry.Frontmatter["name"].(string)
		if !ok || (name != "prototype" && name != "code-review" && name != "research") || seenPackages[name] || entry.URI != "skill://"+name+"/SKILL.md" {
			t.Fatal("collection has duplicate or unexpected package identity")
		}
		seenPackages[name] = true
		if name != "prototype" && len(entry.Resources) != 2 {
			t.Fatalf("incomplete package manifest: %s", name)
		}
		for _, resource := range entry.Resources {
			path := strings.TrimPrefix(resource.URI, "skill://"+name+"/")
			want, err := os.ReadFile(filepath.Join("testdata", "mattpocock-"+name, filepath.FromSlash(path)))
			if err != nil {
				t.Fatal(err)
			}
			if resource.Size != len(want) || resource.Digest != fmt.Sprintf("sha256:%x", sha256.Sum256(want)) {
				t.Fatal("collection resource integrity differs from pinned bytes")
			}
			assertMCPResource(t, client, f, modern, resource.URI, want)
		}
		if entry.URI == "skill://prototype/SKILL.md" {
			original = entry
		}
	}
	if original.URI != "skill://prototype/SKILL.md" || original.Frontmatter["name"] != "prototype" || original.Frontmatter["description"] == "" || len(original.Resources) != 4 {
		t.Fatalf("catalog manifest mismatch: %+v", original)
	}
	revision, ok := original.Meta["io.godav/revision"].(string)
	if !ok || revision == "" {
		t.Fatal("manifest revision missing")
	}
	checkManifest := func(entry skillsWireEntry, expected map[string][]byte) {
		t.Helper()
		if len(entry.Resources) != len(expected) {
			t.Fatal("manifest omitted resource")
		}
		seen := map[string]bool{}
		for _, r := range entry.Resources {
			const prefix = "skill://prototype/"
			name := strings.TrimPrefix(r.URI, prefix)
			data, exists := expected[name]
			if !exists || seen[name] || r.Size != len(data) || r.Digest != fmt.Sprintf("sha256:%x", sha256.Sum256(data)) {
				t.Fatalf("resource manifest mismatch: %+v", r)
			}
			seen[name] = true
		}
	}
	checkManifest(original, files)
	var get struct {
		Skill skillsWireEntry `json:"skill"`
	}
	mcpResult(t, client, f, "skills/get", map[string]any{"uri": original.URI, "expectedRevision": revision}, modern, &get)
	if !reflect.DeepEqual(get.Skill, original) {
		t.Fatal("list/get manifest differs")
	}
	for name, data := range files {
		assertMCPResource(t, client, f, modern, "skill://prototype/"+name, data)
		status, _, got := fileRequest(t, client, f, "GET", "/dav/Skills/prototype/"+name, nil, nil)
		if status != 200 || !bytes.Equal(got, data) {
			t.Fatalf("WebDAV byte parity failed: %s", name)
		}
	}
	// Real legacy wire negotiation, then ordinary tools fallback. No native skills
	// method is used on this client path.
	var init map[string]any
	mcpResult(t, client, f, "initialize", map[string]any{"protocolVersion": legacy, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "fixture-legacy", "version": "1"}}, legacy, &init)
	var toolsList struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	mcpResult(t, client, f, "tools/list", map[string]any{}, legacy, &toolsList)
	names := map[string]bool{}
	for _, tool := range toolsList.Tools {
		names[tool.Name] = true
	}
	if !names["list_skills"] || !names["get_skill"] {
		t.Fatal("fallback tools not advertised")
	}
	type toolReply struct {
		Structured json.RawMessage `json:"structuredContent"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	callTool := func(name string, args map[string]any) toolReply {
		t.Helper()
		var result toolReply
		mcpResult(t, client, f, "tools/call", map[string]any{"name": name, "arguments": args}, legacy, &result)
		return result
	}
	fallback := callTool("list_skills", map[string]any{})
	var fallbackList struct {
		Skills []skillsWireEntry `json:"skills"`
	}
	if fallback.IsError || json.Unmarshal(fallback.Structured, &fallbackList) != nil || !reflect.DeepEqual(fallbackList.Skills, list.Skills) {
		t.Fatal("native/fallback catalog differs")
	}
	for name, want := range files {
		fallback = callTool("get_skill", map[string]any{"uri": "skill://prototype/" + name, "expectedRevision": revision})
		var fetched struct {
			Skill    skillsWireEntry `json:"skill"`
			Contents []struct {
				Text string `json:"text"`
			} `json:"contents"`
		}
		if fallback.IsError || json.Unmarshal(fallback.Structured, &fetched) != nil || !reflect.DeepEqual(fetched.Skill, original) || len(fetched.Contents) != 1 || !bytes.Equal([]byte(fetched.Contents[0].Text), want) {
			t.Fatalf("fallback bytes/manifest differ: %s", name)
		}
		if len(fallback.Content) != 1 || fallback.Content[0].Type != "text" || !json.Valid([]byte(fallback.Content[0].Text)) {
			t.Fatal("fallback JSON text content missing")
		}
		var textResult, structuredResult any
		if json.Unmarshal([]byte(fallback.Content[0].Text), &textResult) != nil || json.Unmarshal(fallback.Structured, &structuredResult) != nil || !reflect.DeepEqual(textResult, structuredResult) {
			t.Fatal("fallback text-only client sees different result")
		}
	}
	// Only a supporting document changes: SKILL.md's version/hash is insufficient.
	changed := append(append([]byte(nil), files["LOGIC.md"]...), []byte("\n<!-- disposable integrity test -->\n")...)
	status, _, _ := fileRequest(t, client, f, "PUT", "/dav/Skills/prototype/LOGIC.md", bytes.NewReader(changed), nil)
	if status != 204 {
		t.Fatalf("supporting file edit: %d", status)
	}
	var updated struct {
		Skills []skillsWireEntry `json:"skills"`
	}
	mcpResult(t, client, f, "skills/list", map[string]any{}, modern, &updated)
	var updatedPrototype skillsWireEntry
	for _, entry := range updated.Skills {
		if entry.URI == original.URI {
			updatedPrototype = entry
		} else {
			for _, old := range list.Skills {
				if old.URI == entry.URI && !reflect.DeepEqual(old, entry) {
					t.Fatal("supporting-file edit changed unrelated package manifest")
				}
			}
		}
	}
	if len(updated.Skills) != 3 || updatedPrototype.Meta["io.godav/revision"] == revision {
		t.Fatal("supporting-file-only edit did not invalidate manifest")
	}
	files["LOGIC.md"] = changed
	checkManifest(updatedPrototype, files)
	for _, method := range []string{"skills/get", "resources/read"} {
		status, reply := mcpWire(t, client, f, method, map[string]any{"uri": original.URI, "expectedRevision": revision}, modern, "FixtureUser", f.password)
		if status != 200 || reply.Error == nil || reply.Error.Code != -32602 || len(reply.Result) != 0 {
			t.Fatalf("stale manifest accepted by %s", method)
		}
	}
	if stale := callTool("get_skill", map[string]any{"uri": original.URI, "expectedRevision": revision}); !stale.IsError {
		t.Fatal("fallback accepted stale manifest")
	}
	fresh := callTool("get_skill", map[string]any{"uri": "skill://prototype/LOGIC.md", "expectedRevision": updatedPrototype.Meta["io.godav/revision"]})
	var freshResult struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	if fresh.IsError || json.Unmarshal(fresh.Structured, &freshResult) != nil || len(freshResult.Contents) != 1 || !bytes.Equal([]byte(freshResult.Contents[0].Text), changed) {
		t.Fatal("fallback cannot retrieve revised supporting file with fresh manifest")
	}
	for name, want := range files {
		assertMCPResource(t, client, f, modern, "skill://prototype/"+name, want)
		status, _, got := fileRequest(t, client, f, "GET", "/dav/Skills/prototype/"+name, nil, nil)
		if status != 200 || !bytes.Equal(got, want) {
			t.Fatalf("post-edit parity failed: %s", name)
		}
	}
	client.CloseIdleConnections()
	p.stop()
	assertAccountServerLogs(t, p, [2]string{"FixtureUser", f.password}, [2]string{"OtherUser", otherPassword})
}

func ingestMCPCollectionExtras(t *testing.T, client *http.Client, f serverFixture) {
	t.Helper()
	packages := map[string]map[string]string{
		"code-review": {"SKILL.md": "9c921a8d88138a41401d8897ca1bb852088ab1b7b605cdd04edc13e3e8aab6dd", "agents/openai.yaml": "8229ca854e11dc8e6aef2131ee03f31fb1561cf905fab9ccc325180cf3331352"},
		"research":    {"SKILL.md": "1f629e18ab899412683bc6a1c4efb4339999e27c2fdac328f26ee10a8c031d33", "agents/openai.yaml": "9b4c470d63221c1f68f22df70b83e2f12401b317babe0d1b7b5f24a974474d0d"},
	}
	for name, files := range packages {
		for _, suffix := range []string{"", "/agents"} {
			status, _, _ := fileRequest(t, client, f, "MKCOL", "/dav/Skills/"+name+suffix, nil, nil)
			if status != 201 {
				t.Fatalf("extra package MKCOL: %d", status)
			}
		}
		for path, hash := range files {
			data, err := os.ReadFile(filepath.Join("testdata", "mattpocock-"+name, filepath.FromSlash(path)))
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(data)) != hash {
				t.Fatalf("pinned %s/%s changed", name, path)
			}
			status, _, _ := fileRequest(t, client, f, "PUT", "/dav/Skills/"+name+"/"+path, bytes.NewReader(data), nil)
			if status != 201 {
				t.Fatalf("extra package PUT: %d", status)
			}
		}
	}
}
