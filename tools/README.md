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

actionlint does not read the steps of a composite action, so the same target
then runs `lint_composite_actions.py`, which finds the `run:` steps of
`.github/actions/*/action.yml` and `action.yaml` and runs the `shellcheck` found
on `PATH` over each one that names `bash` or `sh` as its shell, with the flags
and the `set -eo pipefail` (bash) or `set -e` (sh) line actionlint gives a
workflow script, and with every `${{ ... }}` replaced by underscores of the same
length. A step in any other shell is skipped, a `run:` step with no `shell:` is
a finding, and a finding is reported at its line in the action file. A workflow
finding does not stop it from running, and the target fails for either. Exit
codes: 0 clean, 1 a finding, 2 infrastructure error (the root is not a
directory, no composite action exists, or `shellcheck` is missing or does not
answer in JSON).

Each script also runs on its own:

```
python3 tools/check_toolchain.py
python3 tools/check_markdown_examples.py
python3 tools/check_conflict_markers.py
python3 tools/check_fixup_commits.py
python3 tools/check_observability_readiness.py
python3 tools/check_test_tiers.py
python3 tools/check_test_limits.py
python3 tools/check_e2e_honesty.py
python3 tools/check_platform_errors.py
python3 tools/check_pnpm_filters.py
python3 tools/check_web_build_scripts.py
python3 tools/check_ci_jobs.py
python3 tools/lint_composite_actions.py
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
variable, so it needs `make` and nothing else. It also holds `check` to its
segment set and order — every segment exactly once, lint before the tests,
each announcing the time it starts — to the product dependencies that let a
parallel run reach the same segments (the frontend build that writes the
embedded bundle, ordered after every Go leg that compiles it), and
`web-check` to the three phases it splits into, whose commands together are
the frontend gate. Every tool that takes a width setting is held to
`GATE_JOBS` there as well: go build, go test and go vet, golangci-lint,
vitest, Playwright and pnpm -r must receive the width, default 4 and
following an override, so none falls back to the host's core count. The
cases for the gate's structure read the `Makefile` rather than drive it: a
segment dropped from the gate runs nowhere else, so the expectation is
written out in the test instead of derived from the file. The width cases
drive the legs under the stand-ins and read what each tool was handed.

`test_deploy_workflow.py` is likewise no script's suite: it holds
`.github/workflows/deploy.yml` to running only on a manual dispatch, to failing
a dispatch outside `main` (it runs the `refuse` job's script and requires a
non-zero exit and an error naming the ref), to keeping `decide` on `main`, and
to refusing a commit whose `check` did not succeed (it runs the gate
against a stubbed Actions API with failed, cancelled, queued or in-progress,
empty and successful run sets, and with a failing query), to counting a
successful push run or a run dispatched on `main` and to refusing a
pull-request run or a run on another branch, to granting `actions: read` to
`decide` alone, to the gate
step carrying no `continue-on-error` and only the tip guard that lets it run,
and to installing go alone through the setup action's `tools` input.

`test_demo_redeploy_script.py` is likewise no script's suite: it holds
`starters/demo/deploy/redeploy.sh` to a three-valued health verdict. It keeps
the data when the machine checks pass (healthy), wakes a machine that
auto-stop left stopped and reads it again, falls back to the public `/healthz`
probe only when the checks cannot be read, rebuilds the volume only when a
started machine's check stays failing (unhealthy), and destroys nothing when
it cannot tell (indeterminate: flyctl failed, the output was not usable, a
required check was absent, a wake failed, or the probe did not answer 200). It
also holds the pre-destruction gate: the script re-reads the machine and volume
lists once before the confirmation and once after, and reports "cannot
determine cloud state" instead of destroying when either read fails. It drives
the real script with stand-ins for `flyctl`, `make`, `curl` and `python3` first
on `PATH`, each logging its arguments and answering from a control file, so the
cases cover a healthy deploy, a failed deploy command whose checks still pass,
a stopped machine that wakes to passing, a wake that fails, a network failure
with a 000 probe that destroys nothing, a started machine with a failing check
that reaches the rebuild, a failed cloud-state read that stops before
destroying, the health wait after the second deploy, a failed second deploy, a
destroy that fails midway and reports what was done, the confirmation refusal,
a typed `rebuild` acceptance, `--yes` rebuilding in order, fly.toml values
overriding the built-in defaults, environment values overriding both,
`--dry-run`, `--help` and an unknown argument without a network, a cloud
account or a real deployment.

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
pins have no mirror and are therefore not checked. The demo image's
toolchain is held to the same sources: the tag of every `golang` image in
`starters/demo/Dockerfile` must carry `go.work`'s `go` directive, and the
major of every `node` image must equal `web/.nvmrc`'s. Exit codes: 0 clean,
1 drift, 2 infrastructure error (a source or the Dockerfile missing or
unparsable, a mirrored tool absent from `.mise.toml`, a mirrored image
absent from the Dockerfile).

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
reads them, since the example checker reads only Go blocks and no gate
reads a document's diagrams, sections, indexes or links. The three labeled
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
else, which is the same finding whichever side the misspelling is on. A
GOOS or GOARCH term (a name in `KnownOS` or `KnownArch` of go/build's
`internal/syslist`, in a constraint or as a `_linux`-style file-name suffix;
the sets are wider than the ports `go tool dist list` prints, and the suite
holds them to both) is not a tag a leg passes but the
platform a run executes on: the gate reads the platforms from the
`runs-on` of every job under `.github/workflows`, maps each runner label
through its `RUNNER_PLATFORMS` table, and fails a file no such platform
and no choice of the legs' tags compiles, so a Linux-only test passes and
a Windows-only one is a finding. The
second ruling is that a backend which cannot be reached fails a case
rather than skipping it, and `go test` exits 0 on a binary whose cases
all skipped: a non-test Go file that imports `testing` and calls a skip
method is a finding, since that is where a fixture's skip has to live,
while a `_test.go` file is left alone because a case may skip for a
reason of its own. Tags are read from the head of a file, before its
package clause, which is where Go reads them — a comment quoting one
below the clause is prose. Exit codes: 0 clean, 1 a finding (each with
file, line and what it is), 2 infrastructure error (the root is not a
directory, the Makefile passes no tag, a workflow names a runner label
whose platform the gate does not know, a constraint does not parse, or a
check found no subject at all — the integration layer gone, or no library
to read).

`check_test_limits.py` — holds the test-side half of the load policy: a
test does not choose its width from the machine, and a declared case or
wait budget does not undercut the shared default. In test files and test
configuration only, it fails a read of the host's core count:
`runtime.NumCPU()` under any `runtime` import alias; `runtime.GOMAXPROCS`
with `0`, a negative argument or a variable argument of unknown origin (a
positive literal and a save/restore pair are not probes — the saved value
may be read back from a `0`, negative or positive-literal call, and is not
reported when it is used only to restore the width; the pairing is per
top-level function body, so two saves of one name inside the same function
are judged together and a same-named save in a nested block that escapes
also reports the outer save even when it is restored cleanly, while naming
the two saves differently or moving one to another function keeps them
apart; a range variable iterating a table the test wrote is that test's own
choice); and in JavaScript `os.cpus()` and
`os.availableParallelism()` under the module name or a default, namespace
or named-import alias, a destructured `require('os')` alias, an inline
`require('os').cpus()`, a member read off an awaited `import('node:os')` or
its destructured form, `navigator.hardwareConcurrency`, and a
`maxWorkers`/`minWorkers`/`workers` percentage string. The width is the
caller's `GATE_JOBS`, because a process inside a container sees the node's
cores rather than the cores it was given.

It also fails a per-case, per-hook or per-suite time limit below the shared
default. A case budget of `0` means no limit in both Vitest and Playwright
and passes; a wait timeout of `0` does not. `waitForCondition`, the
project's own wrapper in `web/test-utils/src/wait.ts`, is the one exempt
wait: it passes the larger of the caller's `timeout` and the configured
`asyncUtilTimeout`, so a caller's `0` or `500` cannot lower the budget and
there is no limit to check. The gate asserts that premise on every run —
`waitForCondition`'s body must still compute a `Math.max` over both values —
and exits 2, rather than passing the call silently, when the wrapper no
longer does or the file is missing. The Testing Library `findBy*` and
`waitFor` timeouts and `vi.waitFor` stay under the rule: their `0` and any
value below the shared async default are findings. The Vitest forms read are
an options object after the case name, a trailing budget or options object
(`it(name, fn, 5000)`, `it(name, fn, { timeout })`), the call returned by
`it.each`/`it.for`/`it.skipIf`/`it.runIf`, both the table form
(`it.each([[1]])`) and the tagged template form (`` it.each`...` ``),
`test.setTimeout` and `vi.setConfig`; `testTimeout`/`hookTimeout` in a
config; and a
`findBy*`/`waitFor` wait timeout or a Testing Library
`configure({ asyncUtilTimeout })` against the shared async default. A
numeric budget is a literal or a constant arithmetic expression
(`5 * 1000`), optionally wrapped in the one recognised scaling call
(`scaledTimeout(5_000)`); only that name is peeled, so a table
(`it.each(range(5))`) is not read as its first argument. Playwright's
`test.setTimeout`, `testInfo.setTimeout`, `test.info().setTimeout` and
`test.describe.configure({ timeout })` are read against the Playwright
config's top-level `timeout`, found by its brace depth: a `timeout` inside
`use`, `expect` or a project sits deeper and is skipped. Assertion, element
and retry waits that are not Testing Library queries are not case limits and
are left alone, and Go has no declared case budget to read.

The shared defaults are read from the tree: `SHARED_TIMEOUT_MS` in
`web/vitest.shared.ts`, `ASYNC_UTIL_TIMEOUT_MS` in
`web/test-utils/src/gate-time.ts`, and the Playwright config's top-level
`timeout`. Their floors live in `web/scripts/test-time-floors.json`, a file
that holds only floors — `vitestCaseTimeoutMs`, `playwrightCaseTimeoutMs`
and `asyncUtilTimeoutMs` — so a lowered default cannot lower its own floor
in the same edit (the workspace guard reads the same file). Each default
must clear its own floor; a lowered default would make every per-case limit
below it pass. The file is the one entry point for an intentional floor
change: a change to it carries its reason in the commit message. A floors
file that is missing, unreadable (invalid JSON), or lacks a positive finite
value for any key is an infrastructure error, never a silent pass.

Scope is Go `_test.go` files; any non-test Go file whose import block names
`"testing"` (the test-support packages: dbsuite, bussuite, cutproxy,
routecontract, routefixture and the like); any Go file in a package or
directory whose name ends in `test` (the Go convention for a helper
package: coretest, provisioningtest, authntest and the like); JavaScript
and TypeScript test files; Vitest/Playwright configuration; and the
`test-utils` and `e2e` trees. In the `e2e` tree, specs and their support
files (fixtures, helpers, global setup) are all read for Playwright case
budgets, because a helper that lowered a bound would narrow every case that
used it; only the Vitest-shaped `*.test.*` files there take the Vitest
budget. Product runtime code matches none of the Go shapes and is out of
scope.

The read is a text heuristic with comments, string contents and regex
bodies masked, so a token in prose is not a finding; an inline or dynamic
`os` import is matched on a copy that keeps its module specifier, then kept
only when its keyword survives the mask, so a `require('os')` named inside a
string stays prose. The TS/JS mask is hand-written, not a parser. It reads
JSX with a state machine over the whole source: `<name` at an expression
position -- after `(`, `=`, `?`, `&&`, `=>`, a keyword such as `return`,
`await`, `case` or `yield`, or at the start of a line -- opens a tag, `/>`
closes that tag without opening an element, `>` opens the element's text
when a matching `</name>` follows, `{` in text enters code until the
matching `}`, and `</name>` returns to the parent's text. Inside text
`//`, quotes and `/` are literal, so `<p>http://x</p>`
keeps its URL on any line and after a nested child; "`//` in JSX text is
markup, not a comment" holds. An apostrophe directly after an identifier is
still not a string opener (`<p>it's</p>`), while a keyword that takes an
expression with no space (`x as'user'`) keeps the string reading. A `<` that
ends an operand on the same line is a comparison, not a tag start, so
`s</re/.test(t)` is `s < /re/`, while `render(<Link>a</Link>)` still closes
the element. A `/` after a regex preceder such as `(` still opens a regex,
so `.replace(/>/g, ...)` masks its regex as a unit. A type-level `<T>` is not
an element when no `</T>` follows, so a generic arrow or a type assertion
does not turn the following code into text. A delimiter can still be mispaired
in code or a tag, and then the mask hides text up to the next closing
delimiter, which may be on a later line: a quote inside a string or
attribute pairs with the next quote, and a `/` after `throw` opens a regex
the preceder rule does not recognise, so a quote inside or after it pairs
with the next quote. Fail-closed guarantees only that such a misread cannot
swallow to end of file: a string, template literal, regex or block comment
still open there makes the gate exit 2 naming the file, so it surfaces
rather than passing silently. The frontend's guards do not replace this
scan: the structural guard in `web/scripts/workspace-test-config.test.mjs`
parses every test source with the TypeScript compiler and walks the syntax
tree, so it is not
a second mask; it covers the workspace's test wiring and the shared floors.
The custom rules under `web/eslint-rules/` are a separate set, walking the
TypeScript syntax tree for their own subjects (i18n text, direct HTTP, HTML
sinks, e2e bypass, feedback regions). The Python gate is the only
whole-repository reader that does not need Node, so the budget and
core-count checks stay here. Its known blind spots are a reviewer's
obligation, not the gate's, and a clean run does not stand for them: a dot
import of
`runtime` (`import . "runtime"`) makes `NumCPU()` unqualified; a named
constant passed as a budget (`const ms = 5_000; it('x', { timeout: ms })`);
a budget built from a variable or from a longer expression than constant
arithmetic; a width returned by a helper; a `findBy*`/`waitFor` timeout
passed through a variable; an `os` member reached through a binding the
scan does not model (`const m = require('os'); m['cpus']()`); and a
function that saves one name twice, which the per-body pairing judges
together, so a clean save can be reported because its same-named sibling in
a nested block escaped. A JSX element after a keyword the
expression-position set does not carry (`export default <p>`,
`throw <p>`) is read as code, so a `//` in its text starts a comment
and the rest of the line is hidden. A type-level `<T>` is read as an
element when a `</T>` appears later in prose. That misread opens a text
state, where nothing is blanked, so a name or budget between the two tags
is read as code and reported: the cost is a false finding, not a missed
one. When the text state swallows an opening quote, the closing quote lands
in code as an unterminated string, and the gate exits 2 naming the file.
Exit codes: 0 clean, 1 a finding (file, line and
what it is), 2 infrastructure error (the root is not a directory, nothing
test-shaped was found, a shared default or one of its floors is missing,
unreadable or not a positive finite number,
`web/test-utils/src/wait.ts` is missing, unreadable or no longer takes the
larger of the caller's timeout and the shared async budget, or a TS/JS test
source cannot be parsed because the mask reaches end of file inside a
string, template, regex or block comment).

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

`check_pnpm_filters.py` — holds every pnpm command that filters to the rule
that a filter matching nothing must fail. When every `--filter` selector
misses, pnpm prints "No projects matched the filters" and exits 0, so a
renamed package turns a build or a test run into a no-op under a green leg.
`--fail-if-no-match` makes the same command exit 1, but pnpm reads it only
before the command word, and only when all selectors miss. The gate reads the
Makefile recipes, the Dockerfile `RUN` instructions (continuations joined,
`&&` and `;` split), the `scripts` of every `package.json` and the `run:`
blocks of the workflows and composite actions, and fails a call that filters
without the flag, one with the flag after the command word, and one whose
selector names no member of `web/pnpm-workspace.yaml` (a name, a glob that
matches a member, or a path selector, with the `...` and `^` markers and a
leading `!` removed). Exit codes: 0 clean, 1 a finding (each with file, line
and the call), 2 infrastructure error (the root is not a directory, or the
workspace file is missing or lists no member).

`check_web_build_scripts.py` — holds the workspace members' build scripts to
the shape the frontend gate calls. The gate builds the packages with
`pnpm -r run build:self`, and pnpm silently skips a member whose manifest has
no `build:self` script, so the leg stays green while that package's dist is
never written. The gate reads the members `web/pnpm-workspace.yaml` lists and,
for every one except the two declared exceptions (`@speed/test-utils`, whose
build is its typecheck, and `demo-web`, which builds through its own script),
requires `build:self` to be exactly `tsc -p tsconfig.build.json` and `build`
to be exactly `pnpm --filter <own name>... --fail-if-no-match
--workspace-concurrency=1 run build:self`: the package and its dependency
graph, one script at a time, and a filter that fails rather than matching
nothing. The filter is checked against the package's own name, so a build
copied from a sibling is a finding. Exit codes: 0 clean, 1 a finding (each
with file, line, package name and what is wrong), 2 infrastructure error (the
root is not a directory, the workspace file is missing or lists no readable
member, or a member manifest cannot be read or parsed).

`check_ci_jobs.py` — holds the `check` workflow to the commit gate, leg for
leg. `make check` names one segment per leg, and each segment's recipe calls
the leg through `$(MAKE)`; `.github/workflows/ci.yml` runs the same legs
split across parallel jobs. The gate reads the legs from the `check-*`
prerequisites and the targets of every `make` command in the workflow, and
holds the two sets equal, so a segment added to `check` without a job in the
workflow fails here rather than widening the local gate beyond what CI runs.
A leg narrowed with `MODULES=` runs part of that leg's module loop: the union
of its `MODULES=` values across the jobs must cover exactly the modules
`go.work` lists, and a job running the leg without `MODULES=` covers the
whole roster. A job calling `make check` itself is a finding, not full
coverage, because the union of the jobs' targets must be the legs, not the
target that runs them. Coverage alone is not enough: the run must fail when
a leg fails, so the gate also requires the workflow-level `env:` to set
`GATE_JOBS` and a `GOFLAGS` containing `-count=1`, and refuses a job or a
step whose `run:` invokes a gate leg while setting `continue-on-error`
(anything but `false`) or an `if:` other than `always()` that could skip it.
Its suite,
`test_check_ci_jobs.py`, drives the gate over scratch trees whose Makefile,
workflow and `go.work` it writes. Exit codes: 0 clean, 1 a finding (a leg no
job runs, a target that is no segment, a module no job covers, the workflow
env missing `GATE_JOBS` or `-count=1`, or a gate job that could let a failure
pass), 2 infrastructure error (the root is not a directory, or the Makefile,
the workflow or `go.work` is missing or names nothing to compare).
