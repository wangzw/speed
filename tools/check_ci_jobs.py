#!/usr/bin/env python3
"""CI-jobs gate: every leg of `make check` runs in the `check` workflow.

`make check` declares one segment per leg (`check-lint`, `check-test-race`,
...), and each segment's recipe calls its leg through `$(MAKE)`. The `check`
workflow splits those same legs across parallel jobs. A segment added to
`check` and not to the workflow widens the commit gate beyond what CI runs:
the commit waits for a local gate nobody repeats on the runner, and the
coverage gap is invisible because both sides pass.

The gate reads the leg each `check-*` prerequisite of `check` calls, reads
every `make` command in `.github/workflows/ci.yml`, and holds the two sets
equal. A leg narrowed with `MODULES=` runs part of that leg's module loop, so
the union of a leg's `MODULES=` values across the jobs must cover exactly the
modules `go.work` lists; a job that runs the leg without `MODULES=` covers
the whole roster.

The workflow is read as text with the shape ci.yml has: job keys under
`jobs:`, and each job's `run:` value either inline or a block scalar. A make
command is a word `make` at a shell boundary, its target words are the bare
words after it, and a `MODULES=` assignment narrows it.

A leg's coverage is not enough: the run must also fail when the leg fails.
Two settings would undo that. The workflow-level `env:` must set `GATE_JOBS`
and a `GOFLAGS` that contains `-count=1`, so the tools take the intended
width and a cached test result is never replayed. And a job that runs a
`check` leg must neither set `continue-on-error` (any value but `false`) nor
carry an `if:` that can skip it; `always()`, bare or in `${{ }}`, is the one
condition allowed because it never skips the job. The same holds one level
down: a step whose `run:` invokes a `check` leg must not set
`continue-on-error` and must not carry an `if:` that can skip it, because a
tolerated or skipped step lets the leg fail without failing the run.

Exit codes: 0 clean, 1 a finding (a leg no job runs, a target that is no
segment, a module no job covers, the workflow env missing `GATE_JOBS` or
`-count=1`, or a gate job or gate step that could let a failure pass), 2
infrastructure error -- the root is not a directory, or the Makefile, the
workflow or go.work is missing or names nothing to compare.
"""

from __future__ import annotations

import argparse
import os
import posixpath
import re
import sys

MAKEFILE = "Makefile"
WORKFLOW = os.path.join(".github", "workflows", "ci.yml")
GO_WORK = "go.work"

TARGET_RE = re.compile(r"^([A-Za-z0-9_.-]+)\s*:(?!\s*[?+]?=)(.*)$")
PREREQUISITE_RE = re.compile(r"[A-Za-z0-9_.-]+")
NAME_RE = re.compile(r"[A-Za-z0-9_.-]+")

# A segment's leg is named in its recipe's sub-make call; flags before the
# target are make's own (`--no-print-directory`), not the leg's name.
MAKE_CALL_RE = re.compile(r"\$\(MAKE\)\s+(.*)")

# A shell word `make`, not a path or a longer word containing it. What
# follows runs to the end of the line; shell operators only produce words
# that are not target names, which the target filter drops.
MAKE_COMMAND_RE = re.compile(r"(?<![\w./-])make\s+([^\n]+)")

MODULES_RE = re.compile(r"""MODULES=(?:"([^"]*)"|'([^']*)'|(\S*))""")


def makefile_rules(text: str) -> tuple[dict[str, list[str]], dict[str, str]]:
    """Return each target's prerequisites and recipe text.

    A prerequisite list may be wrapped with a trailing backslash; the list is
    one statement to make, so it is one statement here. `|` introduces
    order-only prerequisites, which still order a build and stay in the list.
    """
    prerequisites: dict[str, list[str]] = {}
    recipes: dict[str, str] = {}
    current = None
    for line in text.replace("\\\n", " ").splitlines():
        match = TARGET_RE.match(line)
        if match:
            current = match.group(1)
            value = match.group(2).split("#", 1)[0].replace("|", " ")
            prerequisites.setdefault(current, []).extend(
                name for name in PREREQUISITE_RE.findall(value) if not name.startswith("$")
            )
            recipes.setdefault(current, "")
            continue
        if line.startswith("\t"):
            if current is not None:
                recipes[current] += line + "\n"
            continue
        if not line.strip():
            continue
        current = None
    return prerequisites, recipes


