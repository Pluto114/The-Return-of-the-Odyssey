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
        if ($proxyUri -and $proxyUri.Host -ne $testUri.Host) {
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
    $odysseyDockerBins = @(
        (Join-Path $env:LOCALAPPDATA 'Programs/DockerDesktop/resources/bin'),
        (Join-Path $env:ProgramFiles 'Docker/Docker/resources/bin')
    )
    foreach ($odysseyDockerBin in $odysseyDockerBins) {
        if (Test-Path -LiteralPath (Join-Path $odysseyDockerBin 'docker.exe')) {
            $odysseyPaths += $odysseyDockerBin
            break
        }
    }
    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
    if (Test-Path -LiteralPath $vswhere) {
        $odysseyVS = & $vswhere -utf8 -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
        if ($odysseyVS) {
            $ninjaDir = Join-Path $odysseyVS 'Common7/IDE/CommonExtensions/Microsoft/CMake/Ninja'
            if (Test-Path -LiteralPath $ninjaDir) { $odysseyPaths += $ninjaDir }
            if ($Client) {
                $odysseyDevShellRoot = $odysseyVS
                if ($odysseyVS -match '[^\x00-\x7F]') {
                    $odysseyDevShellRoot = Join-Path $odysseyTools 'vs-install'
                    if (-not (Test-Path -LiteralPath $odysseyDevShellRoot)) {
                        New-Item -ItemType Junction -Path $odysseyDevShellRoot -Target $odysseyVS | Out-Null
                    }
                }
                $odysseyDevCmd = Join-Path $odysseyDevShellRoot 'Common7/Tools/VsDevCmd.bat'
                $odysseyDevCmdArgs = 'call "' + $odysseyDevCmd + '" -arch=x64 -host_arch=x64 -no_logo && set'
                $odysseyDevEnvironment = & $env:ComSpec /d /s /c $odysseyDevCmdArgs
                if ($LASTEXITCODE -ne 0) { throw 'Visual Studio developer environment initialization failed.' }
                foreach ($odysseyEntry in $odysseyDevEnvironment) {
                    if ($odysseyEntry -cmatch '^([^=]+)=(.*)$' -and -not $Matches[1].Equals('PATH', [StringComparison]::OrdinalIgnoreCase)) {
                        Set-Item -Path "Env:$($Matches[1])" -Value $Matches[2]
                    }
                }
                $odysseyDevPath = $odysseyDevEnvironment | Where-Object { $_ -match '(?i)^PATH=.*\\VC\\Tools\\MSVC\\' } | Select-Object -First 1
                if (-not $odysseyDevPath) { throw 'Visual Studio developer environment did not provide PATH.' }
                $env:PATH = $odysseyDevPath.Substring(5)
                $env:VCPKG_VISUAL_STUDIO_PATH = $odysseyDevShellRoot
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
$odysseyFinalPath = ($odysseyPaths -join [IO.Path]::PathSeparator) + [IO.Path]::PathSeparator + $env:PATH
# Some Windows hosts expose both Path and PATH to PowerShell. Native child
# processes may select the stale entry, so collapse them before launching tools.
Remove-Item -LiteralPath Env:Path -ErrorAction SilentlyContinue
$env:PATH = $odysseyFinalPath
if ($Client -and $IsWindows) {
    $odysseyCompiler = Get-Command cl.exe -ErrorAction Stop
    $env:CC = $odysseyCompiler.Source
    $env:CXX = $odysseyCompiler.Source
}
