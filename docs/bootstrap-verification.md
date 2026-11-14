# Slice 02 verification

Date: 2026-11-14. Native host: Zen, Windows amd64. Initial worktree branch:
codex/godav-02-bootstrap, based on reviewed integration bf6e765.

## Persisted WAL pre-open correction

Correction worktree and branch: `ticket-02-wal-guard`, based on reviewed candidate
`fdab39ee92cd77d3f2a4b5ffdbfbd8d5866f82b1`. Integration `bf6e765` remains an
ancestor. No merge, push, tracker mutation or original-checkout edit occurred.

The latest review identified that SQLite `mode=ro` can create WAL/SHM files for
a cleanly closed WAL database without sidecars. A real SQLite fixture initialized
through the CLI was switched to WAL, checkpointed and closed using fixture-only
PRAGMAs. Its complete closed file set was then copied into new inheriting and
non-inheriting private directories with explicit private file/lock creation
DACLs. Both uncorrected CLI runs failed refusal and created additional state.
The corrected runs refuse and preserve every file's bytes, identity, owner/DACL,
the parent permissions and directory entry set. No existing ACL or token default
was changed; this is not an unrelated-user access exploit or hostile-swap test.

Production validation now reads a bounded 100-byte SQLite header before any
SQLite open, requiring the signature and rollback read/write format bytes 1/1.
It does not change journal mode, repair data or ignore WAL through immutable mode.
Existing sidecar checks remain. Valid rollback no-op in a non-inheriting private
directory, orphan sidecars and corrupt/missing/future-schema refusals still pass.
Both unused `syncParent` hooks were removed; the unchanged directory-fsync and
power-loss limitation now sits beside `publishExclusive`.

Fresh evidence uses isolated Go 1.27.4, unchanged audited module pins and unique
`GoDav-Project-Tools/logs/slice02-walguard-*-20261114.log` files. The expected
`wal-red` failure is retained; `wal-green` passed in 12.573 seconds. Windows/Linux
vet, module verification, tidy-diff and all four application/test vulnerability
scans passed. Govulncheck v1.9.0 reported no known vulnerabilities, covering 12
Windows modules and 11 Linux modules including the application.

Full `go test ./... -count=1 -v -timeout=5m` passed, CLI 133.706 seconds. The
bounded GCC 14.2.0 `CGO_ENABLED=1 go test -race ./internal/cli -run
'^TestInitConfig' -count=1 -v -timeout=5m` passed in 86.629 seconds. This instruments
only the harness/in-process fixtures, not the CGO-free CLI subprocesses.

The final focused/release run passed in 16.388 seconds. Both target releases
exclude test fault tags/markers and retain exact companion notices. Windows
version executed; Linux artifacts were compiled and inspected only. No new
release artifacts are published by this pass; integration will rebuild them.

`slice02-walguard-evidence-sha256-20261114.log` identifies the correction commit
and hashes all 16 new evidence logs, including the expected red failure. Prior
logs/manifests were not overwritten. All prior limits remain: no Linux runtime,
power-loss/interrupted-write or hostile-swap test; no retry of denied
hardlink/junction creation; no real account, dependency, recovery command,
automatic bootstrap, listener, trust, service or install changes. Fixed-commit
independent review remains required before integration.

## YAML privacy and shared Windows component corrections

Correction worktree: `ticket-02-yaml-privacy`, branch
`codex/godav-02-yaml-privacy`, based on reviewed candidate
`a54658db55fd3f7f30eeec7f156b49d031191f4c`. Integration `bf6e765` remains an
ancestor. This pass does not merge or change the prior candidate, integration,
original checkout, owner documents, or existing evidence logs.

The existing-YAML owner/DACL omission was a static-source review finding. A new
public-CLI regression copied legitimate disposable settings/account data into a
new private fixture directory, creating the YAML with an explicit broad file
DACL and the DB with a private DACL. The uncorrected CLI failed the refusal
assertion; the corrected CLI refuses before reading YAML or creating lock state.
The final fixture asserts unchanged YAML/DB bytes and unchanged YAML owner/DACL.
No existing file/directory ACL or token default was changed, and no unrelated-user
access exploit was attempted. Private existing-state no-op, including files in a
non-inheriting private directory, still preserves data. The YAML private-file
check runs initially and again under ownership; this is not a hostile-swap test.

The duplicate Windows component rules now use one small lexical predicate. Raw
settings path policy and absolute local-drive/OS checks remain separate. Public
CLI baseline/refactor coverage preserves device-name refusals (including CLOCK$,
spaces before extensions, COM/LPT digits and superscript prefixes) and successful
initialization/restart for ordinary COM10, COM1tail, LPT10, LPT9notes and NULdata
filenames. An additional red/green case proves raw normalization no longer erases
a superscript-device-prefix component that the absolute-path policy refuses.

Fresh checks on corrected code, using the isolated verified Go 1.27.4 toolchain:

- Full `go test ./... -count=1 -v -timeout=5m`: passed, CLI 129.590 seconds.
- Windows/Linux `go vet ./...`, `go mod verify`, and `go mod tidy -diff`: passed.
  No module or checksum changes were needed.
- Windows/Linux application and `-test` govulncheck v1.9.0: all four scans passed
  with no known vulnerabilities. Windows covered 12 modules and Linux 11,
  including the application. Audited dependency pins remain unchanged.
- Existing GCC 14.2.0 `CGO_ENABLED=1 go test -race ./internal/cli -run
  '^TestInitConfig' -count=1 -v -timeout=5m`: passed, 83.315 seconds. This instruments
  only the harness and in-process fixtures, not the CGO-free CLI subprocesses.
- Final focused correction/release rerun: passed, 21.996 seconds. Both Windows
  and Linux release artifacts exclude test fault tags/markers and retain exact
  companion notices. Windows version executed; Linux was inspected only.

Evidence is outside the repository in
`GoDav-Project-Tools/logs/slice02-yamlprivacy-*-20261114.log`. Suffixes identify
`dacl-red`, `dacl-green`, `path-baseline`, `path-red`, `path-green`, `full-suite`,
`vet-windows`, `vet-linux`, `mod-verify`, `tidy-diff`, four
`vuln-{windows,linux}-{application,tests}` scans, `race-harness`,
`final-focused-release`, and toolchain/scanner/GCC versions. Expected red failures
are retained. `slice02-yamlprivacy-evidence-sha256-20261114.log` records the new
logs and final source identity without overwriting the prior evidence manifest.

All prior limits remain: no Linux runtime, actual power-loss/interrupted-write,
hostile-swap/reparse or broader token-default experiment was performed. Denied
hardlink/junction creation was not retried. Private parent inheritance, pinned
parent handles, bounded NUL-inclusive SQL reads, real PRAGMA metadata and sidecar
refusals remain in place. No new dependency, recovery command, automatic
bootstrap, listener, trust, service, install, push or GitHub write was added.
Independent fixed-commit re-review is still required before integration.

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
