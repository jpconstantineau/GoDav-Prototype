# Slice 01: executable identity contract

Current baseline after slice 02: Go 1.27.0 language/module baseline, verified
Go 1.27.4 compiler, module github.com/jpconstantineau/GoDav-Prototype, and the
audited pinned dependencies in go.mod/go.sum. The original slice 01's Go 1.25.5
standard-library-only baseline is historical, not a current build instruction.
Set GOTOOLCHAIN=local and CGO_ENABLED=0 in the invoking
process (never go env -w). Windows amd64 native execution is primary; Linux
amd64 is cross-compilation and metadata inspection only, not a runtime pass.

Public seam, already approved: actual executable CLI, including the release
build entry point. No tests call private handlers. Commands are case sensitive.
`godav version` exits 0, stderr empty, stdout exactly three LF-terminated lines:
`revision: <full Git hash or unknown>`, `build-time: <UTC RFC3339 or unknown>`,
`source: <clean|dirty|unknown>`. Plain development builds use unknown for all
unstamped fields, even when Go embeds VCS metadata. No commit or startup time
is used as build time.

`godav`, `godav help`, `godav --help`, and `godav -h` print
`Usage: godav <command>`, a blank line, `Commands:`, the explicit `init config`
usage (including default settings path and required username),
`  version  Show build identity`, and `  help     Show this help`, each line LF
terminated; exit 0, stderr empty. Unrecognized command sequences exit 2,
stdout empty, stderr `godav: invalid command\n`
then the same help. These commands do not read config, initialize accounts or a
DB, or open a listener. They must work in empty and malformed-config directories.
The `init config` argument/state/output contract is in [bootstrap-contract.md](bootstrap-contract.md).

Release entry: `go run ./tools/build-release <new-output-directory>` from the
repository root, with GOTOOLCHAIN=local and CGO_ENABLED=0. It requires Git HEAD
and successful Git status, captures time.Now().UTC() once immediately before
building, and stamps the identical full revision/time/source into Windows amd64
and Linux amd64 artifacts. Dirty includes tracked and untracked nonignored files.
Ignored build products are excluded by Git. Dirty releases are allowed but labeled.
The source must remain unchanged during the build. Missing metadata is fatal,
never unknown in a release. Output directory must not exist, preventing stale
artifacts from being mistaken for a successful build. Failure can leave partial
artifacts; only an exit 0 declares a complete build. No environment metadata
substitution or download is permitted. Output: windows-amd64/godav.exe and
linux-amd64/godav, each with a sibling THIRD_PARTY_NOTICES.txt copied from the
required source notice file. Distribute the notice file with each executable;
copying the executable alone is not a complete release package. Git and the
verified supported Go compiler are existing local prerequisites. Normal release
builds clear GOFLAGS and carry no test-only bootstrap fault tags or controls.

Tests use temporary owned source snapshots and Git repositories, independent Git
HEAD expectations, and wall-clock bounds captured around the release invocation.
Fixtures are cleaned by testing.T.TempDir. Tests inspect the Linux artifact's
embedded build flags and target; they do not claim to run it.
