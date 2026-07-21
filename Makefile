# Integration Monitor - top-level developer targets.
# SPDX-License-Identifier: Apache-2.0

SHELL := /bin/bash
GO    ?= go

# Container runtime for the local dev environment. Auto-detects podman
# (preferred when installed) and falls back to docker. Override with:
#   COMPOSE="docker compose" make dev-up
COMPOSE ?= $(shell command -v podman >/dev/null 2>&1 && echo "podman compose" || echo "docker compose")

SERVICES := controlplane cell-api cell-alerting cell-ingest cell-controller

# ── Container images ────────────────────────────────────────────────
# Container CLI for build/push. docker by default, podman if that's all
# that's installed. Override: CONTAINER=podman make docker-push
CONTAINER ?= $(shell command -v docker >/dev/null 2>&1 && echo docker || echo podman)

# Image config. REGISTRY is your registry host:port (e.g.
# registry.lan:5000); REQUIRED for docker-push, optional for a
# local-only docker-build. IMAGE_TAG defaults to the short git SHA.
#   make docker-push REGISTRY=registry.lan:5000
REGISTRY ?=
IMAGE_NAMESPACE ?= sluicio
IMAGE_TAG ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
IMAGE_PREFIX := $(if $(REGISTRY),$(REGISTRY)/,)$(IMAGE_NAMESPACE)

# Services that ship as images (each has services/<svc>/Dockerfile).
# cell-controller is built by `make build` but has no image yet.
IMAGE_SERVICES := cell-api cell-ingest cell-alerting controlplane

# ── Build version ───────────────────────────────────────────────────
# Embedded build metadata (see scripts/version.sh). VERSION is the
# SemVer-from-tags string (e.g. v0.1.0 / v0.1.0-4-gab12cd3 / a bare SHA
# before the first tag). Override on the CLI: VERSION=v1.2.3 make build.
VERSION     ?= $(shell ./scripts/version.sh 2>/dev/null || echo 0.0.0-dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG := github.com/sluicio/sluicio-app/pkg/version
GO_LDFLAGS  := -X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).Commit=$(COMMIT) -X $(VERSION_PKG).BuildDate=$(BUILD_DATE)
VERSION_ARGS := --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE)

.PHONY: help
help: ## Show this help.
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: version
version: ## Print the build version (SemVer from git tags).
	@./scripts/version.sh

.PHONY: changelog
changelog: ## Regenerate CHANGELOG.md from git history (internal).
	@./scripts/changelog.sh

.PHONY: mcp
mcp: ## Build the cell-mcp MCP server binary (stdio; read-only Sluicio tools).
	@go build -o bin/cell-mcp ./services/cell-mcp && echo "built bin/cell-mcp"

.PHONY: openapi
openapi: ## Regenerate the OpenAPI spec from the cell-api route table.
	@go run ./services/cell-api/cmd/openapi-gen

.PHONY: openapi-check
openapi-check: ## Fail if the OpenAPI spec is out of date (CI guard).
	@go run ./services/cell-api/cmd/openapi-gen -check

.PHONY: build
build: ## Build all Go services (version stamped into pkg/version).
	@echo "==> version $(VERSION)"
	@for s in $(SERVICES); do \
		echo "==> building $$s"; \
		(cd services/$$s && $(GO) build -ldflags "$(GO_LDFLAGS)" ./...) || exit 1; \
	done

.PHONY: docker-build
docker-build: ## Build all service + frontend images (tag IMAGE_TAG, version $(VERSION)).
	@for s in $(IMAGE_SERVICES); do \
		echo "==> image $(IMAGE_PREFIX)/$$s:$(IMAGE_TAG) (version $(VERSION))"; \
		$(CONTAINER) build $(VERSION_ARGS) -f services/$$s/Dockerfile -t $(IMAGE_PREFIX)/$$s:$(IMAGE_TAG) . || exit 1; \
	done
	@echo "==> image $(IMAGE_PREFIX)/frontend:$(IMAGE_TAG) (version $(VERSION))"; \
	$(CONTAINER) build --build-arg APP_VERSION=$(VERSION) -f frontend/Dockerfile -t $(IMAGE_PREFIX)/frontend:$(IMAGE_TAG) frontend || exit 1

.PHONY: docker-push
docker-push: require-registry docker-build ## Build + push all images to $$REGISTRY (set REGISTRY=host:port).
	@for s in $(IMAGE_SERVICES) frontend; do \
		echo "==> push $(IMAGE_PREFIX)/$$s:$(IMAGE_TAG)"; \
		$(CONTAINER) push $(IMAGE_PREFIX)/$$s:$(IMAGE_TAG) || exit 1; \
	done
	@echo "pushed $(words $(IMAGE_SERVICES)) services + frontend to $(REGISTRY) at tag $(IMAGE_TAG)"

.PHONY: require-registry
require-registry:
	@if [ -z "$(REGISTRY)" ]; then \
		echo "ERROR: set REGISTRY=<host:port> — e.g. make docker-push REGISTRY=registry.lan:5000"; \
		exit 1; \
	fi

