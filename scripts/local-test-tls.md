# Local test TLS helper

This is the independent fixture helper for issue #14 (slice 13). It is not part
of GoDav bootstrap and is not a production CA. Nothing in this runbook approves
a Windows trust change, service registration, account setup, or native-client
acceptance run. Those are separate actions.

## Fixed prototype contract

- Windows with PowerShell 7.4+ and its .NET certificate/PEM APIs. No install is
  performed. Windows PowerShell 5.1 is unsupported. Record the exact executable
  and version, not just the command name `pwsh`.
- Exact hostname: `localhost`. The certificate has DNS SAN `localhost` and real
  IP SANs `127.0.0.1` and `::1`, never wildcard or LAN names. These IP SANs do not
  enable a listener or broaden GoDav's permitted HTTP origin.
- Self-signed RSA-3072 / SHA-256 server leaf, CA=false, server-auth EKU only,
  digital-signature and key-encipherment usage. Valid for seven days from five
  minutes before generation (clock-skew allowance). No reusable CA is created.
- Four files: `server.crt` (public PEM), `server.key` (unencrypted PKCS#8 PEM),
  `server.cer` (public DER for a separately approved Windows import), and
  `manifest.json` (public certificate/runtime metadata and exact artifact paths).
- Supply a **new absolute local directory whose parent already exists**. It must
  be outside every checkout/worktree and routine temporary storage, with no
  junction/symlink ancestors or ambiguous Windows path components. Its owner-only
  inheritable DACL is supplied atomically when creating the directory. The helper
  never changes existing directory/file ACLs or grants a service identity access.
  Administrators can still take ownership; this is not encryption at rest.
- Existing output, even empty or incomplete, is an error. Exclusive directory
  creation and exclusive file creation prevent reuse/overwrite. The manifest is
  written last; missing manifest means incomplete generation. On failure, leave
  partial files private for exact-path review rather than deleting unknown data.
  Use a new directory after reviewing an interrupted run. Errors return nonzero;
  success prints only the public manifest JSON and returns zero.

These are bounded fixture defaults, not a claim that Windows WebClient trusts
this leaf. Store selection and native compatibility remain unverified until the
separate acceptance steps below are approved and run.

## 1. Generate and inspect, without trust

Choose an existing local parent under your own profile, not a shared/temp folder.
Do not put the directory under this repository or any Git worktree. Example:

```powershell
$pwsh = (Get-Command pwsh -ErrorAction Stop).Source
& $pwsh -NoLogo -NoProfile -NonInteractive -File .\scripts\New-LocalTestTls.ps1 `
  -OutputDirectory 'C:\Users\YOUR_NAME\GoDav-TestTls-2026-11-14'
```

Replace the example with the exact reviewed new directory. A full PowerShell
executable path works if `pwsh` is not on the operator's PATH. Do not use an
execution-policy bypass. If policy blocks the script, report the blocker.

Inspect `manifest.json` and the public `.cer`: subject/issuer, all SANs, server EKU,
CA=false, expiry, SHA-1 Windows thumbprint, SHA-256 fingerprint, and exact output
paths. The manifest records PowerShell executable/version and .NET version. The
DER certificate must have no private key. Never print/copy/upload `server.key`,
put it in source control, or include it in evidence. A private key path in public
metadata is not the private key itself.

The generator does not import trust, edit YAML, provision an account, listen on a
port, or register a service. It creates only its own private output directory and
four files. Its key remains untrusted until separately approved setup.

## 2. Prove Go can load the PEM pair before trust

From the checkout, using the exact generated paths:

```powershell
go run .\scripts\tlscheck\main.go `
  'C:\Users\YOUR_NAME\GoDav-TestTls-2026-11-14\server.crt' `
  'C:\Users\YOUR_NAME\GoDav-TestTls-2026-11-14\server.key'
if ($LASTEXITCODE -ne 0) { throw 'Stop: PEM pair did not load.' }
```

This standalone command uses Go's `tls.LoadX509KeyPair` to parse and match the
pair, then reports public certificate fields. It does not install trust, start
GoDav, make a network request, or claim certificate-chain/hostname validation.
Compare its fingerprint/profile with the reviewed manifest. Keep this successful
loading result and the exact Go/PowerShell versions as evidence before importing.

The public helper tests need only installed Go and PowerShell, not Pester:

```powershell
& $pwsh -NoLogo -NoProfile -NonInteractive -File .\scripts\Test-LocalTestTls.ps1
```

Tests create disposable fixture directories outside the checkout in LOCALAPPDATA,
exercise actual helper/checker processes, check file ACLs and compare current-user
and machine root trust snapshots. They retain private fixtures and print the
exact root for explicit cleanup. No trust import or removal is part of the tests. If the execution environment
forbids creating even a disposable NTFS junction, report that exact setup failure.
`-SkipJunctionFixture` explicitly omits only that case; a run with this flag is not
a full junction/refusal pass. Do not bypass the environment's restriction.

## 3. Separate approval: trust the exact public certificate

**Stop here until approved for this exact certificate and store.** Record the
full public `.cer` path, SHA-256 fingerprint, Windows thumbprint, target store,
scope, and intended test purpose. Verify those against the actual certificate,
not just a possibly stale manifest. Review expiry again.

`Cert:\LocalMachine\Root` is a possible store for WebClient testing, affects all
local users, and requires elevation. It is not preselected or approved here.
`Cert:\CurrentUser\Root` affects only that account and is not assumed sufficient
for Explorer's WebClient service. Browser success does not prove WebClient trust.
A direct self-signed leaf is the initial test approach, not a production issuer.
If Windows rejects it, report the evidence and revisit the certificate/trust plan;
do not silently introduce a CA or bypass validation.

After explicit approval, the operator can import **only the reviewed public CER**
with `Import-Certificate -FilePath <exact CER path> -CertStoreLocation <approved
store>`. Never import the private key. Record the resulting exact store/thumbprint
and verify it matches the reviewed artifact. Keep that trust receipt separately;
`TrustImported: false` in the generation manifest describes generation, not later
machine state. Never claim a trust change without observing the result.

## 4. Separate configuration and native acceptance

When the server/configuration slice is implemented, set its documented YAML
`tls.cert_file` and `tls.key_file` to the exact PEM paths. Set the canonical origin
and Explorer URL to the approved hostname/port, for example the currently
proposed `https://localhost:8443/dav/`. Do not rewrite YAML from this helper.

Foreground operation under the generating user can read the output. A service
identity cannot: any narrowly scoped read grant requires separate approved
provisioning. Do not make keys broadly readable to fix startup.

After separately approved trust/configuration and server startup, run actual
Explorer mapping/browse/copy and desktop VS Code save/readback. Repeat the
required service acceptance later. Test wrong-host, expired and untrusted
certificates as failures, without an ignore-validation flag, WebClient registry
workaround, insecure HTTP, or disabling Basic-auth transport protection.
Standalone PEM loading and HTTP tests do not prove native DAV compatibility.

## 5. Separate exact cleanup or renewal

Stop GoDav and confirm it is not using these files. Review the intended resolved
absolute output directory. Inspect the manifest's four paths, require that each
is the matching direct child filename above, and reject reparse points or paths
outside that one reviewed directory. Verify the public CER fingerprint and
thumbprint again. If generation failed before a manifest existed, inspect only
the exact new output directory and the known filenames; never infer ownership of
other entries.

Trust removal is a **separately approved security change**. Check the saved trust
receipt and actual entry for the exact approved store/thumbprint. If they match,
remove only `Cert:\<approved scope>\Root\<exact thumbprint>` with a literal path.
Verify unrelated trust entries are unchanged. Never delete by subject, common
name, `localhost`, wildcard, or by clearing a store. Do not assume a manifest
means a certificate was actually imported.

File cleanup is separate from trust cleanup. After exact-path review/approval,
remove only `server.key`, `server.crt`, `server.cer`, and `manifest.json` with
`Remove-Item -LiteralPath`. Remove the output directory only when empty, without
`-Recurse` or `-Force`; an unexpected file is a reason to stop and inspect. Test
fixture roots can contain intentionally invalid/sentinel files and junctions;
review each exact artifact and remove the junction itself, never traverse it.
Never delete a computed tree or unrelated data.

Renewal uses a new directory, newly reviewed public metadata, a successful Go PEM
load, and separate trust/configuration approval. It never silently overwrites a
key, extends trust, removes the old entry, or rotates a running server.
