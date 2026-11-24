#!/usr/bin/env python3
"""Test-tier gate: the build tags agree with the legs, and no library skips a case."""

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

# The operating-system and architecture names go/build treats as platforms: its
# KnownOS and KnownArch, in $GOROOT/src/internal/syslist/syslist.go. A file-name
# element from either set restricts the file to that platform, so the sets are
# wider than the ports `go tool dist list` prints; they include names no port
# builds for any more (nacl, amd64p32) and names no port has yet (hurd, zos,
# ppc, riscv). A constraint term from either set is a platform the run executes
# on, not a tag a leg passes. The toolchain is not asked at gate time, which
# keeps this gate a plain script; test_check_test_tiers.py holds the two sets to
# syslist.go in the pinned toolchain and to the ports it builds for, so a
# toolchain that adds a name fails that test until they agree.
GOOS = frozenset({
    "aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios",
    "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1",
    "windows", "zos",
})
GOARCH = frozenset({
    "386", "amd64", "amd64p32", "arm", "armbe", "arm64", "arm64be", "loong64",
    "mips", "mipsle", "mips64", "mips64le", "mips64p32", "mips64p32le", "ppc",
    "ppc64", "ppc64le", "riscv", "riscv64", "s390", "s390x", "sparc", "sparc64",
    "wasm",
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

# A target line, and the recipe under it. `:=`, `?=` and `+=` are variable
# assignments and not targets, so the colon is only a target separator when
# no assignment operator follows it.
TARGET_RE = re.compile(r"^([A-Za-z0-9_.-]+)\s*:(?!\s*[?+]?=)(.*)$")

# A sub-make call and the goal it names. A goal may be preceded by flags, and
# `--no-print-directory` is one this Makefile passes on every sub-make, so the
# flags are skipped rather than read as the goal.
MAKE_CALL_RE = re.compile(r"\$\(MAKE\)(?:\s+--?[A-Za-z0-9-]+)*\s+([A-Za-z0-9_.-]+)")

# A prerequisite that names a target. A variable reference (`$(MAKE)`) or the
# order-only separator is not one; the separator is dropped before the split,
# and a reference is skipped because this parser does not expand variables.
PREREQUISITE_RE = re.compile(r"[A-Za-z0-9_.-]+")


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


def prerequisites_of(text: str) -> dict[str, list[str]]:
    """Return each target's prerequisite names, in the order written.

    Prerequisites are how `check` names its segments, so the walk in
    reachable_from needs them as well as the recipes. `|` introduces
    order-only prerequisites, which still order a build and are kept; a
    variable reference is skipped, since this parser does not expand
    variables and a name it cannot read is not one it should invent.
    """
    prerequisites: dict[str, list[str]] = {}
    current = None
    # A prerequisite list may be wrapped with a trailing backslash; the list is
    # one statement to make, so it is one statement here too.
    for line in text.replace("\\\n", " ").splitlines():
        match = TARGET_RE.match(line)
        if match:
            current = match.group(1)
            value = match.group(2).split("#", 1)[0].replace("|", " ")
            prerequisites[current] = [
                name for name in PREREQUISITE_RE.findall(value) if not name.startswith("$")
            ]
            continue
        if line.startswith("\t") or not line.strip():
            continue
        current = None
    return prerequisites


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

    if stranding or skips:
        print(
            f"test-tiers: {stranding} file(s) compiled by no run, {skips} skip call(s) in "
            f"{libraries} librar(y|ies) read",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
