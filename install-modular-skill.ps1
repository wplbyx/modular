[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$Name,

    [Alias("d")]
    [string]$Directory = (Join-Path (Join-Path $HOME ".claude") "skills"),

    [Alias("f")]
    [switch]$Force
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Fail {
    param([string]$Message)

    Write-Error $Message
    exit 1
}

function Assert-SkillName {
    param([string]$SkillName)

    if ([string]::IsNullOrWhiteSpace($SkillName) -or $SkillName -eq "." -or $SkillName -eq ".." -or $SkillName.Contains("/") -or $SkillName.Contains("\")) {
        Fail "invalid skill name: $SkillName"
    }
}

function Normalize-PathString {
    param([string]$Path)

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $rootPath = [System.IO.Path]::GetPathRoot($fullPath)
    if ([string]::Equals($fullPath, $rootPath, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $fullPath
    }

    return $fullPath.TrimEnd('\', '/')
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

function Get-LinkTarget {
    param([object]$Item)

    $targetProperty = $Item.PSObject.Properties["Target"]
    if ($null -eq $targetProperty -or $null -eq $targetProperty.Value) {
        return ""
    }

    if ($targetProperty.Value -is [array]) {
        if ($targetProperty.Value.Count -eq 0) {
            return ""
        }

        return [string]$targetProperty.Value[0]
    }

    return [string]$targetProperty.Value
}

function Get-LinkType {
    param([object]$Item)

    $linkTypeProperty = $Item.PSObject.Properties["LinkType"]
    if ($null -eq $linkTypeProperty -or $null -eq $linkTypeProperty.Value) {
        return ""
    }

    return [string]$linkTypeProperty.Value
}

if ([string]::IsNullOrWhiteSpace($Directory)) {
    Fail "target parent directory cannot be empty"
}

if ([string]::IsNullOrWhiteSpace($Name)) {
    Fail "missing skill name"
}

Assert-SkillName $Name

$Source = Join-Path (Join-Path $PSScriptRoot "agent") $Name
$SkillFile = Join-Path $Source "SKILL.md"

if (-not (Test-Path -LiteralPath $Source -PathType Container)) {
    Fail "source skill directory not found: $Source"
}

if (-not (Test-Path -LiteralPath $SkillFile -PathType Leaf)) {
    Fail "source skill is missing SKILL.md: $SkillFile"
}

$SourceReal = Normalize-PathString $Source
$TargetParent = Normalize-PathString $Directory

if (-not (Test-Path -LiteralPath $TargetParent -PathType Container)) {
    New-Item -ItemType Directory -Path $TargetParent -Force | Out-Null
}

$TargetFull = Normalize-PathString (Join-Path $TargetParent $Name)
$Existing = Get-Item -LiteralPath $TargetFull -Force -ErrorAction SilentlyContinue
if ($null -ne $Existing) {
    $isReparsePoint = (($Existing.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0)
    $linkType = Get-LinkType $Existing
    $isSymbolicLink = $isReparsePoint -and [string]::Equals($linkType, "SymbolicLink", [System.StringComparison]::OrdinalIgnoreCase)
    $linkTarget = Get-LinkTarget $Existing

    if ($isSymbolicLink -and -not [string]::IsNullOrWhiteSpace($linkTarget)) {
        if (-not [System.IO.Path]::IsPathRooted($linkTarget)) {
            $linkTarget = Join-Path (Split-Path -Parent $TargetFull) $linkTarget
        }

        if ((Test-Path -LiteralPath $linkTarget -PathType Container) -and (Same-Path $linkTarget $SourceReal)) {
            Write-Host "$Name skill is already installed:"
            Write-Host "  $TargetFull -> $SourceReal"
            exit 0
        }
    }

    if ($isSymbolicLink) {
        if ($Force) {
            Remove-Item -LiteralPath $TargetFull -Force
        } else {
            Fail "target already exists and is not the expected symlink:`n  $TargetFull`n`nUse -f to replace an existing symlink, or pass a different parent directory with -d."
        }
    } else {
        Fail "target already exists and is not a symlink:`n  $TargetFull`n`nRefusing to remove a regular file or directory. Remove it manually or pass a different parent directory with -d."
    }
}

try {
    New-Item -ItemType SymbolicLink -Path $TargetFull -Target $SourceReal | Out-Null
} catch {
    Fail "failed to create symbolic link: $($_.Exception.Message)`nRun PowerShell as Administrator and try again."
}

Write-Host "Installed $Name skill:"
Write-Host "  $TargetFull -> $SourceReal"
Write-Host ""
Write-Host "To update it later, run git pull in this repository."
