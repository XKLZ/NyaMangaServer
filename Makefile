SHELL := /bin/sh
export GOTOOLCHAIN := local
export GOPATH := $(CURDIR)/.runtime/cache/go
export GOCACHE := $(CURDIR)/.runtime/cache/go-build
export PLAYWRIGHT_BROWSERS_PATH := $(CURDIR)/.runtime/cache/playwright
export npm_config_cache := $(CURDIR)/.runtime/cache/npm
GO := go
PYTHON ?= python3
RECOMMENDATION_PYTHON ?= $(CURDIR)/.runtime/cache/recommendations/venv/bin/python
GO_PACKAGES := ./cmd/... ./internal/... ./migrations/...
COMMIT := $(shell python3 scripts/check/build-identity.py)

.PHONY: metadata-smoke foundation-smoke reset-dev fixtures install init-dev web-build build dev dev-api dev-web check test test-race e2e browser-install
install:
	npm --prefix web ci --ignore-scripts --no-audit --no-fund
	$(GO) mod download
fixtures: build
	.runtime/bin/fixtures --output .runtime/tests/fixtures/library
reset-dev:
	$(GO) run ./cmd/dev-reset --repository "$(CURDIR)"
init-dev:
	node scripts/dev/dev-config.mjs
web-build:
	npm --prefix web run build
build: web-build
	mkdir -p .runtime/bin
	$(GO) build -trimpath -ldflags '-X main.commit=$(COMMIT)' -o .runtime/bin/server ./cmd/server
	$(GO) build -trimpath -ldflags '-X main.commit=$(COMMIT)' -o .runtime/bin/instance-restore ./cmd/instance-restore
	$(GO) build -trimpath -o .runtime/bin/fixtures ./cmd/fixtures
	$(GO) build -trimpath -o .runtime/bin/dev-reset ./cmd/dev-reset

dev: init-dev build
	.runtime/bin/server --config .runtime/dev/config.json
# Build once with make build before starting; these commands preserve the instance.
dev-api:
	python3 scripts/dev/dev-services.py start api
dev-web:
	python3 scripts/dev/dev-services.py start web
.PHONY: stop-dev status-dev promote-pilot
stop-dev:
	python3 scripts/dev/dev-services.py stop
status-dev:
	python3 scripts/dev/dev-services.py status
promote-pilot:
	python3 scripts/dev/promote-pilot.py
.PHONY: check-source test-web test-dev-tools test-recommendation
check: web-build check-source
	$(GO) vet $(GO_PACKAGES)
check-source:
	python3 scripts/check/release-version.py
	test -z "$$(gofmt -l cmd internal migrations)"
	python3 scripts/check/boundaries.py --source-only
	npm --prefix web run typecheck
	npm --prefix web run lint
	npm --prefix web run format:check
	web/node_modules/.bin/prettier --check scripts/**/*.mjs
	npm --prefix web run api:check
	python3 scripts/check/check-docs.py
test: web-build test-web test-dev-tools
	mkdir -p .runtime/tests/tmp
	TMPDIR=$(CURDIR)/.runtime/tests/tmp $(GO) test $(GO_PACKAGES)
test-web:
	npm --prefix web run test:unit
test-dev-tools:
	PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -B scripts/test/boundaries-test.py
	PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -B scripts/test/dev-config-test.py
	PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -B scripts/test/promote-pilot-test.py
# Explicit local environment; no installation, model downloads or real inference.
test-recommendation:
	@test -x "$(RECOMMENDATION_PYTHON)" || { echo "Recommendation Python environment missing; set RECOMMENDATION_PYTHON to an existing pinned interpreter." >&2; exit 1; }
	mkdir -p .runtime/tests/tmp
	TMPDIR=$(CURDIR)/.runtime/tests/tmp PYTHONDONTWRITEBYTECODE=1 HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 HF_HUB_DISABLE_IMPLICIT_TOKEN=1 HF_HUB_DISABLE_TELEMETRY=1 DO_NOT_TRACK=1 TOKENIZERS_PARALLELISM=false OMP_NUM_THREADS=1 OPENBLAS_NUM_THREADS=1 MKL_NUM_THREADS=1 VECLIB_MAXIMUM_THREADS=1 NUMEXPR_NUM_THREADS=1 "$(RECOMMENDATION_PYTHON)" -B -m unittest discover -s runtime/recommendations -p 'test_*.py'
