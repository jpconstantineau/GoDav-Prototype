package dav

import (
	"encoding/xml"
	"errors"
	"maps"
)

const xmlNamespace = "http://www.w3.org/XML/1998/namespace"
const xmlnsNamespace = "http://www.w3.org/2000/xmlns/"

// encoding/xml's strict mode does not reject unbound namespace prefixes.
// Validate raw lexical names and scoped bindings before the standard decoder
// expands them. This reader also sees every ignored extension subtree.
type namespaceTokens struct {
	decoder *xml.Decoder
	scopes  []map[string]string
	names   []xml.Name
}

func (n *namespaceTokens) Token() (xml.Token, error) {
	token, err := n.decoder.RawToken()
	if err != nil {
		return nil, err
	}
	invalid := errors.New("invalid XML namespace")
	switch t := token.(type) {
	case xml.StartElement:
		scope := map[string]string{"xml": xmlNamespace}
		if len(n.scopes) > 0 {
			scope = maps.Clone(n.scopes[len(n.scopes)-1])
		}
		declared := map[string]bool{}
		for _, attr := range t.Attr {
			if !ncName(attr.Name.Local) || (attr.Name.Space != "" && !ncName(attr.Name.Space)) {
				return nil, invalid
			}
			prefix := ""
			if attr.Name.Space == "xmlns" {
				prefix = attr.Name.Local
			} else if attr.Name.Space != "" || attr.Name.Local != "xmlns" {
				continue
			}
			if declared[prefix] || prefix == "xmlns" || attr.Value == xmlnsNamespace || prefix == "xml" && attr.Value != xmlNamespace || prefix != "xml" && attr.Value == xmlNamespace || prefix != "" && attr.Value == "" {
				return nil, invalid
			}
			declared[prefix] = true
			scope[prefix] = attr.Value
		}
		bound := func(name xml.Name) bool {
			return ncName(name.Local) && (name.Space == "" || ncName(name.Space) && name.Space != "xmlns" && scope[name.Space] != "")
		}
		if !bound(t.Name) {
			return nil, invalid
		}
		for _, attr := range t.Attr {
			if attr.Name.Space == "xmlns" || attr.Name.Space == "" && attr.Name.Local == "xmlns" {
				continue
			}
			if !bound(attr.Name) {
				return nil, invalid
			}
		}
		n.scopes = append(n.scopes, scope)
		n.names = append(n.names, t.Name)
	case xml.EndElement:
		last := len(n.names) - 1
		if last < 0 || n.names[last] != t.Name {
			return nil, invalid
		}
		n.names = n.names[:last]
		n.scopes = n.scopes[:last]
	}
	return token, nil
}

// XML 1.0 (fifth edition) NameStartChar/NameChar, excluding colon (NCName).
func ncName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		start := r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= 0xc0 && r <= 0xd6 || r >= 0xd8 && r <= 0xf6 || r >= 0xf8 && r <= 0x2ff || r >= 0x370 && r <= 0x37d || r >= 0x37f && r <= 0x1fff || r >= 0x200c && r <= 0x200d || r >= 0x2070 && r <= 0x218f || r >= 0x2c00 && r <= 0x2fef || r >= 0x3001 && r <= 0xd7ff || r >= 0xf900 && r <= 0xfdcf || r >= 0xfdf0 && r <= 0xfffd || r >= 0x10000 && r <= 0xeffff
		if !start && (i == 0 || !(r == '-' || r == '.' || r >= '0' && r <= '9' || r == 0xb7 || r >= 0x300 && r <= 0x36f || r >= 0x203f && r <= 0x2040)) {
			return false
		}
	}
	return true
}
