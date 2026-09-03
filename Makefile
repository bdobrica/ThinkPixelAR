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
AGENTD_IMAGE ?= thinkpixel-agentd:development

.PHONY: help deps db-up db-down migrate test-db-migrations test-db-transactions test-db-tenant-isolation test-db-concurrency test-db-restart-replay generate fmt fmt-check vet lint static test test-unit test-race vulnerability license hygiene hygiene-test versions-check build image image-smoke agentd-image agentd-image-smoke openapi-check verify baseline-verify

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

test-db-migrations: db-up ## Test the full migration chain from an empty schema on pinned PostgreSQL.
	THINKPIXELAR_TEST_DATABASE_URL="$${THINKPIXELAR_TEST_DATABASE_URL:-postgres://thinkpixelar:thinkpixelar-development-only@127.0.0.1:$${THINKPIXELAR_POSTGRES_PORT:-55432}/thinkpixelar?sslmode=disable}" $(GO) test ./internal/adapters/postgres/migrations -run '^TestUpFromEmptyPostgreSQL$$' -count=1

test-db-transactions: db-up ## Test transaction rollback behavior on pinned PostgreSQL.
	THINKPIXELAR_DATABASE_URL="$${THINKPIXELAR_TEST_DATABASE_URL:-postgres://thinkpixelar:thinkpixelar-development-only@127.0.0.1:$${THINKPIXELAR_POSTGRES_PORT:-55432}/thinkpixelar?sslmode=disable}" $(GO) run ./cmd/migrate up
	THINKPIXELAR_TEST_DATABASE_URL="$${THINKPIXELAR_TEST_DATABASE_URL:-postgres://thinkpixelar:thinkpixelar-development-only@127.0.0.1:$${THINKPIXELAR_POSTGRES_PORT:-55432}/thinkpixelar?sslmode=disable}" $(GO) test ./internal/adapters/postgres -run '^(TestStoreCommitsAndRollsBackTenantScopedRepositories|TestStoreRollsBackExternalReferenceReservationBeforeCommit)$$' -count=1

test-db-tenant-isolation: db-up ## Test every repository's tenant boundary on pinned PostgreSQL.
	THINKPIXELAR_DATABASE_URL="$${THINKPIXELAR_TEST_DATABASE_URL:-postgres://thinkpixelar:thinkpixelar-development-only@127.0.0.1:$${THINKPIXELAR_POSTGRES_PORT:-55432}/thinkpixelar?sslmode=disable}" $(GO) run ./cmd/migrate up
	THINKPIXELAR_TEST_DATABASE_URL="$${THINKPIXELAR_TEST_DATABASE_URL:-postgres://thinkpixelar:thinkpixelar-development-only@127.0.0.1:$${THINKPIXELAR_POSTGRES_PORT:-55432}/thinkpixelar?sslmode=disable}" $(GO) test ./internal/adapters/postgres -run '^TestStoreIsolatesEveryRepositoryByTenant$$' -count=1

test-db-concurrency: db-up ## Test persistence concurrency invariants on pinned PostgreSQL.
	THINKPIXELAR_DATABASE_URL="$${THINKPIXELAR_TEST_DATABASE_URL:-postgres://thinkpixelar:thinkpixelar-development-only@127.0.0.1:$${THINKPIXELAR_POSTGRES_PORT:-55432}/thinkpixelar?sslmode=disable}" $(GO) run ./cmd/migrate up
	THINKPIXELAR_TEST_DATABASE_URL="$${THINKPIXELAR_TEST_DATABASE_URL:-postgres://thinkpixelar:thinkpixelar-development-only@127.0.0.1:$${THINKPIXELAR_POSTGRES_PORT:-55432}/thinkpixelar?sslmode=disable}" $(GO) test ./internal/adapters/postgres -run '^TestStoreConcurrencyInvariants$$' -count=1

test-db-restart-replay: db-up ## Test durable work replay after process restart on pinned PostgreSQL.
	THINKPIXELAR_DATABASE_URL="$${THINKPIXELAR_TEST_DATABASE_URL:-postgres://thinkpixelar:thinkpixelar-development-only@127.0.0.1:$${THINKPIXELAR_POSTGRES_PORT:-55432}/thinkpixelar?sslmode=disable}" $(GO) run ./cmd/migrate up
	THINKPIXELAR_TEST_DATABASE_URL="$${THINKPIXELAR_TEST_DATABASE_URL:-postgres://thinkpixelar:thinkpixelar-development-only@127.0.0.1:$${THINKPIXELAR_POSTGRES_PORT:-55432}/thinkpixelar?sslmode=disable}" $(GO) test ./internal/adapters/postgres -run '^TestStoreReplaysExpiredWorkAfterRestart$$' -count=1

generate: ## Regenerate committed artifacts.
	$(NPM) run openapi:generate

fmt: ## Format Go source files.
	$(GO) fmt ./...

fmt-check: ## Reject Go source files that are not gofmt-formatted.
	./scripts/check-go-format.sh

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

hygiene: ## Reject tracked local state, credentials, secrets, and binaries.
	./scripts/repository-hygiene.sh

hygiene-test: ## Test the repository hygiene policy with isolated fixtures.
	./scripts/test-repository-hygiene.sh

versions-check: ## Validate the tested toolchain and immutable component pins.
	./scripts/check-supported-versions.sh

build: ## Build the service binary with reproducible path metadata.
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -o $(BUILD_DIR)/thinkpixelar ./cmd/thinkpixelar
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -o $(BUILD_DIR)/thinkpixel-agentd ./cmd/thinkpixel-agentd
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -o $(BUILD_DIR)/migrate ./cmd/migrate

image: ## Build the pinned, non-root thinkpixelar service image.
	$(DOCKER) build --pull --tag $(IMAGE) .

image-smoke: image ## Run the service image as non-root with a read-only filesystem.
	DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" sh ./scripts/smoke-thinkpixelar-image.sh

agentd-image: ## Build the distinct pinned, non-root sandbox supervisor image.
	$(DOCKER) build --pull --file Dockerfile.agentd --tag $(AGENTD_IMAGE) .

agentd-image-smoke: agentd-image ## Run the supervisor image as non-root with a read-only filesystem.
	DOCKER="$(DOCKER)" AGENTD_IMAGE="$(AGENTD_IMAGE)" sh ./scripts/smoke-thinkpixel-agentd-image.sh

openapi-check: ## Validate OpenAPI and reject generated-artifact drift.
	$(NPM) run openapi:check

verify: hygiene hygiene-test versions-check fmt-check static test-unit test-race vulnerability license build openapi-check ## Run the local and CI source verification gate.

baseline-verify: verify image-smoke agentd-image-smoke ## Run the complete Phase 1 source and image baseline.
