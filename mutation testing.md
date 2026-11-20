# Mutation testing

Mutation testing checks whether the suite detects an intentional regression, not just whether it executes the affected code.

## Rule for new tests

For every new test, run it on the correct implementation and with at least one meaningful, temporary change that breaks the behavior the test is meant to protect. For table-driven tests, use a narrowly focused subtest when possible. Use an isolated worktree or a Go overlay, one production-code change at a time. Verify that the intended assertion fails, restore or disable the mutant, and rerun the clean test. Record the test name, production file/symbol, exact before/after change, command, and results here. Do not commit a mutant.

If a behavioral mutant survives, strengthen the assertion or fixture (or add the missing test), rerun the identical mutant, and record how it was caught. If another existing test detects it, record that test and distinguish a focused-suite survivor from a suite survivor. Document equivalent mutants and why their observable behavior is unchanged; a compiler error, no matching tests, or infrastructure failure is not a kill. Never improve the score by overfitting to private implementation details or by weakening a product assertion.

## Audit scope

Baseline source: `14f4eda` (the local MCP search and tool-description branch). The review covers all 55 tracked Go test files (271 top-level test functions in source) at that revision, including Windows-only tests and platform counterparts. The inventory is an assessment of the tests, while the experiment ledger records a **selected** set of regressions actually injected. It is not an exhaustive mutant enumeration or a repository-wide mutation score.

The audit uses disposable fixtures, repo-local code, and built-in Go tooling. It does not modify installed services, machine trust, deployment, or user data. Functional mutations were selected at public result, state, and protocol seams rather than disabling security controls.

## Clean verification

- Windows/amd64, Go 1.27.4, `CGO_ENABLED=0`. Windows socket tests used disposable loopback fixtures.
- Final merged test-only changes: `go test -p=2 ./internal/config ./internal/dav ./internal/mcpfiles ./internal/mcpskills ./internal/service -count=1 -timeout=3m` passed.
- Final focused integration: `go test -p=2 ./internal/cli -run '^(TestMCP|TestHTTPTest)' -count=1 -timeout=4m` passed.
- `go vet -p=2 ./...`, `go mod verify`, formatting of changed Go files, and `git diff --check` passed.
- Full unmutated Windows baseline: `go test ./... -json -count=1 -timeout=20m` passed (270 top-level tests and 322 subtests in six test packages, zero test failures or skips). The baseline started before the test-only edits; the changed suites and MCP/HTTP integration were retested on the final branch as shown above.

## Mutation experiment ledger

**Sample result: 24 valid mutants.** Existing tests caught 17. Seven survived their initial relevant test runs; the identical mutations all failed after strengthening the tests. No production change was retained.

In commands below, run from the repository root with `CGO_ENABLED=0`. A repeated selector is intentional: it runs the exact same test or set of tests before and after the assertion change. To reproduce, make the indicated source edit in an isolated checkout or apply it as a Go overlay, and use the test command shown. Where a Windows test spawns the CLI executable, make sure its separate build uses the same temporary change.

### Configuration, HTTP test mode and MCP wire

Run each row separately with `go test <package> -run '<selector>' -count=1 -timeout=3m` (an optional Go overlay made each edit temporary). The corresponding unmutated test paths pass.

| ID | Package; selector | Production change | Initial result and final evidence |
| --- | --- | --- | --- |
| C01 | `./internal/config`; `^TestMCP` | `settings.go`: `graceMS := int64(5000)` to `graceMS := int64(4000)` | Survived the settings tests. `TestMCPSettingsOptIn` now checks the documented 5s default; the same mutant failed with `ShutdownGrace = 4s, want default 5s`. |
| C02 | `./internal/config`; `^TestMCP` | `settings.go`: `MaxFileBytes: maxBytes,` to `MaxFileBytes: maxBytes - 1,` | Survived the settings tests. `TestMCPSettingsOptIn` now checks the parsed 65,536-byte budget exactly; the same mutant failed with 65,535. |
| H01 | `./internal/dav`; `^TestHTTPTestHandler` | `routing.go`: `port = 80` to `port = 81` in HTTP default authority normalization | Killed by `TestHTTPTestHandlerDefaultPortAndTransportBoundary/default80`: 400, want 404. The Origin/Destination cases also failed at the intended route outcomes. |
| H02 | `./internal/cli`; `^TestHTTPTestCLIRejectsConfigurationAndNetworkOverrides$` | `http_test_mode.go`: `godav: run http-test accepts only --port <1-65535>` to `godav: invalid port configuration` | Killed by the usage assertion: exit code 2 and quiet stdout were preserved, but the `--config` rejection no longer named the correct command. |
| W01 | `./internal/mcpskills`; `^Test` | `handler.go`: `Content-Type` for JSON responses from `application/json` to `application/octet-stream` | Survived initially. `TestAuthAndHTTPBoundaries` now checks the discovery response MIME; the same mutant failed on the exact unexpected type. |
| W02 | `./internal/mcpskills`; `^Test` | `handler.go`: method rejection `Allow` from `POST` to `GET` | Survived initially. `TestAuthAndHTTPBoundaries` now checks the advertised method on a 405 response; the same mutant failed with `Allow = "GET", want POST`. |

