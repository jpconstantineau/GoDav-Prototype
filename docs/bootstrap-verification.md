# Slice 02 verification

Date: 2026-11-14. Native host: Zen, Windows amd64. Initial worktree branch:
codex/godav-02-bootstrap, based on reviewed integration bf6e765.

## Fixed-commit review corrections

Correction worktree: `ticket-02-review-fixes`, branch
`codex/godav-02-review-fixes`, based on candidate
`6167167137bb107d8f126d776654c9fe6f4d4bee`. Integration `bf6e765` is an ancestor.
No merge, push or tracker mutation was performed by the correction pass.

Actual CLI red/green evidence:

- A newly created private directory with no inheritable ACEs and a restricted
  precreated empty lock was previously accepted for fresh provisioning. It is now
  refused before writes. Directory-only inheritance is also refused. This tests
  the missing creation-policy check, not credential exposure under a changed
  process-token default DACL. No token default or existing directory ACL changed.
- Existing valid files copied with explicit private creation DACLs into a new
  non-inheriting private directory still pass a byte-preserving noninteractive
  no-op. Ordinary inheriting fresh fixtures and headless zero-write refusal pass.
- An ordinary `pragma_table_list` table previously concealed a non-STRICT account
  table while preserving its other constraints and records. The actual PRAGMA
  now rejects this fixture with DB/YAML bytes unchanged. Table and view shadows
  of the other three argument-bearing metadata functions fail closed unchanged.
- Username validation now has one account-domain predicate. One-character and
  64-character valid names survive initialization/restart; invalid leading
  punctuation, non-ASCII, whitespace, newline and overlong names retain sanitized
  usage refusals. These were passing baseline/refactor tests, not invented red
  evidence for a behavior-preserving extraction.

Fresh aggregate evidence is persisted outside the repository in
`GoDav-Project-Tools/logs/slice02-reviewfix-*-20261114.log`:

- Full `go test ./... -count=1 -v -timeout=5m`: passed, CLI package 98.087 seconds.
  Includes release fault-tag/marker exclusion and exact companion notices for
  Windows/Linux builds; Windows version executed, Linux artifact inspected only.
- Windows and Linux `go vet ./...`: passed. `go mod verify`: passed.
- Initial `go mod tidy -diff` found CRLF-only module-checksum differences from the
  fresh Windows checkout. Normalization matched the committed module content
  exactly, and the repeated check passed without dependency changes.
  `.gitattributes` now keeps only `go.mod`/`go.sum` LF on fresh checkouts.
- Windows/Linux application and `-test` govulncheck v1.9.0: all four scans passed
  with no known vulnerabilities, on the verified Go 1.27.4 compiler and unchanged
  dependency pins. Windows covered 12 modules and Linux 11, including the app.
- Bounded `CGO_ENABLED=1 go test -race ./internal/cli -run '^TestInitConfig'
  -count=1 -v -timeout=5m` passed in 65.635 seconds using the existing MSYS2 GCC
  14.2.0. This instruments the harness and in-process fixture code, not the
  CGO-free CLI subprocesses. Competing-process behavior has its own CLI test.
- Final focused correction and release-identity rerun passed in 21.566 seconds.

The log suffixes identify `dacl-red`, `dacl-green`, `dacl-preservation`,
`metadata-red`, `metadata-green`, `username-baseline`, `username-refactor`,
`full-suite`, `vet-windows`, `vet-linux`, `mod-verify`, `tidy-diff`,
`module-line-endings`, `tidy-diff-lf`, and the four
`vuln-{windows,linux}-{application,tests}` runs, plus `race-harness` and
`final-focused`. Toolchain/scanner/GCC version logs identify the actual tools.
Expected red logs and the initial line-ending failure are retained rather than
overwritten.

The retained-parent ACL policy protects SQLite-created journals from inception;
it does not freeze permission changes by the authorized owner/administrators.
Hostile parent swaps/reparse paths, broad token defaults, actual interrupted
writes/power loss and Linux runtime remain unproved. Prior hardlink/junction
creation denials were not retried. Independent re-review of the correction commit
is still required.

## Toolchain and dependencies

All current checks use the verified project-local Go 1.27.4 compiler with
GOTOOLCHAIN=local, GOENV=off and no persistent environment changes. Application
language baseline is Go 1.27.0. Normal application/release builds use CGO_ENABLED=0.
Pinned modules and paired libc are recorded in go.mod/go.sum. No x/term, x/net,
third-party CLI/router/logger or new service framework was added.

