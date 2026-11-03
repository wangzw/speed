---
name: document-standards
description: Documentation standards for design documents, requirements documents and architecture decision records — path and language configuration, the rules common to every document type, and the entry point to each type's own standard. Load before creating or editing any document under the configured documentation paths. Use when writing or editing a design document, creating an architecture overview, documenting a module or adding one to the design docs, writing a requirements document, writing an architecture decision record, or reviewing documentation.
---

# Document Standards

The authoritative standard for project documentation. **This document tells you how to write the documents**; each document tells its reader how the system is built.

This file carries what is common to every document type, and points at each type's own standard. Load the type's standard as well before writing, and run the structural checker (section 5) before calling the work finished.

**The premise that overrides everything else**: a document states the current conclusion, not the process that produced it. It says what the system is, not how the team arrived at it, not who decided it, and not when. Every rule below follows from that premise, and the premise outranks any stylistic preference.

---

## 1. Configuration

Documentation paths and language are declared in `CLAUDE.md` under a fixed heading, with lowercase keys:

```markdown
## Documentation

- design: docs/design
- requirements: docs/prd
- adr: docs/adr
- glossary: docs/glossary.md
- references: docs/references.md
- language: zh-CN
```

**Required:**
- Read this block before creating or editing any document. A missing key takes its default: `design` → `docs/design`, `requirements` → `docs/prd`, `adr` → `docs/adr`, `glossary` → `docs/glossary.md`, `references` → `docs/references.md`.
- Resolve the document language in this order: the `language` key; the language of the documents already in the target directory; otherwise **ask the user**. All three inconclusive means ask — never pick a language silently.
- **Project-wide language rules outrank the `language` key.** Where the project already fixes the language of a directory, that rule wins and a `language` declaration never licenses a violation of it.

## 2. Rules Common to Every Document

**Conclusions only.**
- **DO NOT** record how the document was built: no change history, no version number, no author, no date, no review record, no "originally X, later changed to Y" narrative. Git is the authority for all of it. An ADR obeys this in full: it records the decision and the alternatives weighed, never the work of revising it.
- **DO NOT** record project management: no TODO, no schedule, no milestone, no owner, no priority.
- **DO NOT** duplicate the half of another document that only it carries. A design document states the design, including decisions already recorded elsewhere; what belongs to the ADR alone is the context, the options weighed, why each was rejected and the costs accepted. Reference a requirement by id and a decision by ADR id, and do not restate that half.

**No first person, no subjective language.**
- **DO NOT** use first person in any form (`we`, `our`, `I`, `the author`). The assertions in a document are guaranteed by the code and the constraints, not by a narrator. **This holds for ADRs too**: write `Adopt X`, never `We decided to adopt X`.
- **DO NOT** use second person or address the reader. A standard-conforming document is not a tutorial.
- **DO NOT** use subjective modifiers and filler. The test is whether deleting the word changes what the document asserts; if it does not, it was praising, apologising or padding. `obviously`, `simply`, `easily`, `elegant`, `powerful`, `efficient`, `excellent`, `note that`, `it is worth noting`, `unfortunately`, `of course` and `in fact` are the common ones, not the whole of it — a word that passes the test is banned whether or not it appears here, in whatever language the document is written in. They add no information and routinely disguise unverified claims.
- **DO NOT** use restating transitions — any phrase announcing that the previous sentence is about to be said again, `in other words`, `that is to say`, `in summary` and `to sum up` among them. Needing to say it in other words means the previous sentence should be rewritten instead.
- **Replace every subjective judgement with a measurable quantity or an explicit constraint.** `Performance is good` becomes `P99 latency target 50ms`. `X was chosen because Y` becomes `Adopt X (ADR-tenant-isolation)`.

**A rule names what it binds.**
- **A normative sentence states the subject it constrains, not only the constraint.** `every`, `all`, `always` and `never` are the visible signal, but **a bare plural noun is the common one**: `Package-level functions and variables are limited to ...` contains no quantifier at all, and its subject is nonetheless every package-level function, including the unexported helpers no implementation can do without. `Exported package-level functions and variables ...` is the sentence that was meant.
- **The test: take an ordinary member of the subject and apply the rule to it literally.** Ordinary members come from two places — something this document already describes and plainly endorses, **or something any implementation of it must contain**. The second source is not optional: unexported helpers appear nowhere in an architecture document, and only "every Go implementation has them" catches the sentence above. If the literal reading forbids that member, the scope is missing. **A rule that outlaws the system's own accepted practice is not strict, it is unfinished.**
- **A layer's rule binds that layer.** A rule about what a component must produce does not reach what merely passes through it. `The table must cover every startup failure` reached failures the component only relays, which would have obliged it to name every failure mode of every module — in a component whose whole point is that it knows none of them.

