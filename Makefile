.PHONY: help build install test test-race lint lint-fix check clean

build: ## Build the pnop binary into bin/
	@mkdir -p bin
	go build -o bin/pnop ./

install: ## Install pnop to $(GOPATH)/bin
	go install ./

test: ## Run unit tests
	go test ./... $(TESTARGS)

test-race: ## Run unit tests with the race detector
	go test -race ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

lint-fix: ## Run golangci-lint with auto-fixes, then gofmt
	golangci-lint run --fix ./...
	gofmt -w .

check: ## Run lint and tests
	@echo "--- Lint ---"
	@$(MAKE) --no-print-directory lint
	@echo ""
	@echo "--- Tests ---"
	@$(MAKE) --no-print-directory test

clean: ## Remove build artifacts
	rm -rf bin
	go clean

help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
