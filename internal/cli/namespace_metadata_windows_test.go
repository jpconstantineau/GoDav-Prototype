package cli_test

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

type collectionMetadata struct {
	display, modified string
}

func namespaceMetadata(t *testing.T, client *http.Client, f serverFixture, path, depth string) map[string]collectionMetadata {
	t.Helper()
	body := `<d:propfind xmlns:d="DAV:"><d:prop><d:displayname/><d:getlastmodified/><d:resourcetype/><d:supportedlock/><d:creationdate/><d:getcontentlength/><d:getcontenttype/><d:getetag/></d:prop></d:propfind>`
	req, _ := http.NewRequest("PROPFIND", f.origin+path, strings.NewReader(body))
	req.SetBasicAuth("FixtureUser", f.password)
	req.Header.Set("Depth", depth)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("metadata request failed")
	}
	defer response.Body.Close()
	var result struct {
		Responses []struct {
			Href      string `xml:"DAV: href"`
			Propstats []struct {
				Status string `xml:"DAV: status"`
				Prop   struct {
					Values []struct {
						XMLName    xml.Name
						Text       string     `xml:",chardata"`
						Collection *struct{}  `xml:"DAV: collection"`
						Locks      []struct{} `xml:"DAV: lockentry"`
					} `xml:",any"`
				} `xml:"DAV: prop"`
			} `xml:"DAV: propstat"`
		} `xml:"DAV: response"`
	}
	if response.StatusCode != 207 || xml.NewDecoder(response.Body).Decode(&result) != nil {
		t.Fatal("metadata response invalid")
	}
	out := map[string]collectionMetadata{}
	for _, entry := range result.Responses {
		statuses := map[string]int{}
		var metadata collectionMetadata
		for _, stat := range entry.Propstats {
			var code int
			if _, err := fmt.Sscanf(stat.Status, "HTTP/1.1 %d", &code); err != nil {
				t.Fatal("property status invalid")
			}
			for _, property := range stat.Prop.Values {
				if property.XMLName.Space != "DAV:" || statuses[property.XMLName.Local] != 0 {
					t.Fatal("property namespace or duplication invalid")
				}
				statuses[property.XMLName.Local] = code
				switch property.XMLName.Local {
				case "displayname":
					metadata.display = property.Text
				case "getlastmodified":
					metadata.modified = property.Text
				case "resourcetype":
					if property.Collection == nil {
						t.Fatal("collection type missing")
					}
				case "supportedlock":
					if len(property.Locks) != 1 {
						t.Fatal("exclusive lock capability missing")
					}
				}
			}
		}
		for _, name := range []string{"displayname", "getlastmodified", "resourcetype", "supportedlock", "creationdate"} {
			if statuses[name] != 200 {
				t.Fatal("implemented collection metadata unavailable")
			}
		}
		for _, name := range []string{"getcontentlength", "getcontenttype", "getetag"} {
			if statuses[name] != 404 {
				t.Fatal("unimplemented metadata falsely available")
			}
		}
		modified, err := time.Parse(http.TimeFormat, metadata.modified)
		if err != nil || modified.UTC().Format(http.TimeFormat) != metadata.modified || modified.Nanosecond() != 0 {
			t.Fatal("metadata timestamp is not canonical UTC seconds")
		}
		if _, duplicate := out[entry.Href]; duplicate {
			t.Fatal("duplicate metadata resource")
		}
		out[entry.Href] = metadata
	}
	return out
}

func waitPastMetadataSecond(t *testing.T, value string) {
	t.Helper()
	stamp, err := time.Parse(http.TimeFormat, value)
	if err != nil {
		t.Fatal("timestamp fixture invalid")
	}
	if wait := time.Until(stamp.Add(time.Second)); wait > 0 {
		time.Sleep(wait + 20*time.Millisecond)
	}
}

func TestNamespaceMetadataPersistsAndChangesOnlyOnCommit(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	root := namespaceMetadata(t, client, f, "/dav/", "0")
	if len(root) != 1 || root["/dav/"].display != "" {
		t.Fatal("root metadata identity differs")
	}
	namespaceStatus(t, client, f, "MKCOL", "/dav/MixEd%20%26%20Notes/", nil, 201)
	before := namespaceMetadata(t, client, f, "/dav/", "1")
	parentHref := "/dav/MixEd%20&%20Notes/"
	if len(before) != 2 || before[parentHref].display != "MixEd & Notes" {
		t.Fatal("display spelling was not preserved")
	}
	waitPastMetadataSecond(t, before[parentHref].modified)
	namespaceStatus(t, client, f, "MKCOL", "/dav/mixed%20%26%20notes/Child/", nil, 201)
	after := namespaceMetadata(t, client, f, "/dav/MIXED%20%26%20NOTES/", "1")
	parent := after[parentHref]
	childHref := parentHref + "Child/"
	if len(after) != 2 || parent.display != "MixEd & Notes" || after[childHref].display != "Child" {
		t.Fatal("nested metadata differs")
	}
	oldTime, _ := time.Parse(http.TimeFormat, before[parentHref].modified)
	newTime, _ := time.Parse(http.TimeFormat, parent.modified)
	if !newTime.After(oldTime) {
		t.Fatal("successful child creation did not update parent modification time")
	}
	unchangedRoot := namespaceMetadata(t, client, f, "/dav/", "0")
	if unchangedRoot["/dav/"] != before["/dav/"] {
		t.Fatal("child creation modified an unrelated ancestor")
	}
	waitPastMetadataSecond(t, parent.modified)
	namespaceStatus(t, client, f, "MKCOL", "/dav/mixed%20%26%20notes/CHILD/", nil, 405)
	duplicate := namespaceMetadata(t, client, f, "/dav/mixed%20%26%20notes/", "1")
	if duplicate[parentHref] != parent || duplicate[childHref] != after[childHref] {
		t.Fatal("failed create changed timestamps or display spelling")
	}
	p.stop()
	p = startServer(t, bin, f)
	defer p.stop()
	reopened := namespaceMetadata(t, client, f, "/dav/mixed%20%26%20notes/", "1")
	if len(reopened) != 2 || reopened[parentHref] != parent || reopened[childHref] != after[childHref] {
		t.Fatal("persisted collection metadata changed after restart")
	}
}
