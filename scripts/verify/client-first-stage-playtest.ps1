#requires -Version 7.0
<#
.SYNOPSIS
Runs the two-client first-stage playtest (Role C) and collects the evidence logs.

.DESCRIPTION
Starts the gameserver with the project environment, waits until it is listening,
launches two real clients against it with their stdout captured to files, and prints
the checklist from docs/verification/phase2-c/FIRST-STAGE-PLAYTEST.md.

The script automates the mechanical part (server up, clients up, logs on disk). The
gameplay observations in the checklist are manual by nature: this is a GUI client,
so its behaviour is verified by watching it and reading the captured stdout, which is
the evidence chain the project requires.

Nothing here replaces the acceptance run: reward/ready routing, resume and the three
stage run still depend on the server-side work listed in the playtest document.

.EXAMPLE
pwsh -File scripts/verify/client-first-stage-playtest.ps1
pwsh -File scripts/verify/client-first-stage-playtest.ps1 -ServerHost 192.168.1.20 -NoServer
#>
[CmdletBinding()]
param(
    [string]$ServerHost = '127.0.0.1',
    [int]$ServerPort = 7777,
    # Skip starting the server (use when it is already running, e.g. on another machine).
    [switch]$NoServer,
    # Where logs and screenshots for this run are collected.
    [string]$EvidenceRoot = 'docs/verification/phase2-c',
    [int]$ServerWaitSeconds = 30
)

$ErrorActionPreference = 'Stop'
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
. (Join-Path $root 'scripts/env.ps1') -Client:$false

$clientExe = Join-Path $root 'build/client-windows/client/odyssey_client.exe'
if (-not (Test-Path -LiteralPath $clientExe)) {
    throw "Client not built: $clientExe`nRun: pwsh -File scripts/build/build.ps1 -Target client"
}

# The upstream merge added an indirect Go dependency (filippo.io/edwards25519) that
# is not in a typical local module cache yet; the server cannot compile without it,
# so fail early with the exact fix instead of after a confusing build error.
if (-not $NoServer) {
    $moduleCache = (& go env GOMODCACHE) 2>$null
    if ($moduleCache -and -not (Test-Path (Join-Path $moduleCache 'filippo.io'))) {
        Write-Host 'Go modules look incomplete (filippo.io/edwards25519 missing).' -ForegroundColor Yellow
        Write-Host 'Run once, with network access:' -ForegroundColor Yellow
        Write-Host '  go env -w GOPROXY=https://goproxy.cn,direct' -ForegroundColor Yellow
        Write-Host '  go env -w GOSUMDB=sum.golang.google.cn' -ForegroundColor Yellow
        Write-Host '  go -C server mod download all ; go -C bot mod download all' -ForegroundColor Yellow
        Write-Host '  (Go ignores the Windows system proxy, so the default proxy.golang.org times out.)' -ForegroundColor Yellow
    }
}

$stamp = Get-Date -Format 'yyyy-MM-dd-HHmm'
$evidenceDir = Join-Path $root (Join-Path $EvidenceRoot "first-stage-$stamp")
New-Item -ItemType Directory -Force -Path $evidenceDir | Out-Null
$serverLog = Join-Path $evidenceDir 'server.log'
$serverErrLog = Join-Path $evidenceDir 'server.err.log'
$endpoint = "${ServerHost}:${ServerPort}"

Write-Host "Evidence directory: $evidenceDir" -ForegroundColor Cyan

# Refuse to launch clients into a port nobody is listening on: the first thing a
# playtest should not have to debug is whether the server actually came up.
function Test-Endpoint {
    param([string]$Host_, [int]$Port)
    try {
        $client = [System.Net.Sockets.TcpClient]::new()
        $task = $client.ConnectAsync($Host_, $Port)
        $ok = $task.Wait(400) -and $client.Connected
        $client.Dispose()
        return $ok
    } catch {
        return $false
    }
}

