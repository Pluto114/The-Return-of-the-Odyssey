#requires -Version 7.0
<#
.SYNOPSIS
Runs the Release UI performance acceptance measurement (Role C) and writes the report.

.DESCRIPTION
Measures the whole-frame CPU time of the same battle scene twice per round - once with
the UI layer enabled and once with --no-ui - and checks the plan's budget:

    mean(UI on) - mean(UI off) <= 1.5 ms

Plan and rationale: docs/verification/phase2-c/RELEASE-UI-PERF-PLAN.md

This script automates the mechanical part: arming the capture, launching the two runs, the
counterpart bot, parsing the CSV summaries and writing report.md/hardware.md. It deliberately
does NOT start the gameserver: the plan requires an unchanged scene across the two runs of a
round, so the server must stay up between them (start it yourself, or pass -StartServer).

The counterpart bot IS started here, one per run, because the server pairs exactly two
players (lobby.NewMatchmaker(2)) while the bot waits at most 5 seconds per expected message:
a lone bot dies with a match-phase read timeout before any client arrives. The measured
client has no such cap, so the order is client first, bot a moment later. Pass -NoBot to
bring your own second player instead.

The client must be a Release build. Debug numbers are not acceptance evidence and the
script refuses to produce a verdict from them unless -AllowNonRelease is given.

.EXAMPLE
pwsh -File scripts/verify/client-release-ui-perf.ps1
pwsh -File scripts/verify/client-release-ui-perf.ps1 -Rounds 3 -Frames 600
pwsh -File scripts/verify/client-release-ui-perf.ps1 -NoServer -ServerHost 192.168.1.20
pwsh -File scripts/verify/client-release-ui-perf.ps1 -NoBot      # bring your own counterpart
#>
[CmdletBinding()]
param(
    [string]$ClientExe = 'build/client-windows-release/client/odyssey_client.exe',
    [string]$ServerHost = '127.0.0.1',
    [int]$ServerPort = 7777,
    # Frames counted per run; the plan's floor is 600.
    [int]$Frames = 600,
    # UI-on / UI-off pairs. Three rounds let the script judge environment noise.
    [int]$Rounds = 3,
    [double]$BudgetMs = 1.5,
    [string]$EvidenceRoot = 'docs/verification/phase2-c',
    # Start the gameserver for the whole measurement instead of requiring one.
    [switch]$StartServer,
    # Do not require a server on this host (e.g. it runs on another machine, or the
    # probe is blocked). The scene still has to be live, or no run will ever finish.
    [switch]$NoServer,
    [int]$ServerWaitSeconds = 30,
    # Bring your own second player instead of letting the script start one per run.
    [switch]$NoBot,
    # Server Prometheus port, used to read odyssey_match_queue_players so the counterpart bot
    # starts exactly when the client has queued (0 disables the metrics signal).
    [int]$MetricsPort = 19091,
    # Allow a non-Release binary to run (smoke test only; the report is marked invalid).
    [switch]$AllowNonRelease,
    # Do not stop on the first failed run; keep collecting what is collectable.
    [switch]$KeepGoing
)

$ErrorActionPreference = 'Stop'
# The CSV is written with '.' as the decimal separator. Parsing and formatting through
# the invariant culture keeps a comma-decimal locale from turning 0.4123 into 4123 and
# silently flipping the verdict.
$inv = [System.Globalization.CultureInfo]::InvariantCulture
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$clientExe = if ([System.IO.Path]::IsPathRooted($ClientExe)) { $ClientExe } else { Join-Path $root $ClientExe }