test-race: web-build
	mkdir -p .runtime/tests/tmp
	TMPDIR=$(CURDIR)/.runtime/tests/tmp $(GO) test -race $(GO_PACKAGES)
browser-install:
	npm --prefix web exec -- playwright install chromium
foundation-smoke: build
	node scripts/test/foundation-smoke.mjs
e2e: build
	npm --prefix web run test:e2e

metadata-smoke: build
	node scripts/test/metadata-smoke.mjs

.PHONY: search-scale organization-smoke
search-scale:
	mkdir -p .runtime/tests/tmp .runtime/tests/results
	TMPDIR=$(CURDIR)/.runtime/tests/tmp NYAMANGA_SEARCH_SCALE=1 NYAMANGA_SCALE_REPORT=$(CURDIR)/.runtime/tests/results/search-scale.json $(GO) test ./internal/store -run '^TestSearchScale$$' -count=1 -v
organization-smoke: build
	node scripts/test/organization-smoke.mjs

.PHONY: admin-smoke
admin-smoke: build
	node scripts/test/admin-smoke.mjs

.PHONY: rehash-dev
rehash-dev:
	$(GO) run ./cmd/dev-rehash --config .runtime/dev/vite-config.json

# Historical preparation tools: docs/history/development-tools.md. Startup retired.
.PHONY: pilot-prepare pilot-import pilot-reuse-cache pilot-build pilot-api pilot-web
pilot-prepare:
	$(GO) run ./cmd/recommendation-pilot --root "$(CURDIR)" --action prepare
pilot-import:
	$(GO) run ./cmd/recommendation-pilot --root "$(CURDIR)" --action import
pilot-reuse-cache:
	PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python runtime/recommendations/import_cache.py --root "$(CURDIR)"
pilot-build: web-build
	mkdir -p .runtime/bin
	$(GO) build -trimpath -ldflags '-X main.commit=$(COMMIT)' -o .runtime/bin/recommendation-pilot-server ./cmd/server
	chmod 0700 .runtime/bin/recommendation-pilot-server
pilot-api pilot-web:
	node scripts/dev/pilot-retired.mjs

# Explicit offline diagnosis; never builds the web app or starts/stops a service.
.PHONY: health-diagnose
health-diagnose:
	@test -n "$(HEALTH_DATA_DIR)" -a -n "$(HEALTH_OUTPUT_DIR)" || { echo "Set HEALTH_DATA_DIR and a new HEALTH_OUTPUT_DIR." >&2; exit 2; }
	$(GO) run ./cmd/health-diagnose --data-dir "$(HEALTH_DATA_DIR)" --output "$(HEALTH_OUTPUT_DIR)" $(if $(filter 1,$(HEALTH_PRESERVE)),--preserve,)

# Explicit offline original verification; no web build or service lifecycle.
.PHONY: storage-verify
storage-verify:
	@test -n "$(STORAGE_CONFIG)" -a -n "$(ORIGINALS_MANIFEST)" || { echo "Set STORAGE_CONFIG and ORIGINALS_MANIFEST." >&2; exit 2; }
	$(GO) run ./cmd/storage-verify --config "$(STORAGE_CONFIG)" --archive-manifest "$(ORIGINALS_MANIFEST)"

# CI compiles embedded assets once, then runs checks without implicit rebuilds.
.PHONY: ci-check ci-race
ci-check: build check-source test-web test-dev-tools
	mkdir -p .runtime/tests/tmp
	$(GO) vet $(GO_PACKAGES)
	TMPDIR=$(CURDIR)/.runtime/tests/tmp $(GO) test $(GO_PACKAGES)

# SQLite's generated Go code is heavily instrumented by -race. Keep the full
# ordinary suite above and run concurrency-focused regressions serially.
ci-race:
	mkdir -p .runtime/tests/tmp
	TMPDIR=$(CURDIR)/.runtime/tests/tmp python3 scripts/test/release-race.py
