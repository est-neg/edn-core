BIN        := vil-api
MODULE     := github.com/villenneve/vil-core
BUILD_DIR  := bin
CMD        := ./cmd/api
GO         := go

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the API binary
	$(GO) build -trimpath -o $(BUILD_DIR)/$(BIN) $(CMD)

.PHONY: run
run: ## Run the API locally
	$(GO) run $(CMD)

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
