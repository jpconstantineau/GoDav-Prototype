# Native service preparation (partial slice 12)

Issue: https://github.com/jpconstantineau/GoDav-Prototype/issues/13.
Base: 30e136baa7b26054187d6f9a4934497db4d17424.

This is preparatory plumbing, **not a completed native service feature**. The
identity, provisioning and registration contract remains open. No installation,
SCM mutation, service logon, trust import, privilege change or real native service
acceptance has been performed. Issue #13 must stay open.

## Implemented seam

`server.RunObserved` adds typed Starting, Ready, Stopping and Stopped events to the
same lifecycle used by `server.Run`. Logging is not parsed to determine status.
Ready follows successful TLS/config/auth/state validation, listener binding and
namespace/file preparation. Stopping follows admission closure. Stopped is emitted
only after request owners, SQLite and exclusive ownership are released, including
startup-failure cleanup. An observer runs synchronously and must return promptly.

The Windows-only `service.Handler` implements the pinned x/sys v0.50.0 `svc.Handler`
protocol. It publishes StartPending before opening state; Running with Stop and
Shutdown acceptance only after Ready; StopPending when stop is requested and a
second checkpoint on real admission closure. Interrogate repeats actual status;
repeated stop does not reset progress; unsupported pause does not change state.
No timer invents checkpoints. A 35-second stop wait hint covers configured grace
(up to 30 seconds) plus cleanup target; it is not a hard-kill promise. Delayed
owners keep the service pending until joined, following the #12 contract.

The handler returns only after the shared runner has finished. x/sys/svc then
publishes Stopped with the returned code: clean 0; service-specific startup failure
1, serving failure 2, grace expiry 3, cleanup failure 4. Cleanup failure takes
precedence over expiry. There is no forced process exit or success on timeout.

The native dispatcher is not yet wired into the executable: doing so before the
approved service identity/registration contract would expose an incomplete
operational path. The handler tests use SCM protocol channels with the real
HTTPS/SQLite lifecycle, not an installed service and not a fake database.

## CLI validation shell

`install/check/start/stop/uninstall service` require explicit `--name`, restricted
to 1..64 ASCII letters, digits, period, underscore or hyphen. Install additionally
requires an absolute syntactically valid `--config` and explicit `--account`.
Duplicate/unknown flags, password arguments and control characters fail with
usage exit 2 and never echo supplied values. No paths are opened by validation.

On Windows, every syntactically valid operation currently exits 1 with the fixed
message that the identity and provisioning contract is pending. There are no SCM
manager calls or hidden successful installs. On other platforms valid service
commands exit 1 with an explicit unsupported message. These unavailable commands
are not advertised as working operations in the main help.

## Contracts still requiring a decision

- Select explicit service name/account, demand/automatic start policy and stable
  installation paths. Do not default to SYSTEM or silently elevate.
- Current private-state ACL policy allows only the executing user SID, SYSTEM and
  Administrators. Simply adding a different service account to owner-created
  state is rejected. Do not weaken this policy to make installation pass.
- LocalService uses one predefined account SID. A grant to that account would
  make it available to other processes using LocalService, not isolate separate
  GoDav installations. It is not selected as a default by this implementation.
- Reusing an ordinary same-account identity preserves per-user ACL semantics but
  needs supported service logon, an account password and logon-as-service rights.
  An administrator account is not a least-privilege choice. This code does not
  acquire, save, set, or log on with credentials or change account rights.
- A distinct virtual account can avoid password management, but requires an
  explicitly approved provisioning model and a way to preserve offline account
  administration/foreground fallback without granting unrelated principals
  access. No account, service SID policy, ACL or token change is made here.
- Installation must verify executable/config registration and safe paths without
  bootstrapping or changing data. Management must verify the intended registration
  before start/stop/delete, use narrow SCM rights, await true SCM state, and treat
  unregister as registration-only. Those manager operations are not implemented.
- Actual approved native SCM tests remain necessary: identity/logon/ACL access,
  start failure, stop/shutdown drain, persistence, stopped foreground fallback,
  uninstall preservation, and interrupted/repeated command handling.

Primary sources checked 2026-11-14:
- [Microsoft service user accounts](https://learn.microsoft.com/en-us/windows/win32/services/service-user-accounts)
- [Microsoft LocalService account](https://learn.microsoft.com/en-us/windows/win32/services/localservice-account)
- [Microsoft service accounts and virtual accounts](https://learn.microsoft.com/windows/security/identity-protection/access-control/service-accounts)
- [Microsoft CreateServiceW](https://learn.microsoft.com/en-us/windows/win32/api/winsvc/nf-winsvc-createservicew)

## Verification boundary

The fixture tests use temporary private directories, accounts and TLS roots in
client memory. No Windows trust change or service enrollment is part of any test.
Native Go executes on Zen-1000 using the exact project-local Go 1.27.4 and
GOTOOLCHAIN=local. Linux results are cross-build/test-compilation only unless a
separate runtime record explicitly says otherwise. Full verification results are
recorded separately; none of these tests closes native acceptance.
