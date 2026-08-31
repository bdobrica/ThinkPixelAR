.DEFAULT_GOAL := help

GO ?= go
NPM ?= npm

.PHONY: help deps generate fmt vet test test-race openapi-check verify

help: ## Show the supported developer commands.
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "%-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

deps: ## Install pinned build-tool dependencies from the lock file.
	$(NPM) ci --ignore-scripts

generate: ## Regenerate committed artifacts.
	$(NPM) run openapi:generate

fmt: ## Format Go source files.
	$(GO) fmt ./...

vet: ## Run the Go vet analyzer.
	$(GO) vet ./...

test: ## Run Go unit tests.
	$(GO) test ./...

test-race: ## Run Go tests with the race detector.
	$(GO) test -race ./...

openapi-check: ## Validate OpenAPI and reject generated-artifact drift.
	$(NPM) run openapi:check

verify: vet test test-race openapi-check ## Run the current local and CI verification gate.
