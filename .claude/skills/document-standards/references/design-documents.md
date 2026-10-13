# Design Document Standard

Read together with `SKILL.md`, whose common rules apply in full and are not repeated here.

**The premise for this document type**: a design document is the *final design conclusion*, not a design log. Alternatives considered, the choice made and the reasoning behind it belong to the ADR; the design document carries only the reference.

---

## 1. Layout and Naming

```
docs/design/                        # relocatable via CLAUDE.md
├── README.md                       # entry point: brief introduction + index of entry points
├── architecture.md                 # high-level overview
└── modules/
    ├── design-<slug>.md            # a module with a single document
    ├── design-<slug>-<aspect>.md   # only when a module document grows unwieldy
    └── <slug>/                      # a module whose sub-modules have their own documents
        ├── README.md               # the module's index and sub-module status roll-up
        ├── design-<slug>.md        # the module's own document
        └── design-<slug>-<sub>.md  # one document per sub-module
```

The two forms mix freely in one tree; a tree does not pick one. A module that fits in a single document stays flat beside `design-<slug>.md`; a module whose sub-modules each carry their own document is placed in a subdirectory, so its sub-modules are not read as siblings of unrelated modules.

The glossary is **not** part of this tree. The project has one glossary, shared by every document type, at the configured `glossary` path, and it holds only terms that hold project-wide. A term bounded by one module is registered in that module's own document, not there.

**Required:**
- `README.md` and `architecture.md` are fixed names — the explicit exceptions to the `design-<slug>` pattern, which applies inside `modules/`.
- **File names carry no sequence number**, anywhere. Ordering lives in the README index, not in file names.
- A slug is lowercase kebab-case, ASCII only, and **matches the module's directory or package name in the code**, so the document-to-code mapping is mechanical.
- **A slug is permanent.** Renaming breaks every link that points at it.
- One document per real module boundary in the code. A unit with no external interface contract of its own is folded into its parent module's document, not given a file.
- An oversized module document may split by aspect into `design-<slug>-<aspect>.md`, still with no sequence number. The parent slug stays the prefix. **An aspect split is not a sub-module**: it stays flat beside `design-<slug>.md`, and a module subdirectory holds sub-modules. The two use the same `design-<slug>-<x>.md` shape, so the position is what tells them apart.
- **A module subdirectory is named for the module's slug, is permanent like the slug, and holds the module's own document `design-<slug>.md` plus one `design-<slug>-<sub>.md` per sub-module that has its own document.** The parent slug is the prefix of every file inside. **A module subdirectory carries a `README.md`** — the module's index and its sub-module status roll-up (section 2).

## 2. `README.md`

The design tree has two kinds of index: the tree's own `README.md`, and the `README.md` of each module subdirectory. Both are a brief introduction followed by the index.

**The tree's `README.md`** — the entry point:
- **The index covers each entry point exactly once**: `architecture.md`, every module document directly under `modules/`, and the `README.md` of every module subdirectory. A file of the tree's own level absent from the index is an orphan, and an orphan is an error. A glossary link is conventional — the glossary sits outside the tree and is not part of the covered set.
- The introduction states what the system is and what it is for. It does not summarize the architecture — `architecture.md` does that, and two summaries drift.
- Implementation status does not appear here. The roll-up belongs to `architecture.md`.

**A module subdirectory's `README.md`** — the module's index, the entry the tree's index links to:
- **The index covers every file in the subdirectory but itself, each appearing exactly once.**
- It carries the sub-module **Implementation Status** roll-up (section 6): one row per sub-module document, linking to it. The module's own status does not repeat here — `architecture.md` already carries it.

Templates: `templates/design-readme.md` and `templates/design-module-index.md`.

## 3. `architecture.md`

The high-level view of the whole system.

**Required sections:**
- **Overview** — the system's shape and its decomposition into modules.
- **Module dependency graph** — mandatory diagram.
- **Logical deployment architecture** — mandatory diagram.
- **Cross-cutting design** — the concerns no single module owns: error model, authentication and authorization boundaries, data consistency and transaction boundaries, concurrency model, failure and degradation behaviour, observability, performance and capacity targets, security boundaries. **The criterion is the phrase before the list, not the list.** A concern no single module owns earns a heading whether or not it is named above; a named one that genuinely does not exist in the system is omitted. These are the concerns most systems have, not a set to be satisfied.
- **Architecture invariants** — the rules the system must not violate, for example a strictly bottom-up dependency direction. These are what a review can actually adjudicate against, so each is stated so that a violation is recognizable.
- **Implementation status** — one line per top-level module, linking to that module's own document (`modules/design-<slug>.md`, or `modules/<slug>/design-<slug>.md`). Roll-up only; the detail lives in the module document, and a module's sub-modules in its subdirectory's `README.md`. **A sub-module never appears here** — listing one here duplicates the roll-up its subdirectory already carries.

