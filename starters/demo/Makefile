
SHELL := /bin/bash
.DEFAULT_GOAL := help

GOMODULE := server
FRONTEND := web

.PHONY: help build test test-race fmt lint tidy tidy-check \
        web-install web-lint web-typecheck web-test web-a11y web-build check

help: ## List the entry points
	@awk 'BEGIN{FS=":.*## "} /^[a-z][a-z-]*:.*## /{printf "  %-13s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: web-build ## Build the frontend, then the Go program
	go -C $(GOMODULE) build -o demo .

test: ## Test the Go module
	go -C $(GOMODULE) test -timeout 30m ./...

test-race: ## Test the Go module under the race detector
	go -C $(GOMODULE) test -race -timeout 60m ./...

fmt: ## Format the Go module in place
	env -C $(GOMODULE) golangci-lint fmt ./...

lint: ## Report formatting drift and static-analysis findings
	env -C $(GOMODULE) golangci-lint fmt --diff ./...
	env -C $(GOMODULE) golangci-lint run ./...

tidy: ## Tidy the Go module's dependency files in place
	go -C $(GOMODULE) mod tidy

tidy-check: ## Verify the dependency files are tidy, leaving the tree unchanged
	go -C $(GOMODULE) mod tidy -diff

web-install: ## Install the frontend's dependencies
	pnpm -C $(FRONTEND) install

web-lint: ## Lint the frontend
	pnpm -C $(FRONTEND) lint

web-typecheck: ## Typecheck the frontend
	pnpm -C $(FRONTEND) typecheck

web-test: ## Test the frontend
	pnpm -C $(FRONTEND) test

web-a11y: ## Run the frontend's route-level accessibility sweep
	pnpm -C $(FRONTEND) test:a11y

web-build: ## Build the frontend into server/dist
	pnpm -C $(FRONTEND) build

check: ## Run everything above
	@failed=; \
	  $(MAKE) web-install || failed="$${failed:+$$failed; }web-install"; \
	  $(MAKE) build || failed="$${failed:+$$failed; }build"; \
	  $(MAKE) test || failed="$${failed:+$$failed; }test"; \
	  $(MAKE) test-race || failed="$${failed:+$$failed; }test-race"; \
	  $(MAKE) lint || failed="$${failed:+$$failed; }lint"; \
	  $(MAKE) tidy-check || failed="$${failed:+$$failed; }tidy-check"; \
	  $(MAKE) web-lint || failed="$${failed:+$$failed; }web-lint"; \
	  $(MAKE) web-typecheck || failed="$${failed:+$$failed; }web-typecheck"; \
	  $(MAKE) web-test || failed="$${failed:+$$failed; }web-test"; \
	  $(MAKE) web-a11y || failed="$${failed:+$$failed; }web-a11y"; \
	  if [ -n "$$failed" ]; then echo "failed: $$failed" >&2; exit 1; fi
