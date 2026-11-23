# Every check this repository runs has its entry point here. CI installs the
# toolchain and runs the legs of `make check` as parallel jobs, each through
# this Makefile; no other file repeats these commands.
#
# The module set is derived from go.work, the repository's only machine-readable
# module roster, so adding a module to the workspace registers it everywhere.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# The directory this Makefile sits in. A target that reads files by path
# resolves them from here, so it behaves the same whichever directory make was
# started from.
REPO_ROOT := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))

# The script the actionlint leg lints composite actions with. It lives beside
# this Makefile and is found from there, while REPO_ROOT names the tree it reads
# and can point elsewhere.
LINT_COMPOSITE_ACTIONS := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))/tools/lint_composite_actions.py

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

# The CPU budget one gate run may use, in cores, as a number the caller gives
# it. Every tool the gate runs that offers a width setting is told this width
# instead of working one out from the host: go build, go vet and go test take
# -p and GOMAXPROCS (tests also -parallel), golangci-lint run takes
# --concurrency and its internal go list follows GOMAXPROCS, vitest takes
# --maxWorkers, Playwright takes --workers and pnpm -r takes
# --workspace-concurrency. Three tools carry no width this file can set:
# `golangci-lint fmt` has no flag for it, tsc is single-threaded, and the
# rolldown thread pool `vite build` runs on offers no documented setting (its
# only knob is the runtime's RAYON_NUM_THREADS, which no rolldown API
# promises), so those take whatever the runtime decides. A scan of the
# machine is wrong where it matters most: inside a container or a pod a
# process sees the node's cores, not the share it was given. There is no
# probing and no host-derived default; pass the share by variable or by
# environment variable of the same name.
#
# Where a leg nests two of those widths, the product is what must stay within
# the budget, not either factor alone. pnpm -r starting several package scripts
# and each script starting its own vitest process is the case in this tree: a
# workspace concurrency of GATE_JOBS with a GATE_JOBS-wide vitest behind each
# package would use GATE_JOBS squared. Such a leg runs its packages one at a
# time (--workspace-concurrency=1) and gives each process the whole budget
# (--maxWorkers=$(GATE_JOBS)). A leg that starts a single process (web-a11y,
# web-browser) has no package width to multiply, so its per-process worker
# count is already the budget.
#
# `make -j N` and this number multiply. With -j N up to N segments run at
# once, each able to use GATE_JOBS cores, so a caller that runs parallel
# segments divides its CPU budget between the two. Without -j the segments run
# one at a time and GATE_JOBS is the whole width.
#
# In go test, -p bounds package binaries in flight, -parallel bounds cases in
# flight inside one binary (only cases calling t.Parallel count) and
# GOMAXPROCS bounds the threads each binary schedules. The three multiply in
# principle, so all three take the same number and the product stays near it
# in this tree: the module with many packages (pkg) has few parallel cases,
# and the module whose suite is genuinely parallel (the demo server) has a
# handful of packages, only one of which carries that suite. GATE_JOBS is that
# one number.
#
# The same -p bounds the compile phase, which is why build and the vet legs
# take it too: a test or build run compiles that many packages at once, and
# each compile is a process of its own that GOMAXPROCS bounds, so the two
# together are the width of the compile as well.
#
# The demo program's cases start their child processes through childEnv, which
# drops only the test prefix and the OpenTelemetry variables, so a child
# inherits GOMAXPROCS=$(GATE_JOBS) and schedules up to that width itself. A
# case that starts several children, or several such cases running at once,
# therefore uses this width times the children in flight; the number is a
# per-process budget, not a ceiling on the leg.
GATE_JOBS ?= 4

# The caller's multiplier for the test tiers' wait limits: Vitest's shared
# test and hook budget (web/vitest.shared.ts), Testing Library's async budget
# and vi.waitFor/vi.waitUntil through the shared waitForCondition helper
# (web/test-utils/src/setup.ts, web/test-utils/src/wait.ts), and the Playwright
# end-to-end and Go test tiers. The limits bound a wait, so a run that shares
# the machine with other heavy work raises this and a real hang still fails; no
# limit is derived from the machine's load or core count. The value reaches the
# suites through the environment. Unset or empty means 1; the suites refuse any
# other value that is not a finite number of at least 1, so a typo fails the run
# instead of falling back to 1.
GATE_TIME_SCALE ?= 1
export GATE_TIME_SCALE

