// Package mcpfiles provides bounded file tools over the shared namespace service.
// The transport must enforce its write ceiling before calling mutation tools.
package mcpfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

const MaxContentBytes = 64 << 10
const MaxListEntries = 100
const MaxArgumentBytes = 6*MaxContentBytes + 8192

type Descriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}
type Handler struct {
	service  *files.Service
	scope    string
	maxBytes int64
}

func New(service *files.Service, scope string, maxBytes int64) (*Handler, error) {
	names, err := files.Components(scope)
	if err != nil || service == nil || maxBytes < 1 || maxBytes > files.AbsoluteMaxFileBytes {
		return nil, files.ErrInvalid
	}
	return &Handler{service: service, scope: strings.Join(names, "/"), maxBytes: min(maxBytes, MaxContentBytes)}, nil
}
func IsWrite(name string) bool {
	return name == "create_file" || name == "edit_file" || name == "delete_file"
}
func Tools() []Descriptor {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	schema := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	path := str("Decoded path relative to the configured logical root; no traversal, absolute paths, or URL decoding.")
	etag := str("Exact strong ETag returned by read_file/list_files; required for atomic conflict checking.")
	content := map[string]any{"type": "string", "maxLength": MaxContentBytes, "description": "Complete UTF-8 text, at most 65536 encoded bytes."}
	return []Descriptor{
		{"list_files", "List files and folders directly inside one folder in the configured file library. Use an empty path to list the library root. This does not list other libraries or skills. Pages can shift if files change between calls.", schema(map[string]any{"path": path, "offset": map[string]any{"type": "integer", "minimum": 0, "maximum": files.MaxChildren}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": MaxListEntries}}, "path")},
		{"read_file", "Read a UTF-8 text file up to 65536 bytes. The returned ETag identifies the version read; pass it to edit_file or delete_file to avoid changing a newer version. Larger or binary files are refused.", schema(map[string]any{"path": path}, "path")},
		{"create_file", "Create one UTF-8 text file without overwriting. The parent folder must exist. A WebDAV lock can block this change; do not try to bypass it.", schema(map[string]any{"path": path, "content": content}, "path", "content")},
		{"edit_file", "Replace the entire UTF-8 text of a file only if expected_etag still matches. A WebDAV lock can block this change; do not try to bypass it.", schema(map[string]any{"path": path, "content": content, "expected_etag": etag}, "path", "content", "expected_etag")},
		{"delete_file", "Delete one file only if expected_etag still matches. Never deletes a folder. A WebDAV lock can block this change; do not try to bypass it.", schema(map[string]any{"path": path, "expected_etag": etag}, "path", "expected_etag")},
	}
}
func (h *Handler) Tools() []Descriptor {
	tools := Tools()
	for _, tool := range tools {
		if tool.Name == "create_file" || tool.Name == "edit_file" {
			tool.InputSchema["properties"].(map[string]any)["content"].(map[string]any)["maxLength"] = h.maxBytes
		}
	}
	return tools
}

type arguments struct {
	Path         *string `json:"path"`
	Content      *string `json:"content"`
	ExpectedETag *string `json:"expected_etag"`
	Offset       *int    `json:"offset"`
	Limit        *int    `json:"limit"`
}
type Entry struct {
	Name string `json:"name"`
	File bool   `json:"file"`
	Size int64  `json:"size"`
	ETag string `json:"etag,omitempty"`
}

func entry(n files.Node) Entry { return Entry{n.Name, n.File, n.Size, n.ETag} }
func match(principal, tag string) files.Expectation {
	return files.Expectation{Principal: principal, Match: files.TagExpectation{Present: true, Tags: []files.EntityTag{{Value: tag}}}}
}
func validTag(tag string) bool {
	if len(tag) < 2 || len(tag) > 256 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return false
	}
	for _, b := range []byte(tag[1 : len(tag)-1]) {
		if b < 0x21 || b > 0x7e || b == '"' {
			return false
		}
	}
	return true
}
func (h *Handler) resolve(path string, directory bool) (string, error) {
	if !directory && (path == "" || strings.HasSuffix(path, "/")) {
		return "", files.ErrInvalid
	}
	names, err := files.Components(path)
	if err != nil {
		return "", err
	}
	rel := strings.Join(names, "/")
	if h.scope != "" {
		if rel == "" {
			rel = h.scope
		} else {
			rel = h.scope + "/" + rel
		}
	}
	if _, err = files.Components(rel); err != nil {
		return "", err
	}
	return rel, nil
}

// Call requires an authenticated principal. Authorization to write is a caller
// concern, separate from the repository's atomic ETag and lock enforcement.
func (h *Handler) Call(ctx context.Context, principal, name string, raw json.RawMessage) (any, error) {
	if principal == "" || len(raw) > MaxArgumentBytes || !utf8.Valid(raw) {
		return nil, files.ErrInvalid
	}
	var a arguments
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, files.ErrInvalid
	}
	if dec.Decode(new(any)) != io.EOF || a.Path == nil {
		return nil, files.ErrInvalid
	}
	if name != "list_files" && (a.Offset != nil || a.Limit != nil) {
		return nil, files.ErrInvalid
	}
	if name != "create_file" && name != "edit_file" && a.Content != nil {
		return nil, files.ErrInvalid
	}
	if name != "edit_file" && name != "delete_file" && a.ExpectedETag != nil {
		return nil, files.ErrInvalid
	}
	path, err := h.resolve(*a.Path, name == "list_files")
	if err != nil {
		return nil, err
	}
	e := files.Expectation{Principal: principal}
	switch name {
	case "list_files":
		offset, limit := 0, MaxListEntries
		if a.Offset != nil {
			offset = *a.Offset
		}
		if a.Limit != nil {
			limit = *a.Limit
		}
		if offset < 0 || offset > files.MaxChildren || limit < 1 || limit > MaxListEntries {
			return nil, files.ErrInvalid
		}
		snap, err := h.service.Snapshot(ctx, path, true, e)
		if err != nil {
			return nil, err
		}
		if snap.Node.File {
			return nil, files.ErrInvalid
		}
		sort.Slice(snap.Children, func(i, j int) bool { return snap.Children[i].Name < snap.Children[j].Name })
		end := min(offset+limit, len(snap.Children))
		start := min(offset, len(snap.Children))
		entries := make([]Entry, 0, end-start)
		for _, n := range snap.Children[start:end] {
			entries = append(entries, entry(n))
		}
		result := map[string]any{"entries": entries}
		if end < len(snap.Children) {
			result["next_offset"] = end
		}
		return result, nil
	case "read_file":
		snap, err := h.service.Snapshot(ctx, path, false, e)
		if err != nil {
			return nil, err
		}
		if !snap.Node.File {
			return nil, files.ErrInvalid
		}
		if snap.Node.Size > MaxContentBytes {
			return nil, files.ErrTooLarge
		}
		snap, data, release, err := h.service.Read(ctx, path, match(principal, snap.Node.ETag))
		defer release()
		if err != nil {
			return nil, err
		}
		if len(data) > MaxContentBytes {
			return nil, files.ErrTooLarge
		}
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return nil, files.ErrInvalid
		}
		return map[string]any{"file": entry(snap.Node), "content": string(data)}, nil
	case "create_file", "edit_file":
		if a.Content == nil || !utf8.ValidString(*a.Content) || strings.ContainsRune(*a.Content, 0) {
			return nil, files.ErrInvalid
		}
		if int64(len(*a.Content)) > h.maxBytes {
			return nil, files.ErrTooLarge
		}
		contentType := "text/plain; charset=utf-8"
		if name == "create_file" {
			e.NotMatch = files.TagExpectation{Present: true, Any: true}
		} else {
			if a.ExpectedETag == nil || !validTag(*a.ExpectedETag) {
				return nil, files.ErrInvalid
			}
			e = match(principal, *a.ExpectedETag)
			snap, err := h.service.Snapshot(ctx, path, false, e)
			if err != nil {
				return nil, err
			}
			if !snap.Node.File {
				return nil, files.ErrInvalid
			}
			if snap.Node.Size > MaxContentBytes {
				return nil, files.ErrTooLarge
			}
			// The same expected tag pins this validation read and the subsequent
			// publication. Concurrent replacement cannot bypass text validation.
			snap, data, release, err := h.service.Read(ctx, path, e)
			if err != nil {
				release()
				return nil, err
			}
			tooLarge := len(data) > MaxContentBytes
			text := utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
			release()
			if tooLarge {
				return nil, files.ErrTooLarge
			}
			if !text {
				return nil, files.ErrInvalid
			}
			contentType = snap.Node.ContentType
		}
		n, created, err := h.service.Put(ctx, path, strings.NewReader(*a.Content), int64(len(*a.Content)), contentType, h.maxBytes, e)
		if err != nil {
			return nil, err
		}
		return map[string]any{"file": entry(n), "created": created}, nil
	case "delete_file":
		if a.ExpectedETag == nil || !validTag(*a.ExpectedETag) {
			return nil, files.ErrInvalid
		}
		if err := h.service.Delete(ctx, path, files.DeleteRequest{FiniteDepth: true}, match(principal, *a.ExpectedETag)); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true}, nil
	default:
		return nil, files.ErrInvalid
	}
}
