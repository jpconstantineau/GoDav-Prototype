# Slice 01: executable identity contract

Baseline: installed Go 1.25.5, module github.com/jpconstantineau/GoDav-Prototype,
standard library only. Set GOTOOLCHAIN=local and CGO_ENABLED=0 in the invoking
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
`Usage: godav <command>`, a blank line, `Commands:`, `  version  Show build identity`,
and `  help     Show this help`, each line LF terminated; exit 0, stderr empty.
Any other argument sequence exits 2, stdout empty, stderr `godav: invalid command\n`
then the same help. These commands do not read config, initialize accounts or a
DB, or open a listener. They must work in empty and malformed-config directories.
Only these two commands are advertised in this slice.

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
linux-amd64/godav. Git and Go are existing local prerequisites.

Tests use temporary owned source snapshots and Git repositories, independent Git
HEAD expectations, and wall-clock bounds captured around the release invocation.
Fixtures are cleaned by testing.T.TempDir. Tests inspect the Linux artifact's
embedded build flags and target; they do not claim to run it.
