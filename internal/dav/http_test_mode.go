package dav

import (
	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"net"
	"net/http"
	"net/netip"
	"net/url"
)

// HTTPTestHandler is only for a newly created disposable store. It never
// authenticates a real account or accepts credentials over cleartext HTTP.
func HTTPTestHandler(collections *files.Service, origin string, maxBytes int64) http.Handler {
	u, err := url.Parse(origin)
	expected, valid := authority{}, false
	if err == nil {
		expected, valid = parseAuthorityScheme(u.Host, "http")
	}
	if err != nil || !valid || u.Scheme != "http" || expected.host != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "Invalid test origin", 400) })
	}
	mux := http.NewServeMux()
	root := discovery(collections, maxBytes)
	mux.Handle("/dav", root)
	mux.Handle("/dav/", root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip, ipErr := netip.ParseAddr(host)
		if err != nil || ipErr != nil || !ip.Is4() || !ip.IsLoopback() {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if code := routeStatusTransport(r, expected, "http"); code != 0 {
			http.Error(w, http.StatusText(code), code)
			return
		}
		// Do not encourage credential use or accept ambient browser credentials.
		if len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Proxy-Authorization")) != 0 || len(r.Header.Values("Cookie")) != 0 {
			http.Error(w, "Credentials are not accepted in test mode", http.StatusBadRequest)
			return
		}
		// Browsers disclose cross-site requests even where Origin is absent.
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), "http-test")))
	})
}
