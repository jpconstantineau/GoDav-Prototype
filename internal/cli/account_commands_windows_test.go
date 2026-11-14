package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/localstate"
)

func accountFixture(t *testing.T, bin string) string {
	t.Helper()
	dir := privateFixture(t)
	_, _, _, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureOwner")
	if code != 0 {
		t.Fatal("account fixture initialization failed")
	}
	return dir
}

func fixtureBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("fixture state read failed")
	}
	return data
}

func accountSecret(t *testing.T, console, stdout, stderr string) string {
	t.Helper()
	matches := regexp.MustCompile(`Password: ([A-Za-z0-9_-]{32})`).FindAllStringSubmatch(console, -1)
	if len(matches) != 1 || stderr != "" {
		t.Fatal("account secret was not disclosed once privately")
	}
	secret := matches[0][1]
	if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) || strings.Contains(stdout+stderr, "$argon2id$") {
		t.Fatal("secret material escaped to routine output")
	}
	return secret
}

func TestAddUserPersistsAndRejectsDuplicateWithoutMutation(t *testing.T) {
	bin := development(t)
	dir := accountFixture(t, bin)
	config := filepath.Join(dir, "godav.yaml")
	beforeConfig := fixtureBytes(t, config)
	console, out, stderr, code := privateConsoleCommand(t, t.TempDir(), bin, "add", "user", "--config", config, "--username", "Second.User")
	if code != 0 || out != "godav: user added\n" {
		t.Fatal("add user failed")
	}
	password := accountSecret(t, console, out, stderr)
	db := filepath.Join(dir, "godav.sqlite")
	afterDB := fixtureBytes(t, db)
	if !bytes.Equal(beforeConfig, fixtureBytes(t, config)) || bytes.Contains(afterDB, []byte(password)) {
		t.Fatal("account add changed YAML or persisted password")
	}
	console, out, stderr, code = privateConsoleCommand(t, dir, bin, "add", "user", "--username", "sECOND.uSER")
	if code != 1 || out != "" || stderr != "godav: account already exists\n" || strings.Contains(console, "Password:") {
		t.Fatal("duplicate comparison-key account was not refused")
	}
	if sha256.Sum256(afterDB) != sha256.Sum256(fixtureBytes(t, db)) {
		t.Fatal("duplicate add changed database")
	}
	out, stderr, code = command(t, t.TempDir(), bin, "init", "config", "--config", config, "--username", "Ignored")
	if code != 0 || stderr != "" || out != "godav: already initialized\n" {
		t.Fatal("added account state failed reopen validation")
	}
}

func TestResetPasswordPersistsAndRejectsMissingWithoutMutation(t *testing.T) {
	bin := development(t)
	dir := accountFixture(t, bin)
	config := filepath.Join(dir, "godav.yaml")
	beforeConfig := fixtureBytes(t, config)
	db := filepath.Join(dir, "godav.sqlite")
	beforeDB := fixtureBytes(t, db)
	console, out, stderr, code := privateConsoleCommand(t, t.TempDir(), bin, "reset", "password", "--config", config, "--username", "fIXTUREoWNER")
	if code != 0 || out != "godav: password reset\n" {
		t.Fatal("password reset failed")
	}
	password := accountSecret(t, console, out, stderr)
	afterDB := fixtureBytes(t, db)
	if !bytes.Equal(beforeConfig, fixtureBytes(t, config)) || bytes.Contains(afterDB, []byte(password)) || bytes.Equal(beforeDB, afterDB) {
		t.Fatal("password reset persistence contract failed")
	}
	console, out, stderr, code = privateConsoleCommand(t, dir, bin, "reset", "password", "--username", "MissingAccount")
	if code != 1 || out != "" || stderr != "godav: account does not exist\n" || strings.Contains(console, "Password:") {
		t.Fatal("missing account reset was not refused")
	}
	if !bytes.Equal(afterDB, fixtureBytes(t, db)) {
		t.Fatal("missing reset changed database")
	}
	out, stderr, code = command(t, dir, bin, "init", "config", "--username", "Ignored")
	if code != 0 || stderr != "" || out != "godav: already initialized\n" {
		t.Fatal("reset account state failed reopen validation")
	}
}

