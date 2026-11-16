#!/usr/bin/env python3
"""Shellcheck over the `run:` scripts of the repository's composite actions.

actionlint lints workflow files only: its documentation says it does not check
the steps of a composite action, so a script in `.github/actions/*/action.yml`
reaches a runner with whatever shell defects it carries. This script closes
that gap for the shells actionlint covers. For every `run:` step of a composite
action whose `shell:` is bash or sh it runs shellcheck the way actionlint does
for a workflow step:

    shellcheck --norc -f json -x --shell <sh> \\
        -e SC1091,SC2194,SC2050,SC2153,SC2154,SC2157,SC2043 -

with `set -eo pipefail` (bash) or `set -e` (sh) prepended to the script, and
every `${{ ... }}` expression replaced by underscores of the same length, so
the shell sees a word where the expression stood and the columns stay put.
The reported line is the line of the script in the action file.

A step whose shell is anything else (pwsh, python, cmd and so on) is skipped:
shellcheck does not read it. A `run:` step with no `shell:` is a finding, since
a composite action's run step must name one.

Not a gate by name: it takes the shellcheck to run, which the Makefile's
`actionlint` target already requires on PATH, and that target runs it after
actionlint so the two linters report together.

Usage: lint_composite_actions.py [--root DIR] [--shellcheck PATH]

Exit codes: 0 = every checked script is clean; 1 = a finding (file:line:column
and the shellcheck message), or a run step without a shell; 2 = infrastructure
error: the root is not a directory, it holds no composite action, shellcheck is
not found or does not answer in the JSON format. A tool that read nothing has
not passed. Paths in the report are relative to --root.
"""

from __future__ import annotations

import argparse
import glob
import json
import os
import re
import shutil
import subprocess
import sys

# The checks actionlint switches off for a workflow script, because they fire
# on what an expression leaves behind or on what the workflow context supplies.
EXCLUDED = "SC1091,SC2194,SC2050,SC2153,SC2154,SC2157,SC2043"

PREFIX = {"bash": "set -eo pipefail\n", "sh": "set -e\n"}

EXPRESSION_RE = re.compile(r"\$\{\{.*?\}\}", re.DOTALL)
STEP_ITEM_RE = re.compile(r"^(?P<indent>\s*)-(?:\s+(?P<rest>.*))?$")
KEY_RE = re.compile(r"^(?P<indent>\s*)(?:-\s+)?(?P<key>[A-Za-z_][\w-]*):\s*(?P<value>.*)$")
BLOCK_SCALAR_RE = re.compile(r"^[|>][+-]?\d*\s*(#.*)?$")


def _infra(message: str) -> None:
    print(message, file=sys.stderr)
    raise SystemExit(2)


def action_files(root: str) -> list[str]:
    found: list[str] = []
    for pattern in ("action.yml", "action.yaml"):
        found += glob.glob(os.path.join(root, ".github", "actions", "*", pattern))
    return sorted(found)


def unquote(value: str) -> str:
    value = re.sub(r"\s+#.*$", "", value).strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
        return value[1:-1]
    return value


class Step:
    def __init__(self, line: int) -> None:
        self.line = line
        self.run: str | None = None
        self.run_line = line
        self.shell: str | None = None


def steps_of(text: str) -> list[Step]:
    """The steps of the action's `runs.steps` list, each with its `run:` text,
    the line that text starts on, and its `shell:`."""
    lines = text.splitlines()
    start = next(
        (i for i, line in enumerate(lines) if re.match(r"^\s+steps:\s*$", line)), None
    )
    if start is None:
        return []
    steps_indent = len(lines[start]) - len(lines[start].lstrip())
    steps: list[Step] = []
    item_indent: int | None = None
    current: Step | None = None
    index = start + 1
    while index < len(lines):
        line = lines[index]
        if not line.strip() or line.lstrip().startswith("#"):
            index += 1
            continue
        indent = len(line) - len(line.lstrip())
        if indent <= steps_indent:
            break
        item = STEP_ITEM_RE.match(line)
        if item and (item_indent is None or indent == item_indent):
            item_indent = indent
            current = Step(index + 1)
            steps.append(current)
        key = KEY_RE.match(line)
        if current is not None and key and (
            len(key.group("indent")) + (2 if line.lstrip().startswith("-") else 0)
            == (item_indent or 0) + 2
        ):
            name, value = key.group("key"), key.group("value").strip()
            key_indent = (item_indent or 0) + 2
            if name == "shell":
                current.shell = unquote(value)
            elif name == "run":
                if BLOCK_SCALAR_RE.match(value):
                    body: list[str] = []
                    first = index + 2
                    index += 1
                    while index < len(lines) and (
                        not lines[index].strip()
                        or len(lines[index]) - len(lines[index].lstrip()) > key_indent
                    ):
                        body.append(lines[index])
                        index += 1
                    margin = min(
                        (len(b) - len(b.lstrip()) for b in body if b.strip()), default=0
                    )
                    current.run = "\n".join(b[margin:] for b in body) + "\n"
                    current.run_line = first
                    continue
                current.run = unquote(value) + "\n"
                current.run_line = index + 1
        index += 1
    return steps


