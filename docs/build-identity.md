# Build and release

Use this reference when building or identifying an executable. Read
[Testing](testing.md) for execution and acceptance boundaries.

## Build

Use Go 1.27.4 with the Go 1.27.0 module baseline. Dependency versions live in
[go.mod](../go.mod) and [go.sum](../go.sum). Set GOTOOLCHAIN=local and CGO_ENABLED=0
in the invoking process, not persistent Go or OS settings.

From the repository root:

    go build ./cmd/godav
    go run ./tools/build-release <new-output-directory>

Git and the supported compiler must already be available. The release builder
requires readable Git HEAD/status, refuses an existing output directory, clears
GOFLAGS and excludes test-only fault controls. Keep source unchanged during the
build. Failure can leave partial artifacts; only exit 0 identifies a complete
build. Targets are Windows amd64 and Linux amd64.

## Identity and distribution

The builder stamps the same full revision, UTC build time and clean/dirty source
state into both binaries. Dirty includes nonignored untracked files. Dirty releases
are allowed but labeled; missing release metadata is fatal. Development builds
report unknown for unstamped fields. Build time is not commit or startup time.

Run godav version to inspect revision, build-time and source. Server readiness
carries the same identity. Associate results with that identity; a successful build
does not establish runtime acceptance.

Each target directory contains an executable and THIRD_PARTY_NOTICES.txt.
Distribute both together. Byte reproducibility and path anonymization are not
promised. See [the builder](../tools/build-release/main.go) and
[release tests](../internal/cli/release_test.go) for exact output and checks.
