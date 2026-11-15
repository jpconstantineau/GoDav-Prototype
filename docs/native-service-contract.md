# Native Windows service contract (slice 12)

Issue: https://github.com/jpconstantineau/GoDav-Prototype/issues/13.

This supersedes the implementation limits in the historical
[native-service preparation](native-service-preparation.md). The dedicated
per-instance identity and separate operator-access design is approved.
**Actual SCM, service-token, ACL and native-client acceptance remains pending.
Do not close #13 or call this prototype ready on the strength of fake-SCM tests.**

## Native identity and command contract

The same binary uses pinned x/sys v0.50.0 directly. No wrapper service, shell
commands, credential prompts, SYSTEM/LocalService fallback or automatic elevation
is used. A name is 1..64 ASCII letters, digits, period, underscore or hyphen.

All service commands require:
- `--name <instance>`
- `--config <clean absolute settings path>`
- `--operator-sid <canonical ordinary Windows user SID>`

Install additionally requires `--account "NT SERVICE\<instance>"`, exactly matching
the name. No password option exists. The executable path is the absolute path of
the running GoDav binary; every later management command must use that exact
installed binary and the same expected config/operator/name. Moving a binary
requires unregistering the stopped old registration first, not adopting an
unexpected registration.

The operator must resolve to a Windows user and equal the current effective token
user. Being an unrelated administrator is not enough. An already elevated instance
of the explicit operator may be needed for SCM permissions, but the program never
requests elevation or changes service-object permissions. Insufficient rights fail.

Registration is own-process, demand-start, with a unique per-instance virtual
account, NULL password, restricted service SID and only SeChangeNotifyPrivilege.
It has no dependencies, load-order group, recovery actions or triggers. Management
verifies all these settings and the complete quoted executable/argument string
before acting. Unrecognized settings are not repaired or adopted.

Install validates and pins the executable plus its immediate directory against
untrusted modification, and pins the existing settings path against replacement.
It does not read settings contents, open SQLite/TLS, create directories, initialize
accounts, grant permissions, import trust or start the service. Runtime validation
comes after separate provisioning. A policy setup or verification failure attempts
to delete only the newly created registration; rollback uncertainty is reported,
never called success. After valid registration, a failed status query leaves that
registration in place and explicitly reports that its state could not be confirmed.

Start and stop wait up to 45 seconds for observed Running/Stopped, not merely
acceptance of a control request. Timeout does not force termination or promise the
operation was rolled back. Start is idempotent when already Running, and Stop when
cleanly Stopped. Uninstall refuses a running/pending service and only deletes its
matching registration, checking absence after closing its own handle. It does not
remove data, settings, certificates, ACLs or the stable ownership file.

Check prints actual state and both Windows/service-specific exit codes. Usage
errors exit 2; operational errors and nonzero reported service exit codes exit 1,
with one status-only exception: Install and Check succeed when the observed state
is Stopped, the Win32 code is 1077 (ERROR_SERVICE_NEVER_STARTED), and the
service-specific code is zero. SCM uses 1077 when no start has been attempted
since boot; a manual registration need not be started to install or inspect it.
The actual 1077 remains in output. Manager errors still fail, and 1077 never
counts as successful Start or Stop completion. Stop's existing cleanly-Stopped
idempotency is unchanged: a never-started service is not a clean-stop result.
Uninstall still succeeds only after confirming registration absence, irrespective
of the prior stopped status code. Successful actions exit 0. State text is stopped, start-pending, stop-pending,
running or unknown. Unknown SCM states are never relabeled Running or Stopped.
Non-Windows service management reports unsupported and never opens supplied paths.

## Explicit shared-file layout

Runtime binds `NT SERVICE\<instance>` to its actual Windows SID. It never computes
or trusts an arbitrary service SID supplied through the CLI. Native hosting requires
the effective token user to equal that SID; foreground/offline administration
requires the explicit operator. The internal `--scm` invocation is refused outside
a Windows service process, before opening application state.

Private service policy is session-scoped. Nil/zero policy keeps personal-mode
behavior unchanged; shared ACLs do not silently become accepted in personal mode.
Both explicit SIDs must appear directly in each relevant ACL, not merely via group
membership:
- Executable, settings, TLS files and immediate directories: service RX
  (FILE_GENERIC_READ | FILE_GENERIC_EXECUTE), no service write/delete/control.
  Owner is the explicit operator, SYSTEM or Administrators.
- DB, stable lock and mutable DB directory: service and operator Modify
  (0x1301bf). FILE_DELETE_CHILD is permitted additionally on directory grants.
  DB directory must inherit the required file rights to both identities for lock
  and SQLite rollback-journal creation, including a read-only session that may
  create its first ownership file.
- Explicit service WRITE_DAC, WRITE_OWNER, full control and excess rights are
  rejected, including inherit-only ACEs. Mutable files may be service-owned.
  Windows owners retain implicit DACL authority: these ACE restrictions are not
  a sandbox against a malicious data owner.
