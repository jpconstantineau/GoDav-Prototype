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

## Review cleanup verification

The Standards review's duplicated fixture setup was consolidated by reusing
repository(t) for the unreadable-status case. The missing-repository and unborn-HEAD
cases and all public executable assertions remain unchanged; synthetic commits
still use the fixed 2000-01-01 dates. This is a review-stage refactor, not new behavior.

With process-scoped GOTOOLCHAIN=local and CGO_ENABLED=0 on Zen:
- `go test ./... -count=1`: PASS, cli package 35.923s.
- `go vet ./...`: PASS, no diagnostics.
- `git diff --check`: PASS.

## Windows Git fixture teardown with Go 1.27.4

Verified on Zen, Windows amd64, 2026-11-14 UTC, starting from integration
`e0e79a40c550dc60f8451991c27afcc26138dc2d`. Used the verified project-local
Go 1.27.4 executable and caches, Git 2.41.0.windows.3, process-scoped
`GOTOOLCHAIN=local`, `GOENV=off`, and `CGO_ENABLED=0` except for the race harness.
The module's Go directive and default installed toolchain were not changed.

### Red and cause isolation

Before changing the fixture, this existing public-executable regression failed:

`go test ./internal/cli -run '^TestReleaseIdentity$' -count=1 -v -timeout=2m`

Its revision, UTC build-window and output assertions succeeded, then TempDir
cleanup failed with `unlinkat ...: Access is denied.` The fresh failing object,
relative to the existing project-tools temporary root, was
`TestReleaseIdentity435397237/001/.git/objects/11/0290f04131f532ff523cd54ca4ecfdff92019a`.
The test exited 1; this was an OS removal error, not a tool approval denial.

Two bounded diagnostic comparisons used new repositories in that same temporary
root, each with one synthetic file, fixed commit dates and exactly three loose
objects. Both repositories in each comparison had identical commit IDs.

- Default `git init` and `git init --shared=0600` both created all three objects
  with Windows attributes 33 (ReadOnly + Archive). The local 0600 setting was
  confirmed, but did not prevent read-only objects on this installed Git.
  Although Git documents explicit octal repository modes, that option was not
  a working fix here. [Git init documentation](https://git-scm.com/docs/git-init)
- In another new pair, the default repository's `os.RemoveAll` failed. Clearing
  only ReadOnly on the counterpart's three objects changed attributes 33 to 32;
  `os.RemoveAll` then returned nil and the directory was independently absent.
  The diagnostic rejected reparse points before changing any file.

This isolates the read-only objects as the cleanup trigger in this environment.
It does not establish which Go/Windows implementation change introduced it.
Earlier failed fixtures and TLS leftovers were not modified. Failed diagnostic
fixtures were retained; only the new writable counterpart was removed.

### Minimal fixture correction and green

After initializing each synthetic repository, register a Windows-only cleanup
before Git can create objects. Because cleanup callbacks run in reverse order,
this callback runs before the existing TempDir removal. It checks the owned
fixture root and `.git`, rejects reparse points throughout the object walk, and
clears ReadOnly only on regular files under that fixture's `.git/objects`.
Every error fails the test; TempDir still performs and checks the deletion.
There is no global Git setting, ACL change, custom recursive deletion, skipped
test, new private-helper test seam, or production change. Other platforms retain
the existing cleanup behavior.

All existing executable/output/identity assertions remain intact. Results:

- Focused command above: PASS, cli package 4.298s.
- `go test ./... -count=1 -v -timeout=4m`: PASS, cli package 27.470s.
- `go vet ./...`: PASS, no diagnostics.
- `go test -race ./... -count=1 -timeout=4m`: PASS, cli package 30.955s, using
  `CGO_ENABLED=1` and the existing GCC 14.2.0. This instruments the test harness,
  not the separately built CGO-free application subprocesses; it is not
  end-to-end application race coverage.
- Changed Go files match gofmt after CRLF/LF normalization; no unrelated files
  were reformatted. `git diff --check`: PASS.
- Linux amd64 CLI tests cross-compiled with `go test -c`: PASS. No Linux
  executable or test binary was run.

Before/after listings around the focused, full and race runs each retained the
same 13 earlier `TestRelease*` directories and showed no new directory left
behind. Thus each green run also completed its new fixture teardown.

Local raw evidence is in the sibling `GoDav-Project-Tools/logs` directory:
`teardown-01-red-release-identity.log`, `teardown-02-git-modes-probe.log`,
`teardown-03-readonly-cause-probe.log`, `teardown-readonly-probe.go`,
`teardown-04-green-release-identity.log`, `teardown-05-full-tests.log`,
`teardown-06-vet.log`, `teardown-07-race-harness.log`,
`teardown-08-normalized-format.json`, `teardown-09-linux-test-compile.log`,
and the `teardown-04`, `teardown-05`, and `teardown-07` fixture-list JSON records.
