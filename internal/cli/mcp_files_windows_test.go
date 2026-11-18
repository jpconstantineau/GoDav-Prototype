package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestMCPFileToolsAcrossWebDAV(t *testing.T) {
	bin := development(t)
	for _, writes := range []bool{false, true} {
		name := "read only"
		if writes {
			name = "writes enabled"
		}
		t.Run(name, func(t *testing.T) {
			f := newServerFixture(t, bin)
			config := append(fixtureBytes(t, f.config), []byte("\nmcp_enabled: true\nmcp_skills_root: /Skills\nmcp_username: FixtureUser\nmcp_files_root: /Documents\n")...)
			if writes {
				config = append(config, []byte("mcp_file_writes: true\n")...)
			}
			if err := os.WriteFile(f.config, config, 0600); err != nil {
				t.Fatal(err)
			}
			startServer(t, bin, f)
			client := fixtureClient(f)
			defer client.CloseIdleConnections()
			dav := func(method, path, body string, set map[string]string, status int) (http.Header, []byte) {
				t.Helper()
				req, err := http.NewRequest(method, f.origin+"/dav/"+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.SetBasicAuth("FixtureUser", f.password)
				for k, v := range set {
					req.Header.Set(k, v)
				}
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
				if err != nil {
					t.Fatal(err)
				}
				if res.StatusCode != status {
					t.Fatalf("%s %s: status = %d, want %d", method, path, res.StatusCode, status)
				}
				return res.Header, data
			}
			const version = "2025-11-25"
			type toolResult struct {
				IsError    bool            `json:"isError"`
				Structured json.RawMessage `json:"structuredContent"`
			}
			call := func(tool string, args map[string]any, wantOK bool) json.RawMessage {
				t.Helper()
				status, reply := mcpWire(t, client, f, "tools/call", map[string]any{"name": tool, "arguments": args}, version, "FixtureUser", f.password)
				var tr toolResult
				if len(reply.Result) > 0 && json.Unmarshal(reply.Result, &tr) != nil {
					t.Fatal("bad MCP tool response")
				}
				gotOK := status == 200 && reply.Error == nil && !tr.IsError && len(tr.Structured) > 0
				if gotOK != wantOK {
					t.Fatalf("%s success = %v, want %v (HTTP %d, rpc error=%v, tool error=%v)", tool, gotOK, wantOK, status, reply.Error, tr.IsError)
				}
				return tr.Structured
			}
			dav("MKCOL", "Documents", "", nil, 201)
			dav("MKCOL", "Documents/folder", "", nil, 201)
			dav("MKCOL", "Outside", "", nil, 201)
			dav("PUT", "Outside/private.txt", "outside", nil, 201)
			dav("PUT", "Documents/source.txt", "first\n", nil, 201)
			var tools struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			mcpResult(t, client, f, "tools/list", map[string]any{}, version, &tools)
			seen := map[string]bool{}
			for _, tool := range tools.Tools {
				seen[tool.Name] = true
			}
			for _, tool := range []string{"list_files", "read_file"} {
				if !seen[tool] {
					t.Fatalf("%s absent", tool)
				}
			}
			for _, tool := range []string{"create_file", "edit_file", "delete_file"} {
				if seen[tool] != writes {
					t.Fatalf("%s visibility incorrect", tool)
				}
			}
			var list struct {
				Entries []struct {
					Name string `json:"name"`
				} `json:"entries"`
			}
			json.Unmarshal(call("list_files", map[string]any{"path": ""}, true), &list)
			if len(list.Entries) != 2 || list.Entries[0].Name != "folder" || list.Entries[1].Name != "source.txt" {
				t.Fatalf("unexpected listing: %+v", list)
			}
			var read struct {
				Content string `json:"content"`
				File    struct {
					ETag string `json:"etag"`
				} `json:"file"`
			}
			if err := json.Unmarshal(call("read_file", map[string]any{"path": "source.txt"}, true), &read); err != nil || read.Content != "first\n" || read.File.ETag == "" {
				t.Fatalf("read: %+v, %v", read, err)
			}
			call("read_file", map[string]any{"path": "../Outside/private.txt"}, false)
			if !writes {
				call("create_file", map[string]any{"path": "new.txt", "content": "not allowed"}, false)
				call("edit_file", map[string]any{"path": "source.txt", "content": "not allowed", "expected_etag": read.File.ETag}, false)
				call("delete_file", map[string]any{"path": "source.txt", "expected_etag": read.File.ETag}, false)
				_, data := dav("GET", "Documents/source.txt", "", nil, 200)
				if !bytes.Equal(data, []byte("first\n")) {
					t.Fatal("read-only MCP changed bytes")
				}
				dav("GET", "Documents/new.txt", "", nil, 404)
				return
			}
			call("create_file", map[string]any{"path": "new.txt", "content": "MCP\n"}, true)
			call("create_file", map[string]any{"path": "new.txt", "content": "would overwrite"}, false)
			headers, data := dav("GET", "Documents/new.txt", "", nil, 200)
			if !bytes.Equal(data, []byte("MCP\n")) {
				t.Fatal("DAV did not see create")
			}
			tag := headers.Get("ETag")
			call("edit_file", map[string]any{"path": "new.txt", "content": "edited ✓\n", "expected_etag": tag}, true)
			call("edit_file", map[string]any{"path": "new.txt", "content": "stale edit", "expected_etag": tag}, false)
			call("delete_file", map[string]any{"path": "new.txt", "expected_etag": tag}, false)
			headers, data = dav("GET", "Documents/new.txt", "", nil, 200)
			if !bytes.Equal(data, []byte("edited ✓\n")) {
				t.Fatal("DAV did not see edit, or stale action changed bytes")
			}
			tag = headers.Get("ETag")
			call("delete_file", map[string]any{"path": "new.txt", "expected_etag": tag}, true)
			dav("GET", "Documents/new.txt", "", nil, 404)
			call("delete_file", map[string]any{"path": "folder", "expected_etag": tag}, false)
			dav("PROPFIND", "Documents/folder/", "", map[string]string{"Depth": "0"}, 207)
			lockBody := `<?xml version="1.0"?><D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype><D:owner>fixture</D:owner></D:lockinfo>`
			locked, _ := dav("LOCK", "Documents/source.txt", lockBody, map[string]string{"Depth": "0", "Timeout": "Second-60", "Content-Type": "application/xml"}, 200)
			if locked.Get("Lock-Token") == "" {
				t.Fatal("DAV lock did not return a token")
			}
			call("edit_file", map[string]any{"path": "source.txt", "content": "ignore lock", "expected_etag": read.File.ETag}, false)
			call("delete_file", map[string]any{"path": "source.txt", "expected_etag": read.File.ETag}, false)
			_, data = dav("GET", "Documents/source.txt", "", nil, 200)
			if !bytes.Equal(data, []byte("first\n")) {
				t.Fatal("locked file changed")
			}
		})
	}
}
