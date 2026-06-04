param(
  [ValidateSet('loopback', 'lan', 'networked', 'tailscale-system')]
  [string]$Mode = 'tailscale-system',
  [int]$DERPRegionID = 0,
  [string]$DERPRegionCode = '',
  [string]$DERPHostName = '',
  [int]$DERPPort = 0
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path

& (Join-Path $scriptRoot 'Package-PartyLAN.ps1') `
  -Mode $Mode `
  -DERPRegionID $DERPRegionID `
  -DERPRegionCode $DERPRegionCode `
  -DERPHostName $DERPHostName `
  -DERPPort $DERPPort `
  -UseNativeMedia `
  -UseNativeCaptureExperiment `
  -UseNativeEncoderExperiment
