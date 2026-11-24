#!/usr/bin/env python3
"""Web build-script guard: every workspace package builds only itself."""

from __future__ import annotations

import argparse
import glob
import json
import os
import re
import sys

WORKSPACE_FILE = os.path.join("web", "pnpm-workspace.yaml")

BUILD_SELF = "tsc -p tsconfig.build.json"

# The members the build gate does not call `build:self` on: the shared test
# support, whose build is its typecheck, and the demo host, which builds
# itself through its own script (starters/demo/web/package.json).
EXEMPT = frozenset({"@speed/test-utils", "demo-web"})


def workspace_manifests(workspace_root: str) -> list[str]:
    """The package.json paths of the members `web/pnpm-workspace.yaml` lists.

    The member globs are the `- <glob>` entries under `packages:`, read up to
    the next key; each resolves against the workspace root, as pnpm resolves
    it, so a member outside the root (`../...`) counts like any other. A glob
    entry with no manifest beneath it lists no member.
    """
    with open(os.path.join(workspace_root, "pnpm-workspace.yaml"), encoding="utf-8") as handle:
        lines = handle.read().splitlines()
    start = next((i for i, line in enumerate(lines) if line.strip() == "packages:"), None)
    manifests: list[str] = []
    if start is None:
        return manifests
    for line in lines[start + 1 :]:
        entry = re.match(r"^\s+-\s+(\S+)", line)
        if entry is None:
            if line.strip() and not line.strip().startswith("#"):
                break
            continue
        for member in sorted(glob.glob(os.path.join(workspace_root, entry.group(1)))):
            manifest = os.path.join(member, "package.json")
            if os.path.isfile(manifest):
                manifests.append(manifest)
    return manifests


def expected_build(name: str) -> str:
    return (
        f"pnpm --filter {name}... --fail-if-no-match "
        "--workspace-concurrency=1 run build:self"
    )


def judge(name: str, scripts: dict) -> list[tuple[str | None, str]]:
    """The problems of one member's scripts, each with the script key holding
    it, or None when the key is absent."""
    if name in EXEMPT:
        return []
    problems: list[tuple[str | None, str]] = []

    build_self = scripts.get("build:self")
    if not isinstance(build_self, str):
        problems.append(
            (
                None,
                "missing build:self; pnpm -r run build:self skips the package silently",
            )
        )
    elif build_self != BUILD_SELF:
        problems.append(
            ("build:self", f"build:self is {build_self!r}, expected {BUILD_SELF!r}")
        )

    build = scripts.get("build")
    expected = expected_build(name)
    if not isinstance(build, str):
        problems.append((None, f"missing build, expected {expected!r}"))
    elif build != expected:
        problems.append(("build", f"build is {build!r}, expected {expected!r}"))
    return problems


def line_of(text: str, key: str) -> int:
    needle = json.dumps(key)
    for number, line in enumerate(text.splitlines(), start=1):
        if line.strip().startswith(needle):
            return number
    return 1


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
        print(f"web-build-scripts: {args.root} is not a directory", file=sys.stderr)
        return 2
    workspace = os.path.join(root, WORKSPACE_FILE)
    if not os.path.isfile(workspace):
        print(f"web-build-scripts: no {WORKSPACE_FILE} under {args.root}", file=sys.stderr)
        return 2
    try:
        manifests = workspace_manifests(os.path.dirname(workspace))
    except (OSError, UnicodeDecodeError) as exc:
        print(f"web-build-scripts: cannot read {WORKSPACE_FILE}: {exc}", file=sys.stderr)
        return 2
    if not manifests:
        print(
            f"web-build-scripts: {WORKSPACE_FILE} lists no member with a readable manifest",
            file=sys.stderr,
        )
        return 2

    hits = 0
    for manifest in manifests:
        relative = os.path.relpath(manifest, root)
        try:
            with open(manifest, encoding="utf-8") as handle:
                text = handle.read()
            data = json.loads(text)
        except (OSError, UnicodeDecodeError, ValueError) as exc:
            print(f"web-build-scripts: cannot read {relative}: {exc}", file=sys.stderr)
            return 2
        name = data.get("name") if isinstance(data, dict) else None
        if not isinstance(name, str):
            print(f"web-build-scripts: {relative} names no package", file=sys.stderr)
            return 2
        scripts = data.get("scripts")
        if not isinstance(scripts, dict):
            scripts = {}
        for key, problem in judge(name, scripts):
            hits += 1
            line = line_of(text, key) if key is not None else 1
            print(f"{relative}:{line}: {name}: {problem}")
    if hits:
        print(
            f"web-build-scripts: {hits} build-script problem(s); every workspace member "
            f"except {', '.join(sorted(EXEMPT))} needs build:self = {BUILD_SELF!r} and "
            "build = the pnpm filter form the gate calls",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
