#!/usr/bin/env python3
"""Cases for the frontend installs the workflows run outside the Makefile.

The frontend dependencies are installed in three places: the Makefile's static
phase, the check workflow's test job (which runs on its own runner), and the
setup action, before Playwright. Each install takes an explicit width -- the
workspace's shared lockfile in one pass, with --child-concurrency from
GATE_JOBS capping its child processes, and --workspace-concurrency written
alongside to name the width even though a shared-lockfile install ignores it
-- so its width is not left to pnpm's defaults. The setup action
refuses to run without the budget rather than falling back, and the scheduled
workflows that call it declare the budget themselves, as the check workflow
does.

Stdlib-only (unittest). Run directly:

    python3 tools/test_gate_width_workflows.py
"""

from __future__ import annotations

import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
WORKFLOWS = ROOT / ".github" / "workflows"
SETUP_ACTION = ROOT / ".github" / "actions" / "setup" / "action.yml"

WORKSPACE_WIDTH = "--workspace-concurrency=1"


def step_block(text: str, name: str) -> str:
    """The lines of one step, from its `- name:` to the next step's key."""
    lines = text.splitlines()
    start = next(
        (i for i, line in enumerate(lines) if line.strip() == f"- name: {name}"),
        None,
    )
    if start is None:
        return ""
    end = next(
        (
            i
            for i in range(start + 1, len(lines))
            if re.match(r"^\s*- (?:name|uses|run):", lines[i])
        ),
        len(lines),
    )
    return "\n".join(lines[start:end])


def workflow_env(text: str) -> str:
    """The workflow-level env block, from `env:` to the next top-level key."""
    lines = text.splitlines()
    start = next((i for i, line in enumerate(lines) if line == "env:"), None)
    if start is None:
        return ""
    end = next(
        (i for i in range(start + 1, len(lines)) if re.match(r"^\S", lines[i])),
        len(lines),
    )
    return "\n".join(lines[start:end])


class FrontendInstallWidthTests(unittest.TestCase):
    def test_the_check_workflow_installs_at_the_gate_width(self) -> None:
        block = step_block(
            (WORKFLOWS / "ci.yml").read_text(encoding="utf-8"),
            "Install the frontend dependencies",
        )
        self.assertIn(WORKSPACE_WIDTH, block, "ci.yml installs at a default width")
        self.assertIn(
            '--child-concurrency="${GATE_JOBS}"',
            block,
            "ci.yml install does not take its child width from GATE_JOBS",
        )

    def test_the_setup_action_installs_at_the_gate_width_and_requires_it(self) -> None:
        block = step_block(
            SETUP_ACTION.read_text(encoding="utf-8"),
            "Install the frontend dependencies Playwright runs from",
        )
        self.assertIn(WORKSPACE_WIDTH, block, "the setup action installs at a default width")
        self.assertIn(
            '--child-concurrency="${GATE_JOBS:?',
            block,
            "the setup action must refuse to run without GATE_JOBS, not fall back",
        )

    def test_the_scheduled_workflows_declare_the_budget_they_pass(self) -> None:
        for workflow in ("e2e.yml", "browser.yml"):
            with self.subTest(workflow=workflow):
                env = workflow_env((WORKFLOWS / workflow).read_text(encoding="utf-8"))
                self.assertIn(
                    'GATE_JOBS: "4"',
                    env,
                    f"{workflow} declares no workflow-level GATE_JOBS",
                )


if __name__ == "__main__":
    unittest.main()
