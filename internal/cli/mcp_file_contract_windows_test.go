package cli_test

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fileToolFixture struct {
	serverFixture
	client *http.Client
}

func startFileToolFixture(t *testing.T) fileToolFixture {
	t.Helper()
	bin := development(t)
	f := newServerFixture(t, bin)
	config := append(fixtureBytes(t, f.config), []byte("\nmcp_enabled: true\nmcp_skills_root: /Skills\nmcp_username: FixtureUser\nmcp_files_root: /Documents\nmcp_file_writes: true\n")...)
	if err := os.WriteFile(f.config, config, 0600); err != nil {
		t.Fatal(err)
	}
	startServer(t, bin, f)
	client := fixtureClient(f)
	t.Cleanup(client.CloseIdleConnections)
	fixture := fileToolFixture{f, client}
	fixture.dav(t, "MKCOL", "Documents", "", nil, 201)
	return fixture
}

func (f fileToolFixture) dav(t *testing.T, method, path, body string, headers map[string]string, want int) (http.Header, []byte) {
	t.Helper()
	status, h, data := fileRequest(t, f.client, f.serverFixture, method, "/dav/"+path, strings.NewReader(body), headers)
	if status != want {
		t.Fatalf("%s %s: %d, want %d", method, path, status, want)
	}
	return h, data
}

func (f fileToolFixture) startMCP(t *testing.T, version string) {
	t.Helper()
	if version == "2026-07-28" {
		var discovered struct {
			Versions []string `json:"supportedVersions"`
		}
		mcpResult(t, f.client, f.serverFixture, "server/discover", map[string]any{}, version, &discovered)
		if len(discovered.Versions) == 0 || discovered.Versions[0] != version {
			t.Fatalf("modern discovery = %v", discovered.Versions)
		}
		return
	}
	var init struct {
		Version string `json:"protocolVersion"`
	}
	mcpResult(t, f.client, f.serverFixture, "initialize", map[string]any{
		"protocolVersion": version, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "file-contract-fixture", "version": "1"},
	}, version, &init)
	if init.Version != version {
		t.Fatalf("legacy initialize chose %q, want %q", init.Version, version)
	}
}

type fileToolWireResult struct {
	IsError    bool                          `json:"isError"`
	Structured json.RawMessage               `json:"structuredContent"`
	Meta       map[string]any                `json:"_meta"`
	Content    []struct{ Type, Text string } `json:"content"`
}

func (f fileToolFixture) call(t *testing.T, version, name string, args any) (int, skillsWireReply, fileToolWireResult) {
	t.Helper()
	status, reply := mcpWire(t, f.client, f.serverFixture, "tools/call", map[string]any{"name": name, "arguments": args}, version, "FixtureUser", f.password)
	var result fileToolWireResult
	if len(reply.Result) > 0 {
		if err := json.Unmarshal(reply.Result, &result); err != nil {
			t.Fatalf("%s result: %v", name, err)
		}
	}
	return status, reply, result
}

