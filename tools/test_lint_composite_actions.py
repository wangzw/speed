#!/usr/bin/env python3
"""Unit tests for lint_composite_actions.py."""

from __future__ import annotations

import contextlib
import io
import json
import os
import pathlib
import shutil
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))

sys.path.insert(0, HERE)

import lint_composite_actions as m  # noqa: E402

STAND_IN = """\
#!{python}
import json, os, sys
script = sys.stdin.read()
with open(os.environ["STAND_IN_LOG"], "a", encoding="utf-8") as log:
    log.write(json.dumps({{"args": sys.argv[1:], "script": script}}) + "\\n")
comments = [
    {{"line": number, "column": 1, "level": "info", "code": 2086,
      "message": "Double quote to prevent globbing and word splitting."}}
    for number, line in enumerate(script.splitlines(), start=1)
    if "BAD" in line
]
print(json.dumps(comments))
sys.exit(1 if comments else 0)
"""

EXCLUDED = "SC1091,SC2194,SC2050,SC2153,SC2154,SC2157,SC2043"

HEADER = """name: stand-in
description: a composite action
runs:
  using: composite
  steps:
"""


def action(*steps: str) -> str:
    return HEADER + "".join(steps)


BASH_BLOCK = """    - name: block
      shell: bash
      run: |
        echo one
        echo two
"""