func TestAccountCommandsRecoverAfterPrivateDeliveryFailure(t *testing.T) {
	normal := development(t)
	faulty := faultBuild(t, "godav_test_fail_console")
	for _, words := range [][]string{{"add", "user"}, {"reset", "password"}} {
		dir := accountFixture(t, normal)
		username := "FixtureOwner"
		if words[0] == "add" {
			username = "DeliveryUser"
		}
		yaml := fixtureBytes(t, filepath.Join(dir, "godav.yaml"))
		beforeDB := fixtureBytes(t, filepath.Join(dir, "godav.sqlite"))
		args := append(append([]string{}, words...), "--username", username)
		console, out, stderr, code := privateConsoleCommand(t, dir, faulty, args...)
		if code != 1 || out != "" || stderr != "godav: private delivery failed; retain state and explicitly reset password\n" || strings.Contains(console, "Password:") {
			t.Fatal("post-commit delivery failure contract was not followed")
		}
		afterDB := fixtureBytes(t, filepath.Join(dir, "godav.sqlite"))
		if bytes.Equal(beforeDB, afterDB) || !bytes.Equal(yaml, fixtureBytes(t, filepath.Join(dir, "godav.yaml"))) {
			t.Fatal("delivery failure did not retain committed account change and settings")
		}
		out, stderr, code = command(t, dir, normal, "init", "config", "--username", "Ignored")
		if code != 0 || stderr != "" || out != "godav: already initialized\n" || !bytes.Equal(afterDB, fixtureBytes(t, filepath.Join(dir, "godav.sqlite"))) {
			t.Fatal("recovery no-op reseeded or lost committed state")
		}
		console, out, stderr, code = privateConsoleCommand(t, dir, normal, "reset", "password", "--username", username)
		if code != 0 || out != "godav: password reset\n" {
			t.Fatal("explicit reset could not recover retained account")
		}
		accountSecret(t, console, out, stderr)
	}
}

func TestAccountCommandsRequireConsoleBeforeStateWrites(t *testing.T) {
	bin := development(t)
	for _, words := range [][]string{{"add", "user"}, {"reset", "password"}} {
		for _, flags := range []uint32{0x08000000, 0x00000008} {
			dir := accountFixture(t, bin)
			before := snapshotLocalState(t, dir)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			args := append(append([]string{}, words...), "--username", "FixtureOwner")
			cmd := exec.CommandContext(ctx, bin, args...)
			cmd.Dir = dir
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			err := cmd.Run()
			cancel()
			if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || out.Len() != 0 || stderr.String() != "godav: "+words[0]+" "+words[1]+" requires a private interactive console\n" {
				t.Fatal("headless account change was not refused")
			}
			assertLocalStateUnchanged(t, dir, before)
		}
	}
}

func TestAccountCommandsRespectExistingOwnerAndReleaseAfterFailure(t *testing.T) {
	bin := development(t)
	dir := accountFixture(t, bin)
	for _, intent := range []localstate.Intent{localstate.ReadOnly, localstate.WriteExisting} {
		before := snapshotLocalState(t, dir)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		state, err := localstate.OpenExisting(ctx, filepath.Join(dir, "godav.yaml"), intent)
		cancel()
		if err != nil {
			t.Fatal("ownership fixture opening failed")
		}
		defer state.Close()
		for _, words := range [][]string{{"add", "user"}, {"reset", "password"}} {
			console, out, stderr, code := privateConsoleCommand(t, dir, bin, append(append([]string{}, words...), "--username", "FixtureOwner")...)
			if code != 1 || out != "" || stderr != "godav: database is in use or ownership is unsafe\n" || strings.Contains(console, "Password:") {
				t.Fatal("account writer bypassed existing process ownership")
			}
		}
		if state.Close() != nil {
			t.Fatal("fixture ownership close failed")
		}
		assertLocalStateUnchanged(t, dir, before)
		console, out, stderr, code := privateConsoleCommand(t, dir, bin, "reset", "password", "--username", "FixtureOwner")
		if code != 0 {
			t.Fatal("released owner still blocked password reset")
		}
		accountSecret(t, console, out, stderr)
	}
}

