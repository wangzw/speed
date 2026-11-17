# AGENTS.md

Orientation for any AI coding tool that found its way here by convention.

This repository is a configuration-driven modular application framework,
delivered as Go libraries: the modules live under `pkg/`, runnable product
skeletons that assemble them under `starters/`, the design under `docs/`.

**`CLAUDE.md` is the full guide** — where things are, which language to
write in, and the boundaries a change must not cross. Read it before
editing anything. A module's own `AGENTS.md` carries the boundaries
specific to that module.

## Commands

Everything runs from the repository root through `make`. `make help`
lists the entry points; CI runs the same legs as `make check`, split across
parallel jobs.
