# HTTPS discovery verification (slice 03)

Issue: https://github.com/jpconstantineau/GoDav-Prototype/issues/4.
Verified on Zen-1000, Windows amd64, 2026-11-14 UTC.

## Scope and identity

- Foundation: `7110b8fd2569bca3ec043f034f1290188082b0c9`.
- Initial implementation: `04b1015bfeca4c41f7b9b4d000b5c512b85cf608`.
- Independently reviewed final implementation:
  `7e30dbc7767281911834c54f03c23e1190990551`.
- Isolated branch: `codex/godav-03-https`; worktree: `ticket-03-https`.
- Verified compiler: Go 1.27.4; module language baseline: Go 1.27.0.
- This document is a later documentation-only commit. The release binaries and
  final execution evidence below identify the clean implementation commit above.

The approved contract is [https-contract.md](https-contract.md). This slice
implements explicit existing-config startup, loopback TLS, stored Basic
authentication, strict routing, and read-only empty-root DAV discovery. It does
not complete the full GoDav spec, publish changes, or establish client trust.
Coordinator-owned existing-state foundation files were not changed. Account
lookup uses the retained account-store connection, not a second pool acquisition.

Tests use real CLI processes, actual TLS requests and temporary private SQLite
fixtures. Fixture construction may prepare account states directly; authentication
assertions go through HTTPS, not a private verifier or database query. An
in-process real HTTP lifecycle test observes context cancellation and subsequent
ownership acquisition. No real user account, certificate store, service or
persistent setting was changed.

## Test-driven progression

Logs are outside the checkout under `GoDav-Project-Tools/logs`. Numbered names in
this section are prefixed `slice03-`. Expected failing runs are retained, rather
than described as passing gates.

| Behavior | Expected red | Green |
| --- | --- | --- |
| Require explicit existing configuration | 01-cli-red | 02-cli-green |
| TLS and unauthenticated challenge | 03-tls-challenge-red | 04-tls-challenge-green |
| Stored-account authentication | 05-auth-red | 06-auth-green |
| Root OPTIONS and PROPFIND | 07-discovery-red | 08-discovery-green |
| Authority and raw-path rejection | 09-routing-red | 10-routing-green |
| Depth, method and XML bounds | 11-protocol-red | 12-protocol-green |
| Prerequisite failures before readiness | 13-startup-red | 14-startup-green |
| Existing CLI help regression | 17-cli-help-regression-red | 18-cli-help-green |
| Review XML validity and output bound | 39-review-regressions-red | 40-review-regressions-green |

Additional HTTP/CLI regressions cover raw `OPTIONS *` and absolute-form targets,
restart identity, disabled accounts and invalid verifiers, shutdown ownership,
admission saturation/recovery, and a Destination trap proving no outbound fetch.
The complete suite covers both `/dav` and `/dav/`, missing and malformed Basic
credentials, case-insensitive usernames, wrong passwords, TLS trust failure,
obsolete TLS, invalid certificate usage, missing or invalid local prerequisites,
wrong Host/Origin, traversal and encoded separators, unsupported methods,
finite-depth discovery, malformed/oversized XML, and safe completion logging.

## Independent review

### Standards

The independent review of the complete initial implementation against the
foundation found zero documented-standard violations and zero actionable smell
findings. The incremental review of `04b1015..7e30dbc` also found zero new hard
violations or actionable smells. The reviewer confirmed the byte-preserving XML
change, HTTP-seam regressions and reliance on the selected Go URL parser.

### Spec

The independent initial review found two P2 issues in the compatibility step that
removed unsupported write-lock advertising: re-encoding expanded XML tokens could
duplicate namespace attributes, and the transformed response could exceed the
bounded upstream buffer. Real HTTPS regressions reproduced both failures in log
39. The fix uses stdlib `xml.Decoder.InputOffset` to remove only the inner bytes
of `DAV:supportedlock`, preserving every other upstream byte. Log 40 passed in
11.387s. The independent follow-up review confirmed both findings resolved and
found no new issue in the two-file correction. Final open findings: zero.

Reviews were read-only source/evidence reviews; reviewers did not independently
rerun the test suite. Runtime evidence is recorded separately below.

## Final gates on 7e30dbc

All final commands ran with the process-local verified toolchain, no automatic
toolchain download, and isolated project caches/scanner state. Release builds use
`CGO_ENABLED=0`; the race harness temporarily uses the existing approved GCC.

