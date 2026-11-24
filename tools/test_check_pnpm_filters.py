#!/usr/bin/env python3
"""Unit tests for check_pnpm_filters.py's criteria and exit codes."""

from __future__ import annotations

import contextlib
import io
import json
import os
import shutil
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)

sys.path.insert(0, HERE)

import check_pnpm_filters as m  # noqa: E402

WORKSPACE = """packages:
  - packages/*
  - ../host
"""

DOCKERFILE = """FROM node:26 AS web
RUN mkdir -p /out \\
    && pnpm -C /src/web --filter @speed/alpha --fail-if-no-match run build
"""

WORKFLOW = """jobs:
  build:
    runs-on: ubuntu-26.04
    steps:
      - run: pnpm -C web --filter @speed/alpha --fail-if-no-match run build
      - name: more
        run: |
          set -e
          pnpm -C web --filter @speed/beta --fail-if-no-match test
"""

ACTION = """runs:
  using: composite
  steps:
    - run: pnpm -C web --filter @speed/alpha --fail-if-no-match build
      shell: bash
"""

MAKEFILE = """build:
\t@pnpm -C web --filter @speed/alpha --fail-if-no-match run build
"""


def manifest(name: str, build: str | None = None) -> str:
    scripts = {"build": build} if build is not None else {}
    return json.dumps({"name": name, "scripts": scripts}, indent=2) + "\n"


