param(
  [Parameter(Mandatory = $false)]
  [string]$SourceRoot = "tmp/screego-upstream"
)

$ErrorActionPreference = "Stop"
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "../..")
$source = Resolve-Path (Join-Path $repoRoot $SourceRoot)

Push-Location (Join-Path $source "ui")
try {
  if (-not (Test-Path "node_modules/.bin/tsc.cmd") -and -not (Test-Path "node_modules/.bin/tsc")) {
    throw "Screego UI dependencies are missing; install the lockfile dependencies before verification"
  }
  node --test --experimental-strip-types src/iceSession.test.mjs src/Room.fullscreen.test.mjs
  if ($LASTEXITCODE -ne 0) { throw "Screego UI behavior tests failed" }
  npm run build
  if ($LASTEXITCODE -ne 0) { throw "Screego UI build failed" }
} finally {
  Pop-Location
}

Push-Location $source
try {
  $goCommand = "go"
  $cachedToolchain = Join-Path $env:USERPROFILE "go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.3.windows-amd64/bin/go.exe"
  if (Test-Path $cachedToolchain) { $goCommand = $cachedToolchain }
  & $goCommand test ./ws/... ./turn/...
  if ($LASTEXITCODE -ne 0) { throw "Screego Go tests failed" }
} finally {
  Pop-Location
}

Write-Host "Screego patch verification passed"
