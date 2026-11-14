# Atomic file save/read verification

Issue: [#6](https://github.com/jpconstantineau/GoDav-Prototype/issues/6).
Executed on Zen-1000, Windows amd64, 2026-11-14 UTC.

## Source and boundary

- Settled input: `87d5af21bf529f6016cd526deee96552c1528daa`.
- Reviewed implementation: `0dd09c07d2ce9ca50fa45ab396eba209433ea7d3`.
- Implementation tree: `25fcf91443a30bee361dbff12ac21e7ed1c5afe2`.
- Isolated branch: `agent/atomic-files-20261114`.
- Worktree: `GoDav-Worktrees/ticket-05-files-20261114`.
- This report is a subsequent documentation-only commit. The implementation,
  tests, module pins and TLS helper are unchanged by the report.

The [slice contract](files-contract.md) records the design before implementation.
The runtime uses direct request-aware staging and complete SQLite operations,
not WebDAV close-to-publish behavior. A new file's content and metadata publish
in one transaction; a replacement retains its node identity, creation time and
display spelling. The content table is schema v3. Existing v1/v2 state migrates
explicitly at startup, and offline account administration preserves v3 content.
Malformed or conflicting schemas are refused without repair or modification.

The 32 MiB default is configurable through 64 MiB. An application payload budget
of 128 MiB accounts for upload buffer capacity and conservative 64 MiB read
reservations. Database/driver transient copies, HTTP/TLS, authentication and
small metadata allocations are additional memory, not included in that bound.
Store acquisition has a cancellable two-second bound. Staging does not hold it;
network output uses a detached, single-version snapshot. HEAD, PROPFIND and
metadata-only conditional responses do not materialize content.

## Public evidence and TDD

Fixtures use real authenticated loopback HTTPS with certificate verification,
temporary SQLite, native CLI startup/reopen, and independent response parsing.
No OS trust store or live credentials are used. The file tests assert publicly
visible bytes, response status, headers, DAV metadata and reopen results. Stable
private IDs are preserved by source/transaction review, not claimed as a directly
observed public identifier. No endpoint was added for test inspection.

Chronological logs reside in `GoDav-Project-Tools/logs`, with prefix `files05-`.

| Logs | Observed progression |
| --- | --- |
| 01/02 | Empty PUT/read/reopen red (404) then green |
| 03/04 | File listing/representation metadata red then green |
| 05/06 | Absolute configuration ceiling refusal red then green |
| 07 | Actual body boundaries already passed the bounded implementation |
| 08/09 | Combined upload/read reservation red (GET bypassed budget) then green |
| 10/11 | Single-range contract red (multipart was returned) then green |
| 12/14 | Tagged staging/precommit/payload-read seams first failed to exercise their required behavior |
| 15/16 | Metadata-only paths and corrected fault/readback fixtures passed |
| 17/18 | Waiting operation exceeded bound, then two-second acquisition passed |
| 19/20 | Collection PUT and v1 migration-conflict preservation red then green |
| 21/22 | Conditional metadata read attempted payload load, then returned 304 without it |
| 23/24 | Second-resolution date If-Range produced 206, then conservatively returned full content |
| 25 | v2/account preservation, malformed-state refusal and concurrent snapshots passed |
| 26 | First clean implementation full suite passed; CLI 319.210s |
| 27/28 | Review-found bare MIME tokens and empty/zero suffix ranges red, then corrected public/persisted-state/precedence cases green |
| 29 | Added slow-reader, fully staged cancellation and actual 64 MiB boundary evidence passed; 28.097s |

Not every added regression required a production change. Boundary, concurrency
and preservation cases that already passed are not portrayed as fabricated red
steps. Two failed fixture assumptions are retained explicitly: log 13 compared
equivalent XML bytes whose property order varied; it was replaced by independent
namespace-aware property maps. Log 19's disconnected-request cleanup initially
parsed a transient admission 503 as XML; the fixture now waits for an actual
successful response. Those are test-oracle corrections, not storage-loss defects.
Log 27's later conditional checks also encountered state already changed by the
earlier intentionally failing MIME cases; the complete corrected fixture passes.

Coverage includes:

- Empty creation, repeated replacement, binary/equal-length data, strong ETag
  changes and persistence, case-preserved naming, file/collection distinctions,
  missing or file parents, trailing slashes, size/type/time/validator properties.
- Actual body sizes at limit minus one, limit and limit plus one for 7-byte,
  default 32 MiB and absolute 64 MiB configurations, known/chunked lengths, and
  new/replaced files. Failed replacement leaves prior bytes and metadata intact.
- Paused uploads and unrelated saves; two simultaneous 64 MiB reservations;
  rejected extra uploads/reads; metadata progress; cancellation and budget reuse.
- A paused downstream large GET, unrelated publication and replacement of its
  own path. The pending stream retains its old whole version and validator;
  the next GET observes the complete replacement. Capacity is reusable afterward.
- Truncated fixed-length/chunked input, build-tagged staging/precommit failures,
  fully staged precommit cancellation, and successful reopen/next-save afterward.
  A paused transaction proves a competing acquisition returns 503 at about two
  seconds instead of waiting for the five-second fixture pause.
- Single closed/open/suffix ranges, invalid and zero suffixes, empty files,
  multiple/unknown-unit full responses, HEAD, validators and conditional precedence.
  Unsupported write/DAV conditions return 501 without mutation; partial PUT and
  unsupported encoding/type inputs are rejected.
- v1 conflict preservation; v2 folder spelling/time preservation followed by
  file writes and actual offline add/reset commands; file bytes/ETag and account
  authentication survive. Unknown versions, missing or malformed content schema,
  invalid metadata, file parents, orphan content and unexpected triggers refuse
  startup without changing the fixture database.

## Independent review

Both reviewers used the exact base and clean implementation above, reading the
owner/spec/ticket material independently. Their final review was source and
existing-log inspection, not a claim that they executed tests themselves.

| Standards | Spec |
| --- | --- |
| Initial MIME contract defect and XML-prefix-dependent assertion were corrected. | Initial range/MIME defects and slow-reader/cancellation evidence gaps were corrected. |
| Final: no remaining documented-standard violations or new blocking findings. | Final: no remaining actionable Spec findings or scope creep. |
| Broadened lookup/node names were clarified. Transaction-local creation duplication remains an optional future observation. | Write conditions, locks, configurable lifecycle and native-client acceptance remain separate slices. |

Review summary: `files05-43-independent-reviews.json`.

## Completed implementation gates

All following gates ran against clean implementation `0dd09c0` using verified
Go 1.27.4, existing pinned modules and process-local environment/cache settings.

| Gate | Evidence | Result |
| --- | --- | --- |
| Full Windows `go test ./... -count=1 -timeout=10m` | 30-reviewed-full | Pass; CLI 392.432s |
| Windows and Linux vet | 31, 34 | Pass |
| Module verify and tidy-diff | 32, 33 | Pass; no dependency changes |
| Windows/Linux application and test source vulnerability scans | 35 through 38 | No known vulnerabilities |
| Existing GCC 14.2 identity | 39 | Confirmed; no installation |
| Three repetitions of all in-process file concurrency/race cases | 40 | Pass; 47.633s |
| Three repetitions of prior namespace/HTTPS context-stop race cases | 42 | Pass; 20.200s |
| Clean Windows/Linux amd64 release builds | 41-implementation-01 | Pass; CGO disabled, no test tags |
| Windows executable version/help and both build metadata | 41-implementation-02 through 05 | Exact revision, clean source and actual UTC build time |
| Both release binary vulnerability scans | 41-implementation-06, 07 | No known vulnerabilities |
| Strict UTF-8 and normalized gofmt, source/release/preservation proof | 41-implementation-08 | All 75 Go files pass |

Race instrumentation applies to the in-process HTTPS/SQLite server and test
harness. Ordinary spawned CLI binaries explicitly use CGO=0 and are not themselves
race-instrumented. Linux was cross-built, vetted and scanned, not executed.

Implementation artifacts:
`GoDav-Artifacts-Go1.27.4-0dd09c0-files05-41-implementation`.
Both carry the exact unchanged third-party notices. Binary metadata and marker
checks exclude staging, payload-read, precommit and pause test controls, together
with the earlier settings/console fault controls. They have no release runtime
header, environment or command option. Release help/version were executed on
Windows; protocol fixtures exercise the same source in native development builds.

The documentation-only handoff is checked separately with exact-head file
regressions, release identity/build checks and an external SHA-256 manifest of
logs/artifacts. Its full-suite/source/race evidence is carried only after proving
the implementation and dependency tree is unchanged. Neither the primary
integration nor the owner's checkout is merged or edited by this slice.

## Explicit limits

Synthetic failures occur before publication/commit or payload materialization.
They prove recoverable rollback and cleanup, not injected device failures, power
loss or crash recovery. Existing journal/WAL/SHM sidecar refusal is unchanged;
there is no promise to reopen hot-journal state. Cancellation after a successful
commit cannot roll that committed save back.

This slice does not implement conditional writes, dead/creation properties,
PROPPATCH, locks, rename/move/delete/copy, configurable shutdown/admission, Windows
service integration, Explorer or VS Code acceptance, or Linux runtime acceptance.
No new dependency/framework, trust change, service registration, tracker mutation,
merge or push was performed.
