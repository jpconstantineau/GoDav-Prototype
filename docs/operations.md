# Operations

Use this reference to initialize, manage accounts, run HTTPS or provision a Windows
service. Read [Testing](testing.md) before treating the prototype as accepted.

## Initialize and recover

Use a private local directory and private console:

    godav init config --config <path> --username <name>
    godav run server --config <path>
    godav add user --config <path> --username <name>
    godav reset password --config <path> --username <name>

Initialization defaults to godav.yaml; server startup requires explicit config.
Use godav help for current command grammar. Usernames are 1-64 ASCII characters:
alphanumeric first, then alphanumerics, period, underscore or hyphen. Comparison
is ASCII case-insensitive; display spelling and stable account identity survive.

Fresh initialization commits the database, publishes settings, then displays a
generated password once to the private console. Routine stdout/stderr, settings
and logs do not contain it. Choose a private terminal without transcripts or
recording. There is no password argument/export option.

Existing valid initialization is a preserving no-op. Incomplete, invalid or
unsupported state fails closed rather than being replaced. If settings publication
fails after database commit, retain the database and explicitly reconstruct
settings referencing it. If password delivery fails after commit, retain state
and use explicit password reset. Do not create a replacement database to compensate.

Account commands are offline: stop the server first. Add refuses duplicates;
reset refuses missing accounts and preserves identity, enabled status and resources.
Neither rewrites YAML, migrates or repairs state. Reset takes effect on next start;
there is no live reload.

## Settings and HTTPS

Generated settings:

    config_version: 1
    listen_address: '127.0.0.1:8443'
    public_origin: 'https://localhost:8443'
    dav_prefix: '/dav/'
    database_path: 'godav.sqlite'
    tls:
      cert_file: 'tls/server.crt'
      key_file: 'tls/server.key'
    authentication:
      method: basic
    limits:
      max_file_bytes: 33554432

Relative database/TLS paths resolve from the YAML directory, not working directory.
Settings are one strict bounded YAML document with known keys/types. Optional
limits.shutdown_grace_ms defaults to 5000; see [lifecycle limits](storage-and-lifecycle.md).

Initialization creates no certificate, listener, trust entry or service. Supply
protected certificate/key files valid for the HTTPS origin before serving.
The server uses loopback HTTPS, Basic authentication, HTTP/1.1 and TLS 1.2 or later.
No plaintext fallback or automatic client trust installation is provided.

Certificate hostname, configured origin and request authority must agree;
localhost and an IP address are not interchangeable aliases. Readiness follows
prerequisites, bind and state preparation, not merely process launch.
The [local TLS guide](../scripts/local-test-tls.md) covers disposable certificates,
not permission to change system trust.

## Windows service

Use stable executable paths and separate read-only program/settings/TLS from the
service-writable database directory. Registration is demand-start with a dedicated
virtual account, restricted service SID and only SeChangeNotifyPrivilege.
There is no SYSTEM/LocalService fallback, password option, automatic elevation or
permission repair.

All management commands require instance name, clean absolute config path and
explicit canonical operator SID. Install additionally requires the matching
NT SERVICE account for that instance. The operator must be the current effective
Windows user. Every later command verifies the same installed executable and
expected registration rather than adopting unexpected settings.

1. Select stable paths/operator; preserve existing state. Initialize only a new
   installation through the separate bootstrap workflow.
2. Install the stopped registration. Install does not start or initialize/open
   application state. Resolve the registered virtual-account SID through supported
   Windows tooling, rather than inventing one.
3. Provision and verify the split layout and narrow access below.
4. Start and verify real identity/access, HTTPS/authentication and persistence.
   [Testing](testing.md) lists remaining acceptance checks.
5. Stop before offline administration or uninstall. Uninstall confirms absence of
   the matching registration; data, settings, TLS, ACLs and ownership metadata stay.

These are operator procedures, not automatic setup. Registration, elevation,
identity/ACL provisioning and trust changes require explicit approval.

### Access and fallback

Both identities need explicit grants:
- Program/settings/TLS files and immediate directories: service Read/Execute,
  no service write/delete/control; owner is operator, SYSTEM or Administrators.
- Database, stable lock and mutable database directory: service/operator Modify.
  Mutable-directory inheritance must give both safe journal/lock access.
- SYSTEM/Administrators may retain full control. Unrelated private-state access,
  unsupported ACE forms and unsafe paths are refused rather than repaired.

Keep settings/TLS outside the writable data tree. This conservative policy is not
a general effective-access calculator or sandbox against trusted owners/admins.
Actual token/effective-access acceptance remains separate.

Stopped foreground fallback uses run server with paired --service-name and
--operator-sid flags. Offline account commands accept that pair too. Personal mode
does not silently accept shared ACLs. After uninstall, shared-mode use may require
re-registration so the virtual account resolves again.

### Status and diagnostics

Start/stop wait up to 45 seconds for observed completion; timeout neither proves
rollback nor forces a kill. Running follows readiness. Stopped follows joined work,
database closure and ownership release. Uninstall refuses running/pending state.

Check prints actual state and Windows/service-specific codes. Install and Check
accept stopped Windows code 1077 with service-specific zero as never-started,
still printing 1077. This is not successful Start or clean Stop. Unknown states
remain unknown. Usage errors exit 2, operational failure 1, success 0.
Service-specific failure codes: 1 startup, 2 serving, 3 grace expiry, 4 cleanup;
cleanup failure takes precedence.

Foreground traces go to stderr. Normal SCM hosting does not retain stderr.
No Event Log source or log file is automatically created. Retained service
diagnostics remain an acceptance gap.
