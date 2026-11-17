# CLAUDE.md

This file orients an agent working in this repository: where things are,
which language to write in, how to run the checks, and which boundaries a
change must not cross. It is not a record of development status, and it
restates nothing another file already carries:

- The module roster is whatever `go.work` lists. That file is the
  membership authority, and the Makefile derives every module loop from
  it — no prose census here or anywhere else.
- What the architecture is, and why: `docs/design` and `docs/adr`. A
  design question is answered there, never from this file.
- What the product must do: `docs/prd`. A requirement is stated there,
  and a design document that restates one references its id instead.
- What a module's own contract is: that module's `AGENTS.md`, which
  travels with the code.
- What the checks are: the root `Makefile`. `make help` lists the entry
  points.

## Language Rule (read this first)

- **`docs/design/**`, `docs/adr/**`, `docs/prd/**`, `docs/glossary.md` and `docs/references.md` are written in Chinese.** They hold design documents, decision records, requirements documents, the shared glossary and the shared register of external sources.
- **Everything else is English**: code comments, godoc/TSDoc, module docs, per-module `AGENTS.md` files, package READMEs, the root `README.md`, `.claude/skills/**`, commit messages, and GitHub Issues (titles, bodies, and comments).
- User-facing product text is bilingual (zh-CN + en-US) and lives in i18n resources, never in code.

## Documentation

- design: docs/design
- requirements: docs/prd
- adr: docs/adr
- glossary: docs/glossary.md
- references: docs/references.md
- language: zh-CN

Load the `document-standards` skill before creating or editing anything under these paths.

## Commands

Everything runs from the repository root through `make`, and the root
Makefile owns the entry points — this file only says which one a change
needs. `make help` lists them, one line each; the toolchain they need comes
from `mise install`, which reads `.mise.toml`. `.github/workflows/ci.yml`
runs the `check` segments as parallel jobs through `make`, with
`GOFLAGS=-count=1` and `GATE_JOBS` at the Makefile's default;
`tools/check_ci_jobs.py` holds the jobs' legs to `make check`'s segments and
to the module roster in `go.work`.
The browser, end-to-end and integration tiers are separate workflows with
targets of their own.

**Iterate at the smallest useful scope.** `make check` is the commit
gate and runs the Go and frontend legs both, so using it for a one-sided
change spends most of its time on the side that did not move.

- **Frontend only** (`web/`, `starters/demo/web/`): iterate with the
  touched package's own `pnpm -C web/packages/<name> test` (or
  `pnpm -C web --filter demo-web ...` for the demo host), plus `lint` and
  `typecheck`; then `make web-check` for the whole frontend, or
  `make web-check-static` for only its install, lint, typecheck and rule
  tests (`make web-check-test` and `make web-check-build` are its other two
  phases). The heavy legs are `make web-browser` when geometry, focus order
  or focusability is the subject, and `make e2e` when the change is
  user-visible (copy, layout, assembly, a default).
- **Backend only** (`pkg/`, `starters/demo/server/`): iterate with
  `go -C <module> test ./<package>`, then `make build test lint
  tidy-check repo-check`. The heavy legs are `make test-race` and
  `make test-integration` when the change reaches a backend
  `compose.yaml` provides.
- **Contract change** (API schema, generated client): regenerate and run
  both sides, per the first item under "Before asking for review" below.
- **Docs, workflows, deployment files, the Makefile**: `make repo-check`
  and `make tools-test`. When the change touches a document, also run the
  `document-standards` checker and render every diagram in it.

