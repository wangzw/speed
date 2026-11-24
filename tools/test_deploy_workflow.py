#!/usr/bin/env python3
"""Cases for .github/workflows/deploy.yml and the setup action it uses."""

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
    """The block's first `if:` expression, folded onto one line.

    A job block's own `if:` sits above its steps, so it is the first one found;
    a step block's `if:` is found the same way. A folded scalar's continuation
    lines are deeper than the `if:` key.
    """
    lines = block.splitlines()
    start = next(i for i, line in enumerate(lines) if re.match(r"^\s+if:", line))
    indent = len(lines[start]) - len(lines[start].lstrip())
    first = lines[start].split("if:", 1)[1].strip()
    if first not in (">-", ">", "|", "|-"):
        return first
    folded: list[str] = []
    for line in lines[start + 1 :]:
        if line.strip() and (len(line) - len(line.lstrip())) <= indent:
            break
        folded.append(line.strip())
    return " ".join(folded)


def run_script_of(block: str) -> str:
    """The body of the first block-scalar `run:` in a job."""
    lines = block.splitlines()
    start = next(i for i, line in enumerate(lines) if re.match(r"^\s+run: \|\s*$", line))
    return script_from(lines, start)


def step_block(job: str, name: str) -> str:
    """The lines of one `- name:` step in a job, up to the next step."""
    lines = job.splitlines()
    start = next(i for i, line in enumerate(lines) if line.strip() == f"- name: {name}")
    end = next(
        (i for i in range(start + 1, len(lines)) if re.match(r"^\s+- name: ", lines[i])),
        len(lines),
    )
    return "\n".join(lines[start:end])


def script_from(lines: list[str], start: int) -> str:
    """The body of the block-scalar `run:` whose header is at `start`."""
    indent = len(lines[start]) - len(lines[start].lstrip())
    body: list[str] = []
    for line in lines[start + 1 :]:
        if line.strip() and len(line) - len(line.lstrip()) <= indent:
            break
        body.append(line[indent + 2 :] if line.strip() else "")
    return "\n".join(body) + "\n"


def run_script_of_step(step: str) -> str:
    """The body of the first block-scalar `run:` in a step."""
    lines = step.splitlines()
    start = next(i for i, line in enumerate(lines) if re.match(r"^\s+run: \|\s*$", line))
    return script_from(lines, start)


class TestADispatchOutsideMainFails(unittest.TestCase):
    def setUp(self) -> None:
        self.text = WORKFLOW.read_text(encoding="utf-8")
        self.refuse = job_block(self.text, "refuse")

    def test_the_refuse_job_runs_only_for_a_dispatch_off_main(self) -> None:
        """Test that the refuse job runs only for a dispatch off main."""
        self.assertEqual(
            "github.event_name == 'workflow_dispatch' && github.ref != 'refs/heads/main'",
            condition_of(self.refuse),
        )

    def test_the_refuse_job_is_small_and_on_the_pinned_runner(self) -> None:
        """Test that the refuse job is small and on the pinned runner."""
        self.assertIn("runs-on: ubuntu-26.04", self.refuse)
        self.assertIn("timeout-minutes: 5", self.refuse)
        self.assertNotIn("uses:", self.refuse, "the refusal needs no action and no checkout")
        self.assertNotIn("secrets.", self.refuse)

    def test_the_refusal_script_exits_non_zero_and_names_the_ref(self) -> None:
        """Test that the refusal script exits non zero and names the ref."""
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
        """Test that the ref reaches the script through env."""
        self.assertIn("REF: ${{ github.ref }}", self.refuse)
        self.assertNotIn("${{", run_script_of(self.refuse))

    def test_decide_still_requires_main_for_a_dispatch(self) -> None:
        """Test that decide still requires main for a dispatch."""
        self.assertEqual(
            "github.event_name == 'workflow_dispatch' && github.ref == 'refs/heads/main'",
            condition_of(job_block(self.text, "decide")),
        )

    def test_nothing_runs_the_workflow_but_a_dispatch(self) -> None:
        """Test that nothing runs the workflow but a dispatch."""
        self.assertNotIn("\n  workflow_run:", self.text)
        on_block = re.search(r"(?ms)^on:\n(.*?)(?=^\S)", self.text)
        self.assertIsNotNone(on_block, "no on: block")
        self.assertEqual("workflow_dispatch:", on_block.group(1).strip())