func TestMCPFileOutputSchemas(t *testing.T) {
	f := startFileToolFixture(t)
	f.dav(t, "MKCOL", "Documents/Folder", "", nil, 201)
	const phrase = "amber orchard"
	first, _ := f.dav(t, "PUT", "Documents/Folder/Note.txt", phrase, map[string]string{"Content-Type": "text/plain"}, 201)
	f.dav(t, "PUT", "Documents/Second.txt", phrase, nil, 201)
	for _, version := range []string{"2026-07-28", "2025-11-25", "2025-06-18"} {
		t.Run(version, func(t *testing.T) {
			f.startMCP(t, version)
			var list struct {
				Tools []struct {
					Name   string         `json:"name"`
					Output map[string]any `json:"outputSchema"`
				} `json:"tools"`
			}
			mcpResult(t, f.client, f.serverFixture, "tools/list", map[string]any{}, version, &list)
			schemas := map[string]map[string]any{}
			for _, tool := range list.Tools {
				schemas[tool.Name] = tool.Output
			}
			call := func(name string, args map[string]any) map[string]any {
				t.Helper()
				status, reply, r := f.call(t, version, name, args)
				var value map[string]any
				var text any
				if status != 200 || reply.Error != nil || r.IsError || len(r.Content) != 1 || r.Content[0].Type != "text" ||
					json.Unmarshal(r.Structured, &value) != nil || value == nil ||
					json.Unmarshal([]byte(r.Content[0].Text), &text) != nil || !reflect.DeepEqual(value, text) {
					t.Fatalf("%s must return matching structured and readable success: HTTP %d %+v", name, status, reply.Error)
				}
				schema := schemas[name]
				if schema["type"] != "object" || schema["additionalProperties"] != false {
					t.Fatalf("%s did not publish a closed object outputSchema: %v", name, schema)
				}
				if err := checkFileResultSchema(schema, value); err != nil {
					t.Fatalf("%s result does not match discovered outputSchema: %v", name, err)
				}
				return value
			}
			page := call("list_files", map[string]any{"path": "", "limit": 1})
			if page["next_offset"] != float64(1) || page["entries"].([]any)[0].(map[string]any)["name"] != "Folder" {
				t.Fatalf("list page = %v", page)
			}
			last := call("list_files", map[string]any{"path": "", "offset": 1, "limit": 1})
			if _, more := last["next_offset"]; more || last["entries"].([]any)[0].(map[string]any)["name"] != "Second.txt" {
				t.Fatalf("last page = %v", last)
			}
			found := call("search_files", map[string]any{"path": "folder", "query": "note", "limit": 1})
			hit := found["entries"].([]any)[0].(map[string]any)
			if hit["path"] != "Folder/Note.txt" || hit["etag"] != first.Get("ETag") || found["partial"] != true || found["stop_reason"] != "result_limit" {
				t.Fatalf("path search = %v", found)
			}
			noHits := call("search_files", map[string]any{"path": "", "query": "no-matching-name"})
			if noHits["partial"] != false || len(noHits["entries"].([]any)) != 0 {
				t.Fatalf("empty path search = %v", noHits)
			}
			matches := call("search_file_contents", map[string]any{"query": phrase, "limit": 1})
			if matches["partial"] != true || len(matches["entries"].([]any)) != 1 {
				t.Fatalf("limited content search = %v", matches)
			}
			scoped := call("search_file_contents", map[string]any{"path": "folder", "query": phrase})
			if scoped["partial"] != false || len(scoped["entries"].([]any)) != 1 {
				t.Fatalf("scoped content search = %v", scoped)
			}
			contentHit := scoped["entries"].([]any)[0].(map[string]any)
			if contentHit["path"] != hit["path"] || contentHit["etag"] != hit["etag"] || contentHit["content_type"] != "text/plain" || contentHit["size"] != float64(len(phrase)) {
				t.Fatalf("content search metadata = %v", contentHit)
			}
			read := call("read_file", map[string]any{"path": hit["path"]})
			if read["content"] != phrase || read["file"].(map[string]any)["etag"] != hit["etag"] {
				t.Fatalf("read result = %v", read)
			}
			const created = "created.txt"
			made := call("create_file", map[string]any{"path": created, "content": "first"})
			tag := made["file"].(map[string]any)["etag"]
			if tag == "" || made["created"] != true {
				t.Fatalf("create result = %v", made)
			}
			edited := call("edit_file", map[string]any{"path": created, "content": "second", "expected_etag": tag})
			newTag := edited["file"].(map[string]any)["etag"]
			h, data := f.dav(t, "GET", "Documents/"+created, "", nil, 200)
			if newTag != h.Get("ETag") || newTag == tag || edited["created"] != false || string(data) != "second" {
				t.Fatalf("edit result = %v, WebDAV did not confirm the new bytes and ETag", edited)
			}
			removed := call("delete_file", map[string]any{"path": created, "expected_etag": newTag})
			if removed["deleted"] != true {
				t.Fatalf("delete result = %v", removed)
			}
			f.dav(t, "GET", "Documents/"+created, "", nil, 404)
		})
	}
}

