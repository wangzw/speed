#!/usr/bin/env python3
"""Test-tier gate: the build tags agree with the legs, and no library skips a case.

The cases are split into a unit layer and an integration layer by the
`integration` build tag, the commit gate runs the unit layer alone, and
the integration layer is compiled by a leg of its own. Both of those are
properties of the Makefile's legs and of the tags written into the tree,
and each fails silently when the two drift apart:

  * A tag no leg passes takes its file out of every leg at once. A file
    tagged `intergration` is excluded from `go vet ./...` and from
    `go vet -tags=integration ./...` alike, both exit 0, and `go test
    -tags=integration` reports no test files -- the cases are gone with
    nothing red anywhere. The same shape arrives the other way round: a
    leg that passes `-tags=intergration` leaves every correctly tagged
    file uncompiled. This gate holds the two to each other, reading the
    tags from the Makefile rather than restating them, so a new leg and a
    new tag arrive together or the gate says so.

    A GOOS or GOARCH term -- a `//go:build linux`, or the `_linux` of a
    file name -- is not a tag a leg passes: the platform a run executes
    on satisfies it. The platforms the gate runs on are read from the
    `runs-on` of every job under .github/workflows, each runner label
    mapped to its platform by RUNNER_PLATFORMS, and a file whose
    constraints none of them satisfies -- a Windows-only file, say -- is
    compiled by no run and is the same finding. The read is the union of
    the workflows' runners, not which job runs which leg, so a file passes
    when some runner's platform and some choice of the legs' tags satisfy
    it; while every workflow runs on one platform, that runner is the one
    every leg runs on.

  * A case that skips is not a case that ran, and `go test` exits 0 on a
    binary whose cases all skipped. The tier decision is that a backend
    which cannot be reached fails the case rather than skipping it; the
    place that ruling can be undone without anyone noticing is the
    library code a case calls into -- a fixture that returns a skip
    instead of a failure makes the layer green forever. This gate fails a
    non-test Go file that calls one of the skip methods, which is where a
    fixture's skip has to live. A test file is left alone: a case may
    skip for a reason of its own, as one does when a sibling case has
    already reported the failure it would report again.

  * A tag the commit gate only compiles is a layer that rots into not
    running. `vet-review` builds the review-tagged files and executes
    none, so deleting or breaking the code they cover leaves `make check`
    green. The Makefile lists the tags whose cases the commit gate must
    execute in GATED_TEST_TAGS; this gate holds every test file carrying
    such a tag to a test leg the `check` target reaches, which passes the
    tag and selects each of the file's cases with `-run`. A leg that
    passes the tag without `-run`, or whose pattern also selects an
    untagged case in the package, runs the unit layer a second time; a
    case outside every pattern runs nowhere. All three are findings. Tags
    absent from that roster -- the scheduled integration layer, whose leg
    deliberately runs the untagged cases too -- answer to their own legs
    and are left alone.

Exit codes: 0 = the tags and the legs agree, no library skips a case, and
every gated tag's cases are selected; 1 = at least one finding, each
reported with file, line and what it is; 2 = infrastructure error -- the
root is not a directory, the Makefile is missing or passes no tag, a
workflow names a runner whose platform this gate does not know, a
constraint does not parse, or a check found no subject at all (no tagged file, no library, or a gated tag
no test file carries). A gate that checked nothing has not passed. Paths
in the report are relative to --root.
"""

from __future__ import annotations

import argparse
import os
import re
import sys

# The directories that are not this repository's own code: the package
# manager's copy of someone else's files, and the session directories that
# carry no Go module of this repository.
SKIPPED_DIRS = frozenset({"node_modules", ".git", ".claude"})

# The prefixes Go reads a build constraint from. Both spellings are in
# scope: the modern one the toolchain requires and the one it still reads
# from files not yet rewritten.
CONSTRAINT_PREFIXES = ("//go:build ", "// +build ")

# The tag identifiers in a constraint expression: the operators (`&&`,
# `||`, `!`, parentheses) are not among them, so a plain scan for
# identifier-shaped runs is what is left.
TAG_RE = re.compile(r"[A-Za-z0-9_]+")

