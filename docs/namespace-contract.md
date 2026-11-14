# Durable collections (slice 04)

Issue: https://github.com/jpconstantineau/GoDav-Prototype/issues/5.
Base: a3c5192d6496ec7e5f8bc16d869fe3c3d73c6f19. The approved seams remain
actual HTTPS with real temporary SQLite and executable CLI restart. No helper
tests, real credentials, Windows trust changes, services or native-client claims.

## Ownership and evolution

The explicitly invoked server opens WriteExisting from the outset. Existing
opening never migrates: a separate PrepareNamespace runs after static TLS/origin
prerequisites and successful bind, before serving/readiness. It retains the same
private parent pins, exclusive ownership and SQLite connection. No reopen,
permission repair, WAL conversion or intent upgrade. DELETE journaling, FULL
synchronization and zero SQLite busy waiting remain. A context-cancellable gate
serializes complete store operations; listing snapshots release SQLite before
HTTP output. Four-request admission remains the transport bound from slice 03.

Bootstrap continues creating schema v1. A short transaction creates v2 namespace
state without touching accounts, then changes user_version atomically. V2 opens
validate without writes; init config remains an unchanged-account no-op. Unknown
versions, malformed namespaces and conflicting migration objects fail closed.
No IF NOT EXISTS adoption, reseeding, alternate database or recovery command.
Rollback retains v1; normal reopen is not a power-loss guarantee.

## Namespace

Stable random IDs and explicit parent IDs back collections. Root is one persisted
reserved identity with no parent; it is synthetic and excluded from real-folder
counts. Display spelling is preserved separately from a binary sibling key,
enforced by SQLite UNIQUE(parent_id,name_key). Root cannot be created again.

Prototype policy simplefold-min/no-normalization/v1 uses Go Unicode 17.0.0
simple-fold orbits, choosing each orbit's lowest rune. It is locale-independent,
not NTFS equivalence or full Unicode folding. Composed/decomposed accents remain
distinct; Straße/STRASSE remain distinct; Kelvin/K and sigma forms collide.
Persist algorithm and Unicode version; a different runtime table version fails
closed pending an explicit collision-aware migration, never silently rekeys.

Names are valid UTF-8 and XML text, 1-255 UTF-8 bytes, and obey the existing
conservative winpath component policy (including reserved devices, trailing
period/space, controls and Windows-forbidden characters). Maximum depth is 32;
maximum decoded relative path is 4096 bytes. Both limits exclude the mount.
The approved engineering defaults are documented here before affected tests.
Names are never filesystem paths. Decode URL paths exactly once; literal percent
names are allowed. Keep raw-path rejection before ServeMux. Return canonical
stored ancestor spelling in once-escaped hrefs with a collection trailing slash.

Each collection may hold at most 1024 immediate children; creation above this
bound returns 507. Snapshot materialization is bounded by depth and child count,
not the entire tree. XML retains 64 KiB/32-level/256-element input bounds and a
1 MiB response bound. Response overflow fails before emitting 207, never silently
truncates. Global capacity/admission configuration remains slice 11 work.

## HTTP and metadata

MKCOL creates one explicit child: 201 after commit, 405 existing target, 409 absent
parent, 400 invalid/bounded name, 415 any nonempty body including chunked bodies.
Read errors are 400. No mutation occurs until the actual empty body is confirmed.
Missing probes return 404 and never create anything. Unexpected storage failures
are 500; cancellation/operation admission failures are 503. These errors contain
no SQL, local paths or secrets.

PROPFIND Depth 0/1 returns the resource/immediate children from one committed
snapshot. Preserve slice 03 depth/XML/auth/confinement handling. Reuse pinned
x/net PROPFIND parsing/serialization over a read-only snapshot adapter, not an
OS filesystem. Mutations never reach upstream filesystem or lock operations.
OPTIONS advertises OPTIONS, PROPFIND, MKCOL only; no DAV compliance classes,
locking, unsupported write methods or persisted dead-property claims.

Store UTC second-resolution creation and modification times. Successful MKCOL
updates its parent modification time in the same transaction. Expose upstream
collection resourcetype, displayname and getlastmodified; creationdate remains
unavailable (404 when explicitly requested) until the metadata slice. Root has
empty displayname. No collection content length, content type or ETag is claimed.

Tests proceed one failing behavior then its implementation. Use known names and
independent parsed responses, including restart, races, escaped Unicode names,
malformed input and bounds. No private SQL shape assertions. Hostile fixtures may
alter persisted state to test public startup refusal. A migration fault seam,
if needed, is isolated to a test-only build tag with no release control.
