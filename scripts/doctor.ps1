#requires -Version 7.0
param([ValidateSet('all', 'server', 'client', 'dashboard', 'platform')][string]$Role = 'all')
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1') -Client:($Role -eq 'client' -and $IsWindows)
$root = Split-Path $PSScriptRoot -Parent
$versions = Get-Content (Join-Path $PSScriptRoot 'toolchain.json') -Raw | ConvertFrom-Json
$script:failures = 0
function Check-Tool([string]$Name, [string[]]$Arguments, [string]$Expected = '') {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        Write-Host "MISSING $Name"; $script:failures++; return
    }
    $lines = & $Name @Arguments 2>&1
    $ok = $LASTEXITCODE -eq 0
    $output = $lines -join "`n"
    if ($Expected -and -not $output.Contains($Expected)) { $ok = $false }
    if ($ok) { Write-Host "OK $Name : $(($output -split "`n")[0])" }
    else { Write-Host "FAIL $Name (expected: $Expected) : $output"; $script:failures++ }
}
Check-Tool 'git' @('--version')
if ($Role -in @('all', 'server', 'client', 'platform')) {
    Check-Tool 'go' @('version') "go$($versions.go) "
    Check-Tool 'protoc' @('--version') "libprotoc $($versions.protoc)"
}
if ($Role -in @('all', 'dashboard')) {
    Check-Tool 'node' @('--version') "v$($versions.node)"
    Check-Tool 'npm' @('--version') $versions.npm
}
if ($Role -in @('all', 'client')) {
    Check-Tool 'cmake' @('--version')
    Check-Tool 'ninja' @('--version')
    if (-not $env:VCPKG_ROOT -or -not (Test-Path -LiteralPath (Join-Path $env:VCPKG_ROOT 'scripts/buildsystems/vcpkg.cmake'))) {
        Write-Host 'MISSING vcpkg toolchain. Run scripts/setup-client.ps1.'; $script:failures++
    } else { Write-Host "OK vcpkg location: $env:VCPKG_ROOT" }
    Write-Host 'Run scripts/build/build.ps1 -Target client to verify compiler + exact manifest dependencies.'
}
if ($Role -in @('all', 'platform')) {
    Check-Tool 'docker' @('version')
    Check-Tool 'docker' @('compose', 'version')
}
if (-not (Test-Path -LiteralPath (Join-Path $root '.env'))) {
    Write-Host 'MISSING .env. Copy .env.example to .env.'; $script:failures++
}
if ($script:failures -gt 0) { Write-Host "$script:failures environment check(s) failed."; exit 1 }
Write-Host 'Requested environment checks passed.'
