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

.PHONY: help modules build test test-race fmt lint tidy tidy-check repo-check tools-test check python-version modules-present web-check

help: ## List the entry points
	@awk 'BEGIN{FS=":.*## "} /^[a-z][a-z-]*:.*## /{printf "  %-11s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

modules: modules-present ## Print the module directories derived from go.work
	@for m in $(MODULES); do echo $$m; done

build: modules-present ## Build every module, both in the workspace and standalone
	@failed=; $(call each,$(MODULES),build,go -C $$it build ./...); \
	  $(call each,$(MODULES),build standalone,GOWORK=off go -C $$it build ./...); $(each-end)

# A test binary's own deadline bounds how long one module's suite may run; go
# test's default is ten minutes, which the example hosts' end-to-end legs
# exceed under the race detector on a loaded machine. The bound is generous on
# purpose: it is there to end a hung run, not to pace a slow one.
#
# An example host lays its cases out on a light set of stacks unless asked for
# the whole roster, so that a developer's own `go test` comes back sooner. These
# two legs are the gate, and a gate runs the whole matrix; naming the variable
# for every module costs the ones that do not read it nothing.
test: modules-present ## Test every module
	@failed=; $(call each,$(MODULES),test,SPEED_CRM_HOST_ALL_STACKS=1 go -C $$it test -timeout 30m ./...); $(each-end)

test-race: modules-present ## Test every module under the race detector
	@failed=; $(call each,$(MODULES),test -race,SPEED_CRM_HOST_ALL_STACKS=1 go -C $$it test -race -timeout 60m ./...); $(each-end)

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
# why it is a separate root). This leg is deliberately not part of `check`:
# `check`'s toolchain (.mise.toml) pins no Node or pnpm, so CI could not run
# it, and folding it into `check` would break the single entry point the Go
# legs rely on. It is the frontend's own entry point, run where Node and
# pnpm are installed.
web-check: ## Run the frontend checks (pnpm install + lint, typecheck, test, build)
	@pnpm -C web install --frozen-lockfile
	@pnpm -C web -r lint
	@pnpm -C web -r typecheck
	@pnpm -C web -r test
	@pnpm -C web -r build

check: ## Run everything CI runs
	@$(MAKE) build
	@$(MAKE) test
	@$(MAKE) test-race
	@$(MAKE) lint
	@$(MAKE) tidy-check
	@$(MAKE) repo-check
	@$(MAKE) tools-test
