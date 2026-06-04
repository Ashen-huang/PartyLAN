param(
  [string]$AuthKey = '',
  [switch]$ForceTsnet,
  [switch]$LogToStdout,
  [string]$TSControlURL = $env:PARTYLAN_TS_CONTROL_URL,
  [string]$ICEServers = $env:PARTYLAN_ICE_SERVERS,
  [string]$TURNUsername = $env:PARTYLAN_TURN_USERNAME,
  [string]$TURNCredential = $env:PARTYLAN_TURN_CREDENTIAL
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$packageRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $packageRoot

$exePath = Join-Path $packageRoot 'PartyLAN.exe'
if (-not (Test-Path $exePath)) {
  throw "PartyLAN.exe not found beside $($MyInvocation.MyCommand.Name)."
}

# Keep the release package aligned with the currently deployed custom DERP.
$env:PARTYLAN_DERP_REGION_ID = '901'
$env:PARTYLAN_DERP_REGION_CODE = 'cn1'
$env:PARTYLAN_DERP_HOSTNAME = '103.236.92.32'
$env:PARTYLAN_DERP_PORT = '443'

if ($ForceTsnet) {
  $env:PARTYLAN_MODE = 'networked'
  $env:PARTYLAN_NETWORKED_BACKEND = 'tsnet'
} else {
  $env:PARTYLAN_MODE = 'tailscale-system'
  Remove-Item Env:PARTYLAN_NETWORKED_BACKEND -ErrorAction SilentlyContinue
}

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

$launchMode = if ($ForceTsnet) { 'networked/tsnet' } else { 'tailscale-system' }
Write-Host "Launching PartyLAN in $launchMode mode with custom DERP region 901/cn1 at 103.236.92.32:443"
if ($ForceTsnet) {
  Write-Host "Network backend override: tsnet"
}

& $exePath
