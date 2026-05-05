# recreate-checkout.ps1
# Operator recovery tool: recreates a checkout link from a legacy order stored in MongoDB.
#
# Usage:
#   .\scripts\recreate-checkout.ps1 -OrderNSU <NSU>
#   .\scripts\recreate-checkout.ps1 -OrderNSU <NSU> -Channel mobile -DryRun
#   .\scripts\recreate-checkout.ps1 -OrderNSU <NSU> -BaseURL https://dev.edn-core.app
#   .\scripts\recreate-checkout.ps1 -OrderNSU <NSU> -IdempotencyKey my-custom-key
#   .\scripts\recreate-checkout.ps1 -OrderNSU <NSU> -BaseURL https://staging.corp -AllowUnsafeURL
#
# Config is loaded from .env.development then .env.local (same order as tasks.ps1).
# The underlying Go command requires VIL_MONGODB_URI to be set.

param(
    [Parameter(Mandatory = $true)]
    [string]$OrderNSU,

    [string]$Channel = "",

    [string]$BaseURL = "",

    [string]$IdempotencyKey = "",

    [switch]$DryRun,

    [switch]$AllowUnsafeURL
)

$ROOT = Split-Path -Parent $PSScriptRoot

function Import-Env([string[]]$files) {
    foreach ($file in $files) {
        $resolved = Join-Path $ROOT $file
        if (Test-Path $resolved) {
            Get-Content $resolved | ForEach-Object {
                if ($_ -notmatch '^\s*#' -and $_ -match '^\s*([^=]+)=(.*)$') {
                    $name  = $Matches[1].Trim()
                    $value = $Matches[2].Trim()
                    Set-Item -Path "Env:$name" -Value $value
                }
            }
        }
    }
}

Import-Env @(".env.development", ".env.local")

$goArgs = @("-order-nsu", $OrderNSU)

if ($Channel -ne "") {
    $goArgs += @("-channel", $Channel)
}

if ($BaseURL -ne "") {
    $goArgs += @("-base-url", $BaseURL)
}

if ($IdempotencyKey -ne "") {
    $goArgs += @("-idempotency-key", $IdempotencyKey)
}

if ($DryRun) {
    $goArgs += "-dry-run"
}

if ($AllowUnsafeURL) {
    $goArgs += "-allow-unsafe-url"
}

Push-Location $ROOT
try {
    & go run ./cmd/recreate-checkout @goArgs
    exit $LASTEXITCODE
} finally {
    Pop-Location
}

