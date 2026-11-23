#!/usr/bin/env python3
"""Unit tests for check_web_build_scripts.py's criteria and exit codes.

Stdlib-only (unittest + tempfile). Run directly:

    python3 tools/test_check_web_build_scripts.py

The subject is one ruling: every workspace member the build gate runs
`build:self` on carries that script with the prescribed content, and its
`build` selects the package's own dependency graph and calls `build:self`
through the prescribed pnpm form. Both scripts are compared as exact strings,
so added whitespace is a finding. A missing `build:self` is the silent skip
the gate exists to catch: `pnpm -r run build:self` passes over the package
without a word. Cases cover the missing script, the wrong `build:self`, the
wrong `build` form, a filter naming another package, extra whitespace, a
manifest with no scripts (two findings), a manifest with no name (an
infrastructure error), and the exemption, which is by package name so a
non-conforming member under an exception's glob is still checked.

Cases run over a scratch tree holding two ordinary members and the two
exceptions.
"""

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

sys.path.insert(0, HERE)

import check_web_build_scripts as m  # noqa: E402

WORKSPACE = """packages:
  - packages/*
  - test-utils
  - ../host
"""


def expected_build(name: str) -> str:
    return (
        f"pnpm --filter {name}... --fail-if-no-match "
        "--workspace-concurrency=1 run build:self"
    )


def manifest(
    name: str,
    build: str | None = None,
    build_self: str | None = None,
    scripts: dict | None = None,
) -> str:
    if scripts is None:
        scripts = {}
        if build is not None:
            scripts["build"] = build
        if build_self is not None:
            scripts["build:self"] = build_self
    return json.dumps({"name": name, "scripts": scripts}, indent=2) + "\n"


class GateCase(unittest.TestCase):
    """A scratch tree holding two conforming packages and the two exceptions."""

    def setUp(self) -> None:
        self.root = tempfile.mkdtemp(prefix="web-build-scripts-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.write("web/pnpm-workspace.yaml", WORKSPACE)
        self.conforming("web/packages/alpha/package.json", "@speed/alpha")
        self.conforming("web/packages/beta/package.json", "@speed/beta")
        # The two exceptions carry scripts no rule covers.
        self.write(
            "web/test-utils/package.json",
            manifest("@speed/test-utils", build="tsc -p tsconfig.json"),
        )
        self.write(
            "host/package.json",
            manifest("demo-web", build="tsc -p tsconfig.json && vite build"),
        )

    def conforming(self, relative: str, name: str) -> None:
        self.write(
            relative,
            manifest(
                name,
                build=expected_build(name),
                build_self="tsc -p tsconfig.build.json",
            ),
        )

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


class TestTheBuildScripts(GateCase):
    def test_a_conforming_tree_passes(self) -> None:
        self.assert_clean()

    def test_a_package_without_build_self_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/package.json",
            manifest("@speed/beta", build=expected_build("@speed/beta")),
        )
        out = self.assert_finding(
            "web/packages/beta/package.json", "@speed/beta", "missing build:self"
        )
        self.assertNotIn("web/packages/alpha", out)

    def test_a_build_self_with_another_script_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/package.json",
            manifest(
                "@speed/beta",
                build=expected_build("@speed/beta"),
                build_self="tsc -p tsconfig.json",
            ),
        )
        self.assert_finding(
            "web/packages/beta/package.json", "build:self is", "tsc -p tsconfig.build.json"
        )

    def test_a_build_that_does_not_use_the_prescribed_form_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/package.json",
            manifest(
                "@speed/beta",
                build="tsc -p tsconfig.build.json",
                build_self="tsc -p tsconfig.build.json",
            ),
        )
        self.assert_finding("web/packages/beta/package.json", "build is", "expected")
        code, out, err = self.run_gate()
        self.assertNotIn("missing build:self", out)
        self.assertNotIn("build:self is", out)

    def test_a_build_filtering_another_package_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/package.json",
            manifest(
                "@speed/beta",
                build=expected_build("@speed/alpha"),
                build_self="tsc -p tsconfig.build.json",
            ),
        )
        out = self.assert_finding("web/packages/beta/package.json", "build is")
        self.assertIn("@speed/beta...", out)

    def test_a_package_without_build_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/package.json",
            manifest("@speed/beta", build_self="tsc -p tsconfig.build.json"),
        )
        self.assert_finding("web/packages/beta/package.json", "missing build")

    def test_the_exception_packages_are_not_checked(self) -> None:
        self.write(
            "web/test-utils/package.json",
            manifest("@speed/test-utils", scripts={"build": "something else"}),
        )
        self.write(
            "host/package.json",
            manifest("demo-web", scripts={"build": "another thing", "build:self": "x"}),
        )
        self.assert_clean()

    def test_exemption_is_by_name_not_by_path(self) -> None:
        self.write(
            "web/test-utils/package.json",
            manifest("@speed/other-test-support", build="tsc -p tsconfig.json"),
        )
        self.write(
            "host/package.json",
            manifest("some-other-host", build="tsc -p tsconfig.json"),
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, f"stdout={out!r} stderr={err!r}")
        self.assertIn("web/test-utils/package.json", out)
        self.assertIn("host/package.json", out)
        self.assertIn("missing build:self", out)

    def test_a_manifest_without_scripts_reports_both_scripts_missing(self) -> None:
        self.write(
            "web/packages/beta/package.json",
            json.dumps({"name": "@speed/beta"}, indent=2) + "\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, f"stdout={out!r} stderr={err!r}")
        self.assertIn("missing build:self", out)
        self.assertIn("missing build", out)
        self.assertIn("2 build-script problem(s)", err)

    def test_extra_whitespace_in_build_self_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/package.json",
            manifest(
                "@speed/beta",
                build=expected_build("@speed/beta"),
                build_self="tsc  -p tsconfig.build.json",
            ),
        )
        self.assert_finding(
            "web/packages/beta/package.json", "build:self is", "tsc -p tsconfig.build.json"
        )

    def test_extra_whitespace_in_build_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/package.json",
            manifest(
                "@speed/beta",
                build=expected_build("@speed/beta").replace("pnpm ", "pnpm  ", 1),
                build_self="tsc -p tsconfig.build.json",
            ),
        )
        self.assert_finding("web/packages/beta/package.json", "build is")


class TestExitCodes(GateCase):
    def test_a_root_that_is_not_a_directory_exits_2(self) -> None:
        code, _, err = self.run_gate(os.path.join(self.root, "nope"))
        self.assertEqual(2, code)
        self.assertIn("not a directory", err)

    def test_a_tree_without_the_workspace_file_exits_2(self) -> None:
        os.remove(os.path.join(self.root, "web", "pnpm-workspace.yaml"))
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("pnpm-workspace.yaml", err)

    def test_a_workspace_that_lists_no_member_exits_2(self) -> None:
        self.write("web/pnpm-workspace.yaml", "packages:\n  - nothing/*\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("no member", err)

    def test_an_unparsable_manifest_exits_2(self) -> None:
        self.write("web/packages/beta/package.json", "{not json")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("cannot read", err)

    def test_a_manifest_without_a_name_exits_2(self) -> None:
        self.write(
            "web/packages/beta/package.json",
            json.dumps({"scripts": {"build:self": "tsc -p tsconfig.build.json"}}, indent=2)
            + "\n",
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("names no package", err)


if __name__ == "__main__":
    unittest.main()
