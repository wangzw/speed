#!/usr/bin/env python3
"""Unit tests for check_out_of_root_reads.py's criterion and exit codes.

Stdlib-only (unittest + tempfile), matching this directory's own "plain
executables with no third-party dependencies" convention. The cases build a
scratch tree whose module root is starters/demo/server and whose Makefile
carries UNCACHED_PACKAGES, then drive the gate through main().

The criterion under test is how a relative path literal is resolved: from the
package directory, as `go test` runs it, against the module root, however the
literal is spelled (a bare `../`, a `./../`, or a filepath.Join that climbs out
through a sibling), with a filepath.Join of literals resolved once and string
literals inside comments or absolute paths ignored. The exit-code contract is
the other half: a read is a finding and exits 1, while a root with no scope or
a Makefile with no UNCACHED_PACKAGES line is an infrastructure error and exits
2. An uncaught traceback exits 1 and would read as a finding.
"""

from __future__ import annotations

import contextlib
import io
import pathlib
import shutil
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent

sys.path.insert(0, str(HERE))

import check_out_of_root_reads as m  # noqa: E402


class GateCase(unittest.TestCase):
    def setUp(self) -> None:
        self.root = tempfile.mkdtemp(prefix="out-of-root-reads-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.makefile()
        self.write("starters/demo/server/clean_test.go", "package server\n")

    def write(self, name: str, text: str) -> pathlib.Path:
        path = pathlib.Path(self.root) / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        return path

    def makefile(self, uncached: tuple[str, ...] = ()) -> None:
        self.write("Makefile", f"UNCACHED_PACKAGES ?= {' '.join(uncached)}\n")

    def go(self, name: str, body: str) -> None:
        self.write(f"starters/demo/server/{name}", f"package server\n\n{body}\n")

    def run_gate(self) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", self.root])
        return code, out.getvalue(), err.getvalue()


class TestCriterion(GateCase):
    def test_a_clean_tree_passes(self) -> None:
        """Check that a tree with no relative escape passes."""
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0)
        self.assertEqual(out, "")

    def test_a_relative_literal_that_leaves_the_module_root_is_reported(self) -> None:
        """Check that a relative literal climbing out of the module root is a finding."""
        self.go("read_test.go", 'const p = "../../../contracts/api.json"')
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("starters/demo/server/read_test.go:3:", out)

    def test_a_join_of_literals_that_leaves_the_module_root_is_reported(self) -> None:
        """Check that an all-literal filepath.Join climbing out is a finding."""
        self.go("read_test.go", 'var _ = filepath.Join("..", "deploy", "x")')
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("starters/demo/server/read_test.go:3:", out)

    def test_a_dot_slash_relative_literal_that_leaves_the_root_is_reported(self) -> None:
        """Check that a ./../ literal climbing out is a finding."""
        self.go("read_test.go", 'const p = "./../web/backend-prefixes.json"')
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("starters/demo/server/read_test.go:3:", out)

    def test_a_join_starting_with_a_dot_that_leaves_the_root_is_reported(self) -> None:
        """Check that a filepath.Join starting with "." and climbing out is a finding."""
        self.go("read_test.go", 'var _ = filepath.Join(".", "..", "web", "x")')
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("starters/demo/server/read_test.go:3:", out)

    def test_a_join_climbing_out_through_a_sibling_is_reported(self) -> None:
        """Check that a filepath.Join climbing out through a sibling directory is a finding."""
        self.go(
            "read_test.go", 'var _ = filepath.Join("testdata", "..", "..", "web", "x")'
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("starters/demo/server/read_test.go:3:", out)

    def test_a_join_is_resolved_from_the_package_directory(self) -> None:
        """Check that a Join is resolved from its package directory, not the module root."""
        self.write(
            "starters/demo/server/a/b/read_test.go",
            "package b\n\n"
            'var _ = filepath.Join("..", "config.yaml")\n'
            'var _ = filepath.Join("..", "..", "..", "x")\n',
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertNotIn("read_test.go:3:", out)
        self.assertIn("read_test.go:4:", out)

    def test_a_join_with_a_non_literal_argument_still_reports_its_escapes(self) -> None:
        """Check that a Join with a computed argument still reports its literal escapes."""
        self.go(
            "read_test.go",
            'var _ = filepath.Join("..", "..", "..", "pkg", name+".json")',
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("starters/demo/server/read_test.go:3:", out)

    def test_an_absolute_path_and_a_comment_are_not_reads(self) -> None:
        """Check that an absolute path and a literal inside a comment are not findings."""
        self.write(
            "starters/demo/server/read_test.go",
            'package server\n\nvar _ = "/../index.html"\n// "../x"\n',
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0)
        self.assertEqual(out, "")

    def test_a_literal_that_only_starts_with_dots_is_not_a_path(self) -> None:
        """Check that a literal beginning with a third dot is not read as a path."""
        self.go("read_test.go", 'var _ = "... and %d more"')
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0)
        self.assertEqual(out, "")

    def test_a_listed_package_is_skipped_but_its_subdirectories_are_not(self) -> None:
        """Check that a listed package is exempt while its subdirectories are not."""
        self.makefile(("./starters/demo/server/uncached",))
        self.write(
            "starters/demo/server/uncached/read_test.go",
            'package uncached\n\nconst p = "../../../contracts/api.json"\n',
        )
        self.write(
            "starters/demo/server/uncached/sub/read_test.go",
            'package sub\n\nconst p = "../../../../contracts/api.json"\n',
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertNotIn("uncached/read_test.go:", out)
        self.assertIn("uncached/sub/read_test.go:", out)


class TestExitCodes(GateCase):
    def test_a_makefile_without_the_variable_is_an_infrastructure_error(self) -> None:
        """Check that a Makefile without the UNCACHED_PACKAGES line exits 2."""
        self.write("Makefile", "all:\n")
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("UNCACHED_PACKAGES", err)

    def test_a_listed_directory_that_does_not_exist_is_a_finding(self) -> None:
        """Check that a listed entry that is not a directory is a finding."""
        self.makefile(("./starters/demo/server/gone",))
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("gone", out)

    def test_a_listed_directory_without_go_files_is_a_finding(self) -> None:
        """Check that a listed directory carrying no Go file is a finding."""
        self.makefile(("./starters/demo/server/empty",))
        (pathlib.Path(self.root) / "starters/demo/server/empty").mkdir(parents=True)
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("empty", out)


if __name__ == "__main__":
    unittest.main()
