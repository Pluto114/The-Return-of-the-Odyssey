#requires -Version 7.0
[CmdletBinding()]
param(
    [ValidateRange(3, 1000)][int]$Stages = 12,
    [ValidateRange(1, 65535)][int]$Port = 7777
)
$ErrorActionPreference = 'Stop'
$odysseyProject = Split-Path $PSScriptRoot -Parent
$odysseyServer = Join-Path $odysseyProject 'build/polish/gameserver.exe'
if (-not (Test-Path -LiteralPath $odysseyServer -PathType Leaf)) {
    throw '请先在项目根目录执行 go build -o build/polish/gameserver.exe ./server/cmd/gameserver'
}
$odysseyListener = @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
if ($odysseyListener.Count -gt 0) {
    throw "端口 $Port 已由 PID $($odysseyListener.OwningProcess -join ',') 使用。请先结束旧局并停止旧服务端，本脚本不会中断正在运行的对局。"
}
Push-Location $odysseyProject
$odysseyOldTCP = $env:ODYSSEY_TCP_ADDR
$odysseyOldStages = $env:ODYSSEY_STAGE_LIMIT
try {
    $env:ODYSSEY_TCP_ADDR = "0.0.0.0:$Port"
    $env:ODYSSEY_STAGE_LIMIT = [string]$Stages
    Write-Host "启动动态远征服务端：$Stages 关，TCP $Port；请保持本终端打开。"
    Write-Host '若远程连接失败，请检查是否为此新版 gameserver.exe 放行了防火墙。'
    & $odysseyServer -env server/configs/.env
} finally {
    $env:ODYSSEY_TCP_ADDR = $odysseyOldTCP
    $env:ODYSSEY_STAGE_LIMIT = $odysseyOldStages
    Pop-Location
}
