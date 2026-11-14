# Process-lifetime write locks and DAV conditions (#8)

Base: 789c534d028f4d4b4870712ecb1ed09b73dfb314, integrated #7/#12. Public
specification: https://github.com/jpconstantineau/GoDav-Prototype/issues/8 and #1.
The tests use the already approved authenticated real HTTPS/temporary SQLite,
actual executable restart, and in-process HTTPS race boundaries. Native Explorer,
desktop VS Code, SCM and Linux execution remain separate acceptance work.

## Narrow prototype choices

Exclusive write locks only; files and collections support Depth 0 and infinity,
with omission meaning infinity. Collection depth zero protects properties and
immediate member mappings, not existing child contents. Infinity covers the
subtree. Parent membership checks apply to new PUT/MKCOL/LOCK-created files.
Overlapping exclusive locks fail atomically. Descendant conflicts in an infinity
LOCK produce bounded 207/423 and root 424; direct conflicts return 423.

These numeric defaults are provisional bounded engineering choices, not new
operator settings: 600-second default lease, one-second minimum, 3600-second
maximum, 1024 active process leases. Timeout preferences are parsed completely,
including repeated field lines. First preference is selected and clamped;
Infinite selects 3600 seconds and Second-0 selects one. The granted/remaining
finite duration is reported. Expired leases are reclaimed at operation entry.

Random UUID URI tokens are generated with crypto/rand. Stable authenticated
account ID identifies the creator, independently of client-supplied owner XML.
No administrator override or ACL subsystem is introduced. A locked mutation
requires a submitted token and the creator principal. Missing tokens return 423;
another principal presenting a valid token receives 403. Tokens never grant
authentication. Unlocked direct PUT remains valid.

Existing-resource LOCK returns 200; successful missing-resource LOCK creates an
ordinary durable empty file and returns 201. Missing parents return 409 and are
never invented. The lease and empty-file publication are serialized as one
operation; a failed publication creates neither. Empty files remain after unlock,
expiry or restart. This is not the old lock-null model. A separately successful
Explorer empty PUT remains its own committed request.

Empty-body LOCK refresh requires one positive token condition, optionally tagged
with a resource inside that lease's scope. The request must also be within the lease scope;
Depth is ignored only for refresh. It returns 200 and updated lockdiscovery, no
Lock-Token response header. UNLOCK requires one coded Lock-Token, no body, matching
scope and creator; success is 204, absent/out-of-scope token is 409. Other uses of
Lock-Token are rejected 400; combined Microsoft extension commands are not
implemented and X-MSDAVEXT is not advertised.

All locks disappear on process restart. Existing file bytes, metadata and
validators survive. Old tokens no longer satisfy conditions; clients must acquire
new locks. Lock/refresh/unlock on an existing resource do not rotate its file
ETag or modification timestamp. Lock state is not stored in SQLite; no migration,
durable/distributed lock, crash-recovery, or multiple-server claim is introduced.

## DAV If and publication

One complete If field, at most 8192 bytes, 32 lists and 128 conditions. Duplicate
fields, empty/mixed tagged/untagged grammar, malformed coded URIs or ETags are
400; bounds are 431. Validate the whole field before evaluating alternatives.
AND conditions, OR lists, negation, and weak ETag comparison follow RFC4918.
HTTP If-Match retains its existing strong comparison. HTTP and DAV conditions
are independent requirements; a valid lock token cannot override a false ETag.

Resource tags accept root-relative DAV paths or absolute same-origin HTTPS URLs,
using existing strict authority and one-decoding path checks. They never cause
network access. Namespace comparison uses the existing versioned simple-fold key;
tokens are exact, case-sensitive opaque absolute URI identifiers. Missing
resources have no ETag or matching lock token. DAV:no-lock is never issued.
Submitted tokens are collected independently of boolean evaluation, including
tokens in negated/false alternatives. All applicable lock scopes still require
the correct token and principal after an If expression succeeds.

DAV If is evaluated for all supported methods. Read requests do not require a
lock, but a supplied false DAV condition returns 412. GET conditions are checked
again with the detached content snapshot, preserving the metadata-only probe.
PUT stages to EOF and PROPPATCH parses completely before acquiring the store
gate. Current validators, tagged resources, process locks, and publication are
checked while owning the existing operation gate. No slow body holds this gate,
no check-then-publication race or second database connection is introduced.
The two-second acquisition, request deadlines, admission and graceful-drain
contracts remain unchanged. An operation admitted before expiry is serialized
through publication; later operations collect expired leases. Cancellation after
commit cannot undo an already committed outcome.

LOCK XML uses the existing namespace-validation reader: one complete DAV
lockinfo document, 64 KiB body, depth32, 256 elements, 1024/255-byte namespace/name
bounds, at most16 KiB self-contained owner markup. DTD and extra documents are
rejected. Unknown extensions are ignored by complete subtree, never interpreted
as hidden lock commands. Owner namespace bindings are retained. Responses are
bounded to1 MiB and serialization is preflighted before publication. supportedlock
and lockdiscovery are protected live properties. Upstream's ordinary PROPFIND
serialization remains in use with detached live lock data supplied by the project.

## Dependency boundary and later methods

Pinned golang.org/x/net v0.62.0 remains unchanged. Its MemLS does not implement
Condition.Not/ETag or lockdiscovery and has no authenticated-principal parameter.
GoDav therefore owns only the required condition/lease coordinator and handlers;
it does not enable upstream's generic mutating filesystem handlers. Existing
custom atomic PUT/PROPPATCH and namespace rules remain authoritative.

MOVE/DELETE/COPY are still unavailable. Future slices must check every affected
source/destination and parent membership under the same gate, update lease state
only after successful atomic publication, and repeat cancellation/race coverage.
Source locks do not automatically follow node IDs through MOVE. Source-removal
and destination-inheritance outcomes must be tested with those future methods.
Allow lists LOCK/UNLOCK, but no universal DAV compliance class is advertised.

Routine traces continue to exclude credentials, tokens, raw headers, owner XML,
property values and contents. Lock tokens appear only where required in protocol
responses, not diagnostics. Source: RFC4918 sections6-10,15.8,17, and the pinned
webdav lock/if/prop implementation. Planned tests are not verification claims.