func TestMCPFileToolFailureContract(t *testing.T) {
	f := startFileToolFixture(t)
	f.dav(t, "PUT", "Documents/Locked.txt", "stay", nil, 201)
	f.dav(t, "PUT", "Documents/Binary.txt", "binary\x00file", nil, 201)
	old, _ := f.dav(t, "PUT", "Documents/Updated.txt", "before", nil, 201)
	fresh, _ := f.dav(t, "PUT", "Documents/Updated.txt", "after", nil, 204)
	lock, _ := f.dav(t, "LOCK", "Documents/Locked.txt", `<?xml version="1.0"?><D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`, map[string]string{"Depth": "0", "Timeout": "Second-600", "Content-Type": "application/xml"}, 200)
	locked, _ := f.dav(t, "GET", "Documents/Locked.txt", "", nil, 200)
	if lock.Get("Lock-Token") == "" {
		t.Fatal("no lock created")
	}
	for _, version := range []string{"2026-07-28", "2025-11-25", "2025-06-18"} {
		t.Run(version, func(t *testing.T) {
			f.startMCP(t, version)
			fail := func(name string, args map[string]any, code, advice string) {
				t.Helper()
				status, reply, r := f.call(t, version, name, args)
				if status != 200 || reply.Error != nil || !r.IsError || r.Structured != nil ||
					r.Meta["io.godav/errorCode"] != code || len(r.Content) != 1 || r.Content[0].Type != "text" || !strings.Contains(r.Content[0].Text, advice) {
					t.Fatalf("%s: want %s tool failure with %q guidance; HTTP %d rpc=%+v result=%s", name, code, advice, status, reply.Error, reply.Result)
				}
				if version == "2026-07-28" && r.Meta["io.modelcontextprotocol/serverInfo"] == nil {
					t.Fatal("modern tool failure lost serverInfo")
				}
			}
			fail("create_file", map[string]any{"path": "Updated.txt", "content": "no overwrite"}, "already_exists", "Choose a different name")
			fail("edit_file", map[string]any{"path": "Updated.txt", "content": "no stale overwrite", "expected_etag": old.Get("ETag")}, "precondition_failed", "current contents and ETag")
			fail("delete_file", map[string]any{"path": "Updated.txt", "expected_etag": old.Get("ETag")}, "precondition_failed", "current contents and ETag")
			fail("edit_file", map[string]any{"path": "Locked.txt", "content": "blocked", "expected_etag": locked.Get("ETag")}, "locked", "Wait for the lock")
			fail("delete_file", map[string]any{"path": "Locked.txt", "expected_etag": locked.Get("ETag")}, "locked", "Wait for the lock")
			fail("create_file", map[string]any{"path": "Missing/Note.txt", "content": "no parent"}, "parent_not_found", "create the folder through WebDAV")
			fail("read_file", map[string]any{"path": "Missing.txt"}, "not_found", "Use list_files")
			fail("read_file", map[string]any{"path": "Binary.txt"}, "unsupported_content", "Use WebDAV for binary files")
			fail("edit_file", map[string]any{"path": "Updated.txt", "content": "missing revision"}, "invalid_arguments", "required ETag")
			for _, req := range []struct {
				name string
				args any
			}{
				{"unknown_file_tool", map[string]any{}},
				{"read_file", []any{"looks like args but not an object"}},
			} {
				_, reply, r := f.call(t, version, req.name, req.args)
				if reply.Error == nil || reply.Error.Code != -32602 || reply.Result != nil || r.IsError {
					t.Fatalf("%s: wanted RPC invalid params, got %+v", req.name, reply)
				}
			}
			h, data := f.dav(t, "GET", "Documents/Updated.txt", "", nil, 200)
			if h.Get("ETag") != fresh.Get("ETag") || string(data) != "after" {
				t.Fatal("precondition failure changed the actual WebDAV file")
			}
			h, data = f.dav(t, "GET", "Documents/Locked.txt", "", nil, 200)
			if h.Get("ETag") != locked.Get("ETag") || string(data) != "stay" {
				t.Fatal("lock failure changed the actual WebDAV file")
			}
		})
	}
}

