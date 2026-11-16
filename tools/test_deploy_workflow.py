#!/usr/bin/env python3
"""Cases for .github/workflows/deploy.yml and the setup action it uses.

The workflow ships the tip of main and nothing else. A dispatch on another ref
must fail loudly rather than end with every job skipped, and the job that
deploys installs go alone. The cases read the workflow text, run the refusal
script the way the runner does, and hold the conditions that keep the other
jobs on main.

Stdlib-only (unittest, subprocess). Run directly:

    python3 tools/test_deploy_workflow.py
"""

from __future__ import annotations

import os
import pathlib
import re
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
WORKFLOW = ROOT / ".github" / "workflows" / "deploy.yml"
SETUP_ACTION = ROOT / ".github" / "actions" / "setup" / "action.yml"


def job_block(text: str, name: str) -> str:
    """The lines of one job under `jobs:`, from its key to the next job's."""
    lines = text.splitlines()
    start = next(i for i, line in enumerate(lines) if line == f"  {name}:")
    end = next(
        (i for i in range(start + 1, len(lines)) if re.match(r"^  \S", lines[i])),
        len(lines),
    )
    return "\n".join(lines[start:end])


def condition_of(block: str) -> str:
    """The job's `if:` expression, folded onto one line."""
    lines = block.splitlines()
    start = next(i for i, line in enumerate(lines) if line.startswith("    if:"))
    first = lines[start].split("if:", 1)[1].strip()
    if first not in (">-", ">", "|", "|-"):
        return first
    folded: list[str] = []
    for line in lines[start + 1 :]:
        if not line.startswith("      "):
            break
        folded.append(line.strip())
    return " ".join(folded)


def run_script_of(block: str) -> str:
    """The body of the first block-scalar `run:` in a job."""
    lines = block.splitlines()
    start = next(i for i, line in enumerate(lines) if re.match(r"^\s+run: \|\s*$", line))
    indent = len(lines[start]) - len(lines[start].lstrip())
    body: list[str] = []
    for line in lines[start + 1 :]:
        if line.strip() and len(line) - len(line.lstrip()) <= indent:
            break
        body.append(line[indent + 2 :] if line.strip() else "")
    return "\n".join(body) + "\n"


class TestADispatchOutsideMainFails(unittest.TestCase):
    def setUp(self) -> None:
        self.text = WORKFLOW.read_text(encoding="utf-8")
        self.refuse = job_block(self.text, "refuse")

    def test_the_refuse_job_runs_only_for_a_dispatch_off_main(self) -> None:
        self.assertEqual(
            "github.event_name == 'workflow_dispatch' && github.ref != 'refs/heads/main'",
            condition_of(self.refuse),
        )

    def test_the_refuse_job_is_small_and_on_the_pinned_runner(self) -> None:
        self.assertIn("runs-on: ubuntu-26.04", self.refuse)
        self.assertIn("timeout-minutes: 5", self.refuse)
        self.assertNotIn("uses:", self.refuse, "the refusal needs no action and no checkout")
        self.assertNotIn("secrets.", self.refuse)

    def test_the_refusal_script_exits_non_zero_and_names_the_ref(self) -> None:
        script = run_script_of(self.refuse)
        env = {"PATH": os.environ["PATH"], "REF": "refs/heads/x"}
        result = subprocess.run(
            ["bash", "-e", "-c", script], env=env, capture_output=True, text=True, check=False
        )
        self.assertNotEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertIn(
            "::error::deploy ships the tip of main only: dispatched on refs/heads/x; "
            "dispatch it on main",
            result.stdout.splitlines(),
        )

    def test_the_ref_reaches_the_script_through_env(self) -> None:
        # An expression written into the script text would be code to the
        # shell; a branch name is the dispatcher's to choose.
        self.assertIn("REF: ${{ github.ref }}", self.refuse)
        self.assertNotIn("${{", run_script_of(self.refuse))

    def test_decide_still_requires_main_for_a_dispatch(self) -> None:
        condition = condition_of(job_block(self.text, "decide"))
        self.assertIn(
            "github.event_name == 'workflow_dispatch' && github.ref == 'refs/heads/main'",
            condition,
        )
        self.assertIn("github.event_name == 'workflow_run'", condition)

    def test_no_job_but_refuse_runs_for_a_dispatch_off_main(self) -> None:
        # deploy follows decide, which a dispatch off main does not satisfy.
        deploy = job_block(self.text, "deploy")
        self.assertIn("needs: decide", deploy)
        self.assertIn("needs.decide.outputs.is_tip == 'true'", condition_of(deploy))


class TestTheDeployJobInstallsGoAlone(unittest.TestCase):
    def setUp(self) -> None:
        self.deploy = job_block(WORKFLOW.read_text(encoding="utf-8"), "deploy")
        self.action = SETUP_ACTION.read_text(encoding="utf-8")

    def test_the_deploy_job_asks_the_setup_action_for_go_only(self) -> None:
        step = re.search(
            r"uses: \./\.github/actions/setup\n\s+with:\n((?:\s+\S.*\n)+?)(?=\n|\s+- |\s+# )",
            self.deploy + "\n",
        )
        self.assertIsNotNone(step, "the deploy job no longer calls the setup action with inputs")
        self.assertEqual("tools: go", step.group(1).strip())

    def test_the_setup_action_passes_the_named_tools_to_mise(self) -> None:
        self.assertRegex(self.action, r"(?m)^  tools:\n")
        self.assertIn("install_args: ${{ inputs.tools }}", self.action)

    def test_the_setup_action_installs_everything_when_no_tool_is_named(self) -> None:
        # The default stays the whole of .mise.toml, which every other job
        # relies on: the one install step runs unconditionally, and an empty
        # install_args is mise-action's own default.
        step = re.search(
            r"(?s)- name: Install the toolchain from \.mise\.toml\n(.*?)(?=\n    #|\n    - name:|\Z)",
            self.action,
        )
        self.assertIsNotNone(step, "the setup action has no toolchain install step")
        self.assertNotIn("if:", step.group(1))
        self.assertIn("uses: jdx/mise-action@", step.group(1))

    def test_a_named_subset_does_not_leave_the_rest_reported_as_missing(self) -> None:
        # The step that limits mise runs before the install and only when
        # tools are named; the comma list is built by the shell because the
        # expression language has no replace function.
        step = re.search(
            r"- name: Limit mise to the named tools\n\s+if: inputs\.tools != ''\n(.*?)(?=\n    - name:)",
            self.action,
            re.DOTALL,
        )
        self.assertIsNotNone(step, "no step limits mise to the named tools")
        self.assertIn("TOOLS: ${{ inputs.tools }}", step.group(1))
        self.assertIn('MISE_ENABLE_TOOLS=${TOOLS// /,}" >> "${GITHUB_ENV}"', step.group(1))
        self.assertLess(
            self.action.index("Limit mise to the named tools"),
            self.action.index("Install the toolchain from .mise.toml"),
        )

    def test_the_limiting_script_turns_a_space_list_into_a_comma_list(self) -> None:
        match = re.search(r'run: (echo "MISE_ENABLE_TOOLS=.*)', self.action)
        self.assertIsNotNone(match)
        with tempfile.TemporaryDirectory() as scratch:
            out = pathlib.Path(scratch) / "env"
            result = subprocess.run(
                ["bash", "-e", "-c", match.group(1)],
                env={"PATH": os.environ["PATH"], "TOOLS": "go node", "GITHUB_ENV": str(out)},
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual("MISE_ENABLE_TOOLS=go,node\n", out.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
