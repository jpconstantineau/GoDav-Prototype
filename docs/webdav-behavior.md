# WebDAV behavior

Use this reference when selecting clients or changing protocol semantics.
This bounded prototype makes no universal DAV compliance claim. Detailed cases
live in executable tests linked from [Testing](testing.md).

## Surface and names

Supported methods: OPTIONS, PROPFIND, MKCOL, GET, HEAD, PUT, PROPPATCH, LOCK,
UNLOCK, MOVE and DELETE, subject to resource type. Root deletion/replacement is
refused. COPY and Microsoft combined extension commands remain unsupported.
OPTIONS reflects allowed methods; no universal DAV class or X-MSDAVEXT is advertised.

Both /dav and /dav/ identify the authenticated root; canonical href is /dav/.
PROPFIND supports Depth 0/1, not unbounded recursion. MKCOL creates one child under
an existing collection, never missing parents. Names are DAV resources, not host
filesystem paths. Hrefs preserve stored ancestor spelling, escape once and use
a trailing slash for collections.

Comparison is persisted simplefold-min/no-normalization/v1 with Unicode 17.0.0,
not NTFS equivalence or full/locale-sensitive folding. Composed/decomposed accents
remain distinct. A comparison-version change requires explicit collision-aware
migration. Names obey the conservative Windows component policy.
Limits: 255 UTF-8 bytes/name, depth 32, decoded relative path 4096 bytes,
1024 immediate children.

## Files, metadata and conditions

PUT stages the entire bounded body before publication. New files return 201;
replacement returns 204 after commit, preserving identity, creation time and
display spelling. Concurrent unconditional saves serialize: last whole committed
save wins. Empty PUT creates a durable file. Partial PUT is unsupported; bytes
are not transformed.

GET returns a detached committed snapshot, releasing the database before output.
HEAD/PROPFIND return metadata without loading payload. Files have persistent strong
version ETags, content type and UTC-second timestamps; collections have no file
representation/ETag. Every successful PUT rotates the tag; failed saves preserve
state. Default type is application/octet-stream; non-identity encoding is refused.

GET supports single ranges. Multiple ranges/unknown units fall back to full
response; HEAD ignores Range. Supplied If-Range permits a range only for an exact
strong tag; date/weak/malformed values give a full response. ETags distinguish
same-second changes that timestamps cannot.

DAV:creationdate is protected server-owned UTC time. Four properties in
urn:schemas-microsoft-com: are opaque strings: Win32CreationTime,
Win32LastModifiedTime, Win32LastAccessTime and Win32FileAttributes. They do not
change permissions or server timestamps. Unknown property reads are 404;
unsupported patches are refused. Ordered PROPPATCH operations commit atomically.
Effective metadata changes rotate file tags without changing bytes/server-owned
modification time; no-op or failed patches do not.

HTTP and DAV If conditions are independent requirements. If-Match compares
strongly; If-None-Match weakly. HTTP precedence follows RFC 9110. Writes recheck
conditions at publication after staging/parsing, not against stale earlier reads.
Tagged DAV conditions stay within the share and never trigger network fetching.

## Locks

Exclusive write locks support Depth 0/infinity. Collection Depth 0 protects its
properties/immediate membership, not existing child contents. A locked mutation
requires both the creator's stable authenticated account and submitted token.
Tokens do not replace authentication.

Leases default to 600 seconds, clamp to 1-3600 seconds, and cap at 1024 active.
All disappear at process restart; files, properties and validators survive.
Clients must acquire new leases. LOCK on a missing resource creates a durable
empty file; unlock/expiry/restart does not remove it.

Conditions, affected parent/subtree checks and mutation share one gate. Lease
state changes after successful publication. Existing-resource lock/refresh/unlock
does not rotate file tags or timestamps.

## Atomic MOVE and DELETE

MOVE takes an explicit same-origin HTTPS Destination within the share, never
fetching it. Parents must exist. New mapping returns 201; replacement or case-only
rename returns 204. Overwrite defaults to T; F refuses mapped destinations.
An exact unchanged canonical destination is rejected. Case-only rename is an
intentional case-preserving extension.

Move preserves identities, creation times, bytes/types/tags and stored properties.
Real moves update moved files' Last-Modified; case-only rename preserves timestamps
and tags. Replacement is atomic, not a child merge. Source leases do not follow
node IDs. Destination ancestor leases still apply; a destination-rooted lease
survives where its URL still maps, while removed descendant mappings lose leases.

DELETE atomically removes a file/whole collection tree: success 204, absent 404.
Root is protected. Collections require omitted/infinity Depth. Failed changes
preserve the committed tree/leases apart from normal expiry. Recreating a deleted
URL does not restore its old properties/lease.

MOVE/DELETE cap at 4096 affected nodes and 128 MiB logical content/request;
MOVE counts source plus replaced destination. Oversized requests fail before
mutation, not partial fallback. An operator can choose smaller independent
operations. Logical limits are not exact journal/disk-size limits. Neither
operation loads payload to change mappings.

## Bounds and outcomes

XML input: 64 KiB, depth 32, 256 elements; response: 1 MiB. PROPPATCH:
64 operations, 4096 decoded UTF-8 bytes/value, 16 KiB aggregate/request and node.
HTTP tag/DAV If inputs are bounded; tests hold detailed grammar/status cases.

Body/size overflow is refused; capacity/operation timeout generally returns 503,
namespace/tree capacity 507. Disconnected clients may receive no response.
Commit defines the outcome: cancellation or lost response after commit cannot
undo it. Read back state before assuming a retry is harmless.

References: [RFC 4918](https://www.rfc-editor.org/rfc/rfc4918.html),
[RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html).
See [Storage and lifecycle](storage-and-lifecycle.md) for resource/stop policies.