def call_targets(rest: str) -> list[str]:
    """The target words of a make command, after its flags and assignments."""
    targets: list[str] = []
    for token in rest.split():
        token = token.strip("\"'")
        if token.startswith("-") or "$" in token or "=" in token:
            continue
        if NAME_RE.fullmatch(token):
            targets.append(token)
    return targets


def segments(prerequisites: dict[str, list[str]], recipes: dict[str, str]) -> dict[str, list[str]]:
    """Map each `check-*` prerequisite of `check` to the legs its recipe calls."""
    mapping: dict[str, list[str]] = {}
    for name in prerequisites.get("check", []):
        if not name.startswith("check-"):
            continue
        calls: list[str] = []
        for match in MAKE_CALL_RE.finditer(recipes.get(name, "")):
            calls.extend(call_targets(match.group(1)))
        mapping[name] = calls
    return mapping


def workflow_jobs(text: str) -> dict[str, list[tuple[int, str]]]:
    """Return each job's `run:` scripts as (first line number, script) pairs."""
    lines = text.splitlines()
    start = next((i for i, line in enumerate(lines) if line.rstrip() == "jobs:"), None)
    if start is None:
        return {}
    jobs: dict[str, list[tuple[int, str]]] = {}
    current = None
    for index in range(start + 1, len(lines)):
        line = lines[index]
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if not line.startswith(" "):
            break
        job = re.match(r"^  ([A-Za-z0-9_-]+):\s*$", line)
        if job:
            current = job.group(1)
            jobs[current] = []
            continue
        if current is None:
            continue
        run = re.match(r"^(\s*)(-\s+)?run:\s*(.*)$", line)
        if run is None:
            continue
        indent = len(run.group(1)) + len(run.group(2) or "")
        value = run.group(3).strip()
        if re.fullmatch(r"[|>][+-]?\d*", value):
            body: list[str] = []
            following = index + 1
            while following < len(lines):
                entry = lines[following]
                if entry.strip() and (len(entry) - len(entry.lstrip())) <= indent:
                    break
                body.append(entry.strip())
                following += 1
            jobs[current].append((index + 2, "\n".join(body)))
        elif value and not value.startswith("#"):
            jobs[current].append((index + 1, value.strip("\"'")))
    return jobs


def workflow_env(text: str) -> dict[str, str]:
    """Return the workflow-level `env:` mapping.

    The top-level `env:` block is read; a job's own `env:` and a step's `env:`
    are indented, so they are not the workflow's. A value may be inline or a
    block scalar, whose body is joined with newlines.
    """
    lines = text.splitlines()
    # A job's or a step's `env:` is indented; a top-level `env:` is not.
    start = next((i for i, line in enumerate(lines) if line.rstrip() == "env:"), None)
    if start is None:
        return {}
    env: dict[str, str] = {}
    index = start + 1
    while index < len(lines):
        line = lines[index]
        if not line.strip() or line.lstrip().startswith("#"):
            index += 1
            continue
        if not line.startswith(" "):
            break
        match = re.match(r"^  ([A-Za-z_][A-Za-z0-9_]*):\s*(.*)$", line)
        if not match:
            index += 1
            continue
        key, value = match.group(1), match.group(2).strip()
        if re.fullmatch(r"[|>][+-]?\d*", value):
            body: list[str] = []
            index += 1
            while index < len(lines):
                entry = lines[index]
                if entry.strip() and (len(entry) - len(entry.lstrip())) <= 2:
                    break
                body.append(entry.strip())
                index += 1
            env[key] = "\n".join(body)
            continue
        env[key] = value.strip("\"'")
        index += 1
    return env


def job_attributes(text: str) -> dict[str, dict[str, str]]:
    """Return each job's own `if:` and `continue-on-error:` values.

    Only keys at the job's indent are read; a step's `if:` and
    `continue-on-error:` sit deeper and are not the job's.
    """
    lines = text.splitlines()
    start = next((i for i, line in enumerate(lines) if line.rstrip() == "jobs:"), None)
    if start is None:
        return {}
    attrs: dict[str, dict[str, str]] = {}
    current = None
    for index in range(start + 1, len(lines)):
        line = lines[index]
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if not line.startswith(" "):
            break
        job = re.match(r"^  ([A-Za-z0-9_-]+):\s*$", line)
        if job:
            current = job.group(1)
            attrs[current] = {}
            continue
        if current is None:
            continue
        attr = re.match(r"^    (if|continue-on-error):\s*(.*)$", line)
        if attr:
            attrs[current][attr.group(1)] = attr.group(2).strip().strip("\"'")
    return attrs


