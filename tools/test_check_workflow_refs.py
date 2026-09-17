#!/usr/bin/env python3
"""Unit tests for check_workflow_refs.py's guard set and exit-code contract.

Stdlib-only (unittest + tempfile), matching this directory's own "plain
executables with no third-party dependencies" convention. Run directly:

    python3 tools/test_check_workflow_refs.py

The contract under test is the gate's exit codes. A relative reference
inside a guarded file -- a copy of a coding-flow template, identified by
its `uses: vislake/coding-flows@...` line -- is the gate's content
finding and exits 1, and the historical defect behind the gate (template
copies whose comments carried `../../SKILL.md`, dangling once copied
into a repository) is reproduced below as a fixture case. "Nothing was
checked" -- no .github/workflows/ directory, no guarded file to check --
and a file that cannot be read are infrastructure errors and exit 2,
never 0 and never 1: an uncaught traceback exits 1 and would read as a
finding.

The fixtures are pure ASCII on purpose. The real guarded files carry
Chinese comments; none of that text belongs in this file, and tools/**
is English by the repository's language rule.
"""

from __future__ import annotations

import contextlib
import io
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent

sys.path.insert(0, str(HERE))

import check_workflow_refs as m  # noqa: E402

# A guarded file: a byte-copy of an upstream template, marked by the call
# to the composite action upstream publishes.
GUARDED = """\
name: coding-flow designer
on:
  workflow_dispatch:
jobs:
  plan:
    runs-on: ubuntu-latest
    steps:
      - uses: vislake/coding-flows@main
"""

# The historical defect in its original shape: a template copy whose
# header comment referenced the skill-internal path ../../SKILL.md. The
# line is on 2, so a test can pin the line number the report carries.
HISTORICAL = """\
name: coding-flow designer
# see ../../SKILL.md for the shared rules
on:
  workflow_dispatch:
jobs:
  plan:
    runs-on: ubuntu-latest
    steps:
      - uses: vislake/coding-flows@main
"""

# The single-dot form of the same criterion: in a copy, a
# repository-root-relative path is as dangling as a skill-relative one.
DOT_SLASH = """\
name: coding-flow designer
on:
  workflow_dispatch:
jobs:
  plan:
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/plan.sh
      - uses: vislake/coding-flows@main
"""

# A repository-owned workflow: no marker line, so its repository-relative
# paths are legitimate and must not be reported.
OWN = """\
name: ci
on:
  push:
jobs:
  check:
    uses: ./.github/workflows/reusable.yml
"""


def write_workflows(root: pathlib.Path, files: dict[str, str]) -> None:
    """Write the given files into root's .github/workflows/ fixture."""
    workflows = root / ".github" / "workflows"
    workflows.mkdir(parents=True, exist_ok=True)
    for name, text in files.items():
        (workflows / name).write_text(text, encoding="utf-8")


