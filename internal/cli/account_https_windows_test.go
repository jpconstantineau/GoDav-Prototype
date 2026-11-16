package cli_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/localstate"
)

// This narrowly approved seam observes only authenticated identity through the
// exact production lookup/verification path. Call it with the server stopped;
// do not inspect private SQL or add a transport endpoint solely for this test.
type accountIdentity struct{ id, name, created string }

func authenticatedAccountIdentity(t *testing.T, f serverFixture, username, password string) accountIdentity {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, err := localstate.OpenExisting(ctx, f.config, localstate.ReadOnly)
	if err != nil {
		t.Fatal("offline principal fixture could not open existing state")
	}
	defer state.Close()
	account, found, err := state.Accounts().LookupEnabled(ctx, username)
	if err != nil || !found || !account.VerifyPassword(password) || account.ID == "" {
		t.Fatal("production principal lookup did not authenticate fixture")
	}
	identity := accountIdentity{account.ID, account.Name, account.Created}
	if state.Close() != nil {
		t.Fatal("offline principal fixture did not close")
	}
	return identity
}

// serverFixture adds a TLS directory, so snapshot only the account state's three
// files. Snapshot the lock before serving and after stopping, never while owned.
func accountStateSnapshot(t *testing.T, f serverFixture) map[string]localStateFile {
	t.Helper()
	entries, err := os.ReadDir(f.dir)
	if err != nil || len(entries) != 4 {
		t.Fatal("account fixture gained or lost directory entries")
	}
	for _, entry := range entries {
		if entry.Name() != "godav.yaml" && entry.Name() != "godav.sqlite" && entry.Name() != "godav.sqlite.lock" && entry.Name() != "tls" {
			t.Fatal("account fixture contains unexpected sidecar state")
		}
	}
	result := make(map[string]localStateFile)
	for _, name := range []string{"godav.yaml", "godav.sqlite", "godav.sqlite.lock"} {
		path := filepath.Join(f.dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal("account state file inspection failed")
		}
		result[name] = localStateFile{fixtureBytes(t, path), info, localStateSecurity(t, path)}
	}
	return result
}

func assertAccountStateSnapshot(t *testing.T, f serverFixture, before map[string]localStateFile) {
	t.Helper()
	for name, after := range accountStateSnapshot(t, f) {
		old := before[name]
		if !bytes.Equal(old.data, after.data) || old.security != after.security || !os.SameFile(old.info, after.info) {
			t.Fatal("account state changed unexpectedly")
		}
	}
}

func assertAccountHTTPS(t *testing.T, client *http.Client, f serverFixture, username, password string, status int) {
	t.Helper()
	request, err := http.NewRequest("OPTIONS", f.origin+"/dav/", nil)
	if err != nil {
		t.Fatal("account HTTPS fixture request creation failed")
	}
	request.SetBasicAuth(username, password)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("account HTTPS fixture request failed")
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(body) > 4096 || response.StatusCode != status || bytes.Contains(body, []byte(password)) {
		t.Fatal("account HTTPS response violated authentication contract")
	}
	if status == http.StatusUnauthorized && response.Header.Get("WWW-Authenticate") != `Basic realm="GoDav"` {
		t.Fatal("replaced or missing credentials did not receive the normal challenge")
	}
}

func assertAccountServerLogs(t *testing.T, p *serverProcess, credentials ...[2]string) {
	t.Helper()
	logs := p.logs.String()
	if p.out.String() != "" || strings.Contains(logs, "$argon2id$") {
		t.Fatal("account server emitted unsafe routine output")
	}
	for _, credential := range credentials {
		encoded := base64.StdEncoding.EncodeToString([]byte(credential[0] + ":" + credential[1]))
		if strings.Contains(logs, credential[1]) || strings.Contains(logs, encoded) {
			t.Fatal("account credential entered server logs")
		}
	}
}

