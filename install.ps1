<#
.SYNOPSIS
    Build and install Apple Music TUI (amtui) for the current user on Windows.
.DESCRIPTION
    Builds amtui from this source checkout with `go build` and installs it,
    plus the `applemusic` / `applemusic-tui` aliases, under -Prefix (default
    %LOCALAPPDATA%\Programs\amtui). Adds -Prefix to the user PATH unless
    -NoPath is given.

    Run it with:
        powershell -ExecutionPolicy Bypass -File .\install.ps1
.PARAMETER Prefix
    Install into DIR instead of %LOCALAPPDATA%\Programs\amtui.
.PARAMETER NoPath
    Do not modify the user PATH environment variable.
.PARAMETER Help
    Show usage and exit.
.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\install.ps1
.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\install.ps1 -Prefix D:\tools\amtui -NoPath
#>
param(
    [string]$Prefix,
    [switch]$NoPath,
    [switch]$Help
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Program = 'amtui'

function Write-Usage {
    @'
Usage: powershell -ExecutionPolicy Bypass -File .\install.ps1 [options]

Build and install Apple Music TUI for the current user.

Options:
  -Prefix DIR   Install into DIR instead of %LOCALAPPDATA%\Programs\amtui
  -NoPath       Do not modify the user PATH environment variable
  -Help         Show this help

Installed commands:
  amtui.exe
  applemusic.cmd
  applemusic-tui.cmd
'@ | Write-Output
}

if ($Help) {
    Write-Usage
    exit 0
}

function Fail {
    param([string]$Message)
    [Console]::Error.WriteLine("install.ps1: $Message")
    exit 1
}

# --- Resolve prefix -----------------------------------------------------

if (-not $Prefix) {
    if (-not $env:LOCALAPPDATA) {
        Fail 'LOCALAPPDATA is not set; use -Prefix DIR to choose an install directory'
    }
    $Prefix = Join-Path $env:LOCALAPPDATA 'Programs\amtui'
}
if (-not $Prefix.Trim()) {
    Fail '-Prefix requires a nonempty directory'
}
# Absolute before the Set-Location below: a relative -Prefix means the caller's
# directory, and a relative entry on PATH would follow every cd.
$Prefix = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Prefix)

# --- Run from the checkout ----------------------------------------------

Set-Location -LiteralPath $PSScriptRoot
if (-not (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'go.mod') -PathType Leaf)) {
    Fail 'run this script from the applemusic-tui source checkout'
}

# --- Require Go -----------------------------------------------------------

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Fail 'Go is required: https://go.dev/dl/'
}

$GoVersion = $null
try {
    $GoVersion = (& go version) | Out-String
    if ($LASTEXITCODE -ne 0 -or -not $GoVersion.Trim()) {
        Fail 'go version failed; check the Go installation'
    }
} catch {
    Fail 'go version failed; check the Go installation'
}
$GoVersion = $GoVersion.Trim()
Write-Output "Building $Program with $GoVersion..."

# --- Version label --------------------------------------------------------

$Version = 'dev'
if (Get-Command git -ErrorAction SilentlyContinue) {
    try {
        $described = (& git describe --tags --always 2>$null | Out-String).Trim()
        if ($LASTEXITCODE -eq 0 -and $described -match '^[A-Za-z0-9._-]+$') {
            $Version = $described
        }
    } catch {
        # ponytail: git describe is best-effort; any failure keeps $Version = 'dev'.
        Write-Verbose "git describe failed: $_"
    }
}

# --- Build into a fresh temp dir -----------------------------------------