def step_key(
    step: dict[str, object],
    key: str,
    value: str,
    lines: list[str],
    index: int,
    attribute_indent: int,
) -> int:
    """Record one of a step's keys and return the index after it.

    `run:` is the one key kept as a script: an inline command on the key's own
    line, or a block scalar whose body is deeper than the step's keys. Every
    other key is stored as it was written. Reading every key, rather than a
    fixed set, is what finds a leg on the dash line itself: `- run: make lint`
    carries its command there, not on a line below it.
    """
    if key == "run":
        if re.fullmatch(r"[|>][+-]?\d*", value):
            body: list[str] = []
            following = index + 1
            while following < len(lines):
                entry = lines[following]
                if entry.strip() and (len(entry) - len(entry.lstrip())) <= attribute_indent:
                    break
                body.append(entry.strip())
                following += 1
            step["scripts"].append((index + 2, "\n".join(body)))
            return following
        if value and not value.startswith("#"):
            step["scripts"].append((index + 1, value.strip("\"'")))
        return index + 1
    step[key] = value.strip("\"'")
    return index + 1


def workflow_steps(text: str) -> dict[str, list[dict[str, object]]]:
    """Return each job's steps with their own keys and runs.

    A step is an item of the job's `steps:` sequence; its keys sit one level
    deeper than the `- ` dash and its block-scalar `run:` body deeper still.
    A job's own `if:` and `continue-on-error:` sit above the sequence and are
    not a step's, so the two levels cannot be confused. The dash line's first
    key is read whatever it is, and every key at the step's own level is
    recorded, with `run:` kept in a `scripts` list of (first line number,
    script) entries.
    """
    lines = text.splitlines()
    start = next((i for i, line in enumerate(lines) if line.rstrip() == "jobs:"), None)
    if start is None:
        return {}
    jobs: dict[str, list[dict[str, object]]] = {}
    current: str | None = None
    step: dict[str, object] | None = None
    step_indent: int | None = None
    attribute_indent: int | None = None
    index = start + 1
    while index < len(lines):
        line = lines[index]
        if not line.strip() or line.lstrip().startswith("#"):
            index += 1
            continue
        if not line.startswith(" "):
            break
        job = re.match(r"^  ([A-Za-z0-9_-]+):\s*$", line)
        if job:
            current = job.group(1)
            jobs[current] = []
            step, step_indent, attribute_indent = None, None, None
            index += 1
            continue
        if current is None:
            index += 1
            continue
        indent = len(line) - len(line.lstrip())
        steps_key = re.match(r"^(\s*)steps:\s*$", line)
        if steps_key:
            step_indent = len(steps_key.group(1)) + 2
            step, attribute_indent = None, None
            index += 1
            continue
        dash = re.match(r"^(\s*)- (\S.*)$", line)
        if step_indent is not None and dash is not None and indent == step_indent:
            step = {"scripts": []}
            jobs[current].append(step)
            attribute_indent = indent + 2
            first_key = re.match(r"^([A-Za-z0-9_-]+):\s*(.*)$", dash.group(2))
            if first_key is None:
                index += 1
                continue
            index = step_key(
                step,
                first_key.group(1),
                first_key.group(2).strip(),
                lines,
                index,
                attribute_indent,
            )
            continue
        if step is None or attribute_indent is None:
            index += 1
            continue
        if indent < attribute_indent:
            step, attribute_indent = None, None
            index += 1
            continue
        if indent > attribute_indent:
            index += 1
            continue
        match = re.match(r"^\s*([A-Za-z0-9_-]+):\s*(.*)$", line)
        if match is None:
            index += 1
            continue
        index = step_key(
            step,
            match.group(1),
            match.group(2).strip(),
            lines,
            index,
            attribute_indent,
        )
    return jobs


def safe_condition(condition: str) -> bool:
    """A condition that never skips the job: `always()`, bare or in `${{ }}`."""
    text = condition.strip()
    if text.startswith("${{") and text.endswith("}}"):
        text = text[3:-2].strip()
    return text == "always()"


def gate_env_findings(workflow: str) -> int:
    """Print a finding for each missing workflow-level gate setting."""
    env = workflow_env(workflow)
    findings = 0
    if "GATE_JOBS" not in env:
        findings += 1
        print(f"ci-jobs: {WORKFLOW}: the workflow-level env sets no GATE_JOBS")
    if "-count=1" not in env.get("GOFLAGS", "").split():
        findings += 1
        print(
            f"ci-jobs: {WORKFLOW}: the workflow-level GOFLAGS does not contain "
            "-count=1, so a cached test result could be replayed"
        )
    return findings


