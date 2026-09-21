param(
    [Parameter(Mandatory = $true)][string]$ServerHost,
    [ValidateRange(1,65535)][int]$ServerPort = 7777
)
$env:ODYSSEY_SERVER_HOST = $ServerHost
$env:ODYSSEY_SERVER_PORT = [string]$ServerPort
& (Join-Path $PSScriptRoot 'odyssey_client.exe')
