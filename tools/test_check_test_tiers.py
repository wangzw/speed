#!/usr/bin/env python3
"""Unit tests for check_test_tiers.py's criteria and exit codes.

Stdlib-only (unittest + tempfile), matching this directory's own "plain
executables with no third-party dependencies" convention. Run directly:

    python3 tools/test_check_test_tiers.py

The subject is the two rulings the gate holds, and each is pinned by the
case that would otherwise pass it: a tag the legs do not pass hides its
file from every leg, in the file and in the Makefile alike, and a library
that skips a case turns a layer green. The exit-code contract is the
other half: a finding exits 1, while a root that is not a directory, a
Makefile with no tag and a scan with no subject are infrastructure errors
and exit 2. An uncaught traceback exits 1 and would read as a finding.

Most cases are written over a scratch tree that starts with the least it
can hold and still have a subject for both checks -- one file carrying
the tag the legs pass, and one library importing testing. A case about a
control asks for no baseline instead, because a tree with no subject is
what that control is for.
"""

from __future__ import annotations

import contextlib
import io
import os
import pathlib
import shutil
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent
REPO = HERE.parent

sys.path.insert(0, str(HERE))

import check_test_tiers as m  # noqa: E402

# A Makefile with the two legs the vocabulary is read from. The target
# named `test-race` carries the text `-race` without passing it, which is
# the second thing the reader has to get right.
MAKEFILE = """test:
\tgo -C ./pkg test ./...
test-race:
\tgo -C ./pkg test -race ./...
test-integration:
\tgo -C ./pkg test -tags=integration ./...
"""

# The least a tree needs for both checks to have a subject.
BASELINE = {
    "pkg/unit_test.go": """//go:build integration
package pkg

import "testing"

func TestSomething(t *testing.T) {}
""",
    "pkg/fixture.go": """package pkg

import "testing"

func Connect(tb testing.TB) {}
""",
}


def case_file(constraint: str) -> str:
    head = f"{constraint}\n" if constraint else ""
    return f"{head}package pkg\n\nimport \"testing\"\n\nfunc TestSomething(t *testing.T) {{}}\n"


class GateCase(unittest.TestCase):
    """A scratch tree, written by the case and scanned by the gate."""

    def setUp(self) -> None:
        self.root = tempfile.mkdtemp(prefix="test-tiers-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)

    def write(self, relative: str, text: str) -> None:
        path = os.path.join(self.root, relative)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(text)

    def run_gate(self) -> tuple[int, str, str]:
        """Run the gate over the scratch tree, capturing both streams.

        The infrastructure path raises SystemExit rather than returning a
        code, which is how the executable exits; catching it here is what
        the command line does with it.
        """
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            try:
                code = m.main(["--root", self.root])
            except SystemExit as exc:
                code = exc.code
        return code, out.getvalue(), err.getvalue()

    def tree(self, makefile: str = MAKEFILE, baseline: bool = True, **files: str) -> None:
        self.write("Makefile", makefile)
        for relative, text in dict(BASELINE if baseline else {}, **files).items():
            self.write(relative, text)


class TestTheTagsAgreeWithTheLegs(GateCase):
    def test_a_tag_the_legs_pass_is_clean(self):
        self.tree(**{"pkg/tagged_test.go": case_file("//go:build integration")})
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_the_race_tag_is_established_by_the_race_flag(self):
        # `-race` is what establishes the tag; the target named `test-race`
        # carries the same text and must not be read as passing it, so the
        # case removes the flag and holds that the tag is then unknown.
        self.tree(
            makefile=MAKEFILE.replace(" -race ./...", " ./..."),
            **{"pkg/race_test.go": case_file("//go:build !race")},
        )
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/race_test.go", out)

    def test_a_misspelled_tag_in_a_file_is_a_finding(self):
        self.tree(**{"pkg/typo_test.go": case_file("//go:build intergration")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/typo_test.go", out)
        self.assertIn("intergration", out)

    def test_a_misspelled_tag_in_the_makefile_is_a_finding(self):
        # The other direction: the leg passes a tag nothing carries, and the
        # correctly tagged files are then compiled by no run at all.
        self.tree(makefile=MAKEFILE.replace("-tags=integration", "-tags=intergration"))
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("integration", out)

    def test_a_legacy_build_line_is_read_too(self):
        self.tree(**{"pkg/typo_test.go": case_file("// +build intergration")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("intergration", out)

    def test_a_constraint_after_the_package_clause_is_not_read(self):
        # Go reads the constraint from the head; a comment quoting one
        # below the package clause is prose.
        self.tree(**{"pkg/prose.go": "package pkg\n\n// //go:build intergration\n"})
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")


class TestNoLibrarySkipsACase(GateCase):
    def test_a_fixture_that_skips_is_a_finding(self):
        self.tree(
            **{
                "pkg/fixture.go": """package pkg

import "testing"

func Connect(tb testing.TB) {
\ttb.Skip("no broker")
}
""",
            }
        )
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/fixture.go", out)
        self.assertIn("Skip", out)

    def test_a_case_may_skip(self):
        # A test file is left alone: it may skip for a reason of its own.
        self.tree(
            **{
                "pkg/skipping_test.go": """package pkg

import "testing"

func TestSomething(t *testing.T) {
\tt.Skip("a sibling case reported it")
}
""",
            }
        )
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_a_skip_method_on_a_non_testing_type_is_not_read(self):
        # A file that does not import testing has a `Skip` of its own, and
        # that is not this gate's subject.
        self.tree(
            **{
                "pkg/reader.go": """package pkg

type Reader struct{}

func (Reader) Skip(n int) {}

func use(r Reader) { r.Skip(3) }
""",
            }
        )
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")


class TestTheExitCodes(GateCase):
    def test_a_root_that_is_not_a_directory_is_an_infrastructure_error(self):
        missing = os.path.join(self.root, "not-there")
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            try:
                code = m.main(["--root", missing])
            except SystemExit as exc:
                code = exc.code
        self.assertEqual(2, code)

    def test_a_makefile_with_no_tag_is_an_infrastructure_error(self):
        self.tree(makefile="test:\n\tgo -C ./pkg test ./...\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("Makefile", err)

    def test_a_tree_with_no_constraint_has_no_subject(self):
        # The integration layer gone is the disaster, not a pass.
        self.tree(baseline=False, **{"pkg/plain.go": case_file("")})
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("build constraint", err)

    def test_a_tree_with_no_library_has_no_subject(self):
        self.tree(baseline=False, **{"pkg/plain_test.go": case_file("//go:build integration")})
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("non-test", err)


class TestThisRepository(unittest.TestCase):
    """The gate over the tree it ships in: it has to be clean there."""

    def test_the_repository_is_clean(self):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", str(REPO)])
        self.assertEqual(0, code, f"stdout={out.getvalue()!r} stderr={err.getvalue()!r}")


if __name__ == "__main__":
    unittest.main()
