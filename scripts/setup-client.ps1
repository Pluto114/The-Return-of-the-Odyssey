#requires -Version 7.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1') -Client:$IsWindows
$root = Split-Path $PSScriptRoot -Parent
$versions = Get-Content (Join-Path $PSScriptRoot 'toolchain.json') -Raw | ConvertFrom-Json
$vcpkgDir = Join-Path $root '.tools/vcpkg'
if (-not (Test-Path -LiteralPath $vcpkgDir)) {
    & git clone --filter=blob:none --branch $versions.vcpkgTag --single-branch https://github.com/microsoft/vcpkg.git $vcpkgDir
    if ($LASTEXITCODE -ne 0) { throw 'vcpkg clone failed.' }
}
$revision = & git -C $vcpkgDir rev-parse HEAD
if ($LASTEXITCODE -ne 0 -or $revision -ne $versions.vcpkgBaseline) {
    throw "The local vcpkg checkout must be at $($versions.vcpkgBaseline). Existing checkout was not modified."
}
$env:VCPKG_ROOT = $vcpkgDir
if ($IsWindows) { & (Join-Path $vcpkgDir 'bootstrap-vcpkg.bat') -disableMetrics }
else { & (Join-Path $vcpkgDir 'bootstrap-vcpkg.sh') -disableMetrics }
if ($LASTEXITCODE -ne 0) { throw 'vcpkg bootstrap failed.' }
Write-Host 'Client toolchain is ready. Generate protocols, then run the client build script.'
