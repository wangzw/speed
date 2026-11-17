#!/usr/bin/env python3
"""Test-limit gate: a test does not choose its width, nor shorten its clock.

Two rulings of the gate's load policy live here, each enforced only where a
test decides it.

Core count. A test or a test configuration must not derive a concurrency
from the machine it happens to run on. In Go, `runtime.NumCPU()` (under any
import alias), `runtime.GOMAXPROCS(0)` or a negative argument taken as a
default, and a `GOMAXPROCS` call whose argument is a variable of unknown
origin are findings. Setting a fixed width is not a finding, and neither is
saving the process width in order to restore it: a `GOMAXPROCS` call whose
return value is read into a name that is used nowhere but a restore call
passes, whether that call's argument is `0`, negative or a positive literal.
That pairing is per top-level function body, not per block: two saves of one
name inside the same function are judged together, so a same-named save in a
nested block that escapes also reports the outer save of that name even when
it is restored cleanly. Naming one of the two saves differently keeps their
judgements apart.
In JavaScript, `os.cpus()`, `os.availableParallelism()`, their destructured
or aliased bindings, an inline `require('os').cpus()`, a member read off an
awaited `import('node:os')` or its destructured form, `navigator.hardwareConcurrency`,
and a `maxWorkers`/`minWorkers`/`workers` percentage string are findings,
because a percentage is resolved against the host's cores. The width is the
caller's: the gate passes `GATE_JOBS` to every tool that takes one, and a
process inside a container sees the node's cores rather than the cores it
was given, so a probe chooses the wrong number.

Case limits. A declared per-case, per-hook or per-suite time limit must not
sit below the shared default. Zero is the exception both Vitest and
Playwright give it: a case budget of `0` means no limit and passes. Read
forms are Vitest cases (`it`/`test`/`describe`, hooks, `it.each` as a table
or as a tagged template, `it.for`, `it.skipIf`/`it.runIf` chains, an options
object after the body), `test.setTimeout`, `vi.setConfig`; Playwright's
`test.setTimeout`, `testInfo.setTimeout`, `test.info().setTimeout` and
`test.describe.configure({ timeout })`, read against that config's own
`timeout`; the timeout option of a `findBy*`/`waitFor` query and of a
Testing Library `configure({ asyncUtilTimeout })`, read against the shared
async default; and `testTimeout`/`hookTimeout` in a config. A numeric budget
is a literal or a constant arithmetic expression (`5 * 1000`), optionally
wrapped in the one recognised scaling call (`scaledTimeout(5_000)`); only
that name is peeled, so a table or an unrelated call is not read as its
first argument. Assertion and element waits that are not Testing Library
queries are not case limits and are left alone; Go has no declared case
budget, so its waits are not this gate's subject. The shared defaults are
read from the tree -- `SHARED_TIMEOUT_MS` in `web/vitest.shared.ts`,
`ASYNC_UTIL_TIMEOUT_MS` in `web/test-utils/src/gate-time.ts`, and the
top-level `timeout` of the Playwright config. Their floors live apart from
the defaults, in `web/scripts/test-time-floors.json`
(`vitestCaseTimeoutMs`, `playwrightCaseTimeoutMs`, `asyncUtilTimeoutMs`),
which is the one entry point for an intentional floor change; the workspace
guard reads the same file. Each shared default must clear its own floor,
because a lowered default would make every per-case limit below it pass. A
wait timeout of `0` gets no exception: a wait that returns at once buys
nothing and is still below the shared async default, unlike a case budget,
where `0` means no limit.

`waitForCondition`, the project wrapper in `web/test-utils/src/wait.ts`, is
exempt from the async wait rule. It passes the larger of the caller's
`timeout` and the configured `asyncUtilTimeout`, so a caller's `0` or `500`
cannot lower the budget and there is no limit to check. The exemption rests
on that wrapper still taking the larger value, so every run asserts the
premise (`wait_condition_takes_larger_timeout`) and exits 2, rather than
passing the call silently, when the wrapper no longer computes it or the
file cannot be read. The Testing Library `findBy*`/`waitFor` timeouts and
`vi.waitFor` are not exempt: their `0` and any value below the shared async
default are findings.

Scope: Go files that are `_test.go`, that import `"testing"`, or that belong
to a package or directory whose name ends in `test` (the Go convention for a
test-helper package, such as `coretest` or `provisioningtest`); JavaScript
and TypeScript test files (`*.test.*`, `*.spec.*`), test configuration
(`vitest`/`playwright`/`jest`/`cypress` configs, `vitest.shared.*`), and the
`test-utils` and `e2e` trees -- in the `e2e` tree, specs and their support
files (fixtures, helpers, global setup) are all read for Playwright case
budgets, and only Vitest-shaped `*.test.*` files there take the Vitest
budget.

TS/JS sources are read through a hand-written mask, not a parser.
`mask_javascript` blanks comments, string contents, template literals and
regex bodies while keeping their delimiters. JSX is recognised to the
extent a token scan can: `</` before a tag name or `>`, and a `/>` that
follows an attribute-ending `}`, identifier or quote, are markup rather
than a regex start, and an apostrophe directly after an identifier is JSX
text (`<p>it's</p>`), not a string opener. A `/` after a regex preceder
such as `(` still opens a regex, so `.replace(/>/g, ...)` masks its regex
as a unit.

A mispaired delimiter still hides text, up to the next closing delimiter,
which may be on a later line: a quote at the start of JSX text
(`<p>'quoted'</p>`) pairs with the next quote; a `/*` written in JSX text
pairs with a `*/` on a later line; and a `/` after `throw` opens a regex
the preceder rule does not recognise, so a quote inside or after it pairs
with the next quote. Fail-closed only guarantees that such a misread cannot
swallow to end of file: a string, template literal, regex or block comment
still open there is an infrastructure error naming the file.

The frontend's guards do not replace this scan.
`web/scripts/workspace-test-config.test.mjs` (run by `make web-check`,
which needs Node) holds the workspace's test wiring and shared floors with
a parse of every test source by the TypeScript compiler, not a mask. The
custom rules under `web/eslint-rules/` are a separate set: they walk the
TypeScript syntax tree for the subjects they own (i18n text, direct HTTP,
HTML sinks, e2e bypass, feedback regions). The Python gate is the
whole-repository reader that runs without Node, so the budget and
core-count checks stay here.

A text scan cannot catch every shape. These are the gate's known blind spots
and are a reviewer's obligation, not the gate's: a dot import of `runtime`
(`import . "runtime"`) makes `NumCPU()` unqualified and invisible; a named
constant passed as a budget (`const ms = 5_000; it('x', { timeout: ms })`)
is not a literal; a budget built from a variable or from a longer expression
than constant arithmetic; a width returned by a helper; a `findBy*`/`waitFor`
timeout passed through a variable; an `os` member reached through a binding
the scan does not model (`const m = require('os'); m['cpus']()`); and a
function that saves one name twice, which the per-body pairing judges
together, so a clean save can be reported because its same-named sibling in
a nested block escaped -- naming the two saves differently, or moving one to
another function, keeps them apart. Product runtime code is out of scope: a
Go file outside the three shapes above is not read.

Exit codes: 0 = clean; 1 = at least one finding, each reported as
file:line with what it is; 2 = infrastructure error -- the root is not a
directory, no test file or configuration was found, a shared default or
its floor (needed to judge a case limit) is missing, unreadable or not a
positive finite number, the `waitForCondition` wrapper whose exemption
the async wait rule relies on is missing, unreadable or no longer takes the
larger of the caller's value and the shared async budget, or a TS/JS test
source cannot be parsed because the mask reaches end of file inside a
string, template literal, regex or block comment. Paths in the
report are relative to --root.
"""

from __future__ import annotations

import argparse
import ast
import json
import math
import os
import re
import sys

SKIPPED_DIRS = {".git", "node_modules", "dist", ".cache", "__pycache__", "vendor"}

# The floors the shared defaults must clear, before any caller's scale. They
# live in their own file -- one that holds only floors -- rather than beside
# the defaults, so a lowered default cannot lower its own floor in the same
# edit. The workspace guard reads the same file. This file is the one entry
# point for an intentional floor change: a change to it carries its reason in
# the commit message, because a floor that moves without one is
# indistinguishable from a default lowered along with it.
TIME_FLOORS_FILE = os.path.join("web", "scripts", "test-time-floors.json")
VITEST_FLOOR_KEY = "vitestCaseTimeoutMs"
PLAYWRIGHT_FLOOR_KEY = "playwrightCaseTimeoutMs"
ASYNC_FLOOR_KEY = "asyncUtilTimeoutMs"

