#requires -Version 7.0
param([ValidateSet('up', 'down', 'status', 'config')][string]$Action = 'status')
$ErrorActionPreference = 'Stop'
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
Push-Location $root
try {
    switch ($Action) {
        'up' { & docker compose up -d --wait --wait-timeout 180 }
        'down' { & docker compose down }
        'status' { & docker compose ps }
        'config' { & docker compose config --quiet }
    }
    if ($LASTEXITCODE -ne 0) { throw "Docker Compose $Action failed." }
} finally { Pop-Location }
