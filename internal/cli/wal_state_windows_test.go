package cli_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestInitConfigRejectsClosedWALWithoutMutation(t *testing.T) {
	bin := development(t)
	source := privateFixture(t)
	_, _, _, code := privateConsoleCommand(t, source, bin, "init", "config", "--username", "FixtureUser")
	if code != 0 {
		t.Fatal("fixture initialization failed")
	}
	// Switch only this disposable, privately inherited fixture to real WAL,
	// checkpoint and close it. No production validation or repair is involved.
	u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(filepath.Join(source, "godav.sqlite")), RawQuery: "mode=rw"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal("WAL fixture open failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var journal string
	if db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journal) != nil || journal != "wal" {
		t.Fatal("WAL fixture mode was not established")
	}
	var busy, pages, checkpointed int
	if db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &pages, &checkpointed) != nil || busy != 0 || pages != checkpointed {
		t.Fatal("WAL fixture checkpoint failed")
	}
	if db.Close() != nil {
		t.Fatal("WAL fixture close failed")
	}
	files := map[string][]byte{}
	for _, name := range []string{"godav.yaml", "godav.sqlite", "godav.sqlite.lock"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal("closed WAL fixture read failed")
		}
		files[name] = data
	}
	entries, err := os.ReadDir(source)
	if err != nil || len(entries) != len(files) {
		t.Fatal("closed WAL fixture retained unexpected sidecars")
	}
	for _, inheritance := range []string{"OICI", ""} {
		name := "inheriting"
		if inheritance == "" {
			name = "non-inheriting"
		}
		t.Run(name, func(t *testing.T) {
			// All copies and their private DACLs are created together. In the
			// non-inheriting case, even the empty lock is explicitly private.
			// Never change existing ACLs or the process token default.
			dir := privateFixtureWithInheritance(t, inheritance)
			security := func(path string) string {
				t.Helper()
				sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
				if err != nil {
					t.Fatal("WAL fixture security inspection failed")
				}
				return sd.String()
			}
			parentSecurity := security(dir)
			beforeSecurity := map[string]string{}
			beforeIdentity := map[string]os.FileInfo{}
			for name, data := range files {
				path := filepath.Join(dir, name)
				writeRestrictedFixtureFile(t, path, data)
				beforeSecurity[name] = security(path)
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal("WAL fixture identity inspection failed")
				}
				beforeIdentity[name] = info
			}
			out, stderr, code := command(t, dir, bin, "init", "config", "--username", "Replacement")
			if code != 1 || out != "" || stderr != "godav: invalid or unsupported database/account state\n" {
				t.Error("closed WAL state was not safely refused")
			}
			for name, before := range files {
				path := filepath.Join(dir, name)
				after, err := os.ReadFile(path)
				if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
					t.Error("WAL refusal changed existing file bytes")
				}
				info, err := os.Stat(path)
				if err != nil || !os.SameFile(beforeIdentity[name], info) || beforeSecurity[name] != security(path) {
					t.Error("WAL refusal changed file identity or permissions")
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != len(files) {
				t.Error("WAL refusal created additional state")
			}
			if parentSecurity != security(dir) {
				t.Error("WAL refusal changed parent permissions")
			}
		})
	}
}
