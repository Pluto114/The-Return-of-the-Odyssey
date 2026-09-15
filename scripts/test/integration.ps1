#requires -Version 7.0
param([ValidateSet('persistence')][string]$Target = 'persistence')
$ErrorActionPreference = 'Stop'
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
. (Join-Path $root 'scripts/env.ps1')

function Get-DotEnvValue([string]$Name) {
    $envFile = Join-Path $root '.env'
    $entry = Select-String -LiteralPath $envFile -Pattern "^$([Regex]::Escape($Name))=" | Select-Object -First 1
    if (-not $entry) { throw "$Name is missing from $envFile." }
    return $entry.Line.Substring($entry.Line.IndexOf('=') + 1).Trim()
}

& (Join-Path $root 'deploy/scripts/infra.ps1') -Action up
if ($LASTEXITCODE -ne 0) { throw 'Docker infrastructure startup failed.' }

switch ($Target) {
    'persistence' {
        $env:ODYSSEY_REDIS_ADDR = "127.0.0.1:$(Get-DotEnvValue 'REDIS_PORT')"
        $env:ODYSSEY_REDIS_PASSWORD = Get-DotEnvValue 'REDIS_PASSWORD'
        $env:ODYSSEY_REDIS_INTEGRATION = '1'
		$mysqlPort = Get-DotEnvValue 'MYSQL_PORT'
		$mysqlDatabase = Get-DotEnvValue 'MYSQL_DATABASE'
		$mysqlUser = Get-DotEnvValue 'MYSQL_USER'
		$mysqlPassword = Get-DotEnvValue 'MYSQL_PASSWORD'
		$env:ODYSSEY_MYSQL_DSN = "${mysqlUser}:${mysqlPassword}@tcp(127.0.0.1:${mysqlPort})/${mysqlDatabase}?parseTime=true&charset=utf8mb4&loc=UTC"
		$env:ODYSSEY_MYSQL_INTEGRATION = '1'
        $env:GOWORK = 'off'
        Push-Location (Join-Path $root 'server')
        try {
            & go test -tags=integration ./internal/persistence -count=1
            if ($LASTEXITCODE -ne 0) { throw 'Persistence integration tests failed.' }
        } finally {
            Pop-Location
            Remove-Item Env:ODYSSEY_REDIS_PASSWORD -ErrorAction SilentlyContinue
            Remove-Item Env:ODYSSEY_REDIS_INTEGRATION -ErrorAction SilentlyContinue
			Remove-Item Env:ODYSSEY_MYSQL_DSN -ErrorAction SilentlyContinue
			Remove-Item Env:ODYSSEY_MYSQL_INTEGRATION -ErrorAction SilentlyContinue
        }
    }
}
