package dav

import (
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"net/http"
	"strings"
)

// Parse list fields as a single grammar, not comma-split opaque quoted tags.
func parseTags(values []string) (files.TagExpectation, error) {
	out := files.TagExpectation{Present: len(values) > 0}
	if !out.Present {
		return out, nil
	}
	size := -1
	for _, v := range values {
		size += len(v) + 1
		if size > 8192 {
			return out, files.ErrConditionLimit
		}
	}
	s := strings.Trim(strings.Join(values, ","), " \t")
	if s == "*" {
		out.Any = true
		return out, nil
	}
	for s != "" {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			break
		}
		// RFC list extension permits bounded empty members.
		if s[0] == ',' {
			s = s[1:]
			continue
		}
		tag := files.EntityTag{}
		if strings.HasPrefix(s, "W/") {
			tag.Weak = true
			s = s[2:]
		}
		if len(s) == 0 || s[0] != '"' {
			return out, files.ErrConditionSyntax
		}
		end := 1
		for end < len(s) && s[end] != '"' {
			b := s[end]
			if b < 0x21 || b == 0x7f {
				return out, files.ErrConditionSyntax
			}
			end++
		}
		if end == len(s) {
			return out, files.ErrConditionSyntax
		}
		tag.Value = s[:end+1]
		out.Tags = append(out.Tags, tag)
		if len(out.Tags) > 128 {
			return out, files.ErrConditionLimit
		}
		s = strings.TrimLeft(s[end+1:], " \t")
		if s != "" {
			if s[0] != ',' {
				return out, files.ErrConditionSyntax
			}
			s = s[1:]
		}
	}
	if len(out.Tags) == 0 {
		return out, files.ErrConditionSyntax
	}
	return out, nil
}
func publicationExpectation(r *http.Request) files.Expectation {
	var e files.Expectation
	combined := 0
	for _, name := range []string{"If-Match", "If-None-Match"} {
		for i, v := range r.Header.Values(name) {
			combined += len(v)
			if i > 0 {
				combined++
			}
			if combined > 8192 {
				e.Invalid = files.ErrConditionLimit
				return e
			}
		}
	}
	e.Match, e.Invalid = parseTags(r.Header.Values("If-Match"))
	if e.Invalid != nil {
		return e
	}
	e.NotMatch, e.Invalid = parseTags(r.Header.Values("If-None-Match"))
	if e.Invalid != nil {
		return e
	}
	if len(e.Match.Tags)+len(e.NotMatch.Tags) > 128 {
		e.Invalid = files.ErrConditionLimit
		return e
	}
	if !e.Match.Present {
		if values := r.Header.Values("If-Unmodified-Since"); len(values) == 1 {
			e.UnmodifiedSince, _ = http.ParseTime(values[0])
		}
	}
	return e
}

// ServeContent owns read-condition precedence and range output. Validate full
// grammar and combine field lines first, retaining its snapshot-only probe.
func normalizedReadRequest(r *http.Request) (*http.Request, error) {
	e := publicationExpectation(r)
	if e.Invalid != nil {
		return nil, e.Invalid
	}
	clone := r.Clone(r.Context())
	for _, name := range []string{"If-Match", "If-None-Match"} {
		if values := r.Header.Values(name); len(values) > 0 {
			clone.Header.Set(name, strings.Join(values, ","))
		}
	}
	for _, name := range []string{"If-Unmodified-Since", "If-Modified-Since"} {
		if len(r.Header.Values(name)) != 1 {
			clone.Header.Del(name)
		}
	}
	return clone, nil
}
func permitsRange(r *http.Request) bool {
	values := r.Header.Values("If-Range")
	if len(values) == 0 || len(values) == 1 && values[0] == "" {
		return true
	}
	if len(values) != 1 {
		return false
	}
	tags, err := parseTags(values)
	return err == nil && !tags.Any && len(tags.Tags) == 1 && !tags.Tags[0].Weak && strings.Trim(values[0], " \t") == tags.Tags[0].Value
}
