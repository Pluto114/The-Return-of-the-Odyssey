#requires -Version 7.0
$ErrorActionPreference = 'Stop'
$odysseyProject = Split-Path $PSScriptRoot -Parent
$odysseyBuild = Join-Path $odysseyProject 'build/client-polish-release/client'
$odysseyPackage = Join-Path $odysseyProject 'output/odyssey-client-dynamic-expedition-windows-x64'
$odysseyZip = "$odysseyPackage.zip"
if ((Test-Path -LiteralPath $odysseyPackage) -or (Test-Path -LiteralPath $odysseyZip)) {
    throw '发布目录或压缩包已存在；请保留旧包并更换输出版本名称，不自动覆盖。'
}
$odysseyFiles = @('odyssey_client.exe','raylib.dll','glfw3.dll','libprotobuf.dll','abseil_dll.dll','equipment.tsv','equipment.zh-CN.tsv')
foreach ($odysseyFile in $odysseyFiles) {
    if (-not (Test-Path -LiteralPath (Join-Path $odysseyBuild $odysseyFile))) { throw "构建产物缺失：$odysseyFile" }
}
New-Item -ItemType Directory -Path $odysseyPackage | Out-Null
foreach ($odysseyFile in $odysseyFiles) {
    Copy-Item -LiteralPath (Join-Path $odysseyBuild $odysseyFile) -Destination $odysseyPackage
}
foreach ($odysseyFile in @('start-lan.cmd','start-radmin.cmd','start-custom.ps1','README.txt')) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "playtest-client/$odysseyFile") -Destination $odysseyPackage
}
Copy-Item -LiteralPath (Join-Path $odysseyProject 'docs/PLAYER-GUIDE.md') -Destination $odysseyPackage
Compress-Archive -LiteralPath $odysseyPackage -DestinationPath $odysseyZip
Get-FileHash -LiteralPath $odysseyZip -Algorithm SHA256