// Verify the JSON Schema keywords used by the advertised contracts against
// deserialized wire values; this is deliberately not a general-purpose validator.
func checkFileResultSchema(schema map[string]any, value any) error {
	if len(schema) == 0 {
		return fmt.Errorf("missing schema")
	}
	fail := func(want string) error { return fmt.Errorf("expected %s, got %T (%v)", want, value, value) }
	for keyword, rule := range schema {
		switch keyword {
		case "description", "if", "then", "else":
			// An if/then pair is tested as a unit below.
		case "type":
			switch rule {
			case "object":
				if _, ok := value.(map[string]any); !ok {
					return fail("object")
				}
			case "array":
				if _, ok := value.([]any); !ok {
					return fail("array")
				}
			case "boolean":
				if _, ok := value.(bool); !ok {
					return fail("boolean")
				}
			case "string":
				if _, ok := value.(string); !ok {
					return fail("string")
				}
			case "integer":
				if n, ok := value.(float64); !ok || math.Trunc(n) != n {
					return fail("integer")
				}
			default:
				return fmt.Errorf("unexpected schema type: %v", rule)
			}
		case "items":
			if items, ok := value.([]any); ok {
				for i, item := range items {
					if err := checkFileResultSchema(rule.(map[string]any), item); err != nil {
						return fmt.Errorf("item %d: %w", i, err)
					}
				}
			}
		case "properties":
			if fields, ok := value.(map[string]any); ok {
				props := rule.(map[string]any)
				for name, got := range fields {
					if p, exists := props[name]; exists {
						if err := checkFileResultSchema(p.(map[string]any), got); err != nil {
							return fmt.Errorf("property %s: %w", name, err)
						}
					} else if schema["additionalProperties"] == false {
						return fmt.Errorf("unexpected property %s", name)
					}
				}
			}
		case "additionalProperties":
			if rule != false {
				return fmt.Errorf("unexpected additionalProperties: %v", rule)
			}
		case "required":
			if fields, ok := value.(map[string]any); ok {
				for _, name := range rule.([]any) {
					if _, exists := fields[name.(string)]; !exists {
						return fmt.Errorf("missing property %s", name)
					}
				}
			}
		case "enum":
			found := false
			for _, choice := range rule.([]any) {
				found = found || reflect.DeepEqual(choice, value)
			}
			if !found {
				return fail(fmt.Sprint(rule))
			}
		case "minimum":
			if n, ok := value.(float64); !ok || n < rule.(float64) {
				return fail("number at least " + fmt.Sprint(rule))
			}
		case "format":
			got, ok := value.(string)
			_, err := time.Parse(time.RFC3339Nano, got)
			if rule != "date-time" || !ok || err != nil {
				return fail("RFC3339 timestamp")
			}
		case "allOf":
			for _, child := range rule.([]any) {
				if err := checkFileResultSchema(child.(map[string]any), value); err != nil {
					return err
				}
			}
		case "not":
			if checkFileResultSchema(rule.(map[string]any), value) == nil {
				return fail("value not matching excluded schema")
			}
		default:
			return fmt.Errorf("unsupported test schema keyword %q", keyword)
		}
	}
	if condition, ok := schema["if"].(map[string]any); ok {
		branch := "else"
		if checkFileResultSchema(condition, value) == nil {
			branch = "then"
		}
		if target, ok := schema[branch].(map[string]any); ok {
			return checkFileResultSchema(target, value)
		}
	}
	return nil
}
