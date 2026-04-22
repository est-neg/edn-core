param(
    [Parameter(Position=0)]
    [string]$Task = "help"
)

$BIN_APP   = "vil-app"
$BIN       = "vil-api"
$BIN_PAY   = "vil-payments-api"
$BIN_WRK   = "vil-subscription-worker"
$BUILD_DIR = "bin"
$CMD_APP   = "./cmd/app"
$CMD       = "./cmd/api"
$CMD_PAY   = "./cmd/payments-api"
$CMD_WRK   = "./cmd/subscription-worker"

# Carrega um arquivo .env e seu override .local (gitignored) no processo corrente.
# Linhas comecando com # sao ignoradas. O arquivo .local e opcional.
function Load-Env([string]$envFile) {
    foreach ($file in @($envFile, "$envFile.local")) {
        if (Test-Path $file) {
            Get-Content $file | ForEach-Object {
                if ($_ -notmatch '^\s*#' -and $_ -match '^\s*([^=]+)=(.*)$') {
                    $name  = $Matches[1].Trim()
                    $value = $Matches[2].Trim()
                    Set-Item -Path "Env:$name" -Value $value
                }
            }
        }
    }
}

switch ($Task) {

    "dev" {
        Load-Env ".env.development"
        Write-Host ""
        Write-Host "EDN Core [development]" -ForegroundColor Green
        Write-Host "  API  -> http://localhost:8080" -ForegroundColor DarkGray
        Write-Host "  Docs -> http://localhost:8080/docs" -ForegroundColor DarkGray
        Write-Host ""
        go run $CMD_APP
    }

    "run-dev" {
        Load-Env ".env.development"
        Write-Host "payments-api [development]" -ForegroundColor Green
        go run $CMD_PAY
    }

    "run-worker-dev" {
        Load-Env ".env.development"
        Write-Host "subscription-worker [development]" -ForegroundColor Green
        go run $CMD_WRK
    }

    "run" {
        Load-Env ".env.development"
        Write-Host "leads API [development]" -ForegroundColor Green
        go run $CMD
    }

    "deploy-dev" {
        Write-Host "Deploy -> edn-core-dev [funcionario-online-493412]" -ForegroundColor Cyan
        gcloud builds submit --config cloudbuild.development.yaml . --project=funcionario-online-493412
    }

    "deploy-prd" {
        Write-Host "Deploy -> edn-core-prd [funcionario-online-493412]" -ForegroundColor Cyan
        gcloud builds submit --config cloudbuild.yaml . --project=funcionario-online-493412
    }

    "build-app" {
        if (-not (Test-Path $BUILD_DIR)) { New-Item -ItemType Directory -Path $BUILD_DIR | Out-Null }
        go build -trimpath -o "$BUILD_DIR/$BIN_APP.exe" $CMD_APP
        Write-Host "Built: $BUILD_DIR/$BIN_APP.exe" -ForegroundColor Green
    }

    "build" {
        if (-not (Test-Path $BUILD_DIR)) { New-Item -ItemType Directory -Path $BUILD_DIR | Out-Null }
        go build -trimpath -o "$BUILD_DIR/$BIN.exe" $CMD
    }

    "build-payments" {
        if (-not (Test-Path $BUILD_DIR)) { New-Item -ItemType Directory -Path $BUILD_DIR | Out-Null }
        go build -trimpath -o "$BUILD_DIR/$BIN_PAY.exe" $CMD_PAY
    }

    "build-worker" {
        if (-not (Test-Path $BUILD_DIR)) { New-Item -ItemType Directory -Path $BUILD_DIR | Out-Null }
        go build -trimpath -o "$BUILD_DIR/$BIN_WRK.exe" $CMD_WRK
    }

    "build-all" {
        if (-not (Test-Path $BUILD_DIR)) { New-Item -ItemType Directory -Path $BUILD_DIR | Out-Null }
        go build -trimpath -o "$BUILD_DIR/$BIN_APP.exe" $CMD_APP
        go build -trimpath -o "$BUILD_DIR/$BIN.exe" $CMD
        go build -trimpath -o "$BUILD_DIR/$BIN_PAY.exe" $CMD_PAY
        go build -trimpath -o "$BUILD_DIR/$BIN_WRK.exe" $CMD_WRK
        Write-Host "Build completo em $BUILD_DIR/" -ForegroundColor Green
    }

    "test" {
        go test ./... -count=1
    }

    "test-cover" {
        go test ./... -coverprofile=coverage.out -covermode=atomic
        go tool cover -html=coverage.out -o coverage.html
        Write-Host "Relatorio: coverage.html" -ForegroundColor Green
    }

    "vet" { go vet ./... }

    "tidy" { go mod tidy; go mod verify }

    "clean" {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $BUILD_DIR
        Remove-Item -Force -ErrorAction SilentlyContinue coverage.out, coverage.html
    }

    "docker-build" {
        docker build -f deploy/docker/Dockerfile -t "${BIN}:local" .
    }

    "help" {
        Write-Host ""
        Write-Host "Targets disponiveis:" -ForegroundColor Cyan
        Write-Host ""
        Write-Host "  PRINCIPAL:" -ForegroundColor Yellow
        Write-Host "    dev              Sobe tudo (leads + payments + worker) com docs em /docs"
        Write-Host ""
        Write-Host "  MODULOS INDIVIDUAIS:" -ForegroundColor Yellow
        Write-Host "    run              leads API"
        Write-Host "    run-dev          payments-api"
        Write-Host "    run-worker-dev   subscription-worker"
        Write-Host ""
        Write-Host "  GCP DEPLOY:" -ForegroundColor Yellow
        Write-Host "    deploy-dev       Cloud Build -> Cloud Run edn-core-dev"
        Write-Host "    deploy-prd       Cloud Build -> Cloud Run edn-core-prd"
        Write-Host ""
        Write-Host "  BUILD:" -ForegroundColor Yellow
        Write-Host "    build-app        Compila app unificado (cmd/app)"
        Write-Host "    build-all        Compila todos os binarios"
        Write-Host "    build            Compila leads API"
        Write-Host "    build-payments   Compila payments-api"
        Write-Host "    build-worker     Compila subscription-worker"
        Write-Host ""
        Write-Host "  QUALIDADE:" -ForegroundColor Yellow
        Write-Host "    test             Roda todos os testes"
        Write-Host "    test-cover       Testes com relatorio de cobertura"
        Write-Host "    vet              go vet"
        Write-Host "    tidy             go mod tidy + verify"
        Write-Host "    clean            Remove artefatos de build"
        Write-Host ""
        Write-Host "  Secrets locais: crie .env.development.local (gitignored)" -ForegroundColor DarkGray
        Write-Host "  Uso: .\tasks.ps1 <target>" -ForegroundColor DarkGray
        Write-Host ""
    }

    default {
        Write-Host "Target desconhecido: $Task" -ForegroundColor Red
        Write-Host "Execute .\tasks.ps1 help para ver os targets disponiveis."
        exit 1
    }
}
