#requires -Version 7.0
[CmdletBinding()]
param([switch]$WithComposeValidator)
$ErrorActionPreference = 'Stop'
if (-not $IsWindows -or [Runtime.InteropServices.RuntimeInformation]::OSArchitecture -ne 'X64') {
    throw 'This portable bootstrap targets Windows x64. See docs/SETUP.md for Linux installation.'
}
$root = Split-Path $PSScriptRoot -Parent
$versions = Get-Content (Join-Path $PSScriptRoot 'toolchain.json') -Raw | ConvertFrom-Json
$toolsDir = Join-Path $root '.tools'
$cacheDir = Join-Path $toolsDir 'downloads'
New-Item -ItemType Directory -Force $cacheDir | Out-Null
foreach ($item in $versions.windowsDownloads) {
    if ($item.name -eq 'compose-validator' -and -not $WithComposeValidator) { continue }
    $destination = Join-Path $toolsDir $item.destination
    $executable = Join-Path $destination $item.executable
    if (Test-Path -LiteralPath $executable) {
        Write-Host "Already installed locally: $($item.name)"
        continue
    }
    $archive = Join-Path $cacheDir ([Uri]$item.url).Segments[-1]
    if (-not (Test-Path -LiteralPath $archive)) {
        Write-Host "Downloading $($item.name) from the official distribution..."
        Invoke-WebRequest -Uri $item.url -OutFile $archive
    }
    if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant() -ne $item.sha256) {
        throw "SHA256 mismatch for $archive. Remove that file and retry."
    }
    New-Item -ItemType Directory -Force $destination | Out-Null
    if ($archive.EndsWith('.zip')) { Expand-Archive -LiteralPath $archive -DestinationPath $destination -Force }
    else { Copy-Item -LiteralPath $archive -Destination $executable }
    if (-not (Test-Path -LiteralPath $executable)) { throw "Missing executable after extraction: $executable" }
}
foreach ($relative in @('.env', 'server/configs/.env', 'bot/configs/.env', 'client/.env', 'dashboard/.env')) {
    $target = Join-Path $root $relative
    if (-not (Test-Path -LiteralPath $target)) { Copy-Item -LiteralPath "$target.example" -Destination $target }
}
Write-Host 'Bootstrap complete. Run: . ./scripts/env.ps1'
Write-Host 'Docker Desktop and the C++ workload are installed separately; see docs/SETUP.md.'
