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

# The build tags whose cases the commit gate executes rather than merely
# compiles. A tag listed here needs, among the legs `check` reaches, a test leg
# that passes the tag and names its cases with -run: the tagged cases are what
# the leg is for, and a leg without the pattern would re-run the unit cases as
# well. tools/check_test_tiers.py holds this roster to the tree.
#
# `review` is the only tag left with a build variant: it adds the demo
# program's test controls and loopback guard to the default build. The
# capability roster no longer has a tag — every capability is compiled into the
# default build and enabled at run time from configuration — so there is no
# deploy leg to run.
GATED_TEST_TAGS := review

# The modules whose tree carries a file compiled under a gated tag. A module
# with none has no case for the tagged leg to select, and running the leg there
# would build every test binary for nothing. The match is on the constraint
# text; a path found here is the directory holding the tagged file, and the
# leg runs from there, so a tagged case in a subpackage still runs with its
# own package.
REVIEW_MODULES := $(shell paths='$(strip $(MODULES))'; if [ -n "$$paths" ]; then grep -rlE --include='*.go' '^//go:build .*review' $$paths 2>/dev/null | sed -E 's|/[^/]+$$||' | sort -u; fi)

# The packages that carry an OpenAPI generation test. A package without one
# declares no API document, and running the generator's pattern there would
# build every test binary for nothing.
FRAGMENT_PACKAGES := $(shell paths='$(strip $(MODULES))'; if [ -n "$$paths" ]; then grep -rl --include='openapi_generated_test.go' '^package ' $$paths 2>/dev/null | sed -E 's|/[^/]+$$||' | sort -u; fi)

# Every tools/check_*.py is a gate over this tree: it takes no arguments, it
# reports against the current directory, and it is run by `make repo-check`.
# A script under tools/ that is not a gate carries another prefix.
GATES := $(wildcard tools/check_*.py)

PYTHON ?= python3

# The integration stack's state file and the script that maintains it. The
# script allocates this checkout's ports and records them with its compose
# project name; the two variables let a run point the state elsewhere and let
# the Makefile's own suite stand in for the script instead of starting a
# daemon.
TEST_BACKENDS_STATE ?= .cache/test-backends.env
BACKENDS ?= $(PYTHON) tools/backends.py

# Every leg that loops over a list -- the modules, or the gates -- runs each
# item even after an earlier one failed, so one module's red result does not
# hide another's, and fails at the end naming each item that failed.
# `$(call each,<items>,<heading>,<command>)` runs <command> once per item
# with $$it naming the item; `$(each-end)` fails the recipe if any run
# failed. A recipe starts from `failed=;` and keeps every loop and the end
# on one shell line, so several loops in it report together.
each = for it in $(1); do echo "==> $(2) $$it"; $(3) || failed="$${failed:+$$failed; }$(2) $$it"; done
each-end = if [ -n "$$failed" ]; then echo "failed: $$failed" >&2; exit 1; fi

.PHONY: help modules deps-up deps-down build api-docs test test-race test-integration test-integration-race \
        vet-integration vet-review test-review fmt lint tidy tidy-check repo-check tools-test check python-version \
        modules-present web-check web-a11y web-browser e2e e2e-install dev fly

# A target name may carry a digit (web-a11y, e2e), so the name pattern admits
# one; the old [a-z-] pattern silently dropped every such entry point from the
# listing.
help: ## List the entry points
	@awk 'BEGIN{FS=":.*## "} /^[a-z0-9][a-z0-9-]*:.*## /{printf "  %-21s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

modules: modules-present ## Print the module directories derived from go.work
	@for m in $(MODULES); do echo $$m; done

build: modules-present ## Build every module, both in the workspace and standalone
	@failed=; $(call each,$(MODULES),build,go -C $$it build ./...); \
	  $(call each,$(MODULES),build standalone,GOWORK=off go -C $$it build ./...); $(each-end)

# The API document is a build product: a module's fragment comes from its
# operation declarations, and the merged contract comes from the fragments.
# This regenerates both. `make check` holds the committed files to the
# declarations, so a stale file fails there rather than here.
api-docs: modules-present ## Regenerate every OpenAPI fragment and the merged contract
	@failed=; $(call each,$(FRAGMENT_PACKAGES),fragment,env UPDATE_FRAGMENTS=1 go -C $$it test ./... -run '^TestTheFragmentIsGeneratedFromTheDeclarations$$' -count=1); $(each-end)
	@go -C starters/demo/server run . --config file:testdata/sqlite-memory.yaml sqlite-memory gen \
	  --title speed -o ../../../contracts/api.json --features ../../../contracts/features.json

