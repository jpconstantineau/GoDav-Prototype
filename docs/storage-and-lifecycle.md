# Storage and lifecycle

Use this reference when changing persistence, ownership or shutdown.
For provisioning read [Operations](operations.md); for resource semantics read
[WebDAV behavior](webdav-behavior.md).

## Ownership and evolution

One validated session retains private-path handles, exclusive OS-held ownership
and one SQLite connection. Both read-only and writable access retain exclusive
ownership. The stable empty <database>.lock is metadata, not a PID ownership test.
Leave it in place, exclude it from backup inputs, and release the OS lock on
close. Failed validation can leave that empty file.

Opening existing state never creates, migrates, repairs or reseeds it.
Explicit server preparation evolves supported namespace/content/metadata state
after static prerequisites and bind, before readiness. Bootstrap creates account
schema v1; supported server evolution reaches v4. Account/resource identities
survive supported migrations. Unknown/inconsistent state fails closed.
Validation and use retain the same connection rather than reopening it.

SQLite uses DELETE rollback journaling, FULL synchronization and zero busy
waiting, not WAL. Pending sidecars/unsupported state require explicit recovery,
not automatic adoption. Writable parents require safe file inheritance for
journals/locks. Settings/database paths stay pinned during the session.
Unsupported permissions/layouts are refused rather than repaired. Authorized
owners/administrators are trusted.

A cancellable operation gate serializes complete store operations. Stage bodies
and parse patches before acquiring it. Publish bytes, metadata, validators and
namespace changes transactionally; update process leases only after commit.
Detach snapshots before network output. Join users before closing SQLite, then
release ownership/path pins.

## Work budgets

| Resource | Prototype limit |
| --- | --- |
| Accepted transport connections (HTTP/1) | 128 |
| Admitted application requests, per server | 100 |
| Admitted requests, per authenticated account | 10 |
| Receiving PUTs | 2 |
| Concurrent account lookup/password verification | 10 |
| Store acquisition | 2 seconds |
| Application request context | 10 seconds |
| MCP active data operations / FIFO waiters / wait | 1 / 5 / 500 ms |
| Header size/count | 16 KiB / 100 values |
| Header read / request read-write / idle | 5 / 10 / 30 seconds |
| max_file_bytes | 32 MiB default; 1 byte through 64 MiB |
| Shared upload/read payload reservations | 128 MiB |
| shutdown_grace_ms | 5000 default; 1 through 30000 |

Excess admission does not enter an unbounded queue. Authentication permits return
before DAV work; upload permits return at EOF/error before publication. Handlers
remain counted until exit. Payload limits exclude database pages, driver copies,
HTTP/TLS buffers, authentication memory and small metadata.

The authenticated quota counts the stable verified account ID across DAV and
MCP, including body, queue, storage and synchronous response work. At capacity
it returns HTTP 429 with `GoDav-Rejection: account-requests`. Global
application exhaustion remains HTTP 503 with `requests`. Anonymous HTTP test
mode uses the global guard but has no authenticated account quota. The
listener accepts up to 128 HTTP/1 connections by default (including idle sockets).
In anonymous HTTP test mode, the global and transport defaults apply, but MCP
and account quotas are absent and connections are not kept alive.
Authentication remains fail-fast (`authentication` on slot exhaustion).
Each Argon2id verification requests 19 MiB working memory; ten simultaneous
derivations can require roughly 190 MiB before other allocations. This is
not a process RSS bound. Account lookup can still wait on the existing
single SQLite gate; ten authentication slots do not create ten store permits.
GET conservatively reserves 64 MiB per materialized snapshot. PUT reserves declared
size or configured maximum and still checks actual EOF/size. Metadata reads do
not reserve content payload. This is not a total process-memory guarantee.
Configure concurrency at server startup with an optional top-level block.
Omitted fields use these defaults; existing files do not need a migration.

```yaml
concurrency:
  max_connections: 128
  max_requests: 100
  max_requests_per_account: 10
  auth_slots: 10
  receiving_uploads: 2
  mcp_active: 1
  mcp_waiting: 5
  mcp_wait_ms: 500
  request_timeout_ms: 10000
```

All values are positive bounded YAML integers. Maximum override values, in
the same order as above, are 1024, 512, 128, 16, 32, 16, 32, 5000 and 10000.
A lower upstream bound can prevent a downstream quota from being reached.
The request timeout is the application context, not the separate socket
read/write deadlines; the queue wait is also bounded by that context.
Restart to change these settings. A server with MCP enabled rejects
`mcp_active` above the backend's safe capability. SQLite advertises 1; a
future backend may advertise a higher safe width, but this change neither
introduces PostgreSQL nor changes SQLite's one-operation/store invariants.
At the maximum of 16 verifiers, Argon2 alone can request about 304 MiB.
These ceilings are not a guarantee about total process memory.
Only documented configuration limits are operator settings.

## Shutdown and diagnostics

Stop closes admission/listeners and drains accepted work. The stop signal does
not itself cancel active requests. Grace expiry cancels requests/closes sockets,
then joins owners before releasing storage. Cleanup has a two-second target,
not permission to close beneath an active user. An unresponsive owner may retain
ownership longer: report failure, not graceful success. Clean foreground stop
exits 0; expiry/failure exits 1.

Routine diagnostics use fixed errors and allowlisted request/lifecycle fields.
Credentials, verifiers, tokens, raw headers, property values and bodies are
excluded. Storage/writer/payload metrics remain a plan, not a metrics endpoint.
Service retention differs from foreground stderr; see [Operations](operations.md).

Normal restart persistence and synthetic precommit rollback do not prove power-loss
durability, hot-journal recovery or real disk-failure safety. Cancellation after
successful commit cannot reverse it.
