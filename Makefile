# Every check this repository runs has its entry point here. CI installs the
# toolchain and calls `make check`; no other file repeats these commands.
#
# The module set is derived from go.work, the repository's only machine-readable
# module roster, so adding a module to the workspace registers it everywhere.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# A use directive has two legal spellings -- a parenthesised block and a
# bare `use ./dir` line -- so the roster is parsed by the go toolchain
# itself rather than by a matcher here that would know only one of them
# and silently come up empty on the other.
MODULES := $(shell go work edit -json go.work | sed -n 's/^[[:space:]]*"DiskPath": "\(.*\)".*/\1/p')

# Every tools/check_*.py is a gate over this tree: it takes no arguments, it
# reports against the current directory, and it is run by `make repo-check`.
# A script under tools/ that is not a gate carries another prefix.
GATES := $(wildcard tools/check_*.py)

PYTHON ?= python3

# Every leg that loops over a list -- the modules, or the gates -- runs each
# item even after an earlier one failed, so one module's red result does not
# hide another's, and fails at the end naming each item that failed.
# `$(call each,<items>,<heading>,<command>)` runs <command> once per item
# with $$it naming the item; `$(each-end)` fails the recipe if any run
# failed. A recipe starts from `failed=;` and keeps every loop and the end
# on one shell line, so several loops in it report together.
each = for it in $(1); do echo "==> $(2) $$it"; $(3) || failed="$${failed:+$$failed; }$(2) $$it"; done
each-end = if [ -n "$$failed" ]; then echo "failed: $$failed" >&2; exit 1; fi

.PHONY: help modules deps-up deps-down build test test-race test-integration test-integration-race \
        vet-integration fmt lint tidy tidy-check repo-check tools-test check python-version modules-present web-check web-a11y

help: ## List the entry points
	@awk 'BEGIN{FS=":.*## "} /^[a-z][a-z-]*:.*## /{printf "  %-21s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

modules: modules-present ## Print the module directories derived from go.work
	@for m in $(MODULES); do echo $$m; done

build: modules-present ## Build every module, both in the workspace and standalone
	@failed=; $(call each,$(MODULES),build,go -C $$it build ./...); \
	  $(call each,$(MODULES),build standalone,GOWORK=off go -C $$it build ./...); $(each-end)

# The four backends the integration cases run against -- PostgreSQL, Redis,
# NATS and memcached -- are the test environment, not part of the tests: the
# compose file at the repository root declares them, this brings that stack up,
# and every case that needs them only connects. A case that cannot reach its
# backend fails rather than skipping, which is why the integration legs below
# depend on this and the unit legs above do not.
#
# `up --wait` returns once every service's healthcheck passes, so the cases
# never race a container that is up but still initialising.
deps-up: ## Bring the integration cases' backends up
	@docker compose up -d --wait

deps-down: ## Take the integration cases' backends down
	@docker compose down

# A test binary's own deadline bounds how long one module's suite may run; go
# test's default is ten minutes, which a suite that drives a whole program end
# to end exceeds under the race detector on a loaded machine. The bound is
# generous on purpose: it is there to end a hung run, not to pace a slow one.
#
# A case that needs one of the backends carries the `integration` build tag, so
# the two legs here run everything that needs nothing installed -- which is what
# a commit is gated on. The tagged files are still compiled here, by
# vet-integration below: a leg that stopped at "does not run" would let them rot
# into not compiling at all.
test: modules-present ## Test every module (unit cases only)
	@failed=; $(call each,$(MODULES),test,go -C $$it test -timeout 30m ./...); $(each-end)

test-race: modules-present ## Test every module under the race detector (unit cases only)
	@failed=; $(call each,$(MODULES),test -race,go -C $$it test -race -timeout 60m ./...); $(each-end)

# The integration cases: they need the backends above, they are slower by
# orders of magnitude, and they are run on a schedule rather than on every
# commit. `make check` does not reach them.
test-integration: modules-present deps-up ## Test every module's integration cases
	@failed=; $(call each,$(MODULES),test integration,go -C $$it test -tags=integration -timeout 60m ./...); $(each-end)

test-integration-race: modules-present deps-up ## Test every module's integration cases under the race detector
	@failed=; $(call each,$(MODULES),test integration -race,go -C $$it test -race -tags=integration -timeout 90m ./...); $(each-end)

