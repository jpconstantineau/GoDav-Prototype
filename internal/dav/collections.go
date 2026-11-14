package dav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"golang.org/x/net/webdav"
)

func namespaceError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() == nil {
		var conflict *files.LockConflict
		if errors.As(err, &conflict) && conflict.Descendant {
			var b bytes.Buffer
			b.WriteString(`<d:multistatus xmlns:d="DAV:"><d:response><d:href>`)
			xml.EscapeText(&b, []byte((&url.URL{Path: "/dav/" + conflict.Path}).EscapedPath()))
			b.WriteString(`</d:href><d:status>HTTP/1.1 423 Locked</d:status><d:error><d:no-conflicting-lock/></d:error></d:response><d:response><d:href>`)
			xml.EscapeText(&b, []byte(r.URL.EscapedPath()))
			b.WriteString(`</d:href><d:status>HTTP/1.1 424 Failed Dependency</d:status></d:response></d:multistatus>`)
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(207)
			_, _ = w.Write(b.Bytes())
			return
		}
		name, status := "", 0
		switch {
		case errors.Is(err, files.ErrLocked):
			name, status = "lock-token-submitted", 423
		case errors.Is(err, files.ErrLockConflict):
			name, status = "no-conflicting-lock", 423
		case errors.Is(err, files.ErrLockScope):
			name, status = "lock-token-matches-request-uri", 412
		case errors.Is(err, files.ErrUnlockScope):
			name, status = "lock-token-matches-request-uri", 409
		}
		if status != 0 {
			path := r.URL.EscapedPath()
			var required *files.LockRequired
			if errors.As(err, &required) {
				path = (&url.URL{Path: "/dav/" + required.Path}).EscapedPath()
			}
			var b bytes.Buffer
			b.WriteString(`<d:error xmlns:d="DAV:"><d:` + name + `>`)
			if status == 423 {
				b.WriteString(`<d:href>`)
				xml.EscapeText(&b, []byte(path))
				b.WriteString(`</d:href>`)
			}
			b.WriteString(`</d:` + name + `></d:error>`)
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(status)
			_, _ = w.Write(b.Bytes())
			return
		}
	}
	status := http.StatusInternalServerError
	switch {
	case r.Context().Err() != nil:
		status = 503
	case errors.Is(err, files.ErrLocked), errors.Is(err, files.ErrLockConflict):
		status = 423
	case errors.Is(err, files.ErrLockOwner):
		status = 403
	case errors.Is(err, files.ErrLockScope):
		status = 412
	case errors.Is(err, files.ErrUnlockScope):
		status = 409
	case errors.Is(err, files.ErrPrecondition):
		status = 412
	case errors.Is(err, files.ErrConditionSyntax):
		status = 400
	case errors.Is(err, files.ErrConditionLimit):
		status = 431
	case errors.Is(err, files.ErrInvalid):
		status = 400
	case errors.Is(err, files.ErrMissing):
		status = 404
	case errors.Is(err, files.ErrParentMissing):
		status = 409
	case errors.Is(err, files.ErrExists):
		status = 405
	case errors.Is(err, files.ErrCapacity):
		status = 507
	case errors.Is(err, files.ErrTooLarge):
		status = 413
	case errors.Is(err, files.ErrInput):
		status = 400
	case errors.Is(err, files.ErrBusy):
		status = 503
	case errors.Is(err, files.ErrTimeout):
		status = 408
	}
	http.Error(w, http.StatusText(status), status)
}

func makeCollection(w http.ResponseWriter, r *http.Request, service *files.Service, path string) {
	// Check actual bytes, including unknown-length/chunked bodies. Never publish
	// before confirming EOF; ContentLength alone is not a body-presence check.
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
	if err := service.CreateCollection(r.Context(), path, publicationExpectation(r)); err != nil {
		namespaceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// snapshotFS is a read-only transport adapter over one committed, bounded
// listing. It cannot open a database, create nodes, traverse the host or mutate.
type snapshotFS struct{ snapshot files.Snapshot }

func (s snapshotFS) entry(path string) (files.Node, bool, bool) {
	path = strings.Trim(path, "/")
	if path == s.snapshot.Path {
		return s.snapshot.Node, true, true
	}
	prefix := s.snapshot.Path
	if prefix != "" {
		prefix += "/"
	}
	for _, child := range s.snapshot.Children {
		if path == prefix+child.Name {
			return child, false, true
		}
	}
	return files.Node{}, false, false
}
func (s snapshotFS) Stat(_ context.Context, path string) (os.FileInfo, error) {
	node, _, ok := s.entry(path)
	if !ok {
		return nil, os.ErrNotExist
	}
	return nodeInfo{node}, nil
}
func (s snapshotFS) OpenFile(_ context.Context, path string, flag int, _ os.FileMode) (webdav.File, error) {
	if flag != os.O_RDONLY {
		return nil, os.ErrPermission
	}
	node, root, ok := s.entry(path)
	if !ok {
		return nil, os.ErrNotExist
	}
	var children []os.FileInfo
	if root {
		for _, child := range s.snapshot.Children {
			children = append(children, nodeInfo{child})
		}
	}
	return &collectionFile{info: nodeInfo{node}, children: children}, nil
}
func (snapshotFS) Mkdir(context.Context, string, os.FileMode) error { return os.ErrPermission }
func (snapshotFS) RemoveAll(context.Context, string) error          { return os.ErrPermission }
func (snapshotFS) Rename(context.Context, string, string) error     { return os.ErrPermission }

type nodeInfo struct{ node files.Node }

func (i nodeInfo) Name() string { return i.node.Name }
func (i nodeInfo) Size() int64  { return i.node.Size }
func (i nodeInfo) Mode() os.FileMode {
	if i.node.File {
		return 0444
	}
	return os.ModeDir | 0555
}
func (i nodeInfo) ModTime() time.Time                          { return i.node.Modified }
func (i nodeInfo) IsDir() bool                                 { return !i.node.File }
func (nodeInfo) Sys() any                                      { return nil }
func (i nodeInfo) ContentType(context.Context) (string, error) { return i.node.ContentType, nil }
func (i nodeInfo) ETag(context.Context) (string, error)        { return i.node.ETag, nil }

type collectionFile struct {
	info     nodeInfo
	children []os.FileInfo
	position int
}

func (f *collectionFile) Stat() (os.FileInfo, error)   { return f.info, nil }
func (*collectionFile) Read([]byte) (int, error)       { return 0, io.EOF }
func (*collectionFile) Write([]byte) (int, error)      { return 0, os.ErrPermission }
func (*collectionFile) Seek(int64, int) (int64, error) { return 0, os.ErrInvalid }
func (*collectionFile) Close() error                   { return nil }
func (f *collectionFile) Readdir(count int) ([]os.FileInfo, error) {
	remaining := f.children[f.position:]
	if count > 0 && len(remaining) == 0 {
		return nil, io.EOF
	}
	if count > 0 && count < len(remaining) {
		remaining = remaining[:count]
	}
	f.position += len(remaining)
	return remaining, nil
}
