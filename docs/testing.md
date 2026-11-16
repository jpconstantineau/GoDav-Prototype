# Testing and acceptance

Use this reference to select checks and interpret evidence. Tests carry executable
detail; this page records coverage boundaries/open gates. Editing it proves no pass.

## Ordinary checks

Use the environment from [Build and release](build-identity.md):

    go test ./...
    go vet ./...
    go mod verify

Run Windows runtime suites on Windows. A Linux cross-build is not Linux execution.
Tests use disposable state, fixture ACLs/pseudoconsoles and test-client TLS roots,
not machine trust changes. Build-tag-only failure fixtures have no production
runtime switch and are excluded from ordinary/release binaries.

## Coverage map

These links lead to executable tests rather than historical run logs.

| Area | Main sources |
| --- | --- |
| Build/packaging | [release](../internal/cli/release_test.go) |
| Bootstrap/delivery/recovery | [init](../internal/cli/init_config_windows_test.go) |
| Offline accounts/authenticated restart | [commands](../internal/cli/account_commands_windows_test.go), [HTTPS](../internal/cli/account_https_windows_test.go) |
| Session ownership/cleanup | [local state](../internal/cli/localstate_windows_test.go) |
| TLS/auth/routing | [server](../internal/cli/run_server_windows_test.go) |
| Names/persistence | [namespace](../internal/cli/namespace_windows_test.go), [accounts](../internal/cli/namespace_accounts_windows_test.go) |
| Atomic snapshots/limits | [files](../internal/cli/files_windows_test.go) |
| Metadata/conditions/lifecycle | [properties](../internal/cli/metadata_conditions_windows_test.go), [bounds](../internal/cli/metadata_bounds_windows_test.go) |
| Locks/principals/restart | [locks](../internal/cli/locks_windows_test.go), [lifecycle](../internal/cli/locks_lifecycle_windows_test.go) |
| MOVE/DELETE composition | [MOVE](../internal/cli/move_windows_test.go), [DELETE](../internal/cli/delete_windows_test.go), [combined](../internal/cli/move_delete_windows_test.go) |
| Admission/deadlines/shutdown | [bounds](../internal/cli/bounds_windows_test.go) |
| Service management/reporting | [manager](../internal/service/manager_test.go), [host](../internal/cli/service_host_windows_test.go), [policy](../internal/cli/service_policy_windows_test.go), [reporting](../internal/cli/service_reporting_windows_test.go) |

Real loopback HTTPS/SQLite and executable reopen test public behavior.
Some service tests use fake SCM/status backends or synthetic identities; they do
not establish real service logon/effective token access. Loopback fixtures pin
disposable IPv4 endpoints while retaining hostname/certificate verification.
They do not validate machine DNS or native-client interoperability. An automated
Explorer-like sequence is not an actual Explorer acceptance run.

## Recorded baseline

Implementation e2ebed2b5829ca4e7fa4191a50a6b17db1c25cb8 was recorded with:
- 233 ordinary automated Windows tests passing, no skips.
- 60 additional CGO_ENABLED=0 functional concurrency repetitions passing:
  20 each for file snapshots, MOVE collision/independent siblings and lock creation.
- CGO-free Windows/Linux builds, vet, module verification/tidy, formatting and
  dependency checks passing.
- Real service install/check/start, authentication rejection/success, save/readback,
  stop/restart persistence, uninstall and independent absence checks passing.

These results apply to that implementation, not arbitrary later changes.
This reference cleanup is documentation-only; it does not rerun or expand results.
Dated proof logs and slice reviews are not maintained product documentation.

## Remaining gates

- Effective service-token/permissions inspection remains unresolved after Windows
  access denial. Conservative ACL checks do not replace that acceptance.
- Broader real SCM failure/pending/shutdown and retained service diagnostics are
  not fully validated.
- Real cross-volume service sessions remain unproved on the one-volume host.
- Linux runtime execution is untested.
- Actual Explorer/VS Code trusted-HTTPS acceptance and WsgiDAV trace comparison
  are deferred by agreement; ordinary tests make no machine trust changes.
- The broader Windows instrumented race run is inconclusive after a TSan runtime
  failure. No reruns are planned under current direction. Functional repetition
  is not race-freedom proof; instrumented in-process harnesses do not instrument
  their separately built ordinary CLI children.
- Synthetic failures establish controlled rollback/cleanup, not real disk failure,
  power-loss recovery or complete crash durability.

Passing automated checks does not waive these gates or imply production readiness.
