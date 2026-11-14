package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestInitConfigWithoutConsoleFailsBeforeStateWrites(t *testing.T) {
	bin := development(t)
	for _, flags := range []uint32{0x08000000, 0x00000008} { // CREATE_NO_WINDOW, DETACHED_PROCESS
		dir := privateFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "init", "config", "--config", filepath.Join(dir, "godav.yaml"), "--username", "FixtureUser")
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || stdout.Len() != 0 || stderr.String() != "godav: init config requires a private interactive console\n" {
			stage := "unrecognized"
			for _, allowed := range []string{"unsafe settings path", "database directory must be private", "settings directory must be private", "init config requires a private interactive console"} {
				if stderr.String() == "godav: "+allowed+"\n" {
					stage = allowed
				}
			}
			t.Fatalf("noninteractive refusal mismatch: stage=%s", stage)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("initialization without a console modified fixture state")
		}
	}
}

func TestInitConfigExistingStateSurvivesNoninteractiveRestart(t *testing.T) {
	bin := development(t)
	dir := privateFixture(t)
	_, _, _, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
	if code != 0 {
		t.Fatal("fixture initialization failed")
	}
	config := filepath.Join(dir, "godav.yaml")
	db := filepath.Join(dir, "godav.sqlite")
	beforeConfig, err := os.ReadFile(config)
	if err != nil {
		t.Fatal("settings fixture unreadable")
	}
	beforeDB, err := os.ReadFile(db)
	if err != nil {
		t.Fatal("database fixture unreadable")
	}
	out, stderr, code := command(t, t.TempDir(), bin, "init", "config", "--config", config, "--username", "DifferentUser")
	if code != 0 || stderr != "" || out != "godav: already initialized\n" {
		t.Fatal("existing valid state did not permit noninteractive no-op")
	}
	afterConfig, err := os.ReadFile(config)
	if err != nil {
		t.Fatal("settings missing after no-op")
	}
	afterDB, err := os.ReadFile(db)
	if err != nil || sha256.Sum256(beforeDB) != sha256.Sum256(afterDB) || !bytes.Equal(beforeConfig, afterConfig) {
		t.Fatal("no-op mutated persistent settings or accounts")
	}
}

func privateFixture(t *testing.T) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal("fixture token lookup failed")
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		t.Fatal("fixture descriptor failed")
	}
	dir := filepath.Join(t.TempDir(), "private")
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal("fixture path failed")
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if windows.CreateDirectory(path, &sa) != nil {
		t.Fatal("private fixture creation failed")
	}
	return dir
}

func TestInitConfigFreshDisclosesOnlyToPrivateConsole(t *testing.T) {
	bin := development(t)
	dir := privateFixture(t)
	console, stdout, stderr, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
	if code != 0 || stderr != "" || stdout != "godav: initialized; configure TLS before serving\n" {
		stage := "unrecognized"
		for _, allowed := range []string{"unsafe settings path", "database directory must be private", "unsafe database path", "database is in use or ownership is unsafe", "database initialization failed; retain state for recovery", "settings publication failed; retain database and reconstruct settings", "init config requires a private interactive console"} {
			if stderr == "godav: "+allowed+"\n" {
				stage = allowed
			}
		}
		t.Fatalf("fresh initialization failed: exit=%d stage=%s routineSuccess=%v consoleSuccess=%v", code, stage, stdout == "godav: initialized; configure TLS before serving\n", strings.Contains(console, "godav: initialized; configure TLS before serving"))
	}
	secrets := regexp.MustCompile(`Initial password: ([A-Za-z0-9_-]{32})`).FindAllStringSubmatch(console, -1)
	if len(secrets) != 1 {
		t.Fatal("fresh initialization did not disclose exactly one private password")
	}
	settings, err := os.ReadFile(filepath.Join(dir, "godav.yaml"))
	if err != nil || !strings.Contains(string(settings), "database_path: 'godav.sqlite'\n") || strings.Contains(string(settings), "FixtureUser") || bytes.Contains(settings, []byte(secrets[0][1])) {
		t.Fatal("fresh initialization settings missing or contain account material")
	}
	db, err := os.ReadFile(filepath.Join(dir, "godav.sqlite"))
	if err != nil || !bytes.HasPrefix(db, []byte("SQLite format 3\x00")) || bytes.Contains(db, []byte(secrets[0][1])) {
		t.Fatal("fresh initialization did not persist a SQLite store without plaintext password")
	}
}

const bootstrapSettings = "config_version: 1\nlisten_address: '127.0.0.1:8443'\npublic_origin: 'https://localhost:8443'\ndav_prefix: '/dav/'\ndatabase_path: 'godav.sqlite'\ntls:\n  cert_file: 'tls/server.crt'\n  key_file: 'tls/server.key'\nauthentication:\n  method: basic\nlimits:\n  max_file_bytes: 33554432\n"

