# Metadata and bounded lifecycle reconciliation

This isolated candidate combines issue #7 metadata with issue #12 bounded work.
It does not advance the shared integration branch, main, or the remote.

## Exact ancestry

- Common base: `89ca6bd376cc6ed1a6d11bbc74df815688dd2922`.
- First parent, completed #12 candidate: `30e136baa7b26054187d6f9a4934497db4d17424`.
- Second parent, corrected #7 candidate: `737b425f2e837be20958de3962323b88855ddd5c`.
- Recorded merge: `c6067151cf3dfb91e5b1551b187990e7ddcdaa87`.
- The second parent preserves reviewed `1b316177dbb2226d2c6658ddfa6bd356fa03dbc3`
  plus the response QName correction. Neither original candidate was rewritten.

The two branches changed different hunks in `files_windows_test.go` and
`namespace_windows_test.go`; Git combined both without conflict. Their production
files did not overlap textually. Semantic lifecycle acceptance is still required.

## Combined behavior and test boundary

`TestMetadataConcurrentPatchExpectation` uses the existing `retryAdmission`
helper. Only explicit requests/receiving/authentication refusal markers permit a
retry. Arbitrary 503s, stopping refusals, and uncertain transport outcomes do not.

The response QName regressions exercise authenticated HTTPS PROPPATCH. Windows'
independent .NET XML reader rejects invalid reserved namespace declarations;
expanded-name assertions distinguish an empty namespace from DAV. The cases cover
unqualified properties, an explicit empty default namespace, the predefined
`xml:` prefix, and mixed namespace siblings. Rejected atomic patches preserve
content, validators, and existing metadata.

The combined acceptance tests cover:

- An admitted conditional PROPPATCH body finishes after actual isolated ConPTY
  Ctrl+C; all requested property statuses succeed and the new validator and
  metadata survive a fresh-process restart with file bytes unchanged.
- A complete patch pauses after metadata and validator SQL updates but before
  commit. Actual Ctrl+C expires the 100 ms fixture grace, joins the owner, and
  reports cancellation, never clean success. Restart retains the old properties,
  validator and bytes, and a new conditional patch succeeds.
- In-process HTTPS drain and incomplete-body deadline cancellation exercise the
  same server/request lifecycle under the race detector. Restart checks use a
  fresh ordinary CLI child and public GET/PROPFIND, not private-table assertions.

The owned-transaction fixture extends the existing precommit fault seam with a
context parameter. Normal builds still return nil immediately. Only the compile
flag `godav_test_pause_metadata_commit` enables a fixed nonsecret synchronization
record and a bounded, context-aware pause that always returns failure before
commit. It has no runtime option, header, environment control or new endpoint.
Release tests check both target binaries for the pause marker and unexpected tags.

Race coverage instruments the in-process server and test harness. Ordinary CLI
children, including the tagged precommit fixture, remain CGO-free and are not
race-instrumented. Linux cross-build/vet/scans do not establish Linux execution.
No OS trust, SCM, real credentials, new dependency or persistent settings change
is part of this reconciliation.

## Evidence provenance

External chronological logs in `GoDav-Project-Tools/logs` retain the original
candidate's complete full suite (`meta7-91-full-windows-resumed-20261114T211804Z`)
and the correction's exact-head metadata/race/vet checks
(`meta7-qnames-737b425-20261114T213616Z`). They remain evidence for those exact
commits, not a substitute for aggregate checks of this combined candidate.

The first QName probe (92) also rejected harmless PowerShell startup stderr;
probe 93 corrected that fixture assumption and reproduced both actual namespace
defects. Probe 94 passed after the minimal serializer change. The owned-transaction
probe 96 deliberately failed to reach the not-yet-present pause seam; it is a
fixture-capability red step, not a claimed production cancellation defect. Probe
97 exercised the added seam successfully. Probe 95 covered normal drain and
admission-only concurrency; probe 98 covered in-process drain and cancellation.