class WorkflowRefsGateTests(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = pathlib.Path(self._tmp.name)

    def _run(self) -> tuple[int, str, str]:
        """Run the gate; return (exit code, stdout, stderr)."""
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", str(self.root)])
        return code, out.getvalue(), err.getvalue()

    def _run_expect_exit(self) -> tuple[int, str]:
        """Run the gate expecting SystemExit(2); return (exit code, stderr)."""
        err = io.StringIO()
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(err):
            with self.assertRaises(SystemExit) as cm:
                m.main(["--root", str(self.root)])
        return cm.exception.code, err.getvalue()

    def _run_in_subprocess(self) -> tuple[int, str, str]:
        """Run the gate as its own process whose stdout cannot encode non-ASCII.

        The encoding defect cannot be built in-process -- the StringIO a
        redirect supplies takes every character -- so the gate runs for
        real with PYTHONIOENCODING=ascii, the way a pipe under a C-locale
        environment would refuse it. Returns (exit code, stdout, stderr).
        """
        proc = subprocess.run(
            [sys.executable, str(HERE / "check_workflow_refs.py"), "--root", str(self.root)],
            capture_output=True,
            text=True,
            env={**os.environ, "PYTHONIOENCODING": "ascii"},
        )
        return proc.returncode, proc.stdout, proc.stderr

    def test_clean_guarded_files_pass_and_each_is_reported(self) -> None:
        write_workflows(self.root, {"designer.yml": GUARDED, "coder.yml": GUARDED})
        code, out, _ = self._run()
        self.assertEqual(code, 0)
        # A clean run is not a silent one: every guarded file's verdict
        # appears, so a gate that quietly stopped looking cannot pass.
        self.assertIn(".github/workflows/coder.yml", out)
        self.assertIn(".github/workflows/designer.yml", out)
        self.assertIn("workflow-refs: ok", out)

    def test_historical_relative_reference_exits_1(self) -> None:
        # The defect the gate exists for: the copies once carried
        # skill-internal ../../ references, which dangle in this
        # repository. Fails on such a tree, passes on the fixed one.
        write_workflows(self.root, {"designer.yml": HISTORICAL})
        code, out, err = self._run()
        self.assertEqual(code, 1)
        self.assertIn(".github/workflows/designer.yml:2", out)
        self.assertIn("../../SKILL.md", out)
        self.assertIn("1 relative reference(s)", err)

    def test_single_dot_relative_reference_exits_1(self) -> None:
        # The criterion covers `./` as well as `../` (the plan's definition
        # of a relative path segment): any relative reference in a copy
        # dangles, whichever path it names.
        write_workflows(self.root, {"designer.yml": DOT_SLASH})
        code, out, _ = self._run()
        self.assertEqual(code, 1)
        self.assertIn(".github/workflows/designer.yml:8", out)
        self.assertIn("./scripts/plan.sh", out)

    def test_repository_owned_workflow_may_use_relative_paths(self) -> None:
        # The exemption is copy identity, not directory: this repository's
        # own workflows may legitimately use repository-root-relative
        # paths. Pinned so the rule is not later widened to the whole
        # directory, which would flag such a file.
        write_workflows(self.root, {"designer.yml": GUARDED, "ci.yml": OWN})
        code, out, _ = self._run()
        self.assertEqual(code, 0)
        self.assertNotIn("ci.yml", out)

    def test_no_guarded_file_exits_2(self) -> None:
        # A directory that exists but holds no copy is "nothing checked",
        # not a pass.
        write_workflows(self.root, {"ci.yml": OWN})
        code, err = self._run_expect_exit()
        self.assertEqual(code, 2)
        self.assertIn("no guarded file found", err)

    def test_missing_workflows_dir_exits_2(self) -> None:
        code, err = self._run_expect_exit()
        self.assertEqual(code, 2)
        self.assertIn(".github/workflows", err)

    @unittest.skipUnless(
        hasattr(os, "geteuid") and os.geteuid() != 0,
        "permission bits do not bind root; the unreadable case cannot be built",
    )
    def test_unreadable_file_exits_2(self) -> None:
        # A read failure must exit 2 rather than escape as a traceback:
        # an uncaught exception exits 1, which would read as a finding.
        write_workflows(self.root, {"designer.yml": GUARDED})
        target = self.root / ".github" / "workflows" / "designer.yml"
        target.chmod(0o000)
        self.addCleanup(target.chmod, 0o600)
        code, err = self._run_expect_exit()
        self.assertEqual(code, 2)
        self.assertIn("cannot read", err)

    def test_hit_lines_survive_an_ascii_stdout(self) -> None:
        # A hit line is printed back verbatim, and the real copies carry
        # Chinese comments; on a stream that cannot encode them the gate
        # must escape instead of dying in the print, which would exit
        # with an empty report. The comment is written from escapes so
        # this file stays ASCII.
        text = HISTORICAL.replace(
            "# see ../../SKILL.md for the shared rules",
            "#\u89c1 ../../SKILL.md \u7684\u89c4\u5219",
        )
        write_workflows(self.root, {"designer.yml": text})
        code, out, err = self._run_in_subprocess()
        self.assertEqual(code, 1)
        self.assertNotIn("Traceback", err)
        self.assertIn("designer.yml:2", out)

    def test_clean_tree_with_a_non_ascii_name_does_not_false_alarm(self) -> None:
        # The clean report prints each guarded file's name; a stream that
        # cannot encode the name must not turn a clean tree's exit 0 into
        # the exit 1 a finding carries. The name is written from escapes
        # so this file stays ASCII.
        write_workflows(self.root, {"\u770b\u677f.yml": GUARDED})
        code, _out, err = self._run_in_subprocess()
        self.assertEqual(code, 0)
        self.assertNotIn("Traceback", err)


    def test_help_survives_an_ascii_stdout(self) -> None:
        # --help is answered by argparse on stdout before any check
        # runs, so the escape configuration must already be in place;
        # otherwise a program name the stream cannot encode would crash
        # the one path that precedes the report. The name is written
        # from escapes so this file stays ASCII.
        named = self.root / "check_workflow_refs_\u68c0\u67e5.py"
        shutil.copy(HERE / "check_workflow_refs.py", named)
        proc = subprocess.run(
            [sys.executable, str(named), "--help"],
            capture_output=True,
            text=True,
            env={**os.environ, "PYTHONIOENCODING": "ascii"},
        )
        self.assertEqual(proc.returncode, 0)
        self.assertNotIn("Traceback", proc.stderr)


if __name__ == "__main__":
    unittest.main()
