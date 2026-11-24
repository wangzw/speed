#!/usr/bin/env python3
"""Fixup-commit gate for the commits a branch carries beyond main."""

from __future__ import annotations

import argparse
import os
import subprocess
import sys

PREFIXES = ("fixup! ", "squash! ", "amend! ")
BASES = ("origin/main", "main")


def git(root: str, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", "-C", root, *args],
        capture_output=True,
        text=True,
        check=False,
    )


def resolves(root: str, ref: str) -> bool:
    return git(root, "rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}").returncode == 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--root",
        default=os.curdir,
        help="the repository to check (default: the current directory)",
    )
    args = parser.parse_args(argv)

    root = os.path.abspath(args.root)
    if not os.path.isdir(root):
        print(f"fixup-commits: nothing checked, {args.root} is not a directory")
        return 0

    inside = git(root, "rev-parse", "--is-inside-work-tree")
    if inside.returncode != 0 or inside.stdout.strip() != "true":
        print(f"fixup-commits: nothing checked, {args.root} is not inside a git work tree")
        return 0

    base = next((ref for ref in BASES if resolves(root, ref)), None)
    if base is None:
        print("fixup-commits: nothing checked, neither origin/main nor main resolves here")
        return 0
    if not resolves(root, "HEAD"):
        print("fixup-commits: nothing checked, HEAD resolves to no commit")
        return 0

    log = git(root, "log", "--format=%h%x09%s", f"{base}..HEAD")
    if log.returncode != 0:
        print(f"fixup-commits: nothing checked, git log {base}..HEAD failed")
        return 0

    hits = []
    for line in log.stdout.splitlines():
        sha, _, subject = line.partition("\t")
        if subject.startswith(PREFIXES):
            hits.append((sha, subject))

    for sha, subject in hits:
        print(f"{sha} {subject}")
    if hits:
        print(
            f"fixup-commits: {len(hits)} fixup/squash/amend commit(s) in {base}..HEAD; "
            f"run `git rebase --autosquash {BASES[1]}` to fold them in before landing",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
