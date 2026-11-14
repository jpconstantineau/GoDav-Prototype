# Slice 02 verification

Date: 2026-11-14. Native host: Zen, Windows amd64. Worktree branch:
codex/godav-02-bootstrap, based on reviewed integration bf6e765.

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
