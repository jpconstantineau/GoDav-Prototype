package dav

import (
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type authority struct {
	host string
	port int
}

func parseAuthority(raw string) (authority, bool) {
	if raw == "" || strings.ContainsAny(raw, "/\\?#%@") {
		return authority{}, false
	}
	for _, r := range raw {
		if r <= 32 || r >= 127 {
			return authority{}, false
		}
	}
	u, err := url.Parse("https://" + raw)
	if err != nil || u.Host != raw || u.User != nil || u.Hostname() == "" {
		return authority{}, false
	}
	host := strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" {
			return authority{}, false
		}
		host = ip.String()
	}
	port := 443
	if text := u.Port(); text != "" {
		for _, c := range text {
			if c < '0' || c > '9' {
				return authority{}, false
			}
		}
		port, err = strconv.Atoi(text)
		if err != nil || port < 1 || port > 65535 {
			return authority{}, false
		}
	} else if strings.HasSuffix(raw, ":") {
		return authority{}, false
	}
	return authority{host, port}, true
}

// checkedPath rejects ambiguity before ServeMux can normalize or redirect it.
// A percent is decoded once only. Encoded ordinary child names remain ordinary
// names; they are never fed through a second unescape operation.
func checkedPath(raw string) (string, bool) {
	if !strings.HasPrefix(raw, "/") || strings.Contains(raw, "//") || strings.ContainsAny(raw, "\\?#") {
		return "", false
	}
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
		return "", false
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil || !utf8.ValidString(decoded) {
		return "", false
	}
	for _, c := range decoded {
		if c < 32 || c == 127 || c == '\\' {
			return "", false
		}
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." {
			return "", false
		}
	}
	if (decoded == "/dav" || strings.HasPrefix(decoded, "/dav/")) && !(raw == "/dav" || strings.HasPrefix(raw, "/dav/")) {
		return "", false
	}
	return decoded, true
}

func routeStatus(r *http.Request, expected authority) int {
	actual, ok := parseAuthority(r.Host)
	if !ok || actual != expected || r.TLS == nil {
		return 400
	}
	// Reject absolute-form, authority-form and asterisk-form requests rather
	// than acting as a proxy or permitting server-global discovery.
	raw, _, _ := strings.Cut(r.RequestURI, "?")
	if r.URL.IsAbs() || r.URL.Host != "" || !strings.HasPrefix(raw, "/") {
		return 400
	}
	path, ok := checkedPath(raw)
	if !ok || path != r.URL.Path {
		return 400
	}
	if path != "/dav" && !strings.HasPrefix(path, "/dav/") {
		return 404
	}
	if origins := r.Header.Values("Origin"); len(origins) > 0 {
		if len(origins) != 1 {
			return 400
		}
		origin, err := url.Parse(origins[0])
		if err != nil || origin.Scheme != "https" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || strings.Contains(origins[0], "#") {
			return 400
		}
		a, ok := parseAuthority(origin.Host)
		if !ok || a != expected {
			return 400
		}
	}
	if destinations := r.Header.Values("Destination"); len(destinations) > 0 {
		if len(destinations) != 1 {
			return 400
		}
		raw := destinations[0]
		destination, err := url.Parse(raw)
		if err != nil || destination.Opaque != "" || destination.Scheme == "" || destination.Host == "" || destination.User != nil || destination.RawQuery != "" || destination.ForceQuery || destination.Fragment != "" || strings.Contains(raw, "#") {
			return 400
		}
		a, ok := parseAuthority(destination.Host)
		if !ok {
			return 400
		}
		if destination.Scheme != "https" || a != expected {
			return 502
		}
		path, ok := checkedPath(destination.EscapedPath())
		if !ok {
			return 400
		}
		if path != "/dav" && !strings.HasPrefix(path, "/dav/") {
			return 403
		}
	}
	return 0
}

func safeMethod(method string) string {
	switch method {
	case "OPTIONS", "PROPFIND", "GET", "HEAD", "PUT", "POST", "DELETE", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK", "PROPPATCH", "CONNECT", "TRACE":
		return method
	}
	return "OTHER"
}
func safePath(r *http.Request) string {
	raw, _, _ := strings.Cut(r.RequestURI, "?")
	if raw == "/dav" || raw == "/dav/" {
		return "/dav/"
	}
	// Until resource names exist, no arbitrary client-supplied name is useful.
	return "<redacted>"
}
