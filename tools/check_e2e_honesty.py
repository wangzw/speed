#!/usr/bin/env python3
"""End-to-end honesty gate: the journeys, their specs, and the tier's rules.

The e2e tier (starters/demo/web/e2e) claims to prove the product works by
driving the built demo binary through a real browser with mouse and keyboard
only. Six things can quietly falsify that claim, and this gate holds each:

  1. Coverage. Every journey document under docs/prd/journeys is either
     exercised by a spec or registered in the harness's journey registry as
     pending, unreachable or unimplemented *with a reason*. A journey that is
     neither is a documented path nothing vouches for.
  2. Declarations. Each spec declares, in its header, the journey document it
     drives (`@journey <file>.md`) and the requirement ids it exercises
     (`@covers REQ-...`). The declared journey must exist, every covered id
     must be one the journey document itself lists, and the spec must contain
     at least one test that actually runs -- a header alone covers nothing.
  3. Forbidden tokens. No source file under e2e/ may issue network requests,
     intercept routes or evaluate script in the page: those are the ways a
     "browser" test stops touching the rendered product. The one allowed side
     channel is a WebAuthn.* virtual-authenticator fixture over a CDP session;
     any other CDP domain is a finding.
  4. Layers. The harness may assemble its environment (build, seed, probe) in
     fixtures/, but a spec or a helper may not reach around the product: it may
     not spawn processes, write files, open a database client, or read the
     process log beyond the sanctioned reads -- a verification code, the log
     SMS carrier's accepted-message line, and the delivery pipeline's
     external-send record. Only fixtures/log.ts parses logfmt lines; a spec or
     helper may import only `waitForVerificationCode` and asks the server
     fixture for the other two.
  5. Artifacts. Every Playwright artifact path -- outputDir, the HTML
     reporter's outputFolder, any outputFile -- must resolve under /tmp (or
     through the harness's own /tmp ARTIFACT_ROOT), so a run leaves nothing in
     the tree.
  6. Entry point. The root Makefile must carry an `e2e` target, and `check`
     must not invoke the tier directly or indirectly means (the target name,
     `playwright test`, or the tier's type-check).

Registry contract (starters/demo/web/e2e/journeys.ts). The gate reads, not
executes, the TypeScript, so the registry uses a shape it can parse:

    export const journeys = [
      {
        journey: 'journey-example.md',
        status: 'pending',                // 'covered', 'pending', 'unreachable' or 'unimplemented'
        reason: 'why the tier cannot drive it, or when its spec is due',
        covers: ['REQ-example-001'],
      },
    ]

An object must name `journey` and carry one of those four statuses. A
`pending`, `unreachable` or `unimplemented` entry must carry a non-empty
`reason`: `pending` says the path is reachable and its spec is scheduled;
`unimplemented` says the product does not present the path yet, so no spec can
drive it and the gap is the feature rather than the schedule; `unreachable`
names the precondition the tier cannot provide. An absent registry is valid --
a tree whose journeys all have specs needs none.

The gate prints a one-line summary of the covered, pending, unreachable and
unimplemented counts on every run, so the number of journeys still unverified
is visible rather than implied, and an unimplemented path is not miscounted as
a spec merely waiting its turn.

Spec declaration contract (header comment, before the imports):

    // @journey journey-first-registration-to-personal-organization.md
    // @covers REQ-signin-001, REQ-signup-001

Exit codes: 0 = clean; 1 = one or more findings, each printed with its path
(and line for a token hit); 2 = infrastructure error -- the root is not a
directory, the journeys directory is missing or empty (nothing to check is not
a pass), the e2e directory is missing, or the registry cannot be parsed.
"""

from __future__ import annotations

import argparse
import os
import re
import sys

JOURNEYS_DIR = os.path.join("docs", "prd", "journeys")
E2E_DIR = os.path.join("starters", "demo", "web", "e2e")
SPECS_DIR = os.path.join(E2E_DIR, "specs")
HELPERS_DIR = os.path.join(E2E_DIR, "helpers")
FIXTURES_DIR = os.path.join(E2E_DIR, "fixtures")
REGISTRY = os.path.join(E2E_DIR, "journeys.ts")
STATUSES = ("covered", "pending", "unreachable", "unimplemented")
PLAYWRIGHT_CONFIG = os.path.join(E2E_DIR, "playwright.config.ts")
PATHS_MODULE = os.path.join(FIXTURES_DIR, "paths.ts")
LOG_MODULE = os.path.join(FIXTURES_DIR, "log.ts")
MAKEFILE = "Makefile"