- SYSTEM and Administrators may retain full control. Private state rejects
  unrelated principals, deny/object/unmodeled ACEs, unsafe reparse/hardlink paths
  and missing explicit access rather than approximating complex effective access.
  Installer code-integrity checks allow unrelated RX but never unrelated write;
  runtime's private code policy is stricter.
- Settings/TLS and their directories must be outside the service-writable DB tree.
  Place program/read-only material and mutable state in separate directories.
  All path components must be accessible for data/list reads; retained handles
  prevent replacement through the session, not merely check metadata once.
  No function repairs permissions. Distinct validated local volumes are disjoint;
  each path still undergoes its own canonical/no-reparse checks. Real cross-volume
  session acceptance is pending because the test host exposes one usable volume.

The explicit operator is trusted to provision/update the code, settings, TLS and
data. Service data ownership does not confer permission to alter code/settings.
Threat assumptions continue to exclude hostile authorized owners/administrators.
A baseline observation found metadata-only legacy personal-mode handles did not
exclude rename on this Windows host. New service/program pins use read/list access
and have separate rename/release coverage. Personal-mode behavior is intentionally
unchanged and this observation is not a claim of an exploited or lost dataset.

## Manual setup and acceptance, not executed here

Each real registration, ACL/service identity change, elevation, credential/trust
step and actual SCM start/stop requires its applicable separate authorization.
The design approval does not authorize these actions. These are operator steps,
not an automatic setup script:

1. Choose a unique service name and explicit operator SID, plus stable absolute
   program/settings/TLS/data paths. Confirm the intended paths and minimal grants.
   Initialize new accounts only through the separate existing bootstrap workflow,
   or preserve existing state. Do not rerun initialization as service setup.
2. Install the stopped demand-start registration using the explicit virtual account
   and operator. Windows may not resolve a virtual account before its service exists;
   do not invent a SID or broaden grants to work around this.
3. Resolve the registered virtual account SID using supported Windows tooling.
   Separately provision narrow operator/service access and the split layout above,
   including inherited journal/lock access. Verify code/config remain service
   read-only. The application validates but never performs this provisioning.
4. Test stopped foreground fallback using
   `run server --config <absolute> --service-name <instance> --operator-sid <sid>`.
   Offline `add user` and `reset password` accept the same paired service flags
   and retain the private-console delivery contract. No credentials enter argv.
   Omit those flags only for ordinary personal-mode state.
5. Start through SCM and independently verify real token user, restricted SID,
   privileges, filesystem access, TLS/auth readiness and truthful SCM transitions.
   Exercise startup refusal, busy DB, pending/repeated controls, graceful drain,
   grace expiry, persisted auth/data and service/foreground exclusion.
6. Stop, test foreground fallback, then unregister and verify all data/config/TLS,
   file identities/ACLs and ownership artifact remain intact. No ACL cleanup is
   implicit. Shared access still needs the virtual account name to resolve; after
   unregister, re-registration/provisioning may be needed before shared-mode use.
   Do not claim post-uninstall personal mode can read deliberately shared ACLs.
7. Record exact binary identity and real evidence before closing #13. Tests with
   protocol channels or synthetic SID ACLs do not prove service logon or restricted
   token access, even when they exercise actual HTTPS, ConPTY and SQLite.

The shared typed lifecycle provides StartPending before opening state, Running
only after validated TLS/state, bound listener and prepared routes; StopPending
on a stop request and a second real checkpoint after admission closes. Stopped
follows joined work, SQLite close and ownership release. Service-specific codes:
1 startup, 2 serving, 3 grace expiry, 4 cleanup failure; cleanup failure wins.
No timer manufactures progress. Wait hints do not promise a hard-kill deadline.

The foreground runner still emits safe traces to stderr. A normal SCM service
does not provide retained stderr capture. This implementation does not create an
Event Log source, log file or log permissions automatically. An approved retained
service diagnostic setup and its acceptance are still pending; SCM check/exit codes
are available but are not a substitute for retained per-request traces.

## Primary references

- [CreateServiceW virtual account and NULL password](https://learn.microsoft.com/en-us/windows/win32/api/winsvc/nf-winsvc-createservicew)
- [Service accounts and virtual accounts](https://learn.microsoft.com/windows/security/identity-protection/access-control/service-accounts)
- [Restricted service SID policy](https://learn.microsoft.com/en-us/windows/win32/api/winsvc/ns-winsvc-service_sid_info)
- [Required privileges policy](https://learn.microsoft.com/en-us/windows/win32/api/winsvc/ns-winsvc-service_required_privileges_infow)
- [SCM query status and never-started code 1077](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-scmr/cf94d915-b4e1-40e5-872b-a9cb3ad09b46)
