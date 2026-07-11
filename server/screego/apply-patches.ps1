param(
  [Parameter(Mandatory = $false)]
  [string]$SourceRoot = "tmp/screego-upstream"
)

$ErrorActionPreference = "Stop"
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "../..")
$source = Resolve-Path (Join-Path $repoRoot $SourceRoot)
$lockPath = Join-Path $PSScriptRoot "upstream.lock"
$lock = @{}

function Get-NormalizedTextSha256([string]$Path) {
  $text = [System.IO.File]::ReadAllText($Path).Replace("`r`n", "`n")
  $bytes = [System.Text.UTF8Encoding]::new($false).GetBytes($text)
  $sha = [System.Security.Cryptography.SHA256]::Create()
  try {
    return ([System.BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant()
  } finally {
    $sha.Dispose()
  }
}

Get-Content -LiteralPath $lockPath | ForEach-Object {
  if ($_ -match '^([^=]+)=(.*)$') {
    $lock[$matches[1]] = $matches[2]
  }
}

$checks = @(
  @{ Path = "ui/src/useRoom.ts"; Hash = $lock.useRoomSha256 },
  @{ Path = "go.mod"; Hash = $lock.goModSha256 }
)

foreach ($check in $checks) {
  $path = Join-Path $source $check.Path
  $actual = Get-NormalizedTextSha256 $path
  if ($actual -ne $check.Hash) {
    throw "Screego baseline mismatch for $($check.Path): expected $($check.Hash), got $actual"
  }
}

$patches = Get-ChildItem -LiteralPath (Join-Path $PSScriptRoot "patches") -Filter "*.patch" | Sort-Object Name
foreach ($patch in $patches) {
  & git -C $source apply --check --ignore-space-change --ignore-whitespace $patch.FullName
  if ($LASTEXITCODE -ne 0) {
    throw "Patch validation failed: $($patch.Name)"
  }
  & git -C $source apply --ignore-space-change --ignore-whitespace $patch.FullName
  if ($LASTEXITCODE -ne 0) {
    throw "Patch application failed: $($patch.Name)"
  }
}

Write-Host "Applied $($patches.Count) Screego patch(es) to $source"