### MCP file and Skills behavior

The selected package baselines passed. For M01–M03 use `go test ./internal/mcpfiles -count=1 -timeout=60s`; for M04–M05 use `go test ./internal/mcpskills -count=1 -timeout=60s`.

| ID | Production change | Initial result and final evidence |
| --- | --- | --- |
| M01 | `internal/mcpfiles/files.go`: `result["next_offset"] = end` to `result["next_offset"] = end + 1` | Killed by `TestListBounded`: cursor 101, want 100. |
| M02 | `internal/mcpfiles/search.go`: `result.ScannedEntries += len(snap.Children)` to `result.ScannedEntries += len(snap.Children) + 1` | Killed by `TestSearchResultAndScanBounds`: counted 3082, want 3078. |
| M03 | `internal/mcpfiles/search.go`: reverse `snap.Children[i].Name < snap.Children[j].Name` to `>` | Killed by `TestSearchScopeNestedCanonicalAndRead`: `Reports/Plan.TXT` and `z.TXT` appeared in reverse order. |
| M04 | `internal/mcpskills/catalog.go`: `return "sha256:" + hex.EncodeToString(h[:])` to prefix `"sha256-bad:"` | Survived initially. Added `TestSkillManifestDigestKnownVector`, which requires the independently calculated SHA-256 of the seven bytes `hello\r\n` and checks the advertised size. Identical mutant killed on the exact digest mismatch; clean test passed. |
| M05 | `internal/mcpskills/catalog.go`: `mime = "text/markdown"` to `mime = "text/plain"` | Survived initially. Added `TestSkillEntryMIMEAcrossReadSurfaces`, which asserts Markdown independently for native `resources/read` and fallback `get_skill`. The identical mutant failed both subtests; both passed clean. |

### Windows WebDAV and persistence

For each row use `go test -p=2 ./internal/cli -run '^<test>$' -count=1 -timeout=3m` with Windows loopback access. The clean fixture sample passed before and after the edits. An initial sandbox run could not reach the local HTTPS fixture; that was an environmental failure, not a kill.

| ID | Production change | Test and causal failure |
| --- | --- | --- |
| D01 | `internal/dav/content.go`: `w.WriteHeader(204)` to `w.WriteHeader(200)` on replacement PUT | `TestFilesReplaceBytesAndListMetadata`: complete replacement did not get fresh validator/status. |
| D02 | `internal/dav/delete.go`: `w.WriteHeader(http.StatusNoContent)` to `w.WriteHeader(http.StatusOK)` | `TestDeleteFileAndTreeReopen`: file DELETE 200. |
| D03 | `internal/dav/move.go`: `w.WriteHeader(201)` to `w.WriteHeader(204)` on new MOVE destination | `TestMoveFileDepthZeroAndCaseRename`: file MOVE Depth 0 returned 204. |
| D04 | `internal/sqlite/metadata.go`: `delete(node.Metadata, change.Name)` to `node.Metadata[change.Name] = ""` | `TestMetadataRemovePersists`: removed property was synthesized or reappeared after reopening the database. |
| D05 | `internal/dav/content.go`: `w.Header().Set("Content-Type", snapshot.Node.ContentType)` to `w.Header().Set("Content-Type", "application/octet-stream")` | `TestFilesReplaceBytesAndListMetadata`: replacement bytes or metadata differ. |

All five were killed by existing assertions.

### CLI, release and Windows service

Set `GOTOOLCHAIN=local` and append `-count=1 -timeout 3m -v` to the Go test commands below.

