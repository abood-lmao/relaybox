#Requires -Version 5.1
<#
.SYNOPSIS
Runs Relaybox for development on Windows, like `make dev`.

.DESCRIPTION
Builds and starts the Go server on http://127.0.0.1:8787, then starts the
dashboard with hot reload on http://localhost:5173. Press Ctrl+C to stop both.

Arguments are passed to `relaybox serve`. If you change the port, also set
RELAYBOX_DEV_BACKEND so the dashboard proxy can find the server.

.EXAMPLE
.\scripts\dev.ps1

.EXAMPLE
.\scripts\dev.ps1 --database .\data\demo.db
#>
[CmdletBinding()]
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]] $ServeArgs = @()
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$web = Join-Path $root 'web'

foreach ($tool in 'go', 'node', 'npm') {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        Write-Host "$tool was not found on PATH. See docs/development.md for the prerequisites." -ForegroundColor Red
        exit 1
    }
}

if (-not (Test-Path (Join-Path $web 'node_modules'))) {
    Write-Host 'Installing dashboard packages (npm ci)...'
    Push-Location $web
    try {
        npm ci
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    }
    finally { Pop-Location }
}

$exe = if ($env:OS -eq 'Windows_NT') { 'relaybox-dev.exe' } else { 'relaybox-dev' }
$binary = Join-Path (Join-Path $root 'bin') $exe

Write-Host 'Building the server...'
Push-Location $root
try {
    $env:CGO_ENABLED = '0'
    go build -o $binary ./cmd/relaybox
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
finally { Pop-Location }

$serveArgList = @('serve')
if ($ServeArgs) { $serveArgList += $ServeArgs }
$quoted = $serveArgList | ForEach-Object {
    if ($_ -match '\s') { '"' + $_ + '"' } else { $_ }
}
$server = Start-Process -FilePath $binary -ArgumentList $quoted -WorkingDirectory $root -NoNewWindow -PassThru

try {
    Write-Host 'Dashboard with hot reload: http://localhost:5173' -ForegroundColor Green
    Push-Location $web
    # Vite runs through node rather than npm.cmd so that Ctrl+C does not stop
    # at the "Terminate batch job" prompt.
    node (Join-Path $web 'node_modules/vite/bin/vite.js')
}
finally {
    Pop-Location
    if (-not $server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
    }
}