GO_TEST_FILE = re.compile(r"_test\.go$")
GO_PACKAGE_NAME = re.compile(r"^\s*package\s+([A-Za-z_]\w*)", re.MULTILINE)
JS_TEST_FILE = re.compile(r"\.(?:test|spec)\.[cm]?[jt]sx?$")
JS_TEST_CONFIG = re.compile(
    r"(?:^|/)(?:vitest|playwright|jest|karma|cypress)(?:\.[^.]+)?\.config\.[cm]?[jt]s$"
)
JS_SHARED_CONFIG = re.compile(r"(?:^|/)vitest\.shared\.[cm]?[jt]s$")

# A JavaScript numeric budget is a literal or a constant arithmetic
# expression, optionally wrapped in one recognised scaling call. Only a named
# scaling function is peeled: peeling any call would read `it.each(range(5))`
# as a budget of 5 and `asyncBudgetMs(2.5)` as a budget of 2.
ARITHMETIC_CHARS = re.compile(r"[0-9\s()+\-*/]+")
NUMERIC_TOKEN = re.compile(r"\d[\d_]*")
WRAPPER_CALL = re.compile(
    r"^\s*([A-Za-z_$][\w$]*)\s*\((.*)\)\s*$", re.DOTALL
)
SCALING_WRAPPERS = {"scaledTimeout"}

# The JavaScript core-count probes that survive an aliased import. The
# module aliases (`import os from 'node:os'`, `const os = require('os')`)
# and the named aliases (`import { cpus as c }`, `const { cpus: c } = ...`)
# are resolved per file, then these names are matched.
NAMED_OS_MEMBERS = ("cpus", "availableParallelism")

OS_IMPORT_NAMED = re.compile(
    r"\bimport\s+(?:[A-Za-z_$][\w$]*\s*,\s*)?\{([^}]*)\}\s+from\s+"
    r"['\"](?:node:)?os['\"]"
)
OS_IMPORT_DEFAULT = re.compile(
    r"\bimport\s+([A-Za-z_$][\w$]*)\s*(?:,\s*\{[^}]*\})?\s+from\s+"
    r"['\"](?:node:)?os['\"]"
)
OS_IMPORT_NAMESPACE = re.compile(
    r"\bimport\s+\*\s+as\s+([A-Za-z_$][\w$]*)\s+from\s+"
    r"['\"](?:node:)?os['\"]"
)
OS_REQUIRE_DESTRUCTURE = re.compile(
    r"\b(?:const|let|var)\s*\{([^}]*)\}\s*=\s*require\(\s*"
    r"['\"](?:node:)?os['\"]\s*\)"
)
OS_REQUIRE = re.compile(
    r"\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*require\(\s*"
    r"['\"](?:node:)?os['\"]\s*\)"
)

# Inline and dynamic reads the binding scan cannot name: `require('os').cpus()`
# binds no module alias, and neither `(await import('node:os')).cpus()` nor
# `const { cpus } = await import('node:os')` survives the mask, which blanks
# the module specifier. Each is matched on a copy that keeps the string and
# then confirmed against the masked copy: the keyword (`require`/`import`)
# survives masking only in code, never inside a string or template literal.
INLINE_REQUIRE_OS_MEMBER = re.compile(
    r"(?P<kw>\brequire)\s*\(\s*['\"](?:node:)?os['\"]\s*\)\s*\.\s*"
    r"(?P<member>cpus|availableParallelism)\s*\("
)
DYNAMIC_IMPORT_OS_MEMBER = re.compile(
    r"(?P<kw>\bimport)\s*\(\s*['\"](?:node:)?os['\"]\s*\)\s*\)?\s*\??\.\s*"
    r"(?P<member>cpus|availableParallelism)\s*\("
)
DYNAMIC_IMPORT_OS_DESTRUCTURE = re.compile(
    r"\{\s*(?P<spec>[^{}]*)\}\s*=\s*await\s+(?P<kw>\bimport)\s*\(\s*"
    r"['\"](?:node:)?os['\"]\s*\)"
)
DYNAMIC_IMPORT_OS_THEN = re.compile(
    r"(?P<kw>\bimport)\s*\(\s*['\"](?:node:)?os['\"]\s*\)\s*\.\s*then\s*"
    r"\(\s*\(\s*\{(?P<spec>[^{}]*)\}"
)

# A concurrency given as a percentage of the host's cores. Vitest resolves
# `maxWorkers`/`minWorkers` and Playwright resolves `workers` against the
# core count, so the string is a probe even though it names no function. The
# property name is matched on the masked source and the value on the
# original, because masking blanks the string that holds the percentage.
WIDTH_PERCENT_PROPERTY = re.compile(
    r"\b(maxWorkers|minWorkers|workers)\s*:\s*(['\"`])"
)
PERCENT_VALUE = re.compile(r"\d+(?:\.\d+)?\s*%")

# Case-call heads. The chain between the head and the opening parenthesis
# may name modifiers (`it.each`, `it.skipIf`, `test.concurrent`), and the
# ones that return a callable (`each`, `for`, `skipIf`, `runIf`) are then
# followed by a second call that carries the budget.
CASE_CHAIN = re.compile(
    r"\b(it|test|describe|beforeEach|afterEach|beforeAll|afterAll)"
    r"((?:\s*\.\s*[A-Za-z_$][\w$]*)*)\s*\("
)
CHAIN_MODIFIER = re.compile(r"\.\s*([A-Za-z_$][\w$]*)")
CHAIN_RETURNS_CALL = {"each", "for", "skipIf", "runIf"}
CASE_SKIPPED_CHAINS = (["setTimeout"], ["describe", "configure"])

OPTION_TIMEOUT = re.compile(r"\btimeout\s*:\s*([^,}]+)")
CONFIG_BUDGET = re.compile(r"\b(?:testTimeout|hookTimeout)\s*:\s*([^,\n}]+)")
SET_TIMEOUT_CALL = re.compile(
    r"\b(?:test|testInfo)\s*\.\s*setTimeout\s*\("
    r"|\btest\s*\.\s*info\s*\(\s*\)\s*\.\s*setTimeout\s*\("
)
SET_CONFIG_CALL = re.compile(r"\bvi\s*\.\s*setConfig\s*\(")
PLAYWRIGHT_CONFIGURE = re.compile(
    r"\btest\s*\.\s*describe\s*\.\s*configure\s*\("
)
# Testing Library's `configure({ asyncUtilTimeout })` in a test or setup
# lowers the same budget the setups widen; its numeric value is the async
# budget. Other `configure` keys (reactStrictMode and the like) are not.
ASYNC_CONFIGURE_CALL = re.compile(r"\bconfigure\s*\(\s*\{")
ASYNC_CONFIG_TIMEOUT = re.compile(r"\basyncUtilTimeout\s*:\s*([^,}]+)")
# The tagged-template `it.each` form. Masking keeps the backticks and blanks
# the template body, so the head is recognisable and the call it returns is
# read the same way as the table form's.
EACH_TAG_TEMPLATE = re.compile(
    r"\b(it|test|describe)\s*\.\s*each\s*`"
)

# Testing Library waits. A `findBy*`/`findAllBy*` query and `waitFor` carry
# a waitForOptions object whose `timeout` is the shared async budget.
# `waitForCondition` is the project's own wrapper in web/test-utils/src/wait.ts:
# it passes `Math.max(options.timeout ?? 0, asyncUtilTimeout)`, so a caller's
# value cannot lower the budget and its timeout is exempt. The exemption is a
# consequence of the name: `waitFor` matched only when a parenthesis follows,
# and `waitForCondition` continues with a word character there, so this rule
# never reads it. The gate asserts the premise on every run
# (`wait_condition_takes_larger_timeout`).
ASYNC_WAIT_CALL = re.compile(
    r"\b(?:findBy|findAllBy)\w+\s*\(|\bwaitFor\s*\("
    r"|\bwaitForElementToBeRemoved\s*\("
)

# The wrapper whose exemption the async wait rule depends on.
WAIT_CONDITION_FILE = os.path.join("web", "test-utils", "src", "wait.ts")
WAIT_CONDITION_NAME = "waitForCondition"
MAX_CALL = re.compile(r"\bMath\s*\.\s*max\s*\(")
CALLER_TIMEOUT = re.compile(r"\boptions\s*\.\s*timeout\b")
SHARED_ASYNC_BUDGET = re.compile(r"\basyncUtilTimeout\b")

VITEST_SHARED_DEFAULT = re.compile(r"\bSHARED_TIMEOUT_MS\s*=\s*([\d_]+)")
ASYNC_SHARED_DEFAULT = re.compile(r"\bASYNC_UTIL_TIMEOUT_MS\s*=\s*([\d_]+)")

# Go imports, read from a comment-masked copy so a quoted path in prose is
# not an import. An alias is captured when one is written.
GO_IMPORT_SINGLE = re.compile(
    r'\bimport\s+(?:(\.|[A-Za-z_]\w*)\s+)?"([^"]+)"'
)
GO_IMPORT_SPEC = re.compile(
    r'^\s*(?:(\.|[A-Za-z_]\w*)\s+)?"([^"]+)"\s*$', re.MULTILINE
)
GO_IMPORT_BLOCK = re.compile(r"\bimport\s*\((.*?)\)", re.DOTALL)