# The four backends the integration cases run against -- PostgreSQL, Redis,
# NATS and memcached -- are the test environment, not part of the tests: the
# compose file at the repository root declares them, this runs the script that
# brings a stack of their own up for this checkout, and every case that needs
# them only connects. A case that cannot reach its backend fails rather than
# skipping, which is why the integration legs below depend on this and the unit
# legs above do not.
#
# tools/backends.py names the compose project after the checkout root, so two
# checkouts (or a machine already holding the default ports) do not share or
# fight over one stack. It records the project name and the allocated ports in
# $(TEST_BACKENDS_STATE) and waits for every healthcheck, so the cases never
# race a container that is up but still initialising.
deps-up: ## Bring this checkout's integration backends up
	@$(BACKENDS) up --state '$(TEST_BACKENDS_STATE)'

deps-down: ## Take this checkout's integration backends down
	@$(BACKENDS) down --state '$(TEST_BACKENDS_STATE)'

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
#
# The addresses come from the state file deps-up just wrote, exported into the
# same shell that runs go test. A missing state file fails here instead of
# letting the fixtures fall back to the default ports, where they would reach
# whichever stack owns them -- another checkout's, or none.
test-integration: modules-present deps-up ## Test every module's integration cases
	@if [ ! -f '$(TEST_BACKENDS_STATE)' ]; then \
	  echo "test-integration: no backend state at '$(TEST_BACKENDS_STATE)'; run 'make deps-up' and read its output" >&2; \
	  exit 1; \
	fi; \
	backend_env="$$($(BACKENDS) env --state '$(TEST_BACKENDS_STATE)')" || exit 1; \
	set -a; eval "$$backend_env"; set +a; \
	failed=; $(call each,$(MODULES),test integration,go -C $$it test -tags=integration -timeout 60m ./...); $(each-end)

test-integration-race: modules-present deps-up ## Test every module's integration cases under the race detector
	@if [ ! -f '$(TEST_BACKENDS_STATE)' ]; then \
	  echo "test-integration-race: no backend state at '$(TEST_BACKENDS_STATE)'; run 'make deps-up' and read its output" >&2; \
	  exit 1; \
	fi; \
	backend_env="$$($(BACKENDS) env --state '$(TEST_BACKENDS_STATE)')" || exit 1; \
	set -a; eval "$$backend_env"; set +a; \
	failed=; $(call each,$(MODULES),test integration -race,go -C $$it test -race -tags=integration -timeout 90m ./...); $(each-end)

# Compiles the tagged files without running them, so the unit gate still says
# whether they build. Without it a change that breaks an integration case
# compiles at every commit and is only found on the scheduled run.
vet-integration: modules-present ## Compile every module's integration cases, running nothing
	@failed=; $(call each,$(MODULES),vet integration,go -C $$it vet -tags=integration ./...); $(each-end)

# The review build is the default build plus the demo program's test controls and
# loopback guard: its files carry the `review` tag and no leg above compiles
# them. Compiling it here keeps the controls from rotting into not building, and
# gives the tag the leg the test-tier gate requires of every tag written in the
# tree.
vet-review: modules-present ## Compile the review build, running nothing
	@failed=; $(call each,$(MODULES),vet review,go -C $$it vet -tags=review ./...); $(each-end)

# The cases compiled only under the review tag are executed here: vet-review
# above only builds them, so deleting or breaking the review guard would leave
# every leg green. `-run` selects the TestReview prefix, which is why the leg
# does not run the module's unit cases a second time; a review-tagged case whose
# name falls outside the prefix is held to it by tools/check_test_tiers.py.
test-review: modules-present ## Test the cases compiled only under the review tag
	@if [ -z "$(strip $(REVIEW_MODULES))" ]; then \
	  echo "no module carries a review-tagged Go file: the review cases are gone, which is not a pass." >&2; \
	  exit 1; \
	fi
	@failed=; $(call each,$(REVIEW_MODULES),test review,go -C $$it test -tags=review -run '^TestReview' -timeout 10m ./...); $(each-end)

# `golangci-lint run` takes a process-wide lock by default, so two checkouts
# running the gate at once make one of them die with "parallel golangci-lint
# is running" — indistinguishable from a code failure. The flag lets
# concurrent runs proceed; the cost is contention, not a wrong answer. `fmt`
# takes no lock and rejects the flag.
fmt: modules-present ## Format every module in place
	@failed=; $(call each,$(MODULES),fmt,env -C $$it golangci-lint fmt ./...); $(each-end)

lint: modules-present ## Report formatting drift and static-analysis findings
	@failed=; $(call each,$(MODULES),lint fmt --diff,env -C $$it golangci-lint fmt --diff ./...); \
	  $(call each,$(MODULES),lint run,env -C $$it golangci-lint run --allow-parallel-runners ./...); $(each-end)

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
# The eslint-rules unit tests and the workspace configuration guards are their
# own leg for the same reason web/ is its own workspace root: they live outside
# every package, so the root vitest (not any workspace member's `test` script)
# is what runs them. Without this line the rules' own tests have no automatic
# execution path -- the lint leg exercises the rules only over the code that
# happens to exist -- and the guard that every test-running package spreads the
# shared test preset (web/scripts/workspace-test-config.test.mjs) would not run
# at all.
#
# The build leg runs its package scripts one at a time. Each package's script
# rebuilds the workspace siblings it imports before compiling itself, so a
# concurrent -r run has several packages writing the same sibling's dist/ at
# once, and a compiler that reads a half-written declaration file fails with
# "is not a module". The redundancy is the package scripts' own (a standalone
# package build has to bring its dependencies along); serialising the run is
# what makes the gate repeatable.
web-check: ## Run the frontend checks (pnpm install + lint, typecheck, rule tests, test, a11y, build)
	@pnpm -C web install --frozen-lockfile
	@pnpm -C web -r lint
	@pnpm -C web -r typecheck
	@pnpm -C web exec vitest run eslint-rules/ scripts/
	@pnpm -C web -r test
	@$(MAKE) web-a11y
	@pnpm -C web -r --workspace-concurrency=1 build