# The directories whose sources may drive the product but must not reach
# around it. fixtures/ is the assembly layer and is deliberately absent.
RESTRICTED_LAYERS = (SPECS_DIR, HELPERS_DIR)

# A requirement id as the PRD writes it: REQ-<family>-<number>.
REQ_ID = re.compile(r"REQ-[A-Za-z0-9-]+")

# A spec's header declarations.
JOURNEY_DECL = re.compile(r"@journey\s+([A-Za-z0-9._-]+\.md)")
COVERS_DECL = re.compile(r"@covers\s+([^\n]*)")

# A CDP command a send() names: Domain.Method. A string without a dot (a
# websocket frame, say) is not one and is left alone.
CDP_COMMAND = re.compile(r"^[A-Za-z][A-Za-z0-9]*\.")
ALLOWED_CDP_DOMAIN = "WebAuthn"

# The forbidden source tokens. They are matched against comment-stripped text,
# so a comment that merely mentions one is not a finding. The WebAuthn CDP
# family is handled separately, because allowing it means reading the command
# the send() names rather than exempting a bare token.
FORBIDDEN_PATTERNS = [
    ("direct HTTP", re.compile(r"(?<![\w$.])fetch\s*\(")),
    ("direct HTTP", re.compile(r"\b(?:window|globalThis|self)\s*\.\s*fetch\b")),
    ("direct HTTP", re.compile(r"\bnew\s+XMLHttpRequest\b")),
    ("direct HTTP", re.compile(r"\bXMLHttpRequest\b")),
    ("direct HTTP", re.compile(r"from\s+['\"]axios(?:/[^'\"]*)?['\"]")),
    ("direct HTTP", re.compile(r"from\s+['\"]node-fetch(?:/[^'\"]*)?['\"]")),
    ("direct HTTP", re.compile(r"import\s+['\"]axios(?:/[^'\"]*)?['\"]")),
    ("direct HTTP", re.compile(r"import\s+['\"]node-fetch(?:/[^'\"]*)?['\"]")),
    ("direct HTTP", re.compile(r"(?:require|import)\s*\(\s*['\"]axios")),
    ("direct HTTP", re.compile(r"(?:require|import)\s*\(\s*['\"]node-fetch")),
    ("direct HTTP", re.compile(r"\bnew\s+WebSocket\b")),
    ("direct HTTP", re.compile(r"from\s+['\"]ws(?:/[^'\"]*)?['\"]")),
    (
        "in-page script evaluation",
        re.compile(
            r"\.\s*(?:evaluate|evaluateHandle|evaluateAll|\$eval|\$\$eval|addInitScript)\s*\("
        ),
    ),
    (
        "route interception",
        re.compile(r"\.\s*(?:route|routeFromHAR|routeWebSocket|unroute|unrouteAll)\s*\("),
    ),
    (
        "API request context",
        re.compile(
            r"\brequest\s*\.\s*(?:get|post|put|patch|delete|head|fetch|storageState)\s*\("
        ),
    ),
    (
        "API request context",
        re.compile(
            r"\.\s*request\s*\.\s*(?:get|post|put|patch|delete|head|fetch|storageState)\s*\("
        ),
    ),
    ("CDP side channel", re.compile(r"\bconnectOverCDP\s*\(")),
]

# A send() call whose command argument is a literal, and a session-shaped send
# whose command is dynamic (which the gate cannot prove is WebAuthn).
CDP_SEND_LITERAL = re.compile(r"\.\s*send\s*\(\s*(['\"])([^'\"]*)\1")
CDP_SEND_DYNAMIC = re.compile(
    r"\b(\w*(?:session|cdp)\w*)\s*\.\s*send\s*\(\s*(?![`'\"])", re.IGNORECASE
)

# Every Playwright artifact path key. outputDir is the tier's own; the HTML
# reporter writes outputFolder, and other reporters write outputFile.
ARTIFACT_PATH = re.compile(
    r"\b(outputDir|outputFolder|outputFile)\s*:\s*(['\"`])([^'\"`]*)\2"
)
# The harness's per-run root, which the config may compose its paths from.
ARTIFACT_ROOT_DECL = re.compile(r"\bARTIFACT_ROOT\s*=\s*(['\"`])([^'\"`]*)\1")

# Source suffixes the token scan reads.
SOURCE_SUFFIXES = (".ts", ".tsx", ".mts", ".cts", ".js", ".mjs", ".cjs")

