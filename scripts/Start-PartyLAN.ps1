param(
  [ValidateSet('dev', 'build')]
  [string]$Action = 'dev',

  [ValidateSet('gamecast-client', 'loopback', 'lan', 'networked', 'tailscale-system')]
  [string]$Mode = 'gamecast-client',

  [ValidateSet('gamecast-client', 'loopback', 'lan', 'networked', 'tailscale-system')]
  [string]$DefaultMode = 'gamecast-client',

  [string]$AuthKey = $env:PARTYLAN_AUTH_KEY,

  [int]$DERPRegionID = 0,
  [string]$DERPRegionCode = '',
  [string]$DERPHostName = '',
  [int]$DERPPort = 0,
  [string]$TSControlURL = $env:PARTYLAN_TS_CONTROL_URL,
  [string]$ICEServers = $env:PARTYLAN_ICE_SERVERS,
  [string]$TURNUsername = $env:PARTYLAN_TURN_USERNAME,
  [string]$TURNCredential = $env:PARTYLAN_TURN_CREDENTIAL,

  [switch]$UseTsnet,
  [switch]$UseNativeMedia,
  [switch]$UseNativeCaptureExperiment,
  [switch]$UseNativeEncoderExperiment,
  [switch]$Installer
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

function Resolve-NativeCgoCompiler {
  $candidateCommands = @()
  if ($env:CC) {
    $candidateCommands += $env:CC
  }

  $userCc = [Environment]::GetEnvironmentVariable('CC', 'User')
  if ($userCc) {
    $candidateCommands += $userCc
  }

  $machineCc = [Environment]::GetEnvironmentVariable('CC', 'Machine')
  if ($machineCc) {
    $candidateCommands += $machineCc
  }

  $candidateCommands += @(
    'gcc.exe',
    'x86_64-w64-mingw32-gcc.exe'
  )

  foreach ($candidate in ($candidateCommands | Select-Object -Unique)) {
    if (-not $candidate) {
      continue
    }
    $ccCommand = Get-Command $candidate -ErrorAction SilentlyContinue
    if ($ccCommand) {
      return $ccCommand.Source
    }
  }

  $candidatePaths = @(
    'C:\Tools\mingw64\bin\gcc.exe',
    'C:\Program Files\mingw-w64\bin\gcc.exe',
    'C:\mingw64\bin\gcc.exe'
  )

  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $machinePath = [Environment]::GetEnvironmentVariable('Path', 'Machine')
  foreach ($pathValue in @($userPath, $machinePath)) {
    if (-not $pathValue) {
      continue
    }
    foreach ($entry in ($pathValue -split ';')) {
      if (-not $entry) {
        continue
      }
      $candidatePaths += (Join-Path $entry 'gcc.exe')
      $candidatePaths += (Join-Path $entry 'x86_64-w64-mingw32-gcc.exe')
    }
  }

  foreach ($candidatePath in ($candidatePaths | Select-Object -Unique)) {
    if (Test-Path $candidatePath) {
      return $candidatePath
    }
  }

  return $null
}

function Assert-NativeBuildMetadata {
  param(
    [Parameter(Mandatory = $true)]
    [string]$BinaryPath
  )

  if (-not (Test-Path $BinaryPath)) {
    throw "Native media build check failed: binary not found at $BinaryPath"
  }

  $meta = & go version -m $BinaryPath
  if ($LASTEXITCODE -ne 0) {
    throw "Native media build check failed: unable to read build metadata from $BinaryPath"
  }

  $cgoLine = $meta | Where-Object { $_ -match 'CGO_ENABLED=' } | Select-Object -First 1
  $tagsLine = $meta | Where-Object { $_ -match 'partylan_native_media' } | Select-Object -First 1

  if (-not $cgoLine -or $cgoLine -notmatch 'CGO_ENABLED=1') {
    throw "Native media build check failed: CGO_ENABLED is not 1 in output binary."
  }
  if (-not $tagsLine -or $tagsLine -notmatch 'partylan_native_media') {
    throw "Native media build check failed: partylan_native_media tag missing in output binary."
  }
}

$opusRuntimeDir = Join-Path $repoRoot 'third_party\opus\win64'
$opusRuntimePath = Join-Path $opusRuntimeDir 'opus.dll'

$enableTsnet = $UseTsnet -or ($Mode -eq 'networked')

if ($enableTsnet) {
  $Mode = 'networked'
}

$runtimeDefaultMode = if ($PSBoundParameters.ContainsKey('DefaultMode')) { $DefaultMode } else { $Mode }

$nativeMediaExplicit = $PSBoundParameters.ContainsKey('UseNativeMedia')
if (-not $nativeMediaExplicit -and $Mode -eq 'gamecast-client') {
  $autoNativeCc = Resolve-NativeCgoCompiler
  if ($autoNativeCc) {
    $UseNativeMedia = $true
    Write-Host "Auto-enabling native media for gamecast-client using compiler: $autoNativeCc"
  } else {
    Write-Warning @"
gamecast-client started without native media because no supported C compiler was found.
Native GameCast host sharing will stay unavailable until MinGW-w64/gcc is installed or you pass a native-capable build.
"@
  }
}

if ($UseNativeMedia) {
  $nativeCc = Resolve-NativeCgoCompiler
  if (-not $nativeCc) {
    throw @"
-UseNativeMedia requires cgo, but no supported C compiler was found.
Install MinGW-w64 (gcc) and ensure `gcc.exe` (or `x86_64-w64-mingw32-gcc.exe`) is in PATH.
"@
  }

  $nativeBinDir = Split-Path -Parent $nativeCc
  $pathEntries = $env:Path -split ';' | Where-Object { $_ }
  if ($pathEntries -notcontains $nativeBinDir) {
    $env:Path = (@($nativeBinDir) + $pathEntries) -join ';'
  }

  $env:CGO_ENABLED = '1'
  $env:CC = Split-Path -Leaf $nativeCc
  $nativeGpp = Join-Path $nativeBinDir 'g++.exe'
  if (Test-Path $nativeGpp) {
    $env:CXX = Split-Path -Leaf $nativeGpp
  }
  Write-Host "Native media build toolchain: CGO_ENABLED=1 CC=$env:CC PATH+$nativeBinDir"

  if (Test-Path $opusRuntimePath) {
    $pathEntries = $env:Path -split ';' | Where-Object { $_ }
    if ($pathEntries -notcontains $opusRuntimeDir) {
      $env:Path = (@($opusRuntimeDir) + $pathEntries) -join ';'
    }
    Write-Host "Opus runtime available: PATH+$opusRuntimeDir"
  } else {
    Write-Warning "Opus runtime not found at $opusRuntimePath; shared app audio will run video-only unless opus.dll is on PATH."
  }
}

if ($Installer -and $Action -ne 'build') {
  throw '-Installer can only be used with -Action build.'
}
if ($UseNativeCaptureExperiment -and -not $UseNativeMedia) {
  throw '-UseNativeCaptureExperiment requires -UseNativeMedia.'
}
if ($UseNativeEncoderExperiment -and -not $UseNativeMedia) {
  throw '-UseNativeEncoderExperiment requires -UseNativeMedia.'
}

$nsisAvailable = $false
if ($Installer) {
  $nsisAvailable = [bool](Get-Command makensis.exe -ErrorAction SilentlyContinue)
  if (-not $nsisAvailable) {
    $nsisAvailable = [bool](Get-Command makensis -ErrorAction SilentlyContinue)
  }
  if (-not $nsisAvailable) {
    throw 'NSIS is not installed. Install makensis to build an installer.'
  }
}

$env:PARTYLAN_MODE = $Mode
$env:PARTYLAN_AUTH_KEY = $AuthKey
if ($DERPRegionID -gt 0) {
  $env:PARTYLAN_DERP_REGION_ID = [string]$DERPRegionID
}
if ($DERPRegionCode) {
  $env:PARTYLAN_DERP_REGION_CODE = $DERPRegionCode
}
if ($DERPHostName) {
  $env:PARTYLAN_DERP_HOSTNAME = $DERPHostName
}
if ($DERPPort -gt 0) {
  $env:PARTYLAN_DERP_PORT = [string]$DERPPort
}
if ($TSControlURL) {
  $env:PARTYLAN_TS_CONTROL_URL = $TSControlURL
}
if ($ICEServers) {
  $env:PARTYLAN_ICE_SERVERS = $ICEServers
}
if ($TURNUsername) {
  $env:PARTYLAN_TURN_USERNAME = $TURNUsername
}
if ($TURNCredential) {
  $env:PARTYLAN_TURN_CREDENTIAL = $TURNCredential
}
if ($UseNativeCaptureExperiment) {
  $env:PARTYLAN_EXPERIMENTAL_NATIVE_CAPTURE = '1'
} else {
  Remove-Item Env:PARTYLAN_EXPERIMENTAL_NATIVE_CAPTURE -ErrorAction SilentlyContinue
}
if ($UseNativeEncoderExperiment) {
  $env:PARTYLAN_EXPERIMENTAL_NATIVE_ENCODER = '1'
} else {
  Remove-Item Env:PARTYLAN_EXPERIMENTAL_NATIVE_ENCODER -ErrorAction SilentlyContinue
}

$wails = Get-Command wails.exe -ErrorAction SilentlyContinue
if (-not $wails) {
  $candidate = Join-Path (Join-Path $env:USERPROFILE 'go\bin') 'wails.exe'
  if (Test-Path $candidate) {
    $wails = $candidate
  }
}

if (-not $wails) {
  throw 'wails.exe not found. Install Wails CLI or add it to PATH.'
}

$nativeCaptureBuildFlag = if ($UseNativeCaptureExperiment) { '1' } else { '0' }
$nativeEncoderBuildFlag = if ($UseNativeEncoderExperiment) { '1' } else { '0' }
$ldflagsValue = @(
  "-X PartyLAN/internal/appmode.DefaultModeName=$runtimeDefaultMode"
  "-X PartyLAN/internal/room.BuildNativeCaptureExperiment=$nativeCaptureBuildFlag"
  "-X PartyLAN/internal/room.BuildNativeEncoderExperiment=$nativeEncoderBuildFlag"
) -join ' '

switch ($Action) {
  'dev' {
    $tagList = @()
    if ($enableTsnet) { $tagList += 'partylan_tsnet' }
    if ($UseNativeMedia) { $tagList += 'partylan_native_media' }
    Write-Host "Starting partylan dev in mode '$Mode'..."
    if ($tagList.Count -gt 0) {
      & $wails.Source dev -tags ($tagList -join ' ') -ldflags $ldflagsValue
    } else {
      & $wails.Source dev -ldflags $ldflagsValue
    }
    if ($LASTEXITCODE -ne 0) {
      throw "wails dev failed with exit code $LASTEXITCODE"
    }
  }
  'build' {
    $tagList = @()
    if ($enableTsnet) { $tagList += 'partylan_tsnet' }
    if ($UseNativeMedia) { $tagList += 'partylan_native_media' }
    Write-Host "Building partylan in mode '$Mode'..."
    $buildArgs = @('build')
    if ($tagList.Count -gt 0) {
      $buildArgs += '-tags'
      $buildArgs += ($tagList -join ' ')
    }
    $buildArgs += '-ldflags'
    $buildArgs += $ldflagsValue
    if ($nsisAvailable) {
      $buildArgs += '-nsis'
    }
    & $wails.Source @buildArgs
    if ($LASTEXITCODE -ne 0) {
      throw "wails build failed with exit code $LASTEXITCODE"
    }

    if ($UseNativeMedia) {
      Assert-NativeBuildMetadata -BinaryPath (Join-Path $repoRoot 'build\bin\partylan.exe')
      Write-Host "Native media build metadata verified in build\\bin\\partylan.exe"

      if (Test-Path $opusRuntimePath) {
        Copy-Item -Force $opusRuntimePath (Join-Path $repoRoot 'build\bin\opus.dll')
        Write-Host "Copied Opus runtime to build\\bin\\opus.dll"
      }
    }
  }
}
