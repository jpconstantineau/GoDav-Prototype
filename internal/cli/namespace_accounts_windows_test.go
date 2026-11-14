package cli_test

import (
	"bytes"
	"maps"
	"net/http"
	"strings"
	"testing"
)

func TestNamespaceSurvivesOfflineAccountAdministration(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	originalYAML := fixtureBytes(t, f.config)
	originalPassword := f.password
	originalOwner := authenticatedAccountIdentity(t, f, "FixtureUser", originalPassword)
	console, out, stderr, code := privateConsoleCommand(t, t.TempDir(), bin, "add", "user", "--config", f.config, "--username", "Unrelated.User")
	if code != 0 || out != "godav: user added\n" {
		t.Fatal("unrelated account fixture failed")
	}
	unrelatedPassword := accountSecret(t, console, out, stderr)
	unrelatedIdentity := authenticatedAccountIdentity(t, f, "Unrelated.User", unrelatedPassword)

	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	for _, path := range []string{
		"/dav/MixEd%20%26%20Notes/",
		"/dav/mixed%20%26%20notes/Child%20%25/",
		"/dav/MIXED%20%26%20NOTES/child%20%25/Leaf%20%CE%A9/",
		"/dav/UnTouched/",
	} {
		namespaceStatus(t, client, f, "MKCOL", path, nil, http.StatusCreated)
	}
	// Known canonical hrefs and names are independent expectations. The shared
	// parser also asserts collection/property statuses and UTC-second timestamps.
	snapshots := []struct {
		path     string
		names    map[string]string
		metadata map[string]collectionMetadata
	}{
		{"/dav/", map[string]string{"/dav/": "", "/dav/MixEd%20&%20Notes/": "MixEd & Notes", "/dav/UnTouched/": "UnTouched"}, nil},
		{"/dav/mixed%20%26%20notes/", map[string]string{"/dav/MixEd%20&%20Notes/": "MixEd & Notes", "/dav/MixEd%20&%20Notes/Child%20%25/": "Child %"}, nil},
		{"/dav/mixed%20%26%20notes/child%20%25/", map[string]string{"/dav/MixEd%20&%20Notes/Child%20%25/": "Child %", "/dav/MixEd%20&%20Notes/Child%20%25/Leaf%20%CE%A9/": "Leaf Ω"}, nil},
		{"/dav/mixed%20%26%20notes/child%20%25/leaf%20%CF%89/", map[string]string{"/dav/MixEd%20&%20Notes/Child%20%25/Leaf%20%CE%A9/": "Leaf Ω"}, nil},
		{"/dav/untouched/", map[string]string{"/dav/UnTouched/": "UnTouched"}, nil},
	}
	for i := range snapshots {
		snapshot := &snapshots[i]
		snapshot.metadata = namespaceMetadata(t, client, f, snapshot.path, "1")
		if len(snapshot.metadata) != len(snapshot.names) {
			t.Fatal("initial canonical hierarchy differs")
		}
		for href, name := range snapshot.names {
			metadata, found := snapshot.metadata[href]
			if !found || metadata.display != name {
				t.Fatal("initial collection spelling differs")
			}
		}
	}
	for _, command := range [][]string{{"add", "user", "--username", "Blocked.User"}, {"reset", "password", "--username", "FixtureUser"}} {
		args := append(append([]string{}, command...), "--config", f.config)
		console, out, stderr, code = privateConsoleCommand(t, t.TempDir(), bin, args...)
		if code != 1 || out != "" || stderr != "godav: database is in use or ownership is unsafe\n" || strings.Contains(console, "Password:") {
			t.Fatal("live namespace server failed to exclude a private-console account writer")
		}
	}
	assertAccountHTTPS(t, client, f, "FixtureUser", originalPassword, http.StatusOK)
	assertAccountHTTPS(t, client, f, "Unrelated.User", unrelatedPassword, http.StatusOK)
	client.CloseIdleConnections()
	p.stop() // The executable fixture terminates and joins, not graceful shutdown.
	assertAccountServerLogs(t, p, [2]string{"FixtureUser", originalPassword}, [2]string{"Unrelated.User", unrelatedPassword})
	if authenticatedAccountIdentity(t, f, "FixtureUser", originalPassword) != originalOwner || authenticatedAccountIdentity(t, f, "Unrelated.User", unrelatedPassword) != unrelatedIdentity {
		t.Fatal("namespace preparation changed account identity")
	}

	console, out, stderr, code = privateConsoleCommand(t, t.TempDir(), bin, "add", "user", "--config", f.config, "--username", "Added.User")
	if code != 0 || out != "godav: user added\n" {
		t.Fatal("offline add failed with existing collections")
	}
	addedPassword := accountSecret(t, console, out, stderr)
	addedIdentity := authenticatedAccountIdentity(t, f, "added.user", addedPassword)
	if addedIdentity.id == originalOwner.id || addedIdentity.id == unrelatedIdentity.id {
		t.Fatal("offline add reused account identity")
	}
	console, out, stderr, code = privateConsoleCommand(t, t.TempDir(), bin, "reset", "password", "--config", f.config, "--username", "fIXTUREuSER")
	if code != 0 || out != "godav: password reset\n" {
		t.Fatal("offline reset failed with existing collections")
	}
	f.password = accountSecret(t, console, out, stderr)
	if f.password == originalPassword || authenticatedAccountIdentity(t, f, "FixtureUser", f.password) != originalOwner || authenticatedAccountIdentity(t, f, "Unrelated.User", unrelatedPassword) != unrelatedIdentity || authenticatedAccountIdentity(t, f, "Added.User", addedPassword) != addedIdentity {
		t.Fatal("offline administration changed stable identity or unrelated credentials")
	}
	if !bytes.Equal(originalYAML, fixtureBytes(t, f.config)) {
		t.Fatal("offline administration changed settings")
	}
	beforeRestart := accountStateSnapshot(t, f)
	p = startServer(t, bin, f)
	client = fixtureClient(f)
	defer client.CloseIdleConnections()
	assertAccountHTTPS(t, client, f, "FixtureUser", originalPassword, http.StatusUnauthorized)
	assertAccountHTTPS(t, client, f, "FixtureUser", f.password, http.StatusOK)
	assertAccountHTTPS(t, client, f, "Added.User", addedPassword, http.StatusOK)
	assertAccountHTTPS(t, client, f, "Unrelated.User", unrelatedPassword, http.StatusOK)
	assertAccountHTTPS(t, client, f, "Blocked.User", addedPassword, http.StatusUnauthorized)
	for _, snapshot := range snapshots {
		if !maps.Equal(namespaceMetadata(t, client, f, snapshot.path, "1"), snapshot.metadata) {
			t.Fatal("offline administration changed canonical hierarchy or collection metadata")
		}
	}
	client.CloseIdleConnections()
	p.stop()
	assertAccountServerLogs(t, p, [2]string{"FixtureUser", originalPassword}, [2]string{"FixtureUser", f.password}, [2]string{"Added.User", addedPassword}, [2]string{"Unrelated.User", unrelatedPassword}, [2]string{"Blocked.User", addedPassword})
	assertAccountStateSnapshot(t, f, beforeRestart)
}
