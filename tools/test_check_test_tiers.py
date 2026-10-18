#!/usr/bin/env python3
"""Unit tests for check_test_tiers.py's criteria and exit codes.

Stdlib-only (unittest + tempfile), matching this directory's own "plain
executables with no third-party dependencies" convention. Run directly:

    python3 tools/test_check_test_tiers.py

The subject is the three rulings the gate holds, and each is pinned by
the case that would otherwise pass it: a tag the legs do not pass hides
its file from every leg, in the file and in the Makefile alike; a library
that skips a case turns a layer green; and a gated tag whose cases the
commit gate's `-run` pattern does not select runs nothing, while a
pattern that also catches an untagged case runs the unit layer twice.
The exit-code contract is the other half: a
finding exits 1, while a root that is not a directory, a Makefile with no
tag and a scan with no subject are infrastructure errors and exit 2. An
uncaught traceback exits 1 and would read as a finding.

Most cases are written over a scratch tree that starts with the least it
can hold and still have a subject for every check -- one file carrying
each tag the legs pass, and one library importing testing. A case about a
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

# A Makefile with the legs the vocabulary is read from. The target named
# `test-race` carries the text `-race` without passing it, which is the
# second thing the reader has to get right. `check` reaches `test` and
# `test-review` but not the scheduled integration legs, which is the
# boundary the gated-tag check reads.
MAKEFILE = """check:
\t@$(MAKE) test
\t@$(MAKE) test-review
test:
\tgo -C ./pkg test ./...
test-race:
\tgo -C ./pkg test -race ./...
test-integration:
\tgo -C ./pkg test -tags=integration ./...
test-review:
\tgo -C ./pkg test -tags=review -run '^TestReview' ./...
GATED_TEST_TAGS := review
"""

# The least a tree needs for every check to have a subject: a file carrying
# each tag a leg passes, and a library importing testing.
BASELINE = {
    "pkg/unit_test.go": """//go:build integration
package pkg

import "testing"

func TestSomething(t *testing.T) {}
""",
    "pkg/review_guard_test.go": """//go:build review
package pkg

import "testing"

func TestReviewGuardIsLoopback(t *testing.T) {}
""",
    "pkg/fixture.go": """package pkg

import "testing"

func Connect(tb testing.TB) {}
""",
}


def case_file(constraint: str) -> str:
    head = f"{constraint}\n" if constraint else ""
    return f"{head}package pkg\n\nimport \"testing\"\n\nfunc TestSomething(t *testing.T) {{}}\n"


def review_case(name: str) -> str:
    return (
        "//go:build review\n"
        "package pkg\n\n"
        'import "testing"\n\n'
        f"func {name}(t *testing.T) {{}}\n"
    )


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


class TestTheCommitGateRunsGatedCases(GateCase):
    """A gated tag's cases are selected by a test leg the gate reaches.

    A tag absent from GATED_TEST_TAGS is not held to this rule: the baseline
    tree carries an integration file whose leg passes no `-run`, and the
    clean cases below would fail if that scheduled layer were included.
    """

    def test_a_new_gated_case_the_leg_selects_is_clean(self):
        self.tree(**{"pkg/second_review_test.go": review_case("TestReviewSecondGuard")})
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_a_gated_case_outside_the_pattern_is_a_finding(self):
        # The failure the gate exists for: a review-tagged case the leg's
        # -run pattern does not match is compiled by vet-review and executed
        # by nothing.
        self.tree(**{"pkg/second_review_test.go": review_case("TestGuardIsLoopback")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/second_review_test.go", out)
        self.assertIn("TestGuardIsLoopback", out)

    def test_only_the_cases_the_filter_sees_are_held_to_it(self):
        # TestMain always runs and is never filtered, and a lowercase suffix
        # is not a test the toolchain discovers; neither is a case the -run
        # pattern would have to select.
        self.tree(
            **{
                "pkg/helpers_review_test.go": """//go:build review
package pkg

import "testing"

func TestMain(m *testing.M) {}

func Testhelper(t *testing.T) {}
""",
            }
        )
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_a_pattern_that_also_selects_an_untagged_case_is_a_finding(self):
        # The other way a tagged leg re-runs the unit layer: its pattern is
        # broad enough to catch a case that the default leg already ran.
        self.tree(
            makefile=MAKEFILE.replace(" -run '^TestReview'", " -run '^Test'"),
            **{
                "pkg/plain_test.go": """package pkg

import "testing"

func TestSomething(t *testing.T) {}
""",
            },
        )
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("TestSomething", out)

    def test_a_gated_leg_without_run_is_a_finding(self):
        # Without -run the leg runs the untagged cases a second time, which
        # is what the review leg is forbidden to do.
        self.tree(makefile=MAKEFILE.replace(" -run '^TestReview'", ""))
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("without -run", out)

    def test_a_gated_tag_the_check_target_does_not_reach_is_a_finding(self):
        # The tag still has a test leg, but no longer one the commit gate
        # runs: the cases are executed by nothing that gates a commit.
        self.tree(makefile=MAKEFILE.replace("\t@$(MAKE) test-review\n", ""))
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("no test leg", out)

    def test_a_gated_tag_no_file_carries_is_an_infrastructure_error(self):
        self.tree(
            baseline=False,
            **{
                "pkg/unit_test.go": case_file("//go:build integration"),
                "pkg/fixture.go": """package pkg

import "testing"

func Connect(tb testing.TB) {}
""",
            },
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("gates the tag review", err)


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