# The ports the go command builds for, as `go tool dist list` prints them,
# split into the operating systems and the architectures. A constraint term
# from either set is a platform the run executes on, not a tag a leg passes.
# The toolchain is not asked at gate time, which keeps this gate a plain
# script; test_check_test_tiers.py holds the two sets to the command's
# output, so a toolchain that adds a port fails that test until they agree.
GOOS = frozenset({
    "aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios", "js",
    "linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows",
})
GOARCH = frozenset({
    "386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le",
    "mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm",
})
PLATFORM_TERMS = GOOS | GOARCH

# The GitHub-hosted runner labels this gate knows, by the platform a job on
# one executes on. A workflow naming any other runs-on value -- another
# label, a list or an expression -- is an infrastructure error naming it,
# since this gate would otherwise guess the platform its legs run on.
RUNNER_PLATFORMS = {
    "ubuntu-latest": ("linux", "amd64"),
    "ubuntu-26.04": ("linux", "amd64"),
    "ubuntu-24.04": ("linux", "amd64"),
    "ubuntu-22.04": ("linux", "amd64"),
    "ubuntu-26.04-arm": ("linux", "arm64"),
    "ubuntu-24.04-arm": ("linux", "arm64"),
    "ubuntu-22.04-arm": ("linux", "arm64"),
}

# A job's runner line in a workflow file, with its trailing comment.
RUNS_ON_RE = re.compile(r"^\s*runs-on:(.*)$", re.MULTILINE)

# A token of a `//go:build` expression: an operator, a parenthesis or a term.
CONSTRAINT_TOKEN_RE = re.compile(r"\s*(&&|\|\||!|\(|\)|[A-Za-z0-9_.]+)")

# A skip method on a testing value. `Skip`, `Skipf` and `SkipNow` are the
# three `testing` declares; `Skip` alone would also match a method of some
# other type, which is why the file is held to importing `testing` as
# well.
SKIP_RE = re.compile(r"\.Skip(Now|f)?\s*\(")
# A line that imports the testing package: the one-line form, and a line
# inside an import block, with or without an alias. The quote is matched
# to the end of the line so that `"testing/fstest"` and a longer path
# beginning with the same word are not one of them.
TESTING_IMPORT_RE = re.compile(r'^\s*(?:import\s+)?(?:\w+\s+)?"testing"\s*$', re.MULTILINE)

# What a Makefile line hands a leg: the tag list of a `-tags=` flag, and
# the race detector's own always-on tag. The leading whitespace matters
# for the second: a target named `test-race` carries the text `-race`
# without passing it.
TAGS_FLAG_RE = re.compile(r"-tags=(\S+)")
RACE_FLAG_RE = re.compile(r"(?:^|\s)-race\b")

# The Makefile variable whose value names the tags the commit gate must
# execute. It is read from the file for the same reason the tags are: the
# roster has one home.
GATED_TAGS_RE = re.compile(r"^GATED_TEST_TAGS\s*:?=\s*(.*)$", re.MULTILINE)

# A target line, and the recipe under it. `:=`, `?=` and `+=` are variable
# assignments and not targets, so the colon is only a target separator when
# no assignment operator follows it.
TARGET_RE = re.compile(r"^([A-Za-z0-9_.-]+)\s*:(?!\s*[?+]?=)")
MAKE_CALL_RE = re.compile(r"\$\(MAKE\)\s+([A-Za-z0-9_.-]+)")

# A recipe that runs test binaries: `go test`, or `go -C <dir> test`. A vet
# leg compiles without running and is not one of them.
GO_TEST_RE = re.compile(r"\bgo\s+(?:-C\s+\S+\s+)?test\b")

# The `-run` flag in either spelling. The last one wins, as it does for the
# toolchain.
RUN_FLAG_RE = re.compile(r"(?:^|\s)-run(?:=|\s+)(\S+)")