class TestTheDeployRefusesAnUncheckedCommit(unittest.TestCase):
    def setUp(self) -> None:
        self.text = WORKFLOW.read_text(encoding="utf-8")
        self.decide = job_block(self.text, "decide")

    def run_gate(
        self, fixture: str, *, curl_fails: bool = False
    ) -> subprocess.CompletedProcess[str]:
        """Run the check-gate script against a stubbed Actions API."""
        with tempfile.TemporaryDirectory() as scratch:
            stub = pathlib.Path(scratch)
            curl = '#!/bin/sh\necho "curl: (22) HTTP 500" >&2\nexit 22\n'
            if not curl_fails:
                curl = (
                    '#!/bin/sh\n'
                    'for arg in "$@"; do url="$arg"; done\n'
                    'event=""\n'
                    'case "$url" in\n'
                    '  *event=*) event="${url#*event=}"; event="${event%%&*}";;\n'
                    'esac\n'
                    'if [ -n "$event" ]; then\n'
                    '  jq --arg event "$event" '
                    "'{workflow_runs: [.workflow_runs[] | select(.event == $event)]}' "
                    '"${FIXTURE}"\n'
                    'else\n'
                    '  cat "${FIXTURE}"\n'
                    'fi\n'
                )
            (stub / "curl").write_text(curl, encoding="utf-8")
            (stub / "curl").chmod(0o755)
            payload = stub / "payload.json"
            payload.write_text(fixture, encoding="utf-8")
            env = {
                "PATH": f"{stub}{os.pathsep}{os.environ['PATH']}",
                "FIXTURE": str(payload),
                "COMMIT": "0123456789abcdef",
                "GITHUB_REPOSITORY": "owner/repo",
                "GITHUB_API_URL": "https://api.github.com",
                "GITHUB_TOKEN": "token",
            }
            script = run_script_of_step(
                step_block(self.decide, "Refuse a commit whose check did not succeed")
            )
            return subprocess.run(
                ["bash", "-e", "-c", script],
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )

    def test_only_the_decide_job_may_read_the_check_runs(self) -> None:
        """Test that only the decide job may read the check runs."""
        permissions = re.search(r"(?ms)^permissions:\n(.*?)(?=^\S)", self.text)
        self.assertIsNotNone(permissions, "no permissions block")
        self.assertNotIn("actions: read", permissions.group(1))
        self.assertIn("actions: read", self.decide)
        for job in ("refuse", "deploy"):
            self.assertNotIn("actions: read", job_block(self.text, job))

    def test_the_gate_reads_push_and_dispatched_runs_on_main(self) -> None:
        """Test that the gate reads push and dispatched runs on main."""
        script = run_script_of_step(
            step_block(self.decide, "Refuse a commit whose check did not succeed")
        )
        self.assertIn(
            "actions/workflows/ci.yml/runs?head_sha=${COMMIT}&per_page=100", script
        )
        self.assertNotIn("event=push", script)
        self.assertIn(
            '(.event == "push" or .event == "workflow_dispatch")', script
        )
        self.assertIn('.head_branch == "main"', script)
        self.assertIn("Authorization: Bearer ${GITHUB_TOKEN}", script)
        self.assertIn("GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}", self.decide)

    def test_a_successful_check_lets_the_run_through(self) -> None:
        """Test that a successful check lets the run through."""
        result = self.run_gate(
            '{"workflow_runs":['
            '{"event":"push","head_branch":"main","conclusion":"failure"},'
            '{"event":"push","head_branch":"main","conclusion":"success"}]}'
        )
        self.assertEqual(0, result.returncode, result.stdout + result.stderr)

    def test_a_run_dispatched_on_main_lets_the_run_through(self) -> None:
        """Test that a run dispatched on main lets the run through."""
        result = self.run_gate(
            '{"workflow_runs":[{"event":"workflow_dispatch",'
            '"head_branch":"main","conclusion":"success"}]}'
        )
        self.assertEqual(0, result.returncode, result.stdout + result.stderr)

    def test_a_pull_request_run_does_not_count(self) -> None:
        """Test that a pull request run does not count."""
        result = self.run_gate(
            '{"workflow_runs":[{"event":"pull_request",'
            '"head_branch":"main","conclusion":"success"}]}'
        )
        self.assertNotEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertIn(
            "::error::deploy requires a successful check run for 0123456789abcdef "
            "from a push or a dispatch on main: its check workflow runs are "
            "success (pull_request on main);",
            result.stdout,
        )
        self.assertIn("pull_request", result.stdout)

    def test_a_run_on_another_branch_does_not_count(self) -> None:
        """Test that a run on another branch does not count."""
        result = self.run_gate(
            '{"workflow_runs":[{"event":"workflow_dispatch",'
            '"head_branch":"feature","conclusion":"success"}]}'
        )
        self.assertNotEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertIn(
            "::error::deploy requires a successful check run for 0123456789abcdef "
            "from a push or a dispatch on main: its check workflow runs are "
            "success (workflow_dispatch on feature);",
            result.stdout,
        )

    def test_a_failed_check_refuses_the_run(self) -> None:
        """Test that a failed check refuses the run."""
        result = self.run_gate(
            '{"workflow_runs":['
            '{"event":"push","head_branch":"main","conclusion":"failure"},'
            '{"event":"push","head_branch":"main","conclusion":null,"status":"in_progress"}]}'
        )
        self.assertNotEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertIn(
            "::error::deploy requires a successful check run for 0123456789abcdef "
            "from a push or a dispatch on main: "
            "its check workflow runs are "
            "failure (push on main), in_progress (push on main);",
            result.stdout,
        )

    def test_a_commit_with_no_check_run_refuses_the_run(self) -> None:
        """Test that a commit with no check run refuses the run."""
        result = self.run_gate('{"workflow_runs":[]}')
        self.assertNotEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertIn(
            "::error::deploy requires a successful check run for 0123456789abcdef "
            "from a push or a dispatch on main: "
            "its check workflow runs are none;",
            result.stdout,
        )

    def test_only_cancelled_runs_refuse_the_run(self) -> None:
        """Test that only cancelled runs refuse the run."""
        result = self.run_gate(
            '{"workflow_runs":[{"event":"push","head_branch":"main","conclusion":"cancelled"}]}'
        )
        self.assertNotEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertIn(
            "::error::deploy requires a successful check run for 0123456789abcdef "
            "from a push or a dispatch on main: "
            "its check workflow runs are cancelled (push on main);",
            result.stdout,
        )

    def test_only_queued_or_in_progress_runs_refuse_the_run(self) -> None:
        """Test that only queued or in progress runs refuse the run."""
        result = self.run_gate(
            '{"workflow_runs":['
            '{"event":"push","head_branch":"main","conclusion":null,"status":"queued"},'
            '{"event":"push","head_branch":"main","conclusion":null,"status":"in_progress"}]}'
        )
        self.assertNotEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertIn(
            "::error::deploy requires a successful check run for 0123456789abcdef "
            "from a push or a dispatch on main: "
            "its check workflow runs are "
            "queued (push on main), in_progress (push on main);",
            result.stdout,
        )

    def test_a_failed_actions_query_refuses_the_run(self) -> None:
        """Test that a failed actions query refuses the run."""
        result = self.run_gate('{"workflow_runs":[]}', curl_fails=True)
        self.assertNotEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertIn(
            "::error::could not list the check workflow runs for 0123456789abcdef "
            "through the Actions API",
            result.stdout,
        )

    def test_the_gate_step_runs_exactly_when_the_deploy_could(self) -> None:
        """Test that the gate step runs exactly when the deploy could."""
        step = step_block(self.decide, "Refuse a commit whose check did not succeed")
        self.assertEqual("steps.tip.outputs.is_tip == 'true'", condition_of(step))
        self.assertNotIn("continue-on-error:", step)

    def test_no_job_but_refuse_runs_for_a_dispatch_off_main(self) -> None:
        """Test that no job but refuse runs for a dispatch off main."""
        deploy = job_block(self.text, "deploy")
        self.assertIn("needs: decide", deploy)
        self.assertIn("needs.decide.outputs.is_tip == 'true'", condition_of(deploy))