| Gate | Final evidence | Result |
| --- | --- | --- |
| Native full suite, `go test ./... -count=1 -timeout=8m` | 41-final-full-test.log | Pass; CLI package 195.725s |
| HTTPS race harness, `go test -race ./internal/cli -run ^TestRunServer -count=1 -timeout=4m` | 42-final-race-https.log | Pass; 63.399s |
| Windows `go vet ./...` | 43-final-vet-windows.log | Pass |
| `go mod verify` | 44-final-mod-verify.log | All modules verified |
| `go mod tidy -diff` | 45-final-tidy.log | Pass; no diff |
| Windows source/test vulnerability scan | 46-final-source-windows.log | No vulnerabilities found |
| Linux amd64 `go vet ./...` | 47-final-vet-linux.log | Pass |
| Linux source/test vulnerability scan | 48-final-source-linux.log | No vulnerabilities found |
| Clean Windows/Linux release build | 49-final-release-build.log | Pass |
| Windows release `version` | 50-final-release-version.log | Exact clean identity below |
| Windows/Linux `go version -m` | 51/52-final-buildinfo-*.log | Go 1.27.4; expected target and modules; CGO disabled |
| Windows/Linux binary vulnerability scans | 53/54-final-binary-*.log | No vulnerabilities found |
| Formatting and whitespace | 55-final-format.log | All 47 tracked Go files match gofmt after CRLF normalization; clean whitespace |
| Binary and release-notice hashes | 56-final-artifact-hashes.log | Both notice copies match source |

The actual-import dependency audit is retained under
`GoDav-Project-Tools/audits/dav-v062-20261114/AUDIT.md`. It verified the official
module provenance, sumdb checksums, retractions, licenses and actual linked
WebDAV graph. The only new direct requirement is `golang.org/x/net v0.62.0`;
existing selected dependency pins are unchanged. Source and binary scans above
were rerun against this application, not substituted by the audit harness.

## Final release artifacts

Directory: `GoDav-Artifacts-Go1.27.4-7e30dbc-slice03-20261114`.
Both artifacts embed revision `7e30dbc7767281911834c54f03c23e1190990551`,
build time `2026-11-14T17:54:12Z`, and source `clean`.

| Artifact | SHA-256 |
| --- | --- |
| windows-amd64/godav.exe | `9710BCF310B82A6BCDD356F13360912D0FB47030AFCB896EF92F1EDDCA2BAEFD` |
| linux-amd64/godav | `9A7372437850D3C7EAF0E77C07258959F9380E0B3EF2BAAA71E210879CADC380` |
| THIRD_PARTY_NOTICES.txt, both targets and source | `C0D1D36A11F18E737FDA68B1A93F616184716876D154B1E0D966947CC5366781` |

The evidence manifest is
`GoDav-Project-Tools/logs/slice03-evidence-sha256-20261114.json`.
Its 62 entries cover numbered logs 01-56, the dependency audit report and its
manifest, and the four final release files. Every recorded length and SHA-256 was
re-read and verified after writing the manifest. Manifest SHA-256:
`66D6806C42B8E3C04FDD695438CBF4039D070986DD3F445B2352E8367A6098EF`.

## Limits and integration follow-up

- Linux was cross-built, vetted and scanned, not executed. Existing-state
  Windows privacy/ownership remains intentionally unsupported on other systems.
- The race harness instruments the in-process real HTTP lifecycle and test
  harness; ordinary CLI subprocesses are not race-instrumented binaries.
- No native Explorer/WebClient interoperability, SCM installation, real
  certificate trust installation, interactive UI or external-client test was run.
- The empty root is synthetic. Mutation methods cannot reach its private memory
  filesystem or lock system. OPTIONS deliberately makes no DAV class-compliance
  claim; `supportedlock` is empty. Real file persistence belongs to later work.
- Fixed transport/admission limits and shutdown joining are narrow protections,
  not the later configurable admission/staging/lifecycle feature.
- Scans establish no known published vulnerability in the checked configurations,
  not absence of application bugs or security certification.
- The parallel account-command slice can overlap CLI dispatch/help. The merger
  must preserve both command families, rerun aggregate checks, and verify through
  HTTPS that new credentials work, old credentials fail after a password change,
  and server-owned state cannot be changed by an administrative process.
- At handoff the integration base remained `7110b8f` and clean. No merge, tracker
  update or push was performed by this implementation worker.
