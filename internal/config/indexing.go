package config

import (
	"slices"
	"time"

	"go.yaml.in/yaml/v3"
)

// IndexingSettings is comparable so protected-state configuration can be
// rechecked without trusting a mutable allowlist.
type IndexingSettings struct {
	Root                      string
	QuietPeriod, PollInterval time.Duration
	StepTimeout               time.Duration
	MaxFileBytes              int64
	admins                    [maxMCPUsernames]string
	adminCount                int
}

func (i IndexingSettings) AdminUsers() []string { return slices.Clone(i.admins[:i.adminCount]) }

func parseIndexing(node *yaml.Node, settings Settings) (IndexingSettings, bool) {
	var result IndexingSettings
	if node == nil {
		return result, true
	}
	fields, ok := mappingOptional(node, []string{"scope"}, "mcp_admin_users", "quiet_period_ms", "poll_interval_ms", "step_timeout_ms", "max_file_bytes")
	if !ok {
		return result, false
	}
	if result.Root, ok = logicalRoot(fields["scope"]); !ok {
		return result, false
	}
	for _, t := range []struct {
		key      string
		min, max int64
		dst      *time.Duration
	}{
		{"quiet_period_ms", 10, 5000, &result.QuietPeriod},
		{"poll_interval_ms", 10, 1000, &result.PollInterval},
		{"step_timeout_ms", 50, 2000, &result.StepTimeout},
	} {
		if node := fields[t.key]; node != nil {
			value, ok := positiveInt(node)
			if !ok || value < t.min || value > t.max {
				return IndexingSettings{}, false
			}
			*t.dst = time.Duration(value) * time.Millisecond
		}
	}
	if node := fields["max_file_bytes"]; node != nil {
		value, ok := positiveInt(node)
		if !ok || value > 1<<20 {
			return IndexingSettings{}, false
		}
		result.MaxFileBytes = value
	}
	if node := fields["mcp_admin_users"]; node != nil {
		var ok bool
		result.admins, result.adminCount, ok = loadUsernameList(node)
		if !settings.MCPEnabled || !ok {
			return IndexingSettings{}, false
		}
		allowed := settings.MCPAllowedUsernames()
		for _, admin := range result.AdminUsers() {
			if !slices.Contains(allowed, admin) {
				return IndexingSettings{}, false
			}
		}
	}
	return result, true
}