func faultBuild(t *testing.T, tag string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "godav.exe")
	_, _, code := command(t, root(t), "go", "build", "-tags", tag, "-o", bin, "./cmd/godav")
	if code != 0 {
		t.Fatal("fault fixture build failed")
	}
	return bin
}

func TestInitConfigPublicationFailurePreservesCommittedAccount(t *testing.T) {
	faulty := faultBuild(t, "godav_test_fail_yaml")
	normal := development(t)
	dir := privateFixture(t)
	console, out, stderr, code := privateConsoleCommand(t, dir, faulty, "init", "config", "--username", "FixtureUser")
	if code != 1 || out != "" || stderr != "godav: settings publication failed; retain database and reconstruct settings\n" || strings.Contains(console, "Initial password:") {
		t.Fatal("publication failure did not preserve private-delivery ordering")
	}
	if _, err := os.Stat(filepath.Join(dir, "godav.yaml")); !os.IsNotExist(err) {
		t.Fatal("failed publication exposed settings")
	}
	dbPath := filepath.Join(dir, "godav.sqlite")
	before, err := os.ReadFile(dbPath)
	if err != nil || len(before) == 0 {
		t.Fatal("committed database was not preserved")
	}
	_, _, code = command(t, dir, normal, "init", "config", "--username", "Replacement")
	if code != 1 {
		t.Fatal("retry silently reseeded missing-settings state")
	}
	if os.WriteFile(filepath.Join(dir, "godav.yaml"), []byte(bootstrapSettings), 0600) != nil {
		t.Fatal("fixture settings reconstruction failed")
	}
	out, stderr, code = command(t, dir, normal, "init", "config", "--username", "FixtureUser")
	if code != 0 || stderr != "" || out != "godav: already initialized\n" {
		t.Fatal("committed account could not be validated after explicit settings reconstruction")
	}
	after, err := os.ReadFile(dbPath)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("recovery checks changed committed database")
	}
}

func TestInitConfigDeliveryFailurePreservesCommittedState(t *testing.T) {
	faulty := faultBuild(t, "godav_test_fail_console")
	normal := development(t)
	dir := privateFixture(t)
	console, out, stderr, code := privateConsoleCommand(t, dir, faulty, "init", "config", "--username", "FixtureUser")
	if code != 1 || out != "" || stderr != "godav: private delivery failed; retain state and explicitly reset password\n" || strings.Contains(console, "Initial password:") {
		t.Fatal("private delivery failure did not preserve committed-state ordering")
	}
	configPath, dbPath := filepath.Join(dir, "godav.yaml"), filepath.Join(dir, "godav.sqlite")
	beforeConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal("committed settings lost after delivery failure")
	}
	beforeDB, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal("committed database lost after delivery failure")
	}
	console, out, stderr, code = privateConsoleCommand(t, dir, normal, "init", "config", "--username", "Replacement")
	if code != 0 || stderr != "" || out != "godav: already initialized\n" || strings.Contains(console, "Initial password:") {
		t.Fatal("retry disclosed or regenerated credentials after delivery failure")
	}
	afterConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal("settings unavailable after retry")
	}
	afterDB, err := os.ReadFile(dbPath)
	if err != nil || !bytes.Equal(beforeConfig, afterConfig) || sha256.Sum256(beforeDB) != sha256.Sum256(afterDB) {
		t.Fatal("retry changed committed settings or account")
	}
}

func TestInitConfigRejectsInvalidSettingsWithoutMutation(t *testing.T) {
	bin := development(t)
	for _, invalid := range []string{
		"", "not: [valid", bootstrapSettings + "password: not-a-secret\n",
		bootstrapSettings + "config_version: 1\n", bootstrapSettings + "---\n{}\n",
		strings.Replace(bootstrapSettings, "config_version: 1", "config_version: '1'", 1),
		strings.Replace(bootstrapSettings, "config_version: 1", "config_version: 2", 1),
		strings.Replace(bootstrapSettings, "method: basic", "method: true", 1),
		strings.Replace(bootstrapSettings, "method: basic", "method: !custom basic", 1),
		strings.Replace(bootstrapSettings, "method: basic", "method: &auth basic", 1),
		strings.Replace(bootstrapSettings, "127.0.0.1:8443", "0.0.0.0:8443", 1),
		strings.Replace(bootstrapSettings, "https://localhost:8443", "http://localhost:8443", 1),
		strings.Replace(bootstrapSettings, "https://localhost:8443", "https://other.invalid:8443", 1),
		strings.Replace(bootstrapSettings, "database_path: 'godav.sqlite'", "database_path: 'godav.yaml'", 1),
		strings.Repeat("#", 65537),
	} {
		dir := privateFixture(t)
		_, _, _, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
		if code != 0 {
			t.Fatal("fixture initialization failed")
		}
		config := filepath.Join(dir, "godav.yaml")
		if os.WriteFile(config, []byte(invalid), 0600) != nil {
			t.Fatal("invalid fixture write failed")
		}
		dbPath := filepath.Join(dir, "godav.sqlite")
		before, err := os.ReadFile(dbPath)
		if err != nil {
			t.Fatal("fixture read failed")
		}
		out, stderr, code := command(t, dir, bin, "init", "config", "--username", "FixtureUser")
		if code != 1 || out != "" || stderr != "godav: invalid settings\n" {
			t.Fatal("invalid settings were not rejected safely")
		}
		after, err := os.ReadFile(dbPath)
		gotConfig, configErr := os.ReadFile(config)
		if err != nil || configErr != nil || sha256.Sum256(before) != sha256.Sum256(after) || string(gotConfig) != invalid {
			t.Fatal("invalid-state rejection modified persistent data")
		}
	}
}