class LintCase(unittest.TestCase):
    def setUp(self) -> None:
        self.root = tempfile.mkdtemp(prefix="composite-lint-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.log = os.path.join(self.root, "stand-in.jsonl")
        self.shellcheck = os.path.join(self.root, "shellcheck")
        with open(self.shellcheck, "w", encoding="utf-8") as handle:
            handle.write(STAND_IN.format(python=sys.executable))
        os.chmod(self.shellcheck, 0o755)
        os.environ["STAND_IN_LOG"] = self.log
        self.addCleanup(os.environ.pop, "STAND_IN_LOG", None)
        self.repo = os.path.join(self.root, "repo")
        os.makedirs(self.repo)

    def write(self, relative: str, text: str) -> None:
        path = os.path.join(self.repo, relative)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(text)

    def run_lint(self, shellcheck: str | None = None) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            try:
                code = m.main(
                    [
                        "--root",
                        self.repo,
                        "--shellcheck",
                        shellcheck if shellcheck is not None else self.shellcheck,
                    ]
                )
            except SystemExit as exc:
                code = exc.code
        return code, out.getvalue(), err.getvalue()

    def calls(self) -> list[dict]:
        if not os.path.exists(self.log):
            return []
        with open(self.log, encoding="utf-8") as handle:
            return [json.loads(line) for line in handle if line.strip()]


class TestTheCallMatchesActionlint(LintCase):
    def test_a_bash_script_is_checked_with_actionlints_flags_and_prefix(self) -> None:
        """Test that a bash script is checked with actionlints flags and prefix."""
        self.write(".github/actions/a/action.yml", action(BASH_BLOCK))
        code, out, err = self.run_lint()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")
        (call,) = self.calls()
        self.assertEqual(
            ["--norc", "-f", "json", "-x", "--shell", "bash", "-e", EXCLUDED, "-"], call["args"]
        )
        self.assertEqual("set -eo pipefail\necho one\necho two\n", call["script"])

    def test_an_sh_script_is_checked_as_sh_with_set_e(self) -> None:
        """Test that an sh script is checked as sh with set e."""
        self.write(
            ".github/actions/a/action.yml",
            action("    - shell: sh\n      run: echo one\n"),
        )
        code, out, err = self.run_lint()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")
        (call,) = self.calls()
        self.assertEqual("sh", call["args"][call["args"].index("--shell") + 1])
        self.assertEqual("set -e\necho one\n", call["script"])

    def test_an_expression_is_replaced_by_underscores_of_the_same_length(self) -> None:
        """Test that an expression is replaced by underscores of the same length."""
        expression = "${{ inputs.tools }}"
        self.write(
            ".github/actions/a/action.yml",
            action(f"    - shell: bash\n      run: echo {expression} \"${{{{ github.ref }}}}\"\n"),
        )
        self.run_lint()
        (call,) = self.calls()
        self.assertEqual(
            "set -eo pipefail\necho " + "_" * len(expression) + ' "' + "_" * len("${{ github.ref }}") + '"\n',
            call["script"],
        )

    def test_a_multi_line_expression_keeps_its_line_breaks(self) -> None:
        """Test that a multi line expression keeps its line breaks."""
        self.write(
            ".github/actions/a/action.yml",
            action(
                "    - shell: bash\n      run: |\n        echo ${{ format(\n          'x') }}\n        BAD\n"
            ),
        )
        code, out, _ = self.run_lint()
        self.assertEqual(1, code)
        self.assertIn("action.yml:10:1", out)

    def test_other_shells_are_skipped(self) -> None:
        """Test that other shells are skipped."""
        steps = "".join(
            f"    - shell: {shell}\n      run: BAD\n"
            for shell in ("pwsh", "python", "cmd", "powershell")
        )
        self.write(".github/actions/a/action.yml", action(steps))
        code, out, err = self.run_lint()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")
        self.assertEqual([], self.calls())

    def test_a_shell_with_arguments_is_read_by_its_first_word(self) -> None:
        """Test that a shell with arguments is read by its first word."""
        self.write(
            ".github/actions/a/action.yml",
            action("    - shell: bash -e {0}\n      run: BAD\n"),
        )
        code, _, _ = self.run_lint()
        self.assertEqual(1, code)


class TestFindings(LintCase):
    def test_a_finding_is_reported_at_its_line_in_the_action_file(self) -> None:
        """Test that a finding is reported at its line in the action file."""
        self.write(
            ".github/actions/a/action.yml",
            action(
                "    - name: first\n      shell: bash\n      run: |\n        echo ok\n"
                "        ls BAD\n"
            ),
        )
        code, out, err = self.run_lint()
        self.assertEqual(1, code, f"stdout={out!r} stderr={err!r}")
        self.assertIn(".github/actions/a/action.yml:10:1: info: Double quote", out)
        self.assertIn("[SC2086]", out)

    def test_a_single_line_run_is_reported_at_the_run_line(self) -> None:
        """Test that a single line run is reported at the run line."""
        self.write(
            ".github/actions/a/action.yml",
            action("    - name: one\n      shell: bash\n      run: ls BAD\n"),
        )
        code, out, _ = self.run_lint()
        self.assertEqual(1, code)
        self.assertIn("action.yml:8:1", out)

    def test_a_run_step_without_a_shell_is_a_finding(self) -> None:
        """Test that a run step without a shell is a finding."""
        self.write(
            ".github/actions/a/action.yml",
            action("    - name: no shell\n      run: echo hi\n"),
        )
        code, out, _ = self.run_lint()
        self.assertEqual(1, code)
        self.assertIn("action.yml:7", out)
        self.assertIn("no shell", out)
        self.assertEqual([], self.calls(), "a step with no shell was handed to shellcheck")

    def test_every_step_and_every_action_is_checked(self) -> None:
        """Test that every step and every action is checked."""
        self.write(".github/actions/a/action.yml", action(BASH_BLOCK, BASH_BLOCK))
        self.write(".github/actions/b/action.yaml", action(BASH_BLOCK.replace("echo two", "BAD")))
        code, out, _ = self.run_lint()
        self.assertEqual(1, code)
        self.assertEqual(3, len(self.calls()))
        self.assertIn(".github/actions/b/action.yaml:", out)
        self.assertNotIn(".github/actions/a/", out)

    def test_a_clean_run_among_findings_does_not_hide_them(self) -> None:
        """Test that a clean run among findings does not hide them."""
        self.write(
            ".github/actions/a/action.yml",
            action(BASH_BLOCK, "    - shell: bash\n      run: BAD\n"),
        )
        code, out, _ = self.run_lint()
        self.assertEqual(1, code)
        self.assertEqual(1, out.count("[SC2086]"))

    def test_steps_that_run_nothing_are_ignored(self) -> None:
        """Test that steps that run nothing are ignored."""
        self.write(
            ".github/actions/a/action.yml",
            action(
                "    - uses: actions/checkout@v1\n      with:\n        run: BAD\n",
                "    - name: no run\n      shell: bash\n      env:\n        run: BAD\n",
            ),
        )
        code, out, err = self.run_lint()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")
        self.assertEqual([], self.calls())

    def test_quoted_values_are_read(self) -> None:
        """Test that quoted values are read."""
        self.write(
            ".github/actions/a/action.yml",
            action("    - shell: 'bash'\n      run: \"echo hi\"\n"),
        )
        code, out, err = self.run_lint()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")
        (call,) = self.calls()
        self.assertEqual("set -eo pipefail\necho hi\n", call["script"])

    def test_a_workflow_file_is_not_read(self) -> None:
        """Test that a workflow file is not read."""
        self.write(".github/actions/a/action.yml", action(BASH_BLOCK))
        self.write(".github/workflows/ci.yml", action("    - shell: bash\n      run: BAD\n"))
        code, out, err = self.run_lint()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")


class TestInfrastructureErrors(LintCase):
    def test_a_tree_with_no_composite_action_exits_2(self) -> None:
        """Test that a tree with no composite action exits 2."""
        self.write(".github/workflows/ci.yml", "name: ci\n")
        code, _, err = self.run_lint()
        self.assertEqual(2, code)
        self.assertIn("no composite action", err)

    def test_a_missing_shellcheck_exits_2(self) -> None:
        """Test that a missing shellcheck exits 2."""
        self.write(".github/actions/a/action.yml", action(BASH_BLOCK))
        code, _, err = self.run_lint(os.path.join(self.root, "no-such-shellcheck"))
        self.assertEqual(2, code)
        self.assertIn("not on PATH", err)

    def test_a_root_that_is_not_a_directory_exits_2(self) -> None:
        """Test that a root that is not a directory exits 2."""
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            with self.assertRaises(SystemExit) as raised:
                m.main(["--root", os.path.join(self.root, "nope"), "--shellcheck", self.shellcheck])
        self.assertEqual(2, raised.exception.code)
        self.assertIn("not a directory", err.getvalue())

    def test_a_shellcheck_that_fails_without_a_report_exits_2(self) -> None:
        """Test that a shellcheck that fails without a report exits 2."""
        self.write(".github/actions/a/action.yml", action(BASH_BLOCK))
        broken = os.path.join(self.root, "broken")
        with open(broken, "w", encoding="utf-8") as handle:
            handle.write("#!/bin/sh\necho boom >&2\nexit 3\n")
        os.chmod(broken, 0o755)
        code, _, err = self.run_lint(broken)
        self.assertEqual(2, code)
        self.assertIn("boom", err)


class TestThisRepository(unittest.TestCase):
    def test_the_composite_actions_are_found_and_their_scripts_are_read(self) -> None:
        """Test that the composite actions are found and their scripts are read."""
        root = pathlib.Path(HERE).parent
        files = m.action_files(str(root))
        self.assertTrue(files, "the repository has no composite action to lint")
        scripts = [
            (step.shell, step.run)
            for path in files
            for step in m.steps_of(pathlib.Path(path).read_text(encoding="utf-8"))
            if step.run is not None
        ]
        self.assertTrue(scripts, "no run step was read from the composite actions")
        for shell, script in scripts:
            self.assertTrue(shell, f"a run step with no shell: {script!r}")


if __name__ == "__main__":
    unittest.main()
