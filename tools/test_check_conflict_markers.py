#!/usr/bin/env python3
"""Unit tests for check_conflict_markers.py's criterion and exit codes.

Stdlib-only (unittest + tempfile), matching this directory's own "plain
executables with no third-party dependencies" convention. Run directly:

    python3 tools/test_check_conflict_markers.py

The criterion under test is which lines count as a marker. Two of its
decisions are the ones worth pinning: the middle marker is reported only
in a file that also carries a labeled one, because a line of seven `=`
alone is how Markdown underlines a heading, and a file that is not UTF-8
text is not read, because a marker is a line of text. The exit-code
contract is the other half: a marker is a content finding and exits 1,
while a root that is not a directory and a tree with nothing to read are
infrastructure errors and exit 2. An uncaught traceback exits 1 and would
read as a finding.
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
REPO = HERE.parent

sys.path.insert(0, str(HERE))

import check_conflict_markers as m  # noqa: E402

# Git's three labeled markers and its bare middle one, assembled from
# their characters rather than written out: this file is inside the tree
# the gate scans, and a literal marker here would be this gate's own
# first finding.
LT, EQ, GT, PIPE = "<" * 7, "=" * 7, ">" * 7, "|" * 7

# A conflict block as git writes one, labels and all.
BLOCK = "\n".join(
    [
        "before",
        f"{LT} HEAD",
        "ours",
        EQ,
        "theirs",
        f"{GT} feature",
        "after",
        "",
    ]
)


class GateCase(unittest.TestCase):
    def setUp(self) -> None:
        self.root = tempfile.mkdtemp(prefix="conflict-markers-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)

    def write(self, name: str, text: str | bytes) -> str:
        path = pathlib.Path(self.root) / name
        path.parent.mkdir(parents=True, exist_ok=True)
        if isinstance(text, bytes):
            path.write_bytes(text)
        else:
            path.write_text(text, encoding="utf-8")
        return str(path)

    def run_gate(self) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", self.root])
        return code, out.getvalue(), err.getvalue()


class TestCriterion(GateCase):
    def test_a_clean_tree_passes(self) -> None:
        self.write("README.md", "# title\n\ntext\n")
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0)
        self.assertEqual(out, "")

    def test_a_conflict_block_is_reported_line_by_line(self) -> None:
        self.write("doc.md", BLOCK)
        code, out, err = self.run_gate()
        self.assertEqual(code, 1)
        # The whole block, not just its opening line: whoever cuts it out
        # needs to see both ends.
        self.assertIn("doc.md:2: <<<<<<< HEAD", out)
        self.assertIn("doc.md:4: =======", out)
        self.assertIn("doc.md:6: >>>>>>> feature", out)
        self.assertIn("3 marker line(s)", err)

    def test_a_bare_middle_marker_alone_is_not_a_marker(self) -> None:
        # Markdown's setext underline. Reporting it would make this gate a
        # permanent false finding for any document that uses that style.
        self.write("heading.md", "# title\n\n=======\n\nsubtitle\n")
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0)
        self.assertEqual(out, "")

    def test_a_bare_middle_marker_beside_a_labeled_one_is_reported(self) -> None:
        self.write("doc.md", "a\n<<<<<<< HEAD\n=======\n")
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("doc.md:3: =======", out)

    def test_each_labeled_marker_is_a_finding_on_its_own(self) -> None:
        for name, line in (
            ("closing.md", ">>>>>>> feature\n"),
            ("base.md", "||||||| merged common ancestors\n"),
            ("opening.md", "<<<<<<< HEAD\n"),
        ):
            with self.subTest(name=name):
                self.write(name, line)
                code, out, _ = self.run_gate()
                self.assertEqual(code, 1)
                self.assertIn(name, out)
                pathlib.Path(self.root, name).unlink()

    def test_a_marker_indented_or_without_a_label_is_not_a_marker(self) -> None:
        # Git writes the marker in column one; prose about markers, and the
        # seven characters on their own, are not one.
        self.write("prose.md", "  <<<<<<< HEAD\n<<<<<<<\n>>>>>>>\n")
        code, _, _ = self.run_gate()
        self.assertEqual(code, 0)

    def test_a_binary_file_is_not_read(self) -> None:
        self.write("blob.bin", (f"{LT} HEAD\n").encode() + b"\x00\x01\x02\n")
        self.write("keep.md", "text\n")
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0)
        self.assertEqual(out, "")

    def test_node_modules_is_skipped(self) -> None:
        self.write("node_modules/pkg/README.md", BLOCK)
        self.write("keep.md", "text\n")
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0)
        self.assertEqual(out, "")


class TestExitCodes(GateCase):
    def test_a_root_that_is_not_a_directory_is_an_infrastructure_error(self) -> None:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", str(pathlib.Path(self.root, "absent"))])
        self.assertEqual(code, 2)
        self.assertIn("not a directory", err.getvalue())

    def test_a_tree_with_nothing_to_read_is_an_infrastructure_error(self) -> None:
        # Only a binary, so the scan read no text at all: an empty subject
        # is not a pass.
        self.write("blob.bin", b"\x00\x01")
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("read nothing", err)


class TestTheRepositoryItself(unittest.TestCase):
    def test_the_repository_carries_no_conflict_marker(self) -> None:
        """The end-to-end case: this gate's own subject is this tree."""
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", str(REPO)])
        self.assertEqual(code, 0, f"{out.getvalue()}{err.getvalue()}")


if __name__ == "__main__":
    unittest.main()
