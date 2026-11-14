# Account administration and HTTPS cross-verification

Date: 2026-11-14 UTC. Native host: Zen-1000, Windows amd64.

- Combined reviewed base: `37e1597ea9ad0836d39ee00a9b347c2b3b080dca`.
- New test commit: `85793f49e1b9a6612b81bff0f5a2534abc8d015c`.
- Isolated branch/worktree: `codex/godav-10-https-crosscheck` /
  `GoDav-Worktrees/ticket-10-https-crosscheck`.
- Only test and verification documentation are added. Production implementation,
  shared fixtures, dependencies, primary integration and the combined candidate
  worktree are unchanged by this pass.

This completes the currently available #4/#11 cross-checks identified in the
[initial CLI verification](account-admin-verification.md). It does not claim the
full GoDav specification, durable-resource preservation or native-client acceptance.

## Executed scenario

`TestAccountAdministrationAcrossHTTPSRestart` invokes the real executable using
new private disposable settings/SQLite/TLS fixtures. Client trust is an isolated
Go cert pool; certificate verification is enabled and no system trust is changed.
Secrets stay in memory and every failure uses a fixed nonsecret message.

1. Bootstrap through private ConPTY, capture the generated password, and observe
   the authenticated owner identity while offline. Add a second account through
   the actual CLI and independently authenticate both users over real HTTPS.
2. With that server running, invoke both add and reset through the same working
   private-console helper. Both reach the ownership refusal, not the no-console
   gate. Both existing credentials continue authenticating. After server exit,
   verify identical YAML/DB/lock bytes, file identities and ACLs, with no new
   sidecar/directory entries.
3. Reset the second account offline using differently cased username input.
   Authenticate the replacement through the production principal path and verify
   unchanged stable ID, original display spelling and creation time. The original
   owner identity/password remains unchanged and the two users have distinct IDs.
4. Duplicate add and missing-account reset return their specific failures with no
   password disclosure or persisted state change.
5. Restart the executable server. The old second-account password returns 401 with
   the ordinary Basic challenge; the replacement returns 200, as does the untouched
   owner's original password. The missing user remains 401. Serving leaves account
   state unchanged. Routine server output contains neither tested passwords nor
   corresponding encoded Basic credentials or verifier text.

The extra identity observation is the narrowly approved production authentication
seam: `localstate.OpenExisting(..., ReadOnly)`, `AccountStore.LookupEnabled`, then
`auth.Account.VerifyPassword`. Sessions open and close only while the server is
stopped. The helper returns only identity/display/creation fields, never the
verifier. There is no direct SQL, debug endpoint or additional transport surface.
HTTPS independently proves credential usability rather than inferring it from
this seam alone.

## Verification evidence

Evidence is outside the repository in
`GoDav-Project-Tools/logs/slice10-cross-*-20261114.log`.

- Full combined `go test ./... -count=1 -v -timeout=6m` passed, CLI
  242.383 seconds. This includes both command families, existing HTTPS/disabled-
  account coverage, and release metadata/notices/fault-control exclusion checks.
- First real cross-test run passed, CLI 4.721 seconds.
- Three consecutive runs on the final test passed, CLI 13.654 seconds.
- GCC 14.2.0 race-instrumented harness and in-process principal fixture passed,
  6.469 seconds. Actual CLI children remain CGO-free and are not race-instrumented.
- Windows/Linux vet, module verification and tidy-diff passed with no pin changes.
- Govulncheck v1.9.0 application and test source scans passed for both OS targets,
  no known vulnerabilities. Covered 13 Windows and 12 Linux modules, including
  the application and Go 1.27.4 standard library.
- Strict UTF-8, CRLF-normalized gofmt passed for 55 tracked Go files. Whitespace
  checks passed. All commands use the project-local verified Go 1.27.4 with
  GOTOOLCHAIN=local and process-scoped environment changes only.

This adds integration coverage to already implemented behavior; the new test
passed initially. It is not presented as a new production red/green implementation
cycle. Earlier independent CLI/HTTPS red/green evidence remains in their respective
verification documents.

## Independent review

Both axes reviewed fixed diff `37e1597...85793f4` read-only.

### Standards

Zero documented violations and zero actionable code-smell findings. Review checked
the approved seams, meaningful identity/state comparisons, fixed secret-safe
failures, cleanup of response bodies/clients/sessions, and existing process cleanup.

### Spec

Zero blocking specification findings. Review confirmed the actual executable/TLS
sequence, server-held writer exclusion, stable principal identity, old/new password
results and unchanged other account. Source review is distinct from execution.

## Remaining limits

The existing executable fixture stops by terminating and joining its process.
This proves restart after process termination, not graceful foreground or SCM
shutdown. Those lifecycle claims remain with their later slices.

#4 already tests disabled-account HTTPS rejection. This new scenario does not
assert password-reset preservation for a disabled account. It uses enabled
accounts only. Durable file/resource content and identity preservation require
#5/#6 fixtures and remain explicitly deferred; no placeholder resource is used to
pretend that gate passed.

No Linux runtime, hostile filesystem-swap, power-loss durability, real owner
credentials, trust/service changes, global settings, integration merge, push,
deployment or tracker update is claimed or performed by this cross-check pass.