def mask_expressions(script: str) -> str:
    return EXPRESSION_RE.sub(lambda m: re.sub(r"[^\n]", "_", m.group(0)), script)


def shell_name(shell: str) -> str | None:
    """`bash` or `sh` when shellcheck reads the step's shell, else None."""
    words = shell.split()
    return words[0] if words and words[0] in PREFIX else None


def check_script(shellcheck: str, shell: str, script: str, cwd: str) -> list[dict]:
    prefix = PREFIX[shell]
    result = subprocess.run(
        [shellcheck, "--norc", "-f", "json", "-x", "--shell", shell, "-e", EXCLUDED, "-"],
        input=prefix + mask_expressions(script),
        capture_output=True,
        text=True,
        check=False,
        cwd=cwd,
    )
    if not result.stdout.strip():
        if result.returncode != 0:
            _infra(
                f"lint-composite-actions: {shellcheck} exited {result.returncode} without a "
                f"report: {result.stderr.strip()}"
            )
        return []
    try:
        comments = json.loads(result.stdout)
    except ValueError:
        _infra(
            f"lint-composite-actions: {shellcheck} did not answer in the JSON format: "
            f"{result.stdout.strip()[:200]}"
        )
    if isinstance(comments, dict):
        comments = comments.get("comments", [])
    return comments


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--root", default=os.curdir, help="the repository root (default: .)")
    parser.add_argument(
        "--shellcheck",
        default=os.environ.get("SHELLCHECK", "shellcheck"),
        help="the shellcheck executable (default: $SHELLCHECK, else shellcheck on PATH)",
    )
    args = parser.parse_args(argv)

    root = os.path.abspath(args.root)
    if not os.path.isdir(root):
        _infra(f"lint-composite-actions: {args.root} is not a directory")
    actions = action_files(root)
    if not actions:
        _infra(
            f"lint-composite-actions: no composite action under {args.root}/.github/actions, "
            "so there is nothing to lint"
        )
    if shutil.which(args.shellcheck) is None:
        _infra(
            f"lint-composite-actions: '{args.shellcheck}' is not on PATH; without it no "
            "script is checked"
        )

    findings = 0
    checked = 0
    for path in actions:
        relative = os.path.relpath(path, root)
        with open(path, encoding="utf-8") as handle:
            steps = steps_of(handle.read())
        for step in steps:
            if step.run is None:
                continue
            if not step.shell:
                findings += 1
                print(
                    f"{relative}:{step.run_line}: run step has no shell; a composite action's "
                    "run step must name one"
                )
                continue
            shell = shell_name(step.shell)
            if shell is None:
                continue
            checked += 1
            for comment in check_script(args.shellcheck, shell, step.run, root):
                findings += 1
                line = step.run_line + int(comment.get("line", 1)) - 2
                print(
                    f"{relative}:{line}:{comment.get('column', 1)}: {comment.get('level', 'error')}: "
                    f"{comment.get('message', '')} [SC{comment.get('code', 0)}]"
                )
    if findings:
        print(
            f"lint-composite-actions: {findings} finding(s) in the composite actions' scripts",
            file=sys.stderr,
        )
        return 1
    if checked == 0:
        print(
            "lint-composite-actions: no run step in bash or sh was found; nothing was checked",
            file=sys.stderr,
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