class TestTheDeployJobInstallsGoAlone(unittest.TestCase):
    def setUp(self) -> None:
        self.deploy = job_block(WORKFLOW.read_text(encoding="utf-8"), "deploy")
        self.action = SETUP_ACTION.read_text(encoding="utf-8")

    def test_the_deploy_job_asks_the_setup_action_for_go_only(self) -> None:
        """Test that the deploy job asks the setup action for go only."""
        step = re.search(
            r"uses: \./\.github/actions/setup\n\s+with:\n((?:\s+\S.*\n)+?)(?=\n|\s+- |\s+# )",
            self.deploy + "\n",
        )
        self.assertIsNotNone(step, "the deploy job no longer calls the setup action with inputs")
        self.assertEqual("tools: go", step.group(1).strip())

    def test_the_setup_action_passes_the_named_tools_to_mise(self) -> None:
        """Test that the setup action passes the named tools to mise."""
        self.assertRegex(self.action, r"(?m)^  tools:\n")
        self.assertIn("install_args: ${{ inputs.tools }}", self.action)

    def test_the_setup_action_installs_everything_when_no_tool_is_named(self) -> None:
        """Test that the setup action installs everything when no tool is named."""
        step = re.search(
            r"(?s)- name: Install the toolchain from \.mise\.toml\n(.*?)(?=\n    #|\n    - name:|\Z)",
            self.action,
        )
        self.assertIsNotNone(step, "the setup action has no toolchain install step")
        self.assertNotIn("if:", step.group(1))
        self.assertIn("uses: jdx/mise-action@", step.group(1))

    def test_a_named_subset_does_not_leave_the_rest_reported_as_missing(self) -> None:
        """Test that a named subset does not leave the rest reported as missing."""
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
        """Test that the limiting script turns a space list into a comma list."""
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
