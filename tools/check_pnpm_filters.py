#!/usr/bin/env python3
"""pnpm-filter gate: a filter that matches nothing must fail the command.

When every `--filter` selector of a pnpm command misses, pnpm exits 0 with
"No projects matched the filters" and runs nothing. A renamed package then
turns a build, a typecheck or a test run into a silent no-op, and the leg
that ran it stays green. `--fail-if-no-match` makes the same command exit 1,
but only when it stands before the command word (`pnpm --filter x
--fail-if-no-match build`): after the script name pnpm hands it to the
script, where it filters nothing.

The gate reads every place a pnpm command is written and holds each call that
carries `--filter` (or `-F`, `--filter-prod`) to two rules:

- `--fail-if-no-match` appears among the options before the command word;
- every selector names a workspace member: an exact package name, a glob that
  matches at least one member, or a path selector. The `...` and `^` graph
  markers and a leading `!` are removed before the name is compared. A
  selector naming a package the workspace does not list is the miss the flag
  exists to catch, and the flag does not excuse it: a call with the flag
  still fails at run time, so the gate reports it at review time.

Members are the package names of the directories web/pnpm-workspace.yaml
lists. The sources are Makefile recipes, Dockerfile `RUN` instructions (line
continuations joined, `&&` and `;` split), the `scripts` of every
package.json, and the `run:` blocks of workflows and composite actions.

Exit codes: 0 = every filtered call is guarded; 1 = at least one is not, each
reported as file:line with the call; 2 = infrastructure error -- the root is
not a directory, or the workspace file lists no member. Paths in the report
are relative to --root.
"""

from __future__ import annotations

import argparse
import fnmatch
import glob
import json
import os
import re
import shlex
import sys

WORKSPACE_FILE = os.path.join("web", "pnpm-workspace.yaml")

FLAG = "--fail-if-no-match"

# Options that take their value as the next word. The `=` forms need no entry.
VALUE_OPTIONS = {"-C", "--dir", "--filter", "-F", "--filter-prod", "--config", "--reporter"}
FILTER_OPTIONS = {"--filter", "-F", "--filter-prod"}
FILTER_PREFIXES = ("--filter=", "--filter-prod=")

# Directories that hold nobody's writing for this gate.
SKIPPED_DIRS = {".git", "node_modules", "__pycache__", ".cache"}

SEPARATORS = {"&&", "||", ";", "|", "&", "|&", ";;"}


def workspace_package_names(workspace_root: str) -> set[str]:
    """The package.json names of the members a pnpm-workspace.yaml lists.

    The member globs are the `- <glob>` entries under `packages:`, read up
    to the next key; each glob resolves against the workspace root, as pnpm
    resolves it, and a member outside the root (`../...`) counts like any
    other.
    """
    with open(os.path.join(workspace_root, "pnpm-workspace.yaml"), encoding="utf-8") as handle:
        lines = handle.read().splitlines()
    start = next((i for i, line in enumerate(lines) if line.strip() == "packages:"), None)
    names: set[str] = set()
    if start is None:
        return names
    for line in lines[start + 1 :]:
        entry = re.match(r"^\s+-\s+(\S+)", line)
        if entry is None:
            if line.strip() and not line.strip().startswith("#"):
                break
            continue
        for member in glob.glob(os.path.join(workspace_root, entry.group(1))):
            manifest = os.path.join(member, "package.json")
            if os.path.isfile(manifest):
                with open(manifest, encoding="utf-8") as handle:
                    names.add(json.load(handle)["name"])
    return names


def tokens_of(text: str) -> list[str]:
    try:
        return shlex.split(text, comments=False, posix=True)
    except ValueError:
        return text.split()


def commands_in(text: str) -> list[list[str]]:
    """Split one logical line into the commands it chains."""
    commands: list[list[str]] = []
    current: list[str] = []
    for token in tokens_of(text):
        trailing = False
        if token.endswith(";") and len(token) > 1:
            token, trailing = token[:-1], True
        if token in SEPARATORS:
            if current:
                commands.append(current)
            current = []
            continue
        current.append(token)
        if trailing:
            commands.append(current)
            current = []
    if current:
        commands.append(current)
    return commands


