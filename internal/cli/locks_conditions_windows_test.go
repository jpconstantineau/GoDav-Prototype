package cli_test

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLocksTimeoutAndResourceScope(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	for i, tc := range []struct {
		values  []string
		want    int
		timeout string
	}{
		{nil, 201, "Second-600"},
		{[]string{"Infinite"}, 201, "Second-3600"},
		{[]string{"Second-4294967295"}, 201, "Second-3600"},
		{[]string{"Second-0"}, 201, "Second-1"},
		{[]string{"Second-5", "Infinite"}, 201, "Second-5"},
		{[]string{"sEcOnD-5"}, 201, "Second-5"},
		{[]string{"iNfInItE"}, 201, "Second-3600"},
		{[]string{"Second-5, bad"}, 400, ""},
		{[]string{"Second-4294967296"}, 400, ""},
	} {
		req, _ := http.NewRequest("LOCK", f.origin+fmt.Sprintf("/dav/timeout%d", i), strings.NewReader(lockRequestXML))
		req.SetBasicAuth("FixtureUser", f.password)
		for _, v := range tc.values {
			req.Header.Add("Timeout", v)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal("timeout request")
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.want || tc.timeout != "" && !strings.Contains(string(data), tc.timeout) {
			t.Fatalf("timeout %d: %d", i, resp.StatusCode)
		}
	}
	token := acquireFixtureLock(t, c, f, "/dav/scope", "0", "Second-60")
	for _, values := range [][]string{nil, {token, token}} {
		req, _ := http.NewRequest("UNLOCK", f.origin+"/dav/scope", nil)
		req.SetBasicAuth("FixtureUser", f.password)
		for _, value := range values {
			req.Header.Add("Lock-Token", value)
		}
		response, err := c.Do(req)
		if err != nil {
			t.Fatal("UNLOCK header validation request failed")
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("UNLOCK requires exactly one Lock-Token field: %d", response.StatusCode)
		}
		code, _, _ := fileRequest(t, c, f, "PUT", "/dav/scope", strings.NewReader("refused"), nil)
		if code != 423 {
			t.Fatalf("invalid UNLOCK changed the active lease: %d", code)
		}
	}
	fileRequest(t, c, f, "PUT", "/dav/other", strings.NewReader("other"), nil)
	code, _, body := fileRequest(t, c, f, "LOCK", "/dav/other", nil, map[string]string{"If": "(" + token + ")"})
	if code != 412 || !strings.Contains(string(body), "lock-token-matches-request-uri") {
		t.Fatalf("refresh scope response: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "UNLOCK", "/dav/other", nil, map[string]string{"Lock-Token": token})
	if code != 409 {
		t.Fatalf("unlock scope: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "LOCK", "/dav/scope/", nil, map[string]string{"If": "(" + token + ")"})
	if code != 400 {
		t.Fatalf("file trailing slash refresh: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "LOCK", "/dav/scope", nil, map[string]string{"If": "</dav/SCOPE> (" + token + ")", "Depth": "ignored-on-refresh"})
	if code != 200 {
		t.Fatalf("canonical refresh: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "LOCK", "/dav/missing-parent/child", strings.NewReader(lockRequestXML), nil)
	if code != 409 {
		t.Fatalf("LOCK parent creation: %d", code)
	}
}

func TestLocksActiveCapacityReleases(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, _, _ := fileRequest(t, c, f, "MKCOL", "/dav/capacity/", nil, nil)
	if code != 201 {
		t.Fatal("fixture parent")
	}
	first := ""
	for i := 0; i < 1024; i++ {
		token := acquireFixtureLock(t, c, f, fmt.Sprintf("/dav/capacity/f%04d", i), "0", "Second-3600")
		if i == 0 {
			first = token
		}
	}
	code, _, _ = fileRequest(t, c, f, "LOCK", "/dav/capacity/", strings.NewReader(lockRequestXML), map[string]string{"Depth": "0"})
	if code != 507 {
		t.Fatalf("active lease cap: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "UNLOCK", "/dav/capacity/f0000", nil, map[string]string{"Lock-Token": first})
	if code != 204 {
		t.Fatal("release capacity")
	}
	code, _, _ = fileRequest(t, c, f, "LOCK", "/dav/capacity/", strings.NewReader(lockRequestXML), map[string]string{"Depth": "0"})
	if code != 200 {
		t.Fatalf("lease capacity not reusable: %d", code)
	}
}

func TestLocksOwnerImplicitEmptyNamespace(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	body := strings.Replace(lockRequestXML, "synthetic-owner", "<plain>kept</plain>", 1)
	code, _, data := fileRequest(t, c, f, "LOCK", "/dav/owner", strings.NewReader(body), nil)
	if code != 201 {
		t.Fatalf("owner create: %d", code)
	}
	check := func(data []byte) {
		t.Helper()
		decoder := xml.NewDecoder(strings.NewReader(string(data)))
		seen := false
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "plain" {
				seen = true
				if start.Name.Space != "" {
					t.Fatal("unqualified owner child changed namespace")
				}
			}
		}
		if !seen {
			t.Fatal("owner child absent")
		}
	}
	check(data)
	code, _, data = fileRequest(t, c, f, "PROPFIND", "/dav/owner", strings.NewReader(`<d:propfind xmlns:d="DAV:"><d:prop><d:lockdiscovery/></d:prop></d:propfind>`), map[string]string{"Depth": "0"})
	if code != 207 {
		t.Fatal("discovery")
	}
	check(data)
}

func TestLocksOwnerInheritedLanguage(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	for _, tc := range []struct {
		name, root, owner, want string
		present                 bool
	}{
		{"inherited", ` xml:lang="fr"`, "", "fr", true},
		{"override", ` xml:lang="fr"`, ` xml:lang="ja"`, "ja", true},
		{"reset", ` xml:lang="fr"`, ` xml:lang=""`, "", true},
		{"inherited-empty", ` xml:lang=""`, "", "", true},
		{"absent", "", "", "", false},
	} {
		body := `<d:lockinfo xmlns:d="DAV:"` + tc.root + `><d:lockscope><d:exclusive/></d:lockscope><d:locktype><d:write/></d:locktype><d:owner` + tc.owner + `>owner<plain xml:lang="de">text</plain></d:owner></d:lockinfo>`
		path := "/dav/language-" + tc.name
		code, h, data := fileRequest(t, c, f, "LOCK", path, strings.NewReader(body), map[string]string{"Depth": "iNfInItY"})
		if code != 201 || !strings.Contains(string(data), "<D:depth>infinity</D:depth>") {
			t.Fatalf("language %s create: %d", tc.name, code)
		}
		check := func(data []byte) {
			t.Helper()
			decoder := xml.NewDecoder(strings.NewReader(string(data)))
			seen, childSeen := false, false
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				start, ok := token.(xml.StartElement)
				if ok && start.Name == (xml.Name{Local: "plain"}) {
					for _, a := range start.Attr {
						if a.Name == (xml.Name{Space: "http://www.w3.org/XML/1998/namespace", Local: "lang"}) && a.Value == "de" {
							childSeen = true
						}
					}
				}
				if !ok || start.Name != (xml.Name{Space: "DAV:", Local: "owner"}) {
					continue
				}
				seen = true
				present, value := false, ""
				for _, a := range start.Attr {
					if a.Name == (xml.Name{Space: "http://www.w3.org/XML/1998/namespace", Local: "lang"}) {
						present, value = true, a.Value
					}
				}
				if present != tc.present || value != tc.want {
					t.Fatalf("language %s was not preserved", tc.name)
				}
			}
			if !seen || !childSeen {
				t.Fatal("owner or descendant language absent")
			}
		}
		check(data)
		code, _, data = fileRequest(t, c, f, "PROPFIND", path, strings.NewReader(`<d:propfind xmlns:d="DAV:"><d:prop><d:lockdiscovery/></d:prop></d:propfind>`), map[string]string{"Depth": "0"})
		if code != 207 {
			t.Fatal("language discovery")
		}
		check(data)
		fileRequest(t, c, f, "UNLOCK", path, nil, map[string]string{"Lock-Token": h.Get("Lock-Token")})
	}
}

func TestLocksDAVIfConditionsPreserveState(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	code, h, _ := fileRequest(t, c, f, "PUT", "/dav/conditions", strings.NewReader("unchanged"), nil)
	if code != 201 {
		t.Fatal("seed")
	}
	tag := h.Get("ETag")
	token := acquireFixtureLock(t, c, f, "/dav/conditions", "0", "Second-60")
	noop := `<d:propertyupdate xmlns:d="DAV:" xmlns:z="urn:schemas-microsoft-com:"><d:remove><d:prop><z:Win32FileAttributes/></d:prop></d:remove></d:propertyupdate>`
	for _, tc := range []struct {
		name, value string
		want        int
	}{
		{"token", "(" + token + ")", 207},
		{"missing-token", "(<urn:uuid:missing>)", 412},
		{"negated-current", "(Not " + token + ")", 412},
		{"negated-no-space", "(Not" + token + ")", 412},
		{"negated-mixed-case", "(nOt " + token + ")", 412},
		{"opaque-token-case", "(" + strings.ToUpper(token) + ")", 412},
		{"negated-etag-mixed-case", "(" + token + ` NOT["different"])`, 207},
		{"etag-value-case", "(" + token + " [" + strings.ToUpper(tag) + "])", 412},
		{"invalid-negation-suffix", "(NotX " + token + ")", 400},
		{"etag-only-locked", "([" + tag + "])", 423},
		{"conjunction-current", "(" + token + " [" + tag + "])", 207},
		{"conjunction-stale", "(" + token + ` ["stale"])`, 412},
		{"weak-etag", "(" + token + " [W/" + tag + "])", 207},
		{"disjunction", `(["stale"]) (` + token + ")", 207},
		{"submitted-negated", "(Not " + token + ") (Not <DAV:no-lock>)", 207},
		{"unsubmitted-true", "(Not <DAV:no-lock>)", 423},
		{"tagged", "</dav/CONDITIONS> (" + token + ")", 207},
		{"absolute-tag", "<" + f.origin + "/dav/conditions> (" + token + ")", 207},
		{"tag-missing", "</dav/absent> (" + token + ")", 412},
		{"trailing-junk", "(" + token + ") junk", 400},
		{"mixed", "(" + token + ") </dav/conditions> (" + token + ")", 400},
		{"foreign-origin", "<https://example.invalid/dav/conditions> (" + token + ")", 400},
		{"outside-mount", "</other> (" + token + ")", 400},
		{"empty-list", "()", 400},
		{"invalid-token", "(urn:uuid:missing)", 400},
		{"invalid-escape", "(<urn:uuid:bad%xy>)", 400},
		{"fragment", "(<urn:uuid:test#fragment>)", 400},
		{"too-many-lists", strings.Repeat("(Not <DAV:no-lock>) ", 33), 431},
		{"too-many-conditions", "(" + strings.Repeat("Not <DAV:no-lock> ", 129) + ")", 431},
		{"too-many-bytes", "(<urn:uuid:" + strings.Repeat("a", 8192) + ">)", 431},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "etag-value-case" && strings.ToUpper(tag) == tag {
				t.Skip("random fixture entity tag has no cased characters")
			}
			code, _, _ = fileRequest(t, c, f, "PROPPATCH", "/dav/conditions", strings.NewReader(noop), map[string]string{"If": tc.value})
			if code != tc.want {
				t.Fatalf("condition status=%d want=%d", code, tc.want)
			}
			code, h, body := fileRequest(t, c, f, "GET", "/dav/conditions", nil, nil)
			if code != 200 || h.Get("ETag") != tag || string(body) != "unchanged" {
				t.Fatal("condition changed state")
			}
		})
	}
	for _, method := range []string{"GET", "HEAD", "OPTIONS", "PROPFIND"} {
		code, _, _ := fileRequest(t, c, f, method, "/dav/conditions", nil, map[string]string{"If": "(Not " + token + ")", "Depth": "0"})
		if code != 412 {
			t.Fatalf("read condition %s: %d", method, code)
		}
	}
	req, _ := http.NewRequest("PROPPATCH", f.origin+"/dav/conditions", strings.NewReader(noop))
	req.SetBasicAuth("FixtureUser", f.password)
	req.Header.Add("If", "("+token+")")
	req.Header.Add("If", "("+token+")")
	response, err := c.Do(req)
	if err != nil {
		t.Fatal("duplicate If request")
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatalf("duplicate If: %d", response.StatusCode)
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/conditions", strings.NewReader("lost"), map[string]string{"If": "(" + token + ")", "If-Match": `"stale"`})
	if code != 412 {
		t.Fatalf("HTTP precondition lost: %d", code)
	}
	code, _, _ = fileRequest(t, c, f, "UNLOCK", "/dav/conditions", nil, map[string]string{"Lock-Token": token})
	if code != 204 {
		t.Fatal("unlock")
	}
	code, _, _ = fileRequest(t, c, f, "PUT", "/dav/conditions", strings.NewReader("direct condition"), map[string]string{"If": "([" + tag + "])", "If-Match": tag})
	if code != 204 {
		t.Fatalf("unlocked etag condition: %d", code)
	}
}

func TestLocksXMLCompleteAndOwnerNamespaces(t *testing.T) {
	f, c, _ := liveFiles(t, 1024)
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"inherited", `<d:lockinfo xmlns:d="DAV:" xmlns:z="urn:owner"><d:lockscope><d:exclusive/></d:lockscope><d:locktype><d:write/></d:locktype><d:owner><z:label z:kind="z:text">A &amp; B</z:label><plain xmlns="">text</plain></d:owner></d:lockinfo>`, 201},
		{"self-closing", `<d:lockinfo xmlns:d="DAV:"><d:lockscope><d:exclusive/></d:lockscope><d:locktype><d:write/></d:locktype><d:owner/></d:lockinfo>`, 201},
		{"extension", `<d:lockinfo xmlns:d="DAV:" xmlns:x="urn:extension"><x:extra><d:lockscope><d:shared/></d:lockscope></x:extra><d:lockscope><d:exclusive/></d:lockscope><d:locktype><d:write/></d:locktype></d:lockinfo>`, 201},
		{"trailing", lockRequestXML + `<extra/>`, 400},
		{"unbound", strings.Replace(lockRequestXML, "synthetic-owner", "<z:owner/>", 1), 400},
		{"invalid-binding", strings.Replace(lockRequestXML, `xmlns:d="DAV:"`, `xmlns:d="DAV:" xmlns:xml="urn:wrong"`, 1), 400},
		{"wrong-root", strings.ReplaceAll(lockRequestXML, "lockinfo", "other"), 400},
		{"xml-name-case", strings.ReplaceAll(lockRequestXML, "exclusive", "Exclusive"), 400},
		{"doctype", `<!DOCTYPE x>` + lockRequestXML, 400},
		{"owner-cap", strings.Replace(lockRequestXML, "synthetic-owner", strings.Repeat("x", 16385), 1), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := "/dav/xml-" + tc.name
			code, h, data := fileRequest(t, c, f, "LOCK", path, strings.NewReader(tc.body), map[string]string{"Depth": "0"})
			if code != tc.want {
				t.Fatalf("LOCK XML: %d want %d", code, tc.want)
			}
			if code == 201 {
				assertStrictMetadataXML(t, data)
				if tc.name == "inherited" && (!strings.Contains(string(data), `xmlns:z="urn:owner"`) || !strings.Contains(string(data), `<plain xmlns="">`)) {
					t.Fatal("owner namespace meaning lost")
				}
				code, _, data = fileRequest(t, c, f, "PROPFIND", path, strings.NewReader(`<d:propfind xmlns:d="DAV:"><d:prop><d:lockdiscovery/></d:prop></d:propfind>`), map[string]string{"Depth": "0"})
				if code != 207 {
					t.Fatal("discovery")
				}
				assertStrictMetadataXML(t, data)
				fileRequest(t, c, f, "UNLOCK", path, nil, map[string]string{"Lock-Token": h.Get("Lock-Token")})
			} else {
				code, _, _ := fileRequest(t, c, f, "GET", path, nil, nil)
				if code != 404 {
					t.Fatal("failed LOCK left placeholder")
				}
			}
		})
	}
}
