package config

import (
	"regexp"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"go.yaml.in/yaml/v3"
)

// SkillCatalog selects canonical skill names and permits authenticated users.
// The v1 pilot intentionally supports exactly the two configured endpoint names.
type SkillCatalog struct {
	Name string
	// All accepted names are ASCII and contain no spaces. Keeping this as
	// comparable value data preserves Settings recheck and copy semantics.
	users, skills string
}

func (c SkillCatalog) Users() []string  { return strings.Fields(c.users) }
func (c SkillCatalog) Skills() []string { return strings.Fields(c.skills) }

var pilotSkillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func parseSkillCatalogs(node *yaml.Node) ([2]SkillCatalog, bool) {
	var result [2]SkillCatalog
	entries, ok := mapping(node, "coding", "research")
	if !ok {
		return result, false
	}
	for i, name := range []string{"coding", "research"} {
		spec, ok := mapping(entries[name], "users", "skills")
		if !ok {
			return [2]SkillCatalog{}, false
		}
		users, ok := catalogValues(spec["users"], 16, auth.ValidUsername)
		if !ok {
			return [2]SkillCatalog{}, false
		}
		skills, ok := catalogValues(spec["skills"], 32, func(s string) bool {
			return len(s) <= 64 && pilotSkillName.MatchString(s)
		})
		if !ok {
			return [2]SkillCatalog{}, false
		}
		result[i] = SkillCatalog{Name: name, users: strings.Join(users, " "), skills: strings.Join(skills, " ")}
	}
	return result, true
}
func catalogValues(node *yaml.Node, limit int, valid func(string) bool) ([]string, bool) {
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) == 0 || len(node.Content) > limit {
		return nil, false
	}
	result := make([]string, 0, len(node.Content))
	seen := make(map[string]bool, len(node.Content))
	for _, it := range node.Content {
		if it.Kind != yaml.ScalarNode || it.Tag != "!!str" || !valid(it.Value) {
			return nil, false
		}
		name := strings.ToLower(it.Value)
		if seen[name] {
			return nil, false
		}
		seen[name] = true
		result = append(result, it.Value)
	}
	return result, true
}