# A range variable iterates a collection the test wrote (a table of widths),
# so a `GOMAXPROCS` argument that is one is a width the test chose.
GO_RANGE_VARS = re.compile(r"\bfor\s+([^;{\n]+?)\s*:?=\s*range\b")


def is_go_test_file(relative: str) -> bool:
    return GO_TEST_FILE.search(os.path.basename(relative)) is not None


def is_test_file(relative: str) -> bool:
    """A JavaScript/TypeScript test file or configuration, by path.

    A Go file is decided separately, by `_test.go`, an import of
    `"testing"`, or a package or directory whose name ends in `test` (see
    `go_is_test_scope`); a JavaScript test file is decided by its path.
    """
    name = os.path.basename(relative)
    if relative.endswith(".go"):
        return is_go_test_file(relative)
    if JS_TEST_FILE.search(name) or JS_TEST_CONFIG.search(relative):
        return True
    if JS_SHARED_CONFIG.search(relative):
        return True
    parts = relative.split(os.sep)
    if any(part in ("test-utils", "testsupport") for part in parts[:-1]):
        return name.endswith((".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"))
    if "e2e" in parts[:-1]:
        return name.endswith((".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"))
    return False


JS_SPEC_FILE = re.compile(r"\.spec\.[cm]?[jt]sx?$")


def is_javascript(relative: str) -> bool:
    return relative.endswith((".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"))


def is_playwright_spec(relative: str) -> bool:
    """A Playwright case: an `e2e` tree's `*.spec.*`."""
    parts = relative.split(os.sep)
    return "e2e" in parts[:-1] and JS_SPEC_FILE.search(os.path.basename(relative)) is not None


def is_e2e_support(relative: str) -> bool:
    """A Playwright config, fixture or helper: no case budget of its own."""
    parts = relative.split(os.sep)
    return (
        "e2e" in parts[:-1]
        and JS_SPEC_FILE.search(os.path.basename(relative)) is None
        and JS_TEST_FILE.search(os.path.basename(relative)) is None
    )


def is_vitest_scope(relative: str) -> bool:
    """A file whose case budgets are Vitest's: not a Playwright spec or helper.

    The `e2e` tree's own `*.test.*` fixtures are Vitest suites and stay in
    scope; its specs and non-test support are not.
    """
    if not is_javascript(relative):
        return False
    return not is_playwright_spec(relative) and not is_e2e_support(relative)


def strip_comments(text: str) -> str:
    """C-family comments blanked, string literals kept.

    Import paths are string literals, so they survive; a quoted `import` in a
    comment does not.
    """
    chars = list(text)
    index, size = 0, len(text)
    while index < size:
        if text.startswith("//", index):
            end = text.find("\n", index)
            end = size if end == -1 else end
            for at in range(index, end):
                chars[at] = " "
            index = end
        elif text.startswith("/*", index):
            end = text.find("*/", index + 2)
            end = size if end == -1 else end + 2
            for at in range(index, end):
                if chars[at] != "\n":
                    chars[at] = " "
            index = end
        elif text[index] in ('"', "'", "`"):
            quote = text[index]
            index += 1
            while index < size and text[index] != quote:
                index += 2 if text[index] == "\\" else 1
            index += 1
        else:
            index += 1
    return "".join(chars)


def go_imports(text: str) -> list[tuple[str, str | None]]:
    """The (path, alias) of every import, alias None when none is written.

    A dot import has the alias `.`. Both the single-line and the parenthesized
    forms are read.
    """
    source = strip_comments(text)
    found: list[tuple[str, str | None]] = []
    for block in GO_IMPORT_BLOCK.finditer(source):
        for alias, path in GO_IMPORT_SPEC.findall(block.group(1)):
            found.append((path, alias or None))
    for alias, path in GO_IMPORT_SINGLE.findall(source):
        found.append((path, alias or None))
    return found


def imports_go_testing(text: str) -> bool:
    return any(path == "testing" for path, _ in go_imports(text))


def go_is_test_scope(relative: str, text: str) -> bool:
    """Whether a Go file is test-side code the gate reads.

    Three shapes count: a `_test.go` file; any file importing `"testing"`
    (the test-support packages, such as dbsuite or bussuite); and a file in
    a package or directory whose name ends in `test`, the Go convention for
    a helper package (coretest, provisioningtest). Product runtime code
    matches none of them.
    """
    if is_go_test_file(relative):
        return True
    if imports_go_testing(text):
        return True
    package = GO_PACKAGE_NAME.search(strip_comments(text))
    if package is not None and package.group(1).endswith("test"):
        return True
    directory = os.path.basename(os.path.dirname(relative))
    return directory.endswith("test")


def go_runtime_names(text: str) -> set[str]:
    """The names under which this file binds the `runtime` package.

    A dot import binds its exported names unqualified, which a qualifier scan
    cannot follow; it is a documented blind spot, so the default name is
    returned and the dot-imported call is left to review.
    """
    names: set[str] = set()
    for path, alias in go_imports(text):
        if path != "runtime":
            continue
        if alias is None or alias == ".":
            names.add("runtime")
        else:
            names.add(alias)
    return names or {"runtime"}


def js_os_bindings(text: str) -> tuple[set[str], dict[str, str]]:
    """The module aliases and the member aliases of `os` in a JS source.

    A module alias is a name holding the `os` namespace (`import os from
    'node:os'`, `import * as o`, `const o = require('os')`); a member alias
    maps a local name to the `cpus`/`availableParallelism` member it names.
    """
    modules = {"os"}
    members: dict[str, str] = {}
    for name in OS_IMPORT_DEFAULT.findall(text) + OS_IMPORT_NAMESPACE.findall(
        text
    ) + OS_REQUIRE.findall(text):
        modules.add(name)
    for spec in OS_IMPORT_NAMED.findall(text) + OS_REQUIRE_DESTRUCTURE.findall(
        text
    ):
        for entry in spec.split(","):
            entry = entry.strip()
            if not entry:
                continue
            parts = re.split(r"\s+as\s+|\s*:\s*", entry)
            source_name = parts[0].strip()
            local_name = parts[-1].strip()
            if source_name in NAMED_OS_MEMBERS:
                members[local_name] = source_name
    return modules, members


def js_core_patterns(text: str) -> list[tuple[str, re.Pattern[str]]]:
    """The core-count probes in one JS source, aliases resolved per file."""
    modules, members = js_os_bindings(text)
    patterns: list[tuple[str, re.Pattern[str]]] = []
    for module in sorted(modules):
        for member in NAMED_OS_MEMBERS:
            patterns.append(
                (
                    f"os.{member}",
                    re.compile(
                        rf"\b{re.escape(module)}\s*\.\s*{member}\s*\("
                    ),
                )
            )
    for local, member in sorted(members.items()):
        patterns.append(
            (
                f"os.{member} (imported as {local})",
                re.compile(rf"\b{re.escape(local)}\s*\("),
            )
        )
    return patterns


def arithmetic_ms(literal: str) -> int | None:
    """A constant millisecond expression, evaluated or None.

    Only digits with the four arithmetic operators and parentheses are
    accepted, so `5_000`, `5*1000` and `2*60_000` are read and anything that
    names a value is not. The AST is checked node by node before evaluation,
    so no call, name or attribute can run.
    """
    cleaned = literal.replace("_", "").strip()
    if not cleaned or not ARITHMETIC_CHARS.fullmatch(cleaned):
        return None
    if not NUMERIC_TOKEN.search(cleaned):
        return None
    try:
        tree = ast.parse(cleaned, mode="eval")
    except SyntaxError:
        return None
    for node in ast.walk(tree):
        if isinstance(node, (ast.Expression, ast.BinOp, ast.UnaryOp, ast.Constant)):
            if isinstance(node, ast.BinOp) and not isinstance(
                node.op, (ast.Add, ast.Sub, ast.Mult, ast.Div)
            ):
                return None
            if isinstance(node, ast.UnaryOp) and not isinstance(
                node.op, (ast.USub, ast.UAdd)
            ):
                return None
            if isinstance(node, ast.Constant) and not isinstance(
                node.value, (int, float)
            ):
                return None
        elif not isinstance(
            node, (ast.Add, ast.Sub, ast.Mult, ast.Div, ast.USub, ast.UAdd)
        ):
            return None
    try:
        value = eval(  # noqa: S307 - AST validated to numeric arithmetic
            compile(tree, "<case-budget>", "eval"),
            {"__builtins__": {}},
            {},
        )
    except (TypeError, ValueError, ZeroDivisionError):
        return None
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    return int(value)


