package dav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

func moveResource(w http.ResponseWriter, r *http.Request, service *files.Service, source string) {
	// routeStatus has already confined and decoded the absolute Destination.
	destinations := r.Header.Values("Destination")
	if len(destinations) != 1 {
		http.Error(w, "Bad Request", 400)
		return
	}
	u, err := url.Parse(destinations[0])
	if err != nil {
		http.Error(w, "Bad Request", 400)
		return
	}
	destination := strings.TrimPrefix(strings.TrimPrefix(u.Path, "/dav"), "/")
	options := files.MoveOptions{Overwrite: true}
	values := r.Header.Values("Overwrite")
	if len(values) > 1 || len(values) == 1 && !strings.EqualFold(values[0], "T") && !strings.EqualFold(values[0], "F") {
		http.Error(w, "Bad Request", 400)
		return
	}
	if len(values) == 1 {
		options.Overwrite = strings.EqualFold(values[0], "T")
	}
	values = r.Header.Values("Depth")
	if len(values) > 1 || len(values) == 1 && values[0] != "0" && !strings.EqualFold(values[0], "infinity") {
		http.Error(w, "Bad Request", 400)
		return
	}
	options.ZeroDepth = len(values) == 1 && values[0] == "0"
	var probe [1]byte
	n, err := io.ReadFull(r.Body, probe[:])
	if n != 0 {
		http.Error(w, "Unsupported Media Type", 415)
		return
	}
	if err != io.EOF {
		http.Error(w, "Bad Request", 400)
		return
	}
	created, err := service.Move(r.Context(), source, destination, options, publicationExpectation(r))
	if err != nil {
		if r.Context().Err() == nil && moveDescendantError(w, err) {
			return
		}
		if errors.Is(err, files.ErrMoveForbidden) {
			http.Error(w, "Forbidden", 403)
			return
		}
		namespaceError(w, r, err)
		return
	}
	if created {
		w.WriteHeader(201)
	} else {
		w.WriteHeader(204)
	}
}

// MOVE requires a Multi-Status for a failing child even though this store's
// stronger atomic policy rolls back the entire request, not just that subtree.
func moveDescendantError(w http.ResponseWriter, err error) bool {
	path, status, condition := "", 0, ""
	collection := false
	var locked *files.TreeLockFailure
	var failure *files.MoveFailure
	if errors.As(err, &failure) {
		path, collection = failure.Path, failure.Collection
	} else if errors.As(err, &locked) {
		path = locked.Path
	} else {
		return false
	}
	switch {
	case errors.Is(err, files.ErrLocked):
		status, condition = 423, "lock-token-submitted"
	case errors.Is(err, files.ErrLockOwner):
		status = 403
	case errors.Is(err, files.ErrInvalid):
		status = 400
	}
	if status == 0 {
		return false
	}
	var body bytes.Buffer
	body.WriteString(`<d:multistatus xmlns:d="DAV:"><d:response><d:href>`)
	if collection {
		path += "/"
	}
	xml.EscapeText(&body, []byte((&url.URL{Path: "/dav/" + path}).EscapedPath()))
	body.WriteString(`</d:href><d:status>HTTP/1.1 `)
	body.WriteString(strconv.Itoa(status) + " " + http.StatusText(status))
	body.WriteString(`</d:status>`)
	if condition != "" {
		body.WriteString(`<d:error><d:` + condition + `/></d:error>`)
	}
	body.WriteString(`</d:response></d:multistatus>`)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(207)
	_, _ = w.Write(body.Bytes())
	return true
}

// Match the existing source-path privacy policy. Never log client-supplied
// names, authorities, userinfo, queries or raw malformed Destination fields.
func safeMoveDestination(r *http.Request) string {
	values := r.Header.Values("Destination")
	if len(values) != 1 {
		return "<invalid>"
	}
	u, err := url.Parse(values[0])
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(values[0], "#") {
		return "<invalid>"
	}
	path, ok := checkedPath(u.EscapedPath())
	if !ok {
		return "<invalid>"
	}
	if path == "/dav" || path == "/dav/" {
		return "/dav/"
	}
	if strings.HasPrefix(path, "/dav/") {
		return "<redacted>"
	}
	return "<invalid>"
}
