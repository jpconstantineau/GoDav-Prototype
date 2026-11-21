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
	if single != nil {
		if !validUsernameNode(single) {
			return false
		}
		s.MCPUsername = single.Value
		return true
	}
	var ok bool
	s.mcpUsernames, s.mcpUsernameCount, ok = loadUsernameList(multiple)
	return ok
}

func validUsernameNode(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!str" && auth.ValidUsername(node.Value)
}

func loadUsernameList(node *yaml.Node) (names [maxMCPUsernames]string, count int, ok bool) {
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) < 1 || len(node.Content) > len(names) {
		return names, 0, false
	}
	for _, item := range node.Content {
		if !validUsernameNode(item) {
			return [maxMCPUsernames]string{}, 0, false
		}
		name := strings.ToLower(item.Value)
		if slices.Contains(names[:count], name) {
			return [maxMCPUsernames]string{}, 0, false
		}
		names[count] = name
		count++
	}
	slices.Sort(names[:count])
	return names, count, true
}
