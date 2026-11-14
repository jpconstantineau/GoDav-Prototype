package dav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"golang.org/x/net/webdav"
)

const windowsNamespace = "urn:schemas-microsoft-com:"

// encoding/xml accepts declaration-like processing instructions with no version.
// Restrict the one allowed declaration to XML 1.0's complete UTF-8 grammar.
var xmlDeclaration = regexp.MustCompile(`^[ \t\r\n]*version[ \t\r\n]*=[ \t\r\n]*("1\.0"|'1\.0')([ \t\r\n]+encoding[ \t\r\n]*=[ \t\r\n]*("(?i:utf-8)"|'(?i:utf-8)'))?([ \t\r\n]+standalone[ \t\r\n]*=[ \t\r\n]*("(yes|no)"|'(yes|no)'))?[ \t\r\n]*$`)

// Snapshot values are detached from persistence; generic upstream writes stay disabled.
func (f *collectionFile) DeadProps() (map[xml.Name]webdav.Property, error) {
	name := xml.Name{Space: "DAV:", Local: "creationdate"}
	props := map[xml.Name]webdav.Property{name: {XMLName: name, InnerXML: []byte(f.info.node.Created.UTC().Format(time.RFC3339))}}
	for key, value := range f.info.node.Metadata {
		var b bytes.Buffer
		xml.EscapeText(&b, []byte(value))
		name := xml.Name{Space: windowsNamespace, Local: key}
		props[name] = webdav.Property{XMLName: name, InnerXML: b.Bytes()}
	}
	return props, nil
}
func (*collectionFile) Patch([]webdav.Proppatch) ([]webdav.Propstat, error) {
	return nil, os.ErrPermission
}

type propertyChange struct {
	name            xml.Name
	value           string
	remove, invalid bool
	status          int
	protected       bool
}

func protectedProperty(name xml.Name) bool {
	if name.Space != "DAV:" {
		return false
	}
	switch name.Local {
	case "creationdate", "displayname", "resourcetype", "getlastmodified", "getcontentlength", "getcontenttype", "getetag", "getcontentlanguage", "supportedlock", "lockdiscovery":
		return true
	}
	return false
}

