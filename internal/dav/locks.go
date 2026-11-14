package dav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func lockTimeout(values []string) (time.Duration, error) {
	if len(values) == 0 {
		return 600 * time.Second, nil
	}
	size := len(values) - 1
	for _, value := range values {
		size += len(value)
	}
	if size > 8192 {
		return 0, files.ErrConditionSyntax
	}
	var chosen time.Duration
	for _, part := range strings.Split(strings.Join(values, ","), ",") {
		part = strings.Trim(part, " \t")
		var seconds uint64
		if part == "Infinite" {
			seconds = 3600
		} else {
			if !strings.HasPrefix(part, "Second-") {
				return 0, files.ErrConditionSyntax
			}
			digits := strings.TrimPrefix(part, "Second-")
			if digits == "" || len(digits) > 10 {
				return 0, files.ErrConditionSyntax
			}
			for _, c := range digits {
				if c < '0' || c > '9' {
					return 0, files.ErrConditionSyntax
				}
			}
			n, err := strconv.ParseUint(digits, 10, 32)
			if err != nil {
				return 0, files.ErrConditionSyntax
			}
			seconds = n
			if seconds == 0 {
				seconds = 1
			}
		}
		if seconds > 3600 {
			seconds = 3600
		}
		if chosen == 0 {
			chosen = time.Duration(seconds) * time.Second
		}
	}
	return chosen, nil
}

