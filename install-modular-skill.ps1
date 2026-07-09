param(
    [string]$Target = (Join-Path $HOME ".claude\skills\modular")
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Fail {
    param([string]$Message)

    Write-Error $Message
    exit 1
}

function Normalize-PathString {
    param([string]$Path)

    return ([System.IO.Path]::GetFullPath($Path).TrimEnd('\', '/'))
}

function Same-Path {
    param(
        [string]$Left,
        [string]$Right
    )

    $leftNormalized = Normalize-PathString $Left
    $rightNormalized = Normalize-PathString $Right
    return [string]::Equals($leftNormalized, $rightNormalized, [System.StringComparison]::OrdinalIgnoreCase)
}

$Source = Join-Path $PSScriptRoot "agent\modular"
$SkillFile = Join-Path $Source "SKILL.md"

if (-not (Test-Path -LiteralPath $Source -PathType Container)) {
    Fail "source skill directory not found: $Source"
}

if (-not (Test-Path -LiteralPath $SkillFile -PathType Leaf)) {
    Fail "source skill is missing SKILL.md: $SkillFile"
}

$SourceReal = Normalize-PathString $Source
$TargetFull = Normalize-PathString $Target
$TargetParent = Split-Path -Parent $TargetFull

if (-not (Test-Path -LiteralPath $TargetParent -PathType Container)) {
    New-Item -ItemType Directory -Path $TargetParent -Force | Out-Null
}

$Existing = Get-Item -LiteralPath $TargetFull -Force -ErrorAction SilentlyContinue
if ($null -ne $Existing) {
    $isReparsePoint = (($Existing.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0)
    $linkTarget = $Existing.Target

    if ($isReparsePoint -and -not [string]::IsNullOrWhiteSpace($linkTarget)) {
        if (-not [System.IO.Path]::IsPathRooted($linkTarget)) {
            $linkTarget = Join-Path (Split-Path -Parent $TargetFull) $linkTarget
        }

        if ((Test-Path -LiteralPath $linkTarget -PathType Container) -and (Same-Path $linkTarget $SourceReal)) {
            Write-Host "modular skill is already installed:"
            Write-Host "  $TargetFull -> $SourceReal"
            exit 0
        }
    }

    Fail "target already exists and is not the expected symlink:`n  $TargetFull`n`nRemove it manually or pass a different target path."
}

try {
    New-Item -ItemType SymbolicLink -Path $TargetFull -Target $SourceReal | Out-Null
} catch {
    Fail "failed to create symbolic link: $($_.Exception.Message)`nRun PowerShell as Administrator and try again."
}

Write-Host "Installed modular skill:"
Write-Host "  $TargetFull -> $SourceReal"
Write-Host ""
Write-Host "To update it later, run git pull in this repository."
