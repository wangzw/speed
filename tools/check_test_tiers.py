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

Exit codes: 0 = the tags and the legs agree and no library skips a case;
1 = at least one finding, each reported with file, line and what it is;
2 = infrastructure error -- the root is not a directory, the Makefile is
missing or passes no tag, or a check found no subject at all. A gate that
checked nothing has not passed. Paths in the report are relative to
--root.
"""

from __future__ import annotations

import argparse
import os
import re
import sys

# The directories that are not this repository's own: the earlier
# implementations, which `go.work` does not list and the Makefile does not
# build, and the package manager's copy of someone else's files. A build
# tag written in one of them is that tree's business.
SKIPPED_DIRS = frozenset({"go", "web-legacy", "node_modules", ".git", ".claude"})

# The prefixes Go reads a build constraint from. Both spellings are in
# scope: the modern one the toolchain requires and the one it still reads
# from files not yet rewritten.
CONSTRAINT_PREFIXES = ("//go:build ", "// +build ")

# The tag identifiers in a constraint expression: the operators (`&&`,
# `||`, `!`, parentheses) are not among them, so a plain scan for
# identifier-shaped runs is what is left.
TAG_RE = re.compile(r"[A-Za-z0-9_]+")

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


def legs_of_makefile(root: str) -> set[str]:
    """Return the build tags the Makefile's legs establish.

    A tag reaches a leg through `-tags=`, and the race detector adds its
    own tag to any leg that passes `-race`; both are read from the file
    rather than restated here, so the vocabulary has one home.
    """
    path = os.path.join(root, "Makefile")
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            text = fh.read()
    except FileNotFoundError:
        _infra(f"error: {path} is missing -- the file this gate reads the legs from")

    tags: set[str] = set()
    for match in TAGS_FLAG_RE.finditer(text):
        for tag in match.group(1).split(","):
            if tag:
                tags.add(tag)
    if RACE_FLAG_RE.search(text):
        tags.add("race")
    return tags


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

    legs = legs_of_makefile(root)
    if not legs:
        _infra(
            f"error: {os.path.join(root, 'Makefile')} passes no build tag, so this gate has "
            "no vocabulary to hold the tree to -- either every leg lost its tag or this "
            "gate reads the file wrong"
        )

    tagged, stranding, skips, libraries = 0, 0, 0, 0
    for path in sorted(walk_go_files(root)):
        relative = os.path.relpath(path, root)
        tags = tags_of(path)
        if tags:
            tagged += 1
            unknown = sorted(tags - legs)
            if unknown:
                stranding += 1
                print(
                    f"{relative}: the build tag {', '.join(unknown)} is passed by no leg, so "
                    f"this file is compiled by no run; the legs pass {', '.join(sorted(legs))}"
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
            f"test-tiers: {stranding} file(s) tagged by no leg, {skips} skip call(s) in "
            f"{libraries} librar(y|ies) read",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