`GATE_TIME_SCALE` (a Make variable, read from the same-named environment
variable, default 1) multiplies the frontend suites' wait limits: Vitest's
test and hook budget, Testing Library's async budget, and `vi.waitFor`
through the shared `waitForCondition` helper. `vi.waitUntil` has no budget
the setup reaches and is not wrapped: the workspace guard rejects every
member access to `vi.waitFor` or `vi.waitUntil` in a test source — a call,
a destructured alias, an argument, a property value, an arrow's return or
a reference kept uncalled — and every `vi.waitFor` goes through
`waitForCondition`. Testing Library's `waitFor` and `findBy*` queries wait
under the async budget instead, without going through the helper. A
normal run leaves it at 1; a run that shares the machine with other heavy
work raises it. It must be a
finite number of at least 1 — a typo, a non-number or a value that would
lower the limits fails the run rather than falling back to 1. No limit is
derived from the machine's load or core count. The Playwright end-to-end
tier and the Go test tiers read the same variable for their own wait
budgets. A Go case whose bound tells a right implementation from a wrong one
by elapsed time states the wrong implementation's time to
`coretest.DeclaredAgainst`, which fails that case explicitly, naming the
scale, the bound, the wrong implementation's time and the meeting point,
before the scale reaches that point rather than letting the case pass
silently.

A change that crosses the boundary is two changes: run each side's gate.

**The gate's CPU budget is `GATE_JOBS`**, defaulted by the Makefile. Every
tool a gate leg calls that takes a width setting is told that width
explicitly — go build, go test and go vet (`-p` and `GOMAXPROCS`; tests also
`-parallel`), golangci-lint (`--concurrency`), vitest (`--maxWorkers`),
Playwright (`--workers`) and pnpm -r (`--workspace-concurrency`) — so none
of them probes the host's core count, which is wrong inside a container
because the process sees the node's cores rather than the share it was given.
Three carry
no width this repository can set and run at whatever their runtime decides:
`golangci-lint fmt`, tsc, and the rolldown thread pool behind `vite build`.
Set it to the CPU share this run may use. A leg that nests two of those
widths — pnpm -r starting one vitest process per package — keeps the product
within the budget, not just each factor: the frontend test segment runs its
packages one at a time (`--workspace-concurrency=1`) and gives each vitest
process the whole `GATE_JOBS`, so the product is `GATE_JOBS` rather than its
square. `web-a11y` and `web-browser` start a single vitest process, so their
`--maxWorkers` is already the budget. `make -j N check` multiplies it: up to
N segments run at once, each able to use `GATE_JOBS` cores, so a caller
running segments in parallel divides its budget between the two.

**The test reporter is pinned, not selected by the runner**, so a warning a
passing case prints is in the log whichever way the gate was started.

