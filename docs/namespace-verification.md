# Durable collection verification (slice 04)

Issue: https://github.com/jpconstantineau/GoDav-Prototype/issues/5.
Verified on Zen-1000, Windows amd64, 2026-11-14 UTC.

## Scope and identity

- Settled HTTPS base: `a3c5192d6496ec7e5f8bc16d869fe3c3d73c6f19`.
- Initial implementation: `82c3d561296250632447a6f0015b481eae919dfc`.
- Independently reviewed final implementation:
  `a8a2ca81c97fcf04e4ef7a89d63155b04d6e355d`.
- Isolated branch/worktree: `ticket-04-folders`.
- Compiler Go 1.27.4; module language baseline Go 1.27.0; no dependency changes.
- This is a later documentation-only commit. Runtime evidence and release
  artifacts below identify the clean implementation commit, not this document.

The chosen contract is [namespace-contract.md](namespace-contract.md). The slice
adds explicit durable collections, transactional v1-to-v2 preparation, MKCOL,
case-preserving lookup and bounded Depth 0/1 PROPFIND. It does not implement file
content operations, locks, native-client integration or the complete GoDav spec.
All accounts, certificates and local databases used for tests were disposable.
No real credentials, system trust, service or persistent system settings changed.

## Test-driven progression

Evidence is outside the checkout under `GoDav-Project-Tools/logs`; names below
have the prefix `namespace-`. Expected failures are retained as failures.

| Behavior | Expected red | Green |
| --- | --- | --- |
| Durable create and canonical listing | 01-red | 01-green |
| Refuse namespace triggers on reopen | 02-red | 02-green |
| Require sibling uniqueness | 03-red | 03-green |
| Bound the stored canonical path, including Unicode spelling expansion | 05-red | 05-green |
| Require the complete root/non-root CHECK constraint | 06-red | 06-green |

Additional public CLI/HTTPS regressions exercise:

- V1 account-preserving migration, v2 no-op reopening/bootstrap, durable nested
  collections, explicit parents, known missing probes and process restart.
- Concurrent case-colliding creates: exactly one 201 and one 405, one child.
- Preserved Unicode and escaped display spelling, once-decoded percent names,
  simple-fold collisions, and distinct composed/decomposed or full-fold names.
- Nonempty known/chunked MKCOL bodies, invalid Windows components, bad UTF-8,
  encoded separators, traversal, 255-byte names, depth 32 and path 4096 bounds.
- A natural migration DDL conflict: failure after earlier transactional DDL,
  byte-identical rollback, then recovery after removing only the fixture conflict.
- Malformed v2 keys, parent links, cycles, policy/version, root, CHECK, uniqueness
  and triggers: startup/bootstrap refusal without mutating the persisted state.
- 1024-child capacity, complete bounded listing and oversized XML output refusal
  before emitting a partial 207, followed by normal recovery.
- Writable-parent ownership enforcement with otherwise valid private TLS inputs.
- Real in-process HTTPS writers/readers and context shutdown, retaining the same
  SQLite connection and joining operations before ownership is released.
- Independent parsed metadata/status expectations: preserved display names,
  canonical hrefs, collection type, UTC seconds, empty supportedlock and explicit
  404s for unsupported creationdate/content length/content type/ETag. Successful
  child creation changes its parent's time, not an unrelated ancestor. Failed
  duplicate creation and restart preserve the existing metadata.

Logs 09 and 10 retain initial regression-fixture failures caused respectively by
old Allow/pre-migration snapshot expectations and a directory-unaware snapshot
helper. Logs 20 and 21 retain metadata-parser and generic-CLI-error expectation
mistakes. These were corrected in test code, not counted as product regressions.
The missing-uniqueness fixture was then tightened to remove only its target
constraint. In log 23, temporarily removing only the production uniqueness
rejection caused that public test to fail. Source bytes were restored, and log
24 passed the review-focused tests in 16.114 seconds.

No migration-failure adapter was needed; release builds gain no test controls.

## Independent review

The Spec reviewer identified missing explicit metadata assertions, not a known
production defect. The Standards reviewer identified a malformed-schema fixture
that could fail for two different missing constraints, plus repeated fixture
setup and a duplicated production CHECK predicate. The correction adds metadata
coverage, isolates each malformed invariant, shares fixture setup, and extracts
the unchanged production predicate while keeping independent fixture SQL.

Both reviewers approved `a8a2ca8` with no remaining actionable findings. Reviews
were independent read-only source/evidence reviews; reviewers did not themselves
rerun the runtime suite. Their conclusions do not substitute for the gates below.

## Final gates on a8a2ca8

All commands used the verified project-local toolchain and caches, with no
automatic toolchain download. Release builds are CGO-free. Only the race harness
used CGO with the already installed approved GCC.

