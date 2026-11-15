# Native service implementation verification

Date: 2026-11-15 (UTC). Issue: https://github.com/jpconstantineau/GoDav-Prototype/issues/13.

Reviewed implementation: f519eed8a62036f280977d40507b0ff8ee486e2b.
Tree: b3bd597d2f54de5b899f16449a67b919728ee08e.
Starting preparation: 84a7fe6c250780805a8fce5b94d269f8d3bf0cad.
The final documentation-only handback preserves this exact production/test/module
content and receives a separate clean stamped release.

**This is code and fixture verification, not real native-service acceptance.
Issue #13 stays open.** See [the service contract](native-service-contract.md) for
CLI, identity, role-specific ACLs, setup and remaining manual acceptance.

## Implemented and exercised boundaries

- Same-binary Windows manager/dispatcher, using pinned x/sys v0.50.0. Fake SCM
  tests exercise exact expected registration/policy, narrow action behavior,
  pending/repeated status transitions, startup failure, timeouts/cancellation,
  uncertain install/rollback/uninstall outcomes and registration-only removal.
- Actual executable argument rejection, unsupported-platform build path, explicit
  operator refusal, paired shared-state flags and refusal of internal SCM mode
  outside a service process. Tests do not call the real SCM manager.
- Existing typed handler uses actual HTTPS/SQLite lifecycle through SCM protocol
  channels: readiness, bounded drain/cancel, persistence, ownership exclusion,
  interrogation and delayed/repeated status consumption.
- Session-scoped service file policy uses a synthetic service SID and the current
  operator on new disposable fixtures. Tests cover strict identity/ACL roles,
  inheritance, unsafe layout/refusal, real SQLite account commit/reopen,
  exclusion, TLS reads, pin rename prevention/release and nil/zero personal-mode
  isolation. Fixture ACLs are created with the new fixture, not applied to an
  existing user file or system setting.
- Real ConPTY account add/reset and private-console refusal, then real HTTPS
  authentication and restart, verify both preserved and reset credentials.
- Dispatcher failure tests retain cleanup ownership, reject late starts and
  preserve premature-dispatch failure even when cancellation promptly returns
  nil. The prompt-cancellation regression uses scheduler stress (4,096
  channel-synchronized attempts), not a claim of deterministic race ordering.

All TLS trust is in disposable client pools. No Windows certificate-store change
was made. Read-only native process/account/path introspection is used; actual SCM
manager operations and svc.Run dispatch entry were not executed.

## Corrections and independent review

The initial implementation's full Windows suite passed, but a combined race gate
found a real checkptr failure in the pinned SubAuthorityCount syscall wrapper,
which returns an interior pointer into a Go-owned SID buffer. The new path now
counts canonical textual components only after Windows SID validation and exact
canonical round-trip validation. No dependency update, checkptr disable, GORACE
tuning, test exclusion or other suppression was used.

Independent Standards and Spec reviews found and corrected:
1. Premature native dispatcher return could let main exit before runner cleanup.
   Started work is now canceled and joined; a synchronized gate rejects late work.
2. A subsequent cancellation/completion race could mask premature dispatch as
   success. Completion is now sampled before cancellation and that error retained.
3. Different local volumes were incorrectly rejected as containment errors.
   Distinct validated volumes are disjoint; all canonical/ACL checks still apply.
4. Install rollback wording incorrectly covered a later status-query failure.
   Documentation now distinguishes policy rollback from retained registration
   with explicitly uncertain status.

Behavioral regressions failed before the dispatcher corrections. The final
prompt-cancellation case failed the earlier code in all ten stress runs; reviewed
overlay copies passed tests/vet/race before copying. Both review axes then
confirmed the committed files match those exact reviewed bytes and cleared the
final implementation with no outstanding actionable findings.

Earlier full passes (480.747s and 487.207s) are superseded below. An initial worker
race invocation stopped before tests because CGO was disabled; reruns used the
established compiler configuration. The original combined race selection was
rerun unchanged after correction.

## Final frozen implementation gates

Host: Zen-1000, Windows amd64. Exact project-local Go 1.27.4,
GOTOOLCHAIN=local, ordinary application/child builds CGO_ENABLED=0.
Race harness: preinstalled MSYS2 UCRT GCC 14.2.0, process-local CGO_ENABLED=1.
Linux results are cross-build/test compilation, not Linux execution.

Evidence is in the project tools logs with prefix service13-native-.

| Check | Evidence suffix | Result |
| --- | --- | --- |
| Full Windows go test ./... -count=1 -timeout=15m | 48 | Pass; CLI 488.386s, service 1.404s |
| Source/test/module/notices hashes before/after | 47, 49 | Identical; clean exact implementation HEAD |
| Combined service/policy/manager/dispatcher race, count=3 | 50 | Pass; CLI 149.587s, service 5.438s |
| Windows/Linux vet | 51, 54 | Pass |
| Module verification/tidy diff | 52, 53 | Pass; unchanged dependencies |
| Linux CLI and manager test compilation | 55, 56 | Pass; no Linux runtime claim |
| 104 Go files normalized-copy gofmt check | 57 | Pass; source not rewritten |
| Both release targets and identity inspection | 58 through 61 | Pass; clean f519eed, UTC stamp 2026-11-15T01:00:24Z |
| Windows/Linux source scans including tests | 62, 63 | Pass; no vulnerabilities found |
| Windows/Linux binary scans | 64, 65 | Pass; no vulnerabilities found |
| Independent review resolution | 66 | Standards and Spec cleared |

Earlier failing checkptr evidence (13), corrected combined pass (28), and earlier
superseded runs remain retained. Final evidence includes SHA-256 manifests.

## Pending acceptance and scope limits

No actual service enrollment, service start/stop/unregister, native dispatcher
entry, virtual-account token use, elevation, credential transmission, existing
ACL/permission change, trust import, Explorer or VS Code acceptance occurred.
Real restricted-token access, SCM error/status behavior and persistence remain
manual gates requiring applicable separate execution authorization. Mock SCM
and synthetic SID fixtures do not satisfy those gates.

The one-volume host did not prove real cross-volume sessions. Normal SCM does not
retain stderr; service diagnostic capture setup/acceptance remains pending.
No automatic Event Log registration or log-file/permission creation was added.

The service layout separates read-only program/settings/TLS from mutable DB
storage. Existing single-directory bootstrap layouts require separately approved
operator provisioning, not automatic movement or permission repair. The legacy
personal-mode metadata-pin observation is recorded in the contract; that route
was intentionally not broadened in this slice.

This isolated implementation was not merged or pushed. Reconciliation with other
integration work and native acceptance remain coordinator/operator work.