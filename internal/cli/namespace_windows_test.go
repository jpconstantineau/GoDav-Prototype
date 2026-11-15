package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
)

func namespaceListing(t *testing.T, client *http.Client, f serverFixture, path, depth string) []string {
	t.Helper()
	req, _ := http.NewRequest("PROPFIND", f.origin+path, nil)
	req.SetBasicAuth("FixtureUser", f.password)
	req.Header.Set("Depth", depth)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("namespace listing failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 207 {
		t.Fatalf("listing returned %d", response.StatusCode)
	}
	var result struct {
		Responses []struct {
			Href string `xml:"DAV: href"`
		} `xml:"DAV: response"`
	}
	if xml.Unmarshal(body, &result) != nil {
		t.Fatal("namespace XML invalid")
	}
	hrefs := make([]string, 0, len(result.Responses))
	for _, response := range result.Responses {
		hrefs = append(hrefs, response.Href)
	}
	slices.Sort(hrefs)
	return hrefs
}

func TestNamespaceInProcessConcurrentHTTPAndStop(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	logs := &safeCapture{}
	go func() { done <- server.Run(ctx, f.config, server.Identity{}, logs) }()
	stopped := false
	defer func() {
		if !stopped {
			cancel()
			<-done
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "msg=ready") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(logs.String(), "msg=ready") {
		t.Fatal("in-process HTTPS did not become ready")
	}
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	barrier := make(chan struct{})
	statuses := make(chan int, 4)
	var wg sync.WaitGroup
	for _, method := range []string{"MKCOL", "MKCOL", "PROPFIND", "PROPFIND"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			path := "/dav/"
			if method == "MKCOL" {
				path += "Shared/"
			}
			req, _ := http.NewRequest(method, f.origin+path, nil)
			req.SetBasicAuth("FixtureUser", f.password)
			req.Header.Set("Depth", "1")
			response, err := retryAdmission(client, req)
			if err != nil {
				statuses <- 0
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			statuses <- response.StatusCode
		}()
	}
	close(barrier)
	wg.Wait()
	close(statuses)
	got := []int{}
	for code := range statuses {
		got = append(got, code)
	}
	slices.Sort(got)
	if !slices.Equal(got, []int{201, 207, 207, 405}) {
		t.Fatalf("in-process concurrent statuses: %v", got)
	}
	if got := namespaceListing(t, client, f, "/dav/", "1"); !slices.Equal(got, []string{"/dav/", "/dav/Shared/"}) {
		t.Fatal("concurrent snapshot/create lost committed child")
	}
	// All requests and the committed-child readback are complete. Release the
	// client's unused connections before stopping the server.
	client.CloseIdleConnections()
	stopStarted := time.Now()
	cancel()
	err := <-done
	stopped = true
	if err != nil {
		// Keep checking ownership and persistent state after the server has joined.
		t.Errorf("namespace drain failed: err=%v deadline=%t shutdown_failed=%t serving_failed=%t elapsed=%s\nsafe server diagnostics:\n%s",
			err, errors.Is(err, server.ErrShutdownDeadline), errors.Is(err, server.ErrShutdownFailed), errors.Is(err, server.ErrServing), time.Since(stopStarted), logs.String())
	}
	dbPath := filepath.Join(f.dir, "godav.sqlite")
	beforeDB, _ := os.ReadFile(dbPath)
	beforeInfo, _ := os.Stat(dbPath)
	beforeSecurity := localStateSecurity(t, dbPath)
	out, stderr, code := command(t, f.dir, bin, "init", "config", "--config", f.config, "--username", "Other")
	if code != 0 || out != "godav: already initialized\n" || stderr != "" {
		t.Fatal("namespace stop retained ownership or invalid state")
	}
	afterDB, _ := os.ReadFile(dbPath)
	afterInfo, _ := os.Stat(dbPath)
	if !bytes.Equal(beforeDB, afterDB) || !os.SameFile(beforeInfo, afterInfo) || beforeSecurity != localStateSecurity(t, dbPath) {
		t.Fatal("no-op changed database bytes, identity or permissions")
	}
}

func TestNamespaceServerRequiresWritablePrivateParent(t *testing.T) {
	bin := development(t)
	source := newServerFixture(t, bin)
	for _, inheritance := range []string{"", "CI"} {
		dir := privateFixtureWithInheritance(t, inheritance)
		for _, name := range []string{"godav.yaml", "godav.sqlite", "godav.sqlite.lock"} {
			data, err := os.ReadFile(filepath.Join(source.dir, name))
			if err != nil {
				t.Fatal("fixture read failed")
			}
			if name == "godav.yaml" {
				for _, asset := range []string{"server.crt", "server.key"} {
					old := "'tls/" + asset + "'"
					if !bytes.Contains(data, []byte(old)) {
						t.Fatal("TLS fixture setting missing")
					}
					data = bytes.ReplaceAll(data, []byte(old), []byte("'"+filepath.ToSlash(filepath.Join(source.dir, "tls", asset))+"'"))
				}
			}
			writeRestrictedFixtureFile(t, filepath.Join(dir, name), data)
		}
		before := snapshotLocalState(t, dir)
		f := source
		f.dir = dir
		f.config = filepath.Join(dir, "godav.yaml")
		if namespaceServerRefusal(t, bin, f) != "godav: server startup failed\n" {
			t.Fatal("server did not return the fixed startup refusal")
		}
		assertLocalStateUnchanged(t, dir, before)
	}
}

func namespaceServerRefusal(t *testing.T, bin string, f serverFixture) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "run", "server", "--config", f.config)
	cmd.Dir = t.TempDir()
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil || ctx.Err() != nil || cmd.ProcessState.ExitCode() != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "msg=ready") || strings.Contains(stderr.String(), f.password) {
		t.Fatal("namespace startup did not fail safely before readiness")
	}
	return stderr.String()
}

