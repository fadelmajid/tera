# Tera — build, test, lint, run.
#
# One Go binary with the SPA embedded and a SQLite file beside it. Deploy is:
# copy the binary, run it, everyone opens a URL (ARCHITECTURE §1).

BINARY   := tera
BIN_DIR  := bin
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

# modernc.org/sqlite is pure Go, so CGO stays off and the build is one
# self-contained file. That is the whole deployment story; don't turn it on.
export CGO_ENABLED := 0

DB_PATH        ?= ./tera.db
MIGRATIONS_DIR := internal/store/migrations

# Pinned tool versions. A local binary is used when present, otherwise `go run`
# fetches the pinned one — a fresh clone and CI get the same result either way.
GOLANGCI_VERSION ?= v2.13.0
SQLC_VERSION     ?= v1.31.1
GOOSE_VERSION    ?= v3.27.3

GOLANGCI ?= $(shell command -v golangci-lint 2>/dev/null || echo go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION))
SQLC     ?= $(shell command -v sqlc 2>/dev/null || echo go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION))
GOOSE    ?= $(shell command -v goose 2>/dev/null || echo go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION))

.DEFAULT_GOAL := help
.PHONY: help web build release run test test-race cover lint fmt vet tidy sqlc sqlc-vet \
	    migrate-up migrate-down migrate-status migrate-create restore-drill \
	    docker-build docker-run ci clean

help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

web: ## Build the browser client into web/dist (embedded by the Go build)
	cd web && npm ci --no-audit --no-fund && npm run build
	# vite empties the output directory, which takes .gitkeep with it. The Go
	# embed needs at least one file to match, so put it back.
	touch web/dist/.gitkeep

build: ## Build ./bin/tera (embeds whatever web/dist currently holds)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) ./cmd/tera

release: web build ## Build the shippable binary: front end first, then embed it

run: ## Run from source
	go run ./cmd/tera

test: ## Run tests
	go test -count=1 ./...

test-race: ## Run tests under the race detector (needs CGO)
	CGO_ENABLED=1 go test -race -count=1 ./...

cover: ## Run tests with a coverage summary
	go test -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint: ## Run golangci-lint
	$(GOLANGCI) run

fmt: ## Apply formatters in place
	$(GOLANGCI) fmt

vet: ## go vet
	go vet ./...

tidy: ## go mod tidy
	go mod tidy

sqlc: ## Regenerate internal/store/gen from queries/
	$(SQLC) generate

sqlc-vet: ## Lint the SQL in queries/
	$(SQLC) vet

migrate-up: ## Apply all migrations to $(DB_PATH)
	$(GOOSE) -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) up

migrate-down: ## Roll back one migration
	$(GOOSE) -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) down

migrate-status: ## Show migration status
	$(GOOSE) -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) status

migrate-create: ## New migration: make migrate-create name=create_stock_layer
	@test -n "$(name)" || { echo "usage: make migrate-create name=create_stock_layer"; exit 1; }
	$(GOOSE) -dir $(MIGRATIONS_DIR) -s create $(name) sql

restore-drill: build ## Take a backup, restore it into a clean directory, and check it serves (TASKS 8.2)
	./scripts/restore-drill.sh

docker-build: ## Build the container image (test box only — see docs/HOSTING.md)
	docker build --build-arg VERSION=$(VERSION) -t tera:$(VERSION) -t tera:latest .

docker-run: docker-build ## Run the image locally on :8080 with a persistent volume
	docker rm -f tera-test 2>/dev/null || true
	docker run -d --name tera-test -p 8080:8080 -v tera-data:/data tera:latest
	@echo "menunggu server siap..."
	@until curl -sf http://127.0.0.1:8080/readyz >/dev/null 2>&1; do sleep 0.5; done
	@docker logs tera-test 2>&1 | grep "pengguna pertama" || true
	@echo "buka http://localhost:8080"

ci: build vet lint test-race ## Everything CI runs

clean: ## Remove build output
	rm -rf $(BIN_DIR) coverage.out
