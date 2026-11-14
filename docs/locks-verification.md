# Lock slice verification provenance

Approved seams are real authenticated HTTPS backed by disposable real SQLite,
actual CLI reopen, and in-process HTTPS for race instrumentation. All credentials,
files and TLS clients are synthetic. No host trust, native client mapping,
service execution or external network target is part of this slice's tests.

Chronological development logs are outside the checkout in GoDav-Project-Tools.
They are evidence for the code at their invocation, not final-head aggregate gates:

- logs/locks08-01-red-explorer.log: reference empty PUT followed by LOCK failed 405.
- logs/locks08-02-explorer.log: complete synthetic sequence and direct save passed.
- logs/locks08-03-red-collection.log: descendant conflict lacked required 207.
- logs/locks08-04-collection.log: hierarchy, membership and discovery passed.
- logs/locks08-05-conditions-xml.log: full If truth/grammar table, namespace-safe
  owner responses and unchanged snapshots passed. Independent .NET XML reads
  reject malformed namespace declarations without enabling network resolution.
- logs/locks08-06-owner-default.log: implicit empty owner namespace preserved in
  both LOCK and PROPFIND responses.
- logs/locks08-07-capability-regressions.log: then-current lock and discovery
  regressions passed. Older no-lock capability expectations were updated explicitly.
- logs/locks08-08-scope-red.log: refresh scope returned 412 but lacked DAV error code.
- logs/locks08-09-scope-capacity.log: scope/trailing-slash/timeout and 1024-active-lock
  capacity/reuse coverage passed.
- logs/locks08-10-concurrent.log: same-resource case-variant creation raced three
  times, with one 201 and one 423 each time.
- logs/locks08-11-full-suite.log: superseded development run. Review corrections
  overlapped this run, so it is not an immutable exact-head validation result.
- logs/locks08-16-unlock-validation.log: post-fix absent/duplicate Lock-Token fields
  returned 400 while preserving the active lease. No pre-fix failure demo was run.
- logs/locks08-17-reviewed-transport.log: post-fix grammar, language inheritance,
  owner namespace, token/ETag case and input-completeness coverage passed.
- locks08-lifecycle-final.log: refresh/expiry, actual CLI restart, principal
  isolation and late-lock-versus-staged-PUT passed together.
- locks08-lifecycle-controlc-drain.log: actual isolated Ctrl+C drained an admitted
  conditional PUT, clean exit and fresh-process persistence passed.
- locks08-lifecycle-placeholder-fault.log: the existing build-tag-only precommit
  fault left neither a placeholder nor a lease; ordinary reopen/create succeeded.

Race instrumentation can cover the in-process HTTP cases, not separately built
ordinary CLI subprocesses. Placeholder faults reuse existing test-only build
tags; no new runtime fault header/environment/flag or test control was added.

Final exact-head full/race/vet/module/release gates and preservation hashes belong
in the external dated evidence manifest. A completed focused run does not imply
that those gates passed. Linux cross-build/vet is not Linux execution. HTTP replay
does not establish native Windows Explorer/desktop VS Code acceptance, universal
DAV compliance, or durable/distributed locks.