func TestNamespaceMigrationRollsBackAndRecoversExplicitly(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	path := filepath.Join(f.dir, "godav.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal("fixture open failed")
	}
	_, err = db.Exec(`CREATE TABLE nodes(unrelated TEXT); INSERT INTO nodes VALUES('fixture');`)
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatal("fixture creation failed")
	}
	before, _ := os.ReadFile(path)
	config, _ := os.ReadFile(f.config)
	namespaceServerRefusal(t, bin, f)
	after, _ := os.ReadFile(path)
	afterConfig, _ := os.ReadFile(f.config)
	if !bytes.Equal(before, after) || !bytes.Equal(config, afterConfig) {
		t.Fatal("failed migration partially published")
	}
	// Remove only the deliberately conflicting test fixture. Production never
	// removes/adopts an unknown object or exposes a recovery control.
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal("fixture recovery open failed")
	}
	_, err = db.Exec(`DROP TABLE nodes`)
	closeErr = db.Close()
	if err != nil || closeErr != nil {
		t.Fatal("fixture recovery failed")
	}
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	if got := namespaceListing(t, client, f, "/dav/", "1"); !slices.Equal(got, []string{"/dav/"}) {
		t.Fatal("recovered v1 did not initialize cleanly")
	}
	namespaceStatus(t, client, f, "MKCOL", "/dav/Recovered/", nil, 201)
}

func TestNamespaceRefusesMalformedStateWithoutRepair(t *testing.T) {
	bin := development(t)
	for _, statement := range []string{
		`UPDATE nodes SET name_key='WRONG' WHERE name='Child'`,
		`UPDATE nodes SET parent_id='missing' WHERE name='Child'`,
		`UPDATE nodes SET parent_id=id WHERE name='Child'`,
		`UPDATE namespace_policy SET unicode_version='future'`,
		`DELETE FROM namespace_policy`,
		`PRAGMA ignore_check_constraints=ON; UPDATE nodes SET name='wrong' WHERE id='root'`,
		`PRAGMA user_version=5`,
	} {
		f := newServerFixture(t, bin)
		p := startServer(t, bin, f)
		client := fixtureClient(f)
		namespaceStatus(t, client, f, "MKCOL", "/dav/Child/", nil, 201)
		client.CloseIdleConnections()
		p.stop()
		path := filepath.Join(f.dir, "godav.sqlite")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal("fixture open failed")
		}
		_, err = db.Exec(statement)
		closeErr := db.Close()
		if err != nil || closeErr != nil {
			t.Fatal("malformed fixture preparation failed")
		}
		before, _ := os.ReadFile(path)
		config, _ := os.ReadFile(f.config)
		out, stderr, code := command(t, f.dir, bin, "init", "config", "--config", f.config, "--username", "Other")
		if code != 1 || out != "" || stderr == "" {
			t.Fatal("malformed state accepted by read-only bootstrap")
		}
		namespaceServerRefusal(t, bin, f)
		after, _ := os.ReadFile(path)
		afterConfig, _ := os.ReadFile(f.config)
		if !bytes.Equal(before, after) || !bytes.Equal(config, afterConfig) {
			t.Fatal("malformed state was repaired or reset")
		}
	}
}

