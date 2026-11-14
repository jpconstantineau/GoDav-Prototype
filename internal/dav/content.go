package dav

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

func putFile(w http.ResponseWriter, r *http.Request, service *files.Service, path string, maxBytes int64) {
	if _, present := r.Header["Content-Range"]; present {
		http.Error(w, "Bad Request", 400)
		return
	}
	if values := r.Header.Values("Content-Encoding"); len(values) > 0 && (len(values) != 1 || !strings.EqualFold(strings.TrimSpace(values[0]), "identity")) {
		http.Error(w, "Unsupported Media Type", 415)
		return
	}
	contentType := "application/octet-stream"
	if values := r.Header.Values("Content-Type"); len(values) > 0 {
		if len(values) != 1 || len(values[0]) > 255 {
			http.Error(w, "Bad Request", 400)
			return
		}
		if !files.ValidContentType(values[0]) {
			http.Error(w, "Bad Request", 400)
			return
		}
		contentType = values[0]
	}
	node, created, err := service.Put(r.Context(), path, r.Body, r.ContentLength, contentType, maxBytes, publicationExpectation(r))
	if err != nil {
		namespaceError(w, r, err)
		return
	}
	w.Header().Set("ETag", node.ETag)
	if created {
		w.WriteHeader(201)
	} else {
		w.WriteHeader(204)
	}
}

func readFile(w http.ResponseWriter, r *http.Request, service *files.Service, path string) {
	if path != "" && strings.HasSuffix(path, "/") {
		snapshot, err := service.Snapshot(r.Context(), path, false)
		if err != nil {
			namespaceError(w, r, err)
			return
		}
		if snapshot.Node.File {
			namespaceError(w, r, files.ErrInvalid)
		} else {
			namespaceError(w, r, files.ErrExists)
		}
		return
	}
	var snapshot files.Snapshot
	var content io.ReadSeeker
	var err error
	if r.Method == "HEAD" {
		snapshot, err = service.Snapshot(r.Context(), path, false)
		content = &metadataReader{size: snapshot.Node.Size}
	} else {
		// A conditional metadata-only result must not load content. Evaluate via
		// the same stdlib semantics using a HEAD clone. If content is needed,
		// obtain a fresh complete snapshot and re-evaluate against that version.
		if hasReadCondition(r) {
			snapshot, err = service.Snapshot(r.Context(), path, false)
			if err != nil {
				namespaceError(w, r, err)
				return
			}
			if !snapshot.Node.File {
				namespaceError(w, r, files.ErrExists)
				return
			}
			normalized, normalizeErr := normalizedReadRequest(r)
			if normalizeErr != nil {
				namespaceError(w, r, normalizeErr)
				return
			}
			r = normalized
			probe := &conditionResponse{headers: make(http.Header)}
			probe.headers.Set("Content-Type", snapshot.Node.ContentType)
			probe.headers.Set("ETag", snapshot.Node.ETag)
			clone := r.Clone(r.Context())
			clone.Method = "HEAD"
			clone.Header.Del("Range")
			clone.Header.Del("If-Range")
			http.ServeContent(probe, clone, snapshot.Node.Name, snapshot.Node.Modified, &metadataReader{size: snapshot.Node.Size})
			if probe.status == 304 || probe.status == 412 {
				for name, values := range probe.headers {
					w.Header()[name] = values
				}
				w.WriteHeader(probe.status)
				return
			}
		}
		var data []byte
		var release func()
		snapshot, data, release, err = service.Read(r.Context(), path)
		defer release()
		content = bytes.NewReader(data)
	}
	if err != nil {
		namespaceError(w, r, err)
		return
	}
	if !snapshot.Node.File {
		namespaceError(w, r, files.ErrExists)
		return
	}
	normalized, normalizeErr := normalizedReadRequest(r)
	if normalizeErr != nil {
		namespaceError(w, r, normalizeErr)
		return
	}
	r = normalized
	w.Header().Set("Content-Type", snapshot.Node.ContentType)
	w.Header().Set("ETag", snapshot.Node.ETag)
	rangeAllowed := permitsRange(r)
	if values := r.Header.Values("Range"); r.Method == "HEAD" || snapshot.Node.Size == 0 || !rangeAllowed || len(values) != 1 || !strings.HasPrefix(values[0], "bytes=") || strings.Contains(values[0], ",") {
		r = r.Clone(r.Context())
		r.Header.Del("Range")
		r.Header.Del("If-Range")
	} else if invalidSingleRange(strings.TrimPrefix(values[0], "bytes=")) {
		// Keep ServeContent's conditional-request precedence, but normalize
		// syntax it tolerates (empty ranges and zero suffixes) to its 416 path.
		r = r.Clone(r.Context())
		r.Header.Set("Range", "bytes=invalid")
	}
	http.ServeContent(w, r, snapshot.Node.Name, snapshot.Node.Modified, content)
}

func invalidSingleRange(value string) bool {
	first, last, ok := strings.Cut(value, "-")
	first, last = strings.TrimSpace(first), strings.TrimSpace(last)
	if !ok || (first == "" && strings.Trim(last, "0") == "") {
		return true
	}
	for _, part := range []string{first, last} {
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return true
			}
		}
	}
	return false
}

func hasReadCondition(r *http.Request) bool {
	for _, name := range []string{"If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		if _, ok := r.Header[name]; ok {
			return true
		}
	}
	return false
}

type conditionResponse struct {
	headers http.Header
	status  int
}

func (w *conditionResponse) Header() http.Header { return w.headers }
func (w *conditionResponse) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}
func (w *conditionResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return len(p), nil
}

// HEAD's size/seek information is metadata. Reading never queries content.
type metadataReader struct{ size int64 }

func (*metadataReader) Read([]byte) (int, error) { return 0, io.EOF }
func (m *metadataReader) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekEnd {
		return m.size + offset, nil
	}
	return offset, nil
}