# --- layer rules -----------------------------------------------------------

# Modules a spec or helper may not import: they reach around the product.
RESTRICTED_BANNED_MODULES = {
    "node:child_process",
    "child_process",
    "better-sqlite3",
    "sqlite3",
    "sql.js",
    "node:sqlite",
    "@libsql/client",
    "knex",
    "sequelize",
    "typeorm",
    "prisma",
    "pg",
    "pg-promise",
    "mysql",
    "mysql2",
    "mongodb",
}

# Filesystem entries that write. A read-only import (the locale bundles) is
# allowed; a namespace or default import cannot be proven read-only.
FS_MODULES = {"node:fs", "fs", "node:fs/promises", "fs/promises"}
FS_WRITE_FUNCTIONS = {
    "appendFile",
    "appendFileSync",
    "chmod",
    "chmodSync",
    "chown",
    "chownSync",
    "copyFile",
    "copyFileSync",
    "cp",
    "cpSync",
    "createWriteStream",
    "link",
    "linkSync",
    "mkdir",
    "mkdirSync",
    "mkdtemp",
    "mkdtempSync",
    "open",
    "openSync",
    "rename",
    "renameSync",
    "rm",
    "rmSync",
    "rmdir",
    "rmdirSync",
    "symlink",
    "symlinkSync",
    "truncate",
    "truncateSync",
    "unlink",
    "unlinkSync",
    "utimes",
    "utimesSync",
    "write",
    "writeFile",
    "writeFileSync",
    "writeSync",
}

# The one log reader a spec or helper may import directly. The log carrier's
# accepted-message and the pipeline's external-send line are read through the
# server fixture, so they never appear on this import surface.
ALLOWED_LOG_EXPORTS = {"waitForVerificationCode"}
LOG_READ = re.compile(r"\.\s*log\s*\.\s*(?:text|lines)\b")
LOG_WAIT = re.compile(r"\.\s*log\s*\.\s*waitFor\s*\(")
PARSE_LOGFMT = re.compile(r"(?<![\w$.])parseLogfmt\s*\(")
VERIFICATION_FIELD = re.compile(r"\bverification_code\b")

# Import forms. The clause is the text between `import` and `from`; the
# specifier list inside `{}` is read separately.
IMPORT_FROM = re.compile(
    r"\bimport\s+(type\s+)?([^;'\"\n]*?)\s+from\s+(['\"])([^'\"]+)\3"
)
SIDE_EFFECT_IMPORT = re.compile(r"\bimport\s+(['\"])([^'\"]+)\1")
IMPORT_CALL = re.compile(r"\bimport\s*\(\s*(['\"])([^'\"]+)\1\s*\)")
REQUIRE_CALL = re.compile(r"\brequire\s*\(\s*(['\"])([^'\"]+)\1\s*\)")
NAMED_SPECIFIERS = re.compile(r"\{([^}]*)\}")

# A test that actually runs: `test(...)`, `test.only(...)` or `test.step(...)`.
# `test.skip` / `test.fixme` and the suite/hook members do not count.
TEST_CALL = re.compile(r"(?<![\w$.])test\s*(?:\.\s*([A-Za-z_$][\w$]*))?\s*\(")
NON_TEST_MEMBERS = {
    "beforeAll",
    "beforeEach",
    "afterAll",
    "afterEach",
    "describe",
    "expect",
    "extend",
    "use",
}

# How a `check` recipe can reach the e2e tier even without naming the target.
CHECK_E2E_INVOCATION = re.compile(
    r"\be2e(?:-install)?\b"
    r"|playwright\s+test"
    r"|typecheck:e2e"
    r"|starters/demo/web/e2e"
    r"|--config\s+\S*e2e/"
)


class Infrastructure(Exception):
    """A condition that makes the gate unable to judge the tree."""


