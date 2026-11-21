package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const quotaBaseYAML = "config_version: 1\nlisten_address: 127.0.0.1:8443\npublic_origin: https://localhost:8443\ndav_prefix: /dav/\ndatabase_path: data.db\ntls:\n  cert_file: cert.pem\n  key_file: key.pem\nauthentication:\n  method: basic\nlimits:\n  max_file_bytes: 65536\n"

func loadQuotaSettings(t *testing.T, input string) (Settings, error) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "godav.yaml")
	if err := os.WriteFile(file, []byte(input), 0600); err != nil {
		t.Fatal("test settings write failed")
	}
	return Load(file)
}

func TestConcurrencySettingsDefaultsAndOverrides(t *testing.T) {
	defaults := Concurrency{MaxConnections: 128, MaxRequests: 100, MaxRequestsPerAccount: 10,
		AuthSlots: 10, ReceivingUploads: 2, MCPActive: 1, MCPWaiting: 5,
		MCPWait: 500 * time.Millisecond, RequestTimeout: 10 * time.Second}
	if got := DefaultConcurrency(); got != defaults {
		t.Errorf("Go defaults: got %+v; want %+v", got, defaults)
	}
	partial := defaults
	partial.MaxRequests = 1
	partial.RequestTimeout = 31 * time.Millisecond
	for _, tc := range []struct {
		name, appendix string
		want           Concurrency
	}{
		{"omitted", "", defaults},
		{"empty", "concurrency: {}\n", defaults},
		{"partial", "concurrency:\n  max_requests: 1\n  request_timeout_ms: 31\n", partial},
		{"all", "concurrency:\n  max_connections: 81\n  max_requests: 39\n  max_requests_per_account: 7\n  auth_slots: 6\n  receiving_uploads: 3\n  mcp_active: 2\n  mcp_waiting: 4\n  mcp_wait_ms: 291\n  request_timeout_ms: 9700\n",
			Concurrency{MaxConnections: 81, MaxRequests: 39, MaxRequestsPerAccount: 7, AuthSlots: 6, ReceivingUploads: 3, MCPActive: 2, MCPWaiting: 4, MCPWait: 291 * time.Millisecond, RequestTimeout: 9700 * time.Millisecond}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := loadQuotaSettings(t, quotaBaseYAML+tc.appendix)
			if err != nil || got.Concurrency != tc.want {
				t.Fatalf("got %+v, err %v; want %+v", got.Concurrency, err, tc.want)
			}
		})
	}
}

func TestConcurrencySettingsEnforceBoundsAndStrictYAML(t *testing.T) {
	for _, tc := range []struct {
		key string
		max int
	}{
		{"max_connections", 1024}, {"max_requests", 512}, {"max_requests_per_account", 128},
		{"auth_slots", 16}, {"receiving_uploads", 32}, {"mcp_active", 16}, {"mcp_waiting", 32},
		{"mcp_wait_ms", 5000}, {"request_timeout_ms", 10000},
	} {
		t.Run(tc.key, func(t *testing.T) {
			for _, n := range []int{1, tc.max} {
				if _, err := loadQuotaSettings(t, quotaBaseYAML+fmt.Sprintf("concurrency:\n  %s: %d\n", tc.key, n)); err != nil {
					t.Fatalf("valid boundary %d rejected", n)
				}
			}
			for _, n := range []int{-1, 0, tc.max + 1} {
				if got, err := loadQuotaSettings(t, quotaBaseYAML+fmt.Sprintf("concurrency:\n  %s: %d\n", tc.key, n)); err != errSettings || got != (Settings{}) {
					t.Fatalf("invalid boundary %d not rejected without settings", n)
				}
			}
		})
	}
	for name, appendix := range map[string]string{
		"scalar":                "concurrency: enabled\n",
		"sequence":              "concurrency: [8, 9]\n",
		"unknown":               "concurrency: {max_request_count: 1}\n",
		"repeat option":         "concurrency: {auth_slots: 1, auth_slots: 2}\n",
		"repeat section":        "concurrency: {}\nconcurrency: {}\n",
		"numeric string":        "concurrency: {mcp_waiting: '5'}\n",
		"boolean":               "concurrency: {mcp_wait_ms: true}\n",
		"fraction":              "concurrency: {auth_slots: 1.5}\n",
		"hex":                   "concurrency: {auth_slots: 0x2}\n",
		"overflow":              "concurrency: {max_requests: 99999999999999999999999}\n",
		"anchor":                "concurrency: &fixture {max_requests: 10}\n",
		"null":                  "concurrency:\n",
		"one missing top-level": "concurrency: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			base := quotaBaseYAML
			if name == "one missing top-level" {
				base = strings.Replace(base, "database_path: data.db\n", "", 1)
			}
			if got, err := loadQuotaSettings(t, base+appendix); err != errSettings || got != (Settings{}) {
				t.Fatal("malformed YAML not rejected without settings")
			}
		})
	}
}

