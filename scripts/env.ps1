#requires -Version 7.0
param([switch]$Client, [switch]$UseSystemProxy)
$ErrorActionPreference = 'Stop'
$odysseyRoot = Split-Path $PSScriptRoot -Parent
$odysseyTools = Join-Path $odysseyRoot '.tools'
$odysseyVersions = Get-Content (Join-Path $PSScriptRoot 'toolchain.json') -Raw | ConvertFrom-Json
$env:GOBIN = Join-Path $odysseyTools 'bin'
$env:npm_config_cache = Join-Path $odysseyTools 'npm-cache'
New-Item -ItemType Directory -Force $env:GOBIN | Out-Null
$odysseyPaths = @($env:GOBIN)
if ($IsWindows) {
    if ($UseSystemProxy) {
        $testUri = [Uri]'https://github.com'
        $proxyUri = [System.Net.WebRequest]::GetSystemWebProxy().GetProxy($testUri)
        if ($proxyUri.Host -ne $testUri.Host) {
            # WinHTTP-based vcpkg bootstrap expects host:port, without a URL path.
            $env:HTTP_PROXY = $proxyUri.Authority
            $env:HTTPS_PROXY = $proxyUri.Authority
            $env:npm_config_proxy = $proxyUri.GetLeftPart([UriPartial]::Authority)
            $env:npm_config_https_proxy = $env:npm_config_proxy
        }
    }
    foreach ($download in $odysseyVersions.windowsDownloads) {
        $executable = Join-Path (Join-Path $odysseyTools $download.destination) $download.executable
        if (Test-Path -LiteralPath $executable) { $odysseyPaths += Split-Path $executable -Parent }
    }
    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
    if (Test-Path -LiteralPath $vswhere) {
        $odysseyVS = & $vswhere -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
        if ($odysseyVS) {
            $ninjaDir = Join-Path $odysseyVS 'Common7/IDE/CommonExtensions/Microsoft/CMake/Ninja'
            if (Test-Path -LiteralPath $ninjaDir) { $odysseyPaths += $ninjaDir }
            if ($Client) {
                Import-Module (Join-Path $odysseyVS 'Common7/Tools/Microsoft.VisualStudio.DevShell.dll')
                Enter-VsDevShell -VsInstallPath $odysseyVS -SkipAutomaticLocation -DevCmdArguments '-arch=x64 -host_arch=x64' | Out-Null
            }
        } elseif ($Client) { throw 'Install the Visual Studio Desktop development with C++ workload.' }
    } elseif ($Client) { throw 'Visual Studio C++ Build Tools were not found.' }
}
if (Test-Path -LiteralPath (Join-Path $odysseyTools 'vcpkg/scripts/buildsystems/vcpkg.cmake')) {
    $env:VCPKG_ROOT = Join-Path $odysseyTools 'vcpkg'
}
$env:VCPKG_DISABLE_METRICS = '1'
$env:VCPKG_BINARY_SOURCES = "clear;files,$odysseyTools/vcpkg-cache,readwrite"
if (-not $env:VCPKG_MAX_CONCURRENCY) { $env:VCPKG_MAX_CONCURRENCY = '4' }
$env:PATH = ($odysseyPaths -join [IO.Path]::PathSeparator) + [IO.Path]::PathSeparator + $env:PATH
