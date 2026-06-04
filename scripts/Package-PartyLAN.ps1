param(
  [ValidateSet('loopback', 'lan', 'networked', 'tailscale-system')]
  [string]$Mode = 'tailscale-system',

  [switch]$UseTsnet,
  [switch]$UseNativeMedia = $true,
  [switch]$UseNativeCaptureExperiment = $true,
  [switch]$UseNativeEncoderExperiment = $true,
  [int]$DERPRegionID = 0,
  [string]$DERPRegionCode = '',
  [string]$DERPHostName = '',
  [int]$DERPPort = 0,
  [switch]$AllowOverwrite
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

$versionFile = Join-Path $repoRoot 'pkg\version\version.go'
$versionText = Get-Content -Raw $versionFile
if ($versionText -match 'Version\s*=\s*"([^"]+)"') {
  $version = $Matches[1]
} else {
  throw "Unable to parse version from $versionFile"
}

$releaseRoot = Join-Path $repoRoot 'release'
$bundleDir = Join-Path $releaseRoot "PartyLAN-v$version-win64"
$zipPath = Join-Path $releaseRoot "PartyLAN-v$version-win64.zip"
$installerReleasePath = Join-Path $releaseRoot "PartyLAN-v$version-win64-installer.exe"
$stageRoot = Join-Path $releaseRoot '.stage'
$buildBin = Join-Path $repoRoot 'build\bin'
$useStaging = $false
$releaseInfoPath = Join-Path $releaseRoot "PartyLAN-v$version-win64-RELEASE-INFO.txt"
$sumsReleasePath = Join-Path $releaseRoot "PartyLAN-v$version-win64-SHA256SUMS.txt"
$opusRuntimePath = Join-Path $repoRoot 'third_party\opus\win64\opus.dll'
$opusLicensePath = Join-Path $repoRoot 'third_party\opus\win64\COPYING.txt'
$opusSourcePath = Join-Path $repoRoot 'third_party\opus\win64\SOURCE.txt'

$nsisAvailable = [bool](Get-Command makensis.exe -ErrorAction SilentlyContinue)
if (-not $nsisAvailable) {
  $nsisAvailable = [bool](Get-Command makensis -ErrorAction SilentlyContinue)
}

if (-not $AllowOverwrite) {
  $existingReleaseArtifacts = @(@(
    $bundleDir,
    $zipPath,
    $installerReleasePath,
    $releaseInfoPath,
    $sumsReleasePath
  ) | Where-Object { Test-Path $_ })

  if ($existingReleaseArtifacts.Count -gt 0) {
    $existing = $existingReleaseArtifacts -join ', '
    throw "Release v$version already exists ($existing). Bump pkg/version/version.go before packaging, or pass -AllowOverwrite for a deliberate rebuild."
  }
}

if (Test-Path $bundleDir) {
  try {
    Remove-Item -Recurse -Force $bundleDir -ErrorAction Stop
  } catch {
    Write-Warning "Could not clear previous bundle folder, using a staging directory instead: $($_.Exception.Message)"
    $useStaging = $true
  }
}
if (Test-Path $zipPath) {
  Remove-Item -Force $zipPath
}
if (Test-Path $installerReleasePath) {
  Remove-Item -Force $installerReleasePath
}
if (Test-Path $releaseInfoPath) {
  Remove-Item -Force $releaseInfoPath
}
Get-ChildItem -Path $buildBin -Filter '*installer.exe' -ErrorAction SilentlyContinue | Remove-Item -Force -ErrorAction SilentlyContinue

& $PSScriptRoot\Start-PartyLAN.ps1 `
  -Action build `
  -Mode $Mode `
  -UseTsnet:$UseTsnet `
  -UseNativeMedia:$UseNativeMedia `
  -UseNativeCaptureExperiment:$UseNativeCaptureExperiment `
  -UseNativeEncoderExperiment:$UseNativeEncoderExperiment `
  -DERPRegionID $DERPRegionID `
  -DERPRegionCode $DERPRegionCode `
  -DERPHostName $DERPHostName `
  -DERPPort $DERPPort `
  -Installer:$nsisAvailable

$bundleOutputDir = if ($useStaging -or -not (Test-Path $bundleDir)) {
  $stagingDir = Join-Path $stageRoot ("PartyLAN-v{0}-win64-{1}" -f $version, ([guid]::NewGuid().ToString('N')))
  New-Item -ItemType Directory -Path $stagingDir -Force | Out-Null
  $stagingDir
} else {
  $bundleDir
}

New-Item -ItemType Directory -Path $bundleOutputDir -Force | Out-Null
Copy-Item -Force (Join-Path $repoRoot 'build\bin\PartyLAN.exe') $bundleOutputDir
Copy-Item -Force (Join-Path $repoRoot 'README.md') $bundleOutputDir
Copy-Item -Force (Join-Path $repoRoot 'scripts\Start-PartyLAN.md') $bundleOutputDir
Copy-Item -Force (Join-Path $repoRoot 'scripts\Start-PartyLAN-DERP.ps1') $bundleOutputDir
Copy-Item -Force (Join-Path $repoRoot 'scripts\Start-PartyLAN-SmokeTest.ps1') $bundleOutputDir

$opusBundled = $false
if (Test-Path $opusRuntimePath) {
  Copy-Item -Force $opusRuntimePath (Join-Path $bundleOutputDir 'opus.dll')
  if (Test-Path $opusLicensePath) {
    Copy-Item -Force $opusLicensePath (Join-Path $bundleOutputDir 'OPUS-COPYING.txt')
  }
  if (Test-Path $opusSourcePath) {
    Copy-Item -Force $opusSourcePath (Join-Path $bundleOutputDir 'OPUS-SOURCE.txt')
  }
  $opusBundled = $true
} else {
  Write-Warning "Opus runtime not found at $opusRuntimePath; package will keep shared app audio optional/video-only."
}

$exeHash = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $bundleOutputDir 'PartyLAN.exe')).Hash
$derpScriptHash = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $bundleOutputDir 'Start-PartyLAN-DERP.ps1')).Hash
$smokeScriptHash = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $bundleOutputDir 'Start-PartyLAN-SmokeTest.ps1')).Hash
$opusHash = if ($opusBundled) { (Get-FileHash -Algorithm SHA256 -Path (Join-Path $bundleOutputDir 'opus.dll')).Hash } else { $null }
$opusInfo = if ($opusBundled) {
  "Bundled: opus.dll (libopus 1.6.1)"
} else {
  "Not bundled: shared app audio requires opus.dll beside PartyLAN.exe or on PATH."
}

@"
PartyLAN Release Information
Version: $version
BuiltAt: $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss zzz')
DefaultMode: $Mode

Primary launchers:
- .\PartyLAN.exe
- .\Start-PartyLAN-DERP.ps1
- .\Start-PartyLAN-SmokeTest.ps1

Custom DERP defaults:
- RegionID: 901
- RegionCode: cn1
- HostName: 103.236.92.32
- DERPPort: 443

Opus runtime:
- $opusInfo

SHA256:
- PartyLAN.exe: $exeHash
- Start-PartyLAN-DERP.ps1: $derpScriptHash
- Start-PartyLAN-SmokeTest.ps1: $smokeScriptHash
$(
if ($opusHash) {
"- opus.dll: $opusHash"
}
)
"@ | Set-Content -Path (Join-Path $bundleOutputDir 'RELEASE-INFO.txt') -Encoding ASCII

if ($nsisAvailable) {
  $installer = Get-ChildItem -Path $buildBin -Filter '*installer.exe' | Select-Object -First 1
  if ($installer) {
    Copy-Item -Force $installer.FullName (Join-Path $bundleOutputDir 'PartyLAN-installer.exe')
    Copy-Item -Force $installer.FullName $installerReleasePath
  }
}

@(
  "$exeHash  PartyLAN.exe"
  "$derpScriptHash  Start-PartyLAN-DERP.ps1"
  "$smokeScriptHash  Start-PartyLAN-SmokeTest.ps1"
  if ($opusBundled) {
    Get-FileHash -Algorithm SHA256 -Path (Join-Path $bundleOutputDir 'opus.dll') |
      ForEach-Object { "$($_.Hash)  opus.dll" }
    if (Test-Path (Join-Path $bundleOutputDir 'OPUS-COPYING.txt')) {
      Get-FileHash -Algorithm SHA256 -Path (Join-Path $bundleOutputDir 'OPUS-COPYING.txt') |
        ForEach-Object { "$($_.Hash)  OPUS-COPYING.txt" }
    }
    if (Test-Path (Join-Path $bundleOutputDir 'OPUS-SOURCE.txt')) {
      Get-FileHash -Algorithm SHA256 -Path (Join-Path $bundleOutputDir 'OPUS-SOURCE.txt') |
        ForEach-Object { "$($_.Hash)  OPUS-SOURCE.txt" }
    }
  }
  Get-FileHash -Algorithm SHA256 -Path (Join-Path $bundleOutputDir 'RELEASE-INFO.txt') |
    ForEach-Object { "$($_.Hash)  RELEASE-INFO.txt" }
  if (Test-Path (Join-Path $bundleOutputDir 'PartyLAN-installer.exe')) {
    Get-FileHash -Algorithm SHA256 -Path (Join-Path $bundleOutputDir 'PartyLAN-installer.exe') |
      ForEach-Object { "$($_.Hash)  PartyLAN-installer.exe" }
  }
) | Where-Object { $_ } | Set-Content -Path (Join-Path $bundleOutputDir 'SHA256SUMS.txt') -Encoding ASCII

Copy-Item -Force (Join-Path $bundleOutputDir 'SHA256SUMS.txt') $sumsReleasePath
Copy-Item -Force (Join-Path $bundleOutputDir 'RELEASE-INFO.txt') $releaseInfoPath

Compress-Archive -Path (Join-Path $bundleOutputDir '*') -DestinationPath $zipPath -Force

if ($bundleOutputDir -ne $bundleDir) {
  Remove-Item -Recurse -Force $bundleOutputDir -ErrorAction SilentlyContinue
}

Write-Host "Release bundle: $zipPath"
