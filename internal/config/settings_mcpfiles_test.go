package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMCPFileSettingsRejectOverlappingScopes(t *testing.T) {
	const base = "config_version: 1\nlisten_address: 127.0.0.1:8443\npublic_origin: https://localhost:8443\ndav_prefix: /dav/\ndatabase_path: data.db\ntls:\n  cert_file: cert.pem\n  key_file: key.pem\nauthentication:\n  method: basic\nlimits:\n  max_file_bytes: 65536\nmcp_enabled: true\nmcp_skills_root: /Data/Skills\nmcp_username: FixtureUser\n"
	for _, tc := range []struct {
		name, appendix string
		ok, writes     bool
	}{
		{"read only", "mcp_files_root: /Data/Documents\n", true, false},
		{"writes", "mcp_files_root: /Data/Documents\nmcp_file_writes: true\n", true, true},
		{"case insensitive equal", "mcp_files_root: /data/skills\n", false, false},
		{"ancestor", "mcp_files_root: /Data\n", false, false},
		{"descendant", "mcp_files_root: /Data/Skills/Refs\n", false, false},
		{"writes without file root", "mcp_file_writes: true\n", false, false},
		{"noncanonical", "mcp_files_root: /Documents/\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "godav.yaml")
			if err := os.WriteFile(p, []byte(base+tc.appendix), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := Load(p)
			if (err == nil) != tc.ok || s.MCPFileWrites != tc.writes {
				t.Fatalf("writes=%v error=%v", s.MCPFileWrites, err)
			}
			if tc.ok && s.MCPFilesRoot != "Data/Documents" {
				t.Fatalf("unexpected file root: %q", s.MCPFilesRoot)
			}
		})
	}
}
