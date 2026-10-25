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

Each script also runs on its own:

```
python3 tools/check_toolchain.py
python3 tools/check_markdown_examples.py
python3 tools/check_workflow_refs.py
python3 tools/check_conflict_markers.py
python3 tools/check_observability_readiness.py
python3 tools/check_test_tiers.py
python3 tools/check_e2e_honesty.py
```

Their own test suites run together, from the repository root:

```
make tools-test
```

`test_makefile.py` runs there as well. It is not a script's suite: it holds
the root `Makefile`'s legs that loop over the modules or the gates to running
every item and failing at the end, driving the real `Makefile` over scratch
directories with stand-ins for `go`, `golangci-lint` and `backends.py` first on
`PATH`, so it needs `make` and nothing else.

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

`check_workflow_refs.py` — fails when a coding-flow template copy under
`.github/workflows/` carries a relative path segment (a `.` or `..`
that begins one — followed by `/` and not preceded by another `.`, so an
ellipsis is not a hit). The templates are byte-copies of files that
live inside the upstream skill directory, whose own cross-references
(the `../../SKILL.md`-style two-level form, once carried and gone now)
are written relative to it and can arrive with a
sync; copied into a consuming repository the same text points into a
different tree. The gate does not judge where a given reference would
land — a copy's relative path segment is treated as suspected to dangle,
so an intentional repository-relative reference is a hit too, a
criterion trade-off rather than an inline exemption. Guarded files are
the lowercase `.yml`/`.yaml` files carrying a
`uses: vislake/coding-flows@...` line — the call to the composite action
upstream publishes — so this repository's own workflows may keep using
repository-relative paths, and a fifth template lands under the gate
with nothing to maintain here. Exit codes: 0 clean (every guarded file
reported by name), 1 a relative reference found, 2 infrastructure error
(no `.github/workflows/` directory, no guarded file to check, or a file
that cannot be read).

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
