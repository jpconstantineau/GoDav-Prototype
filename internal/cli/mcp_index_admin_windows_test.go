//go:build windows

package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMCPIndexAdminHTTPSOwnerLifecycle(t *testing.T) {
	const route, modern, legacy = "/mcp/admin/indexing", "2026-07-28", "2025-11-25"
	const owner, agent, second = "FixtureUser", "IndexObserver", "OtherAdmin"
	bin := development(t)
	f := newServerFixture(t, bin)
	base := fixtureBytes(t, f.config)
	addAccount := func(name string) string {
		console, stdout, stderr, code := privateConsoleCommand(t, t.TempDir(), bin, "add", "user", "--config", f.config, "--username", name)
		if code != 0 {
			t.Fatalf("account setup failed: %d", code)
		}
		return accountSecret(t, console, stdout, stderr)
	}
	agentPass, secondPass := addAccount(agent), addAccount(second)
	const ordinaryMCP = `
mcp_enabled: true
mcp_skills_root: /Skills
mcp_usernames: [FixtureUser, IndexObserver, OtherAdmin]
mcp_files_root: /Documents
`
	writeConfig := func(addendum string) {
		t.Helper()
		config := append(append([]byte(nil), base...), ordinaryMCP+addendum...)
		if err := os.WriteFile(f.config, config, 0600); err != nil {
			t.Fatal("indexing config write failed")
		}
	}
	writeConfig("")
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	seed := startServer(t, bin, f)
	checkStatus := func(endpoint, method, version, username, password string, p map[string]any, want int) skillsWireReply {
		t.Helper()
		got, reply := mcpWireAt(t, client, f, endpoint, method, p, version, username, password)
		if got != want {
			t.Fatalf("%s %s: HTTP %d, want %d", endpoint, method, got, want)
		}
		return reply
	}
	checkStatus(route, "tools/list", modern, owner, f.password, map[string]any{}, 404)
	for _, path := range []string{"/dav/Skills", "/dav/Documents", "/dav/Outside"} {
		if code, _, _ := fileRequest(t, client, f, "MKCOL", path, nil, nil); code != 201 {
			t.Fatalf("seed collection status %d", code)
		}
	}
	for _, path := range []string{"/dav/Documents/in-scope.txt", "/dav/Outside/outside.txt"} {
		if code, _, _ := fileRequest(t, client, f, "PUT", path, strings.NewReader("synthetic ivory clover phrase"), nil); code != 201 {
			t.Fatalf("seed content status %d", code)
		}
	}
	assertExistingSearch := func() {
		t.Helper()
		var call struct {
			IsError bool `json:"isError"`
			Data    struct {
				Entries []struct{ Path string }
			} `json:"structuredContent"`
		}
		reply := checkStatus("/mcp", "tools/call", modern, agent, agentPass, map[string]any{
			"name": "search_file_contents", "arguments": map[string]any{"query": "ivory clover phrase"},
		}, 200)
		if reply.Error != nil || json.Unmarshal(reply.Result, &call) != nil || call.IsError ||
			len(call.Data.Entries) != 1 || call.Data.Entries[0].Path != "in-scope.txt" {
			t.Fatal("ordinary read-only search depends on a prepared index or lost its scope")
		}
	}
	assertExistingSearch()
	client.CloseIdleConnections()
	seed.stop()

	enable := func(quiet int) {
		t.Helper()
		writeConfig(fmt.Sprintf(`
indexing:
  scope: /Documents
  mcp_admin_users: [FixtureUser, OtherAdmin]
  quiet_period_ms: %d
`, quiet))
	}
	enable(5000)
	active := startServer(t, bin, f)
	assertExistingSearch() // Engine is configured, but no job has started.
	checkStatus(route, "tools/list", modern, "", "", map[string]any{}, 401)
	checkStatus(route, "tools/list", modern, owner, "wrong", map[string]any{}, 401)
	checkStatus(route, "tools/list", modern, agent, agentPass, map[string]any{}, 403)
	checkStatus(route+"/", "tools/list", modern, owner, f.password, map[string]any{}, 404)
	readObject := func(reply skillsWireReply) map[string]any {
		t.Helper()
		var value map[string]any
		if reply.Error != nil || json.Unmarshal(reply.Result, &value) != nil {
			t.Fatalf("expected ordinary result: %+v", reply.Error)
		}
		return value
	}
	for _, version := range []string{modern, legacy} {
		method, params := "server/discover", map[string]any{}
		if version == legacy {
			method = "initialize"
			params = map[string]any{"protocolVersion": legacy, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "fixture", "version": "1"}}
		}
		result := readObject(checkStatus(route, method, version, owner, f.password, params, 200))
		capabilities, _ := result["capabilities"].(map[string]any)
		if len(capabilities) != 1 || capabilities["tools"] == nil {
			t.Fatal("indexing endpoint advertised unsupported capabilities")
		}
		list := readObject(checkStatus(route, "tools/list", version, owner, f.password, map[string]any{}, 200))
		tools := list["tools"].([]any)
		if len(tools) != 1 || tools[0].(map[string]any)["name"] != "manage_indexing" {
			t.Fatalf("unexpected administrative tool list: %v", tools)
		}
	}
	defaultList := readObject(checkStatus("/mcp", "tools/list", modern, agent, agentPass, map[string]any{}, 200))
	for _, raw := range defaultList["tools"].([]any) {
		if raw.(map[string]any)["name"] == "manage_indexing" {
			t.Fatal("admin tool leaked to default discovery")
		}
	}
	status := checkStatus(route, "tasks/get", modern, owner, f.password, map[string]any{}, 404)
	if status.Error == nil || status.Error.Code != -32601 {
		t.Fatalf("unexpected Tasks response: %+v", status.Error)
	}
	taskStart := checkStatus(route, "tools/call", legacy, owner, f.password, map[string]any{
		"name": "manage_indexing", "arguments": map[string]any{"operation": "start"}, "task": map[string]any{"ttl": 1000},
	}, 200)
	if taskStart.Error == nil || taskStart.Error.Code != -32602 || len(taskStart.Result) != 0 {
		t.Fatalf("explicit legacy task request was not rejected before dispatch: %+v", taskStart.Error)
	}

	type toolCall struct {
		IsError    bool                          `json:"isError"`
		Structured json.RawMessage               `json:"structuredContent"`
		Content    []struct{ Type, Text string } `json:"content"`
	}
	call := func(version, user, pass string, args map[string]any) toolCall {
		t.Helper()
		reply := checkStatus(route, "tools/call", version, user, pass, map[string]any{"name": "manage_indexing", "arguments": args}, 200)
		var result toolCall
		if reply.Error != nil || json.Unmarshal(reply.Result, &result) != nil {
			t.Fatalf("unexpected admin call response: %+v", reply.Error)
		}
		return result
	}
	type job struct {
		ID                                          string `json:"job_id"`
		State                                       string
		Total, Processed, Indexed, Skipped, Backlog int
	}
	if premature := call(modern, owner, f.password, map[string]any{"operation": "status"}); !premature.IsError {
		t.Fatal("rejected task request started indexing")
	}
	asJob := func(response toolCall) job {
		t.Helper()
		var got job
		if response.IsError || json.Unmarshal(response.Structured, &got) != nil || got.ID == "" ||
			len(response.Content) != 1 || response.Content[0].Type != "text" ||
			!bytes.Equal(response.Structured, []byte(response.Content[0].Text)) {
			t.Fatalf("invalid job result: %+v", response)
		}
		return got
	}
	do := func(op, id string) job {
		t.Helper()
		args := map[string]any{"operation": op}
		if id != "" {
			args["job_id"] = id
		}
		return asJob(call(modern, owner, f.password, args))
	}
	first := do("start", "")
	if first.State != "queued" {
		t.Fatalf("start claimed work prematurely: %+v", first)
	}
	if duplicate := do("start", ""); duplicate.ID != first.ID {
		t.Fatalf("nonterminal start was not idempotent: %s vs %s", duplicate.ID, first.ID)
	}
	if paused := do("pause", first.ID); paused.State != "paused" {
		t.Fatalf("job not paused: %+v", paused)
	}
	if latest := asJob(call(legacy, owner, f.password, map[string]any{"operation": "status"})); latest.ID != first.ID || latest.State != "paused" {
		t.Fatalf("legacy latest owned status differs: %+v", latest)
	}
	bad := call(modern, second, secondPass, map[string]any{"operation": "status", "job_id": first.ID})
	unknown := call(modern, second, secondPass, map[string]any{"operation": "status", "job_id": "00000000000000000000000000000000"})
	if !bad.IsError || !reflect.DeepEqual(bad, unknown) || len(bad.Structured) != 0 {
		t.Fatal("foreign and unknown jobs had different public failure")
	}
	if foreign := call(modern, second, secondPass, map[string]any{"operation": "cancel", "job_id": first.ID}); !foreign.IsError || len(foreign.Structured) != 0 {
		t.Fatal("second administrator canceled another owner's job")
	}
	if latest := call(modern, second, secondPass, map[string]any{"operation": "status"}); !latest.IsError {
		t.Fatal("latest job exposed foreign owner")
	}
	if intact := do("status", first.ID); intact.State != "paused" {
		t.Fatalf("foreign command changed job: %+v", intact)
	}
	if resumed := do("resume", first.ID); resumed.State != "queued" && resumed.State != "running" {
		t.Fatalf("job not resumed: %+v", resumed)
	}
	if cancelled := do("cancel", first.ID); cancelled.State != "cancelled" {
		t.Fatalf("job not cancelled: %+v", cancelled)
	}
	client.CloseIdleConnections()
	active.stop()

	enable(40)
	reopened := startServer(t, bin, f)
	defer reopened.stop()
	if got := do("status", first.ID); got.State != "cancelled" {
		t.Fatalf("terminal checkpoint lost on reopen: %+v", got)
	}
	if foreign := call(modern, second, secondPass, map[string]any{"operation": "status", "job_id": first.ID}); !foreign.IsError || len(foreign.Structured) != 0 {
		t.Fatal("reopened job lost its owner boundary")
	}
	fresh := do("start", "")
	if fresh.ID == first.ID {
		t.Fatal("new run reused cancelled generation")
	}
	if stale := call(modern, owner, f.password, map[string]any{"operation": "cancel", "job_id": first.ID}); !stale.IsError {
		t.Fatal("stale generation could cancel a current job")
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		got := do("status", fresh.ID)
		if got.State == "completed" {
			if got.Indexed != 1 || got.Processed != 1 || got.Backlog != 0 {
				t.Fatalf("index result misreported scope/progress: %+v", got)
			}
			break
		}
		if got.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("index job did not complete: %+v", got)
		}
		time.Sleep(130 * time.Millisecond)
	}
	client.CloseIdleConnections()
}
