#!/usr/bin/env python3
"""Relative-reference gate for the coding-flow template copies under .github/workflows/.

The coding-flow workflow templates are byte-for-byte copies of files that
live inside the upstream skill directory (vislake/coding-flows,
.claude/skills/coding-flow/templates/workflows/), re-synced from there.
References among the skill's own files are written against the directory
they sit in -- `../../SKILL.md`, `../../references/scheduler.md` -- so a
sync can bring that shape back into a copy at any time. Copying a
template into a repository's .github/workflows/ changes the base
directory entirely: such a reference no longer resolves inside the skill
but against the repository root, a different tree that may hold a
same-named path. This gate does not judge where a relative reference
would land: a relative path segment in a copy is treated as suspected to
dangle, and the gate fails when one appears.

A reference that resolves against the consuming repository's root on
purpose (a same-repo reusable workflow, a local action) is a finding
too -- that is the criterion's deliberate trade-off, and narrowing it
takes an explicit change to this gate as its own topic, not an inline
exemption here.

A guarded file is any lowercase `.yml`/`.yaml` file directly under
.github/workflows/ that carries a `uses: vislake/coding-flows@...` line
(quoted or not): that call is what makes it a copy of a template. The
exemption is drawn by that identity and not by directory, because this
repository's own workflows may legitimately use repository-relative
paths (`uses: ./.github/workflows/reusable.yml`, a `./scripts/...` run
step). That identity is also why a fifth template lands under this gate
with nothing to maintain here.

Exit codes: 0 = every guarded file is clean; 1 = a relative reference
was found (each hit reported with file, line number and line text);
2 = infrastructure error -- no .github/workflows/ directory, no guarded
file to check, or a file that cannot be read. A gate that checked
nothing, or could not read its subject, has not passed. Paths in the
report are relative to --root.
"""

from __future__ import annotations

import argparse
import os
import re
import sys

# The line that marks a copy of an upstream template: the thin wrappers
# invoke the composite action upstream publishes. Indentation, the
# optional list dash and the quoting YAML allows around a scalar value
# are all tolerated; the ref after `@` is not part of the identity.
GUARDED_BY = re.compile(
    r"""^[ \t]*(?:-[ \t]+)?uses:[ \t]+["']?vislake/coding-flows@""",
    re.MULTILINE,
)

# A relative path segment: a `.` or `..` that begins one -- followed by
# `/` and not preceded by another `.`, so the ellipsis in `.../` (prose
# or a URL) is not a hit.
RELATIVE = re.compile(r"(?<!\.)\.\.?/")


def _infra(message: str) -> None:
    """Report an infrastructure failure and exit 2.

    The exit-code contract (module docstring): 2 = infrastructure error
    (nothing to check, or a subject that cannot be read), and only 1 =
    a relative reference found. sys.exit("message") would exit 1 --
    indistinguishable from a finding -- so the message goes to stderr
    first and the bare code is raised.
    """
    print(message, file=sys.stderr)
    raise SystemExit(2)


def _read(path: str, shown: str) -> str:
    """Read path as UTF-8, or report an infrastructure failure.

    shown is the path as the report names it. A file that cannot be read
    (gone, unreadable, or not UTF-8) must exit 2 rather than escape as a
    traceback: an uncaught exception exits 1, which would read as a
    finding.
    """
    try:
        with open(path, encoding="utf-8") as fh:
            return fh.read()
    except (OSError, UnicodeDecodeError) as exc:
        _infra(f"error: cannot read {shown}: {exc}")


def _relative_hits(text: str) -> list[tuple[int, str]]:
    """The line number and stripped text of every line holding a relative segment."""
    return [
        (lineno, line.strip())
        for lineno, line in enumerate(text.splitlines(), start=1)
        if RELATIVE.search(line)
    ]


def main(argv: list[str] | None = None) -> int:
    # The guarded files carry Chinese comments by design and the report
    # prints them back verbatim. stdout's default error handler is
    # 'strict', so on a stream that cannot encode them a write would
    # raise UnicodeEncodeError and exit 1 -- the code for a finding --
    # even on a clean tree. Escape instead, as stderr already does. This
    # precedes parse_args because argparse answers --help on stdout too.
    # (The StringIO a test redirects to has no reconfigure and takes
    # every character.)
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(errors="backslashreplace")

    parser = argparse.ArgumentParser(
        description=(
            "Fail when a coding-flow template copy under .github/workflows/ "
            "carries a relative path segment. The gate does not judge where "
            "the reference would land; an intentional repository-relative "
            "reference is a criterion trade-off to raise as its own topic, "
            "not an inline exemption."
        )
    )
    parser.add_argument(
        "--root",
        default=".",
        help="repository root to check (default: current directory); "
        "paths in the report are relative to it",
    )
    args = parser.parse_args(argv)
    root = os.path.abspath(args.root)
    if not os.path.isdir(root):
        _infra(f"error: --root is not a directory: {args.root}")

    shown_dir = ".github/workflows"
    workflows = os.path.join(root, ".github", "workflows")
    if not os.path.isdir(workflows):
        _infra(f"error: {shown_dir} is missing -- the directory this gate guards")
    try:
        entries = sorted(os.listdir(workflows))
    except OSError as exc:
        _infra(f"error: cannot list {shown_dir}: {exc}")

    guarded = 0
    hits = 0
    for name in entries:
        if not name.endswith((".yml", ".yaml")):
            # GitHub reads workflow files directly under this directory
            # only, and every upstream template is a lowercase .yml: any
            # other entry is not a copy's shape. Skipping it unread is
            # also what keeps a stray non-UTF-8 file out of the gate.
            continue
        path = os.path.join(workflows, name)
        if not os.path.isfile(path):
            # Not a file (a directory named like a workflow).
            continue
        shown = f"{shown_dir}/{name}"
        text = _read(path, shown)
        if not GUARDED_BY.search(text):
            continue
        guarded += 1
        found = _relative_hits(text)
        if not found:
            print(f"workflow-refs: ok          {shown}")
            continue
        for lineno, line in found:
            print(f"workflow-refs: RELATIVE    {shown}:{lineno}: {line}")
        hits += len(found)

    if not guarded:
        # Same rule as check_toolchain.py's exit-code contract: a gate
        # that checked nothing has not passed. A missing .github/workflows/
        # directory can only mean the gate lost its subject.
        _infra(
            f"error: no guarded file found under {shown_dir} -- a file is "
            "guarded when it is a .yml/.yaml file carrying a "
            "`uses: vislake/coding-flows@...` line, and this gate checking "
            "nothing is not a pass"
        )
    if hits:
        print(
            f"error: {hits} relative reference(s) in {guarded} guarded "
            "file(s) -- a copy's base directory is this repository, not the "
            "skill, so a relative path segment in a copy is treated as "
            "suspected to dangle. If a hit resolves against this repository's "
            "root on purpose, that is a criterion trade-off: change the "
            "criterion here as its own topic; the gate offers no inline "
            "exemption.",
            file=sys.stderr,
        )
        return 1
    print(
        f"workflow-refs: {guarded} guarded file(s) checked, "
        "0 relative reference(s)"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
