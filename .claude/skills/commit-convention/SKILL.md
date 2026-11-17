---
name: commit-convention
description: speed Commit Message Convention — Conventional Commits format, scope list and repository-specific rules for every git commit
triggers:
  - creating a commit
  - writing a commit message
  - git commit
  - committing changes
globs:
  - "**/*"
---

# speed Commit Message Convention

Follows [Conventional Commits](https://www.conventionalcommits.org/); the convention is enforced in review — there is no commitlint config or commit-msg hook in this repository.

**Language**: commit messages are written in **English**, consistent with the repository-wide language rule — everything except the design authority (`docs/design/`, `docs/adr/`, `docs/glossary.md`) is English, because this code is delivered to business projects and read by tooling and non-Chinese-speaking collaborators.

## Format

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

- **Header** is required and must be a single line.
- **Keep the header short — aim for 72 characters or less.** This is a guideline, not a hard limit: a header that needs a few more characters to stay clear is better than a truncated one. Do not sacrifice meaning for the count.
- **Scope is required.** This monorepo holds Go code and npm packages; without a scope there is no way to tell what a commit touched.
- **Description** uses the imperative mood, lowercase, no trailing period: "add subdomain tenant resolution", not "added ..." or "adds ...".
- **Body** explains *why*, not *what* — the diff already shows what changed.
- **Footer** carries breaking-change notices (`BREAKING CHANGE:`) and issue references (`Closes #123`).

## Types

| Type | When to use |
|---|---|
| `feat` | New functionality |
| `fix` | Bug fix |
| `docs` | Documentation only (including ADRs and module `AGENTS.md`) |
| `style` | Formatting only, no logic change |
| `refactor` | Restructuring that is neither a feature nor a fix |
| `perf` | Performance improvement |
| `test` | Adding or updating tests |
| `chore` | Build, tooling, dependencies, CI |
| `i18n` | Adding or updating zh-CN / en-US resources |
| `api` | OpenAPI spec change |

## Scopes

### Go packages under `pkg/`

The scope is the capability directory under `pkg/` the change belongs to: `pkg/core` commits under `core`, and `pkg/tenant/catalog` commits under `tenant` -- the capability directory, never a subpackage. A change inside `pkg/internal/` commits under the shared unit it belongs to: `pkg/internal/pgpool/pgtest` commits under `pgpool`, and `pkg/internal/natstest` under `natstest`. The membership authority is the set of those directories: read the directory rather than a list here, which would go stale.

### Frontend npm packages (`web/packages/<scope>/`)

Same rule: the scope is the package directory name under `web/packages/`, and `web/pnpm-workspace.yaml` is the membership authority. The demo starter's web host is an external workspace member (`starters/demo/web`) and commits under the `starters` scope.

### Cross-cutting

`deps`, `ci`, `starters`, `adr`

Repository-level:
- `repo` — repository-level configuration, guidance and standards (`CLAUDE.md`, `.claude/skills/`, `.gitignore`, the Makefile)
- `site` — the documentation site under `docs/site/`, generated from the earlier implementation

## Examples

```
feat(tenancy): resolve tenant from subdomain

fix(billing): prevent credit over-deduction under concurrent debits

fix(jobs): rebuild tenant context before handler execution

api(billing): add trial_end to the subscription response

i18n(auth-ui): add en-US strings for the login page

test(metering): cover outbox recovery after process crash

chore(deps): bump gorm.io/driver/postgres to v1.6.0

docs(adr): record the decision to use spec-first over code-first

refactor(rbac): extract policy cache to avoid full reload per request

feat(storage)!: return a struct from the presign endpoint

BREAKING CHANGE: SignUpload now returns UploadTicket instead of a
plain string; callers must read the .URL field. Migration notes in
docs/upgrade/v1-to-v2.md
```

## Rules

1. **One logical change per commit.** Do not mix unrelated work.
2. **`main` always passes the gate; a commit inside a landing need not.** Every state `main` reaches, the tip after each landing, builds and passes the checks, so a branch cut from `main` at any moment never carries another task's partial, ungated work. A landing that brings several commits onto `main` at once, such as an integration branch or a task split into logical changes, is checked as a whole: the gate runs on its final commit, and an intermediate commit need not compile or pass on its own. Branches other than `main` carry no such requirement. A `git bisect` across such a landing may have to `git bisect skip` its intermediate commits.
3. **No generic messages.** "fix bug", "update code", "misc" are rejected.
4. **Breaking changes** get `BREAKING CHANGE:` in the footer and a `!` after the scope.
   This repository ships libraries consumed via `go get` / `npm install`, so **any change to an exported signature is breaking**, even when the internal behaviour is unchanged.
5. **Do not tag a release by hand.** How versions are cut is a design decision; `docs/design` and `docs/adr` are where it is settled.
6. **API changes commit the declaration and the regenerated artifacts together.** A change to an OpenAPI declaration or fragment commits the regenerated merged contract and both generated clients in the same commit (`make api-docs`, then `api:gen` in `web` and in `starters/demo/web`). Use the `api` type when the contract is the change.
7. **New user-facing text is bilingual in the same commit** — zh-CN and en-US both, with matching key sets.
8. **Every bug fix carries a test that reproduces the bug** (failing before the fix, passing after). If a test genuinely cannot be added, say so in the body with the reason and the follow-up plan.
9. **Rebase before merging; fast-forward merges only.** A merge commit breaks the linear history this keeps. How a branch is folded and landed is in "Landing a branch" below.
10. **Test helper directories are not scopes.** When touching `testutil` or `testdata`, use the domain scope they serve, e.g. `test(billing): ...`.

## Landing a branch

1. **A branch's history is a draft until it lands.** Before a branch lands on `main` its commits may be rewritten freely. Once a commit is on `main` it is treated as pushed: never amended, rebased or reset. A correction found after landing is a new commit that lands like any other change.
2. **A correction to a commit already on the branch is never a standalone commit.** A test the earlier commit broke, a doc or comment line that belongs to it, a review fix: record it with `git commit --fixup=<sha>` against that commit, or amend when it corrects the branch tip. Use `--fixup` rather than `--squash`, because `--autosquash` folds a fixup without opening an editor while a squash stops to edit the combined message.
3. **Branches land on a merge train.** Every branch approved to land boards a local train branch, `train/<YYYYMMDD-HHMM>`, cut from `origin/main` and never pushed; a single approved branch is a train of one. Each branch is rebased onto the train in turn with `git rebase --autosquash` (current git applies autosquash without `-i`), keeping its own commits. A conflict that resolves mechanically (adjacent lines, a lock file, keys added side by side) is resolved in the later branch, and a generated artifact is regenerated from both sides' declarations (`make api-docs`, then `api:gen` in `web` and in `starters/demo/web`); a conflict that needs a judgement leaves the later branch off the train. The fast checks run first (`mise exec -- make lint`, the touched packages' type checks and tests, the generated-artifact checks), then the gate runs once on the train's tip: `make check` plus every heavier tier any boarded branch's plan requires (`make test-integration`, `make web-browser`, `make e2e`). When resources allow, the caller may run the gate on several prefixes of the train at once and land the longest passing prefix first; the caller decides how many run concurrently from its resources. When the gate passes, `main` fast-forwards to the train's tip and is pushed once. A fix counts as simple when its cause is found within fifteen minutes, it stays within about fifty lines and five files inside one branch's changes or the seam between two branches, and it changes no design, contract shape or reviewed behaviour; a simple fix is recorded on the train with `git commit --fixup` against the commit it corrects, and the gate reruns in full. A seam fix goes into the later branch. The train is fixed at most twice before the failing branch leaves it. A failure that needs rework takes its branch, and any branch depending on it, off the train, and the rest is rebuilt from `origin/main` and gated again.
4. **A task that is one logical change lands as one commit.** A branch lands as several commits only when each is one logical change (rule 1 in "Rules"); the gate runs once, on the branch's final commit, which becomes `main`'s new tip (rule 2). When the commits are not separate logical changes, collapse them, for example `git reset --soft` to the fork point and commit once.
5. **Updates that belong to a change ride in its commit.** A documentation, comment, design-status or user-facing-text update that belongs to a change goes in that change's commit, not in a follow-up. Rule 7 covers text; the `document-standards` skill covers documents.
6. **A gate enforces the fold.** `tools/check_fixup_commits.py`, which `make repo-check` and therefore `make check` run, fails while a `fixup!`, `squash!` or `amend!` commit sits between `origin/main` and HEAD.

