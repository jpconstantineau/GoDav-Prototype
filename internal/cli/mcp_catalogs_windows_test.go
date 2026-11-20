package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMCPScopedSkillCatalogs(t *testing.T) {
	const modern, legacy = "2026-07-28", "2025-11-25"
	const coding, research = "/mcp/catalogs/coding", "/mcp/catalogs/research"
	const owner, reader = "FixtureUser", "ResearchReader"
	policy := `
mcp_enabled: true
mcp_skills_root: /Skills
mcp_username: FixtureUser
mcp_files_root: /Library
mcp_skill_catalogs:
  coding:
    users: [FixtureUser]
    skills: [shared, coding-only]
  research:
    users: [FixtureUser, ResearchReader]
    skills: [shared, research-only]
`
	bin := development(t)
	f := newServerFixture(t, bin)
	if err := os.WriteFile(f.config, append(fixtureBytes(t, f.config), policy...), 0600); err != nil {
		t.Fatal(err)
	}
	console, out, stderr, code := privateConsoleCommand(t, t.TempDir(), bin, "add", "user", "--config", f.config, "--username", reader)
	if code != 0 {
		t.Fatal("reader setup failed")
	}
	readerSecret := accountSecret(t, console, out, stderr)
	otherConsole, otherOut, otherErr, otherCode := privateConsoleCommand(t, t.TempDir(), bin, "add", "user", "--config", f.config, "--username", "OtherUser")
	if otherCode != 0 {
		t.Fatal("alternate account setup failed")
	}
	otherSecret := accountSecret(t, otherConsole, otherOut, otherErr)
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	for _, path := range []string{"/dav/Skills", "/dav/Library", "/dav/Skills/shared", "/dav/Skills/coding-only", "/dav/Skills/research-only"} {
		code, _, _ := fileRequest(t, client, f, "MKCOL", path, nil, nil)
		if code != 201 {
			t.Fatalf("create %s: %d", path, code)
		}
	}
	for _, name := range []string{"shared", "coding-only", "research-only"} {
		for filename, data := range map[string]string{"SKILL.md": "---\nname: " + name + "\ndescription: Synthetic " + name + "\n---\nRead GUIDE.md.\n", "GUIDE.md": "Synthetic support for " + name + "\n"} {
			code, _, _ := fileRequest(t, client, f, "PUT", "/dav/Skills/"+name+"/"+filename, strings.NewReader(data), nil)
			if code != 201 {
				t.Fatalf("put %s/%s: %d", name, filename, code)
			}
		}
	}
	call := func(t *testing.T, endpoint, method, protocol, user, pass string, params map[string]any) skillsWireReply {
		t.Helper()
		status, r := mcpWireAt(t, client, f, endpoint, method, params, protocol, user, pass)
		if status != 200 || r.Error != nil {
			t.Fatalf("%s %s: status=%d rpc=%+v", endpoint, method, status, r.Error)
		}
		return r
	}
	type listing struct {
		Skills []skillsWireEntry `json:"skills"`
	}
	type toolReply struct {
		IsError bool            `json:"isError"`
		Data    json.RawMessage `json:"structuredContent"`
	}
	type readReply struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	type lookupReply struct {
		Skill skillsWireEntry `json:"skill"`
		readReply
	}
	decode := func(t *testing.T, r json.RawMessage, to any) {
		t.Helper()
		if err := json.Unmarshal(r, to); err != nil {
			t.Fatal(err)
		}
	}
	getTool := func(t *testing.T, path, u, p, name string, args map[string]any) toolReply {
		t.Helper()
		var out toolReply
		decode(t, call(t, path, "tools/call", legacy, u, p, map[string]any{"name": name, "arguments": args}).Result, &out)
		return out
	}
	uri := func(name, file string) string { return "skill://" + name + "/" + file }
	manifest := func(t *testing.T, path, u, p string) listing {
		t.Helper()
		var x listing
		decode(t, call(t, path, "skills/list", modern, u, p, map[string]any{}).Result, &x)
		return x
	}
	t.Run("CatalogViews", func(t *testing.T) {
		d := manifest(t, "/mcp", owner, f.password)
		if len(d.Skills) != 3 {
			t.Fatalf("default catalog changed: %d", len(d.Skills))
		}
		revisions := map[string]string{}
		for _, e := range d.Skills {
			revisions[e.URI] = fmt.Sprint(e.Meta["io.godav/revision"])
		}
		for _, tc := range []struct {
			path, user, secret, name, own, excluded string
			names                                   []string
		}{
			{coding, owner, f.password, "coding", "coding-only", "research-only", []string{"coding-only", "shared"}},
			{research, reader, readerSecret, "research", "research-only", "coding-only", []string{"research-only", "shared"}},
		} {
			var discover struct {
				Meta map[string]struct {
					Name string `json:"name"`
				} `json:"_meta"`
				Capabilities struct {
					Extensions map[string]json.RawMessage `json:"extensions"`
				} `json:"capabilities"`
			}
			decode(t, call(t, tc.path, "server/discover", modern, tc.user, tc.secret, map[string]any{}).Result, &discover)
			if discover.Meta["io.modelcontextprotocol/serverInfo"].Name != "godav-skills-"+tc.name || discover.Capabilities.Extensions["io.modelcontextprotocol/skills"] == nil {
				t.Fatal("missing distinct native catalog capability/identity")
			}
			native := manifest(t, tc.path, tc.user, tc.secret)
			var names []string
			for _, e := range native.Skills {
				names = append(names, fmt.Sprint(e.Frontmatter["name"]))
			}
			if !reflect.DeepEqual(names, tc.names) {
				t.Fatalf("%s catalog membership: got %v, want %v", tc.name, names, tc.names)
			}
			var fallback listing
			tool := getTool(t, tc.path, tc.user, tc.secret, "list_skills", map[string]any{})
			if tool.IsError {
				t.Fatal("fallback catalog failed")
			}
			decode(t, tool.Data, &fallback)
			if !reflect.DeepEqual(native, fallback) {
				t.Fatal("native/fallback scope differs")
			}
			for _, e := range native.Skills {
				name := e.Frontmatter["name"].(string)
				support := uri(name, "GUIDE.md")
				var meta lookupReply
				var read readReply
				decode(t, call(t, tc.path, "skills/get", modern, tc.user, tc.secret, map[string]any{"uri": e.URI}).Result, &meta)
				if !reflect.DeepEqual(meta.Skill, e) {
					t.Fatal("list/get manifest differs")
				}
				decode(t, call(t, tc.path, "resources/read", modern, tc.user, tc.secret, map[string]any{"uri": support}).Result, &read)
				var got lookupReply
				raw := getTool(t, tc.path, tc.user, tc.secret, "get_skill", map[string]any{"uri": support})
				if raw.IsError {
					t.Fatal("fallback support read failed")
				}
				decode(t, raw.Data, &got)
				if len(read.Contents) != 1 || read.Contents[0].Text != "Synthetic support for "+name+"\n" || !reflect.DeepEqual(read, got.readReply) || !reflect.DeepEqual(got.Skill, e) {
					t.Fatal("native and fallback bytes or manifest differ")
				}
			}
			outside, revision := uri(tc.excluded, "GUIDE.md"), revisions[uri(tc.excluded, "SKILL.md")]
			for _, request := range []struct{ method, version, path string }{{"skills/get", modern, uri(tc.excluded, "SKILL.md")}, {"resources/read", modern, outside}, {"resources/read", legacy, outside}} {
				status, x := mcpWireAt(t, client, f, tc.path, request.method, map[string]any{"uri": request.path, "expectedRevision": revision}, request.version, tc.user, tc.secret)
				if status != 200 || x.Error == nil || x.Error.Code != -32602 || len(x.Result) != 0 {
					t.Fatalf("out-of-catalog %s disclosed a result", request.method)
				}
			}
			rejected := getTool(t, tc.path, tc.user, tc.secret, "get_skill", map[string]any{"uri": outside, "expectedRevision": revision})
			if !rejected.IsError || len(rejected.Data) != 0 {
				t.Fatal("out-of-catalog fallback returned data")
			}
		}

		for _, tc := range []struct {
			path, user, secret string
			want               int
		}{
			{coding, reader, readerSecret, 403}, {research, "OtherUser", otherSecret, 403}, {"/mcp", reader, readerSecret, 403},
			{research, "", "", 401}, {"/mcp/catalogs/unknown", owner, f.password, 404},
		} {
			status, x := mcpWireAt(t, client, f, tc.path, "skills/list", map[string]any{}, modern, tc.user, tc.secret)
			if status != tc.want || len(x.Result) != 0 {
				t.Fatalf("%s authorization status=%d want=%d", tc.path, status, tc.want)
			}
		}
		for _, path := range []string{"/mcp", coding, research} {
			var tools struct{ Tools []struct{ Name string } }
			decode(t, call(t, path, "tools/list", legacy, owner, f.password, map[string]any{}).Result, &tools)
			found := false
			for _, entry := range tools.Tools {
				if entry.Name == "search_file_contents" {
					found = true
				}
			}
			if found != (path == "/mcp") {
				t.Fatalf("project tools crossed catalog boundary at %s", path)
			}
		}
		// Explicitly log in as the non-owner on DAV; catalog membership does not
		// grant that separate transport access to the owner's namespace.
		req, err := http.NewRequest("GET", f.origin+"/dav/Skills/research-only/GUIDE.md", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth(reader, readerSecret)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("MCP catalog changed shared DAV access: %d", response.StatusCode)
		}
	})
	t.Run("WarmCachePartition", func(t *testing.T) {
		type fixtureKey struct{ url, identity, uri string }
		type warmEntry struct {
			body   string
			expiry time.Time
		}
		ttl := time.Minute // positive client-only fixture TTL, not a GoDav setting
		cache := map[fixtureKey]warmEntry{}
		misses := 0
		use := func(path, u, secret string) (string, bool) {
			k := fixtureKey{f.origin + path, u, uri("shared", "GUIDE.md")}
			if v, ok := cache[k]; ok && time.Now().Before(v.expiry) {
				return v.body, true
			}
			misses++
			status, r := mcpWireAt(t, client, f, path, "resources/read", map[string]any{"uri": k.uri}, modern, u, secret)
			if status != 200 || r.Error != nil {
				return "", false
			}
			var out struct {
				readReply
				TTL   *int   `json:"ttlMs"`
				Scope string `json:"cacheScope"`
			}
			decode(t, r.Result, &out)
			if r.CacheControl != "no-store" || out.TTL == nil || *out.TTL != 0 || out.Scope != "private" {
				t.Fatal("live catalog must be private and zero TTL with HTTP no-store")
			}
			if len(out.Contents) != 1 {
				t.Fatal("shared resource absent")
			}
			val := out.Contents[0].Text
			cache[k] = warmEntry{val, time.Now().Add(ttl)}
			return val, false
		}
		for _, step := range []struct {
			path, u, secret string
			hit             bool
			want            string
		}{
			{coding, owner, f.password, false, "Synthetic support for shared\n"},
			{coding, owner, f.password, true, "Synthetic support for shared\n"},
			{research, owner, f.password, false, "Synthetic support for shared\n"},
			{research, reader, readerSecret, false, "Synthetic support for shared\n"},
			{research, reader, readerSecret, true, "Synthetic support for shared\n"},
			{research, "OtherUser", otherSecret, false, ""},
			{coding, reader, readerSecret, false, ""},
		} {
			value, hit := use(step.path, step.u, step.secret)
			if hit != step.hit || value != step.want {
				t.Fatalf("catalog/identity cache partition: path=%s identity=%s hit=%v", step.path, step.u, hit)
			}
		}
		if misses != 5 {
			t.Fatalf("wrong fixture wire request count: %d", misses)
		}
		// A direct request must still authenticate when the test cache is warm.
		status, r := mcpWireAt(t, client, f, research, "resources/read", map[string]any{"uri": uri("shared", "GUIDE.md")}, modern, reader, "wrong")
		if status != 401 || !bytes.Equal(r.Result, nil) {
			t.Fatalf("live authentication was not independent of cache: %d", status)
		}
	})
	client.CloseIdleConnections()
	p.stop()
	assertAccountServerLogs(t, p, [2]string{owner, f.password}, [2]string{reader, readerSecret}, [2]string{"OtherUser", otherSecret})
}