| ID | Exact production edit (restored after run) | Command / causal observation | Classification |
|---|---|---|---|
| S01 | `internal/service/manager.go`: `defer func() { err = errors.Join(err, h.Close()) }()` to `defer func() { _ = h.Close() }()` | `go test ./internal/service` initially passes (4.498s), but `go test ./internal/cli -run '^TestServiceReportingNeverStartedDoesNotHideManagerError$'` fails install subtest at line134: `lost manager error: <nil>`. | Killed by existing cross-package test; not a survivor. |
| S02 | `internal/service/host_windows.go`: `return true, 2` to `return true, 9` | Existing `go test ./internal/service ./internal/cli -run 'Test(Dispatch\|Manager\|Registration\|ServiceHost\|ServiceObserver\|ServiceReporting)'` passes (service4.757s, cli105.891s). | Genuine behavioral survivor: documented serving failure code lost. |
| S02 rerun | Exact same edit | New `go test ./internal/service -run '^TestHandlerReportsDistinctLifecycleFailureCodes$'` fails serving and wrapped-serving: `SCM result specific=true code=9 calls=1; want specific=true code=2 calls=1` (1.178s). | Killed after test-only fix. |
| S03 | `internal/cli/account_commands.go`: `command == "reset password", openPrivateConsole, policy` to `command == "add user", openPrivateConsole, policy` | `go test ./internal/cli -run '^Test(AddUserPersistsAndRejectsDuplicateWithoutMutation\|ResetPasswordPersistsAndRejectsMissingWithoutMutation)$'`: `add user failed` line59, `password reset failed` line89 (8.943s). | Killed. |
| S04 | `internal/cli/init_config.go`: `if created {` to `if !created {` | `go test ./internal/cli -run '^TestInitConfigFreshDisclosesOnlyToPrivateConsole$'`: line253, fresh initialization routineSuccess=false, exit0 (10.184s). | Killed. |
| S05 | `tools/build-release/main.go`: `state = "dirty"` to `state = "clean"` | `go test ./internal/cli -run '^TestReleaseDirtySource/untracked$'`: line168, generated binary incorrectly reports `source: clean` (22.414s). | Killed. |
| S06 | `internal/cli/run_server.go`: `Revision: revision, BuildTime: buildTime, Source: sourceState` to `Revision: "wrong-fixture-revision", BuildTime: buildTime, Source: sourceState` | `go test ./internal/cli -run '^TestRunServerRetainsOwnershipAndCredentialsAcrossRestart$'`: line599, `startup identity differs from version` (3.782s). | Killed. |
| S07 | `internal/cli/version.go`: `var sourceState = "unknown"` to `var sourceState = "clean"` | `go test ./internal/cli -run '^TestDevelopmentVersion$'`: line57, expected unknown but source clean (2.448s). | Killed. |
| S08 | `internal/cli/version.go`: `fmt.Fprint(stderr, "godav: invalid command\n", help)` to `fmt.Fprint(stdout, "godav: invalid command\n", help)` | `go test ./internal/cli -run '^TestInvalidCommand$'`: invalid command writes routine stdout rather than diagnostic stderr. | Killed. |

S02 adds a deterministic fake-SCM test for success and lifecycle codes (startup, serving, deadline, cleanup), including wrapped/joined errors. This does not exercise installed Windows services.

## Test-file candidate inventory

Each row records test files or shared test fixtures, and a behavior worth mutating or a reason to prefer normal execution. A candidate in this section was not necessarily injected.

### Config, DAV routing and HTTP/MCP integration

| File | Candidate and test seam |
| --- | --- |
| `internal/config/settings_mcp_test.go` | Parsed settings, opt-in, sample scope and account; assert exact returned budget/default grace rather than accept/reject alone. |
| `internal/config/settings_mcpfiles_test.go` | Logical root decoding, path-component distinction and default/read-write settings; compare parsed roots and explicit mode, not only the error flag. |
| `internal/dav/http_test_mode_test.go` | Omitted HTTP default port and same-scheme MOVE interpretation; use exact route status and distinguish pre-routing from the intended response. |
| `internal/dav/mcp_route_test.go` | Exact opt-in route versus disabled/subpath routing; select safe response/routing semantics without dropping security guards. |
| `internal/cli/http_test_mode_test.go` | Invalid CLI exit code, quiet stdout and actionable usage diagnostic, using in-process dispatch. |
| `internal/cli/http_test_mode_windows_test.go` | Anonymous workflow, new-store isolation, connection release between pools, cleanup on stop/bind failure, content-length rejection and unchanged bytes after an incomplete upload. Use isolated runtime fixtures; do not use real native mappings as mutation assertions. |
| `internal/cli/mcp_files_windows_test.go` | Tool schema and visibility, proof that search paths round-trip to reads, ETag conflict behavior, WebDAV visibility and exact bytes. Prefer cheap handler tests for combinatorics and use this for cross-protocol integration. |
| `internal/cli/mcp_skills_windows_test.go` | Native/fallback result parity, pinned digest/bytes, stale package rejection after WebDAV edit and opt-in/anonymous separation. Validate fixed fixture content and wire behavior, not agreement between two production helpers. |

