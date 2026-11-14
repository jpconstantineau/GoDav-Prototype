# Bounded work and foreground lifecycle (slice 11)

Issue: https://github.com/jpconstantineau/GoDav-Prototype/issues/12.
Base: 89ca6bd376cc6ed1a6d11bbc74df815688dd2922. Approved scope is real disposable
Windows HTTPS/SQLite and executable Ctrl+C in an isolated private pseudoconsole.
No SCM, trust, global settings, new endpoint, dependency or WAL changes.

## Contract

At most 16 accepted transport connections, four admitted application requests,
two receiving PUTs and two simultaneous account lookup/password verifications.
Excess work does not queue: pre-HTTP connections close, application overload is
503. Authentication permits are returned before DAV work. Upload permits are
returned at EOF/error, before store publication; all handlers remain counted
until they exit. Existing 128 MiB combined upload/read reservations, 64 MiB hard
file ceiling and two-second SQLite operation acquisition remain unchanged. At
most four admitted handlers can request SQLite, so at most three wait while an
operation owns its single gate. This is synchronous request work, not a job queue.

Existing transport/metadata bounds remain: HTTP/1.1, 16 KiB/100 header values,
5s header read, 10s request read/write, 30s idle, XML 64 KiB/depth 32/256 elements,
1 MiB XML response. A ten-second application context covers admitted work.
`limits.shutdown_grace_ms` is optional, defaults to 5000, and accepts 1..30000.
Omitted settings preserve existing config files; malformed/unknown values fail.
Other capacity/time values are explicit prototype constants, not unlimited.

Stop closes admission atomically then shuts listeners/idle connections and drains
accepted work. The signal context does not cancel active request contexts until
the grace expires. Expiry cancels requests and closes sockets, then joins owners
before closing SQLite and releasing exclusive ownership. Cleanup has a 2s target;
if exceeded, report failure and keep ownership until users finish. Never close
under an active handler or report graceful success for forced cancellation.
Clean stop exits 0; expired grace or operational failure exits 1 with distinct
fixed nonsecret diagnostics. Committed outcomes remain committed if a response
is lost; uncommitted replacement/new files preserve prior state/remain absent.

Safe slog records identify stop-admission, draining, cancellation, cleanup delay
and final result; request tracing distinguishes no final response and cancellation.
No credentials, verifiers, raw headers/tokens or body content are captured.
Prometheus storage/payload/writer meanings remain a visibility plan, without a
new dependency, metrics endpoint or implementation requirement.

## Verification boundary

One failing public behavior, minimal change, repeat. Tests use real HTTPS,
independent bytes/metadata and fresh-process restart. Existing temporary private
ACLs, credentials and test-client certificate pools remain fixture-only. Actual
Ctrl+C is a byte written into the isolated child's ConPTY input, never a console-
wide GenerateConsoleCtrlEvent. Forced process termination is failed-test cleanup,
not graceful-stop evidence. Race tests instrument the in-process server;
ordinary CLI children remain CGO-free and are not race-instrumented.

MOVE/DELETE and conditional metadata/lock work must join these same request/store
lifecycles and repeat shutdown/race acceptance when implemented. This slice cannot
claim native service, Explorer or complete-operation acceptance in their absence.

Only pre-operation admission refusals include GoDav-Rejection with the bounded
category requests, receiving, authentication or stopping. Storage/cancellation
503s and uncertain transport failures have no retry authorization. Updated
concurrency fixtures retry only the first three explicit admission categories.


The approved unresponsive-owner fault is isolated to godav_test_stuck_file_commit.
It sleeps three seconds without observing cancellation and always fails before
commit. Release builds contain neither this control nor its synchronization text.
The tested cleanup-delay result is failure; no universal hard-exit promise is made.


Store-gate slog events report only acquisition duration and fixed acquired,
timeout or cancelled outcomes. This gate includes reads and authentication:
these are not claimed as ready-writer-only metrics or transaction duration.
The existing acquisition policy and two-second timeout are unchanged. Request
completion observes the actual admitted child context before its normal cancel,
so the application deadline is distinguishable from a completed response.