func TestInitConfigNeverAdoptsOrphanDatabaseState(t *testing.T) {
	bin := development(t)
	for _, name := range []string{"godav.sqlite", "godav.sqlite-wal", "godav.sqlite-journal", "godav.sqlite-shm"} {
		dir := privateFixture(t)
		path := filepath.Join(dir, name)
		if os.WriteFile(path, []byte("preserve partial fixture"), 0600) != nil {
			t.Fatal("partial fixture setup failed")
		}
		console, out, stderr, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
		if code != 1 || out != "" || stderr == "" || strings.Contains(console, "Initial password:") {
			t.Fatal("orphan state was not refused")
		}
		got, err := os.ReadFile(path)
		entries, readErr := os.ReadDir(dir)
		if err != nil || readErr != nil || string(got) != "preserve partial fixture" || len(entries) != 1 {
			t.Fatal("orphan-state refusal mutated fixture")
		}
	}
}

func TestInitConfigRejectsMissingCorruptAndFutureDatabase(t *testing.T) {
	bin := development(t)
	for _, kind := range []string{"missing", "corrupt", "future", "excessive-verifier"} {
		dir := privateFixture(t)
		_, _, _, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
		if code != 0 {
			t.Fatal("fixture initialization failed")
		}
		path := filepath.Join(dir, "godav.sqlite")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("fixture database read failed")
		}
		switch kind {
		case "missing":
			if os.Remove(path) != nil {
				t.Fatal("missing fixture preparation failed")
			}
		case "corrupt":
			data = []byte("not a SQLite database")
		case "future":
			// SQLite's documented public file header stores user_version at 60.
			// This fixture does not assert or depend on application table layout.
			binary.BigEndian.PutUint32(data[60:64], 2)
		case "excessive-verifier":
			old := []byte("$argon2id$v=19$m=19456,t=2,p=1$")
			if bytes.Count(data, old) != 1 {
				t.Fatal("encoded-verifier fixture not found")
			}
			data = bytes.Replace(data, old, []byte("$argon2id$v=19$m=99999,t=2,p=1$"), 1)
		}
		if kind != "missing" && os.WriteFile(path, data, 0600) != nil {
			t.Fatal("invalid database fixture preparation failed")
		}
		out, stderr, code := command(t, dir, bin, "init", "config", "--username", "Replacement")
		if code != 1 || out != "" || stderr == "" {
			t.Fatal("invalid database accepted or replaced")
		}
		got, err := os.ReadFile(path)
		if kind == "missing" {
			if !os.IsNotExist(err) {
				t.Fatal("missing database was recreated")
			}
		} else if err != nil || !bytes.Equal(got, data) {
			t.Fatal("invalid database was mutated")
		}
	}
}

func TestInitConfigRespectsStableOwnershipAndReleasesIt(t *testing.T) {
	bin := development(t)
	dir := privateFixture(t)
	lockPath := filepath.Join(dir, "godav.sqlite.lock")
	if os.WriteFile(lockPath, nil, 0600) != nil {
		t.Fatal("lock fixture creation failed")
	}
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal("lock fixture inspection failed")
	}
	p, _ := windows.UTF16PtrFromString(lockPath)
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal("OS ownership fixture acquisition failed")
	}
	defer func() {
		if h != windows.InvalidHandle {
			windows.CloseHandle(h)
		}
	}()
	console, out, stderr, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
	if code != 1 || out != "" || stderr == "" || strings.Contains(console, "Initial password:") {
		t.Fatal("initializer ignored OS-held ownership")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("busy initializer changed fixture state")
	}
	if windows.CloseHandle(h) != nil {
		t.Fatal("OS fixture ownership release failed")
	}
	h = windows.InvalidHandle
	_, _, _, code = privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
	if code != 0 {
		t.Fatal("initializer did not acquire released ownership")
	}
	after, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(before, after) || after.Size() != 0 {
		t.Fatal("stable ownership artifact was replaced or contains data")
	}
	_, _, code = command(t, dir, bin, "init", "config", "--username", "FixtureUser")
	if code != 0 {
		t.Fatal("initializer leaked ownership after exit")
	}
}

