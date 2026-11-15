# Atomic same-share MOVE (#9)

Specification: https://github.com/jpconstantineau/GoDav-Prototype/issues/9 and #1.
Base: e00d82aeb0c41dc57c54ec67a35589aec7fd1594. This slice adds MOVE only;
COPY and the DELETE HTTP method remain unavailable in this standalone branch.
Earlier slice documents describe their own historical capability sets.

## Namespace and protocol

MOVE requires exactly one encoded absolute same-origin HTTPS Destination under
/dav/. The existing authority and one-decoding path checks run before dispatch;
no destination ever causes an outbound request or host-filesystem operation.
Absent, duplicate, relative, malformed or query/fragment destinations fail 400;
another origin fails 502 and a destination outside the mount fails 403. Existing
component, reserved-name, depth and decoded-path bounds apply to every resulting
descendant path, not just the new root. Root moves and moves into descendants or
over ancestors fail 403. Missing source is 404; absent/noncollection parent 409.
File trailing slashes are 400, including an existing file addressed as a
collection at the destination. No parents are invented.

File MOVE accepts Depth 0, infinity or omission. Collections require infinity or
omission. Other/duplicate/empty depths are 400. Overwrite defaults to T; T/F
literals accept ASCII case variants; other/duplicate/empty values are 400.
After no-op rejection, Overwrite F returns 412 for every mapped destination,
including case-only rename.
Nonempty bodies return 415; incomplete bodies 400, before acquiring the store.

New mapping returns 201. Replacement and changed case-only spelling return 204.
An exact stored canonical destination with no spelling change returns 403 before
Overwrite F collision handling;
noncanonical source addressing does not turn this into a rename. Case-only
spelling changes are a deliberate case-preserving namespace extension to RFC
4918's same-resource restriction. The requested destination basename is stored,
while existing ancestor spelling remains canonical. File/collection replacement
in either direction is permitted without merging the destination's children.

## Publication and bounds

One files.Service operation validates components and invokes one retained-store
operation. The existing cancellable two-second gate covers fresh source and
destination lookup, all HTTP/DAV preconditions, lock checks, replacement and
publication. One SQLite transaction removes destination metadata/content/children
deepest first and rebinds the original source node's parent/name/key. No content
is recopied or loaded; no schema/migration, second connection, cascade, filesystem
adapter or separate delete transaction is introduced. Source node IDs, creation
times, file bytes/types/ETags and supported dead-property strings are preserved.
Children retain their parent IDs. Sibling comparison uniqueness stays enforced
by SQLite and the operation gate.

Real moves set every moved file's HTTP Last-Modified to the operation's UTC
second, satisfying changed destination representation semantics at the existing
clock precision. Globally random version ETags remain valid across a move.
Case-only rename leaves file timestamps and validators unchanged. Source and
destination parent modification times update in the same transaction. Failed
operations change none of these committed facts. Clock rollback/power-loss
durability is not newly claimed.

The shared #9/#10 bounded tree helper limits one request to 4096 affected nodes
and 128 MiB logical file content, counting source plus replaced destination once
each (case-only has one tree). These are engineering work bounds, not an exact
journal/memory/disk estimate. Metadata-only recursive enumeration has a bounded
depth-first frontier and row limit. Excess returns 507 without partial mutation.
Existing 1024-child destination capacity also applies, while same-parent rename
and replacement do not consume an extra slot. No unbounded all-tree payload
allocation occurs. Context cancellation before commit rolls back; cancellation
after successful commit cannot undo publication.

## Conditions and leases

HTTP conditions refer to the source Request-URI. DAV tagged conditions can refer
to source, destination or parents and use the existing complete If grammar.
Every source and replaced destination subtree lease plus source/destination
parent membership requires the submitted token and creator principal. Missing
tokens produce 423 with DAV:lock-token-submitted; another principal produces 403.
Child lock failures return bounded 207 with the failing child href (including
the collection trailing slash), with no 424
or redundant success statuses. Invalid resulting child paths likewise return
207/400 naming that destination child. The complete request still rolls back;
this deliberate atomic policy is stronger than partial collection MOVE.
The existing bounded tree preflight supplies child kinds before these lock
checks; no new query is performed while constructing an error response.

After successful real MOVE, source-rooted leases are removed, never carried by
stable node IDs. Destination ancestor leases continue to cover incoming paths.
An existing destination-rooted lease is retained when that same URL still maps
after replacement, per RFC 4918 section 7.6. Its path display spelling becomes
the incoming canonical spelling. Overwritten descendant leases whose URLs no
longer map are removed. Case-only rename preserves all same-namespace leases,
updating their displayed root spelling. All lease changes happen only after
successful commit while still holding the gate. A failed commit changes no lease.
Restart continues to discard all process leases, not committed namespace data.

## Evidence boundary

RFC references: https://www.rfc-editor.org/rfc/rfc4918.html sections 7.5, 7.6,
8.8, 9.9 and 10.6. Tests use real loopback HTTPS/SQLite and executable reopen;
in-process HTTP enables race instrumentation. Offline large-tree setup creates
production-shaped disposable fixtures, then real startup validates them and all
assertions use HTTP/reopen. No private table assertions establish success.

Build-tag-only MOVE precommit failure/pause follows the existing fault seam.
It has no runtime flag, header or environment control and is excluded from
ordinary/release builds. Synthetic rollback is not a disk-fault simulation.
The completion trace adds only sanitized destination classification, matching
existing source-path redaction; no raw header, query, token, owner or content.
Independent review and exact-head aggregate gates are recorded externally.
Native Explorer/VS Code/SCM and Linux execution are not established by these tests.
