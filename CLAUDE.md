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
- What a module's own contract is: that module's `AGENTS.md`, which
  travels with the code.
- What the checks are: the root `Makefile`. `make help` lists the entry
  points; CI calls one of them.

## Language Rule (read this first)

- **`docs/design/**`, `docs/adr/**` and `docs/glossary.md` are written in Chinese.** They hold design documents, decision records and the shared glossary.
- **Everything else is English**: code comments, godoc/TSDoc, module docs, per-module `AGENTS.md` files, package READMEs, the root `README.md`, `.claude/skills/**`, commit messages, and GitHub Issues (titles, bodies, and comments — including the coding-flow role workflow).
- User-facing product text is bilingual (zh-CN + en-US) and lives in i18n resources, never in code.

## Documentation

- design: docs/design
- adr: docs/adr
- glossary: docs/glossary.md
- language: zh-CN

Requirements documents are not used. Load the `document-standards` skill before creating or editing anything under these paths.

## Commands

Everything runs from the repository root through `make`, and the root
Makefile is the only place the repository's commands are written down.
`make help` lists the entry points, one line each; `make check` is the one
CI calls. The toolchain they need comes from `mise install`, which reads
`.mise.toml`.

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
| `docs/design`, `docs/adr`, `docs/glossary.md` | The design authority. Chinese, per the language rule above. |
| `tools/` | Repository self-checks. `tools/README.md` says what each one does. |
| `Makefile`, `.mise.toml`, `.golangci.yml`, `.github/workflows/` | The engineering machinery: entry points, toolchain pins, lint configuration, CI. |
| `compose.yaml` | The external backends the `integration`-tagged cases run against: PostgreSQL, Redis, NATS and memcached. The tests connect to it; it is the only thing that starts it. |
| `.claude/skills/` | How the work is done. Each handbook's own description says what it covers; the ones that apply here are at the bottom of this file. |
| `docs/site/` | Documentation generated for the earlier implementation. Nothing `make check` runs builds or checks it. |

**`docs/site/` is legacy and out of scope by default.** It was generated from the implementation this repository superseded and describes an assembly API this repository no longer has, so it is not a second opinion on anything `docs/design` settles. Do not extend the language rule, the boundaries below, or `docs/design`/`docs/adr` authority to it, and do not read it for ordinary work here.

## Boundaries

- **New code goes under `pkg/`; a program that assembles modules goes
  under `starters/`.** The `pkg` module owns its `go.mod`; each
  capability directory owns its `AGENTS.md` and its tests, and each
  shared unit under `pkg/internal/` owns its `AGENTS.md`.
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

## Where the rest lives

| Location | Content |
|---|---|
| `docs/design` | What the architecture is: the whole picture in `architecture.md`, one document per module under `modules/`. |
| `docs/adr` | Why it is that way: one decision per file, alternatives included. |
| `docs/glossary.md` | The shared vocabulary. A term used in a design document means what this file says it means. |
| `<module>/AGENTS.md` | The module's own contract: its boundaries and how to run it. |
| `.claude/skills/` | For work under `pkg/` and `starters/`: `document-standards` before writing under the documentation paths, `commit-convention` for the commit message. |
