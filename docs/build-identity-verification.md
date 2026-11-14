# Slice 01 verification record

Executed on Zen, Windows amd64, installed Go 1.25.5, 2026-11-14 UTC.
All Go commands used process-scoped GOTOOLCHAIN=local and CGO_ENABLED=0.
No dependencies were installed. Original GoDav-Prototype checkout was read-only.

## Red / green observations

Each behavior was exercised at the actual executable seam before its implementation:

1. `go test ./internal/cli -run TestDevelopmentVersion -v`: failed because
   cmd/godav had no Go files. Minimal entry/identity implementation then passed.
2. `go test ./internal/cli -run TestHelpDoesNotBootstrap -v`: failed because
   no arguments printed version instead of help. Help dispatch then passed,
   including help, --help and -h, with malformed godav.yaml preserved.
3. `go test ./internal/cli -run TestInvalidCommand -v`: bogus returned exit 0
   and version output. Argument validation then passed: exit 2, empty stdout,
   error plus help on stderr; extra arguments and unimplemented commands rejected.
4. `go test ./internal/cli -run TestReleaseIdentity -v`: failed because the
   release entry point did not exist. First builder exposed that -trimpath removes
   -ldflags from Go build information; removed that optional flag so the Linux
   declared metadata remains inspectable. Then passed with independent Git HEAD,
   actual UTC build invocation window and equal Linux metadata.
5. `go test ./internal/cli -run TestReleaseDirtySource -v`: untracked, tracked,
   and staged changes all incorrectly reported clean. Git status capture then
   passed all three scenarios.

Additional regression checks were already green when added: release fails before
creating output for absent Git repository, unborn HEAD, and corrupt Git index;
existing output is refused and its sentinel remains unchanged. Metadata-failure
checks invoke `go run -buildvcs=false ./tools/build-release <output>` to reach the
release builder's own checks even when Go's automatic VCS inspection would fail.

Fixture commit dates are fixed at 2000-01-01. Identity execution waits beyond the
last second in the measured build window: substituting either commit time or
process startup time cannot satisfy the acceptance bounds.

## Aggregate verification

`go test ./... -count=1 -v`: PASS, cli package 39.706s.
`go vet ./...`: PASS, no diagnostics.
`git diff --check`: PASS, no diagnostics.

Clean synthetic release example from that run:
- Revision: 9820340d6daf7ad600a55f6a5856b57a3f136c25 (temporary Git fixture).
- Stamp: 2026-11-14T07:34:21Z.
- Independent invocation window: 2026-11-14T07:34:19Z through
  2026-11-14T07:34:30.5773241Z.
- Native Windows executable executed. Linux amd64 artifact built and its Go build
  information inspected; Linux was not executed and has no runtime pass.

Tests own their temporary files/repositories and clean them via testing.T.TempDir.
Help/version have only output effects; no config/DB/account/listener implementation
exists in this slice. The full original acceptance spec is not claimed complete.
Release source must remain unchanged during a build. Build flags preserve source
paths for metadata inspection; byte reproducibility/path anonymization is not claimed.
