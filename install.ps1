#Requires -Version 5.1
<#
.SYNOPSIS
    Install the Flagsmith CLI.
.DESCRIPTION
    irm https://get.flagsmith.com/install.ps1 | iex

    `iex` cannot pass arguments, so either set the environment variables below
    first, or invoke a script block:

    &([scriptblock]::Create((irm https://get.flagsmith.com/install.ps1))) -Version <tag>
.PARAMETER Version
    Version to install. Defaults to $env:FLAGSMITH_CLI_VERSION, else the version
    this script shipped with.
.PARAMETER BinDir
    Where to install. Defaults to $env:FLAGSMITH_INSTALL_DIR, else ~\.local\bin.
.PARAMETER NoModifyPath
    Leave the user PATH alone. Also $env:FLAGSMITH_NO_MODIFY_PATH.
.PARAMETER DryRun
    Report what would be installed, then stop.
#>
param(
    [string]$Version,
    [string]$BinDir,
    [switch]$NoModifyPath,
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'
# Invoke-WebRequest spends most of its time drawing the progress bar.
$ProgressPreference = 'SilentlyContinue'

$DefaultVersion = 'v2.0.0-beta.3' # x-release-please-version

$Repo = 'Flagsmith/flagsmith-cli'
$ExeName = 'flagsmith.exe'
$BaseUrl = if ($env:FLAGSMITH_CLI_BASE_URL) {
    $env:FLAGSMITH_CLI_BASE_URL
} else {
    "https://github.com/$Repo/releases/download"
}

function Get-TargetArch {
    $arch = try {
        [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    } catch {
        # PROCESSOR_ARCHITECTURE from the registry, not the environment: a 32-bit
        # PowerShell under WOW64 reports x86 for its own process.
        (Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment').PROCESSOR_ARCHITECTURE
    }
    switch -Regex ($arch) {
        '^(X64|AMD64)$' { return 'amd64' }
        '^ARM64$' { return 'arm64' }
        default { throw "unsupported architecture '$arch'" }
    }
}

function Get-Checksum {
    param([string]$SumsFile, [string]$Name)

    $pattern = '\s\*?' + [regex]::Escape($Name) + '$'
    $lines = @(Get-Content -LiteralPath $SumsFile | Where-Object { $_ -match $pattern })
    if ($lines.Count -ne 1) {
        throw "expected exactly one checksum for $Name in checksums.txt, found $($lines.Count)"
    }
    return ($lines[0] -split '\s+')[0]
}

# Add-UserPath adds to the user PATH through the registry rather than
# [Environment]::SetEnvironmentVariable, which rewrites REG_EXPAND_SZ as REG_SZ
# and so breaks any %VAR% already in PATH.
function Add-UserPath {
    param([string]$Dir)

    $key = 'registry::HKEY_CURRENT_USER\Environment'
    $current = (Get-Item -LiteralPath $key).GetValue('Path', '', 'DoNotExpandEnvironmentNames') -split ';' -ne ''
    if ($Dir -in $current) { return $false }

    Set-ItemProperty -LiteralPath $key -Name Path -Type ExpandString -Value ((, $Dir + $current) -join ';')
    # Tell running shells and Explorer to reread the environment.
    $dummy = 'flagsmith-' + [guid]::NewGuid().ToString()
    [Environment]::SetEnvironmentVariable($dummy, 'x', 'User')
    [Environment]::SetEnvironmentVariable($dummy, [NullString]::Value, 'User')
    return $true
}

# Write-ConflictWarning reports other flagsmith commands on PATH that may shadow
# (or be shadowed by) the one just installed.
function Write-ConflictWarning {
    param([string]$Target)

    $resolvedTarget = [System.IO.Path]::GetFullPath($Target)
    $others = @(Get-Command -Name 'flagsmith' -All -ErrorAction SilentlyContinue |
            Where-Object { $_.Source -and ([System.IO.Path]::GetFullPath($_.Source) -ne $resolvedTarget) })
    foreach ($other in $others) {
        Write-Output "warning: another 'flagsmith' is on your PATH at $($other.Source)"
    }
    if ($others.Count -gt 0) {
        Write-Output "It may shadow $Target - uninstall it first ('npm uninstall -g flagsmith-cli' removes the old npm CLI), then open a new terminal."
    }
}

# Add-CiPath makes the CLI available to later steps of a GitHub Actions job.
function Add-CiPath {
    param([string]$Dir)

    if ($env:GITHUB_PATH) {
        Write-Output $Dir | Out-File -FilePath $env:GITHUB_PATH -Encoding utf8 -Append
    }
}

if (-not $Version) {
    $Version = if ($env:FLAGSMITH_CLI_VERSION) { $env:FLAGSMITH_CLI_VERSION } else { $DefaultVersion }
}
if ($Version -notlike 'v*') { $Version = "v$Version" }

if (-not $BinDir) {
    $BinDir = if ($env:FLAGSMITH_INSTALL_DIR) {
        $env:FLAGSMITH_INSTALL_DIR
    } else {
        Join-Path $env:USERPROFILE '.local\bin'
    }
}
if ($env:FLAGSMITH_NO_MODIFY_PATH -eq '1') { $NoModifyPath = $true }

$arch = Get-TargetArch
$archive = "flagsmith_$($Version.TrimStart('v'))_windows_$arch.zip"
$archiveUrl = "$BaseUrl/$Version/$archive"
$sumsUrl = "$BaseUrl/$Version/checksums.txt"

if ($DryRun) {
    Write-Output "would install flagsmith $Version (windows/$arch) to $BinDir"
    Write-Output "  archive:   $archiveUrl"
    Write-Output "  checksums: $sumsUrl"
    return
}

# PowerShell 5.1 still defaults to TLS 1.0, which github.com refuses.
if ([Net.ServicePointManager]::SecurityProtocol -notmatch 'Tls12') {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
}

$tmp = New-Item -ItemType Directory -Path (Join-Path ([System.IO.Path]::GetTempPath()) ([guid]::NewGuid()))
try {
    Write-Output "downloading flagsmith $Version (windows/$arch)"
    $zip = Join-Path $tmp $archive
    $sums = Join-Path $tmp 'checksums.txt'
    try {
        Invoke-WebRequest -Uri $archiveUrl -OutFile $zip -UseBasicParsing
    } catch {
        throw "cannot download $archiveUrl`nIf $Version was released moments ago its archives may still be uploading - retry shortly, or choose a version with -Version."
    }
    Invoke-WebRequest -Uri $sumsUrl -OutFile $sums -UseBasicParsing

    $expected = Get-Checksum -SumsFile $sums -Name $archive
    $actual = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash
    if ($actual -ne $expected.ToUpperInvariant()) {
        throw "checksum mismatch for ${archive}: expected $expected, got $actual"
    }

    Expand-Archive -LiteralPath $zip -DestinationPath $tmp -Force
    New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
    $verb, $prep = if (Test-Path -LiteralPath (Join-Path $BinDir $ExeName)) { 'updated', 'at' } else { 'installed', 'to' }
    Move-Item -Force -LiteralPath (Join-Path $tmp $ExeName) -Destination (Join-Path $BinDir $ExeName)
} finally {
    Remove-Item -Recurse -Force -LiteralPath $tmp
}

$exe = Join-Path $BinDir $ExeName
$installed = & $exe --version
if ($LASTEXITCODE -ne 0) { throw "$exe was installed but will not run" }
Write-Output "$verb $installed $prep $exe"
Write-ConflictWarning -Target $exe

$pathAdded = $false
if (-not $NoModifyPath) {
    $pathAdded = Add-UserPath -Dir $BinDir
    Add-CiPath -Dir $BinDir
}

Write-Output ''
if ($pathAdded) {
    Write-Output "Open a new terminal, then run 'flagsmith init' to get started."
} else {
    Write-Output "Run 'flagsmith init' to get started."
}
