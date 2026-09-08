#requires -Version 7.0
$ErrorActionPreference = 'Stop'
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
. (Join-Path $root 'scripts/env.ps1')
$versions = Get-Content (Join-Path $root 'scripts/toolchain.json') -Raw | ConvertFrom-Json
$actualProtoc = & protoc --version
if ($LASTEXITCODE -ne 0 -or $actualProtoc -ne "libprotoc $($versions.protoc)") {
    throw "Use protoc $($versions.protoc) to match the C++ runtime; found $actualProtoc."
}
$plugin = Join-Path $env:GOBIN $(if ($IsWindows) { 'protoc-gen-go.exe' } else { 'protoc-gen-go' })
$pluginVersion = if (Test-Path -LiteralPath $plugin) { & $plugin --version } else { '' }
if ($pluginVersion -ne "protoc-gen-go $($versions.protocGenGo)") {
    & go install "google.golang.org/protobuf/cmd/protoc-gen-go@$($versions.protocGenGo)"
    if ($LASTEXITCODE -ne 0) { throw 'Installing protoc-gen-go failed.' }
}
$goOut = Join-Path $root 'server/generated/protocol'
$cppOut = Join-Path $root 'client/generated/protocol'
New-Item -ItemType Directory -Force $goOut, $cppOut, (Join-Path $root 'build') | Out-Null
Push-Location (Join-Path $root 'proto')
try {
    $schemas = @(Get-ChildItem -Filter '*.proto' | Sort-Object Name | ForEach-Object Name)
    if ($schemas.Count -eq 0) { throw 'No protocol source files found.' }
    & protoc '--proto_path=.' "--plugin=protoc-gen-go=$plugin" "--go_out=$goOut" '--go_opt=paths=source_relative' "--cpp_out=$cppOut" "--descriptor_set_out=$root/build/protocol.pb" '--include_imports' @schemas
    if ($LASTEXITCODE -ne 0) { throw 'Protocol generation failed.' }
} finally { Pop-Location }
Write-Host 'Generated Go + C++ protocol files from proto/. No business messages are defined yet.'