$serverProcess = $null
if (-not $NoServer) {
    if (Test-Endpoint -Host_ $ServerHost -Port $ServerPort) {
        throw "$endpoint is already in use. Stop the existing server or pass -NoServer."
    }
    Write-Host "Starting gameserver on $endpoint (log: $serverLog)..." -ForegroundColor Yellow
    $serverProcess = Start-Process -FilePath 'go' `
        -ArgumentList @('run', './server/cmd/gameserver') `
        -WorkingDirectory $root -PassThru -NoNewWindow `
        -RedirectStandardOutput $serverLog -RedirectStandardError $serverErrLog

    $deadline = (Get-Date).AddSeconds($ServerWaitSeconds)
    $listening = $false
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 400
        if (Test-Endpoint -Host_ $ServerHost -Port $ServerPort) { $listening = $true; break }
        if ($serverProcess.HasExited) { break }
    }
    if (-not $listening) {
        Write-Host "Server did not start listening within $ServerWaitSeconds s." -ForegroundColor Red
        Write-Host "--- server.log tail ---"
        if (Test-Path $serverLog) { Get-Content $serverLog -Tail 25 }
        if (Test-Path $serverErrLog) { Get-Content $serverErrLog -Tail 25 }
        if (-not $serverProcess.HasExited) { Stop-Process -Id $serverProcess.Id -Force }
        exit 1
    }
    Write-Host "Server is listening." -ForegroundColor Green
} else {
    Write-Host "Assuming a server is already listening on $endpoint (-NoServer)." -ForegroundColor Yellow
    if (-not (Test-Endpoint -Host_ $ServerHost -Port $ServerPort)) {
        Write-Host "WARN: nothing is listening on $endpoint yet; clients will retry with backoff." -ForegroundColor Yellow
    }
}

$clients = @()
foreach ($name in @('a', 'b')) {
    $out = Join-Path $evidenceDir "client-$name.log"
    $err = Join-Path $evidenceDir "client-$name.err.log"
    Write-Host "Launching client $name (log: $out)..." -ForegroundColor Yellow
    $clients += Start-Process -FilePath $clientExe `
        -ArgumentList @('--server', $endpoint) `
        -WorkingDirectory $root -PassThru `
        -RedirectStandardOutput $out -RedirectStandardError $err
}

@"
================================================================================
Two-client first-stage playtest is running.

  endpoint : $endpoint
  clients  : $($clients.Id -join ', ')   (close the windows to end their side)
  logs     : $evidenceDir

Checklist (details: docs/verification/phase2-c/FIRST-STAGE-PLAYTEST.md):
  1  startup lines: server endpoint / viewport / asset root / equipment.tsv
  2  net state -> connecting -> connected (no red banner)
  3  lobby card: LOGGING IN -> MATCHMAKING
  4  match ready room=<R> teammates=1   (BOTH clients must show the same <R>)
  5  first world snapshot tick=<T> + input enabled after first snapshot
  6  STAGE STARTED (A1): 'main: stage started index=1', HUD 'STAGE 01  HOSTILES n',
     F1 shows gate=open
  7  WASD moves, crosshair follows the mouse, F1 seq keeps increasing
  8  combat: damage/projectile lines, '-N' floaters, health bars, hit flashes
  9  taking a hit: health ghost shakes, red arc points at the source
 10  stage cleared -> centred STAGE CLEAR card
 11  reward panel: keys 1-3 -> 'reward choice sent' -> 'reward applied ... ok=1'
 12  ENTER after the reward resolves -> 'next stage ready sent ... state=preparing'
 13  kill the server -> LINK LOST banner, retry attempts, 'press R to reconnect'
 14  steady 'main: frame <N> ...' lines, responsive window, no ghosting

Expected gaps on the current main (not client defects, see the playtest doc §5):
  * reward/ready routing and Director are not integrated on main yet
  * resume is disabled by default (Redis), potion needs A's 4462d21 in main
  * equipment slots show SPD/DMG until the snapshot carries equipment ids (A3)

When finished: close both client windows, then press Enter here to stop the server
(if this script started it).
================================================================================
"@ | Write-Host

Read-Host 'Press Enter after the clients are closed'

if ($serverProcess -and -not $serverProcess.HasExited) {
    Write-Host 'Stopping the gameserver...' -ForegroundColor Yellow
    Stop-Process -Id $serverProcess.Id -Force
    # `go run` spawns the compiled binary as a child; make sure nothing is left on
    # the port so the next run does not fail the "already in use" check.
    Start-Sleep -Milliseconds 800
    if (Test-Endpoint -Host_ $ServerHost -Port $ServerPort) {
        Write-Host "WARN: something is still listening on $endpoint (child process?)." -ForegroundColor Yellow
    }
}

Write-Host "Done. Evidence collected in $evidenceDir" -ForegroundColor Green
Write-Host 'Fill in the checklist table and register the run in the phase2-c README.' -ForegroundColor Green