def mask_go(text: str) -> str:
    """Comments and the contents of string and rune literals, blanked.

    Offsets and newlines are preserved, so a match in the masked text maps to
    the same line of the source. A token named only in a comment or quoted in
    a message is prose, not a read of the machine.
    """
    chars = list(text)
    index, size = 0, len(text)
    while index < size:
        if text.startswith("//", index):
            end = text.find("\n", index)
            end = size if end == -1 else end
            for at in range(index, end):
                chars[at] = " "
            index = end
        elif text.startswith("/*", index):
            end = text.find("*/", index + 2)
            end = size if end == -1 else end + 2
            for at in range(index, end):
                if chars[at] != "\n":
                    chars[at] = " "
            index = end
        elif text[index] in ('"', "'", "`"):
            quote = text[index]
            end = index + 1
            while end < size:
                if quote != "`" and text[end] == "\\":
                    end += 2
                    continue
                if text[end] == quote:
                    break
                end += 1
            for at in range(index + 1, min(end, size)):
                if chars[at] != "\n":
                    chars[at] = " "
            index = end + 1
        else:
            index += 1
    return "".join(chars)


def _go_body_opening(masked: str, start: int) -> int | None:
    """The index of a top-level `func`'s body brace, from `start`.

    A `{` in a signature belongs to a `struct{...}` or `interface{...}`
    type, not to the body, so those are passed over; the body brace follows
    the parameter and result lists.
    """
    index = start
    while index < len(masked):
        if masked[index] == "{":
            window = masked[max(0, index - 16) : index]
            if re.search(r"\b(?:struct|interface)\s*$", window) is None:
                return index
        index += 1
    return None


def go_top_level_scopes(masked: str) -> list[tuple[int, int]]:
    """The [start, end) spans a Go save-and-restore pair is judged within.

    A function body is one scope; the text between bodies (package clause,
    imports, package-level declarations) is another. A `prev` saved and
    restored in one test function is independent of a same-named `prev` in
    the next, so one escaping must not accuse the other. The scan works on
    the masked copy, where comments and quoted text are blanked, so braces
    inside them do not move the match. A `func` with no balanced body (an
    assembly declaration) is left to the surrounding region.
    """
    scopes: list[tuple[int, int]] = []
    cursor = 0
    for match in re.finditer(r"(?m)^[ \t]*func\b", masked):
        if match.start() < cursor:
            continue
        opening = _go_body_opening(masked, match.end())
        if opening is None:
            continue
        depth = 0
        end = opening
        closed = False
        while end < len(masked):
            char = masked[end]
            if char == "{":
                depth += 1
            elif char == "}":
                depth -= 1
                if depth == 0:
                    end += 1
                    closed = True
                    break
            end += 1
        if not closed:
            continue
        if match.start() > cursor:
            scopes.append((cursor, match.start()))
        scopes.append((match.start(), end))
        cursor = end
    if cursor < len(masked):
        scopes.append((cursor, len(masked)))
    return scopes


class UnterminatedSource(Exception):
    """The mask reached end of file inside a construct it cannot close.

    A string, template literal, regular expression or block comment that is
    never closed leaves every later line hidden from the scan. The caller
    turns this into an infrastructure error naming the file, so a misread
    can hide at most the text before the next delimiter, never the rest of
    the file.
    """

    def __init__(self, kind: str, offset: int) -> None:
        super().__init__(f"unterminated {kind}")
        self.kind = kind
        self.offset = offset


class GateError(Exception):
    """The tree cannot be judged; `main` prints the message and exits 2."""


# The characters after which a JavaScript `/` opens a regular expression
# rather than dividing, and the keywords that do the same. A regex literal
# is masked as a unit so an apostrophe inside one (`/instance's/`) does not
# open a string and flip the quote parity of the rest of the file.
REGEX_PRECEDER_CHARS = set("(,=:[!&|?{};+-*%^~<>")
REGEX_PRECEDER_WORDS = {
    "return",
    "typeof",
    "case",
    "in",
    "of",
    "delete",
    "void",
    "new",
    "do",
    "else",
    "yield",
    "await",
}

# Keywords that can be followed by a string literal with no space between
# them (`return'x'`, `case'x':`, `from'./mod'`). Every other identifier
# right before an apostrophe is JSX text, where `it's` must not open one.
STRING_PRECEDER_KEYWORDS = REGEX_PRECEDER_WORDS | {
    "throw",
    "instanceof",
    "export",
    "default",
    "import",
    "from",
}


def _is_identifier_char(char: str) -> bool:
    return char.isalnum() or char in "_$"


def closes_condition_clause(text: str, close: int) -> bool:
    """Whether the `)` at `close` ends an `if`/`while`/`for`/`with` header.

    A regex literal is the only expression that can open there, so `/`
    after such a `)` is a regex even though a `)` before `/` is normally a
    division (`(a + b) / 2`). Without this, `if (x) /`/.test(s)` reads the
    backtick inside the regex as a template literal and hides the rest of
    the file.
    """
    depth = 0
    for index in range(close, -1, -1):
        char = text[index]
        if char == ")":
            depth += 1
        elif char == "(":
            depth -= 1
            if depth == 0:
                before = index - 1
                while before >= 0 and text[before] in " \t\r\n":
                    before -= 1
                start = before
                while start >= 0 and _is_identifier_char(text[start]):
                    start -= 1
                return text[start + 1 : before + 1] in {
                    "if",
                    "while",
                    "for",
                    "with",
                }
    return False


# The characters that can end a JSX attribute right before `/>`: the `}` of
# an expression container and an attribute value's closing quote. An
# identifier also ends an attribute (`<Br />`) and is tested separately.
JSX_TAG_END_PRECEDERS = set("}\"'")


def jsx_tag_slash(text: str, index: int) -> bool:
    """Whether the `/` at `index` is part of a JSX tag, not a regex start.

    `</` opens a closing tag (`</name>`, `</>`), so it never starts a regex.
    A `<` immediately before the slash is otherwise a comparison whose right
    operand can be a regex, as in `x</[a-z]/.test(s)`; the tag reading is
    taken only when a tag name or `>` follows the slash, so a regex that
    starts with anything else, such as `x</`/.test(s)`, is still a regex.

    `/>` closes a self-closing tag only when the last character before it
    can end an attribute: the `}` of an expression container, a closing
    quote, or an identifier (`<A p={x} />`, `<A p="x" />`, `<Br />`). After
    a regex preceder such as `(`, as in `.replace(/>/g, ...)`, the slash
    still opens a regex, so its `>` is masked with the regex instead of
    read as a tag end.
    """
    next_index = index + 1
    if next_index >= len(text):
        return False
    following = text[next_index]
    if following == ">":
        if index > 0 and text[index - 1] == "<":
            return True
        previous = index - 1
        while previous >= 0 and text[previous] in " \t\r\n":
            previous -= 1
        if previous < 0:
            return False
        char = text[previous]
        return char in JSX_TAG_END_PRECEDERS or _is_identifier_char(char)
    if index > 0 and text[index - 1] == "<":
        return following == ">" or following.isalpha() or following in "_$"
    return False


def apostrophe_starts_string(text: str, index: int) -> bool:
    """Whether the `'` at `index` opens a string rather than JSX text.

    JSX text writes contractions directly (`<p>it's</p>`), and an
    apostrophe right after an identifier character is that text, not a
    delimiter. A keyword that takes an expression can still be followed by
    a string with no space (`return'x'`), so those keep the string reading.
    """
    if index == 0 or not _is_identifier_char(text[index - 1]):
        return True
    start = index - 1
    while start >= 0 and _is_identifier_char(text[start]):
        start -= 1
    return text[start + 1 : index] in STRING_PRECEDER_KEYWORDS


def regex_can_start(text: str, index: int) -> bool:
    previous = index - 1
    while previous >= 0 and text[previous] in " \t\r\n":
        previous -= 1
    if previous < 0:
        return True
    if text[previous] in REGEX_PRECEDER_CHARS:
        return True
    if text[previous] == ")" and closes_condition_clause(text, previous):
        return True
    start = previous
    while start >= 0 and (text[start].isalnum() or text[start] in "_$"):
        start -= 1
    return text[start + 1 : previous + 1] in REGEX_PRECEDER_WORDS


