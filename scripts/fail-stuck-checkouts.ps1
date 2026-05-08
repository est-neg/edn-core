# fail-stuck-checkouts.ps1
# Operator cleanup tool: marks poisoned checkout orders as failed and fails their
# matching checkout idempotency record when the provider create call was attempted
# but never completed.
#
# Eligibility (ALL must be true):
#   - order status is one of: created, checkout_created, pending
#   - provider_checkout_url is empty
#   - provider_create_attempted_at is set
#
# The tool acquires the distributed Redis order lock before reading or writing.
# Rerun safety: if the order is already failed but the idempotency record is still
# pending, the tool finishes only the idempotency side. Committed idempotency
# records trigger a hard stop before any write.
#
# Usage:
#   .\scripts\fail-stuck-checkouts.ps1 -OrderNSU <NSU>
#   .\scripts\fail-stuck-checkouts.ps1 -OrderNSU <NSU1>,<NSU2>,<NSU3>
#   .\scripts\fail-stuck-checkouts.ps1 -OrderNSU <NSU> -DryRun
#
# Config is loaded from .env.development then .env.local (same order as tasks.ps1).
# The underlying Go command requires VIL_MONGODB_URI and VIL_REDIS_ADDR to be set.

param(
    [Parameter(Mandatory = $true)]
    [string]$OrderNSU,

    [switch]$DryRun
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

if ($DryRun) {
    $goArgs += "-dry-run"
}

Push-Location $ROOT
try {
    & go run ./cmd/fail-stuck-checkouts @goArgs
    exit $LASTEXITCODE
} finally {
    Pop-Location
}