def pnpm_call(command: list[str]) -> list[str] | None:
    """The words after the `pnpm` word of a command, or None without one."""
    for index, word in enumerate(command):
        bare = word.lstrip("@-+").removeprefix("$(").removeprefix("(").lstrip("`")
        if bare == "pnpm" or bare.endswith("/pnpm"):
            return command[index + 1 :]
    return None


def selectors_and_guard(call: list[str]) -> tuple[list[str], bool, bool]:
    """The filter selectors of a call, whether the flag stands before the
    command word, and whether the call carries the flag at all."""
    selectors: list[str] = []
    guarded = False
    anywhere = FLAG in call
    index = 0
    while index < len(call):
        word = call[index]
        if not word.startswith("-"):
            break
        if word == FLAG:
            guarded = True
        elif word in FILTER_OPTIONS:
            if index + 1 < len(call):
                selectors.append(call[index + 1])
            index += 1
        elif word.startswith(FILTER_PREFIXES):
            selectors.append(word.split("=", 1)[1])
        elif word in VALUE_OPTIONS:
            index += 1
        index += 1
    return selectors, guarded, anywhere


def is_member_selector(selector: str, members: set[str]) -> bool:
    name = selector.removeprefix("!")
    name = re.sub(r"^\.\.\.\^?", "", name)
    name = re.sub(r"\^?\.\.\.$", "", name)
    if not name:
        return False
    if name.startswith((".", "/", "{")) or "{" in name:
        return True
    if any(char in name for char in "*?["):
        return any(fnmatch.fnmatchcase(member, name) for member in members)
    return name in members


def judge(call: list[str], members: set[str]) -> list[str]:
    selectors, guarded, anywhere = selectors_and_guard(call)
    if not selectors:
        return []
    problems: list[str] = []
    if not guarded:
        where = "after the command word" if anywhere else "missing"
        problems.append(
            f"{FLAG} is {where}; pnpm applies it only before the command word"
        )
    for selector in selectors:
        if not is_member_selector(selector, members):
            problems.append(f"selector {selector!r} names no workspace member")
    return problems


def logical_lines(text: str) -> list[tuple[int, str]]:
    """The lines of a text with backslash continuations joined, each with the
    number of the line it starts on."""
    joined: list[tuple[int, str]] = []
    pending: str | None = None
    start = 0
    for number, line in enumerate(text.splitlines(), start=1):
        if pending is None:
            start = number
            pending = ""
        pending += line
        if pending.endswith("\\"):
            pending = pending[:-1] + " "
            continue
        joined.append((start, pending))
        pending = None
    if pending is not None:
        joined.append((start, pending))
    return joined


def makefile_scripts(text: str) -> list[tuple[int, str]]:
    return [
        (number, line.strip())
        for number, line in logical_lines(text)
        if line.startswith("\t") and not line.strip().startswith("#")
    ]


def dockerfile_scripts(text: str) -> list[tuple[int, str]]:
    scripts: list[tuple[int, str]] = []
    kept = "\n".join(
        "" if line.lstrip().startswith("#") else line for line in text.splitlines()
    )
    for number, line in logical_lines(kept):
        match = re.match(r"^\s*RUN\s+(?!\[)(.*)$", line, re.IGNORECASE)
        if match:
            scripts.append((number, match.group(1)))
    return scripts


def package_json_scripts(text: str) -> list[tuple[int, str]]:
    try:
        manifest = json.loads(text)
    except ValueError:
        return []
    scripts = manifest.get("scripts") if isinstance(manifest, dict) else None
    if not isinstance(scripts, dict):
        return []
    found: list[tuple[int, str]] = []
    lines = text.splitlines()
    for name, value in scripts.items():
        if not isinstance(value, str):
            continue
        key = json.dumps(name)
        number = next((i for i, line in enumerate(lines, start=1) if line.strip().startswith(key)), 1)
        found.append((number, value))
    return found


