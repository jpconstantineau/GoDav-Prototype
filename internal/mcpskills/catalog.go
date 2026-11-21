// Package mcpskills exposes a bounded, read-only Skills catalog over the DAV domain.
// It never executes skill content or accesses host filesystem paths.
package mcpskills

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"go.yaml.in/yaml/v3"
)

const MaxSkills = 32
const MaxFiles = 512
const MaxFileBytes = 1 << 20
const MaxCatalogBytes = 16 << 20
const MaxFrontmatterBytes = 32 << 10

var errCatalog = errors.New("skill catalog unavailable or exceeds limits")
var errParams = errors.New("invalid or unavailable skill resource")
var skillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type Resource struct {
	URI    string `json:"uri"`
	Digest string `json:"digest"`
	Size   int    `json:"size"`
}
type Skill struct {
	URI         string         `json:"uri"`
	Frontmatter map[string]any `json:"frontmatter"`
	Resources   []Resource     `json:"resources"`
	Meta        map[string]any `json:"_meta"`
}
type content struct {
	data []byte
	mime string
}
type catalog struct {
	skills                    []Skill
	content                   map[string]content
	bytes, count, directories int
}

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}
func resourceURI(path string) string {
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		parts[i] = url.PathEscape(parts[i])
	}
	return "skill://" + strings.Join(parts, "/")
}
func canonicalURI(uri string) (string, error) {
	if len(uri) > files.MaxPathBytes*3 || !strings.HasPrefix(uri, "skill://") || strings.HasSuffix(uri, "/") {
		return "", errParams
	}
	raw := strings.TrimPrefix(uri, "skill://")
	parts := strings.Split(raw, "/")
	if len(parts) < 2 || !skillName.MatchString(parts[0]) || len(parts[0]) > 64 {
		return "", errParams
	}
	for i := 1; i < len(parts); i++ {
		decoded, err := url.PathUnescape(parts[i])
		if err != nil || strings.ContainsAny(decoded, "/\\") {
			return "", errParams
		}
		parts[i] = decoded
	}
	path := strings.Join(parts, "/")
	if _, err := files.Components(path); err != nil || resourceURI(path) != uri {
		return "", errParams
	}
	return path, nil
}

// frontmatter rejects aliases and non-JSON YAML values rather than silently
// rewriting them. Every accepted author field is preserved, including unknowns.
func frontmatter(data []byte, name string) (map[string]any, error) {
	if !utf8.Valid(data) {
		return nil, errCatalog
	}
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(normalized, []byte("---\n")) {
		return nil, errCatalog
	}
	rest := normalized[4:]
	end := bytes.Index(rest, []byte("\n---\n"))
	if end < 0 && bytes.HasSuffix(rest, []byte("\n---")) {
		end = len(rest) - 4
	}
	if end < 0 || end > MaxFrontmatterBytes {
		return nil, errCatalog
	}
	var root yaml.Node
	if yaml.Unmarshal(rest[:end], &root) != nil || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, errCatalog
	}
	var convert func(*yaml.Node, int) (any, error)
	convert = func(n *yaml.Node, depth int) (any, error) {
		if depth > 16 || n.Kind == yaml.AliasNode || n.Anchor != "" {
			return nil, errCatalog
		}
		switch n.Kind {
		case yaml.MappingNode:
			out := map[string]any{}
			for i := 0; i < len(n.Content); i += 2 {
				k := n.Content[i]
				if k.Kind != yaml.ScalarNode || k.Tag != "!!str" {
					return nil, errCatalog
				}
				if _, ok := out[k.Value]; ok {
					return nil, errCatalog
				}
				v, e := convert(n.Content[i+1], depth+1)
				if e != nil {
					return nil, e
				}
				out[k.Value] = v
			}
			return out, nil
		case yaml.SequenceNode:
			out := []any{}
			for _, child := range n.Content {
				v, e := convert(child, depth+1)
				if e != nil {
					return nil, e
				}
				out = append(out, v)
			}
			return out, nil
		case yaml.ScalarNode:
			switch n.Tag {
			case "!!str":
				return n.Value, nil
			case "!!null":
				return nil, nil
			case "!!bool", "!!int", "!!float":
				var v any
				if n.Decode(&v) != nil {
					return nil, errCatalog
				}
				if _, e := json.Marshal(v); e != nil {
					return nil, errCatalog
				}
				return v, nil
			}
		}
		return nil, errCatalog
	}
	v, err := convert(root.Content[0], 0)
	if err != nil {
		return nil, err
	}
	out := v.(map[string]any)
	n, ok := out["name"].(string)
	if !ok || n != name || len(n) > 64 || !skillName.MatchString(n) {
		return nil, errCatalog
	}
	desc, ok := out["description"].(string)
	if !ok || strings.TrimSpace(desc) == "" || utf8.RuneCountInString(desc) > 1024 {
		return nil, errCatalog
	}
	return out, nil
}

