# tools

Repository self-checks. Each script is a plain executable with a `--root`
option (default: the current directory) and prints the paths it reports
relative to that root. They are stdlib-only Python, so `python3` is the
whole dependency list — except `check_markdown_examples.py`, which shells
out to the Go toolchain because compiling an example is its job.

Python 3.11 or newer is required: `check_toolchain.py` parses TOML with
`tomllib`. The root `.mise.toml` pins the version `mise install` provides.

## Running them

A script named `check_*.py` is a gate: it takes no arguments, it reports
against the current directory, and `make repo-check` runs every one of
them, so a gate added here is wired by its name alone.

`make repo-check` also runs `make actionlint`, which is a Makefile target
rather than a script here, because its linter is a pinned binary. It lints
every `.yml` and `.yaml` file directly under `.github/workflows/`, from the
repository root whichever directory `make` starts in, with the `actionlint` and
`shellcheck` found on `PATH`. Those are the versions the root `.mise.toml` pins
when `make` runs under mise (an activated shell, or `mise exec`). The target
fails on any finding, on a workflow directory with no workflow file in it, and
when `shellcheck` is not on `PATH`, because actionlint then skips every `run:`
script without a word and exits 0. Neither a red gate nor a workflow finding
stops the other from running, so `make repo-check` reports both.

Each script also runs on its own:

```
python3 tools/check_toolchain.py
python3 tools/check_markdown_examples.py
python3 tools/check_conflict_markers.py
python3 tools/check_fixup_commits.py
python3 tools/check_observability_readiness.py
python3 tools/check_test_tiers.py
python3 tools/check_e2e_honesty.py
python3 tools/check_platform_errors.py
```

Their own test suites run together, from the repository root:

```
make tools-test
```

`test_makefile.py` runs there as well. It is not a script's suite: it holds
the root `Makefile`'s legs that loop over the modules or the gates to running
every item and failing at the end, and the `actionlint` leg to its file roster
and its failure paths, driving the real `Makefile` over scratch directories
with stand-ins for `go`, `golangci-lint`, `actionlint` and `shellcheck` first
on `PATH` and a stand-in for `backends.py` supplied through the `BACKENDS`
variable, so it needs `make` and nothing else.

## The integration stack

`backends.py` is not a gate and takes no part in `make repo-check`. It brings
this checkout's integration backends up and down: `up` derives a compose
project name from the checkout root, allocates four free loopback ports, writes
them with the project name to `.cache/test-backends.env`, and runs compose,
retrying with a fresh allocation if a start fails; `down` takes down only that
project and deletes the state file; `env` prints the four `SPEED_TEST_*`
addresses for the Makefile to export. The root `Makefile` reaches it through
`make deps-up`, `make deps-down` and the integration legs. Its suite is
`test_backends.py`, which `make tools-test` runs as well.

## The scripts

`check_toolchain.py` — fails when a version pinned in the root
`.mise.toml` no longer mirrors the authoritative source that a tool
actually reads. Three pins are mirrors: `go` against `go.work`'s `go`
directive, `node` against `web/.nvmrc`, and `pnpm` against
`web/package.json`'s `packageManager`; the file's own header says which
pins have no mirror and are therefore not checked. Exit codes: 0 clean,
1 drift, 2 infrastructure error (a source missing or unparsable, a
mirrored tool absent from `.mise.toml`).

