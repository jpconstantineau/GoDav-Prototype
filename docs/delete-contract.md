# Atomic resource deletion (#10)

Issue: https://github.com/jpconstantineau/GoDav-Prototype/issues/10; specification #1.
Base: e00d82aeb0c41dc57c54ec67a35589aec7fd1594. Tests use only the approved
real loopback HTTPS/temporary SQLite and executable reopen boundaries.

DELETE removes one file or a whole collection subtree atomically. Success is
204 with no body; a missing target is 404. Root (/dav or /dav/) is protected with
405. No partial deletion or multistatus success is used. A failed request leaves
all nodes, payloads, properties, validators and live leases intact except normal
lease expiration. Parent modification time changes only with the committed removal.

RFC4918 sections 9.6, 9.6.1, 10.2 and Appendix B guide this contract:
https://www.rfc-editor.org/rfc/rfc4918.html#section-9.6
Collections accept omitted Depth or infinity (ASCII case-insensitive). Files
ignore valid 0/1/infinity Depth, since they have no members. Invalid/duplicate
Depth is 400; 0/1 on a collection is 400. A file with trailing slash is 400.
Nonempty request bodies are unsupported (415); EOF is confirmed before acquiring
the store gate. Read errors are 400. HTTP conditions apply to the Request-URI;
DAV If retains its tagged-resource semantics. False conditions are 412.

Under the existing operation gate, preflight all affected locks, including the
source parent membership and every lease rooted within the deleted tree. Missing
tokens are 423; a supplied token owned by another principal is 403. Commit precedes
removal of leases rooted in the deleted tree. Ancestor and sibling leases survive.
Recreating a deleted path never revives its prior lease or properties.

Provisional bounded engineering defaults: at most 4096 affected resources,
including the selected root, and 128 MiB aggregate logical content. Existing
namespace depth32, path4096-byte and immediate-child1024 limits still apply.
Excess returns 507 before mutation. The operator may explicitly delete smaller
child subtrees in separate requests and then the parent; those are separate
committed operations, never an automatic partial fallback. Logical-content bounds
limit work, not exact SQLite journal/disk bytes. No payload is loaded to delete it.
The existing ten-second request context/two-second acquisition limits still apply.

A single transaction removes supported properties and content before child-first
namespace rows, then updates the surviving parent. No schema/migration, ACL,
settings/account, host-file deletion, or new connection is introduced. Synthetic
build-tag-only precommit fail/pause variants check rollback and lifecycle at the
existing fault boundary. Release builds have no runtime fault switch. Normal
reopen persistence is tested; crash/power-loss durability is not claimed.

DELETE is advertised only where allowed (not on the root). MOVE/COPY remain
separate work. This slice does not claim native Explorer/VS Code acceptance.