# Go caches a passing test result and replays it while the test's inputs are
# unchanged. Those inputs are the package's files, the environment variables
# the test read and the files it opened under the module root; a file read
# outside the module root is not tracked. Two things in this tree escape that.
# The demo program's tests read the committed contracts under ../../../contracts,
# so editing a contract leaves the cached result standing, and the unit legs run
# that module uncached. The integration cases read the addresses of the backend
# stack, and a reused stack on the same addresses replays the earlier run
# instead of exercising the backends, so both integration legs run every module
# uncached. The other modules read nothing outside their root and keep the cache
# on the unit legs, where it is what makes a rerun cheap.
UNCACHED_MODULES ?= ./starters/demo/server
uncached = $$([[ ' $(UNCACHED_MODULES) ' == *" $$it "* ]] && echo -count=1)

# The integration stack's state file and the script that maintains it. The
# script allocates this checkout's ports and records them with its compose
# project name; the two variables let a run point the state elsewhere and let
# the Makefile's own suite stand in for the script instead of starting a
# daemon.
TEST_BACKENDS_STATE ?= .cache/test-backends.env
BACKENDS ?= $(PYTHON) tools/backends.py

# The workflow files the actionlint leg reads: every .yml and .yaml directly
# under .github/workflows, the files GitHub runs as workflows, named relative
# to the repository root so a finding reads as .github/workflows/<file>.
# ACTIONLINT and SHELLCHECK name the two executables. They are looked up on PATH
# like golangci-lint, and .mise.toml pins them, so the pinned versions run when
# make runs under mise. A run can point them elsewhere, and the Makefile's own
# suite stands in for them instead of running the real linters.
WORKFLOW_DIR := .github/workflows
WORKFLOWS := $(sort $(patsubst $(REPO_ROOT)/%,%,$(wildcard $(REPO_ROOT)/$(WORKFLOW_DIR)/*.yml $(REPO_ROOT)/$(WORKFLOW_DIR)/*.yaml)))
ACTIONLINT ?= actionlint
SHELLCHECK ?= shellcheck

# Every leg that loops over a list -- the modules, or the gates -- runs each
# item even after an earlier one failed, so one module's red result does not
# hide another's, and fails at the end naming each item that failed.
# `$(call each,<items>,<heading>,<command>)` runs <command> once per item
# with $$it naming the item; `$(each-end)` fails the recipe if any run
# failed. A recipe starts from `failed=;` and keeps every loop and the end
# on one shell line, so several loops in it report together.
each = for it in $(1); do echo "==> $(2) $$it"; $(3) || failed="$${failed:+$$failed; }$(2) $$it"; done
each-end = if [ -n "$$failed" ]; then echo "failed: $$failed" >&2; exit 1; fi

# A check segment announces itself with the clock time it starts, so a log of
# a run shows how long each segment took without timing the run by hand. The
# timestamp is read by the shell when the line runs, not when the Makefile is
# parsed.
mark = @echo "== $(1) $$(date '+%H:%M:%S')"

# A check segment's output is prefixed with the segment's name, so segments
# running at once can still be told apart in one log. GNU Make 3.81 has no
# --output-sync; awk is here because its fflush is portable where an
# unbuffered sed is not, and the pipe keeps the child's own exit status
# through `set -o pipefail`.
segment-output = 2>&1 | awk '{ printf "[$(1)] %s\n", $$0; fflush() }'

.PHONY: help modules deps-up deps-down build api-docs test test-race test-integration test-integration-race \
        vet-integration vet-review vet-web-bundle test-review fmt lint tidy tidy-check actionlint repo-check tools-test check python-version \
        modules-present web-check web-check-static web-check-test web-check-build web-a11y web-browser e2e e2e-install dev fly \
        check-announce check-lint check-tidy-check check-repo-check check-tools-test check-vet-web-bundle \
        check-web-check-static check-web-check-build check-build check-vet-integration check-vet-review \
        check-test check-web-check-test check-test-review check-test-race

# A target name may carry a digit (web-a11y, e2e), so the name pattern admits
# one; the old [a-z-] pattern silently dropped every such entry point from the
# listing.
help: ## List the entry points
	@awk 'BEGIN{FS=":.*## "} /^[a-z0-9][a-z0-9-]*:.*## /{printf "  %-21s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

modules: modules-present ## Print the module directories derived from go.work
	@for m in $(MODULES); do echo $$m; done

build: modules-present ## Build every module, both in the workspace and standalone
	@failed=; $(call each,$(MODULES),build,GOMAXPROCS=$(GATE_JOBS) go -C $$it build -p=$(GATE_JOBS) ./...); \
	  $(call each,$(MODULES),build standalone,GOWORK=off GOMAXPROCS=$(GATE_JOBS) go -C $$it build -p=$(GATE_JOBS) ./...); $(each-end)

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
	@failed=; $(call each,$(MODULES),test,GOMAXPROCS=$(GATE_JOBS) go -C $$it test -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) $(uncached) -timeout 30m ./...); $(each-end)

test-race: modules-present ## Test every module under the race detector (unit cases only)
	@failed=; $(call each,$(MODULES),test -race,GOMAXPROCS=$(GATE_JOBS) go -C $$it test -race -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) $(uncached) -timeout 60m ./...); $(each-end)

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
	failed=; $(call each,$(MODULES),test integration,GOMAXPROCS=$(GATE_JOBS) go -C $$it test -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) -count=1 -tags=integration -timeout 60m ./...); $(each-end)

test-integration-race: modules-present deps-up ## Test every module's integration cases under the race detector
	@if [ ! -f '$(TEST_BACKENDS_STATE)' ]; then \
	  echo "test-integration-race: no backend state at '$(TEST_BACKENDS_STATE)'; run 'make deps-up' and read its output" >&2; \
	  exit 1; \
	fi; \
	backend_env="$$($(BACKENDS) env --state '$(TEST_BACKENDS_STATE)')" || exit 1; \
	set -a; eval "$$backend_env"; set +a; \
	failed=; $(call each,$(MODULES),test integration -race,GOMAXPROCS=$(GATE_JOBS) go -C $$it test -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) -count=1 -race -tags=integration -timeout 90m ./...); $(each-end)

# Compiles the tagged files without running them, so the unit gate still says
# whether they build. Without it a change that breaks an integration case
# compiles at every commit and is only found on the scheduled run.
vet-integration: modules-present ## Compile every module's integration cases, running nothing
	@failed=; $(call each,$(MODULES),vet integration,GOMAXPROCS=$(GATE_JOBS) go -C $$it vet -p=$(GATE_JOBS) -tags=integration ./...); $(each-end)

# The review build is the default build plus the demo program's test controls and
# loopback guard: its files carry the `review` tag and no leg above compiles
# them. Compiling it here keeps the controls from rotting into not building, and
# gives the tag the leg the test-tier gate requires of every tag written in the
# tree.
vet-review: modules-present ## Compile the review build, running nothing
	@failed=; $(call each,$(MODULES),vet review,GOMAXPROCS=$(GATE_JOBS) go -C $$it vet -p=$(GATE_JOBS) -tags=review ./...); $(each-end)

# The end-to-end tier's binaries embed a web bundle of their own rather than
# the dev tree's dist: the `e2e` tag switches the embedded directory to
# dist-e2e. Its files carry that tag and no leg above compiles them, and a tag
# no leg passes takes its files out of every leg at once, so compiling them
# here keeps the variant from rotting into not building and gives the tag the
# leg tools/check_test_tiers.py requires of every tag written in the tree. Only
# the demo program carries the tag, so the leg runs from that module rather
# than over every module in the workspace.
vet-web-bundle: ## Compile the e2e-tagged build that embeds dist-e2e, running nothing
	@GOMAXPROCS=$(GATE_JOBS) go -C starters/demo/server vet -p=$(GATE_JOBS) -tags=e2e ./...

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
	@failed=; $(call each,$(REVIEW_MODULES),test review,GOMAXPROCS=$(GATE_JOBS) go -C $$it test -p=$(GATE_JOBS) -parallel=$(GATE_JOBS) -tags=review -run '^TestReview' -timeout 10m ./...); $(each-end)

# `golangci-lint run` takes a process-wide lock by default, so two checkouts
# running the gate at once make one of them die with "parallel golangci-lint
# is running" — indistinguishable from a code failure. `--allow-parallel-runners`
# removes that lock; the cost is contention, not a wrong answer. The width of
# the analysis itself is `--concurrency`, which the gate sets from GATE_JOBS;
# `fmt` has no such flag and takes no lock, so it runs one file set at a time.
fmt: modules-present ## Format every module in place
	@failed=; $(call each,$(MODULES),fmt,env -C $$it golangci-lint fmt ./...); $(each-end)

lint: modules-present ## Report formatting drift and static-analysis findings
	@failed=; $(call each,$(MODULES),lint fmt --diff,env -C $$it golangci-lint fmt --diff ./...); \
	  $(call each,$(MODULES),lint run,GOMAXPROCS=$(GATE_JOBS) env -C $$it golangci-lint run --allow-parallel-runners --concurrency=$(GATE_JOBS) ./...); $(each-end)

tidy: modules-present ## Tidy every module's go.mod and go.sum in place
	@failed=; $(call each,$(MODULES),tidy,GOWORK=off go -C $$it mod tidy); $(each-end)

# `go mod tidy -diff` reports what tidying would change without writing
# anything, so the judgement is the dependency files themselves. Judging
# them by the working tree's git status instead both misses an uncommitted
# untidy file (tidying restores the committed bytes, leaving the tree
# clean) and fails on any unrelated edit to a go.mod.
tidy-check: modules-present ## Verify the dependency files are tidy, leaving the tree unchanged
	@failed=; $(call each,$(MODULES),tidy-check,GOWORK=off go -C $$it mod tidy -diff); $(each-end)

# actionlint reads the workflow files themselves, which no other leg does: a
# misspelled key, an expression that cannot evaluate or a `run:` script that
# shellcheck rejects shows up only when GitHub starts the workflow. The leg
# names the files rather than letting actionlint search for them, so it lints
# the same ones from any directory, and it fails when there are none: an empty
# directory would pass without checking anything. actionlint skips its script
# checks without a word when it cannot find shellcheck, so the leg fails
# instead of reporting a partial pass. Any finding makes actionlint exit
# non-zero.
#
# actionlint does not read the steps of a composite action (its documentation
# says so), so a `run:` script under .github/actions is linted by
# tools/lint_composite_actions.py, which runs the same shellcheck the same way.
# It runs after actionlint and does not wait on it: a workflow finding does not
# hide a finding in an action, and the leg fails for either.
actionlint: ## Lint every workflow and composite action script with actionlint and shellcheck
	@if [ -z "$(strip $(WORKFLOWS))" ]; then \
	  echo "actionlint: no workflow file under $(REPO_ROOT)/$(WORKFLOW_DIR); an empty directory would pass without checking anything." >&2; \
	  exit 1; \
	fi
	@if ! command -v '$(SHELLCHECK)' >/dev/null 2>&1; then \
	  echo "actionlint: '$(SHELLCHECK)' is not on PATH; without it actionlint skips every run: script. Install the version .mise.toml pins ('mise install')." >&2; \
	  exit 1; \
	fi
	@failed=; $(call each,workflows,actionlint,env -C '$(REPO_ROOT)' '$(ACTIONLINT)' -shellcheck='$(SHELLCHECK)' $(WORKFLOWS)); \
	  $(call each,composite-actions,actionlint,$(PYTHON) '$(LINT_COMPOSITE_ACTIONS)' --root '$(REPO_ROOT)' --shellcheck '$(SHELLCHECK)'); $(each-end)

# The gates are the scripts in tools/ plus the workflow lint above, which is a
# target of its own because its linter is a pinned binary, not a script here.
# Both run to the end, so a red gate does not hide the workflow findings.
repo-check: python-version ## Run the gates in tools/ and the workflow lint over this tree
	@failed=; $(call each,$(GATES),gate,$(PYTHON) $$it); \
	  $(call each,actionlint,gate,$(MAKE) --no-print-directory actionlint); $(each-end)

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
# is what runs them. Without this step the rules' own tests have no automatic
# execution path -- the lint leg exercises the rules only over the code that
# happens to exist -- and the guard that every test-running package spreads the
# shared test preset (web/scripts/workspace-test-config.test.mjs) would not run
# at all.
#
# The build leg runs each package's `build:self` once, at the GATE_JOBS width:
# that script compiles only its own package into its own dist/, so no two
# concurrent packages write the same output and the width carries no
# correctness condition. pnpm -r orders the packages topologically, so a
# package's dependencies have their declarations in place before it compiles
# against them. The demo host is built separately by vite, which reads the
# packages' sources through its aliases rather than their dist/, and
# test-utils is left out because its build is its typecheck, which the static
# phase already runs.
#
# The unit-test leg is also serial at the package level, but for the budget
# rather than correctness: each package's `test` script starts its own vitest
# process, and pnpm -r's --workspace-concurrency and that process's
# --maxWorkers multiply. Running the packages one at a time with
# --workspace-concurrency=1 lets each vitest process take the whole GATE_JOBS
# budget, so the product stays GATE_JOBS instead of GATE_JOBS squared. The lint
# and typecheck legs start one process per package and carry no second width,
# so they keep GATE_JOBS. Neither emits a file (every package extends
# noEmit), so they also pass --no-sort: no ordering dependency exists to
# respect. The a11y sweep and the browser tier each start a single vitest
# process, so their --maxWorkers is already the budget.
#
# The frontend gate is three phases, so `check` can run each at the point its
# cost belongs: the static phase rejects most changes in a fraction of a run,
# the build writes the bundle the demo program embeds, and the tests read what
# the static phase installs. `web-check` runs all three in order and is the
# whole frontend gate; the phase targets are the entry point for running one
# part of it.
web-check: ## Run the frontend checks (pnpm install + lint, typecheck, rule tests, test, a11y, build)
	@$(MAKE) web-check-static
	@$(MAKE) web-check-test
	@$(MAKE) web-check-build

web-check-static: ## Install the frontend dependencies and run its static checks
	@pnpm -C web install --frozen-lockfile
	@pnpm -C web -r --workspace-concurrency=$(GATE_JOBS) --no-sort lint
	@pnpm -C web -r --workspace-concurrency=$(GATE_JOBS) --no-sort typecheck
	@pnpm -C web exec vitest run --maxWorkers=$(GATE_JOBS) eslint-rules/ scripts/

web-check-test: ## Run the frontend's unit tests and route-level accessibility sweep
	@pnpm -C web -r --workspace-concurrency=1 test --maxWorkers=$(GATE_JOBS)
	@$(MAKE) web-a11y

web-check-build: ## Build every frontend package
	@pnpm -C web -r --filter !@speed/test-utils --filter !demo-web --fail-if-no-match --workspace-concurrency=$(GATE_JOBS) run build:self
	@pnpm -C web --filter demo-web --fail-if-no-match exec vite build

web-a11y: ## Run the frontend's route-level accessibility sweep
	@pnpm -C web --filter demo-web --fail-if-no-match run test:a11y --maxWorkers=$(GATE_JOBS)

# The browser tier: the cases whose subject is real geometry, focus order
# or focusability, in Chromium through Vitest's browser mode. Chromium is
# not part of the .mise.toml toolchain -- that is why this tier is
# separate -- so a machine runs `pnpm -C web exec playwright install
# chromium` once; CI installs it before calling this entry. The tier is
# not part of `make check`; a scheduled workflow calls this same entry
# (see .github/workflows/browser.yml). The second pass injects a fixed
# non-default scale so the setup's scale handling is exercised at a value
# other than 1 (web/vitest.browser.injected-scale.config.ts); it runs one
# case and leaves the first pass's limits untouched.
web-browser: ## Run the frontend's browser tier in Chromium (geometry, focus order, focusability)
	@pnpm -C web exec vitest run --config vitest.browser.config.ts --maxWorkers=$(GATE_JOBS)
	@pnpm -C web exec vitest run --config vitest.browser.injected-scale.config.ts --maxWorkers=$(GATE_JOBS)

# The end-to-end tier: Playwright drives the built demo binary through a real
# browser, against a real sqlite file and real HTTP. It is deliberately not
# part of `check` -- it needs Chromium, WebKit and a built backend and is
# slower than the commit gate -- and tools/check_e2e_honesty.py holds that
# `check` does not reach it. The harness builds its own front end into
# starters/demo/server/dist-e2e and its binaries into /tmp in its global setup,
# so it may run beside `make dev`; `e2e-install` provisions the browsers it
# drives: Chromium for every journey, and WebKit for the journey that holds the
# cookies of a loopback http deployment, unprefixed and without Secure, to
# what WebKit keeps, since WebKit on Linux refuses any Secure cookie set over
# http, loopback host included. The global setup's three Go builds take their
# width from GATE_JOBS too (go build -p, and GOMAXPROCS for the compiler's
# threads), so the recipe hands the Playwright process GATE_JOBS in its
# environment; the variable this file defines is not exported otherwise.
e2e-install: ## Install the Chromium and WebKit builds the end-to-end tier drives
	@pnpm -C web exec playwright install chromium webkit

e2e: ## Run the end-to-end journey specs against the built demo binary
	@pnpm -C web --filter demo-web --fail-if-no-match run typecheck:e2e
	@GATE_JOBS=$(GATE_JOBS) pnpm -C web --filter demo-web --fail-if-no-match exec playwright test --config e2e/playwright.config.ts --workers=$(GATE_JOBS)

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
	mise exec -- pnpm -C web --filter demo-web --fail-if-no-match run dev & \
	vite=$$!; \
	trap 'kill $$vite 2>/dev/null || true; wait $$vite 2>/dev/null || true' EXIT INT TERM; \
	mise exec -- env -C starters/demo/server air -c .air.toml

# The fly leg releases the demo program to Fly.io. It is deliberately not
# part of `check`: it needs a Fly account and the FLY_API_TOKEN secret (a secret
# of the workflow's `production` environment), and the workflow that calls it
# (.github/workflows/deploy.yml) is dispatched by hand on main and runs it only
# for the tip commit and only when that commit's `check` succeeded. flyctl is
# deployment machinery rather than a build input, so
# .mise.toml does not pin it; the workflow installs the pinned version and this
# target calls what is on PATH.
#
# The working directory is the repository root because the image compiles the
# frontend workspace under web/ and the Go workspace go.work declares, both of
# which sit outside starters/demo. The Dockerfile path lives in fly.toml and is
# relative to that file.
fly: ## Build and release the demo program to Fly.io
	@flyctl deploy . --config starters/demo/fly.toml --remote-only

# The commit gate. Its segments are prerequisites, so a plain `make check`
# runs them one at a time in the order listed and `make -j N check` runs up to
# N of them at once. Either way the set of segments is the same: what may run
# together is a product dependency, not a schedule. The frontend build writes
# the bundle the demo program embeds (go:embed) and waits for every Go leg
# that compiles it, and the frontend's test and build phases read the
# dependencies its static phase installs.
#
# The frontend build waits for those Go legs rather than preceding them on
# purpose. Running it first would let them compile the bundle this run wrote,
# but the tests read nothing from the bundle's contents, so the freshness buys
# nothing and the ordering costs the Go tests the length of the frontend
# build. Waiting for them instead keeps the two from overlapping under -j,
# which is the property that matters: a Go leg reading dist while pnpm writes
# it is the hazard, not a dist left from an earlier run.
#
# The order is fastest-failing first: the static checks take a fraction of a
# run and reject most changes, so a red one is reported minutes in rather than
# after every test has run. vet-web-bundle compiles the e2e-tagged build and
# consumes nothing, so it keeps its place before the frontend work. The build,
# then the compile-only vet legs, then the tests, the review cases and the
# race detector, and the frontend build last. A segment prints the time it
# starts, and every line of its output carries the segment's name, so a log of
# a concurrent run still shows where the time went and which segment said what.
#
# check-announce prints GATE_JOBS once before any segment runs; every segment
# depends on it. GATE_JOBS is the width each segment's tools take, and `-j N`
# multiplies it (see its definition above), so the caller allocates both.
#
# The `##` doc belongs on a line of its own here: `make help` reads it from a
# line that starts with the target's name, and the prerequisite list below is
# wrapped, so its last line starts with a segment name instead.
check: ## Run everything a commit is gated on (no external service needed)
check: check-lint check-tidy-check check-repo-check check-tools-test \
       check-vet-web-bundle check-web-check-static \
       check-build check-vet-integration check-vet-review check-test \
       check-web-check-test check-test-review check-test-race \
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

check-web-check-build: check-announce | check-web-check-static check-build check-vet-integration check-vet-review check-test check-test-review check-test-race
	$(call mark,web-check-build)
	@set -o pipefail; $(MAKE) --no-print-directory web-check-build $(call segment-output,web-check-build)

check-build: check-announce
	$(call mark,build)
	@set -o pipefail; $(MAKE) --no-print-directory build $(call segment-output,build)

check-vet-integration: check-announce
	$(call mark,vet-integration)
	@set -o pipefail; $(MAKE) --no-print-directory vet-integration $(call segment-output,vet-integration)

check-vet-review: check-announce
	$(call mark,vet-review)
	@set -o pipefail; $(MAKE) --no-print-directory vet-review $(call segment-output,vet-review)

check-test: check-announce
	$(call mark,test)
	@set -o pipefail; $(MAKE) --no-print-directory test $(call segment-output,test)

check-web-check-test: check-announce | check-web-check-static
	$(call mark,web-check-test)
	@set -o pipefail; $(MAKE) --no-print-directory web-check-test $(call segment-output,web-check-test)

check-test-review: check-announce
	$(call mark,test-review)
	@set -o pipefail; $(MAKE) --no-print-directory test-review $(call segment-output,test-review)

check-test-race: check-announce
	$(call mark,test-race)
	@set -o pipefail; $(MAKE) --no-print-directory test-race $(call segment-output,test-race)