## Content constraints

The subject and body say why this change was needed and what it decides — for the person who will dig through `git log` later. They do not narrate the process that produced the change.

1. **No internal process references anywhere** — subject, body or footer. No finding IDs (`P3-…`, "reviewer finding …"), no round names ("the X round"), no internal doc-filename citations as authority. Naming a file the commit itself changes is describing the diff, not a citation. The footer's reference genre (`Closes #…`) is for durable external identifiers such as public issue numbers only.
2. **The body is a concluding why**: the problem, the decision and its cost. No module biography, no process history (review rounds, verification states, follow-up markers). One sentence of "before this change" context is fine.
3. **Numbers that describe this diff** ("from four to six") are fine — they are checkable against the diff. Numbers describing the current state outside this diff ("N modules now …") are not.
4. These constraints bind future commits only — history already on `main` is never rewritten (see "Landing a branch", item 1).

Good body (concluding why):

```
fix(integration): refuse a negative Rate on a layered limit

Rate 0 means a disabled layer; a negative Rate is a value gone wrong, and
the previous check silently folded it into the disabled case, removing a
layer's throttle with no error anywhere. Allow now refuses a negative
Rate up front with an error naming the layer.
```

Bad body (process narration and artifacts):

```
fix(integration): address reviewer finding P3-ratelimit-C

The verify round flagged that the disabled-layer check used <= 0 (the
P3 finding, from the rate-limit-hardening round's review), and after a
NEEDS_FOLLOWUP discussion the impl decided to refuse negatives up front,
closing the finding per the agreed disposition in the review ledger.
```
