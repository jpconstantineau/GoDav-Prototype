package dav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"

	"golang.org/x/net/webdav"
)

// discovery uses the upstream DAV parser/serializer against a private synthetic
// empty root. The filesystem never escapes this handler and mutations cannot
// reach it. Persistent files begin in the next slice, not in this fixture root.
func discovery() http.Handler {
	upstream := &webdav.Handler{Prefix: "/dav/", FileSystem: webdav.NewMemFS(), LockSystem: webdav.NewMemLS()}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "OPTIONS, PROPFIND")
		if r.URL.Path != "/dav" && r.URL.Path != "/dav/" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case "OPTIONS":
			w.WriteHeader(http.StatusOK)
			return
		case "PROPFIND":
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		depths := r.Header.Values("Depth")
		if len(depths) > 1 {
			http.Error(w, "Bad Request", 400)
			return
		}
		if len(depths) == 0 || depths[0] == "infinity" {
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(403)
			_, _ = io.WriteString(w, `<d:error xmlns:d="DAV:"><d:propfind-finite-depth/></d:error>`)
			return
		}
		if depths[0] != "0" && depths[0] != "1" {
			http.Error(w, "Bad Request", 400)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "Content Too Large", 413)
			} else {
				http.Error(w, "Bad Request", 400)
			}
			return
		}
		if len(body) > 0 && !onePropertyDocument(body) {
			http.Error(w, "Bad Request", 400)
			return
		}
		clone := r.Clone(r.Context())
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.URL.Path = "/dav/"
		clone.URL.RawPath = ""
		response := &discoveryResponse{headers: make(http.Header)}
		upstream.ServeHTTP(response, clone)
		if response.failed {
			http.Error(w, "Response too large", http.StatusInternalServerError)
			return
		}
		if response.status != http.StatusMultiStatus {
			http.Error(w, http.StatusText(response.status), response.status)
			return
		}
		payload, err := withoutWriteLocks(response.body.Bytes())
		if err != nil {
			http.Error(w, "Discovery failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write(payload)
	})
}

type discoveryResponse struct {
	headers http.Header
	body    bytes.Buffer
	status  int
	failed  bool
}

func (w *discoveryResponse) Header() http.Header { return w.headers }
func (w *discoveryResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *discoveryResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.failed || w.body.Len()+len(p) > 1<<20 {
		w.failed = true
		return 0, errors.New("discovery response too large")
	}
	return w.body.Write(p)
}

// Upstream always advertises exclusive write locks. This root-only slice has no
// locking, so the supportedlock value must be empty, including allprop and explicit
// requests. Keep upstream XML status/property behavior; alter only this value.
func withoutWriteLocks(input []byte) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(input))
	var output bytes.Buffer
	copied := int64(0)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name != (xml.Name{Space: "DAV:", Local: "supportedlock"}) {
			continue
		}
		contentStart := decoder.InputOffset()
		depth := 1
		var endStart int64
		for depth > 0 {
			endStart = decoder.InputOffset()
			token, err = decoder.Token()
			if err != nil {
				return nil, err
			}
			switch token.(type) {
			case xml.StartElement:
				depth++
			case xml.EndElement:
				depth--
			}
		}
		// Preserve upstream namespace spelling and attributes exactly. Re-encoding
		// expanded tokens can duplicate xmlns attributes and expand the response.
		// Only remove bytes: the result cannot exceed the bounded upstream buffer.
		output.Write(input[copied:contentStart])
		output.Write(input[endStart:decoder.InputOffset()])
		copied = decoder.InputOffset()
	}
	output.Write(input[copied:])
	return output.Bytes(), nil
}

// Bound XML nesting/property counts and reject a second document or trailing
// material before upstream's single Decode can otherwise ignore that suffix.
func onePropertyDocument(body []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	depth, roots, properties := 0, 0, 0
	selectors := map[xml.Name]bool{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return roots == 1 && depth == 0
		}
		if err != nil {
			return false
		}
		switch t := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || t.Name != (xml.Name{Space: "DAV:", Local: "propfind"}) {
					return false
				}
			}
			if depth == 1 && t.Name.Space == "DAV:" {
				switch t.Name.Local {
				case "allprop", "propname", "prop", "include":
					if selectors[t.Name] {
						return false
					}
					selectors[t.Name] = true
				}
			}
			depth++
			properties++
			if depth > 32 || properties > 256 {
				return false
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(t)) != "" {
				return false
			}
		}
	}
}