# Compiles the tagged files without running them, so the unit gate still says
# whether they build. Without it a change that breaks an integration case
# compiles at every commit and is only found on the scheduled run.
vet-integration: modules-present ## Compile every module's integration cases, running nothing
	@failed=; $(call each,$(MODULES),vet integration,go -C $$it vet -tags=integration ./...); $(each-end)

fmt: modules-present ## Format every module in place
	@failed=; $(call each,$(MODULES),fmt,env -C $$it golangci-lint fmt ./...); $(each-end)

lint: modules-present ## Report formatting drift and static-analysis findings
	@failed=; $(call each,$(MODULES),lint fmt --diff,env -C $$it golangci-lint fmt --diff ./...); \
	  $(call each,$(MODULES),lint run,env -C $$it golangci-lint run ./...); $(each-end)

tidy: modules-present ## Tidy every module's go.mod and go.sum in place
	@failed=; $(call each,$(MODULES),tidy,GOWORK=off go -C $$it mod tidy); $(each-end)

# `go mod tidy -diff` reports what tidying would change without writing
# anything, so the judgement is the dependency files themselves. Judging
# them by the working tree's git status instead both misses an uncommitted
# untidy file (tidying restores the committed bytes, leaving the tree
# clean) and fails on any unrelated edit to a go.mod.
tidy-check: modules-present ## Verify the dependency files are tidy, leaving the tree unchanged
	@failed=; $(call each,$(MODULES),tidy-check,GOWORK=off go -C $$it mod tidy -diff); $(each-end)

repo-check: python-version ## Run the gates in tools/ over this tree
	@failed=; $(call each,$(GATES),gate,$(PYTHON) $$it); $(each-end)

tools-test: python-version ## Run the test suites of the scripts in tools/
	@$(PYTHON) -m unittest discover -s tools -t tools -p 'test_*.py'

# Every leg above loops over $(MODULES), and a loop over an empty list is
# a no-op that exits 0 -- an empty roster would take the whole of `make
# check` through to success without running a single check. No leg runs
# before this says the roster is non-empty.
modules-present:
	@if [ -z "$(strip $(MODULES))" ]; then \
	  echo "no modules: go.work lists nothing this build can read." >&2; \
	  echo "Every check loops over that roster, so an empty one would pass without checking anything." >&2; \
	  exit 1; \
	fi

# The scripts parse TOML with tomllib, which arrived in Python 3.11.
python-version:
	@$(PYTHON) -c 'import sys; sys.exit(0 if sys.version_info >= (3, 11) else 1)' || { \
	  echo "tools/ needs Python 3.11 or newer (tomllib); $(PYTHON) is $$($(PYTHON) -V 2>&1)." >&2; \
	  echo "Install the version .mise.toml pins, or pass PYTHON=<interpreter>." >&2; \
	  exit 1; \
	}

# The frontend is its own pnpm workspace under web/ (see web/README.md for
# why it is a separate root). `check` runs it like every other leg: the root
# .mise.toml pins the Node and pnpm it needs, so `mise install` provides the
# toolchain and both halves of the tree share one entry point.
#
# The route-level accessibility sweep (web-a11y) is its own leg beside lint,
# typecheck and test: it renders the assembled app at every route in three data
# states and runs axe-core over each, so it is slower than the unit suites and
# produces a report of its own. The demo host's default `test` run excludes
# src/a11y/** and this leg runs only it (starters/demo/web/vitest.a11y.config.ts),
# so the sweep is not run twice.
#
# The eslint-rules unit tests are their own leg for the same reason web/ is its
# own workspace root: the rules live outside every package, so the root vitest
# (not any workspace member's `test` script) is what runs them. Without this
# line the rules' own tests have no automatic execution path -- the lint leg
# exercises the rules only over the code that happens to exist.
web-check: ## Run the frontend checks (pnpm install + lint, typecheck, rule tests, test, a11y, build)
	@pnpm -C web install --frozen-lockfile
	@pnpm -C web -r lint
	@pnpm -C web -r typecheck
	@pnpm -C web exec vitest run eslint-rules/
	@pnpm -C web -r test
	@$(MAKE) web-a11y
	@pnpm -C web -r build

web-a11y: ## Run the frontend's route-level accessibility sweep
	@pnpm -C web --filter demo-web run test:a11y

check: ## Run everything a commit is gated on (no external service needed)
	@$(MAKE) build
	@$(MAKE) test
	@$(MAKE) test-race
	@$(MAKE) vet-integration
	@$(MAKE) lint
	@$(MAKE) tidy-check
	@$(MAKE) repo-check
	@$(MAKE) tools-test
	@$(MAKE) web-check
