BIN              := vil-api
BIN_PAYMENTS     := vil-payments-api
BIN_WORKER       := vil-subscription-worker
MODULE           := github.com/villenneve/vil-core
BUILD_DIR        := bin
CMD              := ./cmd/api
CMD_PAYMENTS     := ./cmd/payments-api
CMD_WORKER       := ./cmd/subscription-worker
GO               := go

# Load local env files into the shell environment. Non-fatal if files are absent.
# .env.development: legacy local file, gitignored, still accepted as fallback.
# .env.local: preferred, gitignored; overrides .env.development when both exist.
# Usage: $(call load-env)
define load-env
	$(eval -include .env.development)
	$(eval -include .env.local)
	$(eval export)
endef

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the API binary
	$(GO) build -trimpath -o $(BUILD_DIR)/$(BIN) $(CMD)

.PHONY: build-payments
build-payments: ## Build the payments-api binary
	$(GO) build -trimpath -o $(BUILD_DIR)/$(BIN_PAYMENTS) $(CMD_PAYMENTS)

.PHONY: build-worker
build-worker: ## Build the subscription-worker binary
	$(GO) build -trimpath -o $(BUILD_DIR)/$(BIN_WORKER) $(CMD_WORKER)

.PHONY: build-all
build-all: build build-payments build-worker ## Build all binaries

# ── Local run targets (.env.local preferred; .env.development legacy local also accepted) ──

.PHONY: run
run: ## Run the leads API (.env.local preferred; .env.development accepted as legacy fallback)
	$(call load-env)
	$(GO) run $(CMD)

.PHONY: run-payments-dev
run-payments-dev: ## Run payments-api (.env.local preferred; .env.development accepted as legacy fallback)
	$(call load-env)
	$(GO) run $(CMD_PAYMENTS)

.PHONY: run-worker-dev
run-worker-dev: ## Run subscription-worker (.env.local preferred; .env.development accepted as legacy fallback)
	$(call load-env)
	$(GO) run $(CMD_WORKER)

# ── GCP deploy targets ───────────────────────────────────────────────────────

.PHONY: deploy-dev
deploy-dev: ## Deploy to Cloud Run edn-core-dev (uses deploy/env/cloudrun.development.yaml + Secret Manager)
	gcloud builds submit --config cloudbuild.development.yaml . --project=funcionario-online-493412

.PHONY: deploy-prd
deploy-prd: ## Deploy to Cloud Run edn-core-prd (uses deploy/env/cloudrun.production.yaml + Secret Manager)
	gcloud builds submit --config cloudbuild.yaml . --project=funcionario-online-493412

.PHONY: seed-dev
seed-dev: ## Seed development MongoDB (.env.local preferred; .env.development accepted as legacy fallback)
	$(call load-env)
	$(GO) run ./cmd/dev-seed

.PHONY: test
test: ## Run all tests (add -race on Linux/macOS CI where CGO is available)
	$(GO) test ./... -count=1

.PHONY: test-cover
test-cover: ## Run tests with coverage report
	$(GO) test ./... -coverprofile=coverage.out -covermode=atomic
	$(GO) tool cover -html=coverage.out -o coverage.html

.PHONY: lint
lint: ## Run golangci-lint (requires golangci-lint in PATH)
	golangci-lint run ./...

.PHONY: tidy
tidy: ## Tidy and verify module dependencies
	$(GO) mod tidy
	$(GO) mod verify

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR) coverage.out coverage.html

.PHONY: docker-build
docker-build: ## Build the production Docker image
	docker build -f deploy/docker/Dockerfile -t $(BIN):local .

# kept as aliases for backwards compatibility
.PHONY: cloudbuild-dev
cloudbuild-dev: deploy-dev ## Alias for deploy-dev

.PHONY: cloudbuild-prd
cloudbuild-prd: deploy-prd ## Alias for deploy-prd