$BuildDir = Join-Path ([IO.Path]::GetTempPath()) ('amtui-install-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $BuildDir -Force | Out-Null
$ExePath = Join-Path $BuildDir 'amtui.exe'

try {
    $PrevCgoEnabled = $env:CGO_ENABLED
    $HadCgoEnabled = Test-Path Env:\CGO_ENABLED
    try {
        $env:CGO_ENABLED = '0'
        & go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $ExePath .
        if ($LASTEXITCODE -ne 0) {
            Fail 'go build failed'
        }
    } finally {
        if ($HadCgoEnabled) {
            $env:CGO_ENABLED = $PrevCgoEnabled
        } else {
            Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue
        }
    }

    # ponytail: a read-only GOMODCACHE (install.sh's temp-cache fallback) is
    # skipped here - an edge case on Windows; add if it bites someone.

    # --- Install ------------------------------------------------------------

    if ((Test-Path -LiteralPath $Prefix) -and -not (Test-Path -LiteralPath $Prefix -PathType Container)) {
        Fail "$Prefix already exists and is not a directory"
    }
    New-Item -ItemType Directory -Path $Prefix -Force | Out-Null

    $DestExe = Join-Path $Prefix 'amtui.exe'
    $StagedExe = Join-Path $Prefix ('.amtui-install-' + [guid]::NewGuid().ToString('N') + '.tmp')
    try {
        Copy-Item -LiteralPath $ExePath -Destination $StagedExe -Force
        try {
            Move-Item -LiteralPath $StagedExe -Destination $DestExe -Force
        } catch {
            Fail "cannot replace $DestExe - close amtui and re-run"
        }
    } finally {
        if (Test-Path -LiteralPath $StagedExe) {
            Remove-Item -LiteralPath $StagedExe -Force -ErrorAction SilentlyContinue
        }
    }

    foreach ($Alias in @('applemusic', 'applemusic-tui')) {
        $ShimPath = Join-Path $Prefix "$Alias.cmd"
        if ((Test-Path -LiteralPath $ShimPath) -and -not (Test-Path -LiteralPath $ShimPath -PathType Leaf)) {
            Fail "$ShimPath already exists and is not a regular file"
        }
        [IO.File]::WriteAllText($ShimPath, "@`"%~dp0amtui.exe`" %*`r`n", [Text.Encoding]::ASCII)
    }
} finally {
    if (Test-Path -LiteralPath $BuildDir) {
        Remove-Item -LiteralPath $BuildDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# --- PATH -----------------------------------------------------------------

$PathAdded = $false
if (-not $NoPath) {
    $NormalizedPrefix = $Prefix.TrimEnd('\')
    $RawUserPath = ''
    if (Test-Path 'HKCU:\Environment') {
        $EnvKey = Get-Item -LiteralPath 'HKCU:\Environment'
        $Existing = $EnvKey.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
        if ($null -ne $Existing) {
            $RawUserPath = $Existing
        }
    }
    $Entries = @($RawUserPath -split ';' | Where-Object { $_ -ne '' })
    $AlreadyOnPath = $false
    foreach ($Entry in $Entries) {
        if ($Entry.TrimEnd('\').ToLowerInvariant() -eq $NormalizedPrefix.ToLowerInvariant()) {
            $AlreadyOnPath = $true
            break
        }
    }
    if (-not $AlreadyOnPath) {
        $TrimmedUserPath = $RawUserPath.TrimEnd(';')
        $NewUserPath = if ($TrimmedUserPath) { "$TrimmedUserPath;$Prefix" } else { $Prefix }
        if (-not (Test-Path 'HKCU:\Environment')) {
            New-Item -Path 'HKCU:\Environment' -Force | Out-Null
        }
        New-ItemProperty -Path 'HKCU:\Environment' -Name 'Path' -Value $NewUserPath -PropertyType ExpandString -Force | Out-Null
        $PathAdded = $true
    }
    $env:Path = "$Prefix;$env:Path"
}

# --- Browser check ----------------------------------------------------------

function Find-Browser {
    $Suffixes = @('Google\Chrome\Application\chrome.exe', 'Microsoft\Edge\Application\msedge.exe')
    $Roots = @($env:ProgramFiles, ${env:ProgramFiles(x86)}, $env:LOCALAPPDATA) | Where-Object { $_ }
    foreach ($Suffix in $Suffixes) {
        foreach ($Root in $Roots) {
            $Candidate = Join-Path $Root $Suffix
            if (Test-Path -LiteralPath $Candidate -PathType Leaf) {
                return $Candidate
            }
        }
    }
    return $null
}

if (-not (Find-Browser)) {
    Write-Output ''
    Write-Output 'Google Chrome or Microsoft Edge is required to run amtui.'
    Write-Output '(AMTUI_CHROME can point at any Chromium-based browser instead.)'
    $WingetCmd = Get-Command winget -ErrorAction SilentlyContinue
    $Interactive = [Environment]::UserInteractive -and -not [Console]::IsInputRedirected
    if ($WingetCmd -and $Interactive) {
        $Reply = Read-Host 'Install Google Chrome now with winget? [y/N]'
        if ($Reply -match '^(?i:y|yes)$') {
            & winget install -e --id Google.Chrome
            if ($LASTEXITCODE -ne 0) {
                Fail 'winget install of Google Chrome failed'
            }
        }
    } else {
        Write-Output 'Install Chrome or Edge manually: https://www.google.com/chrome/'
    }
}

# --- Summary ----------------------------------------------------------------

Write-Output ''
Write-Output 'Installed:'
Write-Output "  $DestExe"
Write-Output "  $(Join-Path $Prefix 'applemusic.cmd')"
Write-Output "  $(Join-Path $Prefix 'applemusic-tui.cmd')"

if (-not $NoPath) {
    if ($PathAdded) {
        Write-Output ''
        Write-Output "PATH updated for the current user: $Prefix"
    } else {
        Write-Output ''
        Write-Output "$Prefix is already on the user PATH."
    }
    Write-Output 'Open a new terminal (it is already available in this session).'
}

Write-Output ''
Write-Output 'Use Windows Terminal (the legacy console renders the TUI poorly).'
Write-Output 'On Windows the visualizer runs in its labeled simulated mode (no system-audio capture backend yet).'

Write-Output ''
Write-Output "Run: $Program"
