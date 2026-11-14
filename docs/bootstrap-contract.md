# Bootstrap contract (slice 02)

Sources: [specification #1](https://github.com/jpconstantineau/GoDav-Prototype/issues/1)
and [ticket #3](https://github.com/jpconstantineau/GoDav-Prototype/issues/3).
Recorded 2026-11-14 before bootstrap tests. This is the implementation target,
not a claim that the complete bootstrap is implemented or verified.

## Public interface and test seam

`godav init config --config <path> --username <name>` explicitly initializes a
fresh installation. Config defaults to `godav.yaml`. Username is explicit,
1–64 ASCII characters, an alphanumeric first character followed by alphanumeric,
period, underscore or hyphen. Matching is ASCII case-insensitive; retain display
spelling and a separate stable account ID. Reject unknown/duplicate flags,
positional arguments, password arguments and password export options without
echoing their values. Usage errors exit 2, operational failures exit 1, successful
initialization or an existing-valid-state no-op exits 0.

The approved seam is the actual executable CLI with disposable fixture state.
Observe exit status, nonsecret output, settings, persistence and restart. Do not
assert private SQL layout. Later real HTTPS authentication in slice 03 establishes
credential usability; password reset belongs to slice 10 / issue #11. Neither is
proved by this slice alone. Work one failing behavior and implementation at a time.
Approved malformed-state fixtures may use bounded SQLite metadata to remove
schema constraints or shadow metadata names. Their assertions remain actual CLI
refusal and unchanged persistent state, not private helper calls or SQL formatting.

Development uses the verified project-local Go 1.27.4 toolchain through
`GoDav-Project-Tools/Use-VerifiedGo.ps1` in disposable shells. Do not change
persistent PATH or the older installation. Audited pins are SQLite v1.62.0 paired
with libc v1.80.1, x/crypto v0.59.0 (Argon2), YAML v3.0.6 and x/sys v0.50.0
(Windows APIs). Module language baseline is Go 1.27.0. Use process-scoped `GOTOOLCHAIN=local` and never
automatically download a different toolchain.

## Settings

Generated settings use this exact shape and defaults:

```yaml
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
```

Resolve relative DB and TLS paths against the YAML directory, not the process
working directory. Read one YAML document of at most 64 KiB with known keys and
exact scalar types. Reject unknown/duplicate keys, aliases, merge keys, custom
tags, additional documents and credential/account fields. Validate loopback
binding, HTTPS origin and path conflicts. Initialization permits missing TLS
assets; serving validates them later. Initialization creates no listener, TLS
assets, trust entry or service registration, and repairs no permissions.

## Ownership, state and publication

The database directory must already be private. Windows initialization applies a
conservative owner/ACE allowlist permitting the current-token user, SYSTEM and
Administrators only; it is not a complete effective-access calculation. Refuse
broader or unrecognized grants rather than repairing permissions. Newly created
test fixtures use a protected DACL at creation; no parent directory ACL changes.

Fresh provisioning additionally requires at least one nonzero file-inheritable
allow ACE in the restricted DACL of each retained settings/database parent. A
private directory with only non-inheriting or directory-only inheritance ACEs
is refused before state writes. This ensures the DB, temporary YAML and SQLite's
own rollback journal inherit restricted grants at creation, rather than falling
back to the process token's default DACL. Checking only the new main file after
creation would not protect journal creation. Existing read-only validation does
not require directory inheritance when the directory and existing files already
pass their private owner/DACL checks. No existing permissions or token defaults
are changed. See Microsoft's [new-object DACL algorithm](https://learn.microsoft.com/en-us/windows/win32/secauthz/dacl-for-a-new-object)
and [independent child-file access rules](https://learn.microsoft.com/en-us/windows/win32/fileio/file-security-and-access-rights).

Fresh provisioning validates the real private console before any state write,
including creation of an ownership artifact. Existing-valid no-op does not
require a console; it may create/acquire stable empty lock metadata, but leaves
DB, YAML and credentials unchanged. The lock contains no content and is excluded
from backup inputs. Its path is `<database_path>.lock`; leave it in place rather
than deleting it for recovery. Existing-state validation can also leave this
empty metadata if the database is subsequently rejected. This is not a claim
that every failure leaves the entire directory unchanged. Check its location and
reject unsafe links. Reject unsafe path aliases
and reparse targets. Acquire OS-held exclusive ownership, recheck state under
ownership before opening or creating SQLite, and hold it through publication and
secret delivery. The ownership artifact is stable, not a PID-file-only check;
release the OS lock on every exit without unlinking a lock another process could
hold. Competing initialization fails without mutation.

| YAML | Target database | Result |
| --- | --- | --- |
| Absent | Absent | Fresh initialization only |
| Valid | Valid initialized schema/account | Preserve state; no-op, no secret |
| Absent | Existing | Refuse reseeding; retain DB and reconstruct YAML explicitly |
| Existing | Missing, invalid, partial, corrupt, unsupported or inaccessible | Fail unchanged |
| Invalid or inaccessible | Any | Fail unchanged |
| Any | Owned by another process | Fail without mutation |

An absent database with an existing `-wal`, `-journal` or `-shm` sidecar is partial
state, never a fresh installation. Refuse without deleting/adopting sidecars or
opening a replacement database. Existing databases and their sidecars must be
validated and protected under the same ownership/path checks.

Version 1 is the initial supported schema. Do not invent legacy migrations or
adopt an unversioned/partial SQLite file. Future unsupported schema versions fail
closed; no fallback database path and no reseeding of existing state.

Existing state also requires the ordinary STRICT account table's required typed,
non-null fields, sole ID primary key, nonpartial single-column binary username-key
uniqueness, enabled IN (0,1) check and absence of account triggers. Validate via
SQLite metadata plus bounded CHECK-token recognition, not cosmetic SQL text
equality or replaying stored SQL. This recognizes the selected predicate, not
every logically equivalent SQL expression. Encoded account fields are byte-bounded
with octet_length before materialization (SQL length(TEXT) stops at NUL), then
checked again before Go decoders. These checks do not prove a total process-memory
bound for arbitrary hostile SQLite files.

Read STRICT/table-kind metadata through the actual schema-qualified
`PRAGMA main.table_list('accounts')`, not a bare `pragma_table_list` relation
that a stored table can shadow. Other metadata queries use table functions with
arguments; ordinary tables/views shadowing these names fail closed. No stored SQL
is executed by production validation. See [SQLite PRAGMA documentation](https://www.sqlite.org/pragma.html#pragma_table_list).

Generate the password and verifier outside the DB writer transaction. Commit the
initial versioned schema and account atomically. Then publish complete YAML
exclusively without replacing existing files, and only after durable DB/YAML
setup disclose the committed password once. DB transaction failure does not
announce a secret. YAML publication failure preserves the committed database for
recovery. Secret-output failure preserves both stores and requires a later
explicit password reset, never replacement bootstrap. Normal restart durability
must not be described as proved power-loss safety.

Bootstrap uses one SQLite connection, DELETE journaling, FULL synchronization
and zero busy waiting under exclusive process ownership. It does not enable WAL.
Flush the closed committed database and completed temporary settings file before
exclusive same-directory publication. Windows write-through rename is not a
claim of portable directory fsync or proved parent-name survival across power loss.

## Secrets and recovery

Generate 24 `crypto/rand` bytes encoded as 32 unpadded base64url characters. Use
salted Argon2id version 19, 19 MiB memory, two iterations, parallelism one, a fresh
16-byte salt and a 32-byte hash, subject to dependency audit and measured bounded
cost verification before implementation. Store only a bounded encoded verifier
and stable account identity in SQLite. No plaintext secret or verifier enters
YAML, argv, routine stdout/stderr, logs or error text.

Windows secret delivery writes directly to the validated private `CONOUT$`
console, not the CLI's routine writers. Never introduce a production secret export
flag. Reject headless/no-console launches using console-window and output-mode
checks: Windows can expose console devices even to CREATE_NO_WINDOW processes.
ConPTY has a message-only console window handle. These checks do not prove a
human is observing or that a remote terminal is private; the operator must choose
a private terminal and avoid transcripts or screen recording.
Positive CLI tests require test-owned Windows ConPTY, disposable accounts,
memory-only secret capture, sanitized failures, bounded process deadlines and
complete process/handle cleanup. No real owner access is provisioned by tests.
The agreed additional failure seam is a narrowly injected I/O adapter in
test-build-only code for deterministic post-commit YAML-publication and private
console-write failures. Use distinct build tags; no normal-release flag,
environment variable or interface enables it. An explicit release configuration
regression must prove fault controls are excluded. Tests still invoke the actual
CLI and assert observable state preservation, never private SQL layout. No broad
fault framework is authorized.

For an existing DB with missing YAML, retain the DB and have the operator
reconstruct settings referencing its original path. Do not add a recovery/reset
command here, delete state, choose another DB or silently create credentials.
If private delivery failed after commit, retain the state for explicit reset in
issue #11. Automatic foreground bootstrap remains unresolved for issue #4;
unattended service startup always fails closed when provisioning is incomplete.
