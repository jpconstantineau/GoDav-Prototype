package dav

import (
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"net/http"
	"net/url"
	"strings"
)

// If is a bounded expression grammar. Tokens are identifiers, never fetched.
func parseDAVIf(r *http.Request) (files.DAVExpectation, error) {
	values := r.Header.Values("If")
	out := files.DAVExpectation{Present: len(values) > 0}
	if !out.Present {
		return out, nil
	}
	if len(values) != 1 {
		return out, files.ErrConditionSyntax
	}
	s := values[0]
	if len(s) > 8192 {
		return out, files.ErrConditionLimit
	}
	p := ifParser{s: s}
	p.space()
	if p.i == len(s) {
		return out, files.ErrConditionSyntax
	}
	tagged := s[p.i] == '<'
	for p.i < len(s) {
		path := ""
		if tagged {
			raw, ok := p.angle()
			if !ok {
				return out, files.ErrConditionSyntax
			}
			var err error
			path, err = ifResourcePath(r, raw)
			if err != nil {
				return out, err
			}
			p.space()
		}
		lists := 0
		for p.i < len(s) && s[p.i] == '(' {
			p.i++
			p.space()
			list := files.DAVList{Tagged: tagged, Path: path}
			for p.i < len(s) && s[p.i] != ')' {
				c := files.DAVCondition{}
				if len(s)-p.i >= 3 && protocolLiteral(s[p.i:p.i+3], "Not") {
					p.i += 3
					if p.i == len(s) || (s[p.i] != ' ' && s[p.i] != '\t' && s[p.i] != '<' && s[p.i] != '[') {
						return out, files.ErrConditionSyntax
					}
					p.space()
					c.Not = true
				}
				if p.i < len(s) && s[p.i] == '<' {
					token, ok := p.angle()
					if !ok || !validStateToken(token) {
						return out, files.ErrConditionSyntax
					}
					c.Token = token
				} else if p.i < len(s) && s[p.i] == '[' {
					p.i++
					if strings.HasPrefix(s[p.i:], "W/") {
						p.i += 2
						c.ETag.Weak = true
					}
					if p.i >= len(s) || s[p.i] != '"' {
						return out, files.ErrConditionSyntax
					}
					quote := p.i
					p.i++
					for p.i < len(s) && s[p.i] != '"' {
						if s[p.i] < 0x21 || s[p.i] == 0x7f {
							return out, files.ErrConditionSyntax
						}
						p.i++
					}
					if p.i >= len(s) {
						return out, files.ErrConditionSyntax
					}
					p.i++
					c.ETag.Value = s[quote:p.i]
					if p.i >= len(s) || s[p.i] != ']' {
						return out, files.ErrConditionSyntax
					}
					p.i++
				} else {
					return out, files.ErrConditionSyntax
				}
				list.Conditions = append(list.Conditions, c)
				p.conditions++
				if p.conditions > 128 {
					return out, files.ErrConditionLimit
				}
				p.space()
			}
			if p.i == len(s) || len(list.Conditions) == 0 {
				return out, files.ErrConditionSyntax
			}
			p.i++
			p.space()
			out.Lists = append(out.Lists, list)
			lists++
			if len(out.Lists) > 32 {
				return out, files.ErrConditionLimit
			}
		}
		if lists == 0 {
			return out, files.ErrConditionSyntax
		}
		if !tagged && p.i < len(s) {
			return out, files.ErrConditionSyntax
		}
	}
	return out, nil
}

type ifParser struct {
	s             string
	i, conditions int
}

func (p *ifParser) space() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}
func (p *ifParser) angle() (string, bool) {
	if p.i >= len(p.s) || p.s[p.i] != '<' {
		return "", false
	}
	p.i++
	start := p.i
	for p.i < len(p.s) && p.s[p.i] != '>' {
		b := p.s[p.i]
		if b <= 32 || b >= 127 || b == '<' || b == '"' {
			return "", false
		}
		p.i++
	}
	if p.i == len(p.s) || p.i == start {
		return "", false
	}
	v := p.s[start:p.i]
	p.i++
	return v, true
}
func validStateToken(s string) bool {
	if len(s) == 0 || len(s) > 8192 {
		return false
	}
	for _, b := range []byte(s) {
		if b <= 32 || b >= 127 || strings.ContainsRune(`<>"{}|\^`+"`", rune(b)) {
			return false
		}
	}
	u, err := url.Parse(s)
	if err != nil || !u.IsAbs() || strings.Contains(s, "#") {
		return false
	}
	// net/url deliberately leaves opaque URI data uninterpreted. Still require
	// URI percent escapes to be complete, even for non-HTTP token schemes.
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+2 >= len(s) || !uriHex(s[i+1]) || !uriHex(s[i+2]) {
			return false
		}
		i += 2
	}
	return true
}
func uriHex(b byte) bool { return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F' }
func ifResourcePath(r *http.Request, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", files.ErrConditionSyntax
	}
	if u.IsAbs() {
		a, ok := parseAuthority(u.Host)
		expected, valid := parseAuthority(r.Host)
		if u.Scheme != "https" || !ok || !valid || a != expected {
			return "", files.ErrConditionSyntax
		}
	} else if u.Host != "" || !strings.HasPrefix(raw, "/") {
		return "", files.ErrConditionSyntax
	}
	path, ok := checkedPath(u.EscapedPath())
	if !ok || (path != "/dav" && !strings.HasPrefix(path, "/dav/")) {
		return "", files.ErrConditionSyntax
	}
	return strings.Trim(strings.TrimPrefix(path, "/dav"), "/"), nil
}

// RFC 4918 section 2 imports RFC 2616 section 2.1: quoted protocol
// grammar literals are ASCII case-insensitive. This is not a data normalizer.
func protocolLiteral(value, literal string) bool {
	if len(value) != len(literal) {
		return false
	}
	for i := range value {
		a, b := value[i], literal[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}
