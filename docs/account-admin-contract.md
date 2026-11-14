# Offline account administration contract (slice 10)

Sources: specification #1, ticket #11, and `bootstrap-contract.md`.

`godav add user [--config <path>] --username <name>` and
`godav reset password [--config <path>] --username <name>` require existing valid
settings/accounts. Config defaults to `godav.yaml`; paths resolve as in bootstrap.
The shared ASCII case-insensitive username policy applies. Add refuses an existing
comparison key. Reset refuses a missing account and changes only its verifier,
retaining stable ID, display spelling, enabled status, creation time and resources.
YAML is never rewritten. No initialization, migration, repair or reseeding occurs.

Both commands generate a fresh password using bootstrap's 24-byte random,
32-character base64url contract and its fixed salted Argon2id profile. Generation
and hashing occur before the short SQLite transaction. No hash upgrade is implicit.
There is no password input, argv/export option or echo. A real private console is
required before ownership acquisition. Only after commit, `Password: ...` is
written once to that console, never routine stdout/stderr or metadata. Ordinary
output is a fixed success message. If delivery fails after commit, exit 1 and
instruct the operator to reset explicitly; committed state is never rolled back
or reseeded to compensate for lost delivery.

Unknown/duplicate flags, positional/password arguments and invalid usernames exit
2 without echoing values. Operational failures exit 1; successful mutation and
delivery exit 0. Diagnostics contain no database/driver detail or secret values.
An existing database with missing YAML fails closed; reconstruct YAML explicitly.

The same `localstate.OpenExisting(..., WriteExisting)` session owns path validation,
private directory/file checks, the stable OS-held exclusive lock and one validated
SQLite connection. All account mutation methods require writable access and use
that retained connection. The database parent must provide safe file inheritance
for SQLite journals, even when existing files are private. Ownership lasts through
transaction, secret delivery and SQLite close. Concurrent initialization/server/
account administration is refused, not silently accepted as a second writer.
Offline reset takes effect on the next server start; no live reload/cache promise.

## Approved verification seams

Tests invoke the actual executable against new private disposable Windows fixtures
with bounded ConPTY processes and memory-only credential capture. Verify errors,
private delivery, duplicate/missing behavior, settings persistence, noninteractive
refusal, unsafe state/inheritance refusal, process ownership and recovery. Use
observable CLI behavior and state preservation, not assertions about private SQL.
Use the existing test-build-only console failure tag for deterministic failed
post-commit delivery; no production fault control is added. Linux is compiled,
not claimed as a runtime pass.

Real HTTPS authentication after #4 must establish add-account usability, old/new
reset passwords, disabled-account behavior and stable authenticated identity.
Resource-data preservation requires later DAV fixtures. Neither is claimed by the
initial CLI pass. No real owner account, trust, service or global setting changes.
