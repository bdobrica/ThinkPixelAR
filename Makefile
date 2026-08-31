.DEFAULT_GOAL := help

GO ?= go
NPM ?= npm
NODE ?= node
DOCKER ?= docker
STATICCHECK_VERSION := v0.8.1
GOVULNCHECK_VERSION := v1.7.0
STATICCHECK := $(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
GOVULNCHECK := $(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
BUILD_DIR ?= .cache/bin
IMAGE ?= thinkpixelar:development

.PHONY: help deps db-up db-down migrate generate fmt vet lint static test test-unit test-race vulnerability license build image image-smoke openapi-check verify

help: ## Show the supported developer commands.
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "%-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

deps: ## Install pinned build-tool dependencies from the lock file.
	$(NPM) ci --ignore-scripts

db-up: ## Start the pinned local PostgreSQL development service.
	$(DOCKER) compose up --detach --wait postgres

db-down: ## Stop the local PostgreSQL development service without deleting its volume.
	$(DOCKER) compose down

migrate: ## Run explicit PostgreSQL migrations (ARGS="...").
	$(GO) run ./cmd/migrate $(ARGS)

generate: ## Regenerate committed artifacts.
	$(NPM) run openapi:generate

fmt: ## Format Go source files.
	$(GO) fmt ./...

vet: ## Run the Go vet analyzer.
	$(GO) vet ./...

lint: ## Run the pinned Staticcheck analyzer.
	$(STATICCHECK) ./...

static: vet lint ## Run all static-analysis gates.

test: ## Run Go unit tests.
	$(GO) test ./...

test-unit: test ## Run Go unit tests (explicit CI alias).

test-race: ## Run Go tests with the race detector.
	$(GO) test -race ./...

vulnerability: ## Report reachable vulnerabilities using the pinned Go scanner.
	$(GOVULNCHECK) ./...

license: ## Inventory modules and enforce the dependency/license policy.
	./scripts/dependency-policy.sh "$(GO)"
	$(NODE) ./scripts/check-npm-dependencies.mjs

build: ## Build the service binary with reproducible path metadata.
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -o $(BUILD_DIR)/thinkpixelar ./cmd/thinkpixelar
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -o $(BUILD_DIR)/migrate ./cmd/migrate

image: ## Build the pinned, non-root thinkpixelar service image.
	$(DOCKER) build --pull --tag $(IMAGE) .

image-smoke: image ## Run the service image as non-root with a read-only filesystem.
	DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" sh ./scripts/smoke-thinkpixelar-image.sh

openapi-check: ## Validate OpenAPI and reject generated-artifact drift.
	$(NPM) run openapi:check

verify: static test-unit test-race vulnerability license build openapi-check ## Run the current local and CI verification gate.
