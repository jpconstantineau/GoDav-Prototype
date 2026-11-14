# Slice 10 account administration verification

Date: 2026-11-14. Host: Zen-1000, Windows amd64. Branch:
`codex/godav-10-accounts`, based on reviewed integration
`7110b8fd2569bca3ec043f034f1290188082b0c9`. Implementation commit:
`41c40a64d1feb42e2d702d8a4676cb42b42f482d`.

## Executed public CLI checks

Tests run the actual executable with new disposable private fixtures, bounded
Windows ConPTY children and memory-only credential capture. Assertions never
print captures or query the private SQL layout.

- Missing/invalid usernames, duplicate/unknown/password/export flags and extra
  arguments fail without echo or persistent changes. Top-level help lists both
  clear verb commands.
- Add privately generates one password, leaves YAML byte-identical, never stores
  plaintext, survives reopen and rejects a differently cased duplicate without
  changing DB bytes.
- Reset matches usernames case-insensitively, changes persisted state, preserves
  YAML, survives reopen and refuses a missing account without DB mutation.
- The existing test-build-only console-failure tag proves failed post-commit
  delivery retains the change. Re-initialization does not reseed; an explicit
  reset recovers the retained account. Normal releases exclude the fault control.
- CREATE_NO_WINDOW and DETACHED_PROCESS launches refuse before modifying fixture
  files, identities or permissions.
- A retained shared ReadOnly or WriteExisting session excludes both executable
  commands. Closing the session permits reset. This proves the shared ownership
  boundary, not a running HTTP server test.
- Existing private files in non-inheriting and directory-only-inheriting private
  parents are refused as write targets. Missing YAML/DB, corrupt state, WAL header,
  journal sidecar and broadly accessible YAML are refused without repair.
- Two concurrent add processes produce exactly one success and one private
  disclosure. The loser receives an ownership/duplicate refusal.

The first ownership test attempted to inspect the exclusive lock while held and
failed in fixture setup. It was corrected to snapshot before acquisition and
assert after release. No production change was needed. The correction passed,
then the complete suite and race harness passed. Earlier expected red failures
and this fixture-only failure remain in separate logs.

## Aggregate evidence

Verified project-local Go 1.27.4 with process-scoped GOTOOLCHAIN=local. No toolchain,
module or checksum changes. Evidence is retained outside the repository under
`GoDav-Project-Tools/logs/slice10-*-20261114.log`.

- Full `go test ./... -count=1 -v -timeout=5m`: passed, CLI 174.203 seconds.
- Focused GCC 14.2.0 `go test -race` for all new CLI tests: passed, 42.109 seconds.
  This instruments the test harness/in-process fixtures, not CGO-free executable
  children. Concurrent-process behavior has its separate actual CLI test.
- Windows/Linux `go vet ./...`: passed.
- `go mod verify` and `go mod tidy -diff`: passed with no dependency changes.
- Govulncheck v1.9.0, Windows/Linux application and `-test` source scans: all four
  passed, no known vulnerabilities. Scanned 12 Windows modules and 11 Linux
  modules, including the application and Go 1.27.4 standard library.
- Full-suite release identity tests passed for Windows/Linux, including exact
  notices and exclusion of test fault tags/markers. Windows executed; Linux was
  compiled and inspected only. No release was pushed or deployed.

## Pending integration verification and limits

HTTP old/new credential authentication, stable authenticated identity, disabled
account behavior and actual server-versus-administration exclusion await #4.
Resource-content/identity preservation awaits DAV fixtures. These tests were not
run and ticket #11 must not be called fully verified until its required integrated
checks pass. The reset implementation updates only the verifier, but this pass
does not substitute SQL-layout assertions for the later public identity proof.

No Linux runtime, interrupted-write/power-loss durability or hostile path-swap
proof is claimed. No real owner accounts, trust, service, global settings,
existing permissions, tracker updates, push or integration merge were performed.

## Independent fixed-commit review

Both reviewers compared `7110b8f...41c40a6` read-only.

### Standards

No documented violations and no actionable baseline code-smell findings. Reviewed
repository guidance, TDD/testing/mocking rules and the account/bootstrap/localstate
contracts. CLI/service/store boundaries remain narrow; writable access, retained
connection use, safe diagnostics, secret delivery and fixture cleanup were checked.

### Spec

No blocking specification defects or scope creep. Reset changes only the verifier;
account creation uses the shared existing-state writer and atomic transaction.
No YAML write, implicit initialization, live second writer or production fault
control was introduced. HTTP/identity/resource verification remains deferred,
not silently treated as passing.

Summary: Standards 0 actionable findings; Spec 0 blocking findings. Review is
source inspection, not execution or proof of deferred integrated behavior.

Final focused rerun of every new account/help test plus release identity passed in
38.978 seconds (`slice10-final-focused-release-corrected-20261114.log`). An earlier
focused selector matched only ReleaseIdentity; its narrower pass is retained and
not counted as account coverage. The corrected selector and full suite cover all
new tests. Normalized UTF-8 gofmt passed for all 42 tracked Go files; whitespace
checks passed. There were no production changes after independent review; the
remaining commit records this evidence only.