**Gate stages.** A fix round re-runs only the touched packages' tests and the
generated-artifact checks below. The whole-side gate — the gate commands the
change's own bullet above lists, or `make check` when the change crosses the
boundary — runs once, on the final HEAD: the landable candidate after review
has approved it, which is the tip of the merge train before `main`
fast-forwards to it (the procedure is the commit-convention skill's "Landing
a branch"). What a change sends for review carries the fix round's targeted
tests and generated-artifact checks, not a whole-side run. When the
whole-side gate is `make check`, `make test-race` is already one of its steps
(the demo server alone takes about ten minutes there); run `make test-race`
separately only with the backend-only whole-side gate, `make build test lint
tidy-check repo-check`. The heavy legs run apart from it: `make
test-integration`, `make web-browser`, `make e2e`. A gate failure on the
final HEAD with a small fix is fixed, rerun and landed, with its diff
reported to the reviewer for an after-the-fact check; one that needs rework
goes back to review. A unit compiled into a local integration branch runs
neither when it merges there; the integration branch runs both once after
its last unit lands.

**Before asking for review**, also check:
- A change to an OpenAPI declaration or fragment regenerates all three
  artifacts in the same commit (`make api-docs`, then `api:gen` in `web` and in
  `starters/demo/web`) and runs the demo server's `TestCommittedMergedDocument`,
  which compares the merged contract byte for byte.
- Requirements documents state external behaviour only: no problem codes,
  routes or field names (the document-standards skill).
- An incompatible change to an exported Go signature, a published API shape or
  a problem code is marked breaking in the commit (the commit-convention skill).

`starters/demo` carries a Makefile of its own. That one is the copyable
skeleton's entry point — what a tree lifted out of this repository runs —
and not a second entry point here: the root `make check` covers that module
through `go.work` like every other.

## Where things are

| Path | What it is |
|---|---|
| `go.work` | The module roster. Adding a module here registers it everywhere. |
| `pkg/` | The implementation: one Go module. Each top-level directory under it is a capability directory, and each carries its own `AGENTS.md`. The shared units and their test fixtures live under `pkg/internal/`, each shared unit with an `AGENTS.md` of its own. |
| `starters/` | Runnable, copyable product skeletons that assemble the modules into a running program; `starters/demo` is the starting point. |
| `web/` | The frontend package library: the platform's deliverable npm packages, a pnpm workspace of its own. |
| `docs/design` | What the architecture is: the whole picture in `architecture.md`, one document per module under `modules/`. Chinese, per the language rule. |
| `docs/prd` | What the product must do: one document per capability under `features/`, one per business process under `journeys/`. Chinese. |
| `docs/adr` | Why it is that way: one decision per file, alternatives included. Chinese. |
| `docs/glossary.md`, `docs/references.md` | The shared vocabulary and the register of external sources; a `REF-<slug>` cited in any document names an entry of the latter. Chinese. |
| `<module>/AGENTS.md` | A module's own contract: its boundaries and how to run it. |
| `tools/` | Repository self-checks. `tools/README.md` says what each one does. |
| `Makefile`, `.mise.toml`, `.golangci.yml`, `.github/` | The engineering machinery: entry points, toolchain pins, lint configuration, CI workflows with their toolchain action, dependency updates. |
| `compose.yaml` | The external backends the `integration`-tagged cases run against: PostgreSQL, Redis, NATS and memcached. The tests connect to it; it is the only thing that starts it. |
| `.claude/skills/` | How the work is done: `document-standards` before writing under the documentation paths, `commit-convention` for commit messages, `frontend-coding-standards` for `web/`, and `browser-review` before opening a headed browser against the demo (every command carries `-s=speed-review`, and `open` carries the fixed profile). |
| `docs/site/` | Documentation generated for the earlier implementation. No gate builds or validates its content, though the repository-wide hygiene gates still traverse the tree. |

**`docs/site/` is legacy and out of scope by default.** It was generated from the implementation this repository superseded and describes an assembly API this repository no longer has, so it is not a second opinion on anything `docs/design` settles. Do not extend the language rule, the boundaries below, or `docs/design`/`docs/adr` authority to it, and do not read it for ordinary work here.

## Boundaries

- **New code goes under `pkg/`; a frontend package goes under
  `web/packages/`; a program that assembles modules goes under
  `starters/`.** The `pkg` module owns its `go.mod`; each capability
  directory owns its `AGENTS.md` and its tests, and each shared unit under
  `pkg/internal/` owns its `AGENTS.md`.
- **Read the design before changing a public signature.** The design
  document is where a shape is decided; code that contradicts it is the
  thing to fix, in one direction or the other, before the change lands.
- **A module's own `AGENTS.md` carries boundaries this file does not.**
  Read it before editing inside that module.
- **Every bug fix ships with a test that reproduces the bug** — failing
  before the fix, passing after. If one genuinely cannot be written, say
  so explicitly and say what the follow-up is.
- **Warnings are first-class issues.** Compiler, lint, deprecation and
  race-detector warnings are fixed, not silenced; anything deferred is
  called out rather than left to be discovered.
- **Commit messages are English, in Conventional Commits form, scoped by
  the capability directory the change touches** — `fix(config): reject a
  manifest with two mounts on one path`.
- **Rebase onto the target branch and fast-forward.** History stays
  linear. `main` always passes the gate: a landing of several commits is
  gated on its final commit, and its intermediate commits need not pass
  on their own. Before landing, fold each correction into the commit it
  corrects (fixup plus autosquash), and one logical change lands as one
  commit; the procedure is the commit-convention skill's "Landing a
  branch" section.