func (h *handler) catalog(ctx context.Context) (_ *catalog, resultErr error) {
	c := &catalog{skills: []Skill{}, content: map[string]content{}}
	defer func() {
		if resultErr != nil {
			c.close()
		}
	}()
	// Detect mutations during traversal rather than publishing a manifest with a
	// known stale listing. This is not a database-wide snapshot transaction.
	observed := map[string]string{}
	remember := func(path string, s files.Snapshot) { observed[path] = snapshotIdentity(s) }
	top, err := h.files.Snapshot(ctx, h.scope, true)
	if err != nil || top.Node.File {
		return nil, errCatalog
	}
	remember(h.scope, top)
	sort.Slice(top.Children, func(i, j int) bool { return top.Children[i].Name < top.Children[j].Name })
	for _, dir := range top.Children {
		if dir.File {
			continue
		}
		if !skillName.MatchString(dir.Name) || len(dir.Name) > 64 {
			continue
		}
		// Draft folders and unrelated collections are not published skills. Check
		// the small entry before walking any supporting tree; do not reveal skipped
		// folder names or make one invalid draft hide an otherwise valid catalog.
		entryPath := h.scope + "/" + dir.Name + "/SKILL.md"
		entry, e := h.files.Snapshot(ctx, entryPath, false)
		if errors.Is(e, files.ErrMissing) || errors.Is(e, files.ErrParentMissing) {
			continue
		}
		if e != nil {
			return nil, errCatalog
		}
		if !entry.Node.File {
			continue
		}
		if entry.Node.Size > MaxFileBytes {
			return nil, errCatalog
		}
		expected := files.Expectation{Match: files.TagExpectation{Present: true, Tags: []files.EntityTag{{Value: entry.Node.ETag}}}}
		_, entryBytes, release, e := h.files.Read(ctx, entryPath, expected)
		if e != nil {
			release()
			return nil, errCatalog
		}
		_, parseErr := frontmatter(entryBytes, dir.Name)
		release()
		if parseErr != nil {
			continue
		}
		if len(c.skills) >= MaxSkills {
			return nil, errCatalog
		}
		resources := []Resource{}
		var walk func(string, int) error
		walk = func(path string, depth int) error {
			c.directories++
			if c.directories > MaxFiles {
				return errCatalog
			}
			if depth > files.MaxDepth || ctx.Err() != nil {
				return errCatalog
			}
			snap, e := h.files.Snapshot(ctx, h.scope+"/"+path, true)
			if e != nil || snap.Node.File {
				return errCatalog
			}
			remember(h.scope+"/"+path, snap)
			sort.Slice(snap.Children, func(i, j int) bool { return snap.Children[i].Name < snap.Children[j].Name })
			for _, node := range snap.Children {
				child := path + "/" + node.Name
				if !node.File {
					if e := walk(child, depth+1); e != nil {
						return e
					}
					continue
				}
				if c.count >= MaxFiles || node.Size > MaxFileBytes || node.Size < 0 || int64(c.bytes)+node.Size > MaxCatalogBytes {
					return errCatalog
				}
				expectation := files.Expectation{Match: files.TagExpectation{Present: true, Tags: []files.EntityTag{{Value: node.ETag}}}}
				got, data, release, e := h.files.Read(ctx, h.scope+"/"+child, expectation)
				if e != nil {
					release()
					return errCatalog
				}
				if len(data) > MaxFileBytes || c.bytes+len(data) > MaxCatalogBytes || got.Node.ID != node.ID {
					release()
					return errCatalog
				}
				copyData := bytes.Clone(data)
				release()
				uri := resourceURI(child)
				mime := got.Node.ContentType
				if node.Name == "SKILL.md" {
					mime = "text/markdown"
				}
				c.content[uri] = content{data: copyData, mime: mime}
				c.bytes += len(copyData)
				c.count++
				resources = append(resources, Resource{uri, digest(copyData), len(copyData)})
			}
			return nil
		}
		if e := walk(dir.Name, 0); e != nil {
			return nil, e
		}
		uri := resourceURI(dir.Name + "/SKILL.md")
		main, ok := c.content[uri]
		if !ok {
			return nil, errCatalog
		}
		fm, e := frontmatter(main.data, dir.Name)
		if e != nil {
			return nil, e
		}
		sort.Slice(resources, func(i, j int) bool { return resources[i].URI < resources[j].URI })
		encoded, _ := json.Marshal(resources)
		c.skills = append(c.skills, Skill{uri, fm, resources, map[string]any{"io.godav/revision": digest(encoded)}})
	}
	for path, identity := range observed {
		snap, e := h.files.Snapshot(ctx, path, true)
		if e != nil || snapshotIdentity(snap) != identity {
			return nil, errCatalog
		}
	}
	return c, nil
}

func snapshotIdentity(s files.Snapshot) string {
	type node struct {
		ID, Name, ETag string
		File           bool
		Size           int64
	}
	rows := make([]node, 0, len(s.Children))
	for _, n := range s.Children {
		rows = append(rows, node{n.ID, n.Name, n.ETag, n.File, n.Size})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	b, _ := json.Marshal(struct {
		ID   string
		Rows []node
	}{s.Node.ID, rows})
	return digest(b)
}

func (c *catalog) skill(uri string) (Skill, error) {
	if _, e := canonicalURI(uri); e != nil {
		return Skill{}, e
	}
	for _, s := range c.skills {
		if s.URI == uri {
			return s, nil
		}
	}
	return Skill{}, errParams
}
func (c *catalog) close() {
	for _, v := range c.content {
		clear(v.data)
	}
}
func (c *catalog) read(uri string) (map[string]any, error) {
	if _, e := canonicalURI(uri); e != nil {
		return nil, e
	}
	v, ok := c.content[uri]
	if !ok {
		return nil, errParams
	}
	result := map[string]any{"uri": uri, "mimeType": v.mime}
	if utf8.Valid(v.data) {
		result["text"] = string(v.data)
	} else {
		result["blob"] = encodeBytes(v.data)
	}
	return result, nil
}

// Scope paths are decoded, relative DAV paths (for example "Skills"), not OS paths.
func validateScope(scope string) error {
	parts, e := files.Components(scope)
	if e != nil || len(parts) == 0 || strings.HasSuffix(scope, "/") {
		return fmt.Errorf("invalid MCP skills scope")
	}
	return nil
}