### MCP files, search and Skills

| File | Candidate and test seam |
| --- | --- |
| `internal/mcpfiles/files_test.go` | Create/write preconditions, maximum text sizes, list cursor, argument validation and bounded reads via the public tool call surface. Duplicate fixture names test paging but not ordering. An in-memory test does not prove SQLite atomicity. |
| `internal/mcpfiles/search_test.go` | Canonical path/read round-trip, literal Unicode matching, deterministic traversal, cancellation, result and scan budgets, and partial results after a tree change. Mutate accounting, ordering or boundary rules. |
| `internal/mcpskills/handler_test.go` | Native/fallback discovery and read parity, revision and digest, MIME, JSON/metadata negotiation, tool error shape, invalid entries and changing snapshots. Use independent expected values so two surfaces cannot agree on the same wrong result. |

### Windows WebDAV and persistence

| File (`internal/cli/`) | Assessment |
|---|---|
| `bounds_windows_test.go` | Candidate: admission/deadline/drain outcome mutations; deferred this bounded pass (expensive timing suite); authentication cases excluded. |
| `delete_bounds_windows_test.go` | Candidate: tree-limit off-by-one and finite tree behavior; deferred. Storage-name safety unchanged. |
| `delete_lifecycle_windows_test.go` | Candidate: legitimate conditional delete racing save and stop/drain semantics; deferred; principal/access tests excluded. |
| `delete_windows_test.go` | Selected DAV02: committed DELETE success status through file/tree/reopen test. Other tree, condition and rollback candidates deferred. |
| `files_windows_test.go` | Selected DAV01/DAV05: replacement status and content-type preservation; ranges baseline included. Candidate payload limits, conditional revisions, restart and cancellation not exhaustively mutated. |
| `localstate_windows_test.go` | Candidate lifecycle ownership/release; deferred. Permission/ACL guards deliberately not mutated. |
| `locks_conditions_windows_test.go` | Candidate timeout/refresh/XML response semantics; deferred; access checks not mutated. |
| `locks_lifecycle_windows_test.go` | Candidate lease expiry/restart/drain outcomes; deferred; principal isolation not mutated. |
| `locks_windows_test.go` | Candidate lock-token/discovery/Explorer-sequence outcomes; deferred; no access-guard mutation. |
| `loopback_fixture_windows_test.go` | Test infrastructure, not production target. Pinning/hostname/access checks intentionally not mutated; would undermine trusted fixture boundary. |
| `malformed_state_windows_test.go` | Excluded safety/integrity validation targets from mutation; retain original corruption-refusal regression tests. |
| `metadata_bounds_windows_test.go` | Candidate drain/cancel/stop publication outcomes; deferred timing-heavy checks. |
| `metadata_conditions_windows_test.go` | Selected DAV04: remove becomes empty property, checked after reopen. Other revision/atomicity/XML candidates deferred; malformed-state safety checks excluded. |
| `metadata_response_windows_test.go` | Candidate rejected property QName/status encoding; deferred. |
| `move_bounds_windows_test.go` | Candidate last-modified and tree bounds outcomes; deferred. |
| `move_delete_windows_test.go` | Candidate legitimate composition/competition final-state semantics; deferred. |
| `move_lifecycle_windows_test.go` | Candidate move during save, reopen and failed-publication outcomes; deferred; safe trace/access unchanged. |
| `move_windows_test.go` | Selected DAV03: new-destination MOVE returns wrong success status. Other collection/collision/depth candidates deferred; authorization checks excluded. |
| `namespace_accounts_windows_test.go` | Candidate namespace preservation across offline maintenance; deferred; account/access behavior not mutated. |
| `namespace_metadata_windows_test.go` | Candidate metadata persistence and commit-only changes; deferred. |
| `namespace_windows_test.go` | Candidate listing/spelling/reopen/depth behavior; deferred; malformed-state/ACL guards excluded. |
| `wal_state_windows_test.go` | Excluded storage-integrity refusal guard from mutation; retain existing closed-WAL refusal test. |