def strip_comments(text: str) -> str:
    """Blank out comments while keeping string literals in place.

    A real scanner, not a regular expression: a `//` inside a string is a
    string, not the start of a comment, and a `/*` inside one is not a block
    comment. Comments are replaced character for character (newlines kept), so
    a finding's line number still points at the file as written. String
    literals are deliberately kept: the WebAuthn allowance is decided by
    reading the command string, and a blanket string mask would lose it.
    """
    output: list[str] = []
    index = 0
    size = len(text)
    state = "code"
    # One entry per `${ ... }` expression currently open inside a template.
    # The value is the brace depth inside that expression.
    template_expressions: list[int] = []

    while index < size:
        char = text[index]

        if state == "code":
            if char == "/" and index + 1 < size and text[index + 1] == "/":
                state = "line"
                output.append("  ")
                index += 2
                continue
            if char == "/" and index + 1 < size and text[index + 1] == "*":
                state = "block"
                output.append("  ")
                index += 2
                continue
            if char == "'":
                state = "single"
            elif char == '"':
                state = "double"
            elif char == "`":
                state = "template"
            elif template_expressions:
                if char == "{":
                    template_expressions[-1] += 1
                elif char == "}":
                    if template_expressions[-1] == 0:
                        template_expressions.pop()
                        state = "template"
                        output.append(char)
                        index += 1
                        continue
                    template_expressions[-1] -= 1
            output.append(char)
            index += 1
            continue

        if state == "line":
            if char == "\n":
                state = "code"
                output.append(char)
            else:
                output.append(" ")
            index += 1
            continue

        if state == "block":
            if char == "*" and index + 1 < size and text[index + 1] == "/":
                state = "code"
                output.append("  ")
                index += 2
                continue
            output.append("\n" if char == "\n" else " ")
            index += 1
            continue

        if state in ("single", "double"):
            output.append(char)
            if char == "\\" and index + 1 < size:
                output.append(text[index + 1])
                index += 2
                continue
            if (state == "single" and char == "'") or (
                state == "double" and char == '"'
            ):
                state = "code"
            index += 1
            continue

        # state == "template"
        output.append(char)
        if char == "\\" and index + 1 < size:
            output.append(text[index + 1])
            index += 2
            continue
        if char == "`":
            state = "code"
            index += 1
            continue
        if char == "$" and index + 1 < size and text[index + 1] == "{":
            output.append("{")
            template_expressions.append(0)
            state = "code"
            index += 2
            continue
        index += 1

    return "".join(output)


def line_of(text: str, index: int) -> int:
    """The 1-based line number of a character index."""
    return text.count("\n", 0, index) + 1


def read_text(path: str) -> str:
    with open(path, encoding="utf-8") as handle:
        return handle.read()


def walk_files(root: str, suffix: str | tuple[str, ...]) -> list[str]:
    """Every file under root ending with suffix, in a stable order."""
    found: list[str] = []
    for directory, subdirs, files in os.walk(root):
        subdirs[:] = sorted(d for d in subdirs if d != "node_modules")
        for name in sorted(files):
            if name.endswith(suffix):
                found.append(os.path.join(directory, name))
    return found


# --- registry parsing -------------------------------------------------------


def _balanced(text: str, start: int, opening: str, closing: str) -> str:
    """The balanced expression beginning at text[start] == opening."""
    depth = 0
    quote = None
    escape = False
    for index in range(start, len(text)):
        char = text[index]
        if quote is not None:
            if escape:
                escape = False
            elif char == "\\":
                escape = True
            elif char == quote:
                quote = None
            continue
        if char in "'\"`":
            quote = char
        elif char == opening:
            depth += 1
        elif char == closing:
            depth -= 1
            if depth == 0:
                return text[start : index + 1]
    raise Infrastructure("registry: a bracket literal never closes")


def _objects(array_text: str) -> list[str]:
    """The top-level { ... } object literals inside an array literal."""
    objects: list[str] = []
    depth = 0
    quote = None
    escape = False
    start = None
    for index, char in enumerate(array_text):
        if quote is not None:
            if escape:
                escape = False
            elif char == "\\":
                escape = True
            elif char == quote:
                quote = None
            continue
        if char in "'\"`":
            quote = char
        elif char == "{":
            if depth == 0:
                start = index
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0 and start is not None:
                objects.append(array_text[start : index + 1])
                start = None
    return objects


def _string_field(obj: str, field: str) -> str | None:
    match = re.search(
        rf"\b{field}\s*:\s*(['\"`])(.*?)\1", obj, flags=re.DOTALL
    )
    return match.group(2) if match is not None else None


def _covers_field(obj: str) -> list[str]:
    match = re.search(r"\bcovers\s*:\s*\[([^\]]*)\]", obj, flags=re.DOTALL)
    return REQ_ID.findall(match.group(1)) if match is not None else []


