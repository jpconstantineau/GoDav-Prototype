package cli_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// This approved fixture builder discovers the documented PHC text through
// SQLite's public metadata. It neither names nor asserts private schema objects.
// Schema iteration and replacement size are bounded; no verifier is printed.
func corruptPHCWithNULTail(t *testing.T, file string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(file), RawQuery: "mode=rw"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal("malformed fixture open failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		t.Fatal("fixture metadata read failed")
	}
	var tables []string
	for rows.Next() {
		var table string
		if rows.Scan(&table) != nil || len(tables) >= 16 || len(table) > 128 {
			rows.Close()
			t.Fatal("fixture metadata exceeds bounds")
		}
		tables = append(tables, table)
	}
	if rows.Err() != nil {
		rows.Close()
		t.Fatal("fixture metadata incomplete")
	}
	rows.Close()
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	for _, table := range tables {
		rows, err = db.QueryContext(ctx, "PRAGMA table_info("+quote(table)+")")
		if err != nil {
			t.Fatal("fixture column metadata read failed")
		}
		var columns []string
		for rows.Next() {
			var cid, required, pk int
			var name, kind string
			var defaultValue any
			if rows.Scan(&cid, &name, &kind, &required, &defaultValue, &pk) != nil || len(columns) >= 16 || len(name) > 128 {
				rows.Close()
				t.Fatal("fixture column metadata exceeds bounds")
			}
			columns = append(columns, name)
		}
		if rows.Err() != nil {
			rows.Close()
			t.Fatal("fixture column metadata incomplete")
		}
		rows.Close()
		for _, column := range columns {
			const prefix = "$argon2id$"
			where := "typeof(" + quote(column) + ")='text' AND substr(" + quote(column) + ",1,?)=?"
			var count int
			if db.QueryRowContext(ctx, "SELECT count(*) FROM "+quote(table)+" WHERE "+where, len(prefix), prefix).Scan(&count) != nil {
				t.Fatal("fixture PHC discovery failed")
			}
			if count == 0 {
				continue
			}
			if count != 1 {
				t.Fatal("fixture PHC target is ambiguous")
			}
			payload := prefix + "\x00" + strings.Repeat("x", 2<<20)
			result, err := db.ExecContext(ctx, "UPDATE "+quote(table)+" SET "+quote(column)+"=? WHERE "+where, payload, len(prefix), prefix)
			if err != nil {
				t.Fatal("fixture PHC mutation failed")
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				t.Fatal("fixture PHC mutation was not isolated")
			}
			if db.Close() != nil {
				t.Fatal("fixture close failed")
			}
			return
		}
	}
	t.Fatal("fixture documented PHC representation not found")
}

func TestInitConfigRejectsNULTailedOversizedVerifierWithoutMutation(t *testing.T) {
	bin := development(t)
	dir := privateFixture(t)
	_, _, _, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
	if code != 0 {
		t.Fatal("fixture initialization failed")
	}
	file := filepath.Join(dir, "godav.sqlite")
	corruptPHCWithNULTail(t, file)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("malformed fixture read failed")
	}
	out, stderr, code := command(t, dir, bin, "init", "config", "--username", "FixtureUser")
	if code != 1 || out != "" || stderr != "godav: invalid or unsupported database/account state\n" {
		t.Fatal("NUL-tailed malformed state was not safely rejected")
	}
	after, err := os.ReadFile(file)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("malformed-state rejection mutated database")
	}
	// This verifies rejection/preservation, not a process allocation bound. The
	// byte guards are additionally inspected in code review; no fake red claim.
}

func TestInitConfigRejectsSchemaWithoutConstraints(t *testing.T) {
	bin := development(t)
	for _, kind := range []string{"constraint-free-table", "view"} {
		dir := privateFixture(t)
		_, _, _, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
		if code != 0 {
			t.Fatal("fixture initialization failed")
		}
		file := filepath.Join(dir, "godav.sqlite")
		u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(file), RawQuery: "mode=rw"}
		db, err := sql.Open("sqlite", u.String())
		if err != nil {
			t.Fatal("schema fixture open failed")
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Discover a disposable ordinary table through public SQLite metadata,
		// then preserve its records while deliberately discarding its constraints.
		var name string
		if db.QueryRowContext(ctx, "SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' LIMIT 1").Scan(&name) != nil || len(name) > 128 {
			t.Fatal("schema fixture discovery failed")
		}
		quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("schema fixture transaction failed")
		}
		defer tx.Rollback()
		replace := "ALTER TABLE godav_fixture_copy RENAME TO " + quote(name)
		if kind == "view" {
			replace = "CREATE VIEW " + quote(name) + " AS SELECT * FROM godav_fixture_copy"
		}
		for _, statement := range []string{"CREATE TABLE godav_fixture_copy AS SELECT * FROM " + quote(name), "DROP TABLE " + quote(name), replace} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Fatal("schema fixture construction failed")
			}
		}
		if tx.Commit() != nil || db.Close() != nil {
			t.Fatal("schema fixture publication failed")
		}
		before, err := os.ReadFile(file)
		if err != nil {
			t.Fatal("schema fixture snapshot failed")
		}
		out, stderr, code := command(t, dir, bin, "init", "config", "--username", "FixtureUser")
		if code != 1 || out != "" || stderr != "godav: invalid or unsupported database/account state\n" {
			t.Fatal("unsupported structural schema was accepted")
		}
		after, err := os.ReadFile(file)
		if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
			t.Fatal("unsupported-schema refusal changed database")
		}
	}
}