# A Go test or fuzz target: a declaration `go test -run` filters on. The
# signature is read as well as the name, because a function shaped like a
# test without the testing parameter is a helper the toolchain does not run,
# and TestMain takes *testing.M and is never filtered. Benchmarks are left
# out: a run that passes no `-bench` executes none of them.
TEST_FUNC_RE = re.compile(
    r"^func\s+((?:Test|Fuzz)(?:[A-Z0-9_][A-Za-z0-9_]*)?)\s*\(\s*(?:\w+\s+)?\*testing\.(?:T|F)\s*\)",
    re.MULTILINE,
)

# An example function, which `go test -run` filters on too and which takes no
# parameter.
EXAMPLE_FUNC_RE = re.compile(
    r"^func\s+(Example(?:[A-Z0-9_][A-Za-z0-9_]*)?)\s*\(\s*\)", re.MULTILINE
)


def _infra(message: str) -> None:
    """Report an infrastructure failure and exit 2.

    The exit-code contract (module docstring): 2 = infrastructure error,
    and only 1 = a finding. SystemExit alone cannot carry the distinction
    -- sys.exit("message") exits 1 -- so the message is printed to stderr
    first and the bare code raised.
    """
    print(message, file=sys.stderr)
    raise SystemExit(2)


def walk_go_files(root: str):
    """Yield every Go file under root that belongs to this repository.

    The skipped directories are compared by name at any depth, so a copy
    of one inside a module is skipped too; the cost of that is a module
    that genuinely names a package `go`, and this tree has none.
    """
    for current, dirs, files in os.walk(root):
        dirs[:] = [d for d in dirs if d not in SKIPPED_DIRS]
        for name in files:
            if name.endswith(".go"):
                yield os.path.join(current, name)


def head_of(path: str) -> list[str]:
    """Return the lines before the package clause.

    Go reads a build constraint from the file's head, before the package
    clause, so a line after it is prose and not a constraint -- which is
    what keeps a comment quoting one out of this gate's subject.
    """
    lines = []
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        for line in fh:
            if line.startswith("package "):
                break
            lines.append(line)
    return lines


def tags_of(path: str) -> set[str]:
    """Return the tag identifiers of a file's build constraints."""
    tags: set[str] = set()
    for line in head_of(path):
        for prefix in CONSTRAINT_PREFIXES:
            if line.startswith(prefix):
                tags.update(TAG_RE.findall(line[len(prefix):]))
    return tags


def makefile_text(root: str) -> str:
    """Return the Makefile this gate reads the legs from."""
    path = os.path.join(root, "Makefile")
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            return fh.read()
    except FileNotFoundError:
        _infra(f"error: {path} is missing -- the file this gate reads the legs from")


def legs_in(text: str) -> set[str]:
    """Return the build tags the Makefile's legs establish.

    A tag reaches a leg through `-tags=`, and the race detector adds its
    own tag to any leg that passes `-race`; both are read from the file
    rather than restated here, so the vocabulary has one home.
    """
    tags: set[str] = set()
    for match in TAGS_FLAG_RE.finditer(text):
        for tag in match.group(1).split(","):
            if tag:
                tags.add(tag)
    if RACE_FLAG_RE.search(text):
        tags.add("race")
    return tags


def legs_of_makefile(root: str) -> set[str]:
    """Return the build tags the Makefile's legs establish."""
    return legs_in(makefile_text(root))


def tags_of_recipe(text: str) -> set[str]:
    """Return the build tags one leg's recipe passes with `-tags=`."""
    tags: set[str] = set()
    for match in TAGS_FLAG_RE.finditer(text):
        for tag in match.group(1).strip("'\"").split(","):
            tag = tag.strip("'\"")
            if tag:
                tags.add(tag)
    return tags


def run_pattern_of(text: str) -> str | None:
    """Return the last `-run` pattern in a recipe, or None if it has none.

    The quotes a shell or a Makefile wraps a pattern in are not part of
    what the toolchain reads, so they are stripped here.
    """
    pattern = None
    for match in RUN_FLAG_RE.finditer(text):
        pattern = match.group(1)
    return None if pattern is None else pattern.strip("'\"")