def mask_javascript(text: str) -> str:
    """Comments, string contents and regex bodies blanked, delimiters kept.

    The quotes are kept so a case name is still recognisable as a string
    argument; its contents cannot name a forbidden token. A template
    literal's substitutions are blanked with the rest of it, which is the
    text-heuristic cost this gate accepts. A regex literal is masked as a
    whole, so an apostrophe inside it is not a string delimiter.

    JSX is recognised to the extent a token scan can: `</` before a tag name
    or `>`, and a `/>` that follows an attribute-ending `}`, identifier or
    quote, are markup rather than a regex start, and an apostrophe directly
    after an identifier is JSX text (`<p>it's</p>`) rather than a string
    opener. A quote at the start of JSX text still opens a string that pairs
    with the next quote, a `/*` in JSX text still pairs with a `*/` on a
    later line, and a `/` after `throw` opens a regex the preceder rule does
    not recognise, so a quote after it pairs with the next quote. A mispair
    hides text up to that closing delimiter, which may be on a later line.

    The scan is fail-closed: a string, template literal, regex or block
    comment still open at end of file raises `UnterminatedSource` instead
    of blanking the rest of the file, so an unrecognised construct stops
    the gate with an infrastructure error rather than silently passing.
    """
    chars = list(text)
    index, size = 0, len(text)
    while index < size:
        if text.startswith("//", index):
            end = text.find("\n", index)
            end = size if end == -1 else end
            for at in range(index, end):
                chars[at] = " "
            index = end
        elif text.startswith("/*", index):
            end = text.find("*/", index + 2)
            if end == -1:
                raise UnterminatedSource("block comment", index)
            end += 2
            for at in range(index, end):
                if chars[at] != "\n":
                    chars[at] = " "
            index = end
        elif (
            text[index] == "/"
            and index + 1 < size
            and text[index + 1] not in "/*"
            and not jsx_tag_slash(text, index)
            and regex_can_start(text, index)
        ):
            end = index + 1
            in_class = False
            while end < size:
                char = text[end]
                if char == "\\":
                    end += 2
                    continue
                if char == "[":
                    in_class = True
                elif char == "]":
                    in_class = False
                elif char == "/" and not in_class:
                    break
                elif char == "\n":
                    break
                end += 1
            if end < size and text[end] == "/":
                for at in range(index + 1, end):
                    if chars[at] != "\n":
                        chars[at] = " "
                index = end + 1
                while index < size and text[index].isalpha():
                    index += 1
            elif end >= size:
                raise UnterminatedSource("regular expression", index)
            else:
                index += 1
        elif text[index] in ('"', "'", "`"):
            if text[index] == "'" and not apostrophe_starts_string(text, index):
                index += 1
                continue
            quote = text[index]
            end = index + 1
            while end < size:
                if text[end] == "\\":
                    end += 2
                    continue
                if text[end] == quote:
                    break
                end += 1
            if end >= size:
                kind = "template literal" if quote == "`" else "string"
                raise UnterminatedSource(kind, index)
            for at in range(index + 1, end):
                if chars[at] != "\n":
                    chars[at] = " "
            index = end + 1
        else:
            index += 1
    return "".join(chars)


