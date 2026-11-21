package config

import (
	"slices"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"go.yaml.in/yaml/v3"
)

const maxMCPUsernames = 32

// MCPAllowedUsernames returns canonical names in a caller-owned slice. The
// internal representation keeps Settings comparable for startup rechecks.
func (s Settings) MCPAllowedUsernames() []string {
	if !s.MCPEnabled {
		return nil
	}
	if s.MCPUsername != "" {
		return []string{strings.ToLower(s.MCPUsername)}
	}
	return slices.Clone(s.mcpUsernames[:s.mcpUsernameCount])
}

func (s *Settings) loadMCPUsernames(single, multiple *yaml.Node) bool {
	if (single == nil) == (multiple == nil) {
		return false
	}
	valid := func(node *yaml.Node) bool {
		return node.Kind == yaml.ScalarNode && node.Tag == "!!str" && auth.ValidUsername(node.Value)
	}
	if single != nil {
		if !valid(single) {
			return false
		}
		s.MCPUsername = single.Value
		return true
	}
	if multiple.Kind != yaml.SequenceNode || len(multiple.Content) < 1 || len(multiple.Content) > len(s.mcpUsernames) {
		return false
	}
	for _, item := range multiple.Content {
		if !valid(item) {
			return false
		}
		name := strings.ToLower(item.Value)
		if slices.Contains(s.mcpUsernames[:s.mcpUsernameCount], name) {
			return false
		}
		s.mcpUsernames[s.mcpUsernameCount] = name
		s.mcpUsernameCount++
	}
	slices.Sort(s.mcpUsernames[:s.mcpUsernameCount])
	return true
}