func TestNamespaceBoundsListingAndSiblingCapacity(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	p.stop()
	// Independent known collection fixture; assertions remain HTTP outcomes.
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "godav.sqlite"))
	if err != nil {
		t.Fatal("fixture open failed")
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		t.Fatal("fixture transaction failed")
	}
	for i := 1; i <= 1024; i++ {
		name := fmt.Sprintf("CHILD-%04d", i)
		if _, err = tx.Exec(`INSERT INTO nodes VALUES(?,'root',?,?,1,1)`, fmt.Sprintf("%032x", i), name, name); err != nil {
			tx.Rollback()
			db.Close()
			t.Fatal("collection fixture failed")
		}
	}
	if tx.Commit() != nil || db.Close() != nil {
		t.Fatal("fixture commit failed")
	}
	p = startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	namespaceStatus(t, client, f, "MKCOL", "/dav/Overflow/", nil, 507)
	hrefs := namespaceListing(t, client, f, "/dav/", "1")
	if len(hrefs) != 1025 || !slices.Contains(hrefs, "/dav/CHILD-0001/") || !slices.Contains(hrefs, "/dav/CHILD-1024/") {
		t.Fatal("bounded snapshot truncated known children")
	}
	body := `<d:propfind xmlns:d="DAV:"><d:prop><x:` + strings.Repeat("p", 2000) + ` xmlns:x="urn:fixture"/></d:prop></d:propfind>`
	req, _ := http.NewRequest("PROPFIND", f.origin+"/dav/", strings.NewReader(body))
	req.SetBasicAuth("FixtureUser", f.password)
	req.Header.Set("Depth", "1")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("response-bound request failed")
	}
	data, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != 500 || len(data) > 1024 {
		t.Fatal("oversized listing partially emitted multistatus")
	}
	namespaceStatus(t, client, f, "PROPFIND", "/dav/CHILD-0001/", nil, 207)
}

func TestNamespaceBoundsCanonicalStoredPath(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	parent := ""
	component := strings.Repeat("K", 80)
	for range 17 {
		parent += component + "/"
		namespaceStatus(t, client, f, "MKCOL", "/dav/"+strings.ReplaceAll(parent, "K", url.PathEscape("K")), nil, 201)
	}
	short := strings.ReplaceAll(parent, "K", "k")
	namespaceStatus(t, client, f, "MKCOL", "/dav/"+short+"child/", nil, 400)
	namespaceStatus(t, client, f, "PROPFIND", "/dav/"+short+"child/", nil, 404)
}

func namespaceStatus(t *testing.T, client *http.Client, f serverFixture, method, path string, body io.Reader, want int) {
	t.Helper()
	req, err := http.NewRequest(method, f.origin+path, body)
	if err != nil {
		t.Fatal("namespace request invalid")
	}
	req.SetBasicAuth("FixtureUser", f.password)
	req.Header.Set("Depth", "0")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("namespace request failed")
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("%s %s: status=%d want=%d", method, path, response.StatusCode, want)
	}
}

func TestNamespacePreservesNestedSpellingAcrossRestart(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	namespaceStatus(t, client, f, "MKCOL", "/dav/Projects/", nil, 201)
	namespaceStatus(t, client, f, "MKCOL", "/dav/projects/Specs%20%25%20%C3%A9/", nil, 201)
	namespaceStatus(t, client, f, "MKCOL", "/dav/projects/specs%20%25%20%C3%89/", nil, 405)
	namespaceStatus(t, client, f, "MKCOL", "/dav/Missing/Child/", nil, 409)
	namespaceStatus(t, client, f, "PROPFIND", "/dav/Missing/", nil, 404)
	want := []string{"/dav/Projects/", "/dav/Projects/Specs%20%25%20%C3%A9/"}
	if got := namespaceListing(t, client, f, "/dav/projects/", "1"); !slices.Equal(got, want) {
		t.Fatalf("canonical listing differs: %q", got)
	}
	p.stop()
	beforeDB, _ := os.ReadFile(filepath.Join(f.dir, "godav.sqlite"))
	beforeConfig, _ := os.ReadFile(f.config)
	out, stderr, code := command(t, f.dir, bin, "init", "config", "--config", f.config, "--username", "DifferentUser")
	if code != 0 || out != "godav: already initialized\n" || stderr != "" {
		t.Fatal("v2 bootstrap no-op rejected")
	}
	p = startServer(t, bin, f)
	if got := namespaceListing(t, client, f, "/dav/PROJECTS/", "1"); !slices.Equal(got, want) {
		t.Fatalf("reopened hierarchy differs: %q", got)
	}
	if got := namespaceListing(t, client, f, "/dav/projects/", "0"); !slices.Equal(got, []string{"/dav/Projects/"}) {
		t.Fatal("depth zero included children")
	}
	p.stop()
	afterDB, _ := os.ReadFile(filepath.Join(f.dir, "godav.sqlite"))
	afterConfig, _ := os.ReadFile(f.config)
	if !bytes.Equal(beforeDB, afterDB) || !bytes.Equal(beforeConfig, afterConfig) {
		t.Fatal("v2 open/discovery/no-op modified persistent state")
	}
}