func TestAccountCommandsRefuseUnsafeCreationInheritance(t *testing.T) {
	bin := development(t)
	source := accountFixture(t, bin)
	for _, inheritance := range []string{"", "CI"} {
		dir := privateFixtureWithInheritance(t, inheritance)
		for _, name := range []string{"godav.yaml", "godav.sqlite", "godav.sqlite.lock"} {
			writeRestrictedFixtureFile(t, filepath.Join(dir, name), fixtureBytes(t, filepath.Join(source, name)))
		}
		before := snapshotLocalState(t, dir)
		for _, words := range [][]string{{"add", "user"}, {"reset", "password"}} {
			console, out, stderr, code := privateConsoleCommand(t, dir, bin, append(append([]string{}, words...), "--username", "FixtureOwner")...)
			if code != 1 || out != "" || stderr != "godav: database is in use or ownership is unsafe\n" || strings.Contains(console, "Password:") {
				t.Fatal("account writer accepted unsafe journal inheritance")
			}
			assertLocalStateUnchanged(t, dir, before)
		}
	}
}

func TestAccountCommandsRefuseInvalidStateWithoutRecovery(t *testing.T) {
	bin := development(t)
	source := accountFixture(t, bin)
	for _, state := range []string{"missing-yaml", "missing-db", "corrupt-db", "wal-header", "orphan-journal", "broad-yaml"} {
		dir := privateFixture(t)
		for _, name := range []string{"godav.yaml", "godav.sqlite", "godav.sqlite.lock"} {
			if state == "missing-yaml" && name == "godav.yaml" || state == "missing-db" && name == "godav.sqlite" {
				continue
			}
			data := fixtureBytes(t, filepath.Join(source, name))
			if name == "godav.sqlite" {
				if state == "corrupt-db" {
					data = []byte("not a database")
				}
				if state == "wal-header" {
					data[18], data[19] = 2, 2
				}
			}
			writeFixtureFileWithACL(t, filepath.Join(dir, name), data, state == "broad-yaml" && name == "godav.yaml")
		}
		if state == "orphan-journal" {
			writeRestrictedFixtureFile(t, filepath.Join(dir, "godav.sqlite-journal"), []byte("fixture partial state"))
		}
		before := snapshotLocalState(t, dir)
		for _, words := range [][]string{{"add", "user"}, {"reset", "password"}} {
			console, out, stderr, code := privateConsoleCommand(t, dir, bin, append(append([]string{}, words...), "--username", "FixtureOwner")...)
			if code != 1 || out != "" || stderr == "" || strings.Contains(console, "Password:") {
				t.Fatal("account command adopted invalid state")
			}
			assertLocalStateUnchanged(t, dir, before)
		}
	}
}

func TestConcurrentAddDisclosesOnlyOneCommittedPassword(t *testing.T) {
	bin := development(t)
	dir := accountFixture(t, bin)
	type result struct {
		console, out, stderr string
		code                 int
	}
	results := make(chan result, 2)
	var start sync.WaitGroup
	start.Add(1)
	for range 2 {
		go func() {
			r := result{code: -1}
			defer func() { results <- r }()
			start.Wait()
			console, out, stderr, code := privateConsoleCommand(t, dir, bin, "add", "user", "--username", "RaceUser")
			r = result{console, out, stderr, code}
		}()
	}
	start.Done()
	successes, disclosures := 0, 0
	for range 2 {
		r := <-results
		disclosures += strings.Count(r.console, "Password:")
		if r.code == 0 {
			successes++
			accountSecret(t, r.console, r.out, r.stderr)
		} else if r.code != 1 || r.out != "" || (r.stderr != "godav: account already exists\n" && r.stderr != "godav: database is in use or ownership is unsafe\n") {
			t.Fatal("competing account creation failed unexpectedly")
		}
	}
	if successes != 1 || disclosures != 1 {
		t.Fatal("competing add did not create exactly one account")
	}
}
