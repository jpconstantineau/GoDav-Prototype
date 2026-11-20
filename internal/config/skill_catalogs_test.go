package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSkillCatalogSettings(t *testing.T) {
	const base = "config_version: 1\nlisten_address: 127.0.0.1:8443\npublic_origin: https://localhost:8443\ndav_prefix: /dav/\ndatabase_path: data.db\ntls:\n  cert_file: cert.pem\n  key_file: key.pem\nauthentication:\n  method: basic\nlimits:\n  max_file_bytes: 65536\n"
	const enabled = "mcp_enabled: true\nmcp_skills_root: /Skills\nmcp_username: FixtureUser\n"
	const catalogs = "mcp_skill_catalogs:\n  coding:\n    users: [FixtureUser]\n    skills: [shared, code]\n  research:\n    users: [FixtureUser, ResearchUser]\n    skills: [shared, research]\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "godav.yaml")
	load := func(content string) (Settings, error) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}
	cfg, err := load(base + enabled + catalogs)
	if err != nil {
		t.Fatal(err)
	}
	coding, research := cfg.MCPSkillCatalogs[0], cfg.MCPSkillCatalogs[1]
	if coding.Name != "coding" || research.Name != "research" ||
		!reflect.DeepEqual(coding.Users(), []string{"FixtureUser"}) || !reflect.DeepEqual(coding.Skills(), []string{"shared", "code"}) ||
		!reflect.DeepEqual(research.Users(), []string{"FixtureUser", "ResearchUser"}) || !reflect.DeepEqual(research.Skills(), []string{"shared", "research"}) {
		t.Fatal("configured memberships did not round-trip")
	}
	for _, tc := range []struct{ name, text string }{
		{"requires opt-in", base + catalogs},
		{"duplicate account uses ASCII fold", base + enabled + strings.Replace(catalogs, "FixtureUser, ResearchUser", "FixtureUser, fixtureuser", 1)},
		{"canonical lower-case skills", base + enabled + strings.Replace(catalogs, "shared, code", "shared, Code", 1)},
		{"requires named pair", base + enabled + strings.Replace(catalogs, "  research:", "  presentation:", 1)},
		{"no empty user list", base + enabled + strings.Replace(catalogs, "[FixtureUser]", "[]", 1)},
		{"no empty skill list", base + enabled + strings.Replace(catalogs, "[shared, code]", "[]", 1)},
		{"bounded membership", base + enabled + strings.Replace(catalogs, "[shared, code]", strings.Join(tooManySkills(), ""), 1)},
	} {
		if _, err := load(tc.text); err == nil {
			t.Errorf("accepted invalid setting: %s", tc.name)
		}
	}
	cfg, err = load(base + enabled)
	if err != nil || cfg.MCPSkillCatalogs != ([2]SkillCatalog{}) || cfg.MCPSkillsRoot != "Skills" {
		t.Fatalf("unchanged default configuration unavailable: %v", err)
	}
}

func tooManySkills() []string {
	names := make([]string, 0, 33)
	for i := 'a'; i <= 'z'; i++ {
		names = append(names, "x"+string(i))
	}
	for i := 'a'; i <= 'g'; i++ {
		names = append(names, "y"+string(i))
	}
	return []string{"[", strings.Join(names, ", "), "]"}
}
