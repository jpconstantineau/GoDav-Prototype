# Existing local state foundation

Approved behavior-preserving preparation for server issue #4 and offline account
administration issue #11, based on reviewed bootstrap commit `76f51b0`. Neither
feature is implemented here. Existing CLI tests and command output stay unchanged.

## Interface and lifecycle

`internal/localstate.OpenExisting(ctx, configPath, intent)` returns an owned
`*Session` or a fixed nonsecret error. `Intent` is a `uint8`: `ReadOnly = 1`,
`WriteExisting = 2`; zero and all other values fail before any access or write.
The session exposes a value-copy `Settings() config.Settings`, an opaque
`Accounts() *sqlite.AccountStore`, and idempotent `Close() error`. There is no
raw DB, SQL, transaction accessor or in-place intent upgrade.

`internal/sqlite.OpenExistingReadOnly(ctx, path)` and
`OpenExistingForUpdate(ctx, path)` return the opaque account store. They require
the path/privacy checks and exclusive ownership already held by localstate.
They never create, migrate, repair or reseed. They retain one `*sql.Conn` from
opening through close and validate schema, integrity and account invariants on
that connection, without a validate-close-reopen gap. Feature-specific methods
will be added separately with their consumer tests.

Opening validates raw settings path syntax before normalization, checks private
existing YAML before reading, loads strict settings, pins private settings and
database parents, and acquires the stable `<database>.lock`. Under ownership it
rechecks YAML privacy and unchanged settings, private existing DB and absent
`-wal`, `-shm`, `-journal` files. Before **any SQLite open**, the fixed 100-byte
header must contain the SQLite signature and rollback format bytes 1/1. No
journal conversion or immutable shortcut is used.

Both intents retain exclusive ownership. ReadOnly uses `mode=ro` and keeps the
existing exception for private non-inheriting parents with already-private files.
WriteExisting uses `mode=rw`, FULL synchronization, zero busy waiting and rollback
journaling. The retained DB parent must have restricted file-inheritable ACEs
before a writable connection can create journals. A separate YAML parent is
read-only and need not have file inheritance. No existing ACL or token default
is changed. Shared Windows primitives are narrowly extracted to `internal/localfs`.

The opening context cancels opening and its SQL validation. It never silently
closes a successfully returned session. Callers must drain users and explicitly
close the session. Close attempts DB closure before releasing ownership and parent
pins, never deletes the stable lock and reports only fixed nonsecret errors.
Failed opening attempts all acquired-resource cleanup. Future account commands
must retain the session through commit **and** private secret disclosure.

Bootstrap's existing-state no-op uses ReadOnly. Fresh bootstrap still checks the
private console before any write, requires safe inheritable parents, commits the
schema/accounts before publishing YAML, and discloses secrets last. Fresh
publication, delivery and recovery sequencing remain in bootstrap.

## Approved tests and limits

Minimal foundation API contract tests are approved where a public CLI consumer
does not yet exist. They live alongside the existing CLI helpers to reuse only
test-owned ACL/ConPTY fixtures. They use actual temporary SQLite, fixed error
assertions, file bytes/identity/ACL preservation, OS handle probes and the actual
CLI as an independent ownership contender. No mocks, private SQL assertions or
production test hooks are added. New DACLs are set only when fixtures are created.

Coverage includes invalid intent/no writes, both intent lifecycles, settings
copies, retained ownership after opening-context cancellation, retained DB handle
until close, repeated close, private non-inheriting parent behavior, separate
read-only YAML parents, malformed state refusals and complete error cleanup.
Existing command tests continue to cover fresh setup and unchanged recovery,
closed real WAL rejection, malformed schema/accounts and release fault exclusion.

OS handle probes establish retention and release, not every intermediate driver
event. The no-reopen guarantee also follows from retaining `sql.Conn` rather than
issuing operations through a reconnecting pool. This foundation has no account
mutation API, so no journal write, commit, future operation cancellation, server
shutdown or secret-reset behavior is claimed. Linux is build/scan-only; hostile
owner swaps, actual power-loss durability and denied hardlink/junction cases
remain unproved and are not retried here.
