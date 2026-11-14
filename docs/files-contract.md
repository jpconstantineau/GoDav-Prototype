# Atomic file saves and reads (slice 05)

Issue: https://github.com/jpconstantineau/GoDav-Prototype/issues/6.
Base: 87d5af21bf529f6016cd526deee96552c1528daa. Tests use real authenticated
HTTPS and temporary real SQLite, executable restart, and an in-process server
for race instrumentation. No trust changes, service registration or native-client
acceptance is implied. Use the existing verified Go 1.27.4 and module pins.

## Publication and storage

Custom PUT stages a complete body before entering the store operation gate.
EOF, actual size, request cancellation and input errors are checked before a
single transaction publishes content and metadata. Close never publishes.
New files return 201, replacements 204, only after commit. Replacement preserves
node ID, creation time and display spelling even when addressed in another case.
Concurrent unconditional saves serialize; the last committed whole save wins.
Separate successful empty PUTs are valid placeholders, never rolled back because
a later request fails. Cancellation after commit cannot undo that committed save.

A v3 migration adds bounded file content and metadata to the v2 stable namespace.
Presence of a content record identifies a file; other nodes remain collections.
Migration is explicit after startup prerequisites/bind, never in an existing-state
read or account operation. V1 first receives the existing v2 migration. Accounts,
folder IDs and spelling survive. Unknown/conflicting schema fails closed.
Retain DELETE journal, FULL synchronization, foreign keys and zero SQLite busy
timeout. No WAL conversion, additional connection or implicit recovery. An
existing sidecar remains an explicit-recovery refusal; no crash/power-loss claim.

## Bounds and failure mapping

Configured max_file_bytes defaults to 33554432, accepts 1 through 67108864 bytes.
The process-wide file service reserves at most 134217728 bytes for simultaneous
application-owned upload buffers and read payloads. PUT reserves/allocates its
declared size when known, otherwise its configured maximum; it always probes
actual EOF and rejects excess bytes. GET conservatively reserves 64 MiB before
materializing one snapshot. HEAD/PROPFIND reserve no payload. Reservations are
released on every exit, including cancellation and failures. No disk staging.
Database pages, driver transient copies, HTTP/TLS buffers, authentication memory
and small metadata/probe allocations are additional to this payload bound.

Four transport admission slots remain, without an unbounded waiting queue. Store
operation acquisition is cancellable and limited to two seconds, including reads.
Staging owns no SQLite gate. A slow network reader owns only its detached snapshot.
Existing ten-second transport read/write deadlines remain. Broader configurable
admission and graceful lifecycle policy belongs to #12.

Missing reads are 404; missing/non-collection parents are 409; collection/root PUT
is 405; invalid paths and file trailing slashes are 400. Size overflow is 413,
incomplete input 400, receive timeout 408 where a response remains possible.
Capacity/admission/acquisition/cancellation is 503, namespace child capacity 507,
other staging/storage faults fixed generic 500. Disconnected clients may receive
no response; fresh readback determines committed state. SQL/paths/secrets never
enter error bodies or logs.

## Read and metadata contract

GET returns a complete committed snapshot, releasing the database before network
output. HEAD returns matching length/type/ETag/Last-Modified and no body or payload
load. PROPFIND includes file kind, display name, length, type, modification time
and the same validator without opening content. Creationdate/dead properties
remain #7 work. Collections retain #5 metadata and have no file content or ETag.
Times remain UTC seconds. Strong quoted random per-version ETags are committed
with content, survive reopen and change on every successful PUT, including equal
length and same-second replacement. Failed saves change none of those fields.
Content-Type is a singular valid ASCII media type of at most 255 bytes, or defaults to
application/octet-stream. Non-identity Content-Encoding is rejected with 415;
the server never transforms bytes or silently decodes content.

Use stdlib ServeContent for GET/HEAD conditions, with stored type and validator.
Single byte ranges support bounded/open/suffix forms and 206 Content-Range.
Multiple ranges or unknown units are ignored (full 200); malformed/unsatisfiable
single ranges are 416 for nonempty files. Empty-file ranges use stdlib's full 200.
Range is ignored for HEAD. If-Range mismatch produces a full response. Date
If-Range is conservatively a full response because second-resolution stored
times do not establish a strong validator. Conditional 304/412 reads use metadata
without content; when a body is needed its complete snapshot is checked again.
Date
validators retain HTTP second precision; ETags distinguish rapid changes.

Until #7/#8, PUT with If-Match, If-None-Match, If-Modified-Since,
If-Unmodified-Since, If-Range, DAV If or Lock-Token returns 501 without mutation.
PUT Content-Range is rejected with 400 (no partial PUT). No locking or conditional
write support is implied. OPTIONS adds GET, HEAD and PUT only; no DAV compliance
class, PROPPATCH, LOCK/UNLOCK, MOVE, DELETE or COPY claim.

## TDD and fault evidence

One public failing behavior, minimal implementation, then the next. Independent
fixture bytes, fresh HTTP metadata/readback and executable reopen are the oracle,
not private table values or helper calls. Build-tag-only staging, payload-read
and precommit failures may be used in explicitly built disposable binaries.
They have no release flag/environment/header control. A synthetic precommit
failure demonstrates rollback, not actual device failure or crash recovery.
Controlled paused bodies, cancellation, capacity reuse and concurrent whole-save
fixtures cover cleanup and unrelated progress. Final independent Spec/Standards
reviews and Windows native/source/race/release gates are recorded separately.