def line_of(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


def to_ms(literal: str | None) -> int | None:
    """A millisecond budget: a constant expression, or one scaling call.

    `scaledTimeout(5_000)`, `scaledTimeout(2 * 60_000)` and a bare `5*1000`
    are all read; a recognised scaling wrapper is peeled one level and its
    inner argument is evaluated as constant arithmetic. Any other call is not
    a budget, so a table (`it.each(range(5))`) or a scale (`asyncBudgetMs(2)`)
    is not read as its first argument.
    """
    if literal is None:
        return None
    text = literal.strip()
    wrapper = WRAPPER_CALL.fullmatch(text)
    if wrapper is not None and wrapper.group(1) in SCALING_WRAPPERS:
        inner = to_ms(wrapper.group(2))
        if inner is not None:
            return inner
    return arithmetic_ms(text)


def split_args(call: str) -> list[tuple[str, int]]:
    """Split the arguments of a call text into (text, offset within `call`).

    The call text begins at its opening parenthesis. Commas inside nested
    brackets, braces or parentheses are not separators, so an arrow body or
    an options object stays one argument.
    """
    args: list[tuple[str, int]] = []
    depth = 0
    start = 1
    for index in range(1, max(len(call) - 1, 1)):
        char = call[index]
        if char in "([{":
            depth += 1
        elif char in ")]}":
            depth -= 1
        elif char == "," and depth == 0:
            args.append((call[start:index], start))
            start = index + 1
    tail = call[start:-1] if len(call) >= 2 else ""
    if tail.strip():
        args.append((tail, start))
    return args


def is_object(text: str) -> bool:
    stripped = text.strip()
    return stripped.startswith("{") and stripped.endswith("}")


def option_budget(
    argument: str, offset: int, pattern: re.Pattern[str] = OPTION_TIMEOUT
) -> tuple[int, int] | None:
    """The (offset, ms) of a `timeout`/budget field inside one argument."""
    match = pattern.search(argument)
    if match is None:
        return None
    value = to_ms(match.group(1))
    if value is None:
        return None
    return offset + match.start(1), value


def case_budgets(source: str) -> list[tuple[int, int]]:
    """The (offset, ms) of every numeric case budget in a Vitest source.

    Read forms: an options object after the case name, a trailing budget or
    options object (`it(name, fn, 5000)`, `it(name, fn, { timeout })`), an
    `it.each` (table or tagged template)/`it.for`/`it.skipIf`/`it.runIf`
    chain's returned call, `test.setTimeout`, and `vi.setConfig`. The
    Playwright-only `testInfo.setTimeout` and `test.info().setTimeout` are
    read here too, since a case bound written in a test file lowers the same
    budget. Waits that are not Testing Library queries are not case budgets
    and are not read.
    """
    found: list[tuple[int, int]] = []

    def scan_call(base: int, call: str) -> None:
        args = split_args(call)
        for argument, offset in args[1:]:
            if is_object(argument):
                budget = option_budget(argument, base + offset)
                if budget is not None:
                    found.append(budget)
        if args and not is_object(args[-1][0]):
            value = to_ms(args[-1][0])
            if value is not None:
                found.append((base + args[-1][1], value))

    def scan_returned_call(rest_start: int) -> None:
        """Scan the call returned by `it.each(table)`/`it.skipIf(cond)` etc."""
        rest = source[rest_start:]
        opening = rest.find("(")
        if opening == -1 or rest[:opening].strip():
            return
        outer = call_text(source, rest_start + opening)
        if outer is not None:
            scan_call(rest_start + opening, outer)

    for match in CASE_CHAIN.finditer(source):
        if match.start() > 0 and source[match.start() - 1] == ".":
            continue
        modifiers = CHAIN_MODIFIER.findall(match.group(2))
        if modifiers in CASE_SKIPPED_CHAINS:
            continue
        base = match.end() - 1
        call = call_text(source, base)
        if call is None:
            continue
        if modifiers and modifiers[-1] in CHAIN_RETURNS_CALL:
            # This call's arguments are a table or a condition, never a
            # budget; only the call it returns carries one.
            scan_returned_call(base + len(call))
        else:
            scan_call(base, call)
    for match in EACH_TAG_TEMPLATE.finditer(source):
        # `it.each`a | b`(...)`: the template is the table, and the call
        # after it carries the budget.
        close = source.find("`", match.end())
        if close != -1:
            scan_returned_call(close + 1)
    for match in SET_TIMEOUT_CALL.finditer(source):
        base = match.end() - 1
        call = call_text(source, base)
        if call is None:
            continue
        args = split_args(call)
        if args:
            value = to_ms(args[0][0])
            if value is not None:
                found.append((base + args[0][1], value))
    for match in SET_CONFIG_CALL.finditer(source):
        base = match.end() - 1
        call = call_text(source, base)
        if call is None:
            continue
        for budget in CONFIG_BUDGET.finditer(call):
            value = to_ms(budget.group(1))
            if value is not None:
                found.append((base + budget.start(1), value))
    return found


def async_budgets(source: str) -> list[tuple[int, int]]:
    """The (offset, ms) of every numeric wait timeout in a Vitest source.

    A `findBy*`/`findAllBy*` query and `waitFor` take a waitForOptions
    object; its `timeout` is judged against the shared async default.
    """
    found: list[tuple[int, int]] = []
    for match in ASYNC_WAIT_CALL.finditer(source):
        base = match.end() - 1
        call = call_text(source, base)
        if call is None:
            continue
        args = split_args(call)
        if not args or not is_object(args[-1][0]):
            continue
        budget = option_budget(args[-1][0], base + args[-1][1])
        if budget is not None:
            found.append(budget)
    for match in ASYNC_CONFIGURE_CALL.finditer(source):
        base = source.find("(", match.start())
        call = call_text(source, base)
        if call is None:
            continue
        budget = option_budget(call, base, ASYNC_CONFIG_TIMEOUT)
        if budget is not None:
            found.append(budget)
    return found


def call_text(source: str, open_index: int) -> str | None:
    """The text of the call whose opening parenthesis is at open_index.

    String contents are already blanked, so parentheses inside them are gone
    and a depth count is enough.
    """
    depth = 0
    for index in range(open_index, len(source)):
        if source[index] == "(":
            depth += 1
        elif source[index] == ")":
            depth -= 1
            if depth == 0:
                return source[open_index : index + 1]
    return None


def function_body(source: str, name: str) -> str | None:
    """The masked body of a named `function` declaration, or None.

    The parameter list is consumed as a balanced call, so a default value
    such as `options = {}` is not mistaken for the body's opening brace.
    """
    masked = mask_javascript(source)
    match = re.search(rf"\bfunction\s+{re.escape(name)}\b", masked)
    if match is None:
        return None
    opening = masked.find("(", match.end())
    if opening == -1:
        return None
    params = call_text(masked, opening)
    if params is None:
        return None
    brace = masked.find("{", opening + len(params))
    if brace == -1:
        return None
    depth = 0
    for index in range(brace, len(masked)):
        if masked[index] == "{":
            depth += 1
        elif masked[index] == "}":
            depth -= 1
            if depth == 0:
                return masked[brace : index + 1]
    return None


def wait_condition_takes_larger_timeout(source: str) -> bool:
    """Whether `waitForCondition` still passes the larger of its two budgets.

    The async wait rule exempts this wrapper's `timeout` because the wrapper
    takes `Math.max(options.timeout ?? 0, asyncUtilTimeout)`: a caller's `0`
    or `500` cannot lower the budget. That premise is asserted, not assumed,
    so a wrapper rewritten to use the caller's value directly stops the gate
    instead of silently passing a narrowed wait.
    """
    body = function_body(source, WAIT_CONDITION_NAME)
    if body is None:
        return False
    for match in MAX_CALL.finditer(body):
        call = call_text(body, match.end() - 1)
        if call is None:
            continue
        if CALLER_TIMEOUT.search(call) and SHARED_ASYNC_BUDGET.search(call):
            return True
    return False


def playwright_budgets(source: str) -> list[tuple[int, int]]:
    """The (offset, ms) of every numeric Playwright case budget.

    `test.setTimeout(ms)`, `testInfo.setTimeout(ms)` and
    `test.describe.configure({ timeout: ms })` name a case or suite bound. An
    assertion, element or retry wait names a wait, not a case limit, and is
    left to the assertion's own subject.
    """
    found: list[tuple[int, int]] = []
    for match in SET_TIMEOUT_CALL.finditer(source):
        base = match.end() - 1
        call = call_text(source, base)
        if call is None:
            continue
        args = split_args(call)
        if args:
            value = to_ms(args[0][0])
            if value is not None:
                found.append((base + args[0][1], value))
    for match in PLAYWRIGHT_CONFIGURE.finditer(source):
        base = match.end() - 1
        call = call_text(source, base)
        if call is None:
            continue
        budget = option_budget(call, base)
        if budget is not None:
            found.append(budget)
    return found


def config_budgets(source: str) -> list[tuple[int, int]]:
    """`testTimeout`/`hookTimeout` values written as a number in a config."""
    found: list[tuple[int, int]] = []
    for match in CONFIG_BUDGET.finditer(source):
        value = to_ms(match.group(1))
        if value is not None:
            found.append((match.start(1), value))
    return found


def playwright_default_timeout(source: str) -> tuple[int, int] | None:
    """The (ms, offset) of `defineConfig`'s top-level `timeout`.

    The value is read from the config object, not from any nested one, and
    the read does not depend on the property's indentation: a `timeout` in
    `expect`, `use` or a project is at a deeper brace depth and is skipped.
    """
    masked = mask_javascript(source)
    call = re.search(r"\bdefineConfig\s*\(", masked)
    if call is None:
        return None
    opening = masked.find("{", call.end())
    if opening == -1:
        return None
    depth = 0
    index = opening
    while index < len(masked):
        char = masked[index]
        if char == "{":
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0:
                return None
        elif depth == 1:
            match = re.compile(r"\btimeout\s*:\s*").match(masked, index)
            if match is not None:
                value_start = match.end()
                end = value_start
                nested = 0
                while end < len(masked):
                    char = masked[end]
                    if char in "([{":
                        nested += 1
                    elif char in ")]}":
                        if nested == 0:
                            break
                        nested -= 1
                    elif char == "," and nested == 0:
                        break
                    end += 1
                value = to_ms(masked[value_start:end])
                if value is None:
                    return None
                return value, value_start
        index += 1
    return None


def load_time_floors(root: str) -> tuple[dict[str, int] | None, str | None]:
    """The floors file, or (None, why) when it cannot be used.

    A missing file, invalid JSON, or a key that is missing, non-finite
    (`NaN`, `Infinity`) or not a positive number is an infrastructure error:
    a case budget has nothing to be judged against, and passing silently
    would be worse than stopping.
    """
    path = os.path.join(root, TIME_FLOORS_FILE)
    if not os.path.isfile(path):
        return None, (
            f"{TIME_FLOORS_FILE} is missing, so a case budget has no agreed "
            "floor to clear"
        )
    try:
        with open(path, encoding="utf-8") as handle:
            data = json.load(handle)
    except (OSError, ValueError):
        return None, f"{TIME_FLOORS_FILE} is not readable JSON"
    if not isinstance(data, dict):
        return None, f"{TIME_FLOORS_FILE} is not a JSON object"
    floors: dict[str, int] = {}
    for key in (VITEST_FLOOR_KEY, PLAYWRIGHT_FLOOR_KEY, ASYNC_FLOOR_KEY):
        value = data.get(key)
        # `json` parses NaN and Infinity into floats that pass `<= 0` for
        # neither sign, so finiteness is checked before the conversion that
        # would raise on them.
        if (
            isinstance(value, bool)
            or not isinstance(value, (int, float))
            or not math.isfinite(value)
            or value <= 0
        ):
            return None, (
                f"{TIME_FLOORS_FILE} names no positive finite {key}, so a "
                "shared default has no agreed floor to clear"
            )
        floors[key] = int(value)
    return floors, None


class Gate:
    def __init__(self, root: str) -> None:
        self.root = root
        self.vitest_default: int | None = None
        self.vitest_default_line: int | None = None
        self.vitest_floor: int | None = None
        self.async_default: int | None = None
        self.async_default_line: int | None = None
        self.async_floor: int | None = None
        self.playwright_default: int | None = None
        self.playwright_default_line: int | None = None
        self.playwright_floor: int | None = None
        self.files: list[tuple[str, str]] = []

    def load(self) -> int | None:
        """Read every in-scope file, derive the shared defaults, and report.

        Returns the exit code to use when the shared defaults cannot be read;
        None means the scan can proceed.
        """
        found_vitest = False
        found_playwright_scope = False
        for directory, subdirs, files in os.walk(self.root):
            relative_dir = os.path.relpath(directory, self.root)
            subdirs[:] = sorted(
                name
                for name in subdirs
                if name not in SKIPPED_DIRS
                and os.path.normpath(os.path.join(relative_dir, name))
                != os.path.join(".claude", "worktrees")
            )
            for name in sorted(files):
                path = os.path.join(directory, name)
                if os.path.islink(path):
                    continue
                relative = os.path.relpath(path, self.root)
                is_go = relative.endswith(".go")
                if not is_go and not is_test_file(relative):
                    continue
                try:
                    with open(path, encoding="utf-8") as handle:
                        text = handle.read()
                except (OSError, UnicodeDecodeError):
                    continue
                if is_go and not go_is_test_scope(relative, text):
                    continue
                self.files.append((relative, text))
                if is_go:
                    continue
                if is_playwright_spec(relative) or is_e2e_support(relative):
                    found_playwright_scope = True
                elif is_vitest_scope(relative):
                    found_vitest = True

        if not self.files:
            print(
                "test-limits: no test file or test configuration under "
                f"{self.root}; nothing was checked",
                file=sys.stderr,
            )
            return 2

        if found_vitest or found_playwright_scope:
            floors, problem = load_time_floors(self.root)
            if floors is None:
                print(f"test-limits: {problem}", file=sys.stderr)
                return 2
        else:
            floors = {}

        if found_vitest:
            preset = os.path.join(self.root, "web", "vitest.shared.ts")
            if not os.path.isfile(preset):
                print(
                    "test-limits: web/vitest.shared.ts is missing, so a case "
                    "budget has no shared default to be judged against",
                    file=sys.stderr,
                )
                return 2
            with open(preset, encoding="utf-8") as handle:
                preset_text = handle.read()
            default = VITEST_SHARED_DEFAULT.search(preset_text)
            if default is None:
                print(
                    "test-limits: web/vitest.shared.ts names no "
                    "SHARED_TIMEOUT_MS, so a case budget has no shared "
                    "default to be judged against",
                    file=sys.stderr,
                )
                return 2
            self.vitest_default = int(default.group(1).replace("_", ""))
            self.vitest_default_line = line_of(preset_text, default.start(1))
            self.vitest_floor = floors[VITEST_FLOOR_KEY]
            gate_time = os.path.join(
                self.root, "web", "test-utils", "src", "gate-time.ts"
            )
            if not os.path.isfile(gate_time):
                print(
                    "test-limits: web/test-utils/src/gate-time.ts is missing, "
                    "so a findBy/waitFor timeout has no shared async default "
                    "to be judged against",
                    file=sys.stderr,
                )
                return 2
            with open(gate_time, encoding="utf-8") as handle:
                gate_time_text = handle.read()
            async_default = ASYNC_SHARED_DEFAULT.search(gate_time_text)
            if async_default is None:
                print(
                    "test-limits: web/test-utils/src/gate-time.ts names no "
                    "ASYNC_UTIL_TIMEOUT_MS, so a findBy/waitFor timeout has no "
                    "shared async default to be judged against",
                    file=sys.stderr,
                )
                return 2
            self.async_default = int(async_default.group(1).replace("_", ""))
            self.async_default_line = line_of(gate_time_text, async_default.start(1))
            self.async_floor = floors[ASYNC_FLOOR_KEY]

            # The async wait rule exempts `waitForCondition`'s timeout because
            # the wrapper takes the larger of the caller's value and the
            # configured budget. That premise is checked, not assumed: a
            # wrapper that started using the caller's value directly would
            # make the exemption wrong, and passing the call silently would
            # let a narrowed wait through.
            wait_condition = os.path.join(self.root, WAIT_CONDITION_FILE)
            if not os.path.isfile(wait_condition):
                print(
                    "test-limits: web/test-utils/src/wait.ts is missing, so "
                    "the waitForCondition timeout exemption's premise -- the "
                    "wrapper takes the larger of the caller's value and the "
                    "shared async budget -- cannot be checked",
                    file=sys.stderr,
                )
                return 2
            try:
                with open(wait_condition, encoding="utf-8") as handle:
                    wait_condition_text = handle.read()
            except (OSError, UnicodeDecodeError):
                print(
                    "test-limits: web/test-utils/src/wait.ts cannot be read, "
                    "so the waitForCondition timeout exemption's premise -- the "
                    "wrapper takes the larger of the caller's value and the "
                    "shared async budget -- cannot be checked",
                    file=sys.stderr,
                )
                return 2
            try:
                premise_holds = wait_condition_takes_larger_timeout(
                    wait_condition_text
                )
            except UnterminatedSource as exc:
                print(
                    "test-limits: web/test-utils/src/wait.ts cannot be parsed; "
                    f"the mask reaches end of file inside a {exc.kind} that is "
                    "never closed, so the waitForCondition exemption's premise "
                    "cannot be checked",
                    file=sys.stderr,
                )
                return 2
            if not premise_holds:
                print(
                    "test-limits: waitForCondition in "
                    "web/test-utils/src/wait.ts no longer takes the larger of "
                    "options.timeout and the shared asyncUtilTimeout, so the "
                    "exemption's premise no longer holds; this check must "
                    "change rather than pass the call's timeout silently",
                    file=sys.stderr,
                )
                return 2

        if found_playwright_scope:
            config = os.path.join(
                self.root, "starters", "demo", "web", "e2e", "playwright.config.ts"
            )
            if not os.path.isfile(config):
                print(
                    "test-limits: starters/demo/web/e2e/playwright.config.ts is "
                    "missing, so a Playwright case budget has no shared default "
                    "to be judged against",
                    file=sys.stderr,
                )
                return 2
            with open(config, encoding="utf-8") as handle:
                source = handle.read()
            try:
                default = playwright_default_timeout(source)
            except UnterminatedSource as exc:
                print(
                    "test-limits: starters/demo/web/e2e/playwright.config.ts "
                    "cannot be parsed; the mask reaches end of file inside a "
                    f"{exc.kind} that is never closed, so a case budget has "
                    "no shared default to be judged against",
                    file=sys.stderr,
                )
                return 2
            if default is None:
                print(
                    "test-limits: the Playwright config names no top-level "
                    "timeout, so a case budget has no shared default to be "
                    "judged against",
                    file=sys.stderr,
                )
                return 2
            self.playwright_default, offset = default
            self.playwright_default_line = line_of(source, offset)
            self.playwright_floor = floors[PLAYWRIGHT_FLOOR_KEY]

        return None

    def findings(self) -> list[str]:
        problems: list[str] = []
        for relative, text in self.files:
            try:
                self.scan_file(relative, text, problems)
            except UnterminatedSource as exc:
                line = line_of(text, exc.offset)
                raise GateError(
                    f"{relative}:{line}: cannot parse this test source; the "
                    f"mask reaches end of file inside a {exc.kind} that is "
                    "never closed"
                ) from None
        problems.extend(self.shared_default_findings())
        return problems

    def scan_file(self, relative: str, text: str, problems: list[str]) -> None:
        problems.extend(self.core_count_findings(relative, text))
        if not is_javascript(relative):
            return
        source = mask_javascript(text)
        problems.extend(self.percentage_width_findings(relative, text))
        if is_playwright_spec(relative) or is_e2e_support(relative):
            # Both a spec and its support files (fixtures, helpers,
            # global setup) can name a Playwright case bound; a helper
            # that lowered one would narrow every case that used it.
            if self.playwright_default is not None:
                problems.extend(
                    self.limit_findings(
                        relative,
                        source,
                        playwright_budgets(source),
                        self.playwright_default,
                    )
                )
            return
        if self.vitest_default is not None:
            problems.extend(
                self.limit_findings(
                    relative, source, case_budgets(source), self.vitest_default
                )
            )
            if JS_TEST_CONFIG.search(relative) or JS_SHARED_CONFIG.search(relative):
                problems.extend(
                    self.limit_findings(
                        relative,
                        source,
                        config_budgets(source),
                        self.vitest_default,
                    )
                )
            if self.async_default is not None:
                problems.extend(
                    self.async_limit_findings(
                        relative,
                        source,
                        async_budgets(source),
                        self.async_default,
                    )
                )

    @staticmethod
    def percentage_width_findings(relative: str, text: str) -> list[str]:
        masked = mask_javascript(text)
        problems: list[str] = []
        for match in WIDTH_PERCENT_PROPERTY.finditer(masked):
            quote = match.group(2)
            end = text.find(quote, match.end())
            if end == -1:
                continue
            if PERCENT_VALUE.fullmatch(text[match.end() : end].strip()) is None:
                continue
            line = line_of(masked, match.start())
            problems.append(
                f"{relative}:{line}: {match.group(1)}: a width given as a "
                "percentage of the host's core count; the gate's width comes "
                "from GATE_JOBS, which the caller sets"
            )
        return problems

    def core_count_findings(self, relative: str, text: str) -> list[str]:
        masked = mask_go(text) if relative.endswith(".go") else mask_javascript(text)
        problems: list[str] = []
        reported: set[tuple[int, str]] = set()

        def report(offset: int, token: str, message: str) -> None:
            line = line_of(masked, offset)
            key = (line, token)
            if key in reported:
                return
            reported.add(key)
            problems.append(f"{relative}:{line}: {token}: {message}")

        probe = (
            "derives concurrency from the host's core count; the gate's "
            "width comes from GATE_JOBS, which the caller sets"
        )
        if relative.endswith(".go"):
            problems.extend(self._go_core_count_findings(relative, text, masked))
            return problems
        parser = strip_comments(text)
        for token, pattern in js_core_patterns(parser):
            for match in pattern.finditer(masked):
                report(match.start(), token, probe)
        for match in re.finditer(
            r"\bnavigator\s*\.\s*hardwareConcurrency\b", masked
        ):
            report(match.start(), "navigator.hardwareConcurrency", probe)
        problems.extend(self._js_inline_os_findings(relative, text, masked))
        return problems

    @staticmethod
    def _js_inline_os_findings(relative: str, text: str, masked: str) -> list[str]:
        """Core probes through an inline or dynamic `os` import.

        `require('os').cpus()`, `(await import('node:os')).cpus()` and the
        destructured `const { cpus } = await import('node:os')` bind no alias
        the binding scan can name, and the mask blanks the module specifier
        itself. Each is matched on a copy that keeps the string, then kept
        only when its keyword survives the masked copy -- which it does in
        code, never inside a string or template literal.
        """
        parser = strip_comments(text)
        probe = (
            "derives concurrency from the host's core count; the gate's "
            "width comes from GATE_JOBS, which the caller sets"
        )
        problems: list[str] = []

        def keyword_is_code(match: re.Match[str]) -> bool:
            start, end = match.span("kw")
            return parser[start:end] == masked[start:end]

        def add(offset: int, member: str) -> None:
            line = line_of(masked, offset)
            problems.append(f"{relative}:{line}: os.{member}: {probe}")

        for pattern in (INLINE_REQUIRE_OS_MEMBER, DYNAMIC_IMPORT_OS_MEMBER):
            for match in pattern.finditer(parser):
                if keyword_is_code(match):
                    add(match.start(), match.group("member"))
        for pattern in (DYNAMIC_IMPORT_OS_DESTRUCTURE, DYNAMIC_IMPORT_OS_THEN):
            for match in pattern.finditer(parser):
                if not keyword_is_code(match):
                    continue
                for entry in match.group("spec").split(","):
                    entry = entry.strip()
                    if not entry:
                        continue
                    member = re.split(r"\s+as\s+|\s*:\s*", entry)[0].strip()
                    if member in NAMED_OS_MEMBERS:
                        add(match.start(), member)
        return problems

    def _go_core_count_findings(
        self, relative: str, text: str, masked: str
    ) -> list[str]:
        """Go core probes, with the runtime import alias and the save/restore read.

        `runtime.NumCPU` under any import alias is a probe. A `GOMAXPROCS`
        call with a positive literal sets a width the test chose and passes.
        A call whose return value is read into a name passes when that name
        is used nowhere but a restore call, whatever its argument was (`0`, a
        negative or a positive literal): the idiom reads the process width
        only to put it back. A query (`0` or a negative argument) that is not
        saved passes only in that idiom; a variable argument passes only when
        it is such a saved-and-restored name, or a range variable iterating a
        table the test wrote.
        """
        problems: list[str] = []
        names = go_runtime_names(text)
        runtime_names = "|".join(re.escape(name) for name in sorted(names))
        prefix = rf"(?:(?:{runtime_names})\s*\.\s*)?"
        num_cpu = re.compile(rf"\b(?:{runtime_names})\s*\.\s*NumCPU\s*\(")
        save = re.compile(
            rf"\b([A-Za-z_]\w*)\s*:?=\s*{prefix}GOMAXPROCS\s*\(\s*[+-]?\d+\s*\)"
        )
        query = re.compile(rf"{prefix}GOMAXPROCS\s*\(\s*(?:0|-\d+)\s*\)")
        variable = re.compile(rf"{prefix}GOMAXPROCS\s*\(\s*([A-Za-z_]\w*)\s*\)")
        for match in num_cpu.finditer(masked):
            problems.append(
                f"{relative}:{line_of(masked, match.start())}: runtime.NumCPU: "
                "derives concurrency from the host's core count; the gate's "
                "width comes from GATE_JOBS, which the caller sets"
            )
        for scope_start, scope_end in go_top_level_scopes(masked):
            scope = masked[scope_start:scope_end]
            scope_range_vars: set[str] = set()
            for range_match in GO_RANGE_VARS.finditer(scope):
                for name in re.split(r"[,\s]+", range_match.group(1)):
                    if name.isidentifier():
                        scope_range_vars.add(name)
            saves = list(save.finditer(scope))
            restores = [
                (m.group(1), m.start(1), m.end(1))
                for m in variable.finditer(scope)
            ]
            # A saved name is clean when every occurrence of it within this
            # scope is either the left-hand side of a save or an argument to
            # a restore. Pairing inside one function keeps two same-named
            # `prev` variables from accusing each other, and lets one that
            # escapes be reported without its neighbour. A name that occurs
            # anywhere else has escaped, and the width it holds was a default.
            save_names = {
                span.group(1) for span in saves if span.group(1) != "_"
            }
            lhs = {(span.start(1), span.end(1)) for span in saves}
            restore_args = {(start, end) for _, start, end in restores}
            escaped: set[str] = set()
            for name in save_names:
                for occurrence in re.finditer(rf"\b{re.escape(name)}\b", scope):
                    at = (occurrence.start(), occurrence.end())
                    if at in lhs or at in restore_args:
                        continue
                    escaped.add(name)
                    break
            clean = save_names - escaped
            for span in saves:
                name = span.group(1)
                # `_` discards the width, and a clean name is restored only.
                if name == "_" or name not in escaped:
                    continue
                # A `0`/negative save is reported by the query scan below;
                # this report exists for a positive literal, which the query
                # scan does not match, whose read value escaped anyway.
                if re.search(
                    r"GOMAXPROCS\s*\(\s*(?:0|-\d+)\s*\)", span.group(0)
                ):
                    continue
                problems.append(
                    f"{relative}:{line_of(masked, scope_start + span.start())}: "
                    f"runtime.GOMAXPROCS(...): the width read here into {name} is "
                    "used beyond the restore, so it was taken as a default; the "
                    "gate's width comes from GATE_JOBS, which the caller sets"
                )
            for match in query.finditer(scope):
                # A query that is a save's own call is judged above; the saved
                # value is clean, or it escaped and was reported there.
                if any(
                    span.group(1) in clean
                    and span.start() <= match.start() < span.end()
                    for span in saves
                ):
                    continue
                problems.append(
                    f"{relative}:{line_of(masked, scope_start + match.start())}: "
                    "runtime.GOMAXPROCS(0): takes the process width as a default; "
                    "the gate's width comes from GATE_JOBS, which the caller sets "
                    "(save and restore is the one exception)"
                )
            for match in variable.finditer(scope):
                if (
                    match.group(1) in clean
                    or match.group(1) in scope_range_vars
                ):
                    continue
                problems.append(
                    f"{relative}:{line_of(masked, scope_start + match.start(1))}: "
                    f"runtime.GOMAXPROCS({match.group(1)}): takes a width from a "
                    "variable of unknown origin; the gate's width comes from "
                    "GATE_JOBS, which the caller sets (save and restore is the "
                    "one exception)"
                )
        return problems

    def async_limit_findings(
        self, relative: str, source: str, budgets: list[tuple[int, int]], floor: int
    ) -> list[str]:
        problems: list[str] = []
        seen: set[tuple[int, int]] = set()
        for offset, value in budgets:
            line = line_of(source, offset)
            # No zero exception here: 0 on a wait is a wait that returns at
            # once, not an unlimited one, so it stays below the default.
            if value >= floor or (line, value) in seen:
                continue
            seen.add((line, value))
            problems.append(
                f"{relative}:{line}: {value}ms: an async wait timeout below "
                f"the shared default of {floor}ms; a wait bounds a condition, "
                "so a smaller one buys nothing and fails on a loaded machine"
            )
        return problems

    @staticmethod
    def limit_findings(
        relative: str, source: str, budgets: list[tuple[int, int]], floor: int
    ) -> list[str]:
        problems: list[str] = []
        seen: set[tuple[int, int]] = set()
        for offset, value in budgets:
            line = line_of(source, offset)
            # Both Vitest and Playwright read a case budget of 0 as no limit,
            # which is a widening and passes.
            if value == 0 or value >= floor or (line, value) in seen:
                continue
            seen.add((line, value))
            problems.append(
                f"{relative}:{line}: {value}ms: a case time limit below the "
                f"shared default of {floor}ms; a limit bounds a wait, so a "
                "smaller one buys nothing and fails a correct case on a "
                "loaded machine"
            )
        return problems

    def shared_default_findings(self) -> list[str]:
        problems: list[str] = []
        if (
            self.vitest_default is not None
            and self.vitest_floor is not None
            and self.vitest_default < self.vitest_floor
        ):
            preset = os.path.join("web", "vitest.shared.ts")
            problems.append(
                f"{preset}:{self.vitest_default_line}: {self.vitest_default}ms: "
                "the shared Vitest default is below its floor of "
                f"{self.vitest_floor}ms; a lowered default would make every "
                "per-case limit below it pass"
            )
        if (
            self.async_default is not None
            and self.async_floor is not None
            and self.async_default < self.async_floor
        ):
            relative = os.path.join("web", "test-utils", "src", "gate-time.ts")
            problems.append(
                f"{relative}:{self.async_default_line}: {self.async_default}ms: "
                "the shared async wait default is below its floor of "
                f"{self.async_floor}ms; a lowered default would make every "
                "async wait below it pass"
            )
        if (
            self.playwright_default is not None
            and self.playwright_floor is not None
            and self.playwright_default < self.playwright_floor
        ):
            relative = os.path.join(
                "starters", "demo", "web", "e2e", "playwright.config.ts"
            )
            problems.append(
                f"{relative}:{self.playwright_default_line}: "
                f"{self.playwright_default}ms: the shared Playwright case "
                "default is below its floor of "
                f"{self.playwright_floor}ms; a lowered default would make "
                "every per-case limit below it pass"
            )
        return problems


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--root",
        default=os.curdir,
        help="the tree to scan (default: the current directory)",
    )
    args = parser.parse_args(argv)

    root = os.path.abspath(args.root)
    if not os.path.isdir(root):
        print(f"test-limits: {args.root} is not a directory", file=sys.stderr)
        return 2

    gate = Gate(root)
    failed = gate.load()
    if failed is not None:
        return failed

    try:
        problems = gate.findings()
    except GateError as exc:
        print(f"test-limits: {exc}", file=sys.stderr)
        return 2
    for problem in problems:
        print(problem)
    if problems:
        print(
            f"test-limits: {len(problems)} finding(s); a test takes its width "
            "from GATE_JOBS and its clock from the shared default the caller "
            "scales",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