def parse_registry(text: str) -> list[dict[str, object]]:
    """Parse the journeys.ts registry into a list of entry dicts.

    The gate reads TypeScript rather than executing it, so the contract is the
    literal shape documented in the module docstring: an array of object
    literals with string fields and a string array. A declaration that cannot
    be located is an infrastructure error rather than a silent empty registry,
    because an unreadable registry would make every unreachable journey look
    like a coverage gap.
    """
    declaration = re.search(
        r"\b(?:export\s+)?const\s+journeys\b[^=]*?=\s*(\[)", text
    )
    if declaration is None:
        raise Infrastructure(
            "registry: no `const journeys = [...]` declaration to read"
        )
    array_text = _balanced(text, declaration.start(1), "[", "]")
    entries: list[dict[str, object]] = []
    for obj in _objects(array_text):
        entries.append(
            {
                "journey": _string_field(obj, "journey"),
                "status": _string_field(obj, "status"),
                "reason": _string_field(obj, "reason") or "",
                "covers": _covers_field(obj),
            }
        )
    return entries


# --- checks -----------------------------------------------------------------


def journey_documents(journeys_dir: str) -> dict[str, set[str]]:
    """Map each journey document's file name to the requirement ids it lists."""
    documents: dict[str, set[str]] = {}
    for path in walk_files(journeys_dir, ".md"):
        documents[os.path.basename(path)] = set(REQ_ID.findall(read_text(path)))
    return documents


def has_executable_test(text: str) -> bool:
    """Whether comment-stripped spec text carries a test that will run."""
    for match in TEST_CALL.finditer(text):
        member = match.group(1)
        if member in ("skip", "fixme"):
            continue
        if member in NON_TEST_MEMBERS:
            continue
        rest = text[match.end() :].lstrip()
        if rest.startswith(")"):
            continue
        return True
    return False


def check_specs(root: str, documents: dict[str, set[str]]) -> tuple[set[str], list[str]]:
    """Validate each spec's declarations; return the declared journeys."""
    findings: list[str] = []
    specs_dir = os.path.join(root, SPECS_DIR)
    if not os.path.isdir(specs_dir):
        return set(), findings

    declared_journeys: set[str] = set()
    for path in walk_files(specs_dir, ".spec.ts"):
        relative = os.path.relpath(path, root)
        text = read_text(path)
        journeys = JOURNEY_DECL.findall(text)
        covers = set(REQ_ID.findall(" ".join(COVERS_DECL.findall(text))))

        if not journeys:
            findings.append(f"{relative}: no `@journey <file>.md` declaration")
        elif len(set(journeys)) > 1:
            findings.append(
                f"{relative}: declares more than one journey: {', '.join(sorted(set(journeys)))}"
            )
        else:
            journey = journeys[0]
            if journey not in documents:
                findings.append(
                    f"{relative}: declares an unknown journey document `{journey}`"
                )
            else:
                declared_journeys.add(journey)
                unknown = covers - documents[journey]
                if unknown:
                    findings.append(
                        f"{relative}: covers requirement id(s) absent from `{journey}`: "
                        f"{', '.join(sorted(unknown))}"
                    )

        if not covers:
            findings.append(f"{relative}: no `@covers REQ-...` declaration")

        # A header that names a journey without a test body vouches for
        # nothing: the registry would call the journey covered while no
        # browser ever opens.
        if not has_executable_test(strip_comments(text)):
            findings.append(
                f"{relative}: declares a journey but has no `test(...)` that runs "
                "(`test.skip`/`test.fixme` do not count)"
            )

    return declared_journeys, findings


def check_registry(
    root: str, documents: dict[str, set[str]]
) -> tuple[dict[str, dict[str, object]], list[str]]:
    """Read the registry and validate each entry; return it keyed by journey."""
    findings: list[str] = []
    path = os.path.join(root, REGISTRY)
    if not os.path.isfile(path):
        return {}, findings

    by_journey: dict[str, dict[str, object]] = {}
    for entry in parse_registry(strip_comments(read_text(path))):
        journey = entry["journey"]
        if not journey:
            findings.append(f"{REGISTRY}: an entry names no `journey`")
            continue
        if journey not in documents:
            findings.append(
                f"{REGISTRY}: entry names an unknown journey document `{journey}`"
            )
            continue
        status = entry["status"]
        if status not in STATUSES:
            findings.append(
                f"{REGISTRY}: `{journey}` has unknown status {status!r} "
                "(expected 'covered', 'pending', 'unreachable' or 'unimplemented')"
            )
            continue
        if status in ("pending", "unreachable", "unimplemented") and not str(entry["reason"]).strip():
            findings.append(
                f"{REGISTRY}: `{journey}` is marked {status} with no reason"
            )
        by_journey[journey] = entry
    return by_journey, findings