| Gate | Log | Result |
| --- | --- | --- |
| Native `go test ./... -count=1 -timeout=8m` | 25-final-full-test | Pass; CLI 235.861s |
| Windows `go vet ./...` | 26-final-vet-windows | Pass |
| `go mod verify` | 27-final-mod-verify | All modules verified |
| `go mod tidy -diff` | 28-final-mod-tidy | Pass; no diff |
| Windows source/test vulnerability scan | 29-final-vuln-windows | No vulnerabilities found |
| Linux amd64 `go vet ./...` | 30-final-vet-linux | Pass |
| Linux source/test vulnerability scan | 31-final-vuln-linux | No vulnerabilities found |
| Five real in-process HTTPS/SQLite race runs | 32-final-race-live | Pass; 26.699s |
| Clean Windows/Linux release builds | 33-release-builds | Pass |
| Executed Windows release identity | 34-release-version | Exact clean identity below |
| Windows/Linux build metadata | 35/36-buildinfo-* | Go 1.27.4, intended targets, CGO disabled |
| Windows/Linux binary vulnerability scans | 37/38-vuln-binary-* | No vulnerabilities found |
| Formatting, clean Git state, release notices | 39-final-format-and-notices | All 54 tracked Go files match; both notices match source |

Race command: `go test -race ./internal/cli -run
^TestNamespaceInProcessConcurrentHTTPAndStop$ -count=5 -timeout=4m`. This directly
instruments the server and SQLite namespace operations in the test process.
Ordinary executable CLI children are not race-instrumented.

Formatting used explicit UTF-8 decoding and CRLF normalization. An earlier
PowerShell console-decoded comparison incorrectly flagged Unicode-containing
files; the explicit UTF-8 comparison passed without source changes.

## Artifacts and evidence integrity

Directory: `GoDav-Artifacts-Go1.27.4-a8a2ca8-slice04-20261114`.
Both binaries embed revision `a8a2ca81c97fcf04e4ef7a89d63155b04d6e355d`, build time
`2026-11-14T18:41:58Z`, and source `clean`.

| Artifact | SHA-256 |
| --- | --- |
| windows-amd64/godav.exe | `1574FC73828605D4EE7A80E0812C547AA360BF5D082B20CDF2CE7F9CCBF00565` |
| linux-amd64/godav | `EF4119D19AF4D7ACB9EDDE264FA917FA15EE4E7464F6A3AD503039B7EB15E1F9` |
| THIRD_PARTY_NOTICES.txt, both targets and source | `597073F6057A260E696F9E051918A8F5D1889D3F35ED0B0AAF84E76917024074` |

`GoDav-Project-Tools/logs/namespace-evidence-sha256-20261114.json` records 48
log/release files. Every saved length and hash was re-read and verified. Manifest
SHA-256: `C480BED504EBFDE4152801E0E133D15C689CE3E11CB3DF04EB1DFCEA555D9FA6`.

## Limits and account-slice reconciliation

- Linux was cross-built, vetted and scanned, not run. Windows privacy/ownership
  enforcement remains intentionally unsupported on other operating systems.
- No Explorer/WebClient mounting, real TLS trust installation, SCM service,
  power-loss/crash durability or hostile same-owner mutation claim is made.
- Simple-fold/no-normalization is the versioned prototype naming policy, not
  NTFS equivalence. Future policy changes require explicit collision handling.
- Existing fixed admission/namespace bounds are not the later configurable
  capacity feature. OPTIONS makes no DAV compliance or locking claim.
- The existing upstream PROPFIND dependency is reused; no new module or custom
  general-purpose XML/URL stack was introduced. Vulnerability scans establish
  no known published findings in checked configurations, not certification.
- The parallel account slice was not edited or merged here. Its
  `internal/sqlite/account_write.go` AddAccount and ResetPassword methods must
  join `s.acquire(ctx)`/`defer s.release()` once, after write guards and before
  their whole transaction. Map acquisition failure through their existing fixed
  failure result. Do not reacquire inside transaction statements or add a second
  connection/lock. LookupEnabled and namespace operations already use that gate.
- Preserve `accounts.go` validation of versions 1 and 2, including
  `validateNamespace` for v2. Offline account operations never migrate, reseed or
  reset namespace state. Preserve bootstrap's v1 creation and v2 no-op behavior.
- After integration, exercise public HTTPS creation of nested resources, stop,
  offline account add/password reset, and restart. Verify new credentials work,
  replaced credentials fail, and existing canonical hierarchy and metadata are
  unchanged. Existing-state contention and aggregate gates must also be rerun.
- No integration merge, tracker change or push was performed by this worker.
