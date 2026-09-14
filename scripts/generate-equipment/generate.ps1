#requires -Version 7.0
<#
.SYNOPSIS
Generates the client display table for equipment from Role B's versioned catalog.

.DESCRIPTION
`data/equipment/catalog.json` is the single source of truth for equipment (ids,
names, slots, descriptions) and is owned by Role B. The C++ client must not parse
JSON at runtime, so it consumes a flat CSV display table instead. This script is
the only supported way to produce that table, which keeps the two from drifting
apart by hand (WEEK2 finalization item D1/D7).

The catalog's own `description` text is copied verbatim into the `stats` column:
the client shows exactly what the domain data says and never re-derives numbers
from modifiers.

  -Check   regenerate in memory and compare with the committed file (CI gate);
           exits 1 with a diff summary when the table is stale.
  default  rewrite the table.

.EXAMPLE
pwsh -File scripts/generate-equipment/generate.ps1
pwsh -File scripts/generate-equipment/generate.ps1 -Check
#>
[CmdletBinding()]
param(
    [string]$Catalog = 'data/equipment/catalog.json',
    [string]$Output = 'client/assets/data/equipment.csv',
    [switch]$Check
)

$ErrorActionPreference = 'Stop'

# $PSScriptRoot is <repo>/scripts/generate-equipment.
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent

function Resolve-RepoPath {
    param([string]$Path)
    if ($Path -match '^([A-Za-z]:[\\/]|[\\/])') {
        return $Path
    }
    return (Join-Path $root $Path)
}

function ConvertTo-CsvField {
    param([string]$Value)
    # Always quote: descriptions may contain commas ("Attack +5, Speed +1") and the
    # client's parser unquotes a field it finds wrapped in double quotes.
    $escaped = $Value -replace '"', '""'
    return '"' + $escaped + '"'
}

# Exits with 2 (distinct from the stale-table code 1) without letting the
# $ErrorActionPreference = 'Stop' throw swallow the intended exit code.
function Fail-Catalog {
    param([string]$Message)
    Write-Error -Message $Message -ErrorAction Continue
    exit 2
}

$catalogPath = Resolve-RepoPath $Catalog
$outputPath = Resolve-RepoPath $Output

if (-not (Test-Path -LiteralPath $catalogPath -PathType Leaf)) {
    Fail-Catalog "Equipment catalog not found: $catalogPath"
}

# NOTE: variables are case-insensitive in PowerShell, so the parsed document must
# not be named $catalog - that is the [string] parameter and would be coerced.
$catalogData = Get-Content -LiteralPath $catalogPath -Raw | ConvertFrom-Json
if ($null -eq $catalogData.version) {
    Fail-Catalog "Catalog $catalogPath has no 'version' field."
}
$items = @($catalogData.items)
if ($items.Count -eq 0) {
    Fail-Catalog "Catalog $catalogPath has no items."
}

$seen = @{}
foreach ($item in $items) {
    foreach ($field in @('id', 'name', 'slot', 'description')) {
        if ([string]::IsNullOrWhiteSpace([string]$item.$field)) {
            Fail-Catalog "Catalog item is missing '$field': $($item | ConvertTo-Json -Compress)"
        }
    }
    $id = [int]$item.id
    if ($id -lt 1000) {
        # Ids below 1000 were the old client placeholders; the server never sends
        # them, so their presence means the catalog regressed.
        Fail-Catalog "Catalog id $id is outside the versioned range (expected >= 1000)."
    }
    if ($seen.ContainsKey($id)) {
        Fail-Catalog "Catalog id $id appears more than once."
    }
    $seen[$id] = $true
}

$lines = [System.Collections.Generic.List[string]]::new()
$lines.Add('# Client-side DISPLAY table for equipment (the server only ever sends ids).')
$lines.Add("# Generated from $($Catalog -replace '\\', '/') (version $($catalogData.version)) by scripts/generate-equipment/generate.ps1")
$lines.Add('# Format: id,name,slot,stats   (stats is the catalog description, quoted)')
$lines.Add('# Do not edit by hand: change the catalog and re-run the generator.')
$lines.Add('#   pwsh -File scripts/generate-equipment/generate.ps1')
$lines.Add('#   pwsh -File scripts/generate-equipment/generate.ps1 -Check   # CI staleness gate')
foreach ($item in ($items | Sort-Object { [int]$_.id })) {
    $lines.Add(('{0},{1},{2},{3}' -f [int]$item.id,
                (ConvertTo-CsvField ([string]$item.name)),
                (ConvertTo-CsvField ([string]$item.slot)),
                (ConvertTo-CsvField ([string]$item.description))))
}
$generated = ($lines -join "`n") + "`n"

function Normalize-Newlines {
    param([string]$Text)
    return ($Text -replace "`r`n", "`n")
}

if ($Check) {
    if (-not (Test-Path -LiteralPath $outputPath -PathType Leaf)) {
        Write-Host "Display table missing: $Output (run the generator without -Check)." -ForegroundColor Red
        exit 1
    }
    $current = Normalize-Newlines (Get-Content -LiteralPath $outputPath -Raw)
    if ($current -ceq $generated) {
        Write-Host "Equipment display table is up to date: $Output ($($items.Count) items, catalog version $($catalogData.version))."
        exit 0
    }
    Write-Host 'Equipment display table is STALE - regenerate it:' -ForegroundColor Red
    Write-Host '  pwsh -File scripts/generate-equipment/generate.ps1'
    $currentLines = $current.TrimEnd("`n") -split "`n"
    $generatedLines = $generated.TrimEnd("`n") -split "`n"
    $max = [Math]::Max($currentLines.Count, $generatedLines.Count)
    for ($i = 0; $i -lt $max; $i++) {
        $have = if ($i -lt $currentLines.Count) { $currentLines[$i] } else { '<missing>' }
        $want = if ($i -lt $generatedLines.Count) { $generatedLines[$i] } else { '<missing>' }
        if ($have -cne $want) {
            Write-Host ("  line {0}`n    file: {1}`n    want: {2}" -f ($i + 1), $have, $want)
        }
    }
    exit 1
}

Set-Content -LiteralPath $outputPath -Value $generated -NoNewline -Encoding utf8
Write-Host "Wrote $Output ($($items.Count) items, catalog version $($catalogData.version))."
exit 0
