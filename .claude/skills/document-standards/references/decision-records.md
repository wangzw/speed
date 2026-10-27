# Architecture Decision Record Standard

Read together with `SKILL.md`, whose common rules apply in full and are not repeated here.

**The premise for this document type**: the ADR tree records the decisions in force. Each record states one architecturally significant decision: the constraints that forced it, the options weighed, the one adopted, and the price accepted. It carries the reasoning behind the decision, not the project's work history.

This does not contradict the conclusions-only rule. What that rule bans is recording how a *document* was written; the options an ADR weighs are the **content of the decision**, not process residue. The line to hold: **an ADR is not meeting minutes and not a changelog.** It states what the options were and why each lost. It does not narrate how the discussion got there, who argued for what, or how the record itself has changed over time.

---

## 1. What Warrants a Record

The failure mode of this document type is inflation: hundreds of records, none of them read.

**Required:**
- A decision earns a record when it is **architecturally significant** — hard to reverse, or affecting module boundaries, public API, the data model, deployment shape, external dependencies or security posture.
- A choice that can be reversed within a day gets no record.
- **One decision per record.** A record that needs many pages is several decisions that have not been separated.

## 2. A Record Is Revised in Place

**A record states the current decision on its subject.** It is an ordinary document in this respect: when the decision changes, the record changes with it. There is no frozen original and no successor record.

**The tree holds only decisions in force.** A record whose decision no longer applies is rewritten into the decision that replaced it, or removed when nothing replaces it. Whether a decision is in force is therefore not a property the record carries: there is no status field, and a record that would need one does not belong in the tree.

- Rewrite, restructure, retitle, rename the file, merge or split records — drafting and revision are the same operation.
- **Revising a record leaves no trace in the record.** No revision note, no date, no "updated", no changelog, no "originally X, later changed to Y". An error found and fixed leaves a corrected record, nothing more. The record must read as if the current decision is the one that was always there.
- **A replaced decision survives only as an option.** When the current decision displaces an earlier one, the earlier decision appears in *Options Considered* if it was a real alternative, with the reason it was rejected. It is not recorded as "what was decided before" and gets no separate paragraph. If it was not a real alternative, it is dropped.
- **The reference check is the cost of this freedom.** Other documents cite the record, and code follows it. After revising, confirm that every citation still says something true — that a design document referencing `ADR-<slug>` has not been left asserting the option the record no longer takes.

**This is why the ADR is a supplement to the design.** Both are revised on the same rhythm, in place, from the same authority: the current state of the system. Keeping a chain of successor records would put the two document types on different rules for no gain, and the ADR's value is its reasoning, which a fresh record does not preserve any better than a rewritten one.

## 3. Layout and Naming

```
docs/adr/                       # relocatable via CLAUDE.md
├── README.md                   # entry point: brief introduction + index grouped by topic
└── adr-<slug>.md
```

**Required:**
- **The slug names the decision, and is unique within the tree.** It is lowercase kebab-case ASCII. It carries no date and no sequence number: neither is part of the decision's identity, and a record's file name changes only when the decision it names changes enough to deserve a different slug.
- The reference form is `ADR-<slug>`.
- **Two records may share a topic without sharing a slug.** A topic can hold several decisions that all still apply; each gets a slug naming its own decision, and the README groups them under the topic. A record whose subject is the same decision as an existing one is that record, revised — not a second file.
- **One decision, one record.** A record that needs many pages is several decisions that have not been separated. Resolve that by splitting, not by adding a file per revision.

Template: `templates/adr-record.md`.

## 4. Sections

Fixed order. Only the last is optional. A record opens with its title and goes straight to the decision's context; there is no status and no other field between them.

### Context

The forces that made a decision necessary: the constraints in play, what was being blocked, what had to be true.

Facts and constraints, not narrative. **DO NOT** write how the team came to look at the problem.

### Options Considered

Each option, and what adopting it would have meant.

- **Every rejected option states why it was rejected.** An option listed with no rejection reason is decoration — it makes the record look thorough while carrying no information, and it is the most common way this section degrades.
- **Options are the only place a displaced decision may appear.** It is an alternative that lost, stated as such; it is not a "previous decision" subsection and carries no revision narrative.
- Options are described as they stand, not as a sequence of drafts. A better option found later is folded into this section with its rejection or adoption, not appended as an update.

### Decision

The decision, stated impersonally: `Adopt X.`

**DO NOT** write `We decided to adopt X`. This standard bans first person in every document type, and an ADR is not exempt despite the conventional wording of the form elsewhere.

### Consequences

What the project is now committed to.

**Negative consequences are mandatory.** Every architectural decision buys something at a price, and the price is the part a future reader needs. A record listing only benefits is a sales pitch, not a decision record — and it is unusable for judging, later, whether the trade still holds.

### Invalidation Conditions

**Optional.** What would show this decision to have been wrong: the measurement that would contradict it, the scale at which it stops holding, the assumption whose failure undoes it.

Worth writing when the decision rests on an assumption that can actually be checked later. The condition states when the decision stops holding; what happens to the record is governed by section 2 — rewritten into the decision that replaced it, or removed when nothing replaces it.

## 5. Division of Labour with the Design Document

- **The ADR carries the decision and its consequences. It does not carry the design that follows from the decision** — that belongs to the design document.
- **The design document references the ADR and does not restate it.**

Both halves matter. Describing the same thing in both places guarantees the two descriptions drift, and then neither can be trusted.

## 6. `README.md`

A brief introduction, then the index **grouped by topic**, each record listed by its title.

**Required:**
- **The index covers every record, each appearing exactly once.** A record absent from the index is an orphan, and an orphan is an error.
- Grouping by topic is what makes the index worth having: a reader sees a topic's decisions together.
- **No date column and no status column.** The index names the decision and links it. When the record is revised, the index row stays as it is unless the title or the file name changed.

Template: `templates/adr-readme.md`.

## 7. What Never Appears in a Record

- A status, state or acceptance field
- Meeting minutes, discussion narrative, who argued for what
- Change history in any form: revision notes, dates, "originally", "previously", "updated", a changelog, a list of fixes
- The design detail that follows from the decision (that is the design document's)
- A displaced decision recorded as history rather than as a rejected option
- Proposals that have not been decided
- TODO lists, schedules, owners

## 8. Checklist

The common checklist in `SKILL.md` applies as well.

- [ ] The decision is architecturally significant; a same-day-reversible choice got no record
- [ ] One decision in the record
- [ ] File name is `adr-<slug>`, lowercase kebab-case, with no date and no sequence number; the slug names the decision and is unique
- [ ] No status, state or acceptance field
- [ ] Every rejected option states why it was rejected
- [ ] A displaced decision, if it appears at all, appears as a rejected option with no revision narrative
- [ ] The decision is stated impersonally: `Adopt X`
- [ ] Consequences include the costs accepted, not only the benefits
- [ ] No revision note, date, "originally", changelog or other change history anywhere in the record
- [ ] No design detail that belongs to the design document
- [ ] Every document citing this record was rechecked after the revision
- [ ] The record appears in the README index exactly once, under its topic
- [ ] `scripts/check_documents.py` was run and exits 0