def check_coverage(
    documents: dict[str, set[str]],
    declared_journeys: set[str],
    registry: dict[str, dict[str, object]],
) -> list[str]:
    """Every journey is either declared by a spec or registered non-covered."""
    findings: list[str] = []
    for journey in sorted(documents):
        entry = registry.get(journey)
        if journey in declared_journeys:
            continue
        if entry is not None and entry["status"] in (
            "pending",
            "unreachable",
            "unimplemented",
        ):
            continue
        if entry is not None and entry["status"] == "covered":
            findings.append(
                f"{REGISTRY}: `{journey}` is marked covered but no spec declares it"
            )
            continue
        findings.append(
            f"{JOURNEYS_DIR}/{journey}: neither covered by a spec nor registered "
            "pending, unreachable or unimplemented with a reason"
        )
    return findings


def coverage_counts(
    documents: dict[str, set[str]],
    declared_journeys: set[str],
    registry: dict[str, dict[str, object]],
) -> tuple[int, int, int, int]:
    """The covered, pending, unreachable and unimplemented counts, one per document.

    A journey a spec declares is covered; the registry's claim cannot override
    the spec. Otherwise the registry's non-covered status decides, and a
    journey left unaccounted for is counted in none of the four -- the coverage
    finding names it.
    """
    covered = pending = unreachable = unimplemented = 0
    for journey in documents:
        if journey in declared_journeys:
            covered += 1
            continue
        entry = registry.get(journey)
        status = entry["status"] if entry is not None else None
        if status == "pending":
            pending += 1
        elif status == "unreachable":
            unreachable += 1
        elif status == "unimplemented":
            unimplemented += 1
    return covered, pending, unreachable, unimplemented


def _scan_file(relative: str, text: str) -> list[str]:
    """Return the findings in one source file's text."""
    findings: list[str] = []
    stripped = strip_comments(text)

    for label, pattern in FORBIDDEN_PATTERNS:
        for match in pattern.finditer(stripped):
            findings.append(
                f"{relative}:{line_of(stripped, match.start())}: {label}: "
                f"`{match.group(0).strip()}`"
            )

    # The one allowed CDP family: WebAuthn.* virtual-authenticator setup.
    for match in CDP_SEND_LITERAL.finditer(stripped):
        command = match.group(2)
        if not CDP_COMMAND.match(command):
            continue
        domain = command.split(".", 1)[0]
        if domain == ALLOWED_CDP_DOMAIN:
            continue
        findings.append(
            f"{relative}:{line_of(stripped, match.start())}: CDP side channel: "
            f"`send('{command}')` is outside the WebAuthn.* family"
        )
    for match in CDP_SEND_DYNAMIC.finditer(stripped):
        findings.append(
            f"{relative}:{line_of(stripped, match.start())}: CDP side channel: "
            f"`{match.group(1)}.send(...)` has a non-literal command"
        )
    return findings


def _named_specifiers(clause: str) -> list[str] | None:
    """The names in an import clause, or None when it is not a named import.

    A namespace (`* as fs`) or default (`fs`) binding cannot be proven to
    touch only read functions, so the caller treats None as opaque.
    """
    match = NAMED_SPECIFIERS.search(clause)
    if match is None:
        return None
    names: list[str] = []
    for part in match.group(1).split(","):
        name = part.strip()
        if name == "":
            continue
        names.append(re.split(r"\s+as\s+", name)[0].strip())
    return names


def _module_name(raw: str) -> str:
    return raw[5:] if raw.startswith("node:") else raw


