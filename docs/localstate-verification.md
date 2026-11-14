# Local state foundation verification

2026-11-14, Zen Windows amd64. Dedicated branch
`codex/godav-localstate-foundation` started at reviewed integration
`76f51b0f4eb2c5cce81a5fe3a2829ffdf993ed3f`. The original checkout was read-only.
This is preparation for #4/#11, not implementation of either feature.

## Tests and checks

All evidence is uniquely named `GoDav-Project-Tools/logs/localstate-20261114-*`.
The final `evidence-sha256.log` records the candidate commit and log hashes.
No earlier evidence was overwritten. Tests use disposable credentials and
memory-only ConPTY capture. Existing CLI test files were not changed.

- TDD: invalid-intent test first failed because the new package did not exist,
  then passed with the minimal rejection contract. The retained-ownership test
  first failed on the absent Session methods. An intermediate implementation
  build failed on three extracted error-variable declarations; that failed log
  is retained as `ownership-green` rather than rewritten. The corrected
  `ownership-green2` passed. Additional approved contract regressions passed.
- Final API contract run: passed in 8.253 seconds. Tests exercise both intents,
  private non-inheriting and directory-only-inheriting parents, a separate
  read-only settings parent, retained ownership after opening cancellation,
  actual CLI contention, settings copies, retained DB handles, repeated Close,
  malformed state preservation and cleanup after failed connection validation.
- Fresh `go test ./... -count=1 -v -timeout=5m`: passed. CLI package 146.421
  seconds, including all unchanged CLI, publication/delivery-fault, malformed
  state and release-identity tests.
- Windows and Linux `go vet ./...`: passed. `go mod verify`: all modules verified.
  `go mod tidy -diff`: passed with no dependency or checksum changes.
- Four govulncheck application/test scans for Windows and Linux: no known
  vulnerabilities. Windows scanned 12 modules and Linux 11, including the app,
  plus the Go standard library.
- Bounded `CGO_ENABLED=1 go test -race ./internal/cli -run
  '^(TestLocalState|TestInitConfig)' -count=1 -v -timeout=5m`: passed in 103.231
  seconds using existing MSYS2 GCC 14.2.0. This instruments the new in-process
  foundation calls and harness/fixtures, not the CGO-free CLI subprocesses.
- Final focused release identity and closed-real-WAL refusal run: passed in
  12.431 seconds. Both target releases exclude test fault tags/markers and carry
  exact companion notices. Windows version executed; Linux artifact inspected.
  The final API contracts are recorded separately, not matched by that focused
  release run's exact-name regex.
- Strict UTF-8 decoded, CRLF-normalized gofmt comparison: all 35 Go files passed
  without source writes. Git whitespace and unchanged-module checks passed.

All runs used the project-local verified Go 1.27.4 and govulncheck 1.9.0 through
`Use-VerifiedGo.ps1` in disposable processes. Language baseline remains 1.27.0;
all audited module pins remain unchanged. No old Go, toolchain auto-download,
installation, global environment or persistent permission changes were used.

## Known limits

The tests observe an open DB handle through the session lifetime and its release,
not every intermediate driver event. Review of the retained `sql.Conn` establishes
the no-validate-close-reopen structure. The new API exposes no mutation methods,
so future account transactions, secret disclosure, operation cancellation and
server shutdown still need tests with their real consumers.

No Linux runtime, hostile same-owner/administrator swap, hardlink/junction retry,
actual journal-write interruption or power-loss durability claim is made.
The conservative ACL policy is unchanged. An empty stable ownership artifact may
remain after existing-state refusal, as before. No real owner account, listener,
trust entry, service, push, tracker change or integration merge was performed.
Independent review and integration remain the parent's next steps.