.PHONY: test
test: ## Run all Go tests.
	@for s in $(SERVICES); do \
		echo "==> testing $$s"; \
		(cd services/$$s && $(GO) test ./...) || exit 1; \
	done
	@(cd pkg && $(GO) test ./...) || true
	@(cd plugins && $(GO) test ./...) || true

.PHONY: test-integration
test-integration: ## Run build-tagged integration tests (real Postgres via testcontainers; needs Docker or Podman).
	@sock=$$(podman machine inspect --format '{{.ConnectionInfo.PodmanSocket.Path}}' 2>/dev/null | head -1); \
	if [ -n "$$sock" ] && [ -S "$$sock" ]; then \
		echo "==> using podman socket $$sock"; \
		DOCKER_HOST="unix://$$sock" TESTCONTAINERS_RYUK_DISABLED=true TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock \
			$(GO) test -tags integration -count=1 ./...; \
	else \
		$(GO) test -tags integration -count=1 ./...; \
	fi

.PHONY: lint
lint: ## Run go vet across the workspace.
	@for s in $(SERVICES); do \
		echo "==> vetting $$s"; \
		(cd services/$$s && $(GO) vet ./...) || exit 1; \
	done

.PHONY: tidy
tidy: ## Run go mod tidy in every module.
	@for s in $(SERVICES); do \
		echo "==> tidy $$s"; \
		(cd services/$$s && $(GO) mod tidy) || exit 1; \
	done
	@(cd pkg && $(GO) mod tidy) || true
	@(cd plugins && $(GO) mod tidy) || true

.PHONY: dev-up
dev-up: ## Start the full local stack (Postgres, ClickHouse, Prometheus, cell-api, cell-ingest). Builds app images on first run.
	$(COMPOSE) up -d

.PHONY: dev-rebuild
dev-rebuild: ## Rebuild + restart the app containers after a code change (data stores untouched).
	$(COMPOSE) up -d --build cell-api cell-ingest

.PHONY: dev-down
dev-down: ## Stop the local dev environment (containers; named volumes keep their data).
	$(COMPOSE) down

.PHONY: dev-logs
dev-logs: ## Tail logs from the local dev environment.
	$(COMPOSE) logs -f

.PHONY: dev-ps
dev-ps: ## Show the local dev environment status.
	$(COMPOSE) ps

.PHONY: run-controlplane
run-controlplane: ## Run the control plane service.
	cd services/controlplane && $(GO) run ./cmd/controlplane

.PHONY: run-cell-api
run-cell-api: ## Run the cell API service.
	cd services/cell-api && $(GO) run ./cmd/cell-api

.PHONY: run-cell-alerting
run-cell-alerting: ## Run the alerting engine.
	cd services/cell-alerting && $(GO) run ./cmd/cell-alerting

.PHONY: run-cell-ingest
run-cell-ingest: ## Run the OTLP ingest service.
	# INGEST_ALLOW_ANONYMOUS lets local/seed telemetry through without a
	# per-org ingest key (dev only). Unset it to exercise real key auth.
	cd services/cell-ingest && INGEST_ALLOW_ANONYMOUS=true $(GO) run ./cmd/cell-ingest

.PHONY: frontend-dev
frontend-dev: ## Run the frontend dev server.
	cd frontend && npm install && npm run dev

.PHONY: seed-traces
seed-traces: ## Send a synthetic batch of traces, logs, and metrics to cell-ingest.
	$(GO) run ./services/cell-ingest/cmd/seed-traces

.PHONY: seed-traces-loop
seed-traces-loop: ## Continuously send synthetic traces, logs, and metrics (Ctrl-C to stop).
	$(GO) run ./services/cell-ingest/cmd/seed-traces -continuous

.PHONY: e2e-install
e2e-install: ## Install Playwright + browser and the frontend deps the e2e suite needs.
	cd e2e && npm ci && npm run install:browsers
	cd frontend && npm ci || npm install

.PHONY: e2e
e2e: ## Run the Playwright e2e suite (assumes the stack is already up — see dev-up).
	cd e2e && npm test

.PHONY: e2e-up
e2e-up: ## Bring up the stack, wait for cell-api, then run the e2e suite (leaves the stack up).
	$(COMPOSE) up -d
	@echo "==> waiting for cell-api on :8081 (public install-state probe)"
	@for i in $$(seq 1 60); do \
		curl -fsS http://localhost:8081/api/v1/auth/install-state >/dev/null 2>&1 && { echo "   ready"; break; }; \
		sleep 2; \
		[ $$i -eq 60 ] && { echo "   cell-api did not become healthy"; exit 1; }; \
	done
	cd e2e && npm test

.PHONY: clean
clean: ## Remove build artifacts.
	rm -rf bin/ build/ dist/
	find . -name '*.test' -delete
	find . -name 'coverage.*' -delete