func TestNamespaceConcurrentCaseCreatesStayUnique(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	start := make(chan struct{})
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for _, path := range []string{"/dav/Race/", "/dav/rACE/"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req, _ := http.NewRequest("MKCOL", f.origin+path, nil)
			req.SetBasicAuth("FixtureUser", f.password)
			response, err := client.Do(req)
			if err != nil {
				statuses <- 0
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			statuses <- response.StatusCode
		}()
	}
	close(start)
	wg.Wait()
	close(statuses)
	got := []int{}
	for code := range statuses {
		got = append(got, code)
	}
	slices.Sort(got)
	if !slices.Equal(got, []int{201, 405}) {
		t.Fatalf("competing creates returned %v", got)
	}
	hrefs := namespaceListing(t, client, f, "/dav/", "1")
	if len(hrefs) != 2 || !strings.EqualFold(hrefs[1], "/dav/Race/") {
		t.Fatal("competing creates changed namespace cardinality")
	}
}

func assertNamespaceSchemaRefused(t *testing.T, bin, statement string) {
	t.Helper()
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	p.stop()
	path := filepath.Join(f.dir, "godav.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal("fixture open failed")
	}
	// Keep these earlier namespace-constraint fixtures independently v2. A
	// rename must not accidentally fail only because v3's content FK changed.
	_, err = db.Exec(`DROP TABLE node_metadata; DROP TABLE file_contents; PRAGMA user_version=2;`)
	if err == nil {
		_, err = db.Exec(statement)
	}
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatal("fixture mutation failed")
	}
	before, _ := os.ReadFile(path)
	out, stderr, code := command(t, f.dir, bin, "init", "config", "--config", f.config, "--username", "DifferentUser")
	after, _ := os.ReadFile(path)
	if code != 1 || out != "" || stderr == "" || !bytes.Equal(before, after) {
		t.Fatal("unsafe namespace was accepted or changed")
	}
}

func TestNamespaceRejectsUnexpectedTriggerWithoutMutation(t *testing.T) {
	assertNamespaceSchemaRefused(t, development(t), `CREATE TRIGGER namespace_side_effect AFTER INSERT ON nodes BEGIN UPDATE accounts SET enabled=0; END;`)
}

func TestNamespaceCreatesAndListsCollection(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	response := davRequest(t, client, f, "MKCOL", "/dav/Projects/", true)
	response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatalf("MKCOL returned %d", response.StatusCode)
	}
	if got := namespaceListing(t, client, f, "/dav/", "1"); !slices.Equal(got, []string{"/dav/", "/dav/Projects/"}) {
		t.Fatalf("listing differs: %q", got)
	}
}

func TestNamespaceRejectsMissingUniquenessWithoutMutation(t *testing.T) {
	// Preserve all other constraints, independently of production SQL constants.
	assertNamespaceSchemaRefused(t, development(t), `ALTER TABLE nodes RENAME TO old_nodes;
 CREATE TABLE nodes(id TEXT PRIMARY KEY NOT NULL,parent_id TEXT REFERENCES nodes(id),name TEXT NOT NULL,name_key TEXT NOT NULL,created_at INTEGER NOT NULL,modified_at INTEGER NOT NULL,
 CHECK((id='root' AND parent_id IS NULL AND name='' AND name_key='') OR (id<>'root' AND parent_id IS NOT NULL AND octet_length(name) BETWEEN 1 AND 255 AND octet_length(name_key) BETWEEN 1 AND 255))) STRICT;
 INSERT INTO nodes SELECT * FROM old_nodes;
 DROP TABLE old_nodes;`)
}

