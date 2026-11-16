package cli_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/localstate"
	"golang.org/x/sys/windows"
)

// The foundation API is an explicitly approved additional seam until the
// server and account commands exist. Reuse only test-owned CLI/ACL fixtures.
func TestLocalStateRejectsInvalidIntentWithoutWrites(t *testing.T) {
	for _, intent := range []localstate.Intent{0, 3, 255} {
		dir := privateFixture(t)
		session, err := localstate.OpenExisting(context.Background(), filepath.Join(dir, "godav.yaml"), intent)
		if session != nil || err == nil || err.Error() != "invalid local state intent" {
			t.Fatal("invalid intent was not safely refused")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("invalid intent wrote state")
		}
	}
}

func TestLocalStateRetainsOwnershipUntilClose(t *testing.T) {
	bin := development(t)
	dir := privateFixture(t)
	_, _, _, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
	if code != 0 {
		t.Fatal("fixture initialization failed")
	}
	configPath, dbPath := filepath.Join(dir, "godav.yaml"), filepath.Join(dir, "godav.sqlite")
	beforeConfig, configErr := os.ReadFile(configPath)
	beforeDB, dbErr := os.ReadFile(dbPath)
	lockBefore, lockErr := os.Stat(dbPath + ".lock")
	if configErr != nil || dbErr != nil || lockErr != nil {
		t.Fatal("fixture state inspection failed")
	}
	for _, intent := range []localstate.Intent{localstate.ReadOnly, localstate.WriteExisting} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		session, err := localstate.OpenExisting(ctx, configPath, intent)
		cancel()
		if err != nil {
			t.Fatal("valid existing state could not be opened")
		}
		defer session.Close()
		if session.Settings().DatabasePath != dbPath || session.Settings().ListenAddress != "127.0.0.1:8443" || session.Accounts() == nil {
			t.Fatal("session did not expose selected settings/accounts")
		}
		settings := session.Settings()
		settings.DatabasePath = "changed by caller"
		if session.Settings().DatabasePath != dbPath {
			t.Fatal("settings did not return a value copy")
		}
		// An exclusive OS read must conflict with the retained SQLite handle,
		// independently of the separate ownership artifact. No SQL is exposed.
		p, _ := windows.UTF16PtrFromString(dbPath)
		h, openErr := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if openErr == nil {
			windows.CloseHandle(h)
			t.Fatal("session did not retain an open database connection")
		}
		// Cancellation of the opening context must not release a returned
		// session. The actual CLI remains excluded until explicit Close.
		out, stderr, code := command(t, dir, bin, "init", "config", "--username", "Replacement")
		if code != 1 || out != "" || stderr != "godav: database is in use or ownership is unsafe\n" {
			t.Fatal("session failed to retain exclusive ownership")
		}
		if session.Close() != nil || session.Close() != nil {
			t.Fatal("session close was not successful and idempotent")
		}
		h, openErr = windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if openErr != nil {
			t.Fatal("session close leaked the database connection")
		}
		if windows.CloseHandle(h) != nil {
			t.Fatal("database handle probe close failed")
		}
		out, stderr, code = command(t, dir, bin, "init", "config", "--username", "Replacement")
		if code != 0 || stderr != "" || out != "godav: already initialized\n" {
			t.Fatal("session close did not release ownership")
		}
	}
	afterConfig, configErr := os.ReadFile(configPath)
	afterDB, dbErr := os.ReadFile(dbPath)
	lockAfter, lockErr := os.Stat(dbPath + ".lock")
	entries, entriesErr := os.ReadDir(dir)
	if configErr != nil || dbErr != nil || lockErr != nil || entriesErr != nil || len(entries) != 3 ||
		!bytes.Equal(beforeConfig, afterConfig) || !bytes.Equal(beforeDB, afterDB) || !os.SameFile(lockBefore, lockAfter) || lockAfter.Size() != 0 {
		t.Fatal("existing session changed persistent state or lock identity")
	}
}

func TestLocalStateRequiresInheritanceOnlyForWritableParent(t *testing.T) {
	bin := development(t)
	source := privateFixture(t)
	_, _, _, code := privateConsoleCommand(t, source, bin, "init", "config", "--username", "FixtureUser")
	if code != 0 {
		t.Fatal("fixture initialization failed")
	}
	for _, inheritance := range []string{"", "CI"} {
		dir := privateFixtureWithInheritance(t, inheritance)
		for _, name := range []string{"godav.yaml", "godav.sqlite", "godav.sqlite.lock"} {
			data, err := os.ReadFile(filepath.Join(source, name))
			if err != nil {
				t.Fatal("source fixture read failed")
			}
			writeRestrictedFixtureFile(t, filepath.Join(dir, name), data)
		}
		before := snapshotLocalState(t, dir)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		session, err := localstate.OpenExisting(ctx, filepath.Join(dir, "godav.yaml"), localstate.ReadOnly)
		if err != nil {
			cancel()
			t.Fatal("read-only session refused already-private files")
		}
		if session.Close() != nil {
			cancel()
			t.Fatal("read-only session close failed")
		}
		session, err = localstate.OpenExisting(ctx, filepath.Join(dir, "godav.yaml"), localstate.WriteExisting)
		cancel()
		if session != nil || err == nil || err.Error() != "database is in use or ownership is unsafe" {
			if session != nil {
				session.Close()
			}
			t.Fatal("writer accepted a parent without file inheritance")
		}
		assertLocalStateUnchanged(t, dir, before)
		// Failed writable opening must not retain even the earlier YAML pin.
		moved := dir + "-released"
		if os.Rename(dir, moved) != nil || os.Rename(moved, dir) != nil {
			t.Fatal("failed writable session leaked directory pins")
		}

		// A separate settings parent is read-only even for a writer. Only the
		// selected DB parent needs file inheritance for new journal files.
		settingsDir := privateFixtureWithInheritance(t, inheritance)
		yaml := strings.Replace(bootstrapSettings, "database_path: 'godav.sqlite'", "database_path: '"+filepath.ToSlash(filepath.Join(source, "godav.sqlite"))+"'", 1)
		writeRestrictedFixtureFile(t, filepath.Join(settingsDir, "godav.yaml"), []byte(yaml))
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		session, err = localstate.OpenExisting(ctx, filepath.Join(settingsDir, "godav.yaml"), localstate.WriteExisting)
		cancel()
		if err != nil {
			t.Fatal("writer incorrectly required inheritance in read-only settings parent")
		}
		if session.Close() != nil {
			t.Fatal("writer close failed")
		}
	}
}