func TestAccountAdministrationAcrossHTTPSRestart(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	originalYAML := fixtureBytes(t, f.config)
	originalOwner := authenticatedAccountIdentity(t, f, "fixtureuser", f.password)

	console, out, stderr, code := privateConsoleCommand(t, t.TempDir(), bin, "add", "user", "--config", f.config, "--username", "Added.User")
	if code != 0 || out != "godav: user added\n" {
		t.Fatal("offline add could not prepare HTTPS account")
	}
	addedPassword := accountSecret(t, console, out, stderr)
	addedIdentity := authenticatedAccountIdentity(t, f, "aDDED.uSER", addedPassword)
	if addedIdentity.id == originalOwner.id || authenticatedAccountIdentity(t, f, "FixtureUser", f.password) != originalOwner {
		t.Fatal("adding an account changed or reused existing identity")
	}

	p := startServer(t, bin, f)
	// The first explicit server start prepares v2 collections. Establish the
	// unchanged-state baseline only after that separate, intentional migration.
	p.stop()
	assertAccountServerLogs(t, p, [2]string{"FixtureUser", f.password}, [2]string{"Added.User", addedPassword})
	beforeServing := accountStateSnapshot(t, f)
	p = startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	assertAccountHTTPS(t, client, f, "FixtureUser", f.password, http.StatusOK)
	assertAccountHTTPS(t, client, f, "added.user", addedPassword, http.StatusOK)
	for _, command := range [][]string{{"add", "user", "--username", "BlockedUser"}, {"reset", "password", "--username", "Added.User"}} {
		args := append(append([]string{}, command...), "--config", f.config)
		console, out, stderr, code = privateConsoleCommand(t, t.TempDir(), bin, args...)
		if code != 1 || out != "" || stderr != "godav: database is in use or ownership is unsafe\n" || strings.Contains(console, "Password:") {
			t.Fatal("live server failed to exclude a private-console account writer")
		}
	}
	assertAccountHTTPS(t, client, f, "FixtureUser", f.password, http.StatusOK)
	assertAccountHTTPS(t, client, f, "Added.User", addedPassword, http.StatusOK)
	client.CloseIdleConnections()
	p.stop()
	assertAccountServerLogs(t, p, [2]string{"FixtureUser", f.password}, [2]string{"Added.User", addedPassword}, [2]string{"added.user", addedPassword})
	assertAccountStateSnapshot(t, f, beforeServing)

	console, out, stderr, code = privateConsoleCommand(t, t.TempDir(), bin, "reset", "password", "--config", f.config, "--username", "aDDED.uSER")
	if code != 0 || out != "godav: password reset\n" {
		t.Fatal("offline reset failed after server termination")
	}
	newPassword := accountSecret(t, console, out, stderr)
	if newPassword == addedPassword || authenticatedAccountIdentity(t, f, "added.user", newPassword) != addedIdentity || authenticatedAccountIdentity(t, f, "FixtureUser", f.password) != originalOwner {
		t.Fatal("password reset changed stable identity or another account")
	}
	if !bytes.Equal(originalYAML, fixtureBytes(t, f.config)) {
		t.Fatal("account changes rewrote settings")
	}

	beforeRefusals := accountStateSnapshot(t, f)
	for _, refusal := range []struct {
		args    []string
		message string
	}{
		{[]string{"add", "user", "--username", "ADDED.USER"}, "godav: account already exists\n"},
		{[]string{"reset", "password", "--username", "MissingUser"}, "godav: account does not exist\n"},
	} {
		args := append(append([]string{}, refusal.args...), "--config", f.config)
		console, out, stderr, code = privateConsoleCommand(t, t.TempDir(), bin, args...)
		if code != 1 || out != "" || stderr != refusal.message || strings.Contains(console, "Password:") {
			t.Fatal("duplicate/missing account refusal failed")
		}
		assertAccountStateSnapshot(t, f, beforeRefusals)
	}

	p = startServer(t, bin, f)
	client = fixtureClient(f)
	defer client.CloseIdleConnections()
	assertAccountHTTPS(t, client, f, "Added.User", addedPassword, http.StatusUnauthorized)
	assertAccountHTTPS(t, client, f, "Added.User", newPassword, http.StatusOK)
	assertAccountHTTPS(t, client, f, "FixtureUser", f.password, http.StatusOK)
	assertAccountHTTPS(t, client, f, "MissingUser", newPassword, http.StatusUnauthorized)
	client.CloseIdleConnections()
	p.stop()
	assertAccountServerLogs(t, p, [2]string{"Added.User", addedPassword}, [2]string{"Added.User", newPassword}, [2]string{"FixtureUser", f.password}, [2]string{"MissingUser", newPassword})
	assertAccountStateSnapshot(t, f, beforeRefusals)
}