### CLI, release and Windows service

Paths are relative to `internal/cli/` unless otherwise indicated. The non-Windows service-command counterpart was reviewed but is not executed by Windows.

| Files | Concrete reviewed contracts / candidacy |
|---|---|
| account_commands_test.go; account_commands_windows_test.go; account_https_windows_test.go | Usage refusals; duplicate/missing accounts; private output; recovery after delivery failure; durable add/reset and retained principal identity; cross-restart TLS authentication. Candidate: swapped add/reset operation selection. |
| cli_test.go; version_test.go | Exact help/invalid-command results, no writes, development identity; command/build helpers. Candidate: invalid-command exit or incorrect default identity. |
| conpty_windows_test.go | Helper-only review: isolated pseudoconsole, independent channels, bounded capture, EOF/drain/child ownership. Not mutated: would test test infrastructure rather than production contract. |
| init_config_test.go; init_config_windows_test.go | Usage/private-output checks, fresh/no-op outcomes, committed-state recovery, orphan/malformed state, OS ownership and Windows paths. Candidate: fresh vs already-initialized result inversion. Security/path gates not weakened. |
| release_test.go; release_windows_test.go; release_other_test.go | Revision/stamp/source-state, both artifact notices, platform metadata, refusal of existing destination, disposable Git cleanup. Candidate: mislabel dirty source as clean. Non-Windows cleanup helper read, not executed. |
| run_server_test.go; run_server_windows_test.go | Explicit config; real verified TLS; routing/discovery/status/XML; ownership and restart; startup identity consistency; lifecycle cleanup. Candidate: wrong startup build identity. Auth/TLS/routing safeguards not removed. |
| service_commands_test.go; service_commands_other_test.go; service_dispatch_windows_test.go | Invalid usage/paired flags and rejected SCM context; non-Windows unsupported behavior. No live SCM mutation; non-Windows test not executed. |
| service_host_windows_test.go; service_observer_windows_test.go | Channel-based SCM lifecycle, active upload drain, deadlines, checkpoint order, restart after release. Candidate: wrong service-specific terminal error classification. |
| service_policy_windows_test.go | Scoped synthetic ACL fixtures, program/file pins, retained operator access and private administration. Reviewed but no weakening mutations. |
| service_reporting_windows_test.go | Never-started 1077 exception is narrow, errors retained, start/stop and confirmed uninstall outcomes. Existing test kills install Close-error suppression even though service-only suite misses it. |
| internal/service/manager_test.go; internal/service/dispatch_windows_test.go | Fake SCM control/state transitions, policy match, observed completion, rollback/absence/deadline; dispatcher joins runner cleanup and preserves error causality. Candidate: Close-error propagation and terminal result code. |

## FTS5 content search follow-up

Windows/amd64, Go 1.27.4, `CGO_ENABLED=0`. All six new tests below
passed on clean source and failed on their corresponding valid mutant.
Each mutant was applied in isolation and removed before rerunning. No mutants
or persistent index data are part of the branch.

- F01–F04: `go test ./internal/sqlite -run '^<test>$' -count=1 -timeout=90s` in a scratch checkout.
- F05: `go test ./internal/mcpfiles -run '^TestContentSearchFilterContract$' -count=1 -timeout=2m` with a temporary Go overlay.
- F06: `go test -p=2 ./internal/cli -run '^TestMCPContentSearchAcrossWebDAV$' -count=1 -v -timeout=4m` in a scratch checkout with a disposable HTTPS fixture. Use an actual source edit there: the fixture clears `GOFLAGS` for its child build, so the initial overlay attempt did not affect the server executable and is excluded.

