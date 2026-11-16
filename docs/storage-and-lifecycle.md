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
| Accepted transport connections | 16 |
| Admitted application requests | 4 |
| Receiving PUTs | 2 |
| Concurrent account lookup/password verification | 2 |
| Store acquisition | 2 seconds |
| Application request context | 10 seconds |
| Header size/count | 16 KiB / 100 values |
| Header read / request read-write / idle | 5 / 10 / 30 seconds |
| max_file_bytes | 32 MiB default; 1 byte through 64 MiB |
| Shared upload/read payload reservations | 128 MiB |
| shutdown_grace_ms | 5000 default; 1 through 30000 |

Excess admission does not enter an unbounded queue. Authentication permits return
before DAV work; upload permits return at EOF/error before publication. Handlers
remain counted until exit. Payload limits exclude database pages, driver copies,
HTTP/TLS buffers, authentication memory and small metadata.

GET conservatively reserves 64 MiB per materialized snapshot. PUT reserves declared
size or configured maximum and still checks actual EOF/size. Metadata reads do
not reserve content payload. This is not a total process-memory guarantee.
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