**Conditional sections** — include when the trigger holds, delete outright otherwise:
- **Physical deployment architecture** — when the system runs as more than one process or replica.
- **Inter-module call relationships** — when call chains are non-trivial.
- **Data flow** — when data moves across module boundaries.

Template: `templates/design-architecture.md`.

## 4. Module Design Documents

Fixed section order, so modules can be read against each other and checked mechanically:

1. **Responsibilities** — and, explicitly, **non-responsibilities**. Writing down what the module does not own is the cheapest available defence against scope creep.
2. **Glossary** — optional, and rare. The terms this document gives a meaning of its own, each stated with the scope it applies to. A term that holds project-wide belongs to the project glossary instead, never to both.
3. **External interface contract** — what other modules may call, and what stability is promised. This is the most valuable part of a module document.
4. **Dependencies** — what it depends on, and whether the direction satisfies the architecture invariants.
5. **UML diagrams** — structure and call relationships, selected per section 5.
6. **Core data structures**.
7. **Core algorithms and flows** — with complexity analysis for performance-sensitive algorithms. Procedural flows need no complexity analysis.
8. **Error and failure semantics** — failure modes, retry, idempotency, degradation behaviour.
9. **Concurrency and transaction boundaries** — when applicable.
10. **Extension points** — when applicable.
11. **Implementation status**.

The fixed order binds every module document, whether it sits flat under `modules/` or inside a module subdirectory. A sub-module document is a module document like any other and carries the whole order. The `README.md` of a module subdirectory is an index, not a module document, and follows section 2 instead.

Template: `templates/design-module.md`.

## 5. Diagrams

**Required:**
- **Mermaid is the only diagram format.** No ASCII art, no linked images, no binary sources. A diagram must be reviewable as a text diff.
- **Every diagram carries accompanying prose.** The diagram carries structure; the constraints, the invariants and the pointer to the rationale are carried by the text. A section holding a diagram and nothing else is incomplete.
- **Select UML diagram types by fact, never to fill a template.** Class diagram for core data structures and their relationships; sequence diagram for cross-module interaction; state diagram for entities with a lifecycle; activity diagram for branch-heavy flows. When the module holds no corresponding fact, draw nothing.
- Rendering must be verified after every edit — a common rule, stated in `SKILL.md`.

## 6. Implementation Status

The fastest-rotting content in any design document, and therefore the most tightly constrained.

**Required:**
- The value is one of exactly `Implemented`, `Partially implemented`, `Not implemented`, rendered in the document's language with consistent wording. **DO NOT** write vague states such as `mostly complete` or `basically working`.
- **An entry states the status and nothing else.** It does not explain the state, name what is missing, list what remains, or say where the rest has landed — one status word per entry, no trailing clause. An explanation is a second assertion about the code, and it rots on its own schedule: correcting the status edits one word, while an explanation written beside it survives the correction and is read as fact long after it stopped being one.
- Every entry that claims implementation carries a **code anchor: path plus symbol name, no line number**. A status is an assertion falsifiable against the code, not an impression.
- **Each document is authoritative for its own status, and the roll-up above it links down.** A module document rolls up no sub-module; `architecture.md` carries one line per top-level module; a module whose sub-modules have their own documents carries their roll-up in that module subdirectory's `README.md` (section 1). Detail appears once, in the document it belongs to; the same fact written twice will drift.

`Partially implemented` exists here and **does not exist in a requirements document**. The two document types look at the system from different places: a design document is the implementation view, where partial implementation is a real and informative state; a requirements document is the outside view, where it is not.

## 7. What Never Appears in a Design Document

- Change history, version numbers, authors, dates, review records
- TODO lists, schedules, milestones, owners
- Requirement content (reference the requirement id instead)
- Alternatives considered, the choice made, and the reasoning behind it (reference the ADR instead)
- Large pasted code (signatures or pseudocode only, and only where they carry design meaning)
- API reference material (that is generated from the code)
- Narrative about how the design evolved

## 8. Checklist

The common checklist in `SKILL.md` applies as well.

- [ ] File name matches `design-<slug>`, with no sequence number; the slug matches the module's name in the code
- [ ] A module whose sub-modules have their own documents lives in a `modules/<slug>/` subdirectory, with its own `design-<slug>.md` and a `README.md`
- [ ] No alternatives or rationale — ADR referenced by id instead
- [ ] Every diagram is Mermaid and carries prose
- [ ] No diagram drawn to fill a template slot
- [ ] Conditional sections that do not apply are deleted, not left empty
- [ ] Module document follows the fixed section order, including non-responsibilities
- [ ] External interface contract states what is callable and what stability is promised
- [ ] Complexity analysis present for every performance-sensitive algorithm
- [ ] Every implementation-status entry that claims implementation has a code anchor, and the entry states the status and no explanation
- [ ] Status detail appears only in the document it belongs to; `architecture.md` carries one line per top-level module, a module subdirectory's `README.md` one line per sub-module
- [ ] Every entry point appears in the tree's README index exactly once; every file appears in its module subdirectory's README index exactly once
- [ ] `scripts/check_documents.py` was run and exits 0