const quotaMCPSkills = "mcp_enabled: true\nmcp_skills_root: /Skills\n"

func TestMCPAccountListExplicitOptIn(t *testing.T) {
	disabled, err := loadQuotaSettings(t, quotaBaseYAML)
	if err != nil || disabled.MCPEnabled || disabled.MCPAllowedUsernames() != nil {
		t.Fatal("MCP not opted out by default")
	}
	single, err := loadQuotaSettings(t, quotaBaseYAML+quotaMCPSkills+"mcp_username: FixtureUser\n")
	if err != nil || !single.MCPEnabled || single.MCPUsername != "FixtureUser" || !reflect.DeepEqual(single.MCPAllowedUsernames(), []string{"fixtureuser"}) {
		t.Fatal("legacy spelling was not normalized without breaking compatibility")
	}
	// Use the same settings path for comparable checks; only the allowlist
	// order and case change while loading the two documents.
	file := filepath.Join(t.TempDir(), "godav.yaml")
	load := func(list string) Settings {
		t.Helper()
		if err := os.WriteFile(file, []byte(quotaBaseYAML+quotaMCPSkills+"mcp_usernames: "+list+"\n"), 0600); err != nil {
			t.Fatal("test settings write failed")
		}
		s, err := Load(file)
		if err != nil || !s.MCPEnabled || s.MCPUsername != "" {
			t.Fatal("explicit list was not accepted")
		}
		return s
	}
	list := load("[Other_User, 'A.B', OPERAtor-7]")
	equivalent := load("[operator-7, other_user, a.b]")
	if list != equivalent || !reflect.DeepEqual(list.MCPAllowedUsernames(), []string{"a.b", "operator-7", "other_user"}) {
		t.Fatal("list did not normalize to a comparable set")
	}
	exposed := list.MCPAllowedUsernames()
	exposed[0] = "replacement"
	if list.MCPAllowedUsernames()[0] != "a.b" {
		t.Fatal("accessor leaked mutable state")
	}
	many := load("[" + testMCPNames(32) + "]")
	if len(many.MCPAllowedUsernames()) != 32 {
		t.Fatal("maximum-size valid list lost names")
	}
}

func testMCPNames(n int) string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("account-%02d", i+1)
	}
	return strings.Join(names, ", ")
}

func TestMCPAccountListRejectsAmbiguousOrMalformedSettings(t *testing.T) {
	for name, appendix := range map[string]string{
		"orphan list":    "mcp_usernames: [Fixture]\n",
		"orphan legacy":  "mcp_username: Fixture\n",
		"missing list":   quotaMCPSkills,
		"both spellings": quotaMCPSkills + "mcp_username: Fixture\nmcp_usernames: [Fixture]\n",
		"empty":          quotaMCPSkills + "mcp_usernames: []\n",
		"null":           quotaMCPSkills + "mcp_usernames:\n",
		"scalar":         quotaMCPSkills + "mcp_usernames: Fixture\n",
		"wrong type":     quotaMCPSkills + "mcp_usernames: [Fixture, 1, true]\n",
		"nested list":    quotaMCPSkills + "mcp_usernames: [[Fixture]]\n",
		"blank":          quotaMCPSkills + "mcp_usernames: ['']\n",
		"bad name":       quotaMCPSkills + "mcp_usernames: [Fixture, 'fixture bad']\n",
		"exact repeat":   quotaMCPSkills + "mcp_usernames: [Fixture, Fixture]\n",
		"case repeat":    quotaMCPSkills + "mcp_usernames: [Fixture, fIXTURE]\n",
		"repeat key":     quotaMCPSkills + "mcp_usernames: [Fixture]\nmcp_usernames: [Other]\n",
		"anchor":         quotaMCPSkills + "mcp_usernames: [&fixture Fixture]\n",
		"oversize":       quotaMCPSkills + "mcp_usernames: [" + testMCPNames(33) + "]\n",
	} {
		t.Run(name, func(t *testing.T) {
			s, err := loadQuotaSettings(t, quotaBaseYAML+appendix)
			if err != errSettings || s != (Settings{}) {
				t.Fatal("malformed allowlist not rejected with a fixed error and no settings")
			}
		})
	}
}
