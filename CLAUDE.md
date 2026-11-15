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
runs `make check`; the browser, end-to-end and integration tiers are
separate workflows with targets of their own.

**Iterate at the smallest useful scope, and run the whole of the changed
side before calling the work done.** `make check` is the commit gate and
runs the Go and frontend legs both, so using it for a one-sided change
spends most of its time on the side that did not move.

- **Frontend only** (`web/`, `starters/demo/web/`): iterate with the
  touched package's own `pnpm -C web/packages/<name> test` (or
  `pnpm -C starters/demo/web ...` for the demo host), plus `lint` and
  `typecheck`; then `make web-check` for the whole frontend. Add
  `make web-browser` when geometry, focus order or focusability is the
  subject, and `make e2e` when the change is user-visible (copy, layout,
  assembly, a default).
- **Backend only** (`pkg/`, `starters/demo/server/`): iterate with
  `go -C <module> test ./<package>`, then `make build test test-race lint
  tidy-check repo-check`. Add `make test-integration` when the change
  reaches a backend `compose.yaml` provides.
- **Contract change** (API schema, generated client): regenerate the
  contract and the client (`make api-docs`, then `starters/demo/web`'s own
  `api:gen`), and run both sides.
- **Docs, workflows, deployment files, the Makefile**: `make repo-check`
  and `make tools-test`. When the change touches a document, also run the
  `document-standards` checker and render every diagram in it.

A change that crosses the boundary is two changes: run each side's gate.

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
| `Makefile`, `.mise.toml`, `.golangci.yml`, `.github/workflows/` | The engineering machinery: entry points, toolchain pins, lint configuration, CI. |
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
  linear.