def check_restricted_layer(relative: str, text: str) -> list[str]:
    """Layer rules for a spec or helper: no reaching around the product."""
    findings: list[str] = []
    stripped = strip_comments(text)

    for match in IMPORT_FROM.finditer(stripped):
        type_only = match.group(1) is not None
        clause = match.group(2).strip()
        raw_module = match.group(4)
        module = _module_name(raw_module)
        line = line_of(stripped, match.start())

        if module in RESTRICTED_BANNED_MODULES:
            findings.append(
                f"{relative}:{line}: layer: a spec or helper may not import "
                f"`{raw_module}`; that belongs to the fixtures' assembly layer"
            )
            continue
        if module in FS_MODULES and not type_only:
            names = _named_specifiers(clause)
            if names is None:
                findings.append(
                    f"{relative}:{line}: layer: a spec or helper may not import "
                    f"`{raw_module}` as a namespace or default binding; only "
                    "explicit read functions are allowed"
                )
            else:
                writes = [name for name in names if name in FS_WRITE_FUNCTIONS]
                if writes:
                    findings.append(
                        f"{relative}:{line}: layer: a spec or helper may not "
                        f"import filesystem write(s) {', '.join(writes)} from "
                        f"`{raw_module}`"
                    )
        if module.endswith("fixtures/log") or module.endswith("/log"):
            names = _named_specifiers(clause)
            if names is None:
                findings.append(
                    f"{relative}:{line}: layer: a spec or helper may only import "
                    "`waitForVerificationCode` from the log fixture"
                )
            else:
                forbidden = [
                    name
                    for name in names
                    if name not in ALLOWED_LOG_EXPORTS
                    and not type_only
                    and name not in ("VerificationKind", "VerificationCode")
                ]
                if forbidden:
                    findings.append(
                        f"{relative}:{line}: layer: a spec or helper may not "
                        f"import {', '.join(forbidden)} from the log fixture; "
                        "only `waitForVerificationCode` is on the import surface"
                    )

    for match in SIDE_EFFECT_IMPORT.finditer(stripped):
        module = _module_name(match.group(2))
        if module in RESTRICTED_BANNED_MODULES or module in FS_MODULES:
            findings.append(
                f"{relative}:{line_of(stripped, match.start())}: layer: a spec "
                f"or helper may not side-effect import `{match.group(2)}`"
            )

    for match in list(IMPORT_CALL.finditer(stripped)) + list(
        REQUIRE_CALL.finditer(stripped)
    ):
        raw_module = match.group(2)
        module = _module_name(raw_module)
        if (
            module in RESTRICTED_BANNED_MODULES
            or module in FS_MODULES
            or module.endswith("fixtures/log")
            or module.endswith("/log")
        ):
            findings.append(
                f"{relative}:{line_of(stripped, match.start())}: layer: a spec "
                f"or helper may not load `{raw_module}` dynamically; the "
                "import surface of a journey is fixed"
            )

    for label, pattern in (
        ("directly reads the process log", LOG_READ),
        ("waits on raw process-log lines", LOG_WAIT),
    ):
        for match in pattern.finditer(stripped):
            findings.append(
                f"{relative}:{line_of(stripped, match.start())}: layer: a spec "
                f"or helper {label}; ask `waitForVerificationCode` for a code, or "
                "the server fixture for a carrier or outbound record"
            )

    for match in PARSE_LOGFMT.finditer(stripped):
        findings.append(
            f"{relative}:{line_of(stripped, match.start())}: layer: a spec or "
            "helper may not parse logfmt lines; only fixtures/log.ts does"
        )

    return findings


def check_fixture_log_interpretation(root: str) -> list[str]:
    """Only fixtures/log.ts may interpret process-log lines."""
    findings: list[str] = []
    fixtures_dir = os.path.join(root, FIXTURES_DIR)
    log_module = os.path.join(root, LOG_MODULE)
    if not os.path.isdir(fixtures_dir):
        return findings

    for path in walk_files(fixtures_dir, SOURCE_SUFFIXES):
        if os.path.abspath(path) == os.path.abspath(log_module):
            continue
        relative = os.path.relpath(path, root)
        stripped = strip_comments(read_text(path))
        for match in PARSE_LOGFMT.finditer(stripped):
            findings.append(
                f"{relative}:{line_of(stripped, match.start())}: log "
                "interpretation: only fixtures/log.ts may parse logfmt lines"
            )
        for match in VERIFICATION_FIELD.finditer(stripped):
            findings.append(
                f"{relative}:{line_of(stripped, match.start())}: log "
                "interpretation: only fixtures/log.ts may read "
                "`verification_code`"
            )
    return findings


def check_sources(root: str) -> list[str]:
    """Scan every e2e source file for forbidden tokens and layer breaches."""
    e2e_dir = os.path.join(root, E2E_DIR)
    findings: list[str] = []
    for path in walk_files(e2e_dir, SOURCE_SUFFIXES):
        relative = os.path.relpath(path, root)
        text = read_text(path)
        findings.extend(_scan_file(relative, text))
        normalised = os.path.relpath(path, root)
        if any(
            normalised.startswith(os.path.join(layer, ""))
            for layer in RESTRICTED_LAYERS
        ):
            findings.extend(check_restricted_layer(relative, text))
    findings.extend(check_fixture_log_interpretation(root))
    return findings


def _artifact_root_is_tmp(root: str) -> bool:
    path = os.path.join(root, PATHS_MODULE)
    if not os.path.isfile(path):
        return False
    match = ARTIFACT_ROOT_DECL.search(strip_comments(read_text(path)))
    return match is not None and match.group(2).startswith("/tmp/")


