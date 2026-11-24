#!/usr/bin/env python3
"""Unit tests for check_fixup_commits.py."""

from __future__ import annotations

import contextlib
import io
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent

sys.path.insert(0, str(HERE))

import check_fixup_commits as g  # noqa: E402

IDENT = [
    "-c", "user.name=Test",
    "-c", "user.email=test@example.invalid",
    "-c", "commit.gpgsign=false",
    "-c", "init.defaultBranch=main",
]


class FixupCase(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory(prefix="fixup-commits-")
        self.addCleanup(self._tmp.cleanup)
        self.root = self._tmp.name
        self.counter = 0

    def git(self, *args: str) -> str:
        env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
        env["GIT_CONFIG_GLOBAL"] = os.devnull
        env["GIT_CONFIG_SYSTEM"] = os.devnull
        out = subprocess.run(
            ["git", *IDENT, "-C", self.root, *args],
            capture_output=True, text=True, check=True, env=env,
        )
        return out.stdout.strip()

    def init(self) -> None:
        self.git("init", "-q")
        self.commit("feat(x): base")

    def commit(self, subject: str) -> None:
        self.counter += 1
        path = pathlib.Path(self.root, f"f{self.counter}.txt")
        path.write_text(f"{self.counter}\n")
        self.git("add", path.name)
        self.git("commit", "-q", "-m", subject)

    def run_gate(self) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = g.main(["--root", self.root])
        return code, out.getvalue(), err.getvalue()

    def branch(self) -> None:
        self.git("checkout", "-q", "-b", "topic")

    def test_fixup_commit_fails_and_is_listed(self) -> None:
        """Test that fixup commit fails and is listed."""
        self.init()
        self.branch()
        self.commit("feat(x): thing")
        self.commit("fixup! feat(x): thing")
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("fixup! feat(x): thing", out)
        self.assertEqual(len(out.splitlines()), 1)

    def test_squash_and_amend_are_caught(self) -> None:
        """Test that squash and amend are caught."""
        self.init()
        self.branch()
        self.commit("squash! feat(x): thing")
        self.commit("amend! feat(x): thing")
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("squash! feat(x): thing", out)
        self.assertIn("amend! feat(x): thing", out)

    def test_clean_branch_passes(self) -> None:
        """Test that clean branch passes."""
        self.init()
        self.branch()
        self.commit("feat(x): thing")
        self.commit("docs(x): explain the fixup! prefix")
        self.assertEqual(self.run_gate()[0], 0)

    def test_fixup_already_on_base_is_not_listed(self) -> None:
        """Test that fixup already on base is not listed."""
        self.init()
        self.commit("fixup! feat(x): base")
        self.branch()
        self.commit("feat(x): thing")
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0)
        self.assertEqual(out.strip(), "")

    def test_failure_hints_at_autosquash(self) -> None:
        """Test that failure hints at autosquash."""
        self.init()
        self.branch()
        self.commit("fixup! feat(x): base")
        code, out, err = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("git rebase --autosquash", out + err)

    def test_head_equal_to_base_passes(self) -> None:
        """Test that head equal to base passes."""
        self.init()
        self.assertEqual(self.run_gate()[0], 0)

    def test_origin_main_is_preferred_over_main(self) -> None:
        """Test that origin main is preferred over main."""
        self.init()
        self.git("update-ref", "refs/remotes/origin/main", "HEAD")
        self.commit("fixup! feat(x): base")
        self.git("update-ref", "refs/heads/main", "HEAD")
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("fixup! feat(x): base", out)

    def test_main_is_used_without_origin(self) -> None:
        """Test that main is used without origin."""
        self.init()
        self.branch()
        self.commit("fixup! feat(x): base")
        self.assertEqual(self.run_gate()[0], 1)

    def test_non_repository_reports_nothing_checked(self) -> None:
        """Test that non repository reports nothing checked."""
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0)
        self.assertIn("nothing checked", out)

    def test_no_base_ref_reports_nothing_checked(self) -> None:
        """Test that no base ref reports nothing checked."""
        self.init()
        self.git("branch", "-m", "other")
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0)
        self.assertIn("nothing checked", out)


if __name__ == "__main__":
    unittest.main()