def gated_tags_of(text: str) -> list[str]:
    """Return the tags GATED_TEST_TAGS names, or an empty list if it is absent.

    A tree that gates no tag skips the third check; a tree that names one
    holds its files and its `check` target to each other.
    """
    match = GATED_TAGS_RE.search(text)
    if not match:
        return []
    value = match.group(1).split("#", 1)[0]
    return [tag for tag in value.strip().strip("'\"").split() if tag]


def targets_of(text: str) -> dict[str, str]:
    """Return each Makefile target's recipe text, keyed by target name.

    A recipe is every line under the target that starts with a tab; a
    comment, a variable assignment or a new target ends it. Lines before
    the first target belong to no recipe and are collected nowhere.
    """
    targets: dict[str, str] = {}
    current = None
    for line in text.splitlines():
        match = TARGET_RE.match(line)
        if match:
            current = match.group(1)
            targets.setdefault(current, "")
            continue
        if line.startswith("\t"):
            if current is not None:
                targets[current] += line + "\n"
            continue
        if not line.strip():
            continue
        current = None
    return targets


def reachable_from(targets: dict[str, str], start: str) -> set[str]:
    """Return every target the `start` target reaches through `$(MAKE)`.

    A sub-make is how `check` names each leg, so the walk follows exactly
    those calls; a target no reachable recipe calls is not part of the
    commit gate.
    """
    seen: set[str] = set()
    stack = [start]
    while stack:
        name = stack.pop()
        if name in seen or name not in targets:
            continue
        seen.add(name)
        stack.extend(MAKE_CALL_RE.findall(targets[name]))
    return seen


def constraint_of(path: str) -> str:
    """Return the build constraint expression of a file, or the empty string."""
    for line in head_of(path):
        for prefix in CONSTRAINT_PREFIXES:
            if line.startswith(prefix):
                return line[len(prefix):].strip()
    return ""


def platforms_of_workflows(root: str) -> set[tuple[str, str]]:
    """Return the platforms the workflows' jobs run on, as (GOOS, GOARCH) pairs.

    Every `runs-on` under .github/workflows is read and mapped through
    RUNNER_PLATFORMS. A tree with no workflow runs on nothing, so each
    platform-constrained file in it is compiled by no run.
    """
    directory = os.path.join(root, ".github", "workflows")
    platforms: set[tuple[str, str]] = set()
    if not os.path.isdir(directory):
        return platforms
    for name in sorted(os.listdir(directory)):
        if not name.endswith((".yml", ".yaml")):
            continue
        path = os.path.join(directory, name)
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            text = fh.read()
        for match in RUNS_ON_RE.finditer(text):
            label = re.split(r"\s#", match.group(1), maxsplit=1)[0].strip().strip("'\"")
            platform = RUNNER_PLATFORMS.get(label)
            if platform is None:
                _infra(
                    f"error: {os.path.relpath(path, root)}: runs-on {label!r} is not a runner "
                    "label whose platform this gate knows, so it cannot say which "
                    "platform-constrained files the workflows compile; RUNNER_PLATFORMS in "
                    "tools/check_test_tiers.py maps each label it reads"
                )
            platforms.add(platform)
    return platforms


def _bad_constraint(where: str, text: str) -> None:
    _infra(f"error: {where}: the build constraint {text!r} does not parse, so this gate cannot say which run compiles the file")


