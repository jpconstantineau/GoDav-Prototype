# Never-started service reporting correction (#13)

Date: 2026-11-15 UTC. Base integration:
190efc3bce4d2e3b49fc36f0b89d7f45c1287267.

## Evidence and correction

The separately authorized native rehearsal registered a stopped demand-start
service successfully, but Install returned exit 1 with empty stderr and stdout:

```text
service: stopped
win32-exit: 1077
service-exit: 0
```

The synthetic regression replays that status through the real service manager
and CLI result reporter. Before correction, it reproduces the exact exit 1 and
output, despite successful registration and no start attempt. Check reproduced
the same classification defect separately.

[Microsoft's SCM query specification](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-scmr/cf94d915-b4e1-40e5-872b-a9cb3ad09b46)
requires 1077 when no service-start attempt has occurred since boot.
[The Windows error reference](https://learn.microsoft.com/en-us/windows/win32/debug/system-error-codes--1000-1299-)
identifies it as ERROR_SERVICE_NEVER_STARTED. It is not evidence that creation
failed. The existing manager already accepts a confirmed stopped registration;
the defect was the CLI's unconditional rejection of any nonzero status code.

The correction only changes CLI command outcome for Install and Check with
Stopped/1077/0 after a successful manager result. It preserves the real status
in output and leaves manager production code, registration policy, native
adapter, account and filesystem checks unchanged.

Start still requires observed Running with successful status. A post-start 1077,
a contradictory state, other Windows or service-specific errors, and manager
errors remain failures. Stop retains its existing cleanly-Stopped contract;
this change does not expand its idempotency to never-started status. Uninstall
retains its verified-absence condition and can remove a never-started service.

## Focused verification

Exact project-local Go 1.27.4, Windows amd64, GOTOOLCHAIN=local, CGO_ENABLED=0.
Logs use the project-tools prefix `service13-never-started-`:

- 01-install-red: regression failed with exact native exit/output.
- 02-install-green: Install passed with original status preserved.
- 03-check-red: independent Check regression failed with exit 1.
- 04-check-green: Install and Check passed.
- 05-reporting-regressions: all synthetic reporting cases passed.
- 06-focused-final: manager and reporting tests passed on final code.
- 07-focused-vet: affected CLI and service packages passed vet.

Regression cases cover the exact status exception, contradictory states,
nonzero service-specific and other Windows errors, manager close errors,
successful first Start, failed and pending Start, unchanged Stop behavior, and
both confirmed and unconfirmed Uninstall absence. They use a synthetic backend;
no test calls ManageNative or accesses real SCM, accounts, ACLs or certificates.

No full suite, race gate, release build, vulnerability gate or new native
rehearsal was run for this focused correction. These focused checks do not prove
native acceptance. The original native worker owns fixture cleanup. Issue #13
remains open pending reviewed integration and separately authorized acceptance.