The preceding dependency audit verified SQLite 3.54.1 and its upstream source ID,
the authenticated module graph, licenses, and synthetic Argon2id cost samples
(approximately 36–47 ms for 19 MiB / t=2 / p=1). These samples are not a concurrency
or total-memory benchmark. THIRD_PARTY_NOTICES.txt preserves applicable notices,
including YAML NOTICE and Windows-linked MIT dependencies. The release builder
copies that file alongside both target executables.

## Actual public CLI evidence

Tests launch actual development executables, not private command handlers. Fresh
positive fixtures use test-owned Windows ConPTY and private directories created
with protected current-user/SYSTEM/Administrators DACLs. Console captures stay in
memory and are never printed. Normal stdout/stderr are captured separately.

Observed red then green:

- Missing explicit username; invalid username; duplicate/secret/positional flags.
- Fresh real SQLite/YAML initialization and one-time private password disclosure.
- Existing valid noninteractive no-op from a different working directory, with
  byte-identical persistent state.
- Post-commit YAML publication failure: retained DB, no secret, refused reseeding,
  and successful validation after explicit fixture-only YAML reconstruction.
- Post-commit private-console write failure: retained DB/YAML, no secret on retry.
- Unsafe TLS directory targets and fresh YAML/DB/sidecar/lock path collisions.
- Raw bad-component, drive-relative and root-relative paths before normalization.
- Structurally invalid database with preserved records but removed constraints.
- Release fixture go.sum completeness, help advertisement, and companion notices.

Additional actual CLI coverage passed:

- Headless CREATE_NO_WINDOW and DETACHED_PROCESS refusal on private fresh targets,
  with absolutely no writes. A previous gate incorrectly treated output-mode
  failure as no-console proof; the final gate also checks GetConsoleWindow.
  Independent probes confirmed ConPTY's nonzero message-only window handle.
- Strict YAML size/document/key/type/version/tag/anchor and endpoint rejection.
- Missing/corrupt/future-schema DB, orphan WAL/journal/SHM, excessive verifier cost.
- NUL-tailed 2 MiB malformed verifier: refusal and unchanged DB. This test did not
  establish a pre-fix acceptance failure or measure allocation bounds. SQL
  octet_length guards and cheap encoded-byte checks address the reviewed defect.
- View replacement rejected without changing database bytes.
- OS-held exclusive ownership refusal, stable empty artifact identity, release on
  exit, and two competing initializers yielding exactly one secret disclosure.
- Broad-directory refusal without permission repair.
- Normal release build metadata has no test-fault tags; fault marker strings are
  absent from both release binaries. Test-only build tags are the sole injected
  I/O failure controls; no normal runtime flag or environment setting enables them.

## Aggregate checks

After the audit fixes:

- `go test ./... -count=1`: passed; internal/cli 86.989 seconds.
- Windows and Linux `go vet ./...`: passed.
- `go mod verify`: all modules verified; `go mod tidy -diff`: no changes.
- Windows and Linux `govulncheck -test -show=verbose ./...`: no known
  vulnerabilities; 12 and 11 modules respectively, including the application,
  plus the Go 1.27.4 standard library.
- Git whitespace checks passed. CRLF conversion notices are repository working
  tree normalization notices, not test failures.
- `CGO_ENABLED=1 go test -race ./internal/cli -run '^TestInitConfig' -count=1`:
  passed in 48.200 seconds using the already-installed GCC. This instruments the
  harness and in-process fixture code, not its CGO-free CLI subprocesses. The real
  competing-process test separately exercises application ownership behavior.

The fixed-commit independent review is recorded separately when completed.
Source-scanner silence is not an application-security
proof. Linux was compiled/scanned and its release metadata inspected, not executed.

## Limits and recovery

No real owner credentials, Windows trust/service changes, original-checkout edits,
push or GitHub tracker mutations were performed. Test fixtures are disposable.

Windows denied the attempted same-fixture hardlink creation with Access is denied;
that runtime alias case was not executed against the application. Reparse-point,
hostile directory-swap, broader ACL inheritance variants and real interrupted disk
writes remain unproved. The code pins and revalidates the actual private DB and
YAML parents before writes; review distinguishes that fix from a runtime exploit
test. It does not defend against an authorized owner/admin changing permissions.

This slice proves normal restart persistence, not power-loss namespace durability.
An empty stable lock may remain after an existing-state validation failure; DB,
YAML and credentials remain unchanged. Do not delete/reseed a DB to recover a lost
YAML or password. Reconstruct settings for the retained DB; password reset belongs
to issue #11. HTTPS authentication belongs to issue #4 and is not claimed here.
