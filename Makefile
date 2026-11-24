
SHELL := /bin/bash
.DEFAULT_GOAL := help

REPO_ROOT := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))

LINT_COMPOSITE_ACTIONS := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))/tools/lint_composite_actions.py

MODULES := $(shell go work edit -json go.work | sed -n 's/^[[:space:]]*"DiskPath": "\(.*\)".*/\1/p')

FRAGMENT_PACKAGES := $(shell paths='$(strip $(MODULES))'; if [ -n "$$paths" ]; then grep -rl --include='openapi_generated_test.go' '^package ' $$paths 2>/dev/null | sed -E 's|/[^/]+$$||' | sort -u; fi)

GATES := $(wildcard tools/check_*.py)

PYTHON ?= python3

GATE_JOBS ?= 4

GATE_TIME_SCALE ?= 1
export GATE_TIME_SCALE

UNCACHED_PACKAGES ?= ./starters/demo/server/treecheck ./starters/demo/server/e2e/factorseed

uncached-dirs = $(abspath $(UNCACHED_PACKAGES))

TEST_BACKENDS_STATE ?= .cache/test-backends.env
BACKENDS ?= $(PYTHON) tools/backends.py

WORKFLOW_DIR := .github/workflows
WORKFLOWS := $(sort $(patsubst $(REPO_ROOT)/%,%,$(wildcard $(REPO_ROOT)/$(WORKFLOW_DIR)/*.yml $(REPO_ROOT)/$(WORKFLOW_DIR)/*.yaml)))
ACTIONLINT ?= actionlint
SHELLCHECK ?= shellcheck

each = for it in $(1); do echo "==> $(2) $$it"; $(3) || failed="$${failed:+$$failed; }$(2) $$it"; done
each-end = if [ -n "$$failed" ]; then echo "failed: $$failed" >&2; exit 1; fi

mark = @echo "== $(1) $$(date '+%H:%M:%S')"

segment-output = 2>&1 | awk '{ printf "[$(1)] %s\n", $$0; fflush() }'

.PHONY: help modules deps-up deps-down build api-docs test test-race test-integration test-integration-race \
        vet-integration vet-web-bundle fmt lint tidy tidy-check actionlint repo-check tools-test check python-version \
        modules-present web-check web-check-static web-check-test web-check-build web-a11y web-browser e2e e2e-install dev fly \
        check-announce check-lint check-tidy-check check-repo-check check-tools-test check-vet-web-bundle \
        check-web-check-static check-web-check-build check-build check-vet-integration \
        check-test check-web-check-test check-test-race

help: ## List the entry points
	@awk 'BEGIN{FS=":.*## "} /^[a-z0-9][a-z0-9-]*:.*## /{printf "  %-21s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

modules: modules-present ## Print the module directories derived from go.work
	@for m in $(MODULES); do echo $$m; done

build: modules-present ## Build every module, both in the workspace and standalone
	@failed=; $(call each,$(MODULES),build,GOMAXPROCS=$(GATE_JOBS) go -C $$it build -p=$(GATE_JOBS) ./...); \
	  $(call each,$(MODULES),build standalone,GOWORK=off GOMAXPROCS=$(GATE_JOBS) go -C $$it build -p=$(GATE_JOBS) ./...); $(each-end)

api-docs: modules-present ## Regenerate every OpenAPI fragment and the merged contract
	@failed=; $(call each,$(FRAGMENT_PACKAGES),fragment,env UPDATE_FRAGMENTS=1 go -C $$it test ./... -run '^TestTheFragmentIsGeneratedFromTheDeclarations$$' -count=1); $(each-end)
	@go -C starters/demo/server run . --config file:testdata/sqlite-memory.yaml sqlite-memory gen \
	  --title speed -o ../../../contracts/api.json --features ../../../contracts/features.json

deps-up: ## Bring this checkout's integration backends up
	@$(BACKENDS) up --state '$(TEST_BACKENDS_STATE)'

deps-down: ## Take this checkout's integration backends down
	@$(BACKENDS) down --state '$(TEST_BACKENDS_STATE)'

test: modules-present ## Test every module (unit cases only)
	@failed=; for it in $(MODULES); do \
	  echo "==> test $$it"; \
	  if [[ " $(GOFLAGS) " == *" -count=1 "* ]]; then \
	    GOMAXPROCS=$(GATE_JOBS) go -C $$it test -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) -timeout 30m ./... \
	      || failed="$${failed:+$$failed; }test $$it"; \
	    continue; \
	  fi; \
	  if ! listing=$$(GOMAXPROCS=$(GATE_JOBS) go -C $$it list -p=$(GATE_JOBS) -f '{{.ImportPath}} {{.Dir}}' ./...); then \
	    failed="$${failed:+$$failed; }test $$it (go list)"; continue; \
	  fi; \
	  cached= uncached=; \
	  while read -r ip dir; do \
	    [ -n "$$ip" ] || continue; \
	    hit=; for u in $(uncached-dirs); do [ "$$dir" = "$$u" ] && hit=1; done; \
	    if [ -n "$$hit" ]; then uncached="$$uncached $$ip"; else cached="$$cached $$ip"; fi; \
	  done <<< "$$listing"; \
	  if [ -n "$$cached" ]; then \
	    GOMAXPROCS=$(GATE_JOBS) go -C $$it test -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) -timeout 30m $$cached \
	      || failed="$${failed:+$$failed; }test $$it (cached pass)"; \
	  fi; \
	  if [ -n "$$uncached" ]; then \
	    GOMAXPROCS=$(GATE_JOBS) go -C $$it test -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) -count=1 -timeout 30m $$uncached \
	      || failed="$${failed:+$$failed; }test $$it (uncached pass)"; \
	  fi; \
	done; $(each-end)

test-race: modules-present ## Test every module under the race detector (unit cases only)
	@failed=; for it in $(MODULES); do \
	  echo "==> test -race $$it"; \
	  if [[ " $(GOFLAGS) " == *" -count=1 "* ]]; then \
	    GOMAXPROCS=$(GATE_JOBS) go -C $$it test -race -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) -timeout 60m ./... \
	      || failed="$${failed:+$$failed; }test -race $$it"; \
	    continue; \
	  fi; \
	  if ! listing=$$(GOMAXPROCS=$(GATE_JOBS) go -C $$it list -p=$(GATE_JOBS) -f '{{.ImportPath}} {{.Dir}}' ./...); then \
	    failed="$${failed:+$$failed; }test -race $$it (go list)"; continue; \
	  fi; \
	  cached= uncached=; \
	  while read -r ip dir; do \
	    [ -n "$$ip" ] || continue; \
	    hit=; for u in $(uncached-dirs); do [ "$$dir" = "$$u" ] && hit=1; done; \
	    if [ -n "$$hit" ]; then uncached="$$uncached $$ip"; else cached="$$cached $$ip"; fi; \
	  done <<< "$$listing"; \
	  if [ -n "$$cached" ]; then \
	    GOMAXPROCS=$(GATE_JOBS) go -C $$it test -race -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) -timeout 60m $$cached \
	      || failed="$${failed:+$$failed; }test -race $$it (cached pass)"; \
	  fi; \
	  if [ -n "$$uncached" ]; then \
	    GOMAXPROCS=$(GATE_JOBS) go -C $$it test -race -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) -count=1 -timeout 60m $$uncached \
	      || failed="$${failed:+$$failed; }test -race $$it (uncached pass)"; \
	  fi; \
	done; $(each-end)

test-integration: modules-present deps-up ## Test every module's integration cases
	@if [ ! -f '$(TEST_BACKENDS_STATE)' ]; then \
	  echo "test-integration: no backend state at '$(TEST_BACKENDS_STATE)'; run 'make deps-up' and read its output" >&2; \
	  exit 1; \
	fi; \
	backend_env="$$($(BACKENDS) env --state '$(TEST_BACKENDS_STATE)')" || exit 1; \
	set -a; eval "$$backend_env"; set +a; \
	failed=; $(call each,$(MODULES),test integration,GOMAXPROCS=$(GATE_JOBS) go -C $$it test -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) -count=1 -tags=integration -timeout 60m ./...); $(each-end)

test-integration-race: modules-present deps-up ## Test every module's integration cases under the race detector
	@if [ ! -f '$(TEST_BACKENDS_STATE)' ]; then \
	  echo "test-integration-race: no backend state at '$(TEST_BACKENDS_STATE)'; run 'make deps-up' and read its output" >&2; \
	  exit 1; \
	fi; \
	backend_env="$$($(BACKENDS) env --state '$(TEST_BACKENDS_STATE)')" || exit 1; \
	set -a; eval "$$backend_env"; set +a; \
	failed=; $(call each,$(MODULES),test integration -race,GOMAXPROCS=$(GATE_JOBS) go -C $$it test -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) -count=1 -race -tags=integration -timeout 90m ./...); $(each-end)

vet-integration: modules-present ## Compile every module's integration cases, running nothing
	@failed=; $(call each,$(MODULES),vet integration,GOMAXPROCS=$(GATE_JOBS) go -C $$it vet -p=$(GATE_JOBS) -tags=integration ./...); $(each-end)

vet-web-bundle: ## Compile the e2e-tagged build that embeds dist-e2e, running nothing
	@GOMAXPROCS=$(GATE_JOBS) go -C starters/demo/server vet -p=$(GATE_JOBS) -tags=e2e ./...

fmt: modules-present ## Format every module in place
	@failed=; $(call each,$(MODULES),fmt,GOMAXPROCS=$(GATE_JOBS) env -C $$it golangci-lint fmt ./...); $(each-end)

lint: modules-present ## Report formatting drift and static-analysis findings
	@failed=; $(call each,$(MODULES),lint fmt --diff,GOMAXPROCS=$(GATE_JOBS) env -C $$it golangci-lint fmt --diff ./...); \
	  $(call each,$(MODULES),lint run,GOMAXPROCS=$(GATE_JOBS) env -C $$it golangci-lint run --allow-parallel-runners --concurrency=$(GATE_JOBS) ./...); $(each-end)

tidy: modules-present ## Tidy every module's go.mod and go.sum in place
	@failed=; $(call each,$(MODULES),tidy,GOWORK=off GOMAXPROCS=$(GATE_JOBS) go -C $$it mod tidy); $(each-end)

tidy-check: modules-present ## Verify the dependency files are tidy, leaving the tree unchanged
	@failed=; $(call each,$(MODULES),tidy-check,GOWORK=off GOMAXPROCS=$(GATE_JOBS) go -C $$it mod tidy -diff); $(each-end)

actionlint: ## Lint every workflow and composite action script with actionlint and shellcheck
	@if [ -z "$(strip $(WORKFLOWS))" ]; then \
	  echo "actionlint: no workflow file under $(REPO_ROOT)/$(WORKFLOW_DIR); an empty directory would pass without checking anything." >&2; \
	  exit 1; \
	fi
	@if ! command -v '$(SHELLCHECK)' >/dev/null 2>&1; then \
	  echo "actionlint: '$(SHELLCHECK)' is not on PATH; without it actionlint skips every run: script. Install the version .mise.toml pins ('mise install')." >&2; \
	  exit 1; \
	fi
	@failed=; $(call each,workflows,actionlint,GOMAXPROCS=$(GATE_JOBS) env -C '$(REPO_ROOT)' '$(ACTIONLINT)' -shellcheck='$(SHELLCHECK)' $(WORKFLOWS)); \
	  $(call each,composite-actions,actionlint,$(PYTHON) '$(LINT_COMPOSITE_ACTIONS)' --root '$(REPO_ROOT)' --shellcheck '$(SHELLCHECK)'); $(each-end)

repo-check: python-version ## Run the gates in tools/ and the workflow lint over this tree
	@failed=; $(call each,$(GATES),gate,GOMAXPROCS=$(GATE_JOBS) GOFLAGS="$(GOFLAGS) -p=$(GATE_JOBS)" $(PYTHON) $$it); \
	  $(call each,actionlint,gate,$(MAKE) --no-print-directory actionlint); $(each-end)

tools-test: python-version ## Run the test suites of the scripts in tools/
	@GOMAXPROCS=$(GATE_JOBS) GOFLAGS="$(GOFLAGS) -p=$(GATE_JOBS)" $(PYTHON) -m unittest discover -s tools -t tools -p 'test_*.py'

modules-present:
	@if [ -z "$(strip $(MODULES))" ]; then \
	  echo "no modules: go.work lists nothing this build can read." >&2; \
	  echo "Every check loops over that roster, so an empty one would pass without checking anything." >&2; \
	  exit 1; \
	fi

python-version:
	@$(PYTHON) -c 'import sys; sys.exit(0 if sys.version_info >= (3, 11) else 1)' || { \
	  echo "tools/ needs Python 3.11 or newer (tomllib); $(PYTHON) is $$($(PYTHON) -V 2>&1)." >&2; \
	  echo "Install the version .mise.toml pins, or pass PYTHON=<interpreter>." >&2; \
	  exit 1; \
	}

web-check: ## Run the frontend checks (pnpm install + lint, typecheck, rule tests, test, a11y, build)
	@$(MAKE) web-check-static
	@$(MAKE) web-check-test
	@$(MAKE) web-check-build

web-check-static: ## Install the frontend dependencies and run its static checks
	@pnpm -C web install --frozen-lockfile --workspace-concurrency=1 --child-concurrency=$(GATE_JOBS)
	@pnpm -C web -r --workspace-concurrency=$(GATE_JOBS) --no-sort lint --concurrency=off
	@pnpm -C web -r --workspace-concurrency=$(GATE_JOBS) --no-sort typecheck
	@pnpm -C web exec vitest run --maxWorkers=$(GATE_JOBS) eslint-rules/ scripts/

web-check-test: ## Run the frontend's unit tests and route-level accessibility sweep
	@pnpm -C web exec vitest run --config vitest.unit.config.ts --maxWorkers=$(GATE_JOBS)
	@$(MAKE) web-a11y

web-check-build: ## Build every frontend package
	@pnpm -C web -r --filter !@speed/test-utils --filter !demo-web --fail-if-no-match --workspace-concurrency=$(GATE_JOBS) run build:self
	@pnpm -C web --filter demo-web --fail-if-no-match exec vite build

web-a11y: ## Run the frontend's route-level accessibility sweep
	@pnpm -C web --filter demo-web --fail-if-no-match run test:a11y --maxWorkers=$(GATE_JOBS)

web-browser: ## Run the frontend's browser tier in Chromium (geometry, focus order, focusability)
	@pnpm -C web exec vitest run --config vitest.browser.config.ts --maxWorkers=$(GATE_JOBS)
	@pnpm -C web exec vitest run --config vitest.browser.injected-scale.config.ts --maxWorkers=$(GATE_JOBS)

e2e-install: ## Install the Chromium and WebKit builds the end-to-end tier drives
	@pnpm -C web exec playwright install chromium webkit

e2e: ## Run the end-to-end journey specs against the built demo binary
	@pnpm -C web --filter demo-web --fail-if-no-match run typecheck:e2e
	@GATE_JOBS=$(GATE_JOBS) pnpm -C web --filter demo-web --fail-if-no-match exec playwright test --config e2e/playwright.config.ts --workers=$(GATE_JOBS)

dev: ## Run the demo locally: vite dev server plus the default build under air
	@if ! command -v mise >/dev/null 2>&1; then \
	  echo "dev: mise is not on PATH. It supplies .env and the pinned toolchain; install it and run 'mise install'." >&2; \
	  exit 1; \
	fi; \
	mise exec -- pnpm -C web --filter demo-web --fail-if-no-match run dev & \
	vite=$$!; \
	trap 'kill $$vite 2>/dev/null || true; wait $$vite 2>/dev/null || true' EXIT INT TERM; \
	mise exec -- env -C starters/demo/server air -c .air.toml

fly: ## Build and release the demo program to Fly.io
	@flyctl deploy . --config starters/demo/fly.toml --remote-only

check: ## Run everything a commit is gated on (no external service needed)
check: check-lint check-tidy-check check-repo-check check-tools-test \
       check-vet-web-bundle check-web-check-static \
       check-build check-vet-integration check-test \
       check-web-check-test check-test-race \
       check-web-check-build

check-announce:
	@echo "== check GATE_JOBS=$(GATE_JOBS) $$(date '+%H:%M:%S') -- the CPU share this run may use; set GATE_JOBS (default 4) to what was allocated to it"

check-lint: check-announce
	$(call mark,lint)
	@set -o pipefail; $(MAKE) --no-print-directory lint $(call segment-output,lint)

check-tidy-check: check-announce
	$(call mark,tidy-check)
	@set -o pipefail; $(MAKE) --no-print-directory tidy-check $(call segment-output,tidy-check)

check-repo-check: check-announce
	$(call mark,repo-check)
	@set -o pipefail; $(MAKE) --no-print-directory repo-check $(call segment-output,repo-check)

check-tools-test: check-announce
	$(call mark,tools-test)
	@set -o pipefail; $(MAKE) --no-print-directory tools-test $(call segment-output,tools-test)

check-vet-web-bundle: check-announce
	$(call mark,vet-web-bundle)
	@set -o pipefail; $(MAKE) --no-print-directory vet-web-bundle $(call segment-output,vet-web-bundle)

check-web-check-static: check-announce
	$(call mark,web-check-static)
	@set -o pipefail; $(MAKE) --no-print-directory web-check-static $(call segment-output,web-check-static)

check-web-check-build: check-announce | check-web-check-static check-build check-vet-integration check-test check-test-race
	$(call mark,web-check-build)
	@set -o pipefail; $(MAKE) --no-print-directory web-check-build $(call segment-output,web-check-build)

check-build: check-announce
	$(call mark,build)
	@set -o pipefail; $(MAKE) --no-print-directory build $(call segment-output,build)

check-vet-integration: check-announce
	$(call mark,vet-integration)
	@set -o pipefail; $(MAKE) --no-print-directory vet-integration $(call segment-output,vet-integration)

check-test: check-announce
	$(call mark,test)
	@set -o pipefail; $(MAKE) --no-print-directory test $(call segment-output,test)

check-web-check-test: check-announce | check-web-check-static
	$(call mark,web-check-test)
	@set -o pipefail; $(MAKE) --no-print-directory web-check-test $(call segment-output,web-check-test)

check-test-race: check-announce
	$(call mark,test-race)
	@set -o pipefail; $(MAKE) --no-print-directory test-race $(call segment-output,test-race)
