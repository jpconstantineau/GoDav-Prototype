package mcpfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"strings"
	"time"
	"unicode"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

const defaultContentSearchLimit = 25

func contentSearchDescriptor() Descriptor {
	text := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	size := func(description string) map[string]any {
		return map[string]any{"type": "integer", "minimum": 0, "maximum": files.AbsoluteMaxFileBytes, "description": description}
	}
	return Descriptor{"search_file_contents", "Search whole UTF-8 text files by a word or phrase in this file library, not by filename. Results are ranked and include paths and metadata, not file contents. Narrow by folder, extension, media type, modified date, or byte size. A broad search can exceed the budget; use a smaller folder.", map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"query"},
		"properties": map[string]any{
			"query":           map[string]any{"type": "string", "minLength": 1, "maxLength": files.MaxContentSearchQueryBytes, "description": "A word or contiguous phrase, at most 512 UTF-8 bytes. Uses FTS5 token matching; operators and wildcards are not interpreted."},
			"path":            text("Directory relative to the configured file library, including subdirectories. Omit or use an empty string for the library root."),
			"extension":       text("Exact final extension, including the dot, for example .md. Case-insensitive."),
			"content_type":    text("Exact base MIME type, for example text/plain. Case-insensitive, no parameters or wildcards."),
			"modified_after":  text("Inclusive lower bound for last modified time, in RFC3339 with timezone."),
			"modified_before": text("Exclusive upper bound for last modified time, in RFC3339 with timezone."),
			"min_size":        size("Inclusive minimum size in bytes."),
			"max_size":        size("Inclusive maximum size in bytes."),
			"limit":           map[string]any{"type": "integer", "minimum": 1, "maximum": files.MaxContentSearchResults, "default": defaultContentSearchLimit, "description": "Maximum returned matches. partial=true means more matches exist; narrow the filters."},
		},
	}, nil}
}

func (h *Handler) searchContent(ctx context.Context, principal string, raw json.RawMessage) (any, error) {
	var a struct {
		Query          string `json:"query"`
		Path           string `json:"path"`
		Extension      string `json:"extension"`
		ContentType    string `json:"content_type"`
		ModifiedAfter  string `json:"modified_after"`
		ModifiedBefore string `json:"modified_before"`
		MinSize        *int64 `json:"min_size"`
		MaxSize        *int64 `json:"max_size"`
		Limit          *int   `json:"limit"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&a) != nil || dec.Decode(new(any)) != io.EOF || len(a.Query) > files.MaxContentSearchQueryBytes || strings.ContainsRune(a.Query, 0) || !strings.ContainsFunc(a.Query, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsNumber(r)
	}) {
		return nil, files.ErrInvalid
	}
	path, err := h.resolve(a.Path, true)
	if err != nil {
		return nil, err
	}
	opts := files.ContentSearchOptions{
		Query: strings.TrimSpace(a.Query), MinSize: a.MinSize, MaxSize: a.MaxSize, Limit: defaultContentSearchLimit,
	}
	if a.Extension != "" {
		if !validSearchExtension(a.Extension) {
			return nil, files.ErrInvalid
		}
		opts.Extension = strings.ToLower(a.Extension)
	}
	if a.ContentType != "" {
		mt, params, err := mime.ParseMediaType(a.ContentType)
		if err != nil || len(params) != 0 || !strings.Contains(mt, "/") || strings.Contains(mt, "*") || len(a.ContentType) > 255 {
			return nil, files.ErrInvalid
		}
		opts.ContentType = mt
	}
	if a.ModifiedAfter != "" {
		opts.ModifiedAfter, err = time.Parse(time.RFC3339Nano, a.ModifiedAfter)
		if err != nil || opts.ModifiedAfter.IsZero() {
			return nil, files.ErrInvalid
		}
	}
	if a.ModifiedBefore != "" {
		opts.ModifiedBefore, err = time.Parse(time.RFC3339Nano, a.ModifiedBefore)
		if err != nil || opts.ModifiedBefore.IsZero() {
			return nil, files.ErrInvalid
		}
	}
	if !opts.ModifiedAfter.IsZero() && !opts.ModifiedBefore.IsZero() && !opts.ModifiedAfter.Before(opts.ModifiedBefore) {
		return nil, files.ErrInvalid
	}
	if !validSearchSize(opts.MinSize) || !validSearchSize(opts.MaxSize) || opts.MinSize != nil && opts.MaxSize != nil && *opts.MinSize > *opts.MaxSize {
		return nil, files.ErrInvalid
	}
	if a.Limit != nil {
		opts.Limit = *a.Limit
	}
	if opts.Limit < 1 || opts.Limit > files.MaxContentSearchResults {
		return nil, files.ErrInvalid
	}
	result, err := h.service.SearchContent(ctx, path, principal, opts)
	if err != nil {
		return nil, err
	}
	if result.Entries == nil {
		result.Entries = []files.ContentSearchHit{}
	}
	for i := range result.Entries {
		rel, err := h.searchRelative(result.Entries[i].Path)
		if err != nil || rel == "" {
			return nil, files.ErrUnavailable
		}
		result.Entries[i].Path = rel
	}
	return result, nil
}

func validSearchExtension(extension string) bool {
	if len(extension) < 2 || len(extension) > 33 || extension[0] != '.' {
		return false
	}
	for i := 1; i < len(extension); i++ {
		c := extension[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validSearchSize(n *int64) bool {
	return n == nil || *n >= 0 && *n <= files.AbsoluteMaxFileBytes
}
