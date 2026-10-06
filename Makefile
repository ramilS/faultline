GO ?= go
NPM ?= npm

# Pinned because golangci-lint's own documentation recommends installing a
# release binary over `go install` / `tool` directives: those compile it with
# whatever Go and dependency graph happen to be local, which upstream does not
# test. Bumping this is a deliberate, reviewable change.
GOLANGCI_VERSION ?= 2.13.2
GOLANGCI ?= bin/golangci-lint

.PHONY: help install build build-web build-go lint lint-fix test test-go test-web test-integration test-integration-clean migrate-up migrate-down migrate-version compose-up compose-down compose-config clean

help: ## Show available commands
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\n"} /^[a-zA-Z_-]+:.*## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

install: ## Install JavaScript workspace dependencies
	$(NPM) ci

build: build-web build-go ## Build the dashboard and the server

build-web: ## Build the dashboard
	$(NPM) run build

build-go: ## Build the Faultline server binary
	mkdir -p bin
	$(GO) build -o bin/faultline ./cmd/faultline

$(GOLANGCI):
	@set -e; \
	os="$$(uname -s | tr 'A-Z' 'a-z')"; \
	case "$$(uname -m)" in \
	  arm64|aarch64) arch=arm64 ;; \
	  x86_64) arch=amd64 ;; \
	  *) echo "unsupported architecture $$(uname -m)" >&2; exit 1 ;; \
	esac; \
	name="golangci-lint-$(GOLANGCI_VERSION)-$$os-$$arch"; \
	base="https://github.com/golangci/golangci-lint/releases/download/v$(GOLANGCI_VERSION)"; \
	tmp="$$(mktemp -d)"; \
	echo "downloading $$name"; \
	curl -sSfL "$$base/$$name.tar.gz" -o "$$tmp/$$name.tar.gz"; \
	curl -sSfL "$$base/golangci-lint-$(GOLANGCI_VERSION)-checksums.txt" -o "$$tmp/checksums.txt"; \
	(cd "$$tmp" && grep " $$name.tar.gz\$$" checksums.txt | shasum -a 256 -c -); \
	tar xzf "$$tmp/$$name.tar.gz" -C "$$tmp"; \
	mkdir -p bin; \
	install -m 0755 "$$tmp/$$name/golangci-lint" $(GOLANGCI); \
	rm -rf "$$tmp"; \
	$(GOLANGCI) --version

lint: $(GOLANGCI) ## Run the Go linters and static analysis
	$(GOLANGCI) run

lint-fix: $(GOLANGCI) ## Run the Go linters, applying the fixes they can apply
	$(GOLANGCI) run --fix

test: lint test-go test-web ## Run available automated checks

test-go: ## Run all Go tests
	$(GO) test ./...

test-web: ## Type-check and unit-test JavaScript workspaces
	$(NPM) run typecheck
	$(NPM) test

test-integration: ## Run Docker-backed integration tests
	$(GO) test -tags=integration ./internal/api ./internal/app ./internal/platform/migrate ./internal/storage/postgres ./internal/ingest ./internal/command -v

test-integration-clean: ## Remove leftover integration-test containers
	@ids="$$(docker ps -aq --filter label=faultline-itest)"; \
	if [ -n "$$ids" ]; then docker rm --force --volumes $$ids; \
	else echo "no leftover integration-test containers"; fi

test-e2e: ## Run real Sentry SDKs against a local Faultline
	./e2e/run.sh

migrate-up: ## Apply all pending database migrations
	$(GO) run ./cmd/faultline migrate up

migrate-down: ## Roll back one database migration
	$(GO) run ./cmd/faultline migrate down

migrate-version: ## Print the current database migration version
	$(GO) run ./cmd/faultline migrate version

compose-up: ## Build and start the two-container stack
	docker compose up --build

compose-down: ## Stop the local stack
	docker compose down

compose-config: ## Render and validate the Compose configuration
	docker compose config

clean: ## Remove local build output
	$(GO) clean
	$(NPM) run --if-present clean --workspaces
