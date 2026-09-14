[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Push-Location $repositoryRoot
try {
    Write-Output "timestamp=$(Get-Date -Format o)"
    go version
    go env GOOS GOARCH

    $env:ODYSSEY_D9_REALTIME = '1'
    go test -count=1 -v -run '^TestRealtimeTickCadenceUnder50RoomCombat$' ./server/internal/room
    if ($LASTEXITCODE -ne 0) { throw '50-room real-time gate failed.' }

    go test -count=1 -run 'Test(ManyRoomsKeepCombatStateIsolated|RoomInputRateDoesNotIncreaseMovementOrFireRate)$' ./server/internal/room
    if ($LASTEXITCODE -ne 0) { throw 'Room isolation or input-rate gate failed.' }

    go test -run '^$' -bench '^BenchmarkGameCore$' -benchmem -benchtime=1s -count=5 ./server/internal/game
    if ($LASTEXITCODE -ne 0) { throw 'Game-core microbenchmarks failed.' }

    $profileDirectory = Join-Path ([System.IO.Path]::GetTempPath()) "odyssey-d9-$PID"
    New-Item -ItemType Directory -Path $profileDirectory -Force | Out-Null
    $profilePath = Join-Path $profileDirectory 'collision.cpu'
    # `go test -cpuprofile` retains its test binary in the package invocation's
    # working directory even when profile output uses -outputdir.
    $profileBinary = Join-Path $repositoryRoot 'game.test.exe'
    go test -run '^$' -bench '^BenchmarkGameCore/Collision64x256$' -benchtime=3s -outputdir $profileDirectory -cpuprofile $profilePath ./server/internal/game
    if ($LASTEXITCODE -ne 0) { throw 'Collision CPU profile failed.' }
    go tool pprof -top -nodecount=15 $profilePath
    if ($LASTEXITCODE -ne 0) { throw 'Collision profile report failed.' }
} finally {
    foreach ($artifact in @($profilePath, $profileBinary)) {
        if ($artifact) { Remove-Item -LiteralPath $artifact -Force -ErrorAction SilentlyContinue }
    }
    if ($profileDirectory) { Remove-Item -LiteralPath $profileDirectory -Force -ErrorAction SilentlyContinue }
    Remove-Item Env:ODYSSEY_D9_REALTIME -ErrorAction SilentlyContinue
    Pop-Location
}