if (-not (Test-Path -LiteralPath $clientExe)) {
    throw @"
Client not built: $clientExe

Release build (until the shared preset lands, use the plan's interim configure):
  docs/verification/phase2-c/RELEASE-UI-PERF-PLAN.md section 2.1 / 2.2
"@
}

# A Debug binary must not silently produce acceptance numbers.
$isRelease = $clientExe -match 'release'
if (-not $isRelease -and -not $AllowNonRelease) {
    throw @"
Refusing to measure '$clientExe': the path does not look like a Release build.

The criterion is Release-only. Build the Release target first, or pass
-AllowNonRelease to run a smoke test whose report is explicitly marked invalid.
"@
}

function Test-Endpoint {
    param([string]$Host_, [int]$Port)
    try {
        $probe = [System.Net.Sockets.TcpClient]::new()
        $task = $probe.ConnectAsync($Host_, $Port)
        $ok = $task.Wait(600) -and $probe.Connected
        $probe.Dispose()
        return $ok
    } catch {
        return $false
    }
}

$endpoint = "${ServerHost}:${ServerPort}"
$stamp = Get-Date -Format 'yyyy-MM-dd-HHmm'
$evidenceDir = Join-Path $root (Join-Path $EvidenceRoot "release-ui-perf-$stamp")
New-Item -ItemType Directory -Force -Path $evidenceDir | Out-Null
Write-Host "Evidence directory: $evidenceDir" -ForegroundColor Cyan

# The measurement is only meaningful on a live battle scene: without a server the client
# never reaches trigger=playing and no run would ever complete.
$serverProcess = $null
if ($NoServer) {
    Write-Host "Assuming the live scene's server is reachable at $endpoint (-NoServer)." -ForegroundColor Yellow
    Write-Host 'If the client never prints "perf capture started", the scene is not live and no data is produced.' -ForegroundColor Yellow
} elseif ($StartServer) {
    if (Test-Endpoint -Host_ $ServerHost -Port $ServerPort) {
        throw "$endpoint is already in use. Stop it, or drop -StartServer to measure against it."
    }
    . (Join-Path $root 'scripts/env.ps1') -Client:$false
    $moduleCache = (& go env GOMODCACHE) 2>$null
    if ($moduleCache -and -not (Test-Path (Join-Path $moduleCache 'filippo.io'))) {
        Write-Host 'Go modules look incomplete. Run once with network: cd server; go mod download all' -ForegroundColor Yellow
    }
    $serverLog = Join-Path $evidenceDir 'server.log'
    $serverErr = Join-Path $evidenceDir 'server.err.log'
    Write-Host "Starting gameserver on $endpoint..." -ForegroundColor Yellow
    $serverProcess = Start-Process -FilePath 'go' `
        -ArgumentList @('run', './server/cmd/gameserver') `
        -WorkingDirectory $root -PassThru -NoNewWindow `
        -RedirectStandardOutput $serverLog -RedirectStandardError $serverErr
    $deadline = (Get-Date).AddSeconds($ServerWaitSeconds)
    $listening = $false
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 400
        if (Test-Endpoint -Host_ $ServerHost -Port $ServerPort) { $listening = $true; break }
        if ($serverProcess.HasExited) { break }
    }
    if (-not $listening) {
        if (-not $serverProcess.HasExited) { Stop-Process -Id $serverProcess.Id -Force }
        Write-Host "Server did not start listening within $ServerWaitSeconds s." -ForegroundColor Red
        if (Test-Path $serverLog) { Get-Content $serverLog -Tail 20 }
        exit 1
    }
    Write-Host 'Server is listening.' -ForegroundColor Green
    if ($NoBot) {
        Write-Host 'Reminder (-NoBot): a second player must already be queued, or no stage will start.' -ForegroundColor Yellow
    } else {
        Write-Host 'The script starts one loadbot per run, as soon as the client reports matching.' -ForegroundColor Green
    }
} elseif (-not (Test-Endpoint -Host_ $ServerHost -Port $ServerPort)) {
    throw @"
Nothing is listening on $endpoint.

The measurement needs a live battle scene. Start the server first, or pass -StartServer to
have this script start it. The counterpart bot is started by this script unless -NoBot.
"@
}

# The scene must be identical for both runs, so no other client of this build may be
# running: it would compete for CPU and pollute the very numbers being compared.
$stray = Get-Process -Name 'odyssey_client' -ErrorAction SilentlyContinue
if ($stray) {
    Write-Host "WARNING: $($stray.Count) odyssey_client process(es) already running (pids: $($stray.Id -join ', '))." -ForegroundColor Yellow
    Write-Host 'Close them: a second GUI client steals CPU and invalidates the comparison.' -ForegroundColor Yellow
    if (-not $KeepGoing) { throw 'Stray client processes would invalidate the measurement.' }
}

$env:ODYSSEY_PERF_FRAMES = "$Frames"
$env:ODYSSEY_PERF_TRIGGER = 'playing'

# Waits until the measured client has actually queued for a match. The counterpart bot dies
# after 5 seconds of waiting for a message, so it has to start when the client is already
# queued - a client that is merely launched is not enough, and its startup time varies with
# the machine's load.
#
# Two signals, either is enough:
#   * the client's own "match request sent (queued)" line (present in current builds), and
#   * the server's odyssey_match_queue_players gauge (works with any client build, so the
#     measurement does not depend on the client binary being the newest one).
function Get-MatchQueuePlayers {
    if ($MetricsPort -le 0) {
        return -1
    }
    try {
        $text = (Invoke-WebRequest -Uri "http://${ServerHost}:${MetricsPort}/metrics" `
                    -TimeoutSec 3 -UseBasicParsing).Content
    } catch {
        return -1
    }
    $found = [regex]::Match($text, '(?m)^odyssey_match_queue_players\s+(\d+)')
    if ($found.Success) {
        return [int]$found.Groups[1].Value
    }
    return -1
}

