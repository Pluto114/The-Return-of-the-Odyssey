#requires -Version 7.0
param([ValidateSet('scaffold', 'client', 'dashboard')][string]$Target = 'scaffold')
$ErrorActionPreference = 'Stop'
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
. (Join-Path $root 'scripts/env.ps1') -Client:($Target -eq 'client' -and $IsWindows)
Push-Location $root
try {
    if ($Target -eq 'dashboard') {
        & npm --prefix dashboard run build
        if ($LASTEXITCODE -ne 0) { throw 'Dashboard build failed.' }
    } else {
        $preset = if ($Target -eq 'scaffold') { 'scaffold' } elseif ($IsWindows) { 'client-windows' } else { 'client-linux' }
        & cmake --preset $preset
        if ($LASTEXITCODE -ne 0) { throw 'CMake configure failed.' }
        & cmake --build --preset $preset
        if ($LASTEXITCODE -ne 0) { throw 'CMake build failed.' }
    }
} finally { Pop-Location }
