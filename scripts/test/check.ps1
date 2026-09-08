#requires -Version 7.0
param([switch]$Race)
$ErrorActionPreference = 'Stop'
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
. (Join-Path $root 'scripts/env.ps1')
# Validate each module independently so go.work does not hide a missing dependency.
$env:GOWORK = 'off'
foreach ($module in @('server', 'bot')) {
    Push-Location (Join-Path $root $module)
    try {
        & go mod download all
        if ($LASTEXITCODE -ne 0) { throw "Dependency resolution failed in $module." }
        & go mod verify
        if ($LASTEXITCODE -ne 0) { throw "Dependency verification failed in $module." }
        $sources = @(Get-ChildItem -Recurse -Filter '*.go')
        if ($sources.Count -eq 0) {
            Write-Host "SKIP $module tests: no Go source files yet."
            continue
        }
        $testArgs = @('test')
        if ($Race) { $testArgs += '-race' }
        $testArgs += './...'
        & go @testArgs
        if ($LASTEXITCODE -ne 0) { throw "Go tests failed in $module." }
        & go vet ./...
        if ($LASTEXITCODE -ne 0) { throw "Go vet failed in $module." }
    } finally { Pop-Location }
}
Write-Host 'Checks complete. Generated-code compilation is not a gameplay test.'
