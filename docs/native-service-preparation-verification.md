# Native service preparation verification

Issue #13 remains **open and incomplete**. This records the limited preparation
contract in [native-service-preparation.md](native-service-preparation.md), not
native SCM installation, logon, dispatch or operating-system acceptance.

## Exact implementation

- Base: `30e136baa7b26054187d6f9a4934497db4d17424`.
- Initial preparation: `1dfdf15c45723e08fddbca3aa6bc68fcfc9f8d3f`.
- Reviewed, corrected implementation: `9c4b829a634f93cc56db679cb2964fd451336b9e`.
- Implementation tree: `ca935e4df453d3d22dce006f70c35008911b0811`.
- Isolated branch: `agent/native-service13-20261114` on Zen-1000.
- Exact project-local Go 1.27.4, GOTOOLCHAIN=local, CGO-free normal executable.
  Existing x/sys v0.50.0 is reused; no module, notice, ACL or OS setting change.

## Public evidence

The real HTTPS/SQLite runner is driven through the typed lifecycle observer and
native svc.Handler protocol with **fake SCM channels**. Tests establish:

- Authenticated HTTPS after Ready; Stopped after resource release; independent
  foreground executable restart after shutdown.
- Stop drains an accepted upload and committed bytes survive restart.
- Invalid startup never publishes Running and returns service-specific failure.
- Shutdown grace expiry cancels an unfinished overwrite, reports failure and
  preserves prior bytes. Concurrent foreground opening is refused while the
  handler owns the database. Repeated Stop and Interrogate preserve status.
- Sixteen real idle-server stops with deliberately delayed SCM status consumption
  preserve admission-closure progress before completion.
- Executable argument validation rejects ambiguity, unknown/password flags and
  account control characters without changing fixture files or echoing values.
  Valid Windows service commands explicitly refuse the unresolved contract.

All state, accounts and TLS are disposable fixtures. TLS roots are private test
client pools, not Windows trust. Ordinary CLI children remain CGO-free; the race
harness instruments the in-process server/handler, not those child executables.

## Development and review

Initial missing API/package compile failures preceded the observer/handler
implementation. The executable argument-validation behavior also failed before
its shell was implemented. Additional already-passing cases are regression
coverage, not invented behavioral red steps.

One exploratory deadline test timed out because its client io.Pipe remained open
after the server correctly completed. Closing the client pipe after server
completion fixed this test harness; it was not evidence of a server hang. An
initial gofmt wildcard invocation was rejected by Windows argument handling;
format verification below uses explicit normalized copies and passes.

Independent Standards and Spec reviews of the initial preparation found three
distinct issues: completion overtaking pending lifecycle events, incomplete
control-character rejection, and Windows-only path validation preventing a valid
POSIX install request from reaching its unsupported message. The first two were
reproduced at the public executable/SCM seams; the idle-stop failure occurred in
9 of 16 iterations. The correction uses a single FIFO including terminal return,
Unicode control-character checks and platform-specific path validation. Both
reviews cleared the exact correction commit with no new blocker. Linux behavior
was reviewed and compiled, not executed here.

## Frozen implementation gates

Evidence is under `GoDav-Project-Tools/logs`, prefix `service13-prep-`.

| Check | Evidence suffix | Result |
| --- | --- | --- |
| Review regressions before correction | 15 | Expected fail, exit 1 |
| All service-focused tests after correction | 16 | Pass, 18.542s |
| Full Windows `go test ./... -count=1 -timeout=15m` | 18 | Pass, CLI 431.569s |
| Before/after code, tests, modules and notices hashes | 17, 19 | Identical, clean exact implementation HEAD |
| Windows/Linux vet | 20, 24 | Pass |
| Module verify/tidy diff | 21, 22 | Pass, no dependency edits |
| Focused `-race` tests, three repetitions | 23 | Pass, 63.193s |
| Linux CLI test compilation | 25 | Pass; not Linux execution |
| Windows/Linux release build and identity | 26 through 29 | Pass, clean 9c4b829, UTC stamp 2026-11-14T21:43:32Z |
| Windows/Linux source scans including tests | 30, 31 | Pass, no vulnerabilities found |
| Windows/Linux binary scans | 32, 33 | Pass, no vulnerabilities found |
| All 90 Go files normalized for CRLF then gofmt checked | 34 | Pass; source not reformatted by this check |
| Independent review resolution summary | 35 | Both axes cleared |
| SHA-256 evidence manifest | 36 | Log sizes and hashes retained |

An earlier mutable development full suite passed in 460.786s. It is superseded by
the frozen implementation run above and is not used as final-source proof.

The final handback adds only this verification document. Its production/test/
module tree must remain byte-identical to the reviewed implementation. A separate
clean release is stamped from that final docs-only commit before handback.

## Still unproved and unimplemented

No real service registration, manager operation, SCM dispatcher entry, elevation,
service-account provisioning, credential transmission, ACL grant, certificate
trust or native Windows-client test was executed. Installation/check/start/stop/
uninstall are deliberately blocked on Windows; the native dispatcher and SCM
manager are not yet connected to the executable. No default identity or SYSTEM
fallback is selected. Approved identity/provisioning design, manager/dispatcher
implementation and separately authorized native acceptance remain prerequisites
for completing #13 and then the full prototype acceptance milestone.
