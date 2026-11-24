#!/usr/bin/env python3
"""Conflict-marker gate for every text file in the tree."""

from __future__ import annotations

import argparse
import os
import re
import sys

# The three markers git labels: seven characters, a space, then the ref
# or name the merge wrote. A run of seven alone is not one of them --
# `<<<<<<<` with nothing after it does not occur in a real conflict.
LABELED = re.compile(r"^(?:<<<<<<< |>>>>>>> |\|\|\|\|\|\|\| )")

BARE_MIDDLE = re.compile(r"^=======$")

SKIPPED_DIRS = {".git", "node_modules", "__pycache__"}

# How much of a file is read to decide whether it is text. A NUL byte in
# the first block is what `file` and git both treat as the signal.
SNIFF = 8192


def is_text(path: str) -> bool:
    """Report whether a file holds UTF-8 text rather than binary.

    The read is bounded and the answer is a byte test, not a decode: a
    marker is seven ASCII characters, so a file that decodes as UTF-8 is
    worth scanning and one that does not is not.
    """
    try:
        with open(path, "rb") as handle:
            head = handle.read(SNIFF)
    except OSError:
        return False
    if b"\0" in head:
        return False
    try:
        head.decode("utf-8")
    except UnicodeDecodeError:
        return False
    return True


def walk(root: str) -> list[str]:
    """List every candidate file under root, in a stable order."""
    found: list[str] = []
    for directory, subdirs, files in os.walk(root):
        subdirs[:] = sorted(d for d in subdirs if d not in SKIPPED_DIRS)
        for name in sorted(files):
            path = os.path.join(directory, name)
            if os.path.islink(path) or not os.path.isfile(path):
                continue
            found.append(path)
    return found


def markers_in(path: str) -> list[tuple[int, str]]:
    """Return the conflict markers in one file, as (line number, text)."""
    try:
        with open(path, encoding="utf-8") as handle:
            lines = handle.read().splitlines()
    except (OSError, UnicodeDecodeError):
        return []

    labeled = [
        (number, line) for number, line in enumerate(lines, start=1) if LABELED.match(line)
    ]
    if labeled:
        labeled += [
            (number, line)
            for number, line in enumerate(lines, start=1)
            if BARE_MIDDLE.match(line)
        ]
        labeled.sort()
    return labeled


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
        print(f"conflict-markers: {args.root} is not a directory", file=sys.stderr)
        return 2

    scanned = 0
    hits = 0
    for path in walk(root):
        if not is_text(path):
            continue
        scanned += 1
        for number, line in markers_in(path):
            hits += 1
            print(f"{os.path.relpath(path, root)}:{number}: {line}")

    if scanned == 0:
        print(
            f"conflict-markers: no text file found under {args.root}, so the scan read "
            "nothing there",
            file=sys.stderr,
        )
        return 2

    if hits:
        print(
            f"conflict-markers: {hits} marker line(s) in {scanned} file(s) scanned; a "
            "committed marker is content, and git status will not show it",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