def parse_go_build(text: str, where: str):
    """Parse a `//go:build` expression into nested tuples.

    A node is ("tag", name), ("not", node), ("and", left, right) or
    ("or", left, right), with Go's precedence: `!` binds tightest, then
    `&&`, then `||`.
    """
    tokens: list[str] = []
    pos, text = 0, text.strip()
    while pos < len(text):
        match = CONSTRAINT_TOKEN_RE.match(text, pos)
        if not match:
            _bad_constraint(where, text)
        tokens.append(match.group(1))
        pos = match.end()

    def disjunction(i: int):
        node, i = conjunction(i)
        while i < len(tokens) and tokens[i] == "||":
            right, i = conjunction(i + 1)
            node = ("or", node, right)
        return node, i

    def conjunction(i: int):
        node, i = unary(i)
        while i < len(tokens) and tokens[i] == "&&":
            right, i = unary(i + 1)
            node = ("and", node, right)
        return node, i

    def unary(i: int):
        if i >= len(tokens) or tokens[i] in ("&&", "||", ")"):
            _bad_constraint(where, text)
        if tokens[i] == "!":
            inner, i = unary(i + 1)
            return ("not", inner), i
        if tokens[i] == "(":
            inner, i = disjunction(i + 1)
            if i >= len(tokens) or tokens[i] != ")":
                _bad_constraint(where, text)
            return inner, i + 1
        return ("tag", tokens[i]), i + 1

    node, end = disjunction(0)
    if end != len(tokens):
        _bad_constraint(where, text)
    return node


def parse_plus_build(lines: list[str], where: str):
    """Parse `// +build` lines into the tree parse_go_build returns.

    A space separates alternatives, a comma joins required terms, `!`
    negates one term, and the lines are all required.
    """
    node = None
    for line in lines:
        options = None
        for option in line.split():
            terms = None
            for term in option.split(","):
                negated = term.startswith("!")
                name = term[1:] if negated else term
                if not re.fullmatch(r"[A-Za-z0-9_.]+", name):
                    _bad_constraint(where, line)
                leaf = ("not", ("tag", name)) if negated else ("tag", name)
                terms = leaf if terms is None else ("and", terms, leaf)
            options = terms if options is None else ("or", options, terms)
        if options is not None:
            node = options if node is None else ("and", node, options)
    return node


def constraint_tree_of(path: str, where: str):
    """Return the parsed build constraint of a file, or None if it has none.

    A `//go:build` line is the constraint when there is one, as for the
    toolchain; the `// +build` lines are read only in a file without it.
    """
    go_build, plus_build = [], []
    for line in head_of(path):
        if line.startswith(CONSTRAINT_PREFIXES[0]):
            go_build.append(line[len(CONSTRAINT_PREFIXES[0]):])
        elif line.startswith(CONSTRAINT_PREFIXES[1]):
            plus_build.append(line[len(CONSTRAINT_PREFIXES[1]):])
    if go_build:
        return parse_go_build(go_build[0], where)
    if plus_build:
        return parse_plus_build(plus_build, where)
    return None


def terms_of(node) -> set[str]:
    """Return every term a parsed constraint names."""
    if node is None:
        return set()
    if node[0] == "tag":
        return {node[1]}
    return set().union(*(terms_of(child) for child in node[1:]))


def holds(node, true_terms: set[str]) -> bool:
    """Evaluate a parsed constraint with the given terms satisfied."""
    kind = node[0]
    if kind == "tag":
        return node[1] in true_terms
    if kind == "not":
        return not holds(node[1], true_terms)
    if kind == "and":
        return holds(node[1], true_terms) and holds(node[2], true_terms)
    return holds(node[1], true_terms) or holds(node[2], true_terms)


def platform_of_name(name: str) -> tuple[str | None, str | None]:
    """Return the (GOOS, GOARCH) a file name constrains its file to.

    The toolchain's rule: after the first underscore of the name without
    its extension, and without a trailing `_test`, a final `_GOOS_GOARCH`,
    `_GOOS` or `_GOARCH` element restricts the file to that platform. Either
    half is None when the name leaves it open.
    """
    stem = name.split(".", 1)[0]
    first = stem.find("_")
    if first < 0:
        return None, None
    parts = stem[first:].split("_")
    if parts[-1] == "test":
        parts = parts[:-1]
    if len(parts) >= 2 and parts[-2] in GOOS and parts[-1] in GOARCH:
        return parts[-2], parts[-1]
    if parts and parts[-1] in GOOS:
        return parts[-1], None
    if parts and parts[-1] in GOARCH:
        return None, parts[-1]
    return None, None


