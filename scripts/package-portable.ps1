param(
    [Parameter(Mandatory = $true)]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [string]$ExePath,

    [Parameter(Mandatory = $true)]
    [string]$OutputDir
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$resolvedExe = (Resolve-Path $ExePath).Path
$resolvedOutputDir = Join-Path $repoRoot $OutputDir
$portableDir = Join-Path $resolvedOutputDir "portable"
$quickStartPath = Join-Path $repoRoot "packaging\README-QuickStart.txt"

New-Item -ItemType Directory -Force -Path $resolvedOutputDir | Out-Null
Remove-Item -LiteralPath $portableDir -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $portableDir | Out-Null

Copy-Item -LiteralPath $resolvedExe -Destination (Join-Path $resolvedOutputDir "my-browser.exe") -Force
Copy-Item -LiteralPath $resolvedExe -Destination (Join-Path $portableDir "my-browser.exe") -Force
New-Item -ItemType File -Path (Join-Path $portableDir "portable.flag") -Force | Out-Null
Copy-Item -LiteralPath $quickStartPath -Destination (Join-Path $portableDir "README-QuickStart.txt") -Force

$zipPath = Join-Path $resolvedOutputDir ("my-browser-portable-{0}.zip" -f $Version)
if (Test-Path $zipPath) {
    Remove-Item -LiteralPath $zipPath -Force
}

Compress-Archive -Path (Join-Path $portableDir "*") -DestinationPath $zipPath -Force

Write-Host "Prepared release artifacts:"
Write-Host " -" (Join-Path $resolvedOutputDir "my-browser.exe")
Write-Host " -" $zipPath
