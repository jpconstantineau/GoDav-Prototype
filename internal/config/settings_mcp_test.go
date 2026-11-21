package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMCPSettingsOptIn(t *testing.T) {
	const base = "config_version: 1\nlisten_address: 127.0.0.1:8443\npublic_origin: https://localhost:8443\ndav_prefix: /dav/\ndatabase_path: data.db\ntls:\n  cert_file: cert.pem\n  key_file: key.pem\nauthentication:\n  method: basic\nlimits:\n  max_file_bytes: 65536\n"
	const enabled = "mcp_enabled: true\nmcp_skills_root: /Skills\nmcp_username: FixtureUser\n"
	tests := []struct {
		name, appendix string
		ok, active     bool
	}{
		{"default", "", true, false},
		{"enabled", enabled, true, true},
		{"partial", "mcp_enabled: true\n", false, false},
		{"duplicate", enabled + "mcp_username: Another\n", false, false},
		{"unknown", enabled + "unknown: true\n", false, false},
		{"root scope", "mcp_enabled: true\nmcp_skills_root: /\nmcp_username: FixtureUser\n", false, false},
		{"relative scope", "mcp_enabled: true\nmcp_skills_root: Skills\nmcp_username: FixtureUser\n", false, false},
		{"ambiguous scope", "mcp_enabled: true\nmcp_skills_root: /Skills/../Other\nmcp_username: FixtureUser\n", false, false},
		{"account type", "mcp_enabled: true\nmcp_skills_root: /Skills\nmcp_username: true\n", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "godav.yaml")
			if err := os.WriteFile(p, []byte(base+tc.appendix), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := Load(p)
			if (err == nil) != tc.ok || s.MCPEnabled != tc.active {
				t.Fatalf("enabled=%v error=%v", s.MCPEnabled, err)
			}
			if tc.active && (s.MCPSkillsRoot != "Skills" || s.MCPUsername != "FixtureUser") {
				t.Fatal("scope or account changed")
			}
		})
	}
}