| ID | Test | Temporary production change | Causal failure |
| --- | --- | --- | --- |
| F01 | `TestContentSearchFullBodyPhraseAndRanking` | In `internal/sqlite/content_search.go`, replace FTS INSERT argument `string(data)` with `string(data[:min(len(data), 65536)])`. | `Documents/deep.txt` was missing when its only match was past 64 KiB. |
| F02 | `TestContentSearchRefreshAndScopedIdentity` | In `internal/sqlite/content_search.go`, CTE seed `SELECT ?, ?, 0` to `SELECT ?, lower(?), 0`. | Hit `documents/sub/one.txt` instead of canonical `Documents/Sub/one.txt`. |
| F03 | `TestContentSearchFiltersAndAccurateLimit` | In `internal/sqlite/content_search.go`, upper-date rejection `!hit.Modified.Before(o.ModifiedBefore)` to `hit.Modified.After(o.ModifiedBefore)`. | Incorrectly included `C.txt` at the exclusive upper timestamp; expected only `B.md`. |
| F04 | `TestContentSearchCapacityAndTemporaryState` | In `internal/sqlite/content_search.go`, change `count > MaxContentSearchNodes` to `count >= MaxContentSearchNodes`. | The exactly 2,048-node subtree incorrectly returned a capacity error. |
| F05 | `TestContentSearchFilterContract` | In `internal/mcpfiles/content_search.go`, change `opts.Extension = strings.ToLower(a.Extension)` to `opts.Extension = ""`. | Dropped the requested extension filter: got empty, want `.txt`. |
| F06 | `TestMCPContentSearchAcrossWebDAV` | In `internal/sqlite/content_search.go`, replace the FTS INSERT argument `string(data)` with `string(data[:min(len(data), 64<<10)])`. | The HTTPS result omitted `Large.txt`, whose match occurs after the 64 KiB MCP read limit. |

The clean Windows integration also verified pre-existing content, full-text
versus path matching, metadata filters, result limits, WebDAV replacement,
delete and MOVE (including collections moved into and out of the search
root), MCP create/edit, and reopen. No survivors in this sample.

## Scoped coding and research Skill catalogs

Host: Zen-1000, Windows amd64, Go 1.27.4 with `CGO_ENABLED=0`. Base PR #20 at
`e6cf433f3b8aa7a861ccedef45843d6a5b50f927`. The integration test starts the actual
GoDav executable on disposable authenticated HTTPS/WebDAV state. It seeds
synthetic shared, coding-only and research-only packages centrally, then checks
native and legacy fallback catalogs, supporting bytes, cross-scope URI
rejection, and the unchanged default. A controlled client cache uses a positive
one-minute TTL: repeated identical reads hit, while switching endpoint or
identity misses. Live GoDav continues to return zero TTL/private/no-store and
perform authentication independently of the test cache. This is a wire test,
not a claim about an interactive Copilot cache implementation.

Clean run (all selected tests):
`go test ./internal/config ./internal/mcpskills ./internal/cli -run '^(TestSkillCatalogSettings|TestNamedCatalogSharesDispatchAdmission|TestMCPScopedSkillCatalogs)$' -count=1 -timeout=120s`.

Each temporary mutation was run one at a time in a detached scratch checkout
using the respective package and named test (or `top-level/subtest`) as the
`-run` filter; each was restored byte-for-byte afterward.

| Selected test | Temporary production mutation | Result |
| --- | --- | --- |
| `TestSkillCatalogSettings` | `internal/config/skill_catalogs.go`: `catalogValues(spec["skills"], 32,` to `catalogValues(spec["skills"], 33,` | Killed, exit 1: accepted invalid setting, bounded membership. |
| `TestMCPScopedSkillCatalogs/CatalogViews` | `internal/mcpskills/catalog.go`: `!h.selectedSkills[dir.Name]` to `(!h.selectedSkills[dir.Name] || dir.Name != "shared")` | Killed, exit 1: coding view lost its unique package. |
| `TestMCPScopedSkillCatalogs/WarmCachePartition` | `internal/mcpskills/handler.go`: `result["ttlMs"] = 0` to `result["ttlMs"] = 1` | Killed, exit 1: live catalog no longer reported zero TTL. |
| `TestNamedCatalogSharesDispatchAdmission` | `internal/mcpskills/handler.go`: `slots: base.slots` to `slots: make(chan struct{}, 1)` in the named handler | Killed, exit 1: a busy shared dispatch returned 200 instead of 503. |

Restored run passed (config 0.072s, MCP Skills 0.032s, CLI 4.979s); 4 killed,
0 survivors, and 0 invalid/equivalent among the selected mutants. No mutated
source is committed.
