#requires -Version 7.0
<#
.SYNOPSIS
Builds the C++ client in Release into its own build tree (interim path, role C).

.DESCRIPTION
The performance acceptance criterion is Release-only, but the shared build entry points
(CMakePresets.json, scripts/build/build.ps1) still have no Release configuration. The plan
therefore documents a temporary path: configure a SEPARATE Release tree that leaves the
Debug tree alone. This script is that path as one command.

It deliberately does NOT modify CMakePresets.json or scripts/build/build.ps1: those are
shared files owned by D and assembled by A. When the shared preset lands, use
    pwsh -File scripts/build/build.ps1 -Target client-release
instead and delete this script along with plan section 2.2.

The variables it passes are copied 1:1 from the client-windows preset (generator, triplet,
toolchain, manifest dir, ODYSSEY_CLIENT_DEPS) with CMAKE_BUILD_TYPE switched to Release, so
the only difference from the Debug build is the optimisation level.

.EXAMPLE
pwsh -File scripts/verify/build-client-release.ps1
pwsh -File scripts/verify/build-client-release.ps1 -Reconfigure
pwsh -File scripts/verify/build-client-release.ps1 -BuildDir build/client-windows-release -Jobs 8
#>
[CmdletBinding()]
param(
    # Separate from build/client-windows on purpose: reusing the Debug tree would flip its
    # configuration and invalidate the Debug ctest baseline.
    [string]$BuildDir = 'build/client-windows-release',
    # Re-run the configure step even when a cache already exists.
    [switch]$Reconfigure,
    [int]$Jobs = 4,
    [string]$Target = 'odyssey_client'
)

$ErrorActionPreference = 'Stop'
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
if (-not [System.IO.Path]::IsPathRooted($BuildDir)) {
    $BuildDir = Join-Path $root $BuildDir
}

# scripts/env.ps1 supplies VCPKG_ROOT; the CMake toolchain file is resolved through it.
. (Join-Path $root 'scripts/env.ps1') -Client

foreach ($tool in @('cmake', 'ninja')) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        throw "$tool was not found on PATH. Run this from the same shell you use for scripts/build/build.ps1."
    }
}
if (-not $env:VCPKG_ROOT) {
    throw 'VCPKG_ROOT is not set after sourcing scripts/env.ps1; the toolchain file cannot be resolved.'
}

Write-Host "Release build tree: $BuildDir" -ForegroundColor Cyan

# Refuse to reuse a tree configured for something else: a Debug cache in this directory
# would silently answer the wrong question, and a stale generator cannot be changed in place.
$cache = Join-Path $BuildDir 'CMakeCache.txt'
if (Test-Path -LiteralPath $cache) {
    $cached = Select-String -LiteralPath $cache -Pattern '^CMAKE_BUILD_TYPE:STRING=(.*)$' |
        Select-Object -First 1
    $cachedType = if ($cached) { $cached.Matches[0].Groups[1].Value } else { '<unset>' }
    if ($cachedType -ne 'Release' -and -not $Reconfigure) {
        throw @"
$BuildDir is already configured as '$cachedType'.

Point -BuildDir somewhere else (the default is build/client-windows-release), or delete
that tree, or pass -Reconfigure to overwrite its configuration.
"@
    }
}

$configure = -not (Test-Path -LiteralPath $cache) -or $Reconfigure
if ($configure) {
    Write-Host 'Configuring (Release, Ninja, vcpkg x64-windows)...' -ForegroundColor Yellow
    & cmake -S $root -B $BuildDir -G Ninja `
        "-DCMAKE_BUILD_TYPE=Release" `
        "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON" `
        "-DCMAKE_TOOLCHAIN_FILE=$env:VCPKG_ROOT/scripts/buildsystems/vcpkg.cmake" `
        "-DVCPKG_MANIFEST_DIR=$root/client" `
        "-DVCPKG_TARGET_TRIPLET=x64-windows" `
        "-DVCPKG_HOST_TRIPLET=x64-windows" `
        '-DODYSSEY_CLIENT_DEPS=ON' `
        '-DCMAKE_C_COMPILER=cl' `
        '-DCMAKE_CXX_COMPILER=cl'
    if ($LASTEXITCODE -ne 0) {
        throw 'CMake configure failed.'
    }
}

Write-Host "Building $Target..." -ForegroundColor Yellow
& cmake --build $BuildDir --target $Target --parallel $Jobs
if ($LASTEXITCODE -ne 0) {
    throw 'CMake build failed.'
}

$exe = Join-Path $BuildDir "client/$Target.exe"
if (-not (Test-Path -LiteralPath $exe)) {
    throw "Build reported success but $exe is missing; check the target name."
}

Write-Host ''
Write-Host "Release client: $exe" -ForegroundColor Green
Write-Host 'Next (performance acceptance, needs a live scene: gameserver + one loadbot):' -ForegroundColor Green
Write-Host '  pwsh -File scripts/verify/client-release-ui-perf.ps1 -Rounds 3 -Frames 600' -ForegroundColor Green
Write-Host 'Plan: docs/verification/phase2-c/RELEASE-UI-PERF-PLAN.md' -ForegroundColor Green
