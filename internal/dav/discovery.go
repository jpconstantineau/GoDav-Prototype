package dav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"golang.org/x/net/webdav"
)

// discovery reuses upstream PROPFIND over a read-only committed snapshot.
func discovery(collections *files.Service, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "OPTIONS, PROPFIND, PROPPATCH, MKCOL, GET, HEAD, PUT, LOCK, UNLOCK, MOVE")
		path := strings.TrimPrefix(r.URL.Path, "/dav")
		path = strings.TrimPrefix(path, "/")
		switch r.Method {
		case "MOVE":
			moveResource(w, r, collections, path)
			return
		case "LOCK":
			lockResource(w, r, collections, path)
			return
		case "UNLOCK":
			unlockResource(w, r, collections, path)
			return
		case "PROPPATCH":
			patchProperties(w, r, collections, path)
			return
		case "PUT":
			putFile(w, r, collections, path, maxBytes)
			return
		case "GET", "HEAD":
			readFile(w, r, collections, path)
			return
		case "MKCOL":
			makeCollection(w, r, collections, path)
			return
		case "OPTIONS":
			snapshot, err := collections.Snapshot(r.Context(), path, false, publicationExpectation(r))
			if err != nil {
				namespaceError(w, r, err)
				return
			}
			if err := publicationExpectation(r).CheckWrite(snapshot.Node, true); err != nil {
				namespaceError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		case "PROPFIND":
		default:
			if _, err := collections.Snapshot(r.Context(), path, false); err != nil {
				namespaceError(w, r, err)
				return
			}
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
		snapshot, err := collections.Snapshot(r.Context(), path, depths[0] == "1", publicationExpectation(r))
		if err != nil {
			namespaceError(w, r, err)
			return
		}
		if err := publicationExpectation(r).CheckWrite(snapshot.Node, true); err != nil {
			namespaceError(w, r, err)
			return
		}
		upstream := &webdav.Handler{Prefix: "/dav/", FileSystem: snapshotFS{snapshot}, LockSystem: webdav.NewMemLS()}
		clone := r.Clone(r.Context())
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.URL.Path = "/dav/" + snapshot.Path
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
		payload := response.body.Bytes()
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
