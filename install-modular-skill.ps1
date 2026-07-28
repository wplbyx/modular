[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$Name,

    [Alias("d")]
    [string]$Directory = (Join-Path (Join-Path $HOME ".claude") "skills"),

    [Alias("f")]
    [switch]$Force,

    [Alias("l")]
    [switch]$DevelopmentLink
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Fail {
    param([string]$Message)

    throw $Message
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

function Assert-ChildPath {
    param(
        [string]$Parent,
        [string]$Child
    )

    $parentFull = Normalize-PathString $Parent
    $childFull = Normalize-PathString $Child
    $prefix = $parentFull + [System.IO.Path]::DirectorySeparatorChar
    if (-not $childFull.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        Fail "install path escapes target parent: $childFull"
    }
}

function Get-Python {
    foreach ($candidate in @("python", "python3")) {
        $command = Get-Command $candidate -ErrorAction SilentlyContinue
        if ($null -ne $command) {
            return $command.Source
        }
    }
    Fail "python is required to run the modular skill self-check"
}

function Invoke-SelfCheck {
    param([string]$SkillRoot)

    $cli = Join-Path (Join-Path $SkillRoot "scripts") "modular.py"
    if (-not (Test-Path -LiteralPath $cli -PathType Leaf)) {
        return
    }
    $python = Get-Python
    & $python $cli self-check
    if ($LASTEXITCODE -ne 0) {
        Fail "post-install self-check failed for $SkillRoot"
    }
}

if ([string]::IsNullOrWhiteSpace($Directory)) {
    Fail "target parent directory cannot be empty"
}
if ([string]::IsNullOrWhiteSpace($Name)) {
    Fail "missing skill name"
}
Assert-SkillName $Name

$source = Join-Path (Join-Path $PSScriptRoot "agent") $Name
$skillFile = Join-Path $source "SKILL.md"
if (-not (Test-Path -LiteralPath $source -PathType Container)) {
    Fail "source skill directory not found: $source"
}
if (-not (Test-Path -LiteralPath $skillFile -PathType Leaf)) {
    Fail "source skill is missing SKILL.md: $skillFile"
}

$sourceFull = Normalize-PathString $source
$targetParent = Normalize-PathString $Directory
if (-not (Test-Path -LiteralPath $targetParent -PathType Container)) {
    New-Item -ItemType Directory -Path $targetParent -Force | Out-Null
}
$target = Normalize-PathString (Join-Path $targetParent $Name)
Assert-ChildPath $targetParent $target
if ([string]::Equals($sourceFull, $target, [System.StringComparison]::OrdinalIgnoreCase)) {
    Fail "source and target are the same path: $target"
}

$nonce = [Guid]::NewGuid().ToString("N")
$stage = Normalize-PathString (Join-Path $targetParent ".$Name.install-$nonce")
$backup = Normalize-PathString (Join-Path $targetParent ".$Name.backup-$nonce")
Assert-ChildPath $targetParent $stage
Assert-ChildPath $targetParent $backup
$backupCreated = $false
$targetInstalled = $false

try {
    if ($DevelopmentLink) {
        New-Item -ItemType SymbolicLink -Path $stage -Target $sourceFull | Out-Null
    } else {
        Copy-Item -LiteralPath $sourceFull -Destination $stage -Recurse -Force
    }
    Invoke-SelfCheck $stage

    $existing = Get-Item -LiteralPath $target -Force -ErrorAction SilentlyContinue
    if ($null -ne $existing) {
        if (-not $Force) {
            Fail "target already exists: $target`nUse -Force to replace it with rollback protection, or choose another parent with -Directory."
        }
        if (-not $existing.PSIsContainer -and (($existing.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -eq 0)) {
            Fail "target is a regular file and will not be replaced: $target"
        }
        Move-Item -LiteralPath $target -Destination $backup
        $backupCreated = $true
    }

    Move-Item -LiteralPath $stage -Destination $target
    $targetInstalled = $true
    Invoke-SelfCheck $target

    if ($backupCreated) {
        Remove-Item -LiteralPath $backup -Recurse -Force
        $backupCreated = $false
    }
} catch {
    if ($targetInstalled -and (Test-Path -LiteralPath $target)) {
        Remove-Item -LiteralPath $target -Recurse -Force
    }
    if ($backupCreated -and (Test-Path -LiteralPath $backup)) {
        Move-Item -LiteralPath $backup -Destination $target
        $backupCreated = $false
    }
    if (Test-Path -LiteralPath $stage) {
        Remove-Item -LiteralPath $stage -Recurse -Force
    }
    Write-Error $_
    exit 1
}

$mode = if ($DevelopmentLink) { "development link" } else { "copy" }
Write-Host "Installed $Name skill ($mode):"
if ($DevelopmentLink) {
    Write-Host "  $target -> $sourceFull"
} else {
    Write-Host "  $target"
}
Write-Host ""
Write-Host "Run this installer again with -Force after updating the repository."
