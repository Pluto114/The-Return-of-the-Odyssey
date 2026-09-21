#requires -Version 7.0
[CmdletBinding()]
param(
    [switch]$CheckOnly,
    [ValidateRange(1, 2)][int]$Count = 2
)

$ErrorActionPreference = 'Stop'
$odysseyRoot = Split-Path $PSScriptRoot -Parent
$odysseyClient = Join-Path $odysseyRoot 'build/client-polish-release/client/odyssey_client.exe'
if (-not (Test-Path -LiteralPath $odysseyClient -PathType Leaf)) {
    throw "找不到新版客户端：$odysseyClient。请先按 client/README.md 构建。"
}
$odysseyClient = (Resolve-Path -LiteralPath $odysseyClient).Path
$odysseyBinary = [Text.Encoding]::UTF8.GetString([IO.File]::ReadAllBytes($odysseyClient))
foreach ($odysseyFeatureText in @('再玩一把', '弹匣已空', 'AI 导演')) {
    if (-not $odysseyBinary.Contains($odysseyFeatureText)) {
        throw ('这份客户端缺少最新功能 [{0}]：{1}。请重新构建。' -f $odysseyFeatureText, $odysseyClient)
    }
}

$odysseyOldClients = @(
    Get-Process -Name odyssey_client -ErrorAction SilentlyContinue |
        Where-Object {
            $_.Path -and $_.Path.StartsWith($odysseyRoot + '\', [StringComparison]::OrdinalIgnoreCase) -and
            -not $_.Path.Equals($odysseyClient, [StringComparison]::OrdinalIgnoreCase)
        }
)
$odysseyProblems = @()
if ($odysseyOldClients.Count -gt 0) {
    $odysseyDetails = ($odysseyOldClients | ForEach-Object { "PID $($_.Id): $($_.Path)" }) -join "`n"
    $odysseyProblems += "仍有旧客户端窗口，请先手动关闭：`n$odysseyDetails"
}

$odysseyServerSources = @(
    'server/cmd/gameserver/application.go',
    'server/internal/game/pickups.go',
    'server/internal/convert/snapshot.go',
    'proto/game.proto'
) | ForEach-Object { Get-Item -LiteralPath (Join-Path $odysseyRoot $_) }
$odysseyServerChanged = ($odysseyServerSources | Measure-Object -Property LastWriteTime -Maximum).Maximum
$odysseyServers = @(Get-Process -Name gameserver -ErrorAction SilentlyContinue)
if ($odysseyServers.Count -eq 0) {
    $odysseyProblems += '没有运行中的 gameserver；请先启动新版服务端，再双开客户端。'
}
$odysseyOldServers = @(
    $odysseyServers |
        Where-Object { $_.StartTime -lt $odysseyServerChanged }
)
if ($odysseyOldServers.Count -gt 0) {
    $odysseyIds = ($odysseyOldServers | ForEach-Object Id) -join ', '
    $odysseyProblems += "服务端进程（PID $odysseyIds）早于地图道具代码，请先在原终端按 Ctrl+C，再重新启动 gameserver。"
}
if ($odysseyProblems.Count -gt 0) {
    throw ($odysseyProblems -join "`n")
}

if ($CheckOnly) {
    Write-Output "检查通过：新版客户端 $odysseyClient；未发现旧客户端或旧服务端进程。"
    return
}

for ($odysseyIndex = 0; $odysseyIndex -lt $Count; ++$odysseyIndex) {
    Start-Process -FilePath $odysseyClient -WorkingDirectory (Split-Path $odysseyClient) -WindowStyle Normal
}
Write-Output "已打开 $Count 个动态远征客户端；默认十二关，R 换弹，F4 简化特效，最终关后可再玩一把。"
