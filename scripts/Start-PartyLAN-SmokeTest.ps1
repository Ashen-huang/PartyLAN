param(
  [string]$BinaryPath = '',
  [ValidateSet('gamecast-client', 'loopback', 'lan', 'networked', 'tailscale-system')]
  [string]$Mode = 'gamecast-client',
  [string]$AuthKey = '',
  [int]$WaitSeconds = 20,
  [switch]$LogToStdout,
  [string]$TSControlURL = '',
  [string]$ICEServers = '',
  [string]$TURNUsername = '',
  [string]$TURNCredential = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = Split-Path -Parent $scriptRoot
$candidateRelease = Join-Path $scriptRoot 'partylan.exe'
$candidateBuild = Join-Path $repoRoot 'build\bin\partylan.exe'

if (-not $BinaryPath) {
  if (Test-Path $candidateRelease) {
    $BinaryPath = $candidateRelease
  } elseif (Test-Path $candidateBuild) {
    $BinaryPath = $candidateBuild
  } else {
    throw "partylan.exe not found beside the script or in build\\bin."
  }
}

if (-not (Test-Path $BinaryPath)) {
  throw "Binary not found: $BinaryPath"
}

$env:PARTYLAN_MODE = $Mode
if ($Mode -eq 'networked') {
  $env:PARTYLAN_DERP_REGION_ID = '901'
  $env:PARTYLAN_DERP_REGION_CODE = 'cn1'
  $env:PARTYLAN_DERP_HOSTNAME = '103.236.92.32'
  $env:PARTYLAN_DERP_PORT = '443'
  $env:PARTYLAN_NETWORKED_BACKEND = 'tsnet'
} else {
  Remove-Item Env:PARTYLAN_DERP_REGION_ID -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_DERP_REGION_CODE -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_DERP_HOSTNAME -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_DERP_PORT -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_NETWORKED_BACKEND -ErrorAction SilentlyContinue
}
$smokeLogRoot = Join-Path $env:TEMP ("PartyLAN-SmokeTest-" + [guid]::NewGuid().ToString('N'))
$env:PARTYLAN_LOG_DIR = $smokeLogRoot
if ($LogToStdout) {
  $env:PARTYLAN_LOG_STDOUT = '1'
} else {
  Remove-Item Env:PARTYLAN_LOG_STDOUT -ErrorAction SilentlyContinue
}
if ($AuthKey) {
  $env:PARTYLAN_AUTH_KEY = $AuthKey
}
if ($TSControlURL) {
  $env:PARTYLAN_TS_CONTROL_URL = $TSControlURL
} else {
  Remove-Item Env:PARTYLAN_TS_CONTROL_URL -ErrorAction SilentlyContinue
}
if ($ICEServers) {
  $env:PARTYLAN_ICE_SERVERS = $ICEServers
} else {
  Remove-Item Env:PARTYLAN_ICE_SERVERS -ErrorAction SilentlyContinue
}
if ($TURNUsername) {
  $env:PARTYLAN_TURN_USERNAME = $TURNUsername
} else {
  Remove-Item Env:PARTYLAN_TURN_USERNAME -ErrorAction SilentlyContinue
}
if ($TURNCredential) {
  $env:PARTYLAN_TURN_CREDENTIAL = $TURNCredential
} else {
  Remove-Item Env:PARTYLAN_TURN_CREDENTIAL -ErrorAction SilentlyContinue
}

$process = Start-Process -FilePath $BinaryPath -WorkingDirectory (Split-Path -Parent $BinaryPath) -PassThru
try {
  if ($WaitSeconds -lt 5) {
    throw '-WaitSeconds must be at least 5.'
  }

  Start-Sleep -Seconds $WaitSeconds
  $logDirs = @(
    (Join-Path $smokeLogRoot 'logs'),
    (Join-Path (Split-Path -Parent $BinaryPath) 'logs'),
    (Join-Path $env:APPDATA 'PartyLAN\logs'),
    (Join-Path $env:TEMP 'PartyLAN\logs')
  ) | Where-Object { Test-Path $_ }

  $latestLog = $null
  foreach ($dir in $logDirs) {
    $candidate = Get-ChildItem $dir -File -Filter 'partylan-*.log' -ErrorAction SilentlyContinue |
      Sort-Object LastWriteTime -Descending |
      Select-Object -First 1
    if ($candidate) {
      $latestLog = $candidate
      break
    }
  }

  if (-not $latestLog) {
    throw "Smoke test failed: no log file found."
  }

  $content = Get-Content -Raw $latestLog.FullName
  $checks = @(
    '[video/capability] startup',
    '[audio/capability] startup'
  )
  if ($Mode -eq 'networked') {
    $checks += 'custom DERP present'
    $checks += 'region=901/cn1'
  }
  if ($ICEServers) {
    $checks += 'media ICE servers configured'
  } else {
    $checks += 'media ICE servers not configured'
  }
  if ($TURNUsername -or $TURNCredential) {
    $checks += 'media TURN credentials configured'
  } elseif ($ICEServers -match '(?i)turns?:') {
    $checks += 'media TURN configured without credentials'
  } else {
    $checks += 'media TURN not configured'
  }
  if ($TSControlURL) {
    $checks += 'tsnet control URL override configured'
  }
  $missing = @($checks | Where-Object { $content -notmatch [regex]::Escape($_) })
  if ($missing.Count -gt 0) {
    throw "Smoke test failed. Missing markers: $($missing -join ', '). Log: $($latestLog.FullName)"
  }
  if ($content -match [regex]::Escape('network start failed')) {
    throw "Smoke test failed: selected mode '$Mode' could not start. Log: $($latestLog.FullName)"
  }

  $warnings = @()
  if ($content -match [regex]::Escape("Tailscale could not connect to the 'PartyLAN CN Relay' relay server")) {
    $warnings += "relay reachability warning is present in the latest log"
  }
  if ($content -match [regex]::Escape('custom DERP missing')) {
    $warnings += "custom DERP missing was observed in the latest log"
  }

  Write-Host "Smoke test passed."
  Write-Host "Log: $($latestLog.FullName)"
  Write-Host "Mode: $Mode"
  if ($Mode -eq 'networked') {
    Write-Host "DERP note: custom DERP present and region=901/cn1 observed."
  }
  if ($ICEServers) {
    Write-Host "Media note: ICE/TURN configuration markers were observed."
  } else {
    Write-Host "Media note: no ICE/TURN config was injected and the runtime reported that state."
  }
  if ($warnings.Count -gt 0) {
    Write-Warning ($warnings -join '; ')
  }
}
finally {
  if ($process -and -not $process.HasExited) {
    $process.CloseMainWindow() | Out-Null
    Start-Sleep -Seconds 2
    if (-not $process.HasExited) {
      $process.Kill()
    }
  }
  Remove-Item Env:PARTYLAN_LOG_DIR -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_LOG_STDOUT -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_TS_CONTROL_URL -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_ICE_SERVERS -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_TURN_USERNAME -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_TURN_CREDENTIAL -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_AUTH_KEY -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_MODE -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_NETWORKED_BACKEND -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_DERP_REGION_ID -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_DERP_REGION_CODE -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_DERP_HOSTNAME -ErrorAction SilentlyContinue
  Remove-Item Env:PARTYLAN_DERP_PORT -ErrorAction SilentlyContinue
}