func TestNamespaceRejectsMissingRootConstraintWithoutMutation(t *testing.T) {
	assertNamespaceSchemaRefused(t, development(t), `ALTER TABLE nodes RENAME TO old_nodes;
 CREATE TABLE nodes(id TEXT PRIMARY KEY NOT NULL,parent_id TEXT REFERENCES nodes(id),name TEXT NOT NULL,name_key TEXT NOT NULL,created_at INTEGER NOT NULL,modified_at INTEGER NOT NULL,UNIQUE(parent_id,name_key)) STRICT;
 INSERT INTO nodes SELECT * FROM old_nodes; DROP TABLE old_nodes;`)
}

func TestNamespaceUnicodePolicyAndLiteralEscapes(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		name string
		code int
	}{
		{"K", 201}, {"K", 405}, {"Σ", 201}, {"σ", 405}, {"ς", 405},
		{"Straße", 201}, {"STRASSE", 201}, {"STRAẞE", 405},
		{"é", 201}, {"e\u0301", 201}, {"space % &", 201}, {"%2e%2e", 201}, {"%2F", 201},
	} {
		namespaceStatus(t, client, f, "MKCOL", "/dav/"+url.PathEscape(tc.name)+"/", nil, tc.code)
	}
	want := []string{"/dav/", "/dav/K/", "/dav/%CE%A3/", "/dav/Stra%C3%9Fe/", "/dav/STRASSE/", "/dav/%C3%A9/", "/dav/e%CC%81/", "/dav/space%20%25%20&/", "/dav/%252e%252e/", "/dav/%252F/"}
	slices.Sort(want)
	if got := namespaceListing(t, client, f, "/dav/", "1"); !slices.Equal(got, want) {
		t.Fatalf("namespace policy/escape mismatch: %q", got)
	}
	p.stop()
	p = startServer(t, bin, f)
	defer p.stop()
	if got := namespaceListing(t, client, f, "/dav/", "1"); !slices.Equal(got, want) {
		t.Fatal("namespace policy changed after reopen")
	}
}

func TestNamespaceRejectsBodiesAndInvalidNamesWithoutCreation(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	for _, body := range []io.Reader{strings.NewReader("body"), io.NopCloser(strings.NewReader("chunked"))} {
		namespaceStatus(t, client, f, "MKCOL", "/dav/Body/", body, 415)
	}
	for _, name := range []string{"CON", "NUL.txt", "CLOCK$", "COM0", "LPT¹suffix", "trailing.", "trailing ", "colon:name", "bad\u0085control", "bad\uffff", strings.Repeat("x", 256)} {
		namespaceStatus(t, client, f, "MKCOL", "/dav/"+url.PathEscape(name)+"/", nil, 400)
	}
	for _, path := range []string{"/dav/../escape/", "/dav/%2e%2e/escape/", "/dav/a%2fb/", "/dav/a%5cb/", "/dav//double/", "/dav/%ff/"} {
		namespaceStatus(t, client, f, "MKCOL", path, nil, 400)
	}
	if got := namespaceListing(t, client, f, "/dav/", "1"); !slices.Equal(got, []string{"/dav/"}) {
		t.Fatal("rejected request created a node")
	}
	namespaceStatus(t, client, f, "MKCOL", "/dav/"+strings.Repeat("x", 255)+"/", nil, 201)
	namespaceStatus(t, client, f, "MKCOL", "/dav/COM10/", nil, 201)
	namespaceStatus(t, client, f, "MKCOL", "/dav/", nil, 405)
}

func TestNamespaceBoundsDepthAndPreservesDiscovery(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	path := "/dav/"
	for range 32 {
		path += "d/"
		namespaceStatus(t, client, f, "MKCOL", path, nil, 201)
	}
	namespaceStatus(t, client, f, "MKCOL", path+"d/", nil, 400)
	response := davRequest(t, client, f, "OPTIONS", path, true)
	response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Allow") != "OPTIONS, PROPFIND, PROPPATCH, MKCOL, GET, HEAD, PUT" || response.Header.Get("DAV") != "" {
		t.Fatal("collection capability advertisement is false")
	}
	for _, method := range []string{"PUT", "DELETE", "MOVE", "LOCK", "UNLOCK", "COPY"} {
		namespaceStatus(t, client, f, method, path, nil, 405)
	}
	if got := namespaceListing(t, client, f, path, "1"); len(got) != 1 {
		t.Fatal("unsupported operation created a child")
	}
}