def check_playwright_artifacts(root: str) -> list[str]:
    """Every Playwright artifact path must resolve under /tmp."""
    path = os.path.join(root, PLAYWRIGHT_CONFIG)
    if not os.path.isfile(path):
        return [f"{PLAYWRIGHT_CONFIG}: the Playwright config is missing"]
    text = strip_comments(read_text(path))
    matches = list(ARTIFACT_PATH.finditer(text))
    findings: list[str] = []

    if not any(match.group(1) == "outputDir" for match in matches):
        findings.append(
            f"{PLAYWRIGHT_CONFIG}: no literal outputDir; it must be a /tmp path "
            "so a run leaves no artifact in the tree"
        )

    root_is_tmp = _artifact_root_is_tmp(root)
    for match in matches:
        key, value = match.group(1), match.group(3)
        if value.startswith("/tmp/"):
            continue
        if "${ARTIFACT_ROOT}" in value and root_is_tmp:
            continue
        findings.append(
            f"{PLAYWRIGHT_CONFIG}: {key} `{value}` is not under /tmp; a run "
            "must not write its artifacts into the tree"
        )
    return findings


def _target_block(text: str, name: str) -> str | None:
    """The recipe lines of a Makefile target, or None when it is absent."""
    lines = text.splitlines()
    start = None
    for index, line in enumerate(lines):
        if re.match(rf"^{re.escape(name)}\s*:", line):
            start = index
            break
    if start is None:
        return None
    block: list[str] = []
    for line in lines[start + 1 :]:
        if re.match(r"^[A-Za-z][A-Za-z0-9_-]*\s*:", line):
            break
        block.append(line)
    return "\n".join(block)


def check_makefile(root: str) -> list[str]:
    """`e2e` exists, and `check` does not reach it by name or by command."""
    path = os.path.join(root, MAKEFILE)
    if not os.path.isfile(path):
        return [f"{MAKEFILE}: the root Makefile is missing"]
    text = read_text(path)

    findings: list[str] = []
    for target in ("e2e", "e2e-install"):
        if _target_block(text, target) is None:
            findings.append(f"{MAKEFILE}: no `{target}` target")

    check_block = _target_block(text, "check")
    if check_block is None:
        findings.append(f"{MAKEFILE}: no `check` target")
    elif CHECK_E2E_INVOCATION.search(check_block):
        findings.append(
            f"{MAKEFILE}: the `check` target invokes the e2e tier; it needs "
            "Chromium and a built backend and is not part of the commit gate"
        )
    return findings


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
        print(f"e2e-honesty: {args.root} is not a directory", file=sys.stderr)
        return 2

    journeys_dir = os.path.join(root, JOURNEYS_DIR)
    if not os.path.isdir(journeys_dir):
        print(
            f"e2e-honesty: {JOURNEYS_DIR} is missing, so there is nothing to check",
            file=sys.stderr,
        )
        return 2
    documents = journey_documents(journeys_dir)
    if not documents:
        print(
            f"e2e-honesty: {JOURNEYS_DIR} holds no journey document, so the gate "
            "checked nothing",
            file=sys.stderr,
        )
        return 2
    if not os.path.isdir(os.path.join(root, E2E_DIR)):
        print(
            f"e2e-honesty: {E2E_DIR} is missing, so the harness is gone",
            file=sys.stderr,
        )
        return 2

    try:
        registry, registry_findings = check_registry(root, documents)
        declared_journeys, spec_findings = check_specs(root, documents)
    except Infrastructure as error:
        print(f"e2e-honesty: {error}", file=sys.stderr)
        return 2

    findings: list[str] = []
    findings.extend(registry_findings)
    findings.extend(spec_findings)
    findings.extend(check_coverage(documents, declared_journeys, registry))
    findings.extend(check_sources(root))
    findings.extend(check_playwright_artifacts(root))
    findings.extend(check_makefile(root))

    for finding in findings:
        print(finding)

    covered, pending, unreachable, unimplemented = coverage_counts(
        documents, declared_journeys, registry
    )
    print(
        f"e2e-honesty: {len(documents)} journey document(s): "
        f"{covered} covered, {pending} pending, {unreachable} unreachable, "
        f"{unimplemented} unimplemented"
    )

    if findings:
        print(
            f"e2e-honesty: {len(findings)} finding(s) across {len(documents)} "
            "journey document(s)",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