def gate_job_findings(workflow: str, gate_jobs: set[str]) -> int:
    """Print a finding for each gate job that could let a failure pass."""
    attrs = job_attributes(workflow)
    findings = 0
    for job in sorted(gate_jobs):
        values = attrs.get(job, {})
        continue_on_error = values.get("continue-on-error")
        if continue_on_error is not None and continue_on_error.lower() != "false":
            findings += 1
            print(
                f"ci-jobs: {WORKFLOW}: job {job} sets "
                f"continue-on-error: {continue_on_error}, so its failure would "
                "not fail the run"
            )
        condition = values.get("if")
        if condition is not None and condition and not safe_condition(condition):
            findings += 1
            print(
                f"ci-jobs: {WORKFLOW}: job {job} runs only under if: {condition}, "
                "which can skip a gate segment"
            )
    return findings


def gate_step_findings(workflow: str, gate_legs: set[str]) -> int:
    """Print a finding for each gate step that could let a failure pass."""
    findings = 0
    for job, steps in workflow_steps(workflow).items():
        for step in steps:
            gate = False
            line = 0
            for first, script in step["scripts"]:
                for offset, text in enumerate(script.splitlines()):
                    if text.strip().startswith("#"):
                        continue
                    for command in MAKE_COMMAND_RE.finditer(text):
                        if any(target in gate_legs for target in call_targets(command.group(1))):
                            gate, line = True, first + offset
                            break
                    if gate:
                        break
                if gate:
                    break
            if not gate:
                continue
            name = step.get("name")
            where = f"job {job} step {name}" if name else f"job {job} step at line {line}"
            continue_on_error = step.get("continue-on-error")
            if continue_on_error is not None and str(continue_on_error).lower() != "false":
                findings += 1
                print(
                    f"ci-jobs: {WORKFLOW}: {where} sets "
                    f"continue-on-error: {continue_on_error}, so its failure would "
                    "not fail the run"
                )
            condition = step.get("if")
            if condition and not safe_condition(str(condition)):
                findings += 1
                print(
                    f"ci-jobs: {WORKFLOW}: {where} runs only under if: {condition}, "
                    "which can skip a gate segment"
                )
    return findings


def invocations(jobs: dict[str, list[tuple[int, str]]]) -> list[tuple[str, str | None, list[str] | None, int]]:
    """Return (job, target, modules, line) for every make target ci.yml names.

    `modules` is None when the command carries no `MODULES=` assignment, which
    means the leg runs its whole module loop; a present but empty assignment
    is an empty list, which covers nothing and is a finding.
    """
    found: list[tuple[str, str | None, list[str] | None, int]] = []
    for job, scripts in jobs.items():
        for first, script in scripts:
            for offset, line in enumerate(script.splitlines()):
                if line.strip().startswith("#"):
                    continue
                for command in MAKE_COMMAND_RE.finditer(line):
                    rest = command.group(1)
                    modules: list[str] | None = None
                    if MODULES_RE.search(rest):
                        modules = []
                        for match in MODULES_RE.finditer(rest):
                            value = next(group for group in match.groups() if group is not None)
                            modules.extend(word for word in value.split() if word)
                    for target in call_targets(rest):
                        found.append((job, target, modules, first + offset))
    return found


def go_work_modules(text: str) -> list[str]:
    """Return the module paths a go.work file lists."""
    modules: list[str] = []
    in_use = False
    for raw in text.splitlines():
        line = raw.split("//", 1)[0].strip()
        if not line:
            continue
        if in_use:
            if line == ")":
                in_use = False
            else:
                entry = line.strip("\"'")
                if entry:
                    modules.append(entry)
            continue
        block = re.match(r"^use\s*\((.*)$", line)
        if block:
            remainder = block.group(1).strip()
            if remainder:
                modules.append(remainder.strip("\"'"))
            else:
                in_use = True
            continue
        single = re.match(r"^use\s+(.+)$", line)
        if single:
            modules.append(single.group(1).strip().strip("\"'"))
    return modules