`check_markdown_examples.py` — compiles or parse-checks the fenced Go code
blocks in markdown prose, which no other harness covers (godoc `Example`
functions compile inside their module's own test binary; prose does not).
A block that carries its own `package` clause is claiming to be real
code and is built and vetted for real, in a throwaway module wired by
`replace` to the checkout it documents. A block without one is a
fragment, and is syntax-checked under several wrappings instead of being
forced into a padded full program. The script's own docstring carries the
scope rules and the escape hatch for a fragment that genuinely cannot
parse.

`check_conflict_markers.py` — fails when a line of git's conflict
markers survives into a file. A resolution done by hand and committed
leaves them as ordinary content: `git status` is clean and no other gate
reads them, since the document checker reads diagrams, sections, indexes
and links and the example checker reads Go blocks. The three labeled
markers are findings on their own; the bare middle one is reported only
in a file that also carries a labeled one, because a line of seven `=`
alone is also how Markdown underlines a heading. Everything text-shaped
in the tree is in scope, `node_modules` and non-UTF-8 files excepted.
Exit codes: 0 clean, 1 markers found (each with file, line and text),
2 infrastructure error (the root is not a directory, or the scan read no
text file at all).

`check_fixup_commits.py` — fails when a commit whose subject starts with
`fixup! `, `squash! ` or `amend! ` sits on the branch beyond its base. Such a
commit is a correction waiting to be folded into the commit it corrects by
`git rebase --autosquash main`; landed as it is, it becomes a standalone
commit on a history that is never rewritten. The range is `origin/main..HEAD`
when `origin/main` resolves in the repository at `--root`, `main..HEAD`
otherwise, and HEAD equal to the base is an empty range. Each hit is reported
with its short SHA and subject. A root outside a git work tree, or one where
neither ref resolves, has no range to read: one line says nothing was
checked and the exit code is 0. Exit codes: 0 clean or nothing checked,
1 at least one such commit.

`check_observability_readiness.py` — fails when a package under `pkg/`
breaks one of the observability obligations that three Go tests guard, each
with a hand-written list of the modules it covers so that a module absent
from the list passes silently: a package that resolves the logging
capability must declare the requirement, a package that declares a
`Sensitive` config item must register its value as a redaction rule (checked
per item: a value is attributed to the items only when the registration that
hands it over carries a module-prefixed label, and the `Secrets` hook is read
in its `[]string`-returning shapes, not as any field merely named
`Secrets`), and a package that opens an egress client must wrap its
transport. A fourth check is the reverse assertion, that `pkg/` must call
none of the package-level entries of the upstream otel global. The scan
derives the call sites from the source rather than restating the tests'
lists, and the presence patterns read the code with comments and the
contents of string and rune literals masked out, so a call named only in a
comment or quoted in a string is not a call site. Each check carries a
positive control and treats an empty subject as an infrastructure error. The
checks are text heuristics, not a type analysis: a deliberately constructed
spelling can defeat the shape a registration is drawn in, so a clean run
here is a backstop, not a guarantee — the type-level guarantee lives in the
Go tests this gate backstops (`pkg/internal/logdeps` and the per-package
`secrecy_test.go`). Exit codes: 0 clean, 1 a finding (each with file, line
and what is missing), 2 infrastructure error (the root is not a directory,
`pkg/` is missing, no Go file was read, or a positive control saw an empty
set).


`check_e2e_honesty.py` — holds the end-to-end tier's claim that its journeys
prove the product through a real browser. It checks that every journey
document under `docs/prd/journeys` is either declared by a spec or registered
`pending` or `unreachable` with a reason, and rejects any other status; that
each spec declares one known journey, covers only ids that document lists, and
contains at least one `test(...)` that runs (`test.skip`/`test.fixme` do not
count); that no e2e source issues network requests, intercepts routes,
evaluates script or uses a non-WebAuthn CDP domain; that a spec or helper does
not import the process, a filesystem write or a database client, and that only
`fixtures/log.ts` parses logfmt lines or reads `verification_code`; that every
Playwright artifact path (`outputDir`, the HTML reporter's `outputFolder`, any
`outputFile`) resolves under `/tmp`; and that the root Makefile has an `e2e`
target while `check` does not reach the tier by target name, `playwright test`
or `typecheck:e2e`. Every run also prints the covered / pending / unreachable
counts, so the journeys still unverified are visible rather than implied.
Exit codes: 0 clean, 1 findings (each with its path, and a line for a token
hit), 2 infrastructure error (the root is not a directory, the journeys
directory is missing or empty, the e2e directory is missing, or the journey
registry cannot be parsed).

`check_test_tiers.py` — holds the two rulings of the test-tier decision
that nothing else in the tree holds, each of which fails silently. A
build tag no leg passes takes its file out of every leg at once: `go vet
./...` and `go vet -tags=integration ./...` both exit 0 on a file tagged
`intergration`, and `go test -tags=integration` then reports no test
files, so the cases are gone with nothing red anywhere. The gate reads
the tags from the Makefile's `-tags=` flags and from its `-race` — a leg
passing the race detector establishes that tag, and a target merely named
`test-race` does not — and fails a file whose constraint names anything
else, which is the same finding whichever side the misspelling is on. The
second ruling is that a backend which cannot be reached fails a case
rather than skipping it, and `go test` exits 0 on a binary whose cases
all skipped: a non-test Go file that imports `testing` and calls a skip
method is a finding, since that is where a fixture's skip has to live,
while a `_test.go` file is left alone because a case may skip for a
reason of its own. Tags are read from the head of a file, before its
package clause, which is where Go reads them — a comment quoting one
below the clause is prose. Exit codes: 0 clean, 1 a finding (each with
file, line and what it is), 2 infrastructure error (the root is not a
directory, the Makefile passes no tag, or a check found no subject at all
— the integration layer gone, or no library to read).

`check_platform_errors.py` — holds the platform error-copy bundle
(`web/packages/i18n/src/platform-errors/locales/{zh-CN,en-US}.json`) to
the code set the backend can write. The frontend resolves a refusal
through that bundle by the `<module>.<code>` form of its RFC 9457 problem
type (`urn:speed:<module>:<code>`, each dash in the code normalized to an
underscore), so a code the backend writes without a bundle entry renders
as the raw key, and an entry no backend writes is dead copy no lookup can
reach. The gate derives the writable set from the Go source of the two
modules `go.work` lists (`pkg/` and `starters/`) rather than restating
it: a whole URN literal, a package's `urn:speed:<module>:` prefix plus a
literal or a resolved constant (`mount.CodeLoginFailed`, from the same
package or an imported one), a helper's call-site arguments when it takes
the code as a parameter, and a mount-style `Code…` table. A missing code
is a finding; so is an entry the backend never writes, unless it is in
the script's `KEPT` list with the frontend files that still refer to it —
legacy codes the web packages name themselves (the shared
session-lifecycle vocabulary, tests that pin a fallback lookup) would
break their own callers if deleted. A `KEPT` entry whose reference is
gone is itself a finding, so the list cannot rot silently, and a
duplicate key in either language file is a finding unless `DUPLICATES`
names it. Both language files must carry the same leaf set. The read is a
text heuristic, not a type analysis: a code built by a shape the rules do
not recognize is invisible to it, so a clean run is a backstop for the
ways this tree writes problem types rather than a proof that no other
shape exists. Exit codes: 0 clean, 1 a finding (each with the key and
what is wrong with it), 2 infrastructure error (the root is not a
directory, a backend root or the locale directory is missing, a Go file
cannot be read, a helper's call sites named no code, or the derivation
found nothing at all).
