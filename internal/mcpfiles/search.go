package mcpfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

const MaxSearchResults = 100
const MaxSearchEntries = 4096
const MaxSearchDirectories = 32
const MaxSearchQueryBytes = 4096

func searchDescriptor() Descriptor {
	return Descriptor{"search_files", "Find files by a case-insensitive literal substring of their path, including the filename. Searches through subfolders, not file contents. Pass a returned path to read_file. If partial is true, narrow the search to a smaller folder or a more specific query.", map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"path", "query"}, "properties": map[string]any{
			"path":  map[string]any{"type": "string", "description": "Decoded directory path relative to the configured files root; empty string searches that root. Returned paths are relative to the files root, not to this search directory. No absolute paths or traversal."},
			"query": map[string]any{"type": "string", "minLength": 1, "maxLength": MaxSearchQueryBytes, "description": "Nonempty literal path substring, at most 4096 UTF-8 bytes; slash matches path separators. Whitespace is literal."},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": MaxSearchResults, "default": MaxSearchResults, "description": "Maximum returned file matches (not scan size), default 100. Reaching this limit can return partial=true even if no additional matches exist."},
		}}, nil}
}

type SearchEntry struct {
	Path string `json:"path"`
	Entry
}
type SearchResult struct {
	Entries            []SearchEntry `json:"entries"`
	Partial            bool          `json:"partial"`
	StopReason         string        `json:"stop_reason,omitempty"`
	ScannedEntries     int           `json:"scanned_entries"`
	ScannedDirectories int           `json:"scanned_directories"`
}

func (h *Handler) search(ctx context.Context, principal string, raw json.RawMessage) (any, error) {
	var a struct {
		Path  *string `json:"path"`
		Query *string `json:"query"`
		Limit *int    `json:"limit"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&a) != nil || dec.Decode(new(any)) != io.EOF || a.Path == nil || a.Query == nil || *a.Query == "" || len(*a.Query) > MaxSearchQueryBytes {
		return nil, files.ErrInvalid
	}
	limit := MaxSearchResults
	if a.Limit != nil {
		limit = *a.Limit
	}
	if limit < 1 || limit > MaxSearchResults {
		return nil, files.ErrInvalid
	}
	path, err := h.resolve(*a.Path, true)
	if err != nil {
		return nil, err
	}
	query := files.Key(*a.Query)
	result := SearchResult{Entries: make([]SearchEntry, 0, min(limit, MaxSearchResults))}
	stop := func(reason string) { result.Partial = true; result.StopReason = reason }
	var visit func(string, bool) error
	visit = func(path string, root bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if result.ScannedDirectories >= MaxSearchDirectories {
			stop("directory_limit")
			return nil
		}
		// Snapshot materializes all children. Reserve its worst-case size before
		// calling, so total loaded entries never exceeds the budget, even on mutation.
		if MaxSearchEntries-result.ScannedEntries < files.MaxChildren {
			stop("scan_limit")
			return nil
		}
		snap, err := h.service.Snapshot(ctx, path, true, files.Expectation{Principal: principal})
		if err != nil {
			if !root && errors.Is(err, files.ErrMissing) {
				stop("tree_changed")
				return nil
			}
			return err
		}
		result.ScannedDirectories++
		if snap.Node.File {
			if root {
				return files.ErrInvalid
			}
			stop("tree_changed")
			return nil
		}
		if len(snap.Children) > files.MaxChildren {
			return files.ErrUnavailable
		}
		result.ScannedEntries += len(snap.Children)
		rel, err := h.searchRelative(snap.Path)
		if err != nil {
			return err
		}
		sort.Slice(snap.Children, func(i, j int) bool { return snap.Children[i].Name < snap.Children[j].Name })
		for _, n := range snap.Children {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !files.ValidName(n.Name) {
				return files.ErrUnavailable
			}
			child := n.Name
			if rel != "" {
				child = rel + "/" + child
			}
			if n.File {
				if strings.Contains(files.Key(child), query) {
					result.Entries = append(result.Entries, SearchEntry{Path: child, Entry: entry(n)})
					if len(result.Entries) == limit {
						stop("result_limit")
						return nil
					}
				}
			} else {
				absolute, err := h.resolve(child, true)
				if err != nil {
					return err
				}
				if err = visit(absolute, false); err != nil {
					return err
				}
				if result.Partial {
					return nil
				}
			}
		}
		return nil
	}
	if err := visit(path, true); err != nil {
		return nil, err
	}
	return result, nil
}

// Strip the configured scope by component count, not byte length or spelling:
// Unicode simple-fold-equivalent spellings can have different UTF-8 lengths.
func (h *Handler) searchRelative(canonical string) (string, error) {
	names, err := files.Components(canonical)
	if err != nil {
		return "", files.ErrUnavailable
	}
	scope, err := files.Components(h.scope)
	if err != nil || len(names) < len(scope) {
		return "", files.ErrUnavailable
	}
	for i, n := range scope {
		if files.Key(n) != files.Key(names[i]) {
			return "", files.ErrUnavailable
		}
	}
	return strings.Join(names[len(scope):], "/"), nil
}
