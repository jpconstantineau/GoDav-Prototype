package cli_test

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Windows' independent XML reader rejects reserved namespace bindings that
// encoding/xml's permissive namespace handling accepts even in strict mode.
func assertStrictMetadataXML(t *testing.T, data []byte) {
	t.Helper()
	dir := t.TempDir()
	response := filepath.Join(dir, "response.xml")
	script := filepath.Join(dir, "read.ps1")
	if err := os.WriteFile(response, data, 0600); err != nil {
		t.Fatal(err)
	}
	const read = `param([string] $Response)
$ErrorActionPreference = 'Stop'
try {
    $settings = New-Object System.Xml.XmlReaderSettings
    $settings.DtdProcessing = [System.Xml.DtdProcessing]::Prohibit
    $settings.XmlResolver = $null
    $reader = [System.Xml.XmlReader]::Create($Response, $settings)
    try { while ($reader.Read()) {} } finally { $reader.Dispose() }
} catch { [Console]::Error.WriteLine($_.Exception.Message); exit 1 }
`
	if err := os.WriteFile(script, []byte(read), 0600); err != nil {
		t.Fatal(err)
	}
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	out, stderr, code := command(t, dir, powershell, "-NoProfile", "-NonInteractive", "-File", script, response)
	if code != 0 {
		t.Fatalf("patch response is not namespace-well-formed XML: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
}

func TestMetadataRejectedPropertyQNames(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, _, _ := fileRequest(t, c, f, "PUT", "/dav/qnames", strings.NewReader("unchanged bytes"), nil)
	if code != 201 {
		t.Fatal("seed failed")
	}
	seed := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:set><d:prop><z:Win32CreationTime>kept</z:Win32CreationTime></d:prop></d:set></d:propertyupdate>`
	_, h, _ := metadataPatch(t, c, f, "/dav/qnames", strings.NewReader(seed), nil, "Win32CreationTime")
	tag := h.Get("ETag")
	for _, test := range []struct {
		name, declarations, properties string
		want                           []xml.Name
	}{
		{"unqualified", "", `<x/>`, []xml.Name{{Local: "x"}}},
		{"reset-default", `xmlns="DAV:"`, `<x xmlns=""/>`, []xml.Name{{Local: "x"}}},
		{"reserved-xml", "", `<xml:lang/>`, []xml.Name{{Space: "http://www.w3.org/XML/1998/namespace", Local: "lang"}}},
		{"mixed", "", `<x/><q:x xmlns:q="urn:test:custom"/><xml:lang/>`, []xml.Name{{Local: "x"}, {Space: "urn:test:custom", Local: "x"}, {Space: "http://www.w3.org/XML/1998/namespace", Local: "lang"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:" ` + test.declarations + `><d:set><d:prop>` + test.properties + `<z:Win32CreationTime>must not publish</z:Win32CreationTime></d:prop></d:set></d:propertyupdate>`
			code, h, data := fileRequest(t, c, f, "PROPPATCH", "/dav/qnames", strings.NewReader(body), nil)
			if code != 207 || h.Get("ETag") != tag {
				t.Fatalf("rejected patch changed response/tag: %d", code)
			}
			assertStrictMetadataXML(t, data)
			var doc metadataReply
			if err := xml.Unmarshal(data, &doc); err != nil || len(doc.Responses) != 1 || len(doc.Responses[0].Stats) != len(test.want)+1 {
				t.Fatal("patch omitted requested property statuses")
			}
			want := append(append([]xml.Name(nil), test.want...), xml.Name{Space: "urn:schemas-microsoft-com:", Local: "Win32CreationTime"})
			for i, stat := range doc.Responses[0].Stats {
				status := "HTTP/1.1 403 Forbidden"
				if i == len(test.want) {
					status = "HTTP/1.1 424 Failed Dependency"
				}
				if stat.Status != status || stat.Error != nil || len(stat.Prop.Values) != 1 || stat.Prop.Values[0].XMLName != want[i] || stat.Prop.Values[0].Text != "" {
					t.Fatalf("property %d response differs: status=%q property=%+v want=%+v", i, stat.Status, stat.Prop.Values, want[i])
				}
			}
			code, h, data = fileRequest(t, c, f, "GET", "/dav/qnames", nil, nil)
			if code != 200 || h.Get("ETag") != tag || string(data) != "unchanged bytes" {
				t.Fatal("rejected patch changed content or validator")
			}
			assertStoredMetadata(t, c, f, "/dav/qnames", map[string]string{"Win32CreationTime": "kept"})
		})
	}
}