function Wait-ForClientQueued {
    param($Client, [string]$Log, [int]$TimeoutMs = 60000)

    $deadline = (Get-Date).AddMilliseconds($TimeoutMs)
    while ((Get-Date) -lt $deadline) {
        if ($Client.HasExited) {
            return $false  # the client gave up first; no point starting a counterpart
        }
        if (Test-Path -LiteralPath $Log) {
            if (Select-String -LiteralPath $Log -Pattern 'match request sent' -Quiet -ErrorAction SilentlyContinue) {
                return $true
            }
        }
        if ((Get-MatchQueuePlayers) -ge 1) {
            return $true
        }
        Start-Sleep -Milliseconds 200
    }
    return $false
}

# Starts one counterpart bot for a measured run and returns its process, or $null.
#
# Why the bot has to start AFTER the client has queued: the server pairs exactly two players
# (lobby.NewMatchmaker(2) in the gameserver), and the bot waits at most 5 seconds for each
# expected message (readUntil in bot/internal/client/worker.go). A lone bot therefore dies
# with a match-phase read timeout before any client shows up, which is the failure this
# automation removes. The measured client has no such cap - it sits in matchmaking - so the
# client queues first and the bot second, and the pair forms immediately.
function Start-BotCounterpart {
    param([string]$Label)

    $botLog = Join-Path $evidenceDir "bot-$Label.log"
    $botArgs = @('run', './bot/cmd/loadbot', '-mode', 'functional', '-clients', '1',
                 '-stages', '1', '-duration', '5m', '-ramp', '0s', '-use-potion=false',
                 '-resume=false', '-server', $endpoint)
    $process = Start-Process -FilePath 'go' -ArgumentList $botArgs `
        -WorkingDirectory $root -PassThru -NoNewWindow `
        -RedirectStandardOutput $botLog `
        -RedirectStandardError (Join-Path $evidenceDir "bot-$Label.err.log")
    Write-Host " (bot $($process.Id))" -ForegroundColor DarkGray -NoNewline
    return [pscustomobject]@{ Process = $process; Log = $botLog }
}

# Kills the `go run` wrapper AND the bot binary it spawned; stopping only the wrapper
# would leave the bot holding a seat in the room and poison the next run.
function Stop-BotCounterpart {
    param($Bot)

    if (-not $Bot) {
        return
    }
    if (-not $Bot.Process.HasExited) {
        & taskkill /F /T /PID $Bot.Process.Id 2>&1 | Out-Null
    }
    # Give the server a moment to see the seat free up before the next run matches.
    Start-Sleep -Milliseconds 700
}

# One run. Returns the means parsed out of the CSV, or $null when the run produced none.
function Invoke-PerfRun {
    param([string]$Label, [string[]]$ExtraArgs)

    $csv = Join-Path $evidenceDir "$Label.csv"
    $log = Join-Path $evidenceDir "$Label.log"
    $env:ODYSSEY_PERF_LOG = $csv
    Write-Host "  run $Label ..." -ForegroundColor Yellow -NoNewline

    $arguments = @('--server', $endpoint) + $ExtraArgs
    $process = Start-Process -FilePath $clientExe -ArgumentList $arguments `
        -WorkingDirectory $root -PassThru -NoNewWindow `
        -RedirectStandardOutput $log -RedirectStandardError (Join-Path $evidenceDir "$Label.err.log")

    # Client first, counterpart second, and only once the client has actually queued.
    $bot = $null
    if (-not $NoBot) {
        $queued = Wait-ForClientQueued -Client $process -Log $log
        if (-not $queued -and -not $process.HasExited) {
            Write-Host ' (client never reported matching; starting the bot anyway)' -ForegroundColor DarkYellow -NoNewline
        }
        if (-not $process.HasExited) {
            $bot = Start-BotCounterpart -Label $Label
        }
    }

    if (-not $process.WaitForExit(600000)) {
        Stop-Process -Id $process.Id -Force
        Stop-BotCounterpart -Bot $bot
        Write-Host ' TIMEOUT (10 min); no battle scene was reached?' -ForegroundColor Red
        return $null
    }
    Stop-BotCounterpart -Bot $bot

    if (-not (Test-Path $csv)) {
        Write-Host " NO CSV (exit $($process.ExitCode)) - see $log" -ForegroundColor Red
        $tail = Select-String -Path $log -Pattern 'perf capture' -ErrorAction SilentlyContinue
        foreach ($line in $tail) { Write-Host "    $($line.Line)" -ForegroundColor DarkGray }
        return $null
    }
    $summary = Get-Content $csv | Where-Object { $_ -like '# *' } | Select-Object -Index 1
    if (-not $summary) {
        Write-Host " CSV has no summary line ($csv)" -ForegroundColor Red
        return $null
    }
    $parts = $summary.Split(',')
    $result = [pscustomobject]@{
        Label  = $Label
        Frames = [int]$parts[0].TrimStart('# ')
        Mean   = [double]::Parse($parts[1], $inv)
        Median = [double]::Parse($parts[2], $inv)
        P95    = [double]::Parse($parts[3], $inv)
        Max    = [double]::Parse($parts[4], $inv)
        Min    = [double]::Parse($parts[5], $inv)
    }
    Write-Host ([string]::Format($inv,
        ' mean={0:F4}ms median={1:F4} p95={2:F4} max={3:F4} frames={4}',
        $result.Mean, $result.Median, $result.P95, $result.Max, $result.Frames))
    return $result
}

$rows = @()
for ($round = 1; $round -le $Rounds; ++$round) {
    Write-Host "Round $round of $Rounds" -ForegroundColor Cyan
    $on = Invoke-PerfRun -Label "ui-on-$round" -ExtraArgs @()
    $off = Invoke-PerfRun -Label "ui-off-$round" -ExtraArgs @('--no-ui')
    if ($on -and $off) {
        $rows += [pscustomobject]@{
            Round = $round
            On    = $on
            Off   = $off
            Delta = $on.Mean - $off.Mean
        }
    } elseif (-not $KeepGoing) {
        Write-Host 'Round produced no usable data; stopping. Re-run once the scene is live.' -ForegroundColor Red
        break
    }
}

if ($serverProcess -and -not $serverProcess.HasExited) {
    Write-Host 'Stopping the gameserver this script started...' -ForegroundColor Yellow
    Stop-Process -Id $serverProcess.Id -Force
}

# ---------------------------------------------------------------- hardware template
$hardwarePath = Join-Path $evidenceDir 'hardware.md'
if (-not (Test-Path $hardwarePath)) {
    @'
# Test machine and environment

Fill every row: the acceptance criterion is only reproducible with this attached.

| Item | Value |
| --- | --- |
| CPU | |
| Memory | |
| GPU / driver | |
| Storage | |
| OS build | |
| Display resolution / scaling | |
| Client window / viewport scale | |
| Power mode | |
| Background load | |
| Build commit | |
| Build type / compiler | |

Notes:
'@ | Set-Content -LiteralPath $hardwarePath -Encoding utf8
    Write-Host "Wrote the hardware template: $hardwarePath (fill it in)" -ForegroundColor Yellow
}

# ---------------------------------------------------------------------- report
$deltas = @($rows | ForEach-Object { $_.Delta })
$validRows = @($rows | Where-Object { $_.On.Frames -ge 600 -and $_.Off.Frames -ge 600 })
$verdict = 'INVALID - no usable rounds'
$deltaMedian = $null
$noise = $null
if ($validRows.Count -gt 0) {
    $validDeltas = @($validRows | ForEach-Object { $_.Delta } | Sort-Object)
    $deltaMedian = $validDeltas[[int][math]::Floor($validDeltas.Count / 2)]
    $noise = $validDeltas[-1] - $validDeltas[0]
    $deltaText = $deltaMedian.ToString('F4', $inv)
    if (-not $isRelease) {
        $verdict = "INVALID - not a Release build (delta median $deltaText ms)"
    } elseif ($deltaMedian -le $BudgetMs) {
        $verdict = "PASS - UI per-frame delta $deltaText ms <= $BudgetMs ms"
    } else {
        $verdict = "FAIL - UI per-frame delta $deltaText ms > $BudgetMs ms"
    }
    if ($null -ne $noise -and $noise -gt ($BudgetMs * 0.3)) {
        $verdict += " | UNSTABLE - round-to-round spread $($noise.ToString('F4', $inv)) ms > 30% of budget; re-measure"
    }
}

$report = Join-Path $evidenceDir 'report.md'
$lines = @()
$lines += '# Release UI performance acceptance run'
$lines += ''
$lines += "Date: $(Get-Date -Format 'yyyy-MM-dd HH:mm')  "
$lines += "Endpoint: $endpoint  "
$lines += "Build: ``$clientExe``  "
$lines += "Frames per run: $Frames (plan floor 600)  "
$lines += "Budget: mean(UI on) - mean(UI off) <= $BudgetMs ms  "
$lines += "Script: scripts/verify/client-release-ui-perf.ps1"
$lines += ''
$lines += '## Verdict'
$lines += ''
$lines += "**$verdict**"
$lines += ''
$lines += '## Per-round results'
$lines += ''
$lines += '| Round | UI | frames | mean (ms) | median | p95 | max (ms) |'
$lines += '| --- | --- | --- | --- | --- | --- | --- |'
foreach ($row in $rows) {
    $lines += [string]::Format($inv, '| {0} | on | {1} | {2:F4} | {3:F4} | {4:F4} | {5:F4} |',
        $row.Round, $row.On.Frames, $row.On.Mean, $row.On.Median, $row.On.P95, $row.On.Max)
    $lines += [string]::Format($inv, '| {0} | off | {1} | {2:F4} | {3:F4} | {4:F4} | {5:F4} |',
        $row.Round, $row.Off.Frames, $row.Off.Mean, $row.Off.Median, $row.Off.P95, $row.Off.Max)
    $lines += [string]::Format($inv, '| {0} | delta | | **{1:F4}** | | | {2:F4} |',
        $row.Round, $row.Delta, ($row.On.Max - $row.Off.Max))
}
$lines += ''
if ($null -ne $deltaMedian) {
    $lines += "Median delta over valid rounds: **$($deltaMedian.ToString('F4', $inv)) ms** (budget $BudgetMs ms)  "
    $lines += "Round-to-round spread: $($noise.ToString('F4', $inv)) ms  "
}
$lines += ''
$lines += '## Scene and environment'
$lines += ''
$lines += '- Stage index / result (fill in from the logs): '
$lines += '- Second player (bot command line): '
$lines += '- Window size and viewport scale (from the startup lines in ui-on-1.log): '
$lines += '- Hardware: see hardware.md'
$lines += ''
$lines += '## Evidence'
$lines += ''
$lines += '| File | Contents |'
$lines += '| --- | --- |'
$lines += '| ui-on-N.csv / ui-off-N.csv | Per-frame CPU times plus the summary line |'
$lines += '| ui-on-N.log / ui-off-N.log | Client stdout: capture start line, summary line, startup lines |'
$lines += '| bot-ui-on-N.log | Counterpart bot report (JSON) for that run, when the script started it |'
$lines += '| hardware.md | Test machine template (must be filled in) |'
$lines += '| server.log | Only when -StartServer was used |'
$lines += ''
$lines += "Counterpart: $(if ($NoBot) { 'provided externally (-NoBot)' } else { 'one loadbot per run, started once the client reports matching' })  "
$lines += "Frames per run: $Frames; rounds: $Rounds; budget: $BudgetMs ms"
$lines += ''
$lines += 'Raw data is authoritative: this report is a summary of it.'
$lines | Set-Content -LiteralPath $report -Encoding utf8

Write-Host ''
Write-Host "Report: $report" -ForegroundColor Green
Write-Host "Verdict: $verdict" -ForegroundColor $(if ($verdict -like 'PASS*') { 'Green' } else { 'Yellow' })
Write-Host "Now fill in hardware.md and the scene rows in report.md, then register the run in the phase2-c README." -ForegroundColor Green

if ($verdict -like 'PASS*') { exit 0 } else { exit 1 }
