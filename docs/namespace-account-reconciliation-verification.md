# Collections and offline account reconciliation

Issues: [#5](https://github.com/jpconstantineau/GoDav-Prototype/issues/5) and
[#11](https://github.com/jpconstantineau/GoDav-Prototype/issues/11).
Verified on Zen-1000, Windows amd64, 2026-11-14 UTC.

## Candidate and scope

- Clean implementation merge: `fb84458b57484937051fab373221cca9c1be2a72`.
- First parent, settled HTTPS/accounts: `849d4839af528384cb791ee28224034e73abecea`.
- Second parent, reviewed collections handoff: `325c68f78e938044a2bbb56c8bd762a881b6dc3c`.
- Isolated branch: `codex/godav-05-11-integration-check`.
- Worktree: `GoDav-Worktrees/integration-check-folders-accounts`.
- This document is a later documentation-only handoff. All final runtime gates
  and release artifacts identify the implementation commit above.

The merge was conflict-free. All 17 paths changed by the collections branch are
blob-identical to its reviewed handoff. The narrow reconciliation adds one
`s.acquire(ctx)` and deferred release to each of `AddAccount` and `ResetPassword`,
after initial guards and before the entire retained-connection transaction.
Acquisition failures retain the existing fixed errors. Rollback/commit completes
before release; there is no nested gate, extra connection, lock or API.

Existing opening still accepts v1/v2 and validates namespace state for v2 without
migrating. Bootstrap still creates v1. Only explicit server startup separately
prepares namespace under WriteExisting ownership. Header, sidecar, private-parent,
permission, exclusive ownership and account identity guards remain unchanged.
Dependencies and their pins are unchanged.

Beyond the reviewed source union, the only code changes are the eight gate lines
in `internal/sqlite/account_write.go`, the existing account HTTPS fixture's
post-preparation snapshot baseline, and the new public regression in
`internal/cli/namespace_accounts_windows_test.go`.

## Public regression and progression

`TestNamespaceSurvivesOfflineAccountAdministration` uses real disposable CLI
bootstrap, private ConPTY account commands and actual loopback HTTPS with an
isolated trusted test certificate pool. Certificate verification stays enabled.

It creates three nested, case-preserved collections with escaped percent, space,
ampersand and Unicode names, plus an unrelated collection. Shared independent XML
parsing verifies known canonical hrefs/display names, collection property statuses,
unsupported-property 404s and UTC-second metadata. While the server owns state,
actual private-console add/reset commands must reach the ownership refusal.

After terminating and joining the server, actual offline commands add an account
and reset the original owner's password. The previously approved production
LookupEnabled/VerifyPassword seam checks stable identity and unchanged unrelated
credentials only while offline. Restarted HTTPS proves the replaced password
returns 401, replacement/new/unrelated credentials work, the refused new account
does not exist, and the complete canonical hierarchy and metadata are unchanged.
Settings bytes and post-administration state remain unchanged by restart/serving.
There are no private SQL preservation assertions, mock stores or new endpoints.
Secret captures remain memory-only; failures and server-output checks are sanitized.

The new combined regression passed before the gate insertion. It is integration
coverage, not a claimed red/green production defect. The first run also exposed
the older account test's v1 byte snapshot preceding intentional v2 preparation.
That test now starts/stops the real server before taking its unchanged-state
baseline; all previous assertions remain. Its failed run is retained as evidence,
not relabeled as a production failure. No additional concurrent-write test seam
or fault control was introduced to manufacture a red result.

## Executed gates

Logs are under `GoDav-Project-Tools/logs`, prefixed `reconcile05-11-` and suffixed
`-20261114.log` (JSON where noted). Commands use only verified Go 1.27.4 and
govulncheck 1.9.0 with process-local settings and no automatic toolchain download.

| Gate | Log number/name | Result |
| --- | --- | --- |
| Before-gate combined check | 01-before-gate | New regression passed; old snapshot failed as described |
| Corrected focused check and three repeats | 02-focused-after-gate, 03-repeat | Pass |
| Exact clean-commit focused tests, three repeats | 23-exact-commit-focused-repeat | Pass; 23.310s |
| Full Windows `go test ./... -count=1 -v -timeout=10m` | 04-full-test | Pass; CLI 275.542s |
| Windows/Linux vet | 05-vet-windows, 08-vet-linux | Pass |
| Module verify and tidy-diff | 06-mod-verify, 07-mod-tidy | Pass; no changes |
| Windows/Linux application and test source scans | 09 through 12 | No known vulnerabilities |
| Existing GCC 14.2; bounded integrated race run | 13-existing-gcc, 14-race-integrated | Pass; 28.686s |
| Five real in-process namespace race/lifecycle runs | 22-race-namespace-repeat | Pass; 15.870s |
| Clean release builds, version and both build metadata | 15 through 18 | Pass; exact clean identity |
| Both release binary scans | 19, 20 | No known vulnerabilities |
| Strict UTF-8, CRLF-normalized formatting; source/release checks | 21-source-release-verification.json | All 63 Go files pass |
| Final code/source/evidence preservation | 24-final-code-preservation.json | Pass |

The integrated race run covers in-process namespace concurrency and context stop,
HTTPS context stop, the combined account regressions and foundation lifecycle,
ownership and refusal tests. It uses the existing GCC at
`C:/msys64/ucrt64/bin/gcc.exe`. Ordinary CLI children remain CGO-free and are not
race-instrumented. The whole-transaction account gate is also verified structurally;
these results do not claim a newly added in-process concurrent account-write test.

## Artifacts and integrity

Release directory: `GoDav-Artifacts-Go1.27.4-fb84458-reconcile05-11-20261114`.
Both targets embed revision `fb84458b57484937051fab373221cca9c1be2a72`, build time
`2026-11-14T18:54:43Z`, source `clean`, Go 1.27.4 and CGO disabled. Companion
notices exactly match source and test-only fault tags/markers are absent.

| Artifact | SHA-256 |
| --- | --- |
| windows-amd64/godav.exe | `C621C245D118E3968218A1B8E0AF031C7472CA55678B003FA50F29A260787625` |
| linux-amd64/godav | `BCD0932AE8CF1BE7671ADCE7EC53036632E1F0CEC52A589588F29F26DC63AD43` |
| THIRD_PARTY_NOTICES.txt, both targets/source | `597073F6057A260E696F9E051918A8F5D1889D3F35ED0B0AAF84E76917024074` |

`GoDav-Project-Tools/logs/reconcile05-11-evidence-sha256-20261114.json` records 32
files, with every saved length/hash re-read and verified. Manifest SHA-256:
`A55EBAAC4B0F98F0F56CD5390EF3560A5D76458968DBFD964B266748E9883E06`.
The earlier collections and integrated-account manifests and all their 93 entries
were verified before and after this work without modification.

## Limits and review handoff

The new executable restart regression force-terminates and joins its process;
it does not prove graceful foreground/SCM shutdown. Separate existing in-process
context-stop tests still pass. This establishes collection hierarchy and exposed
metadata preservation, not file-content preservation, power-loss durability,
native-client acceptance or completion of the full GoDav specification.

Linux was cross-built, vetted and scanned, not run. No real credentials, trust,
service/global settings, dependency updates, denied hardlink/junction retries,
existing fixture cleanup, tracker changes or pushes were involved. Primary,
reviewed source worktree and the original read-only checkout/owner notes remained
unchanged. This isolated candidate is prepared for fresh independent review;
only a subsequent merger may settle primary after approval and verification.