def normalise(path: str) -> str:
    """A module path in the form both go.work and a MODULES= value write."""
    return posixpath.normpath(path.strip().strip("\"'"))


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--root",
        default=os.curdir,
        help="the tree to read (default: the current directory)",
    )
    args = parser.parse_args(argv)

    root = os.path.abspath(args.root)
    if not os.path.isdir(root):
        print(f"ci-jobs: {args.root} is not a directory", file=sys.stderr)
        return 2
    paths = {
        "Makefile": os.path.join(root, MAKEFILE),
        "workflow": os.path.join(root, WORKFLOW),
        "go.work": os.path.join(root, GO_WORK),
    }
    for label, path in paths.items():
        if not os.path.isfile(path):
            print(f"ci-jobs: no {label} under {args.root}", file=sys.stderr)
            return 2

    with open(paths["Makefile"], encoding="utf-8") as handle:
        prerequisites, recipes = makefile_rules(handle.read())
    if "check" not in prerequisites:
        print(f"ci-jobs: {MAKEFILE} has no `check` target", file=sys.stderr)
        return 2
    segment_legs = segments(prerequisites, recipes)
    if not segment_legs:
        print(f"ci-jobs: `check` names no `check-*` segment", file=sys.stderr)
        return 2
    for name, legs in segment_legs.items():
        if not legs:
            print(f"ci-jobs: {name} calls no leg through $(MAKE)", file=sys.stderr)
            return 2

    with open(paths["workflow"], encoding="utf-8") as handle:
        workflow_text = handle.read()
    jobs = workflow_jobs(workflow_text)
    if not jobs:
        print(f"ci-jobs: {WORKFLOW} declares no job", file=sys.stderr)
        return 2
    calls = invocations(jobs)
    if not calls:
        print(f"ci-jobs: {WORKFLOW} runs no make target", file=sys.stderr)
        return 2

    leg_of: dict[str, list[str]] = {}
    for name, legs in segment_legs.items():
        for leg in legs:
            leg_of.setdefault(leg, []).append(name)
    check_legs = set(leg_of)
    ci_legs = {target for _, target, _, _ in calls}
    gate_jobs = {job for job, target, _, _ in calls if target in check_legs}

    findings = 0
    findings += gate_env_findings(workflow_text)
    findings += gate_job_findings(workflow_text, gate_jobs)
    findings += gate_step_findings(workflow_text, check_legs)
    for leg in sorted(check_legs - ci_legs):
        findings += 1
        print(
            f"ci-jobs: {WORKFLOW}: no job runs `{leg}`, the leg of "
            f"{', '.join(sorted(leg_of[leg]))}"
        )
    for leg in sorted(ci_legs - check_legs):
        jobs_named = sorted({job for job, target, _, _ in calls if target == leg})
        findings += 1
        print(
            f"ci-jobs: {WORKFLOW}: job {', '.join(jobs_named)} runs `{leg}`, "
            "which no `check` segment runs"
        )

    with open(paths["go.work"], encoding="utf-8") as handle:
        roster = [normalise(module) for module in go_work_modules(handle.read())]
    roster_set = set(roster)
    if not roster_set:
        print(f"ci-jobs: {GO_WORK} lists no module", file=sys.stderr)
        return 2

    narrowed: dict[str, list[tuple[str, list[str], int]]] = {}
    full: set[str] = set()
    for job, target, modules, line in calls:
        if target not in check_legs:
            continue
        if modules is None:
            full.add(target)
            continue
        narrowed.setdefault(target, []).append((job, modules, line))
    for leg in sorted(narrowed):
        if leg in full:
            continue
        covered: set[str] = set()
        for job, modules, line in narrowed[leg]:
            if not modules:
                findings += 1
                print(
                    f"ci-jobs: {WORKFLOW}:{line}: job {job} runs `{leg}` with "
                    "MODULES= naming no module"
                )
                continue
            covered.update(normalise(module) for module in modules)
        for module in sorted(roster_set - covered):
            findings += 1
            print(
                f"ci-jobs: {WORKFLOW}: job {', '.join(sorted({job for job, _, _ in narrowed[leg]}))} "
                f"runs `{leg}` on part of the roster; go.work module {module} is covered by no job"
            )
        for module in sorted(covered - roster_set):
            findings += 1
            print(
                f"ci-jobs: {WORKFLOW}: job {', '.join(sorted({job for job, _, _ in narrowed[leg]}))} "
                f"runs `{leg}` with MODULES={module}, which go.work does not list"
            )

    if findings:
        print(
            f"ci-jobs: {findings} finding(s); the jobs must run exactly the legs "
            "`check` runs, over exactly the modules go.work lists",
            file=sys.stderr,
        )
        return 1
    print(f"ci-jobs: {len(jobs)} job(s) run the {len(check_legs)} leg(s) of `check`")
    return 0


if __name__ == "__main__":
    sys.exit(main())