// Decode one complete bounded document before any operation gate is acquired.
func parsePropertyPatch(body []byte) ([]propertyChange, int) {
	d := xml.NewDecoder(bytes.NewReader(body))
	var stack []xml.Name
	var changes []propertyChange
	elements, roots, aggregate := 0, 0, 0
	remove := false
	instructionCount, propCount := 0, 0
	skipDepth := 0
	seenDeclaration := false
	for {
		token, err := d.Token()
		if err == io.EOF {
			if roots != 1 || len(stack) != 0 || len(changes) == 0 {
				return nil, 400
			}
			return changes, 0
		}
		if err != nil {
			return nil, 400
		}
		switch t := token.(type) {
		case xml.Directive:
			return nil, 400
		case xml.ProcInst:
			if t.Target != "xml" || roots != 0 || seenDeclaration || !bytes.HasPrefix(body, []byte("<?xml")) || !xmlDeclaration.Match(t.Inst) {
				return nil, 400
			}
			seenDeclaration = true
		case xml.StartElement:
			attributes := map[xml.Name]bool{}
			for _, a := range t.Attr {
				if attributes[a.Name] {
					return nil, 400
				}
				attributes[a.Name] = true
			}
			depth := len(stack)
			elements++
			if depth >= 32 || elements > 256 || len(t.Name.Space) > 1024 || len(t.Name.Local) > 255 {
				return nil, 413
			}
			if skipDepth > 0 {
				stack = append(stack, t.Name)
				continue
			}
			switch depth {
			case 0:
				roots++
				if roots != 1 || t.Name != (xml.Name{Space: "DAV:", Local: "propertyupdate"}) {
					return nil, 400
				}
			case 1:
				if t.Name.Space != "DAV:" || (t.Name.Local != "set" && t.Name.Local != "remove") {
					skipDepth = depth + 1
					stack = append(stack, t.Name)
					continue
				}
				remove = t.Name.Local == "remove"
				instructionCount = 0
			case 2:
				if t.Name != (xml.Name{Space: "DAV:", Local: "prop"}) {
					skipDepth = depth + 1
					stack = append(stack, t.Name)
					continue
				}
				if instructionCount != 0 {
					return nil, 400
				}
				instructionCount++
				propCount = 0
			case 3:
				if len(changes) >= files.MaxMetadataChanges {
					return nil, 413
				}
				propCount++
				changes = append(changes, propertyChange{name: t.Name, remove: remove})
			default:
				changes[len(changes)-1].invalid = true
			}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || a.Name.Space == "" && a.Name.Local == "xmlns" {
					continue
				}
				if depth >= 3 {
					changes[len(changes)-1].invalid = true
				}
			}
			stack = append(stack, t.Name)
		case xml.EndElement:
			depth := len(stack)
			if skipDepth > 0 {
				stack = stack[:depth-1]
				if depth == skipDepth {
					skipDepth = 0
				}
				continue
			}
			if depth == 3 && propCount == 0 || depth == 2 && instructionCount == 0 {
				return nil, 400
			}
			stack = stack[:depth-1]
		case xml.CharData:
			if skipDepth > 0 {
				continue
			}
			if len(stack) >= 4 {
				c := &changes[len(changes)-1]
				c.value += string(t)
				aggregate += len(t)
				if len(c.value) > files.MaxMetadataValueBytes || aggregate > 16384 {
					return nil, 413
				}
			} else if strings.TrimSpace(string(t)) != "" {
				return nil, 400
			}
		}
	}
}
func patchProperties(w http.ResponseWriter, r *http.Request, service *files.Service, path string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
	if err != nil {
		code := 400
		if _, ok := err.(*http.MaxBytesError); ok {
			code = 413
		}
		http.Error(w, http.StatusText(code), code)
		return
	}
	changes, status := parsePropertyPatch(body)
	if status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	failed := false
	var operations []files.MetadataChange
	for i := range changes {
		c := &changes[i]
		c.status = 200
		c.protected = protectedProperty(c.name)
		if c.protected || c.name.Space != windowsNamespace || !files.SupportedMetadata(c.name.Local) {
			c.status = 403
		} else if c.invalid || !files.ValidMetadataValue(c.value) {
			c.status = 409
		}
		if c.status != 200 {
			failed = true
		}
		operations = append(operations, files.MetadataChange{Name: c.name.Local, Value: c.value, Remove: c.remove})
	}
	if failed {
		operations = nil
		for i := range changes {
			if changes[i].status == 200 {
				changes[i].status = 424
			}
		}
	}
	// Prepare both bounded wire outcomes before publication. A response encoding
	// failure must never turn a committed operation into an HTTP failure.
	payload, err := encodePatchResponse(r.URL.Path, changes)
	storageChanges := append([]propertyChange(nil), changes...)
	for i := range storageChanges {
		storageChanges[i].protected = false
		storageChanges[i].status = 424
	}
	storageChanges[0].status = 500
	storagePayload, storageErr := encodePatchResponse(r.URL.Path, storageChanges)
	if err != nil || storageErr != nil {
		http.Error(w, "Metadata response failed", 500)
		return
	}
	node, err := service.PatchMetadata(r.Context(), path, operations, publicationExpectation(r))
	if err != nil {
		if !errors.Is(err, files.ErrUnavailable) {
			namespaceError(w, r, err)
			return
		}
		payload = storagePayload
	} else if node.File {
		w.Header().Set("ETag", node.ETag)
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(207)
	_, _ = w.Write(payload)
}

func encodePatchResponse(requestPath string, changes []propertyChange) ([]byte, error) {
	var output bytes.Buffer
	e := xml.NewEncoder(&output)
	start := func(local string) { _ = e.EncodeToken(xml.StartElement{Name: xml.Name{Space: "DAV:", Local: local}}) }
	end := func(local string) { _ = e.EncodeToken(xml.EndElement{Name: xml.Name{Space: "DAV:", Local: local}}) }
	text := func(local, value string) { start(local); _ = e.EncodeToken(xml.CharData(value)); end(local) }
	start("multistatus")
	start("response")
	href := (&url.URL{Path: requestPath}).EscapedPath()
	text("href", href)
	for _, c := range changes {
		start("propstat")
		start("prop")
		_ = e.EncodeToken(xml.StartElement{Name: c.name})
		_ = e.EncodeToken(xml.EndElement{Name: c.name})
		end("prop")
		text("status", fmt.Sprintf("HTTP/1.1 %d %s", c.status, http.StatusText(c.status)))
		if c.protected {
			start("error")
			start("cannot-modify-protected-property")
			end("cannot-modify-protected-property")
			end("error")
		}
		end("propstat")
	}
	end("response")
	end("multistatus")
	if e.Flush() != nil || output.Len() > 1<<20 {
		return nil, errors.New("metadata response exceeds bounds")
	}
	return output.Bytes(), nil
}
