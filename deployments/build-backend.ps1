# Build all three binaries consumed by Docker Compose. Go 1.25.1+ required.
# Run from any directory: pwsh -NoProfile -File deployments/build-backend.ps1
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$root = Split-Path -Parent $PSScriptRoot
$saved = @{}
foreach ($key in @('GOOS', 'GOARCH', 'CGO_ENABLED')) {
  $saved[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
}

Push-Location -LiteralPath $root
try {
  Get-Command go -ErrorAction Stop | Out-Null
  $env:GOOS = 'linux'
  $env:GOARCH = 'amd64'
  $env:CGO_ENABLED = '0'
  $targets = @(
    @{ Name = 'gateway'; Package = './gateway/cmd/server/' },
    @{ Name = 'media-worker'; Package = './gateway/cmd/media-worker/' },
    @{ Name = 'notification-worker'; Package = './notification/cmd/worker/' }
  )
  foreach ($target in $targets) {
    $output = "deployments/$($target.Name)-linux-amd64"
    & go build -buildvcs=false -o $output $target.Package
    if ($LASTEXITCODE -ne 0) { throw "Build failed for $($target.Name), exit code $LASTEXITCODE" }
    Write-Host "Built $output"
  }
} finally {
  foreach ($key in $saved.Keys) {
    [Environment]::SetEnvironmentVariable($key, $saved[$key], 'Process')
  }
  Pop-Location
}