def compiled_by_some_run(
    name: str, node, platforms: set[tuple[str, str]], legs: set[str]
) -> bool:
    """Report whether some gate platform and some choice of leg tags build a file.

    A file is built on a platform its name allows when its constraint
    holds with that platform's GOOS and GOARCH satisfied and some subset of
    the tags the legs pass set; a tag no leg passes is never set.
    """
    name_os, name_arch = platform_of_name(name)
    free = sorted(terms_of(node).intersection(legs) - PLATFORM_TERMS)
    for goos, goarch in sorted(platforms):
        if name_os not in (None, goos) or name_arch not in (None, goarch):
            continue
        if node is None:
            return True
        for bits in range(1 << len(free)):
            chosen = {tag for index, tag in enumerate(free) if bits >> index & 1}
            if holds(node, chosen | {goos, goarch}):
                return True
    return False


def test_names_of(path: str) -> list[str]:
    """Return the test, fuzz and example names a file declares."""
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        text = fh.read()
    return TEST_FUNC_RE.findall(text) + EXAMPLE_FUNC_RE.findall(text)


def selects(pattern: str, name: str) -> bool:
    """Report whether a `-run` pattern selects a case name.

    The pattern is a regular expression matched against the name, and a
    slash separates the name from a subtest pattern, which is not this
    gate's subject. The match is a search, not an anchor, as in the
    toolchain; an expression Python cannot read is an infrastructure
    error rather than a finding, since this gate then judged nothing.
    """
    top = pattern.split("/", 1)[0]
    try:
        return re.search(top, name) is not None
    except re.error as exc:
        _infra(f"error: the -run pattern {pattern!r} is not a regular expression this gate can read: {exc}")


def imports_testing(path: str) -> bool:
    """Report whether a file imports the testing package."""
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        return TESTING_IMPORT_RE.search(fh.read()) is not None