RUN_KEY = re.compile(r"^(?P<indent>\s*)(?:-\s+)?run:\s*(?P<value>.*)$")


def yaml_run_blocks(text: str) -> list[tuple[int, str]]:
    """The `run:` values of a workflow or composite action, each with the line
    its script starts on. A block scalar is read up to the first line indented
    no deeper than its key."""
    lines = text.splitlines()
    blocks: list[tuple[int, str]] = []
    index = 0
    while index < len(lines):
        match = RUN_KEY.match(lines[index])
        if match is None:
            index += 1
            continue
        key_indent = len(match.group("indent")) + (2 if lines[index].lstrip().startswith("-") else 0)
        value = match.group("value").strip()
        if re.match(r"^[|>][+-]?\d*\s*(#.*)?$", value):
            body: list[str] = []
            first = index + 2
            index += 1
            while index < len(lines) and (
                not lines[index].strip() or len(lines[index]) - len(lines[index].lstrip()) > key_indent
            ):
                body.append(lines[index].strip())
                index += 1
            blocks.append((first, "\n".join(body)))
            continue
        if value and not value.startswith("#"):
            blocks.append((index + 1, value.strip("\"'")))
        index += 1
    return blocks


def sources(root: str) -> list[tuple[str, list[tuple[int, str]]]]:
    found: list[tuple[str, list[tuple[int, str]]]] = []
    for directory, subdirs, files in os.walk(root):
        relative_dir = os.path.relpath(directory, root)
        subdirs[:] = sorted(
            d
            for d in subdirs
            if d not in SKIPPED_DIRS
            and os.path.normpath(os.path.join(relative_dir, d)) != os.path.join(".claude", "worktrees")
        )
        for name in sorted(files):
            path = os.path.join(directory, name)
            relative = os.path.relpath(path, root)
            parts = relative.split(os.sep)
            reader = None
            if name == "Makefile" or name.endswith(".mk"):
                reader = makefile_scripts
            elif name == "Dockerfile" or name.startswith("Dockerfile.") or name.endswith(".Dockerfile"):
                reader = dockerfile_scripts
            elif name == "package.json":
                reader = package_json_scripts
            elif (
                parts[:2] == [".github", "workflows"] and len(parts) == 3 and name.endswith((".yml", ".yaml"))
            ) or (
                parts[:2] == [".github", "actions"] and len(parts) == 4 and name in ("action.yml", "action.yaml")
            ):
                reader = yaml_run_blocks
            if reader is None or os.path.islink(path):
                continue
            try:
                with open(path, encoding="utf-8") as handle:
                    found.append((relative, reader(handle.read())))
            except (OSError, UnicodeDecodeError):
                continue
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
        print(f"pnpm-filters: {args.root} is not a directory", file=sys.stderr)
        return 2
    workspace = os.path.join(root, WORKSPACE_FILE)
    if not os.path.isfile(workspace):
        print(f"pnpm-filters: no {WORKSPACE_FILE} under {args.root}", file=sys.stderr)
        return 2
    members = workspace_package_names(os.path.dirname(workspace))
    if not members:
        print(
            f"pnpm-filters: {WORKSPACE_FILE} lists no member with a package name, so no "
            "selector can be checked against it",
            file=sys.stderr,
        )
        return 2

    hits = 0
    for relative, scripts in sources(root):
        for number, script in scripts:
            for offset, line in logical_lines(script):
                for command in commands_in(line):
                    call = pnpm_call(command)
                    if call is None:
                        continue
                    for problem in judge(call, members):
                        hits += 1
                        print(f"{relative}:{number + offset - 1}: pnpm {' '.join(call)}: {problem}")
    if hits:
        print(
            f"pnpm-filters: {hits} unguarded or unresolved filter(s); a filter that matches "
            f"nothing exits 0 unless {FLAG} stands before the command word",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
