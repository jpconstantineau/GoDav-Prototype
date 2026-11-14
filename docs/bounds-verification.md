# Bounded work and foreground shutdown verification

Issue: [#12](https://github.com/jpconstantineau/GoDav-Prototype/issues/12).
Executed on Zen-1000, Windows amd64, 2026-11-14 UTC.

## Source and contract

- Settled input: `89ca6bd376cc6ed1a6d11bbc74df815688dd2922`.
- Reviewed implementation: `9c6b38115ba9eac51bbef9b1a9e60fd4617e285d`.
- Implementation tree: `aa2fd26610820aee7d88382ad9623eb6aa96797b`.
- Isolated branch: `agent/bounded-shutdown-20261114`.
- Worktree: `GoDav-Worktrees/ticket-12-bounds-20261114`.
- This report and trailing-whitespace correction form a documentation-only
  follow-up. Production source, tests, dependencies and TLS helper are unchanged.

The [bounds contract](bounds-contract.md) records the approved behavior. Capacity
is 16 accepted transport connections, four admitted requests, two receiving PUTs
and two account lookup/password verifications. There is no admission queue.
Authentication permits end before DAV handling; receiving permits end at body
EOF/error, before publication. Whole requests retain ownership until exit.
Existing payload reservations and the two-second SQLite operation gate remain
unchanged. Safe gate logs distinguish acquisition wait from future writer metrics.

Actual Ctrl+C stops admission and drains without immediately cancelling active
request contexts. Grace expiry cancels requests and closes sockets, then joins
owners before closing SQLite. Cleanup beyond the two-second target is reported
as failure while retaining database ownership. The optional strict configuration
setting `limits.shutdown_grace_ms` defaults to 5000 and accepts 1..30000.

## Public behavior and TDD

Fixtures use real authenticated loopback HTTPS, temporary SQLite, client-private
certificate pools, synthetic credentials and executable reopen/readback. Ctrl+C
is byte 3 sent only to the disposable child's private ConPTY input. No console-
wide event, OS trust edit or service registration is used. Forced process kill
is failed-test cleanup only, never evidence of graceful shutdown.

Chronological evidence lives under `GoDav-Project-Tools/logs`, prefix `bounds12-`.

| Logs | Observed progression |
| --- | --- |
| 01/02 | Accepted upload interrupted by immediate cancellation, then drained by actual Ctrl+C |
| 03/04 | Third receiving PUT wrongly succeeded, then receiving capacity was bounded while reads progressed |
| 05/06 | Optional grace configuration refused, then exact deadline/new-file/replacement preservation passed |
| 07/08 | Seventeenth connection remained open, then pre-HTTP cap and capacity reuse passed |
| 09/10 | New admission cases passed; old concurrent fixtures encountered legitimate overload, then explicit safe refusal handling restored their exact eventual outcomes |
| 11/12 | Corrected an unsafe test assumption about literal paths in redacted logs; lifecycle regressions passed |
| 13/14 | Replaced a client-pipe timeout assumption with raw TLS observation; read-timeout and delayed-owner preservation passed |
| 15 | Earlier full suite passed, CLI 468.635s; superseded by immutable final implementation run 28 |
| 24 | Observability fixture compilation error, not a behavioral red result |
| 25/26 | Missing gate-wait and actual application-deadline trace evidence failed, then both passed |
| 27 | Review regression and negative retry-marker assertions passed, 25.736s |

Added scenarios that already passed are not portrayed as fabricated red steps.
The safe retry contract is deliberately narrow: only pre-operation admission
refusals carry fixed `GoDav-Rejection` categories. Concurrent test writers retry
only `requests`, `receiving` or `authentication`; an arbitrary 503, transport
failure or potentially committed PUT is never retried. The fixed `stopping`
category is not retried by those fixtures. Dedicated immediate-overload checks
remain separate from eventual exact 201/405/whole-snapshot assertions.

Coverage includes:

- Graceful actual-process Ctrl+C during an accepted upload, coherent bytes after
  restart, grace expiry before complete body and during owned precommit work,
  absent new targets and preserved prior replacements.
- A committed response deliberately left unread, followed by stop and fresh
  reopen: successful commit remains durable even when delivery is uncertain.
- Sixteen transport connections, excess refusal, closure and reuse; four admitted
  application requests and immediate fifth refusal; two receiving bodies with
  independent read capacity, cancellation and later upload capacity reuse.
- Two authentication acquisitions waiting behind a paused operation; the third
  is immediately refused with an authentication marker. The admitted acquisitions
  reach the existing two-second deadline without that safe-retry marker.
- Ten-second raw TLS body timeout and ten-second application context cancellation,
  no publication, released capacity and truthful cancellation tracing. Request
  tracing distinguishes no written final response from successful status 200.
- Strict malformed/out-of-range grace refusal without database mutation.
- The approved three-second cancellation-ignoring precommit fault, isolated to
  `godav_test_stuck_file_commit`: delayed cleanup is failure, a second real CLI
  process cannot take ownership while a handler remains, and coherent restart
  succeeds afterward. The fault always fails before commit.

## Independent reviews

Both reviewers inspected the original scope and the clean implementation delta
against `8e448d7`. Reviews were read-only source and existing-log inspection, not
claims that reviewers executed the tests themselves.

- Spec: closed missing shared-store wait visibility and actual admitted-context
  cancellation tracing. No remaining actionable spec gaps or scope creep.
- Standards: closed the remaining arbitrary-failure PUT retry. No remaining
  documented-standard violations. Repeated local grace-fixture setup remains a
  low-priority optional extraction, not a blocking finding.
- Summary record: `bounds12-39-independent-reviews.json`.

## Completed implementation gates

All final implementation checks used clean `9c6b381`, verified project-local Go
1.27.4, `GOTOOLCHAIN=local`, existing pinned modules, downloads disabled and
process-local environments/caches. No globally installed Go or config changed.

| Gate | Evidence | Result |
| --- | --- | --- |
| Full Windows `go test ./... -count=1 -timeout=10m` | 28 | Pass; CLI 435.111s |
| Windows/Linux vet | 29, 32 | Pass |
| Module verify and tidy diff | 30, 31 | Pass, no dependency changes |
| Windows/Linux application and test source vulnerability scans | 33 through 36 | No known vulnerabilities |
| Scoped in-process races, three repetitions | 37 | Pass; 80.506s |
| Windows/Linux amd64 releases | 38-implementation-01 | Pass; CGO disabled, no test tags |
| Windows version/help and both build metadata | 38-implementation-02 through 05 | Clean implementation identity |
| Both release binary scans | 38-implementation-06, 07 | No known vulnerabilities |

The race command covers both new in-process admission cases, all existing
`TestFilesInProcess` cases, namespace concurrent HTTP/stop and context-stop
ownership. The test harness uses the already installed GCC for race support;
ordinary child executables explicitly remain CGO-free and are not themselves
race-instrumented. Linux was cross-built, vetted and scanned, not executed.

The implementation release preservation script stopped after those successful
builds/scans on a documentation-only trailing blank line. Its incomplete result
is recorded in `bounds12-38-implementation-08-preservation-incomplete.json`, not
called a complete pass. The documentation-only handoff removes that whitespace,
reruns focused bounds tests, builds exact-final clean releases and repeats both
binary scans plus formatting, identity, test-control and preservation checks.
An external SHA-256 manifest binds the completed final logs and artifacts.
Full-suite/source/race results are carried forward only after proving production,
test and dependency trees are identical to the reviewed implementation.

## Limits and integration

Cancellation after commit cannot undo that committed operation. Synthetic faults
prove precommit rollback/ownership, not real disk exhaustion, power loss, crash
durability or hot-journal recovery. The cleanup target is not a universal forced-
exit promise: an owner ignoring cancellation can retain ownership longer.

No native Windows service, Explorer/WebClient, VS Code, Linux runtime, MOVE,
DELETE or lock acceptance is claimed. Future operations must use the same bounded
request/store lifecycles and repeat shutdown/race acceptance. Storage/payload and
ready-writer/transaction metrics remain the accepted visibility plan, not a new
metrics endpoint or an assertion that the shared operation gate measures writers.

Parallel issue #7 owns DAV property/conditional semantics and schema changes.
This slice owns handler auth/trace regions, server/config lifecycle, operation
wait diagnostics and two existing concurrent-test functions. It does not alter
SQLite schema, pool size, gate policy, WAL, dependencies or existing migration
call sites. Merge assertion-only edits to shared test files without replacing
these concurrency functions. Neither the integration nor owner checkout was
edited or merged, and no push or tracker mutation was performed.
