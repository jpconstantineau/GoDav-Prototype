package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMCPExplicitTenAccountAllowlist(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	usernames := []string{"FixtureUser"}
	passwords := []string{f.password}
	for i := 2; i <= 11; i++ {
		name := fmt.Sprintf("FixtureAgent%02d", i)
		console, out, stderr, code := privateConsoleCommand(t, t.TempDir(), bin, "add", "user", "--config", f.config, "--username", name)
		if code != 0 {
			t.Fatal("cannot create disposable account", i)
		}
		usernames = append(usernames, name)
		passwords = append(passwords, accountSecret(t, console, out, stderr))
	}
	setup := "\nmcp_enabled: true\nmcp_skills_root: /Skills\nmcp_files_root: /Documents\nmcp_usernames: [" + strings.Join(usernames[:10], ", ") + "]\n"
	if err := os.WriteFile(f.config, append(fixtureBytes(t, f.config), setup...), 0600); err != nil {
		t.Fatal(err)
	}
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	if status, _, _ := fileRequest(t, client, f, "MKCOL", "/dav/Documents", nil, nil); status != 201 {
		t.Fatal("create fixture library", status)
	}
	if status, _, _ := fileRequest(t, client, f, "PUT", "/dav/Documents/proof.txt", strings.NewReader("shared synthetic bytes\n"), nil); status != 201 {
		t.Fatal("create fixture data", status)
	}
	for i, user := range usernames {
		args := map[string]any{"name": "read_file", "arguments": map[string]any{"path": "proof.txt"}}
		status, reply := mcpWire(t, client, f, "tools/call", args, "2025-11-25", strings.ToLower(user), passwords[i])
		if i == 10 {
			if status != 403 || len(reply.Result) != 0 {
				t.Fatalf("unlisted account reached /mcp: %d", status)
			}
			break
		}
		var result struct {
			IsError bool `json:"isError"`
			Value   struct {
				Content string `json:"content"`
			} `json:"structuredContent"`
		}
		if status != 200 || reply.Error != nil || json.Unmarshal(reply.Result, &result) != nil || result.IsError || result.Value.Content != "shared synthetic bytes\n" {
			t.Fatalf("allowlisted agent %d got HTTP %d or an invalid shared read", i+1, status)
		}
	}
}
