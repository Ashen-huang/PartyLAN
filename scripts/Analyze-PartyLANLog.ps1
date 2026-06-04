param(
  [Parameter(Mandatory = $true)]
  [string]$Path,
  [switch]$AsJson
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if (-not (Test-Path $Path)) {
  throw "Log file not found: $Path"
}

$lines = Get-Content -Path $Path

function Get-NumberStats {
  param(
    [double[]]$Values
  )
  if (-not $Values -or $Values.Count -eq 0) {
    return [pscustomobject]@{
      Count = 0
      Avg   = $null
      Min   = $null
      Max   = $null
    }
  }
  $avg = ($Values | Measure-Object -Average).Average
  $min = ($Values | Measure-Object -Minimum).Minimum
  $max = ($Values | Measure-Object -Maximum).Maximum
  return [pscustomobject]@{
    Count = $Values.Count
    Avg   = [math]::Round($avg, 2)
    Min   = [math]::Round($min, 2)
    Max   = [math]::Round($max, 2)
  }
}

function Parse-SendKey {
  param([string]$Line)
  if ($Line -match 'backend=([^\s]+)\s+target=(\d+x\d+@\d+fps)') {
    return "$($matches[1])|$($matches[2])"
  }
  if ($Line -match 'target=(\d+x\d+@\d+fps)') {
    return "unknown|$($matches[1])"
  }
  return $null
}

function Parse-RecvKey {
  param([string]$Line)
  if ($Line -match 'backend=([^\s]+)\s+frame=(\d+x\d+)') {
    return "$($matches[1])|$($matches[2])"
  }
  if ($Line -match 'frame=(\d+x\d+)') {
    return "unknown|$($matches[1])"
  }
  return $null
}

$sendGroups = @{}
$recvGroups = @{}
$decodeErrLines = @()
$shareSend = @()
$shareRecv = @()
$derpLines = @()
$directPathLines = @()
$derpPathLines = @()
$peerRelayPathLines = @()
$customDerpPresentLines = @()
$customDerpMissingLines = @()
$mediaIceLines = @()
$mediaIceStateLines = @()
$mediaIceSelectedLines = @()
$mediaIceRelayLines = @()

foreach ($line in $lines) {
  if ($line -match '\[share/send\]') { $shareSend += $line }
  if ($line -match '\[share/recv\]') { $shareRecv += $line }
  if ($line -match '\[stream/recv\] decode errors=') { $decodeErrLines += $line }
  if ($line -match '\[network/derp\]') {
    $derpLines += $line
    if ($line -match 'custom DERP present') { $customDerpPresentLines += $line }
    if ($line -match 'custom DERP missing') { $customDerpMissingLines += $line }
    if ($line -match 'path=direct') { $directPathLines += $line }
    if ($line -match 'path=derp') { $derpPathLines += $line }
    if ($line -match 'path=peer-relay') { $peerRelayPathLines += $line }
  }
  if ($line -match '\[media/ice\]') {
    $mediaIceLines += $line
    if ($line -match 'state=') { $mediaIceStateLines += $line }
    if ($line -match 'selected path=') { $mediaIceSelectedLines += $line }
    if ($line -match 'selected path=relay' -or $line -match 'TURN credentials configured' -or $line -match 'TURN configured') {
      $mediaIceRelayLines += $line
    }
  }

  if ($line -match '\[stream/send\]') {
    $key = Parse-SendKey -Line $line
    if ($null -eq $key) { continue }
    if (-not $sendGroups.ContainsKey($key)) {
      $sendGroups[$key] = [pscustomobject]@{
        ActualFps = New-Object System.Collections.Generic.List[double]
        Bitrate   = New-Object System.Collections.Generic.List[double]
        CaptureMs = New-Object System.Collections.Generic.List[double]
        ResizeMs  = New-Object System.Collections.Generic.List[double]
        EncodeMs  = New-Object System.Collections.Generic.List[double]
      }
    }
    $g = $sendGroups[$key]
    if ($line -match 'actual=([0-9.]+)fps') { [void]$g.ActualFps.Add([double]$matches[1]) }
    if ($line -match 'bitrate=([0-9.]+)kbps') { [void]$g.Bitrate.Add([double]$matches[1]) }
    if ($line -match 'cap=([0-9.]+)ms') { [void]$g.CaptureMs.Add([double]$matches[1]) }
    if ($line -match 'resize=([0-9.]+)ms') { [void]$g.ResizeMs.Add([double]$matches[1]) }
    if ($line -match 'enc=([0-9.]+)ms') { [void]$g.EncodeMs.Add([double]$matches[1]) }
  }

  if ($line -match '\[stream/recv\].*frame=') {
    $key = Parse-RecvKey -Line $line
    if ($null -eq $key) { continue }
    if (-not $recvGroups.ContainsKey($key)) {
      $recvGroups[$key] = [pscustomobject]@{
        DecodeFps = New-Object System.Collections.Generic.List[double]
        SourceFps = New-Object System.Collections.Generic.List[double]
        Bitrate   = New-Object System.Collections.Generic.List[double]
      }
    }
    $g = $recvGroups[$key]
    if ($line -match 'decode=([0-9.]+)fps') { [void]$g.DecodeFps.Add([double]$matches[1]) }
    if ($line -match 'source=([0-9.]+)fps') { [void]$g.SourceFps.Add([double]$matches[1]) }
    if ($line -match 'bitrate=([0-9.]+)kbps') { [void]$g.Bitrate.Add([double]$matches[1]) }
  }
}

$sendReport = @()
foreach ($k in ($sendGroups.Keys | Sort-Object)) {
  $parts = $k.Split('|', 2)
  $g = $sendGroups[$k]
  $sendReport += [pscustomobject]@{
    Backend       = $parts[0]
    Target        = $parts[1]
    ActualFps     = Get-NumberStats -Values $g.ActualFps
    BitrateKbps   = Get-NumberStats -Values $g.Bitrate
    CaptureMs     = Get-NumberStats -Values $g.CaptureMs
    ResizeMs      = Get-NumberStats -Values $g.ResizeMs
    EncodeMs      = Get-NumberStats -Values $g.EncodeMs
  }
}

$recvReport = @()
foreach ($k in ($recvGroups.Keys | Sort-Object)) {
  $parts = $k.Split('|', 2)
  $g = $recvGroups[$k]
  $recvReport += [pscustomobject]@{
    Backend       = $parts[0]
    Frame         = $parts[1]
    DecodeFps     = Get-NumberStats -Values $g.DecodeFps
    SourceFps     = Get-NumberStats -Values $g.SourceFps
    BitrateKbps   = Get-NumberStats -Values $g.Bitrate
  }
}

$result = [pscustomobject]@{
  Path = (Resolve-Path $Path).Path
  Summary = [pscustomobject]@{
    ShareSendEvents = $shareSend.Count
    ShareRecvEvents = $shareRecv.Count
    DecodeErrorLines = $decodeErrLines.Count
    LastDecodeErrorLine = if ($decodeErrLines.Count -gt 0) { $decodeErrLines[-1] } else { $null }
    DERPDiagnosticLines = $derpLines.Count
    CustomDERPPresentLines = $customDerpPresentLines.Count
    CustomDERPMissingLines = $customDerpMissingLines.Count
    DirectPathLines = $directPathLines.Count
    DERPPathLines = $derpPathLines.Count
    PeerRelayPathLines = $peerRelayPathLines.Count
    LastDERPDiagnosticLine = if ($derpLines.Count -gt 0) { $derpLines[-1] } else { $null }
    MediaDiagnosticLines = $mediaIceLines.Count
    MediaStateLines = $mediaIceStateLines.Count
    MediaSelectedPathLines = $mediaIceSelectedLines.Count
    MediaRelayLines = $mediaIceRelayLines.Count
    LastMediaDiagnosticLine = if ($mediaIceLines.Count -gt 0) { $mediaIceLines[-1] } else { $null }
  }
  Send = $sendReport
  Recv = $recvReport
}

if ($AsJson) {
  $result | ConvertTo-Json -Depth 8
  exit 0
}

Write-Host "Log: $($result.Path)"
Write-Host "Share events: send=$($result.Summary.ShareSendEvents), recv=$($result.Summary.ShareRecvEvents)"
Write-Host "Decode error lines: $($result.Summary.DecodeErrorLines)"
if ($result.Summary.LastDecodeErrorLine) {
  Write-Host "Last decode error: $($result.Summary.LastDecodeErrorLine)"
}
Write-Host "DERP diagnostics: total=$($result.Summary.DERPDiagnosticLines), custom_present=$($result.Summary.CustomDERPPresentLines), custom_missing=$($result.Summary.CustomDERPMissingLines), direct_paths=$($result.Summary.DirectPathLines), derp_paths=$($result.Summary.DERPPathLines), peer_relay_paths=$($result.Summary.PeerRelayPathLines)"
if ($result.Summary.LastDERPDiagnosticLine) {
  Write-Host "Last DERP diagnostic: $($result.Summary.LastDERPDiagnosticLine)"
}
Write-Host "Media diagnostics: total=$($result.Summary.MediaDiagnosticLines), state_lines=$($result.Summary.MediaStateLines), selected_paths=$($result.Summary.MediaSelectedPathLines), relay_lines=$($result.Summary.MediaRelayLines)"
if ($result.Summary.LastMediaDiagnosticLine) {
  Write-Host "Last media diagnostic: $($result.Summary.LastMediaDiagnosticLine)"
}

Write-Host ""
Write-Host "=== Sender ==="
foreach ($item in $result.Send) {
  Write-Host ("[{0}] {1}" -f $item.Backend, $item.Target)
  Write-Host ("  actual fps: avg={0} min={1} max={2} n={3}" -f $item.ActualFps.Avg, $item.ActualFps.Min, $item.ActualFps.Max, $item.ActualFps.Count)
  Write-Host ("  bitrate kbps: avg={0} min={1} max={2}" -f $item.BitrateKbps.Avg, $item.BitrateKbps.Min, $item.BitrateKbps.Max)
  Write-Host ("  capture ms: avg={0} min={1} max={2}" -f $item.CaptureMs.Avg, $item.CaptureMs.Min, $item.CaptureMs.Max)
  Write-Host ("  resize ms: avg={0} min={1} max={2}" -f $item.ResizeMs.Avg, $item.ResizeMs.Min, $item.ResizeMs.Max)
  Write-Host ("  encode ms: avg={0} min={1} max={2}" -f $item.EncodeMs.Avg, $item.EncodeMs.Min, $item.EncodeMs.Max)
}

Write-Host ""
Write-Host "=== Receiver ==="
foreach ($item in $result.Recv) {
  Write-Host ("[{0}] {1}" -f $item.Backend, $item.Frame)
  Write-Host ("  decode fps: avg={0} min={1} max={2} n={3}" -f $item.DecodeFps.Avg, $item.DecodeFps.Min, $item.DecodeFps.Max, $item.DecodeFps.Count)
  Write-Host ("  source fps: avg={0} min={1} max={2}" -f $item.SourceFps.Avg, $item.SourceFps.Min, $item.SourceFps.Max)
  Write-Host ("  bitrate kbps: avg={0} min={1} max={2}" -f $item.BitrateKbps.Avg, $item.BitrateKbps.Min, $item.BitrateKbps.Max)
}
