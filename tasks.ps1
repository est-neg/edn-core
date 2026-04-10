param(
    [Parameter(Position=0)]
    [string]$Task = "help"
)

$BIN       = "vil-api"
$BUILD_DIR = "bin"
$CMD       = "./cmd/api"

switch ($Task) {
    "help" {
        Write-Host ""
        Write-Host "Targets disponíveis:" -ForegroundColor Cyan
        Write-Host "  build        Compila o binário da API"
        Write-Host "  run          Executa a API localmente"
        Write-Host "  test         Roda todos os testes"
        Write-Host "  test-cover   Testes com relatório de cobertura"
        Write-Host "  vet          Executa go vet"
        Write-Host "  tidy         Atualiza dependências (go mod tidy)"
        Write-Host "  clean        Remove artefatos de build"
        Write-Host "  docker-build Build da imagem Docker de produção"
        Write-Host ""
        Write-Host "Uso: .\tasks.ps1 <target>" -ForegroundColor Yellow
    }

    "build" {
        if (-not (Test-Path $BUILD_DIR)) { New-Item -ItemType Directory -Path $BUILD_DIR | Out-Null }
        go build -trimpath -o "$BUILD_DIR/$BIN.exe" $CMD
    }

    "run" {
        go run $CMD
    }

    "test" {
        go test ./... -count=1 -v
    }

    "test-cover" {
        go test ./... -coverprofile=coverage.out -covermode=atomic
        go tool cover -html=coverage.out -o coverage.html
        Write-Host "Relatório: coverage.html" -ForegroundColor Green
    }

    "vet" {
        go vet ./...
    }

    "tidy" {
        go mod tidy
        go mod verify
    }

    "clean" {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $BUILD_DIR
        Remove-Item -Force -ErrorAction SilentlyContinue coverage.out, coverage.html
    }

    "docker-build" {
        docker build -f deploy/docker/Dockerfile -t "${BIN}:local" .
    }

    default {
        Write-Host "Target desconhecido: $Task" -ForegroundColor Red
        Write-Host "Execute .\tasks.ps1 help para ver os targets disponíveis."
        exit 1
    }
}