// Keep owner markup lexical, but copy all inherited namespace bindings onto its
// root. QName-valued text and attributes then retain their original meaning.
func parseLockInfo(body []byte) (string, int) {
	raw := xml.NewDecoder(bytes.NewReader(body))
	ns := &namespaceTokens{decoder: raw}
	d := xml.NewTokenDecoder(ns)
	var stack []xml.Name
	roots, elements := 0, 0
	seen := map[string]bool{}
	choice := map[string]bool{}
	ownerStart := -1
	ownerXML := ""
	ownerBindings := ""
	decl := false
	skipDepth := 0
	for {
		offset := int(raw.InputOffset())
		token, err := d.Token()
		if err == io.EOF {
			if roots != 1 || len(stack) != 0 || !seen["lockscope"] || !seen["locktype"] || !choice["lockscope"] || !choice["locktype"] {
				return "", 400
			}
			return ownerXML, 0
		}
		if err != nil {
			return "", 400
		}
		switch t := token.(type) {
		case xml.Directive:
			return "", 400
		case xml.ProcInst:
			if t.Target != "xml" || roots != 0 || decl || !bytes.HasPrefix(body, []byte("<?xml")) || !xmlDeclaration.Match(t.Inst) {
				return "", 400
			}
			decl = true
		case xml.StartElement:
			depth := len(stack)
			elements++
			if depth >= 32 || elements > 256 || len(t.Name.Space) > 1024 || len(t.Name.Local) > 255 {
				return "", 413
			}
			attrs := map[xml.Name]bool{}
			for _, a := range t.Attr {
				if attrs[a.Name] {
					return "", 400
				}
				attrs[a.Name] = true
			}
			if skipDepth > 0 {
				stack = append(stack, t.Name)
				continue
			}
			if depth == 0 {
				roots++
				if roots != 1 || t.Name != (xml.Name{Space: "DAV:", Local: "lockinfo"}) {
					return "", 400
				}
			}
			if depth == 1 {
				if t.Name.Space != "DAV:" || (t.Name.Local != "lockscope" && t.Name.Local != "locktype" && t.Name.Local != "owner") {
					skipDepth = depth + 1
					stack = append(stack, t.Name)
					continue
				}
				if seen[t.Name.Local] {
					return "", 400
				}
				seen[t.Name.Local] = true
				if t.Name.Local == "owner" {
					ownerStart = offset
					declared := map[string]bool{}
					for _, a := range t.Attr {
						if a.Name.Space == "xmlns" {
							declared[a.Name.Local] = true
						} else if a.Name.Space == "" && a.Name.Local == "xmlns" {
							declared[""] = true
						}
					}
					scope := ns.scopes[len(ns.scopes)-1]
					keys := make([]string, 0, len(scope))
					for k := range scope {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					var b bytes.Buffer
					for _, k := range keys {
						if k == "xml" || declared[k] {
							continue
						}
						b.WriteString(" xmlns")
						if k != "" {
							b.WriteByte(':')
							b.WriteString(k)
						}
						b.WriteString(`="`)
						xml.EscapeText(&b, []byte(scope[k]))
						b.WriteByte('"')
					}
					ownerBindings = b.String()
				}
			}
			if depth >= 2 && stack[1].Local != "owner" {
				parent := stack[1].Local
				expected := "write"
				if parent == "lockscope" {
					expected = "exclusive"
				}
				if depth != 2 || t.Name.Space != "DAV:" || t.Name.Local != expected {
					if depth == 2 && parent == "lockscope" && t.Name == (xml.Name{Space: "DAV:", Local: "shared"}) {
						return "", 400
					}
					skipDepth = depth + 1
					stack = append(stack, t.Name)
					continue
				}
				if choice[parent] {
					return "", 400
				}
				choice[parent] = true
			}
			stack = append(stack, t.Name)
		case xml.EndElement:
			if skipDepth > 0 {
				depth := len(stack)
				stack = stack[:depth-1]
				if depth == skipDepth {
					skipDepth = 0
				}
				continue
			}
			if len(stack) == 2 && stack[1].Local == "owner" {
				original := string(body[ownerStart:int(raw.InputOffset())])
				i := 1
				for i < len(original) && !strings.ContainsRune(" \t\r\n/>", rune(original[i])) {
					i++
				}
				ownerXML = original[:i] + ownerBindings + original[i:]
				if len(ownerXML) > 16384 {
					return "", 413
				}
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if skipDepth > 0 {
				continue
			}
			if !(len(stack) >= 2 && stack[1].Local == "owner") && strings.TrimSpace(string(t)) != "" {
				return "", 400
			}
		}
	}
}

func lockResource(w http.ResponseWriter, r *http.Request, service *files.Service, path string) {
	expectation := publicationExpectation(r)
	if expectation.Invalid != nil {
		namespaceError(w, r, expectation.Invalid)
		return
	}
	duration, err := lockTimeout(r.Header.Values("Timeout"))
	if err != nil {
		namespaceError(w, r, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
	if err != nil {
		status := 400
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			status = 413
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	var lease files.Lease
	created := false
	refresh := len(body) == 0
	if refresh {
		token := ""
		if len(expectation.DAV.Lists) != 1 {
			http.Error(w, "Bad Request", 400)
			return
		}
		list := expectation.DAV.Lists[0]
		if len(list.Conditions) != 1 {
			http.Error(w, "Bad Request", 400)
			return
		}
		c := list.Conditions[0]
		if c.Not || c.Token == "" {
			http.Error(w, "Bad Request", 400)
			return
		}
		token = c.Token
		lease, err = service.RefreshLock(r.Context(), path, token, duration, expectation)
	} else {
		owner, status := parseLockInfo(body)
		if status != 0 {
			http.Error(w, http.StatusText(status), status)
			return
		}
		depth := r.Header.Values("Depth")
		zero := false
		if len(depth) > 1 || len(depth) == 1 && depth[0] != "0" && depth[0] != "infinity" {
			http.Error(w, "Bad Request", 400)
			return
		}
		if len(depth) == 1 {
			zero = depth[0] == "0"
		}
		request := files.LockRequest{OwnerXML: owner, ZeroDepth: zero, Duration: duration}
		// Validate serialization before publication. Stored leases contain only this
		// validated owner and path plus a bounded server-generated ASCII token.
		_, err = encodeLeaseResponse(files.Lease{Token: strings.Repeat("x", 256), Path: path, OwnerXML: owner, ZeroDepth: zero, Duration: duration})
		if err != nil {
			http.Error(w, "Lock response failed", 500)
			return
		}
		lease, created, err = service.Lock(r.Context(), path, request, expectation)
	}
	if err != nil {
		namespaceError(w, r, err)
		return
	}
	payload, err := encodeLeaseResponse(lease)
	if err != nil {
		http.Error(w, "Lock response failed", 500)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	if !refresh {
		w.Header().Set("Lock-Token", "<"+lease.Token+">")
	}
	status := 200
	if created {
		status = 201
	}
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
func unlockResource(w http.ResponseWriter, r *http.Request, service *files.Service, path string) {
	values := r.Header.Values("Lock-Token")
	size := len(values) - 1
	for _, value := range values {
		size += len(value)
	}
	if size > 8192 {
		http.Error(w, "Bad Request", 400)
		return
	}
	p := ifParser{s: strings.Trim(values[0], " \t")}
	token, ok := p.angle()
	if !ok || p.i != len(p.s) || !validStateToken(token) {
		http.Error(w, "Bad Request", 400)
		return
	}
	// UNLOCK has no request entity, including chunked bodies.
	var probe [1]byte
	n, err := io.ReadFull(r.Body, probe[:])
	if n != 0 || err != io.EOF {
		http.Error(w, "Bad Request", 400)
		return
	}
	if err := service.Unlock(r.Context(), path, token, publicationExpectation(r)); err != nil {
		namespaceError(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func encodeLockDiscovery(leases []files.Lease) ([]byte, error) {
	var b bytes.Buffer
	text := func(name, value string) {
		b.WriteString("<D:" + name + ">")
		xml.EscapeText(&b, []byte(value))
		b.WriteString("</D:" + name + ">")
	}
	for _, lease := range leases {
		b.WriteString(`<D:activelock xmlns:D="DAV:"><D:locktype><D:write/></D:locktype><D:lockscope><D:exclusive/></D:lockscope>`)
		depth := "infinity"
		if lease.ZeroDepth {
			depth = "0"
		}
		text("depth", depth)
		b.WriteString(lease.OwnerXML)
		remaining := lease.Duration
		if !lease.Expires.IsZero() {
			remaining = time.Until(lease.Expires)
		}
		seconds := int64((remaining + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		text("timeout", "Second-"+strconv.FormatInt(seconds, 10))
		b.WriteString("<D:locktoken>")
		text("href", lease.Token)
		b.WriteString("</D:locktoken><D:lockroot>")
		root := "/dav/" + strings.Trim(lease.Path, "/")
		text("href", (&url.URL{Path: root}).EscapedPath())
		b.WriteString("</D:lockroot></D:activelock>")
		if b.Len() > 1<<20 {
			return nil, errors.New("lock response exceeds bounds")
		}
	}
	return b.Bytes(), nil
}
func encodeLeaseResponse(lease files.Lease) ([]byte, error) {
	discovery, err := encodeLockDiscovery([]files.Lease{lease})
	if err != nil {
		return nil, err
	}
	payload := append([]byte(`<D:prop xmlns:D="DAV:"><D:lockdiscovery>`), discovery...)
	payload = append(payload, []byte(`</D:lockdiscovery></D:prop>`)...)
	if len(payload) > 1<<20 {
		return nil, errors.New("lock response exceeds bounds")
	}
	return payload, nil
}
