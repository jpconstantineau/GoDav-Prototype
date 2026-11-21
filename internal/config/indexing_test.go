package config

import (
	"reflect"
	"testing"
	"time"
)

func TestIndexingSettingsScopedExplicitAdminGrant(t *testing.T) {
	ordinary := quotaBaseYAML + quotaMCPSkills + "mcp_usernames: [Agent, IndexAdmin]\n"
	disabled, err := loadQuotaSettings(t, ordinary)
	if err != nil || disabled.Indexing != (IndexingSettings{}) {
		t.Fatalf("indexing was not absent by default: %v", err)
	}
	worker, err := loadQuotaSettings(t, quotaBaseYAML+"indexing: {scope: /Documents}\n")
	if err != nil || worker.Indexing.Root != "Documents" || len(worker.Indexing.AdminUsers()) != 0 {
		t.Fatalf("engine-only configuration is not valid: %v", err)
	}
	s, err := loadQuotaSettings(t, ordinary+"indexing: {scope: /Documents, mcp_admin_users: [inDexADMIN], quiet_period_ms: 12, poll_interval_ms: 27, step_timeout_ms: 55, max_file_bytes: 8192}\n")
	if err != nil || s.Indexing.Root != "Documents" || !reflect.DeepEqual(s.Indexing.AdminUsers(), []string{"indexadmin"}) ||
		s.Indexing.QuietPeriod != 12*time.Millisecond || s.Indexing.PollInterval != 27*time.Millisecond ||
		s.Indexing.StepTimeout != 55*time.Millisecond || s.Indexing.MaxFileBytes != 8192 {
		t.Fatalf("configured indexing budgets/grants did not round trip: %+v %v", s.Indexing, err)
	}
	for _, raw := range []string{
		"indexing: {scope: /Documents, mcp_admin_users: [Unknown]}\n",
		"indexing: {scope: /Documents, mcp_admin_users: [IndexAdmin, indexadmin]}\n",
		"indexing: {scope: /Documents, mcp_admin_users: []}\n",
		"indexing: {scope: /Documents, mcp_admin_users: '*'}\n",
		"indexing: {scope: /}\n",
		"indexing: {scope: /Documents/../Elsewhere}\n",
		"indexing: {scope: /Documents, scope: /Elsewhere}\n",
		"indexing: {scope: /Documents, credentials: unknown}\n",
		"indexing: {scope: /Documents, quiet_period_ms: 9}\n",
		"indexing: {scope: /Documents, max_file_bytes: 1048577}\n",
		"indexing: null\n",
	} {
		if s, err := loadQuotaSettings(t, ordinary+raw); err != errSettings || s != (Settings{}) {
			t.Fatalf("invalid indexing setting was accepted: %q", raw)
		}
	}
	if s, err := loadQuotaSettings(t, quotaBaseYAML+"indexing: {scope: /Documents, mcp_admin_users: [IndexAdmin]}\n"); err != errSettings || s != (Settings{}) {
		t.Fatal("index administrator grant accepted without ordinary authenticated MCP opt-in")
	}
}