func TestInitConfigCompetingProcessesDiscloseOnlyOnePassword(t *testing.T) {
	bin := development(t)
	dir := privateFixture(t)
	type result struct {
		console, stdout, stderr string
		code                    int
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			<-start
			console, out, stderr, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
			results <- result{console, out, stderr, code}
		})
	}
	close(start)
	group.Wait()
	close(results)
	disclosures, initializations := 0, 0
	for r := range results {
		disclosures += strings.Count(r.console, "Initial password:")
		if r.code == 0 && r.stdout == "godav: initialized; configure TLS before serving\n" && r.stderr == "" {
			initializations++
		} else if r.code != 1 && !(r.code == 0 && r.stdout == "godav: already initialized\n") {
			t.Fatal("competing initializer returned unexpected status")
		}
	}
	if disclosures != 1 || initializations != 1 {
		t.Fatal("competing initializers did not yield exactly one committed credential disclosure")
	}
	_, _, code := command(t, dir, bin, "init", "config", "--username", "FixtureUser")
	if code != 0 {
		t.Fatal("competing initialization did not leave valid restart state")
	}
}

func TestInitConfigRejectsUnsafeTLSPathBeforeProvisioning(t *testing.T) {
	bin := development(t)
	dir := privateFixture(t)
	tls := filepath.Join(dir, "tls")
	if os.Mkdir(tls, 0700) != nil {
		t.Fatal("TLS fixture directory failed")
	}
	cert := filepath.Join(tls, "server.crt")
	if os.Mkdir(cert, 0700) != nil {
		t.Fatal("TLS directory-target fixture failed")
	}
	console, out, stderr, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
	if code != 1 || out != "" || stderr == "" || strings.Contains(console, "Initial password:") {
		t.Fatal("unsafe TLS directory target was accepted")
	}
	for _, name := range []string{"godav.sqlite", "godav.yaml", "godav.sqlite.lock"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatal("unsafe TLS target caused provisioning writes")
		}
	}
}

func TestInitConfigRejectsFreshSettingsStorageCollisions(t *testing.T) {
	bin := development(t)
	for _, name := range []string{"godav.sqlite", "godav.sqlite.lock", "godav.sqlite-wal", "godav.sqlite-shm", "godav.sqlite-journal"} {
		dir := privateFixture(t)
		console, out, stderr, code := privateConsoleCommand(t, dir, bin, "init", "config", "--config", filepath.Join(dir, name), "--username", "FixtureUser")
		if code != 1 || out != "" || stderr == "" || strings.Contains(console, "Initial password:") {
			t.Fatal("fresh settings/storage collision was accepted")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("fresh settings/storage collision caused state writes")
		}
	}
}

func TestInitConfigRefusesBroadDirectoryWithoutPermissionRepair(t *testing.T) {
	bin := development(t)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal("fixture token lookup failed")
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;GR;;;WD)")
	if err != nil {
		t.Fatal("broad fixture descriptor failed")
	}
	dir := filepath.Join(t.TempDir(), "broad-empty-fixture")
	p, _ := windows.UTF16PtrFromString(dir)
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if windows.CreateDirectory(p, &sa) != nil {
		t.Fatal("broad empty fixture creation failed")
	}
	before, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal("fixture ACL read failed")
	}
	console, out, stderr, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
	if code != 1 || out != "" || stderr != "godav: settings directory must be private\n" || strings.Contains(console, "Initial password:") {
		t.Fatal("broad directory was not refused before provisioning")
	}
	entries, err := os.ReadDir(dir)
	after, aclErr := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil || aclErr != nil || len(entries) != 0 || before.String() != after.String() {
		t.Fatal("refusal changed directory state or permissions")
	}
}

func TestInitConfigRejectsRawPathAliasesBeforeNormalization(t *testing.T) {
	bin := development(t)
	for _, kind := range []string{"bad-component", "drive-relative", "root-relative"} {
		dir := privateFixture(t)
		var path string
		switch kind {
		case "bad-component":
			path = dir + `\bad.\..\godav.yaml`
		case "drive-relative":
			path = filepath.VolumeName(dir) + "godav.yaml"
		case "root-relative":
			path = strings.TrimPrefix(dir, filepath.VolumeName(dir)) + `\godav.yaml`
		}
		console, out, stderr, code := privateConsoleCommand(t, dir, bin, "init", "config", "--config", path, "--username", "FixtureUser")
		if code != 1 || out != "" || stderr == "" || strings.Contains(console, "Initial password:") {
			t.Fatal("raw path alias bypassed validation")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("raw path alias refusal wrote state")
		}
	}
}
