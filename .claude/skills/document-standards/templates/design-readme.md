<!--
Template for the design documentation entry point. Delete every comment
block before committing.

The introduction says what the system is and what it is for. It does NOT
summarize the architecture -- architecture.md does that, and two summaries
drift apart.

The index covers each entry point exactly once: architecture.md, every
module document directly under modules/, and the README.md of every module
subdirectory. A missing entry is an orphan, and an orphan is an error. A
glossary link is conventional. A module subdirectory's own README.md
indexes that module's files and carries the sub-module status roll-up.

The glossary and references links assume the default paths; adjust each to
the configured `glossary` and `references` path. Delete the References line
if the project cites no public external source.

Implementation status does NOT belong here. The roll-up lives in
architecture.md, the detail in each module document.
-->

# <System name>

<What the system is and what it is for.>

## Index

- [Architecture](architecture.md) — system-wide structure, cross-cutting design and architecture invariants
- [Glossary](../glossary.md) — project-agreed terms, shared by every document type
- [References](../references.md) — public external sources cited by the documentation

### Modules

| Module | Document |
|---|---|
| `<module-name>` | [<module-name>](modules/design-<slug>.md) |
| `<module-name>` | [<module-name>](modules/<slug>/README.md) |
