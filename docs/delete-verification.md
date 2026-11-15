# DELETE focused verification (#10)

Base: e00d82aeb0c41dc57c54ec67a35589aec7fd1594. Worktree:
C:/Users/pierre/git/GoDav-Worktrees/ticket-10-delete-20261115.
Exact Go1.27.4, GOTOOLCHAIN=local, CGO_ENABLED=0 and unchanged pinned modules.
All resources, accounts, certificates and SQLite files were disposable fixtures;
HTTPS used fixture-local certificate pools, with no host trust changes.

## Red/green observations

- TestDeleteFileAndTreeReopen first failed on DELETE405, then passed with204,
  missing404, recursive removal, GET/HEAD/PROPFIND absence, sibling preservation,
  and executable termination/reopen.
- TestDeleteFailedPublicationPreservesTreeAndLease first failed because the
  synthetic build still returned204, then passed after the isolated precommit
  failure seam:500, prior bytes/ETag/lease preserved, unrelated save healthy,
  normal executable reopen and later successful deletion.
- Conditions, root protection, method advertising, file/collection Depth,
  locked parent membership and descendant refusal, recreation and stale tokens
  passed in the same focused run (13.330s).
- Resource bound4097 refuses atomically; explicit child removal permits exact4096.
  Logical content128MiB+1 refuses; explicit one-byte child removal permits128MiB.
  Both survive reopen and preserve unrelated siblings. Bounds and same-named
  DAV/settings/DB/host-sibling separation passed (14.200s).
- Receiving DELETE permits unrelated work and observes a newly granted lock at
  publication. Eight in-process conditional DELETE/PUT races produce one204 and
  one412. Isolated executable Ctrl+C drains an accepted DELETE successfully;
  deadline cancellation during a paused transaction preserves the tree on reopen.
  Direct and descendant other-principal tokens are403. Focused lifecycle run
  passed (20.745s).

The large bound fixtures use offline construction of production-shaped records
in stopped disposable SQLite files to avoid thousands of setup requests. Startup
validates those records. All acceptance oracles use HTTPS/listing/GET/reopen,
not SQL table assertions. Small ordinary tree setup runs entirely through HTTPS.

Logs: C:/Users/pierre/git/GoDav-Project-Tools/logs/delete10-first-red.log,
delete10-first-green.log, delete10-fault-red.log, delete10-fault-green.log,
delete10-bounds.log, delete10-lifecycle.log. Each focused command used -count=1.
The test-only fail/pause seam is selected at build time and always rolls back.
No fault flag, header or environment control is exposed by release binaries.

These are focused passes, not aggregate/release/race/full scanner claims.
Independent Spec and Standards views are requested before expensive final gates.
No native Explorer/VS Code/SCM, Linux runtime, real-user-data deletion,
power-loss recovery, push or issue mutation was performed.

The pre-existing non-root capability/depth regression initially failed on its
obsolete no-DELETE Allow assertion (4.640s). Its expectation now includes DELETE,
retains PUT/MOVE/COPY refusals, and checks204 then404 at namespace depth32.
The first fixture adaptation used a shared helper that supplies Depth1, so its
DELETE correctly returned400 (delete10-capability-green.log, retained failure).
The final assertion omits Depth via the normal HTTP helper; final result is
delete10-capability-final.log. Production unchanged.
