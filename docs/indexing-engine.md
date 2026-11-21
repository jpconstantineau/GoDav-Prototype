# Background indexing engine pilot

This is the first slice of [#25](https://github.com/jpconstantineau/GoDav-Prototype/issues/25).
The engine builds a private, durable SQLite FTS5 shadow index for one configured
logical WebDAV collection. It does not change `search_file_contents`, which still
uses the committed snapshot it reads for each query. It does not start on normal
server startup, add an MCP tool, or change the wire protocol. The separate
server/MCP integration must explicitly prepare and start the engine with a
server-selected allowlist and collection. Do not use the shadow index for reads.

## Lifecycle and ownership

One store has one latest job and one worker. Starting again as the same owner
while that job is queued, running or paused returns the same ID without adding
work. Another owner cannot observe or control the job. An eligible owner can
start a new job after a terminal result (the new job supersedes the previous
status). The worker supports `Queued` → `Running` → `Completed` or `Failed`;
pause prevents more publication, resume continues the checkpoint, and cancel
persists `Cancelled`. Controls other than start and status specify the current
job ID. Status without an ID can only resolve the caller's own latest job.

The controller receives the authenticated stable account ID, not a request-supplied
owner. The integration must pass a pure lookup of the approved immutable account
IDs to `Options.OwnerAllowed` and resolve those IDs from the configured grants at
startup. The adapter also checks the persistent account is enabled in each control
and work transaction. Revoked or disabled owners cannot control or observe a job;
an old job with such an owner cannot progress and fails with `owner_unavailable`
when the worker checks it. A configured collection is resolved server-side. The
worker checks the same stable collection ID is still at that path before both
snapshot and publication; if it is not, the job fails with `scope_unavailable`.
There is no caller-supplied path override, SQL entry point, content response,
or additional database reader connection.

Pending work is durable, including metadata keyed by stable file ID and
revision. A process shutdown drops any prepared RAM slot, not its pending work;
the integrator cancels and joins the worker before closing the retained SQLite
connection. On the next opted-in startup, an active job resumes without starting
over. Manual pause, rollback and terminal outcomes persist. Status reports
counts for the current plan, so a reconciliation pass can reset counts; they
are not a cumulative history. Before the job plans its scope, total size is not
known. Cancel is not a purge: earlier private shadow rows may remain until the
next successful run reconciles them, and are not available through search.

## Budgets and foreground priority

- One worker retains at most one source file, 256 KiB by default (configurable
  up to 1 MiB), plus bounded metadata. UTF-8 and NUL validation happens after
  the snapshot releases SQLite. Files over the limit or not valid text are
  counted as skipped, not truncated. The string passed to SQLite may make a
  second bounded copy. The FTS table is contentless: it retains indexed terms,
  not another copy of each full body, within the existing private database.
- The plan enumerates no more than 2,048 namespace nodes (including folders
  and the root) and 128 MiB of potentially eligible bytes in that collection.
  Planning and final verification operate on metadata in SQLite; source bytes
  are fetched independently. Limit overflow fails with `capacity_exceeded`.
  Short-lived old/new shadow overlap is separately capped at twice the logical
  corpus limits. These are logical source-size limits, not a promise about the
  exact on-disk token index size.
- SQLite stays on its existing single retained connection. Every background
  acquisition first checks live MCP data waiters, waiting/active foreground
  SQLite traffic (including authentication, DAV and MCP), and a quiet period
  since foreground release (default 100 ms). A foreground waiter already
  registered for the gate cannot be overtaken by the index worker. An operation
  that has acquired the gate keeps it until that transaction releases. An empty
  MCP queue alone does not establish idle storage.
- Each step has a deadline (default 750 ms) and saves at most one document per
  transaction. Default pressure polling is 25 ms, outside the store gate. When
  there is no runnable job, the worker parks until a control wakes it. Busy
  foreground traffic can defer work indefinitely; no foreground request is
  delayed to promise an indexing completion deadline. Hold-time samples are
  delivered after release using the optional safe observer (stage and duration
  only). Status reports coarse pressure: `mcp_queue`, `foreground`,
  `quiet_period`, or `manual`.
- An unexpected storage failure or five consecutive background step timeouts
  stops the worker and surfaces `storage_unavailable` in that process if status
  can still be read. It does not change an unconfirmed write into a success.
  A restart restores the durable checkpoint; that transient failure marker does
  not claim a durable terminal state when the store could not confirm one.

## Publication and freshness

The save transaction checks that the job is still runnable and the prepared
candidate is still pending. It recomputes membership and the canonical path
using relational ancestry, then compares ID, ETag, path, type, byte count and
modification timestamp. A case-only ancestor rename is a mismatch even when
the file's ETag stays the same. A mismatched candidate remains pending; a later
snapshot reads the current state. Deleted or moved-out pending nodes become
tombstones and remove the prior postings. Other stale shadow rows are pruned
one at a time. FTS publication and progress are one transaction.

Before publishing terminal success, the worker checks the live bounded
manifest still equals the completed durable plan, including adds, replacements,
moves and removals after individual saves. A change starts a fresh bounded
pass; more than eight replans fail with `scope_changing`. `Completed` means
the shadow snapshot and checkpoint were committed for that observed collection
at completion, with explicit skipped counts. It does not imply continuous
freshness after completion, indexing events for every later DAV write, or any
change in public search results.

## Verification boundaries

The tests use disposable files and SQLite. They cover reopen/recovery, scoped
ownership, revoked grants and disabled owners, actual FTS matches, pruning,
stale ancestor spelling without a new ETag, replan/cancel, and publication
rollback. Synchronized tests prove the low-priority gate, observable MCP wait
count, quiet-period recovery, and one prepared slot. End-to-end authorized
MCP administrative control and protocol Tasks, plus a matched foreground
latency comparison through the real server, belong to later integration work.