def skipped_calls(path: str) -> list[tuple[int, str]]:
    """Return the skip calls of a file, as (line number, line text)."""
    found = []
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        for number, line in enumerate(fh, start=1):
            if SKIP_RE.search(line):
                found.append((number, line.strip()))
    return found


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
        print(f"test-tiers: {args.root} is not a directory", file=sys.stderr)
        return 2

    text = makefile_text(root)
    legs = legs_in(text)
    if not legs:
        _infra(
            f"error: {os.path.join(root, 'Makefile')} passes no build tag, so this gate has "
            "no vocabulary to hold the tree to -- either every leg lost its tag or this "
            "gate reads the file wrong"
        )

    platforms = platforms_of_workflows(root)
    gated = gated_tags_of(text)
    # The tagged cases a gated tag selects, by tag: a test file's constraint
    # says which build compiles it. A negated constraint excludes the file from
    # the tagged build instead of selecting it, so it is not collected here.
    gated_files: dict[str, list[tuple[str, list[str]]]] = {tag: [] for tag in gated}
    # The untagged cases, by package: a tagged leg whose -run pattern also
    # selects one of these runs the unit layer a second time.
    untagged: dict[str, set[str]] = {}

    tagged, stranding, skips, libraries = 0, 0, 0, 0
    for path in sorted(walk_go_files(root)):
        relative = os.path.relpath(path, root)
        tags = tags_of(path)
        # The tier tags are the terms a leg opts into with -tags; a GOOS or
        # GOARCH term is met by the platform the run executes on instead.
        tier_tags = tags - PLATFORM_TERMS
        name = os.path.basename(path)
        platform_bound = bool(tags & PLATFORM_TERMS) or platform_of_name(name) != (None, None)
        if tags:
            tagged += 1
        unknown = sorted(tier_tags - legs)
        if unknown:
            stranding += 1
            print(
                f"{relative}: the build tag {', '.join(unknown)} is passed by no leg, so "
                f"this file is compiled by no run; the legs pass {', '.join(sorted(legs))}"
            )
        elif platform_bound and not compiled_by_some_run(
            name, constraint_tree_of(path, relative), platforms, legs
        ):
            stranding += 1
            ran_on = ", ".join(f"{goos}/{goarch}" for goos, goarch in sorted(platforms)) or "nothing"
            print(
                f"{relative}: no platform a workflow runs on satisfies this file's build "
                f"constraint and file name, so this file is compiled by no run; the "
                f"workflows run on {ran_on}"
            )
        if path.endswith("_test.go"):
            constraint = constraint_of(path)
            selected = [] if "!" in constraint else sorted(tier_tags.intersection(gated))
            if gated and (selected or not tier_tags):
                names = test_names_of(path)
                for tag in selected:
                    gated_files[tag].append((relative, names))
                if not tier_tags:
                    untagged.setdefault(os.path.dirname(relative), set()).update(names)
        if path.endswith("_test.go") or not imports_testing(path):
            continue
        libraries += 1
        for number, line in skipped_calls(path):
            skips += 1
            print(
                f"{relative}:{number}: {line} -- a library that skips a case decides for it, "
                "and a backend that cannot be reached is a failure"
            )

    if tagged == 0:
        _infra(
            f"error: no Go file under {args.root} carries a build constraint, so the first "
            "check read nothing -- the integration layer would be gone and this gate would "
            "call that clean"
        )
    if libraries == 0:
        _infra(
            f"error: no non-test Go file importing testing was read under {args.root}, so the "
            "second check has no subject"
        )

    # The commit gate's gated tags: a test leg the check target reaches passes
    # the tag and selects every case a file under that tag declares. The leg is
    # read from the recipe rather than restated here, so a renamed target or a
    # changed pattern arrives with the case it covers.
    targets = targets_of(text)
    reached = reachable_from(targets, "check")
    gated_legs: dict[str, list[tuple[str, str | None]]] = {tag: [] for tag in gated}
    for name in sorted(reached):
        recipe = targets[name]
        if not GO_TEST_RE.search(recipe):
            continue
        for tag in sorted(tags_of_recipe(recipe).intersection(gated)):
            gated_legs[tag].append((name, run_pattern_of(recipe)))

    gating = 0
    for tag in gated:
        if not gated_files[tag]:
            _infra(
                f"error: {os.path.join(root, 'Makefile')} gates the tag {tag} but no test "
                "file carries it, so the commit gate executes no case from that layer -- a "
                "check with no subject has not passed"
            )
        legs_for_tag = gated_legs[tag]
        if not legs_for_tag:
            gating += 1
            print(
                f"Makefile: the tag {tag} is listed in GATED_TEST_TAGS, but no test leg the "
                "check target reaches passes it; the cases it selects are executed by no run "
                "in the commit gate"
            )
            continue
        for name, pattern in legs_for_tag:
            if pattern is None:
                gating += 1
                print(
                    f"Makefile: {name} passes -tags={tag} without -run, so it runs the "
                    "untagged cases as well; a -run pattern is what keeps the leg to the "
                    "tagged cases"
                )
        patterned = [(name, pattern) for name, pattern in legs_for_tag if pattern]
        if not patterned:
            # The missing -run finding above already reports this tag.
            continue
        repeated: set[tuple[str, str, str]] = set()
        for relative, names in gated_files[tag]:
            directory = os.path.dirname(relative)
            for case in names:
                if not any(selects(pattern, case) for _, pattern in patterned):
                    gating += 1
                    print(
                        f"{relative}: {case} is compiled under the tag {tag}, but no -run "
                        "pattern of a test leg the check target reaches selects it; the leg "
                        "executes no case from this file"
                    )
            for name, pattern in patterned:
                for case in sorted(untagged.get(directory, ())):
                    if (name, pattern, directory) in repeated:
                        continue
                    if selects(pattern, case):
                        repeated.add((name, pattern, directory))
                        gating += 1
                        print(
                            f"Makefile: {name}'s -run pattern {pattern!r} also selects the "
                            f"untagged case {case} in {directory}/, so the tagged leg runs "
                            "it a second time"
                        )

    if stranding or skips or gating:
        print(
            f"test-tiers: {stranding} file(s) compiled by no run, {skips} skip call(s) in "
            f"{libraries} librar(y|ies) read, {gating} commit-gate finding(s)",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
