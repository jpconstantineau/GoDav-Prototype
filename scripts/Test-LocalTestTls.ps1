#requires -Version 7.4
# Public-process tests; no Pester, trust import, service, or real account required.
[CmdletBinding()]
param([switch] $SkipJunctionFixture)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'These helper tests require Windows.' }
$pwsh = (Get-Process -Id $PID).Path
$helper = Join-Path $PSScriptRoot 'New-LocalTestTls.ps1'
$root = Join-Path $env:LOCALAPPDATA ('GoDav-TlsTest-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $root | Out-Null
$checker = Join-Path $root 'tlscheck.exe'
& go build -o $checker (Join-Path $PSScriptRoot 'tlscheck\main.go')
if ($LASTEXITCODE -ne 0) { throw 'Could not build standalone Go PEM checker.' }
$output = Join-Path $root 'valid'
$started = [DateTimeOffset]::UtcNow
function Get-TrustSnapshot {
    @('Cert:\CurrentUser\Root', 'Cert:\LocalMachine\Root') | ForEach-Object {
        $store = $_
        Get-ChildItem -LiteralPath $store | ForEach-Object { "$store\$($_.Thumbprint)" }
    } | Sort-Object
}
function Assert-Rejected([string] $Path) {
    $messages = & $pwsh -NoLogo -NoProfile -NonInteractive -File $helper -OutputDirectory $Path 2>&1
    if ($LASTEXITCODE -eq 0) { throw "Expected rejection: $Path" }
    if ($messages -match 'BEGIN (RSA |EC |)PRIVATE KEY') { throw 'Private material in failure output.' }
}
function Assert-Private([string] $Path) {
    $acl = Get-Acl -LiteralPath $Path
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    if ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $sid) { throw 'Wrong output owner.' }
    $rules = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
    if ($rules.Count -ne 1 -or $rules[0].IdentityReference.Value -ne $sid -or
        $rules[0].AccessControlType -ne 'Allow' -or $rules[0].FileSystemRights -ne 'FullControl') {
        throw 'Output ACL permits an unexpected principal or rights.'
    }
}
$trustBefore = @(Get-TrustSnapshot)
try {
    $result = & $pwsh -NoLogo -NoProfile -NonInteractive -File $helper -OutputDirectory $output 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Helper failed: $result" }
    $manifestText = Get-Content -LiteralPath (Join-Path $output 'manifest.json') -Raw
    $manifest = $manifestText | ConvertFrom-Json
    if ($manifest.Hostname -ne 'localhost') { throw 'Expected localhost profile.' }
    if (((Get-ChildItem -LiteralPath $output -File).Name | Sort-Object) -join ',' -ne 'manifest.json,server.cer,server.crt,server.key') { throw 'Unexpected artifact set.' }
    if ($manifest.PowerShellVersion -ne $PSVersionTable.PSVersion.ToString() -or $manifest.PowerShellExecutable -ne $pwsh) { throw 'Runtime identity not recorded accurately.' }
    if ($manifest.TrustImported -or $null -ne $manifest.TrustStore) { throw 'Must not claim trust.' }
    if (($result -join "`n") -match 'BEGIN (RSA |EC |)PRIVATE KEY' -or $manifestText -match 'BEGIN (RSA |EC |)PRIVATE KEY') { throw 'Private material leaked into public output.' }
    $keyText = [IO.File]::ReadAllText($manifest.Files.PrivateKeyPem)
    if (-not $keyText.StartsWith('-----BEGIN PRIVATE KEY-----')) { throw 'Expected PKCS#8 PEM.' }
    $keyBody = ($keyText -split '\r?\n')[1]
    if (($result -join "`n").Contains($keyBody) -or $manifestText.Contains($keyBody)) { throw 'Private key bytes leaked.' }
    Assert-Private $output
    if (-not (Get-Acl -LiteralPath $output).AreAccessRulesProtected) { throw 'Output directory inherits unrelated access.' }
    foreach ($file in Get-ChildItem -LiteralPath $output -File) { Assert-Private $file.FullName }
    $public = [Security.Cryptography.X509Certificates.X509Certificate2]::new($manifest.Files.PublicCertificateDer)
    try {
        if ($public.HasPrivateKey -or $public.Thumbprint -ne $manifest.ThumbprintSha1 -or
            $public.GetCertHashString([Security.Cryptography.HashAlgorithmName]::SHA256) -ne $manifest.FingerprintSha256) { throw 'DER/manifest mismatch or private key in import artifact.' }
    } finally { $public.Dispose() }
    Write-Output 'PASS: private artifacts, public-only manifest/DER, and exact runtime metadata.'

    $check = & $checker $manifest.Files.CertificatePem $manifest.Files.PrivateKeyPem 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Go PEM-pair check failed: $check" }
    $profile = $check | ConvertFrom-Json
    if ($profile.dns_names.Count -ne 1 -or $profile.dns_names[0] -ne 'localhost') { throw 'Wrong DNS SAN.' }
    if (($profile.ip_addresses -join ',') -ne '127.0.0.1,::1') { throw 'Wrong IP SANs.' }
    if ($profile.key_bits -ne 3072 -or $profile.signature -ne 'SHA256-RSA') { throw 'Wrong key/signature profile.' }
    if ($profile.is_ca -or ($profile.extended_key_usage -join ',') -ne '1') { throw 'Expected server-only non-CA leaf.' }
    if ($profile.key_usage -ne 5) { throw 'Expected digital-signature and key-encipherment usage only.' }
    if (([DateTimeOffset]$profile.not_after - [DateTimeOffset]$profile.not_before).TotalDays -ne 7) { throw 'Expected seven-day validity.' }
    if ([DateTimeOffset]$profile.not_before -gt $started -or [DateTimeOffset]$profile.not_before -lt $started.AddMinutes(-6)) { throw 'Wrong not-before bound.' }
    if ($profile.sha256 -ne $manifest.FingerprintSha256) { throw 'Manifest fingerprint mismatch.' }
    if ($profile.subject -ne 'CN=localhost' -or $profile.issuer -ne 'CN=localhost') { throw 'Expected self-signed localhost.' }
    Write-Output 'PASS: Go TLS loads PEM pair; independently parsed SAN, key, EKU, usage, expiry and fingerprint match.'

    $hashBefore = @(Get-ChildItem -LiteralPath $output -File | Sort-Object Name | Get-FileHash | Select-Object -ExpandProperty Hash)
    Assert-Rejected $output
    $hashAfter = @(Get-ChildItem -LiteralPath $output -File | Sort-Object Name | Get-FileHash | Select-Object -ExpandProperty Hash)
    if (Compare-Object $hashBefore $hashAfter) { throw 'Existing files changed on rejection.' }
    $empty = New-Item -ItemType Directory -Path (Join-Path $root 'empty')
    Assert-Rejected $empty.FullName
    if (@(Get-ChildItem -LiteralPath $empty.FullName -Force).Count) { throw 'Pre-existing empty directory was reused.' }
    $partial = New-Item -ItemType Directory -Path (Join-Path $root 'partial')
    $sentinel = Join-Path $partial.FullName 'server.key'
    [IO.File]::WriteAllText($sentinel, 'incomplete-fixture-sentinel')
    Assert-Rejected $partial.FullName
    if ([IO.File]::ReadAllText($sentinel) -ne 'incomplete-fixture-sentinel') { throw 'Partial fixture was replaced.' }
    Write-Output 'PASS: nonempty, empty and interrupted-output directories are never reused.'

    foreach ($kind in @('checkout', 'worktree')) {
        $repo = New-Item -ItemType Directory -Path (Join-Path $root $kind)
        $marker = Join-Path $repo.FullName '.git'
        if ($kind -eq 'checkout') { New-Item -ItemType Directory -Path $marker | Out-Null }
        else { [IO.File]::WriteAllText($marker, 'gitdir: fixture') }
        $rejectedPath = Join-Path $repo.FullName 'tls'
        Assert-Rejected $rejectedPath
        if (Test-Path -LiteralPath $rejectedPath) { throw 'Created output inside a checkout/worktree.' }
    }
    $missingParent = Join-Path $root 'missing\tls'
    Assert-Rejected $missingParent
    if (Test-Path -LiteralPath (Split-Path $missingParent -Parent)) { throw 'Created an unapproved output parent.' }
    Assert-Rejected '.\relative-tls'
    Assert-Rejected (Join-Path $root 'trailing-dot.')
    Assert-Rejected (Join-Path $root 'NUL')
    Assert-Rejected (Join-Path ([IO.Path]::GetTempPath()) ('godav-' + [guid]::NewGuid()))
    if ($SkipJunctionFixture) {
        Write-Output 'SKIP: junction fixture/refusal test, explicitly disabled for restricted execution environments.'
    } else {
        $junction = Join-Path $root 'redirect'
        New-Item -ItemType Junction -Path $junction -Target $empty.FullName | Out-Null
        Assert-Rejected (Join-Path $junction 'tls')
        if (Test-Path -LiteralPath (Join-Path $empty.FullName 'tls')) { throw 'Followed an output junction.' }
        Write-Output 'PASS: redirected output ancestor rejected.'
    }
    Write-Output 'PASS: checkout/worktree, relative, missing-parent, ambiguous and temporary paths rejected.'

    $raceOutput = Join-Path $root 'concurrent'
    $processes = @()
    foreach ($number in 1..2) {
        $start = [Diagnostics.ProcessStartInfo]::new($pwsh)
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        foreach ($argument in @('-NoLogo', '-NoProfile', '-NonInteractive', '-File', $helper, '-OutputDirectory', $raceOutput)) { $start.ArgumentList.Add($argument) }
        $process = [Diagnostics.Process]::Start($start)
        $processes += [pscustomobject]@{ Process = $process; Output = $process.StandardOutput.ReadToEndAsync(); Error = $process.StandardError.ReadToEndAsync() }
    }
    $codes = @()
    foreach ($entry in $processes) {
        $entry.Process.WaitForExit()
        $codes += $entry.Process.ExitCode
        if ($entry.Output.Result -match 'PRIVATE KEY' -or $entry.Error.Result -match 'PRIVATE KEY') { throw 'Race output leaked private key material.' }
        $entry.Process.Dispose()
    }
    if (($codes | Sort-Object) -join ',' -ne '0,1') { throw 'Concurrent creation must have exactly one winner.' }
    $raceManifest = Get-Content -LiteralPath (Join-Path $raceOutput 'manifest.json') -Raw | ConvertFrom-Json
    $raceCheck = & $checker $raceManifest.Files.CertificatePem $raceManifest.Files.PrivateKeyPem 2>&1
    if ($LASTEXITCODE -ne 0) { throw 'Concurrent winner did not publish a complete matching pair.' }
    $wrongPair = & $checker $manifest.Files.CertificatePem $raceManifest.Files.PrivateKeyPem 2>&1
    if ($LASTEXITCODE -eq 0 -or ($wrongPair -join "`n").Contains($keyBody)) { throw 'Mismatched PEM pair must fail safely.' }
    Write-Output 'PASS: concurrent generation has one complete winner; mismatched keys are rejected.'

    $badPem = Join-Path $root 'invalid.pem'
    [IO.File]::WriteAllText($badPem, 'not a certificate')
    $badCheck = & $checker $badPem $manifest.Files.PrivateKeyPem 2>&1
    if ($LASTEXITCODE -eq 0 -or ($badCheck -join "`n").Contains($keyBody)) { throw 'Go checker must reject malformed certificates safely.' }
    Write-Output 'PASS: Go loading rejects malformed PEM without exposing private bytes.'
    if (Compare-Object $trustBefore @(Get-TrustSnapshot)) { throw 'Root trust stores changed during helper tests.' }
    Write-Output 'PASS: CurrentUser/LocalMachine root trust unchanged.'
    Write-Output "Verified PowerShell: $pwsh ($($PSVersionTable.PSVersion))"
    Write-Output (& go version)
    Write-Output "Public manifest: $($manifest.Files.Manifest)"
    Write-Output "PASS: enabled helper checks. Private test fixtures retained for explicit exact-path cleanup: $root"
} catch {
    Write-Output "FAILED. Review isolated test fixtures at: $root"
    throw
}