web-a11y: ## Run the frontend's route-level accessibility sweep
	@pnpm -C web --filter demo-web run test:a11y

# The browser tier: the cases whose subject is real geometry, focus order
# or focusability, in Chromium through Vitest's browser mode. Chromium is
# not part of the .mise.toml toolchain -- that is why this tier is
# separate -- so a machine runs `pnpm -C web exec playwright install
# chromium` once; CI installs it before calling this entry. The tier is
# not part of `make check`; a scheduled workflow calls this same entry
# (see .github/workflows/browser.yml).
web-browser: ## Run the frontend's browser tier in Chromium (geometry, focus order, focusability)
	@pnpm -C web exec vitest run --config vitest.browser.config.ts

# The end-to-end tier: Playwright drives the built demo binary through a real
# browser, against a real sqlite file and real HTTP. It is deliberately not
# part of `check` -- it needs Chromium and a built backend and is slower than
# the commit gate -- and tools/check_e2e_honesty.py holds that `check` does not
# reach it. The harness builds the frontend and the review binary into /tmp in
# its global setup; `e2e-install` provisions the one browser it drives.
e2e-install: ## Install the Chromium build the end-to-end tier drives
	@pnpm -C web exec playwright install chromium

e2e: ## Run the end-to-end journey specs against the built demo binary
	@pnpm -C starters/demo/web run typecheck:e2e
	@pnpm -C starters/demo/web exec playwright test --config e2e/playwright.config.ts

# The local development loop: the frontend's vite dev server in the background
# and the Go program under air in the foreground. Air builds the default binary
# and starts it with no --config (starters/demo/server/.air.toml), so a local
# run reads the defaults compiled into the binary plus whatever `mise exec`
# loads from .env, the file .env.example documents; a variable set there
# overrides the compiled-in default for this run. The review tag belongs to the
# end-to-end tier, not this loop, so its test controls and loopback guard are
# absent here. Ctrl-C stops air with the rest of the foreground group; the
# recipe then stops the vite job too, so one interrupt ends both. The compiled
# defaults put the backend and its ops probes on 127.0.0.1:8081 and the dev
# server on 8080; a second checkout sets DEMO_HTTP__LISTENERS, DEMO_WEB_PORT and
# DEMO_WEB_BACKEND_PROXY in its own .env (the pairs .env.example shows).
dev: ## Run the demo locally: vite dev server plus the default build under air
	@if ! command -v mise >/dev/null 2>&1; then \
	  echo "dev: mise is not on PATH. It supplies .env and the pinned toolchain; install it and run 'mise install'." >&2; \
	  exit 1; \
	fi; \
	mise exec -- pnpm -C starters/demo/web run dev & \
	vite=$$!; \
	trap 'kill $$vite 2>/dev/null || true; wait $$vite 2>/dev/null || true' EXIT INT TERM; \
	mise exec -- env -C starters/demo/server air -c .air.toml

# The fly leg releases the demo program to Fly.io. It is deliberately not
# part of `check`: it needs a Fly account and the FLY_API_TOKEN secret, and the
# workflow that calls it (.github/workflows/deploy.yml) runs only after `check`
# passed on main. flyctl is deployment machinery rather than a build input, so
# .mise.toml does not pin it; the workflow installs the pinned version and this
# target calls what is on PATH.
#
# The working directory is the repository root because the image compiles the
# frontend workspace under web/ and the Go workspace go.work declares, both of
# which sit outside starters/demo. The Dockerfile path lives in fly.toml and is
# relative to that file.
fly: ## Build and release the demo program to Fly.io
	@flyctl deploy . --config starters/demo/fly.toml --remote-only

check: ## Run everything a commit is gated on (no external service needed)
	@echo "check: run one gate per machine at a time — the lint leg now tolerates a concurrent runner, but two full gates starve the test processes and fail them on the clock, not on an assertion"
	@$(MAKE) build
	@$(MAKE) test
	@$(MAKE) test-race
	@$(MAKE) vet-integration
	@$(MAKE) vet-review
	@$(MAKE) test-review
	@$(MAKE) lint
	@$(MAKE) tidy-check
	@$(MAKE) repo-check
	@$(MAKE) tools-test
	@$(MAKE) web-check