class GateCase(unittest.TestCase):
    """A scratch tree, written by the case and scanned by the gate."""

    def setUp(self) -> None:
        self.root = tempfile.mkdtemp(prefix="pnpm-filters-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.write("web/pnpm-workspace.yaml", WORKSPACE)
        self.write(
            "web/packages/alpha/package.json",
            manifest("@speed/alpha", "pnpm --filter @speed/beta --fail-if-no-match build"),
        )
        self.write("web/packages/beta/package.json", manifest("@speed/beta", "tsc"))
        self.write("host/package.json", manifest("host-web"))

    def write(self, relative: str, text: str) -> None:
        path = os.path.join(self.root, relative)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(text)

    def run_gate(self, root: str | None = None) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            try:
                code = m.main(["--root", root if root is not None else self.root])
            except SystemExit as exc:
                code = exc.code
        return code, out.getvalue(), err.getvalue()

    def assert_clean(self) -> None:
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def assert_finding(self, path: str, *wanted: str) -> str:
        code, out, err = self.run_gate()
        self.assertEqual(1, code, f"stdout={out!r} stderr={err!r}")
        self.assertIn(path, out)
        for text in wanted:
            self.assertIn(text, out)
        return out


class TestTheFlagGuardsEverySource(GateCase):
    def test_a_clean_tree_passes(self) -> None:
        """Test that a clean tree passes."""
        self.write("starters/demo/Dockerfile", DOCKERFILE)
        self.write(".github/workflows/ci.yml", WORKFLOW)
        self.write(".github/actions/setup/action.yml", ACTION)
        self.write("Makefile", MAKEFILE)
        self.assert_clean()

    def test_a_dockerfile_without_the_flag_is_a_finding(self) -> None:
        """Test that a dockerfile without the flag is a finding."""
        self.write(
            "starters/demo/Dockerfile",
            DOCKERFILE.replace(" --fail-if-no-match", ""),
        )
        out = self.assert_finding("starters/demo/Dockerfile:2", "--fail-if-no-match")
        self.assertIn("missing", out)

    def test_a_package_json_script_without_the_flag_is_a_finding(self) -> None:
        """Test that a package json script without the flag is a finding."""
        self.write(
            "web/packages/beta/package.json",
            manifest("@speed/beta", "pnpm --filter @speed/alpha build && tsc"),
        )
        self.assert_finding("web/packages/beta/package.json", "--fail-if-no-match")

    def test_a_workflow_run_block_without_the_flag_is_a_finding(self) -> None:
        """Test that a workflow run block without the flag is a finding."""
        self.write(
            ".github/workflows/ci.yml",
            WORKFLOW.replace("--fail-if-no-match test", "test"),
        )
        self.assert_finding(".github/workflows/ci.yml:9", "--fail-if-no-match")

    def test_a_composite_action_without_the_flag_is_a_finding(self) -> None:
        """Test that a composite action without the flag is a finding."""
        self.write(
            ".github/actions/setup/action.yml",
            ACTION.replace(" --fail-if-no-match", ""),
        )
        self.assert_finding(".github/actions/setup/action.yml:4", "--fail-if-no-match")

    def test_a_makefile_recipe_without_the_flag_is_a_finding(self) -> None:
        """Test that a makefile recipe without the flag is a finding."""
        self.write("Makefile", MAKEFILE.replace(" --fail-if-no-match", ""))
        self.assert_finding("Makefile:2", "--fail-if-no-match")

    def test_a_continued_dockerfile_line_is_read_as_one_command(self) -> None:
        """Test that a continued dockerfile line is read as one command."""
        self.write(
            "starters/demo/Dockerfile",
            "RUN pnpm -C /src/web \\\n    --filter @speed/alpha \\\n    run build\n",
        )
        self.assert_finding("starters/demo/Dockerfile:1", "--fail-if-no-match")

    def test_a_call_without_a_filter_needs_no_flag(self) -> None:
        """Test that a call without a filter needs no flag."""
        self.write("Makefile", "check:\n\t@pnpm -C web -r build\n\t@pnpm -C web install\n")
        self.assert_clean()

    def test_the_equals_form_of_a_selector_is_read(self) -> None:
        """Test that the equals form of a selector is read."""
        self.write("Makefile", "build:\n\t@pnpm -C web --filter=@speed/alpha run build\n")
        self.assert_finding("Makefile:2", "--fail-if-no-match")

    def test_the_second_command_of_a_chain_is_read(self) -> None:
        """Test that the second command of a chain is read."""
        self.write(
            "Makefile",
            "build:\n\t@pnpm -C web install && pnpm -C web --filter host-web run build\n",
        )
        self.assert_finding("Makefile:2", "--fail-if-no-match")


class TestTheFlagMustStandBeforeTheCommandWord(GateCase):
    def test_the_flag_after_the_script_name_is_a_finding(self) -> None:
        """Test that the flag after the script name is a finding."""
        self.write(
            "Makefile",
            "build:\n\t@pnpm -C web --filter @speed/alpha build --fail-if-no-match\n",
        )
        out = self.assert_finding("Makefile:2", "after the command word")
        self.assertNotIn("is missing", out)

    def test_the_flag_after_run_is_a_finding(self) -> None:
        """Test that the flag after run is a finding."""
        self.write(
            "Makefile",
            "build:\n\t@pnpm -C web --filter @speed/alpha run --fail-if-no-match build\n",
        )
        self.assert_finding("Makefile:2", "after the command word")

    def test_the_flag_before_the_filter_is_accepted(self) -> None:
        """Test that the flag before the filter is accepted."""
        self.write(
            "Makefile",
            "build:\n\t@pnpm -C web --fail-if-no-match --filter @speed/alpha run build\n",
        )
        self.assert_clean()

    def test_the_flag_before_the_command_word_holds_for_exec(self) -> None:
        """Test that the flag before the command word holds for exec."""
        self.write(
            "Makefile",
            "e2e:\n\t@pnpm -C web --filter host-web --fail-if-no-match exec playwright test\n",
        )
        self.assert_clean()


class TestEverySelectorNamesAMember(GateCase):
    def test_a_selector_naming_no_member_is_a_finding_even_with_the_flag(self) -> None:
        """Test that a selector naming no member is a finding even with the flag."""
        self.write(
            "Makefile",
            "build:\n\t@pnpm -C web --filter @speed/gamma --fail-if-no-match run build\n",
        )
        out = self.assert_finding("Makefile:2", "'@speed/gamma'", "no workspace member")
        self.assertNotIn("is missing", out)

    def test_one_missing_selector_among_members_is_a_finding(self) -> None:
        """Test that one missing selector among members is a finding."""
        self.write(
            "Makefile",
            "build:\n\t@pnpm -C web --filter @speed/alpha --filter @speed/gone "
            "--fail-if-no-match build\n",
        )
        self.assert_finding("Makefile:2", "'@speed/gone'")

    def test_a_member_outside_the_workspace_directory_counts(self) -> None:
        """Test that a member outside the workspace directory counts."""
        self.write(
            "Makefile",
            "build:\n\t@pnpm -C web --filter host-web --fail-if-no-match run build\n",
        )
        self.assert_clean()

    def test_graph_markers_and_negation_are_removed_before_the_comparison(self) -> None:
        """Test that graph markers and negation are removed before the comparison."""
        for selector in ("...@speed/alpha", "@speed/alpha...", "...^@speed/alpha", "!@speed/beta"):
            with self.subTest(selector=selector):
                self.write(
                    "Makefile",
                    f"build:\n\t@pnpm -C web --filter '{selector}' --fail-if-no-match build\n",
                )
                self.assert_clean()

    def test_a_graph_marker_does_not_excuse_a_missing_name(self) -> None:
        """Test that a graph marker does not excuse a missing name."""
        self.write(
            "Makefile",
            "build:\n\t@pnpm -C web --filter '...@speed/gone' --fail-if-no-match build\n",
        )
        self.assert_finding("Makefile:2", "'...@speed/gone'")

    def test_a_glob_must_match_a_member(self) -> None:
        """Test that a glob must match a member."""
        self.write(
            "Makefile",
            "ok:\n\t@pnpm -C web --filter '@speed/*' --fail-if-no-match build\n"
            "bad:\n\t@pnpm -C web --filter '@gone/*' --fail-if-no-match build\n",
        )
        out = self.assert_finding("Makefile:4", "'@gone/*'")
        self.assertNotIn("Makefile:2", out)

    def test_a_path_selector_is_accepted(self) -> None:
        """Test that a path selector is accepted."""
        for selector in ("./packages/alpha", "{packages/alpha}", "@speed/alpha{./packages/alpha}"):
            with self.subTest(selector=selector):
                self.write(
                    "Makefile",
                    f"build:\n\t@pnpm -C web --filter '{selector}' --fail-if-no-match build\n",
                )
                self.assert_clean()


class TestExitCodes(GateCase):
    def test_a_root_that_is_not_a_directory_exits_2(self) -> None:
        """Test that a root that is not a directory exits 2."""
        code, _, err = self.run_gate(os.path.join(self.root, "nope"))
        self.assertEqual(2, code)
        self.assertIn("not a directory", err)

    def test_a_tree_without_the_workspace_file_exits_2(self) -> None:
        """Test that a tree without the workspace file exits 2."""
        os.remove(os.path.join(self.root, "web", "pnpm-workspace.yaml"))
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("pnpm-workspace.yaml", err)

    def test_a_workspace_that_lists_no_member_exits_2(self) -> None:
        """Test that a workspace that lists no member exits 2."""
        self.write("web/pnpm-workspace.yaml", "packages:\n  - nothing/*\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("no member", err)


class TestTheRealRepository(unittest.TestCase):
    def test_every_filtered_call_in_this_repository_is_guarded(self) -> None:
        """Test that every filtered call in this repository is guarded."""
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", REPO])
        self.assertEqual(0, code, f"stdout={out.getvalue()!r} stderr={err.getvalue()!r}")


if __name__ == "__main__":
    unittest.main()