**No valueless counts.**
- **DO NOT** state the cardinality of a structure the document already lists: `the project contains 21 modules`, `divided into five stages`, `there are 3 extension points`. The only source of that number is the table right below it, the reader can count it, and every addition or removal requires editing it. **Write the list, never the size of the list.**
- **DO NOT** refer to enumerated items by position or count: `the former`, `the three above`, `the first two`. Inserting an item invalidates all of them. Name the item instead.
- Numbers that *are* the design stay: complexity bounds, performance and capacity targets, protocol-fixed widths, timeouts, retry counts, thresholds, and closed value sets that are themselves a constraint. The test: **when this number changes, has the design changed, or has a list merely grown?** Delete the second kind.

**Criteria, not catalogues.**
- **When a list is what a rule means, write the criterion and let the list illustrate it.** A list used as the definition must be amended every time a case appears that the criterion already covered, and that amendment is exactly the edit nobody comes back to make.
- **The test is the one above**: add a member — has the design changed, or has a list merely grown? A closed value set that is itself the constraint stays exhaustive (the lifecycle's stages, the permitted status values). A list of instances that all satisfy one stated test is illustrative.
- **Say which kind it is.** An exhaustive list a reader may rely on and an open list of examples look identical on the page, so the difference belongs in the sentence that introduces the list. Leaving it unsaid is how a list written as examples comes to be cited elsewhere as the complete definition.

**One language per document.**
- Mixed-language text is allowed only for universally recognized terms, for agreed terms **registered in the glossary that covers them** — the project glossary, or the local one of the context the document sits in — and for the name of a source registered in the project references file, which is a proper name. Nothing else.

**Glossary.**
- **Registering a term is the exception, not the habit.** A word earns an entry only when the project has given it a meaning the reader would not arrive at unaided, and reading it the ordinary way changes what the document says. Universally recognized vocabulary stays out, and so does a word this project uses in its ordinary sense: an entry that tells the reader what they already knew adds maintenance and dilutes the entries that carry real meaning.
- **The project glossary, at the configured `glossary` path, holds only the terms that hold project-wide** — the same meaning in every document, every document type and every part of the system. It is the one glossary every document shares, and it is mandatory once the documentation uses any term registered in it.
- **A term whose meaning is bounded by a context does not go into the project glossary.** A word defined by one module, one document or one subsystem means something else, or nothing, elsewhere — and the project glossary is read from everywhere. Registering it there makes the contextual meaning look global, which is the more expensive mistake: it licenses the term's use in documents its definition was never meant to reach.
- **Where a context genuinely needs its terms registered, it registers them itself** — the optional glossary section each document type's standard places in its fixed order, or a glossary file beside the documents of that scope — and the local glossary states the scope it applies to. **DO NOT** create one per documentation tree as a matter of course: a glossary that exists because the layout suggested one, rather than because a context needed it, is what drifts apart from the project glossary.
- **A local glossary never redefines, narrows or restates a project term.** Two definitions of one term is a defect regardless of which is right. A term that outgrows its context moves up — it enters the project glossary and leaves the local one; a project term never moves down.
- **Every entry carries the term and its English counterpart** — the one English word or phrase that the English documentation, the public interface and the code's identifiers all use. It is a fixed name, not a translation of the definition. It is required in every document not written in English: the counterpart is what lets a Chinese document and its English version be matched entry by entry, and what lets a reader find the concept in the code. Without it the translation picks its own word, which is the drift the glossary exists to prevent.
- **A glossary is a table, in a fixed column order**: `Term | English | Scope | Definition` for a local glossary, `Term | English | Definition` for the project glossary. **A document written in English omits the English column**, because its term column is that form. A definition list is not a glossary under this standard: the entry that loses its counterpart is the one nobody notices, and only a column can be checked.
- **The counterpart is reused verbatim**, like the term itself: a second English rendering of one term is synonym drift one language over.
- **Once a term is registered, its wording is uniform throughout the scope that registered it** — the whole documentation for a project term, the owning context for a local one. Synonym drift is a defect, in either language.
- **Uniformity binds every document.** An ADR is revised in place like every other document, so a term rename reaches it too; no record keeps an outdated wording.

**External sources.**
- **A document cites what the project did not originate and what the document cannot vouch for by itself.** Two tests, and either one makes the citation mandatory. *Attribution*: the sentence states something the project did not create — a component, an algorithm, a protocol, a format, a constraint — and a reader could take it for the project's own work. *Traceability*: the authority behind the sentence is the source itself — a format a specification defines, a clause a regulation imposes, a number a standard fixes — so the reader has to be able to open it. The combined test: **delete the citation; can the sentence now be read as the project's own work, or as an assertion with nothing behind it? Either way, the citation stays.**
- **A source every reader attributes correctly unaided is not cited** — TCP, JSON, SQL. This is the glossary's universally-recognized test applied to sources, and it is what keeps citing from degrading into decoration. A recent open-source project the system embeds is the paradigm case on the other side: nothing in the text tells the reader it was not built here.
- **The project references file, at the configured `references` path, holds one entry per public external source the documentation cites.** It is the one bibliography every document type shares, it is not part of any documentation tree, and it is mandatory, like the glossary. An entry identifies the source and nothing more: the identifier `REF-<slug>`, the source's own name, one line saying what kind of thing it is and what it covers, the URL, and the version, commit, edition or date the cited content depends on — `—` when it depends on none. That column records the cited source's version, which is content; the ban on version numbers above is about the document's own.
- **An entry carries no reason and no usage.** Why the source was chosen belongs to an ADR; how the system uses it belongs to the design document; an evaluation belongs nowhere.
- **The file registers public sources only.** The source column is a URL any reader can open. Material the project does not own but that is not public — another internal project's documents, a contract, a counterpart's interface agreement — is not registered here; a requirement that traces to such an authority names it in words, as the requirements standard describes.
- **`REF-<slug>` names the source, is unique in the file, and is permanent.** The slug is lowercase kebab-case ASCII, like an ADR slug, and renaming it breaks every citation. One source, one entry: a version is a column, not a second row.
- **A document cites a source once, at the first sentence that relies on it**, by identifier plus link; later mentions in the same document use the name alone.
- **There is no per-document reference section, in any document type.** The citations are in the sentences that need them. A list of them at the end of the document restates the body, and it is the restatement that drifts when a sentence is rewritten; every document type with a fixed section set rejects such a section.
- **A citation of an unregistered identifier and an entry no document cites are both defects.** An uncited entry is an orphan, like a document absent from its index.

**Examples convey intent, not runnability.**
- Example code **is not required to compile**, to be complete, or to match the code verbatim. Omit imports, error-handling boilerplate and initialization; `...` for elided parts is fine.
- **Trim every example to the part that carries design meaning.** Pasting a whole function turns the document into a copy of the code, which rots and carries no value. Runnable snippets belong in the code and its tests.
- This governs documentation only. Code-side rules requiring compilable examples (godoc `Example*` functions, module `AGENTS.md`) are unaffected.

**Diagrams render, or they do not ship.**
- **After editing a document that contains a diagram, render every diagram in that document and confirm every one succeeds.** This is mandatory, not advisory: a diagram that fails to parse is invisible to the reader, and the failure shows up nowhere in a plain text diff.
- **Verification means running a renderer over the blocks**, not reading them. A block that looks well-formed is not evidence.
- Verify every diagram in the edited document, not only the blocks touched. An edit elsewhere in the file can break an untouched block.
- An **empty diagram body** is the most frequent parse failure — `classDiagram` with no members fails outright. A diagram with nothing in it also renders blank, which carries no information even where it happens to parse.

**Timeliness.**
- **A code change that changes what a document asserts updates that document in the same commit.** This is the only mechanism that keeps documentation alive.
- When a document and the code disagree, the code is authoritative **and the disagreement is a defect**: fix the document.

**References.**
- Internal links are relative and point at files. Write the path from the documentation root consistently; mismatched directory depth is the most frequent link defect.
- Reference code by **path plus symbol name, never a line number**. Line numbers rot on the next edit.
- Reference a decision by its **ADR identifier (`ADR-<slug>`) plus link**.
- Reference a public external source by its **reference identifier (`REF-<slug>`) plus link to the project references file**: `embeds etcd's raft library ([REF-etcd-raft](../references.md))`.
- **References flow one way across document types**: a design document references requirements and ADRs; requirements and ADRs do not link back. Bidirectional links between the two layers rot, because the upper document changes on a different rhythm from the lower one.
- **Within one type, documents reference each other freely.** Two design documents describing modules that call each other say so in both; one ADR cites another it depends on. The rule above is about layers, not about neighbours.

## 3. Document Types

| Type | Default path | Standard |
|---|---|---|
| Design | `docs/design` | [Design Documents](references/design-documents.md) |
| Requirements | `docs/prd` | [Requirements Documents](references/requirements-documents.md) |
| ADR | `docs/adr` | [Decision Records](references/decision-records.md) |

## 4. Templates

`templates/` holds the starting point for each document type. They carry the mandatory section order; delete the sections that do not apply rather than leaving them empty. `templates/glossary.md` and `templates/references.md` are the starting points for the two files every document type shares.

## 5. Mechanical Check

`scripts/check_documents.py` checks the structural half of this standard: section
presence and order, file naming, index coverage, status vocabulary, diagram
bodies, glossary columns and every entry's English counterpart, the process
metadata a document never carries, and whether every `REF-<slug>` citation
resolves to an entry of the project references file that is itself cited.
It reads the paths and the language from the `## Documentation` block of
section 1, skips a declared tree that does not exist, and treats a
configuration under which no tree exists, or a value that is not a single
path, as a usage error (exit 2).

**Running it is mandatory, not advisory.** After creating or editing any document
under the configured paths, run it from the repository root and leave it at exit 0.
The script lives in this skill's `scripts/` directory, wherever the skill is installed:

```bash
python3 ~/.claude/skills/document-standards/scripts/check_documents.py   # user-level install
python3 .claude/skills/document-standards/scripts/check_documents.py    # repository-level install
```

Every finding is printed as `path:line: what is wrong`. Fix the documents; do not
silence the checker.

**What it does not check** — and therefore what review still owns: subjective
language, first and second person, restating transitions, valueless counts,
whether a non-functional number is sourced, whether a rejected option's reason is
real, whether a requirement is testable, whether a word deserves a glossary entry
at all and whether it belongs to the project glossary or to a local one, whether a
normative sentence's literal scope is the one intended, whether a list is meant as
a criterion or as an exhaustive set, whether a sentence that relies on an external
source cites it, whether a cited source is one the reader would have attributed
correctly unaided, whether a journey is a process the business actually runs, whether
a status entry carries an explanation it should not, and
whether a document still matches the code it asserts things about. It also does not render diagrams: it checks that a
mermaid block has a type and a body, while the rendering rule in section 2 stays a
step performed during the edit.

`scripts/test_check_documents.py`, beside the checker, is its planted-violation
suite, run the same way. A new rule in this standard earns a check there, or it is
not enforced.

## 6. Common Checklist

Each document type's standard adds its own checklist on top of this one.

- [ ] Path and language resolved from `CLAUDE.md`, not assumed
- [ ] One language throughout; every mixed-in term is universally recognized, registered in the glossary covering its scope, or the name of a registered source
- [ ] Every glossary entry carries the term's English counterpart, and its scope where the glossary is local; in an English document the English column is omitted
- [ ] Every sentence the attribution or traceability test catches cites its source by `REF-<slug>` at first use, and every cited source is registered in the project references file
- [ ] No first person, no second person, no subjective modifiers, no restating transitions
- [ ] No counts of structures, no positional references to enumerated items
- [ ] Every normative sentence names what it binds, and its literal reading was applied to an ordinary member of that subject
- [ ] A list that defines gives the criterion with examples; a list meant to be exhaustive says so
- [ ] No history, no version, no author, no date, no TODO, no schedule
- [ ] Content owned by another document is referenced by id, not restated
- [ ] Every diagram in the edited document was rendered and confirmed to succeed
- [ ] Code references carry no line numbers
- [ ] The document changed in the same commit as the code that changed what it asserts
- [ ] `scripts/check_documents.py` was run and exits 0
