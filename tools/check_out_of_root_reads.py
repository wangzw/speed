#!/usr/bin/env python3
"""Out-of-root read gate for the demo server's tests."""

from __future__ import annotations

import argparse
import os
import pathlib
import re
import sys

SCOPE = "starters/demo/server"
VARIABLE = "UNCACHED_PACKAGES"

JOIN = re.compile(r"filepath\.Join\s*\(")
ASSIGNMENT = re.compile(rf"^{VARIABLE}\s*\?=\s*(.*)$", re.MULTILINE)


def _scan(source: str) -> tuple[list[tuple[int, int, int, str]], str]:
    """Return every string literal and a copy with comments blanked out.

    Each literal is (start, end, line, value); the copy keeps offsets so a
    filepath.Join can be located in it.
    """
    literals: list[tuple[int, int, int, str]] = []
    code = list(source)
    i, line, size = 0, 1, len(source)
    while i < size:
        char = source[i]
        if char == "\n":
            line += 1
            i += 1
        elif source.startswith("//", i):
            end = source.find("\n", i)
            end = size if end < 0 else end
            for k in range(i, end):
                code[k] = " "
            i = end
        elif source.startswith("/*", i):
            end = source.find("*/", i + 2)
            end = size if end < 0 else end + 2
            for k in range(i, end):
                if source[k] != "\n":
                    code[k] = " "
            line += source.count("\n", i, end)
            i = end
        elif char == '"':
            start = i
            i += 1
            value: list[str] = []
            while i < size and source[i] != '"':
                if source[i] == "\\" and i + 1 < size:
                    escape = source[i + 1]
                    value.append(escape if escape in ('"', "\\") else "\\" + escape)
                    i += 2
                else:
                    value.append(source[i])
                    i += 1
            i = min(i + 1, size)
            literals.append((start, i, line, "".join(value)))
        elif char == "`":
            start = i
            i += 1
            while i < size and source[i] != "`":
                i += 1
            end = min(i + 1, size)
            value = source[start + 1 : end - 1] if end - 1 > start else ""
            literals.append((start, end, line, value))
            line += source.count("\n", start, end)
            i = end
        elif char == "'":
            start = i
            i += 1
            while i < size and source[i] != "'":
                i += 2 if source[i] == "\\" else 1
            i = min(i + 1, size)
            for k in range(start, i):
                if source[k] != "\n":
                    code[k] = " "
        else:
            i += 1
    return literals, "".join(code)


def _blank(text: str, spans: list[tuple[int, int]]) -> str:
    chars = list(text)
    for start, end in spans:
        for k in range(start, end):
            if chars[k] != "\n":
                chars[k] = " "
    return "".join(chars)


def _join_paths(
    source: str, code: str, literals: list[tuple[int, int, int, str]]
) -> tuple[list[tuple[int, str]], set[int]]:
    """Return each all-literal filepath.Join as (line, path), and its literals.

    A Join with a non-literal argument falls to the per-literal pass, so its
    literal arguments are still checked.
    """
    skeleton = _blank(code, [(start, end) for start, end, _, _ in literals])
    joined: list[tuple[int, str]] = []
    used: set[int] = set()
    for match in JOIN.finditer(skeleton):
        open_at = match.end() - 1
        depth, close_at = 1, open_at + 1
        while close_at < len(skeleton) and depth:
            if skeleton[close_at] == "(":
                depth += 1
            elif skeleton[close_at] == ")":
                depth -= 1
            close_at += 1
        if depth:
            continue
        close_at -= 1
        segments: list[tuple[int, int]] = []
        start, nesting = open_at + 1, 0
        for k in range(open_at + 1, close_at):
            if skeleton[k] in "([{":
                nesting += 1
            elif skeleton[k] in ")]}":
                nesting -= 1
            elif skeleton[k] == "," and nesting == 0:
                segments.append((start, k))
                start = k + 1
        segments.append((start, close_at))
        values: list[str] = []
        tokens: list[tuple[int, int, int, str]] = []
        for segment_start, segment_end in segments:
            stripped = source[segment_start:segment_end].strip()
            if not stripped:
                continue
            found = [
                token
                for token in literals
                if segment_start <= token[0] and token[1] <= segment_end
            ]
            if len(found) != 1 or source[found[0][0] : found[0][1]] != stripped:
                values = []
                tokens = []
                break
            values.append(found[0][3])
            tokens.append(found[0])
        if values and tokens:
            line = source.count("\n", 0, match.start()) + 1
            joined.append((line, "/".join(values)))
            used.update(token[0] for token in tokens)
    return joined, used


def _escapes(package: str, path: str) -> bool:
    """Report whether path, read from package, resolves outside the module root."""
    resolved = os.path.normpath(os.path.join(package, path))
    return resolved == ".." or resolved.startswith(".." + os.sep)


def _uncached_entries(makefile: pathlib.Path) -> list[str] | None:
    if not makefile.is_file():
        return None
    match = ASSIGNMENT.search(makefile.read_text(encoding="utf-8"))
    if match is None:
        return None
    return match.group(1).split()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--root",
        default=os.curdir,
        help="the repository to check (default: the current directory)",
    )
    args = parser.parse_args(argv)

    root = pathlib.Path(args.root).resolve()
    if not root.is_dir():
        print(f"out-of-root-reads: {args.root} is not a directory", file=sys.stderr)
        return 2

    entries = _uncached_entries(root / "Makefile")
    if entries is None:
        print(
            f"out-of-root-reads: {root / 'Makefile'} has no {VARIABLE} ?= assignment",
            file=sys.stderr,
        )
        return 2

    scope = root / SCOPE
    if not scope.is_dir():
        print(f"out-of-root-reads: {SCOPE} is not a directory", file=sys.stderr)
        return 2
    files = sorted(scope.rglob("*.go"))
    if not files:
        print(f"out-of-root-reads: no Go file under {SCOPE}", file=sys.stderr)
        return 2

    findings: list[str] = []
    uncached: set[pathlib.Path] = set()
    for entry in entries:
        path = pathlib.Path(entry)
        path = path if path.is_absolute() else root / path
        uncached.add(path.resolve())
        if not path.is_dir():
            findings.append(f"{entry}: listed in {VARIABLE} but is not a directory")
        elif not any(path.glob("*.go")):
            findings.append(f"{entry}: listed in {VARIABLE} but carries no Go file")

    for path in files:
        if path.parent.resolve() in uncached:
            continue
        relative = path.relative_to(root).as_posix()
        package = path.parent.resolve().relative_to(scope.resolve()).as_posix()
        source = path.read_text(encoding="utf-8")
        literals, code = _scan(source)
        joined, used = _join_paths(source, code, literals)
        candidates = list(joined)
        candidates.extend(
            (line, value) for start, _, line, value in literals if start not in used
        )
        reported: set[int] = set()
        for line, value in sorted(candidates):
            if line in reported or not _escapes(package, value):
                continue
            reported.add(line)
            findings.append(f"{relative}:{line}: {value!r} leaves {SCOPE}")

    for finding in findings:
        print(finding)
    if findings:
        print(
            f"out-of-root-reads: {len(findings)} finding(s); a read outside the "
            f"module root belongs in a package listed in {VARIABLE}",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