// Snapshots observe only bytes, identity, permissions and entries at the
// approved API seam. They never assert the application's private SQL layout.
type localStateFile struct {
	data     []byte
	info     os.FileInfo
	security string
}

func localStateSecurity(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal("fixture security inspection failed")
	}
	return sd.String()
}

func snapshotLocalState(t *testing.T, dir string) map[string]localStateFile {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal("fixture directory inspection failed")
	}
	files := map[string]localStateFile{"": {security: localStateSecurity(t, dir)}}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		info, statErr := os.Stat(path)
		if err != nil || statErr != nil {
			t.Fatal("fixture file inspection failed")
		}
		files[entry.Name()] = localStateFile{data, info, localStateSecurity(t, path)}
	}
	return files
}

func assertLocalStateUnchanged(t *testing.T, dir string, before map[string]localStateFile) {
	t.Helper()
	after := snapshotLocalState(t, dir)
	if len(before) != len(after) {
		t.Fatal("session changed directory entries")
	}
	for name, old := range before {
		current, exists := after[name]
		if !exists || !bytes.Equal(old.data, current.data) || old.security != current.security || (name != "" && !os.SameFile(old.info, current.info)) {
			t.Fatal("session changed bytes, identity or permissions")
		}
	}
}

func TestLocalStateFailedOpenPreservesStateAndReleasesResources(t *testing.T) {
	bin := development(t)
	source := privateFixture(t)
	_, _, _, code := privateConsoleCommand(t, source, bin, "init", "config", "--username", "FixtureUser")
	if code != 0 {
		t.Fatal("fixture initialization failed")
	}
	for _, intent := range []localstate.Intent{localstate.ReadOnly, localstate.WriteExisting} {
		for _, kind := range []string{"future-schema", "wal-header", "truncated", "sidecar", "broad-settings", "raw-alias", "canceled"} {
			t.Run(kind+"/"+map[localstate.Intent]string{localstate.ReadOnly: "read", localstate.WriteExisting: "write"}[intent], func(t *testing.T) {
				dir := privateFixture(t)
				for _, name := range []string{"godav.yaml", "godav.sqlite", "godav.sqlite.lock"} {
					data, err := os.ReadFile(filepath.Join(source, name))
					if err != nil {
						t.Fatal("source fixture read failed")
					}
					if name == "godav.sqlite" {
						switch kind {
						case "future-schema":
							binary.BigEndian.PutUint32(data[60:64], 2)
						case "wal-header":
							data[18], data[19] = 2, 2
						case "truncated":
							data = data[:99]
						}
					}
					writeFixtureFileWithACL(t, filepath.Join(dir, name), data, kind == "broad-settings" && name == "godav.yaml")
				}
				want := "invalid or unsupported database/account state"
				configPath := filepath.Join(dir, "godav.yaml")
				switch kind {
				case "sidecar":
					writeRestrictedFixtureFile(t, filepath.Join(dir, "godav.sqlite-journal"), []byte("preserve partial fixture"))
					want = "partial database state requires explicit recovery"
				case "broad-settings":
					want = "settings are missing, unsafe or inaccessible"
				case "raw-alias":
					configPath = dir + `\bad.\..\godav.yaml`
					want = "invalid settings path"
				case "canceled":
					want = "local state opening canceled"
				}
				before := snapshotLocalState(t, dir)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if kind == "canceled" {
					cancel()
				}
				session, err := localstate.OpenExisting(ctx, configPath, intent)
				cancel()
				if session != nil || err == nil || err.Error() != want {
					if session != nil {
						session.Close()
					}
					t.Fatal("invalid state was not refused with a fixed safe error")
				}
				assertLocalStateUnchanged(t, dir, before)
				for _, name := range []string{"godav.sqlite", "godav.sqlite.lock"} {
					p, _ := windows.UTF16PtrFromString(filepath.Join(dir, name))
					h, openErr := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
					if openErr != nil {
						t.Fatal("failed open leaked database or ownership handle")
					}
					if windows.CloseHandle(h) != nil {
						t.Fatal("released handle probe close failed")
					}
				}
				// This also catches retained SQLite or ownership handles following
				// a failure after the connection was opened (future schema).
				moved := dir + "-released"
				if os.Rename(dir, moved) != nil || os.Rename(moved, dir) != nil {
					t.Fatal("failed open leaked connection, ownership or parent pins")
				}
			})
		}
	}
}
