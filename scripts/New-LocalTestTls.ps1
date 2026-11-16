#requires -Version 7.4
<#
.SYNOPSIS
Creates untrusted, seven-day localhost test TLS files in a new private directory.
.DESCRIPTION
Windows only. The output parent must already exist. Never imports trust, edits
configuration, registers a service, or overwrites output. See local-test-tls.md.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $OutputDirectory
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

try {
    if (-not $IsWindows) { throw 'This helper requires Windows and PowerShell 7.4 or later.' }
    $output = [IO.Path]::GetFullPath($OutputDirectory)
    if (-not [IO.Path]::IsPathFullyQualified($OutputDirectory) -or $output -notmatch '^[A-Za-z]:\\') {
        throw 'OutputDirectory must be an absolute local drive path.'
    }
    $output = $output.TrimEnd('\')
    $parent = [IO.Path]::GetDirectoryName($output)
    if (-not [IO.Directory]::Exists($parent)) { throw 'The output parent directory must already exist.' }
    if (Test-Path -LiteralPath $output) { throw 'OutputDirectory already exists; nothing was overwritten.' }
    foreach ($part in $OutputDirectory.TrimEnd('\').Substring(3).Split('\')) {
        if ($part -match '[<>:"/|?*]' -or $part -match '[. ]$' -or
            $part -match '^(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])(?:\.|$)') {
            throw 'OutputDirectory contains an ambiguous Windows path component.'
        }
    }
    # Do not follow junctions/symlinks, write under any checkout, or place a key
    # in routine temporary storage. Worktrees have a .git file, not a directory.
    $temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\')
    if ($output.Equals($temporaryRoot, [StringComparison]::OrdinalIgnoreCase) -or
        $output.StartsWith($temporaryRoot + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Use a dedicated private output directory outside temporary storage.'
    }
    for ($ancestor = [IO.DirectoryInfo]::new($parent); $null -ne $ancestor; $ancestor = $ancestor.Parent) {
        if (($ancestor.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw 'OutputDirectory ancestors must not be reparse points.'
        }
        if (Test-Path -LiteralPath (Join-Path $ancestor.FullName '.git')) {
            throw 'OutputDirectory must be outside every checkout or worktree.'
        }
    }

    # Supply the owner-only DACL at directory creation, before any key exists.
    # CreateDirectoryW fails if another invocation has already claimed the path.
    Add-Type -TypeDefinition @"
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
public static class GoDavPrivateDirectory {
    [StructLayout(LayoutKind.Sequential)]
    private struct SecurityAttributes {
        public int Length;
        public IntPtr Descriptor;
        [MarshalAs(UnmanagedType.Bool)] public bool InheritHandle;
    }
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CreateDirectoryW(string path, ref SecurityAttributes attributes);
    public static void Create(string path, byte[] descriptor) {
        var pinned = GCHandle.Alloc(descriptor, GCHandleType.Pinned);
        try {
            var attributes = new SecurityAttributes {
                Length = Marshal.SizeOf<SecurityAttributes>(),
                Descriptor = pinned.AddrOfPinnedObject(), InheritHandle = false
            };
            if (!CreateDirectoryW(path, ref attributes))
                throw new Win32Exception(Marshal.GetLastWin32Error());
        } finally { pinned.Free(); }
    }
}
"@
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $security = [Security.AccessControl.DirectorySecurity]::new()
    $security.SetOwner($sid)
    $security.SetAccessRuleProtection($true, $false)
    $security.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new(
        $sid, 'FullControl', 'ContainerInherit, ObjectInherit', 'None', 'Allow'))
    [GoDavPrivateDirectory]::Create($output, $security.GetSecurityDescriptorBinaryForm())
    # Fail before key generation if the filesystem did not preserve the DACL.
    $actualAcl = Get-Acl -LiteralPath $output
    $actualRules = @($actualAcl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
    if (-not $actualAcl.AreAccessRulesProtected -or
        $actualAcl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $sid.Value -or
        $actualRules.Count -ne 1 -or $actualRules[0].IdentityReference.Value -ne $sid.Value -or
        $actualRules[0].AccessControlType -ne 'Allow' -or $actualRules[0].FileSystemRights -ne 'FullControl' -or
        $actualRules[0].InheritanceFlags -ne 'ContainerInherit, ObjectInherit') {
        throw 'The output filesystem did not establish the required private ACL.'
    }

    $rsa = [Security.Cryptography.RSA]::Create(3072)
    $certificate = $null
    try {
        $request = [Security.Cryptography.X509Certificates.CertificateRequest]::new(
            'CN=localhost', $rsa, [Security.Cryptography.HashAlgorithmName]::SHA256,
            [Security.Cryptography.RSASignaturePadding]::Pkcs1)
        $request.CertificateExtensions.Add([Security.Cryptography.X509Certificates.X509BasicConstraintsExtension]::new($false, $false, 0, $true))
        $request.CertificateExtensions.Add([Security.Cryptography.X509Certificates.X509KeyUsageExtension]::new(
            [Security.Cryptography.X509Certificates.X509KeyUsageFlags]::DigitalSignature -bor
            [Security.Cryptography.X509Certificates.X509KeyUsageFlags]::KeyEncipherment, $true))
        $eku = [Security.Cryptography.OidCollection]::new()
        [void] $eku.Add([Security.Cryptography.Oid]::new('1.3.6.1.5.5.7.3.1'))
        $request.CertificateExtensions.Add([Security.Cryptography.X509Certificates.X509EnhancedKeyUsageExtension]::new($eku, $false))
        $san = [Security.Cryptography.X509Certificates.SubjectAlternativeNameBuilder]::new()
        $san.AddDnsName('localhost')
        $san.AddIpAddress([Net.IPAddress]::Parse('127.0.0.1'))
        $san.AddIpAddress([Net.IPAddress]::Parse('::1'))
        $request.CertificateExtensions.Add($san.Build())
        $notBefore = [DateTimeOffset]::UtcNow.AddMinutes(-5)
        $certificate = $request.CreateSelfSigned($notBefore, $notBefore.AddDays(7))
        $paths = [ordered]@{
            CertificatePem = Join-Path $output 'server.crt'
            PrivateKeyPem = Join-Path $output 'server.key'
            PublicCertificateDer = Join-Path $output 'server.cer'
            Manifest = Join-Path $output 'manifest.json'
        }
        function Write-NewFile([string] $Path, [byte[]] $Bytes) {
            $file = [IO.File]::Open($Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
            try { $file.Write($Bytes) } finally { $file.Dispose() }
        }
        Write-NewFile $paths.CertificatePem ([Text.Encoding]::ASCII.GetBytes($certificate.ExportCertificatePem() + "`n"))
        Write-NewFile $paths.PrivateKeyPem ([Text.Encoding]::ASCII.GetBytes($rsa.ExportPkcs8PrivateKeyPem() + "`n"))
        Write-NewFile $paths.PublicCertificateDer ($certificate.Export([Security.Cryptography.X509Certificates.X509ContentType]::Cert))
        # Describe the emitted certificate, not a second copy of its profile.
        $parsedSan = [Security.Cryptography.X509Certificates.X509SubjectAlternativeNameExtension]::new(
            $certificate.Extensions['2.5.29.17'].RawData, $false)
        $constraints = [Security.Cryptography.X509Certificates.X509BasicConstraintsExtension] $certificate.Extensions['2.5.29.19']
        $usage = [Security.Cryptography.X509Certificates.X509EnhancedKeyUsageExtension] $certificate.Extensions['2.5.29.37']
        $manifest = [ordered]@{
            FormatVersion = 1
            Purpose = 'GoDav local test TLS only; not trusted'
            Hostname = $certificate.GetNameInfo([Security.Cryptography.X509Certificates.X509NameType]::DnsName, $false)
            DnsNames = @($parsedSan.EnumerateDnsNames())
            IpAddresses = @($parsedSan.EnumerateIPAddresses() | ForEach-Object { $_.ToString() })
            Subject = $certificate.Subject
            Issuer = $certificate.Issuer
            CertificateAuthority = $constraints.CertificateAuthority
            ExtendedKeyUsage = @($usage.EnhancedKeyUsages | ForEach-Object { $_.Value })
            KeyAlgorithm = $certificate.PublicKey.Oid.FriendlyName
            KeySize = $rsa.KeySize
            SignatureAlgorithm = $certificate.SignatureAlgorithm.FriendlyName
            SignatureAlgorithmOid = $certificate.SignatureAlgorithm.Value
            NotBeforeUtc = $certificate.NotBefore.ToUniversalTime().ToString('o')
            NotAfterUtc = $certificate.NotAfter.ToUniversalTime().ToString('o')
            ThumbprintSha1 = $certificate.Thumbprint
            FingerprintSha256 = $certificate.GetCertHashString([Security.Cryptography.HashAlgorithmName]::SHA256)
            PowerShellExecutable = (Get-Process -Id $PID).Path
            PowerShellVersion = $PSVersionTable.PSVersion.ToString()
            DotNetVersion = [Environment]::Version.ToString()
            OwnerSid = $sid.Value
            Files = $paths
            TrustImported = $false
            TrustStore = $null
        }
        Write-NewFile $paths.Manifest ([Text.Encoding]::UTF8.GetBytes(($manifest | ConvertTo-Json -Depth 4) + "`n"))
        $manifest | ConvertTo-Json -Depth 4
    } finally {
        if ($null -ne $certificate) { $certificate.Dispose() }
        $rsa.Dispose()
    }
} catch {
    # Never echo provider exception text, which could contain private data.
    # Preserve partial output for exact-path review; a retry cannot replace it.
    [Console]::Error.WriteLine('TLS fixture generation failed. Verify the new absolute output path, its parent and permissions. Existing or partial output is never overwritten; review it before cleanup.')
    exit 1
}
