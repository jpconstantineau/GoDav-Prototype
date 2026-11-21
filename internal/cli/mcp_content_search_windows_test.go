package cli_test

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// This test observes the public HTTPS surface only: existing DAV bytes become
// searchable, and later DAV/MCP publications remain coherent with the index.
func TestMCPContentSearchAcrossWebDAV(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	dav := func(method, path, body string, headers map[string]string, want int) http.Header {
		t.Helper()
		status, h, _ := fileRequest(t, client, f, method, "/dav/"+path, strings.NewReader(body), headers)
		if status != want {
			t.Fatalf("%s %s: %d, want %d", method, path, status, want)
		}
		return h
	}
	const phrase = "violet meadow lantern"
	const short = "The violet meadow lantern is lit."
	large := strings.Repeat("ordinary padding ", 4500) + phrase
	dav("MKCOL", "Documents", "", nil, 201)
	dav("MKCOL", "Documents/Folder", "", nil, 201)
	dav("MKCOL", "Elsewhere", "", nil, 201)
	textType := map[string]string{"Content-Type": "text/plain; charset=utf-8"}
	shortHeader := dav("PUT", "Documents/Folder/Note.TXT", short, textType, 201)
	dav("PUT", "Documents/Large.txt", large, textType, 201)
	dav("PUT", "Documents/Separated.txt", "violet unrelated meadow unrelated lantern", textType, 201)
	dav("PUT", "Documents/Binary.txt", phrase+"\x00", textType, 201)
	dav("PUT", "Documents/Invalid.txt", phrase+"\xff", textType, 201)
	dav("PUT", "Elsewhere/Other.txt", phrase, textType, 201)
	// Enable MCP after data exists, then reopen the same disposable database.
	p.stop()
	client.CloseIdleConnections()
	config := append(fixtureBytes(t, f.config), []byte("\nmcp_enabled: true\nmcp_skills_root: /Skills\nmcp_username: FixtureUser\nmcp_files_root: /Documents\nmcp_file_writes: true\n")...)
	if err := os.WriteFile(f.config, config, 0600); err != nil {
		t.Fatal(err)
	}
	p = startServer(t, bin, f)
	defer p.stop()
	call := func(name string, args map[string]any) json.RawMessage {
		t.Helper()
		status, reply := mcpWire(t, client, f, "tools/call", map[string]any{"name": name, "arguments": args}, "2025-11-25", "FixtureUser", f.password)
		var result struct {
			IsError    bool            `json:"isError"`
			Structured json.RawMessage `json:"structuredContent"`
		}
		if status != 200 || reply.Error != nil || json.Unmarshal(reply.Result, &result) != nil || result.IsError || len(result.Structured) == 0 {
			t.Fatalf("%s failed: HTTP %d rpc=%v result=%s", name, status, reply.Error, reply.Result)
		}
		return result.Structured
	}
	type hit struct {
		Path        string `json:"path"`
		Name        string `json:"name"`
		ContentType string `json:"content_type"`
		ETag        string `json:"etag"`
		Size        int64  `json:"size"`
		Modified    string `json:"modified"`
	}
	type result struct {
		Entries []hit `json:"entries"`
		Partial bool  `json:"partial"`
	}
	search := func(args map[string]any, want ...string) result {
		t.Helper()
		args["query"] = phrase
		var got result
		if err := json.Unmarshal(call("search_file_contents", args), &got); err != nil {
			t.Fatal(err)
		}
		paths := make([]string, 0, len(got.Entries))
		for _, h := range got.Entries {
			paths = append(paths, h.Path)
		}
		sort.Strings(paths)
		sort.Strings(want)
		if len(paths) != len(want) || (len(paths) > 0 && !reflect.DeepEqual(paths, want)) || got.Partial {
			t.Fatalf("search %v: paths=%v partial=%v, want=%v", args, paths, got.Partial, want)
		}
		return got
	}
	got := search(map[string]any{}, "Folder/Note.TXT", "Large.txt")
	var note hit
	for _, h := range got.Entries {
		if h.Path == "Folder/Note.TXT" {
			note = h
		}
	}
	if note.Name != "Note.TXT" || note.ETag != shortHeader.Get("ETag") || note.Size != int64(len(short)) || !strings.EqualFold(strings.Split(note.ContentType, ";")[0], "text/plain") {
		t.Fatalf("search metadata mismatch: %+v", note)
	}
	modified, err := time.Parse(time.RFC3339, note.Modified)
	if err != nil || modified.IsZero() {
		t.Fatalf("invalid modified timestamp %q", note.Modified)
	}
	search(map[string]any{"path": "folder", "extension": ".tXt", "content_type": "TEXT/PLAIN", "min_size": len(short), "max_size": len(short), "modified_after": note.Modified, "modified_before": modified.Add(time.Second).Format(time.RFC3339)}, "Folder/Note.TXT")
	search(map[string]any{"path": "Folder", "modified_before": note.Modified})
	search(map[string]any{"path": "Folder", "modified_after": modified.Add(time.Second).Format(time.RFC3339)})
	search(map[string]any{"extension": ".md"})
	search(map[string]any{"content_type": "application/json"})
	search(map[string]any{"max_size": len(short) - 1})
	search(map[string]any{"min_size": len(large) + 1})
	var limited result
	if err := json.Unmarshal(call("search_file_contents", map[string]any{"query": phrase, "limit": 1}), &limited); err != nil || len(limited.Entries) != 1 || !limited.Partial {
		t.Fatalf("limit result=%+v err=%v", limited, err)
	}
	dav("MOVE", "Documents/Folder/Note.TXT", "", map[string]string{"Destination": f.origin + "/dav/Documents/Folder/Renamed.txt"}, 201)
	search(map[string]any{}, "Folder/Renamed.txt", "Large.txt")
	dav("PUT", "Documents/Folder/Renamed.txt", "new ordinary text", textType, 204)
	search(map[string]any{}, "Large.txt")
	dav("DELETE", "Documents/Large.txt", "", nil, 204)
	search(map[string]any{})
	call("create_file", map[string]any{"path": "Created.txt", "content": phrase})
	created := search(map[string]any{}, "Created.txt").Entries[0]
	call("edit_file", map[string]any{"path": "Created.txt", "content": "replacement without search terms", "expected_etag": created.ETag})
	search(map[string]any{})
	// Legitimate collection moves refresh membership in the configured search root.
	dav("MKCOL", "Elsewhere/Incoming", "", nil, 201)
	dav("PUT", "Elsewhere/Incoming/Only.TXT", phrase, textType, 201)
	search(map[string]any{})
	dav("MOVE", "Elsewhere/Incoming", "", map[string]string{"Destination": f.origin + "/dav/Documents/Imported"}, 201)
	search(map[string]any{}, "Imported/Only.TXT")
	dav("MOVE", "Documents/Imported", "", map[string]string{"Destination": f.origin + "/dav/Elsewhere/Departed"}, 201)
	search(map[string]any{})
	// Reopen must not resurrect stale indexed versions removed by either API.
	p.stop()
	client.CloseIdleConnections()
	p = startServer(t, bin, f)
	defer p.stop()
	search(map[string]any{})
}
