#!/usr/bin/env python3
"""Unit tests for check_platform_errors.py's derivation and exit codes.

Stdlib-only (unittest + tempfile), matching this directory's own "plain
executables with no third-party dependencies" convention. Run directly:

    python3 tools/test_check_platform_errors.py

The cases pin the four things the gate claims: the derivation reads every
construction that writes a problem type (a whole URN literal, a prefix plus
a literal, prefix plus a same-package or imported constant, a helper's
call-site arguments, and a mount-style `Code…` table); a backend code the
catalog lacks is a finding; a catalog entry no backend writes is a finding
unless it is in KEPT, and a kept entry nothing refers to is stale; and the
two languages must carry the same leaves with no unexplained duplicate.
The exit-code contract is the other half: a finding exits 1, while a root
that is not a directory, a missing catalog, and a derivation with no
subject are infrastructure errors and exit 2.

The fixtures are scratch trees holding the least a check needs: one Go
package writing the codes under test, and the two locale files. A case
about a kept entry adds one frontend file mentioning it, because the
reason's reference is what keeps the exemption honest.
"""

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

HERE = pathlib.Path(__file__).resolve().parent

sys.path.insert(0, str(HERE))

import check_platform_errors as m  # noqa: E402

CATALOG = os.path.join(m.LOCALE_DIR, "en-US.json")
CATALOG_ZH = os.path.join(m.LOCALE_DIR, "zh-CN.json")

# One package that writes a problem type in every shape the derivation
# reads: a whole URN literal, a prefix plus a literal, a prefix plus a
# same-package constant, a prefix plus an imported constant, and a helper
# that takes its code from its call sites (both a literal and an imported
# constant). The `Code…` constant is deliberately not a problem code: this
# package carries no mount-style table, so it must not be collected.
ALPHA = """package alpha

import "github.com/vislake/speed/pkg/mount"

const (
\ttypePrefix   = "urn:speed:alpha:"
\tconflictCode = "conflict"
\tnotACode     = "not-a-code"
)

const whole = "urn:speed:alpha:literal-code"

func fail(code string) error { return Fault{Status: 400, Type: typePrefix + code} }

var (
\tinline   = Fault{Type: typePrefix + "inline-code"}
\tconflict = Fault{Type: typePrefix + conflictCode}
\timported = Fault{Type: typePrefix + mount.CodeThrottled}
)

func handlers() {
\t_ = fail(mount.CodeBadRequest)
\t_ = fail("arg-code")
}
"""

# A mount-style closed table: a package that declares its prefix and its
# code constants answers every one of them through its checked writers.
BETA = """package beta

const (
\ttypePrefix = "urn:speed:beta:"
\tCodeAlpha  = "alpha-code"
\tCodeBeta   = "beta-code"
)
"""

MOUNT = """package mount

const (
\tCodeBadRequest = "bad-request"
\tCodeThrottled  = "throttled"
)
"""


def write(path: pathlib.Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def write_bundle(root: pathlib.Path, language: str, keys: list[str]) -> None:
    write(
        root / m.LOCALE_DIR / f"{language}.json",
        json.dumps({"errors": {key: f"{language}:{key}" for key in keys}}, indent=2),
    )


def run(root: pathlib.Path, *args: str) -> tuple[int, str, str]:
    """Run main over a scratch root, returning (exit code, stdout, stderr)."""
    out, err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        try:
            code = m.main(["--root", str(root), *args])
        except SystemExit as exc:
            code = exc.code if isinstance(exc.code, int) else 1
    return code, out.getvalue(), err.getvalue()


class DerivationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.root = pathlib.Path(tempfile.mkdtemp(prefix="platform-errors-"))
        write(self.root / "pkg" / "alpha" / "alpha.go", ALPHA)
        write(self.root / "pkg" / "beta" / "beta.go", BETA)
        write(self.root / "pkg" / "mount" / "mount.go", MOUNT)
        (self.root / "starters").mkdir()

    def tearDown(self) -> None:
        shutil.rmtree(self.root)

    def test_reads_every_construction(self) -> None:
        derived, unresolved, _ = m.derive(str(self.root))
        self.assertEqual(
            derived,
            {
                "alpha.literal_code",
                "alpha.inline_code",
                "alpha.conflict",
                "alpha.throttled",
                "alpha.bad_request",
                "alpha.arg_code",
                "beta.alpha_code",
                "beta.beta_code",
            },
        )
        # The helper's `prefix + code` is resolved through its call sites, so
        # the site is reported as a note and not as a gap.
        self.assertEqual(len(unresolved), 1)

    def test_reads_a_literal_prefix_plus_constant(self) -> None:
        # `unavailableType` in the tree has this shape: the prefix is a
        # literal and the code is a constant, so neither the whole-URN rule
        # nor the prefix-constant rule sees it. The constant deliberately
        # does not start with `Code`, so only this rule can find it.
        root = pathlib.Path(tempfile.mkdtemp(prefix="platform-errors-"))
        try:
            write(
                root / "pkg" / "gamma" / "gamma.go",
                'package gamma\n\nconst (\n'
                '\tunavailable = "urn:speed:gamma:" + goneCode\n'
                '\tgoneCode    = "gone-code"\n'
                ")\n",
            )
            (root / "starters").mkdir()
            derived, _, _ = m.derive(str(root))
            self.assertEqual(derived, {"gamma.gone_code"})
        finally:
            shutil.rmtree(root)

    def test_a_tree_with_no_go_file_is_infrastructure(self) -> None:
        empty = pathlib.Path(tempfile.mkdtemp(prefix="platform-errors-empty-"))
        try:
            (empty / "pkg").mkdir()
            (empty / "starters").mkdir()
            code, _, err = run(empty)
            self.assertEqual(code, 2)
            self.assertIn("no Go file", err)
        finally:
            shutil.rmtree(empty)

    def test_a_missing_backend_root_is_infrastructure(self) -> None:
        # A workspace root the gate scans but that is not present is an
        # infrastructure error: skipping it would silently shrink the derived
        # set, and an empty set must never read as clean.
        root = pathlib.Path(tempfile.mkdtemp(prefix="platform-errors-missing-"))
        try:
            write(root / "pkg" / "alpha" / "alpha.go", ALPHA)
            code, _, err = run(root)
            self.assertEqual(code, 2)
            self.assertIn("backend root starters/ is missing", err)
        finally:
            shutil.rmtree(root)

    def test_an_unreadable_go_file_is_infrastructure(self) -> None:
        root = pathlib.Path(tempfile.mkdtemp(prefix="platform-errors-unreadable-"))
        try:
            write(root / "pkg" / "alpha" / "alpha.go", ALPHA)
            (root / "starters").mkdir()
            # A broken symlink named like a Go source is listed by the walk and
            # fails to open, which is the unreadable-file path without needing
            # a permission bit that a privileged test runner would ignore.
            (root / "pkg" / "alpha" / "broken.go").symlink_to(
                root / "pkg" / "alpha" / "absent.go"
            )
            code, _, err = run(root)
            self.assertEqual(code, 2)
            self.assertIn("could not be read", err)
        finally:
            shutil.rmtree(root)


class CatalogTest(unittest.TestCase):
    def setUp(self) -> None:
        self.root = pathlib.Path(tempfile.mkdtemp(prefix="platform-errors-"))
        write(self.root / "pkg" / "alpha" / "alpha.go", ALPHA)
        write(self.root / "pkg" / "mount" / "mount.go", MOUNT)
        (self.root / "starters").mkdir()
        self.saved_kept = dict(m.KEPT)
        self.saved_duplicates = dict(m.DUPLICATES)
        m.KEPT = {}
        m.DUPLICATES = {}

    def tearDown(self) -> None:
        m.KEPT = self.saved_kept
        m.DUPLICATES = self.saved_duplicates
        shutil.rmtree(self.root)

    def test_matching_catalogs_pass(self) -> None:
        keys = [
            "alpha.literal_code",
            "alpha.inline_code",
            "alpha.conflict",
            "alpha.throttled",
            "alpha.bad_request",
            "alpha.arg_code",
        ]
        write_bundle(self.root, "en-US", keys)
        write_bundle(self.root, "zh-CN", keys)
        code, out, err = run(self.root)
        self.assertEqual(code, 0, out + err)
        self.assertIn("6 backend code(s)", out)

    def test_a_backend_code_without_copy_is_a_finding(self) -> None:
        keys = [
            "alpha.literal_code",
            "alpha.inline_code",
            "alpha.conflict",
            "alpha.throttled",
            "alpha.bad_request",
        ]
        write_bundle(self.root, "en-US", keys)
        write_bundle(self.root, "zh-CN", keys)
        code, out, _ = run(self.root)
        self.assertEqual(code, 1)
        self.assertIn("alpha.arg_code: the backend writes this code", out)

    def test_an_unwritten_catalog_entry_is_a_finding(self) -> None:
        keys = [
            "alpha.literal_code",
            "alpha.inline_code",
            "alpha.conflict",
            "alpha.throttled",
            "alpha.bad_request",
            "alpha.arg_code",
            "dead.code",
        ]
        write_bundle(self.root, "en-US", keys)
        write_bundle(self.root, "zh-CN", keys)
        code, out, _ = run(self.root)
        self.assertEqual(code, 1)
        self.assertIn("dead.code: the catalog carries this code", out)

    def test_a_kept_entry_is_exempt_and_a_stale_reason_is_a_finding(self) -> None:
        keys = [
            "alpha.literal_code",
            "alpha.inline_code",
            "alpha.conflict",
            "alpha.throttled",
            "alpha.bad_request",
            "alpha.arg_code",
            "legacy.kept",
        ]
        write_bundle(self.root, "en-US", keys)
        write_bundle(self.root, "zh-CN", keys)
        m.KEPT = {"legacy.kept": "referenced by a frontend test"}

        # No frontend file mentions the code, so the reason is stale and the
        # case is a finding until the reference exists.
        code, out, _ = run(self.root)
        self.assertEqual(code, 1)
        self.assertIn("legacy.kept: KEPT says", out)

        write(
            self.root / "web" / "packages" / "x" / "x.test.ts",
            "const code = 'legacy.kept'\n",
        )
        code, out, err = run(self.root)
        self.assertEqual(code, 0, out + err)

    def test_a_language_gap_is_a_finding(self) -> None:
        keys = [
            "alpha.literal_code",
            "alpha.inline_code",
            "alpha.conflict",
            "alpha.throttled",
            "alpha.bad_request",
            "alpha.arg_code",
        ]
        write_bundle(self.root, "en-US", keys)
        write_bundle(self.root, "zh-CN", keys[:-1])
        code, out, _ = run(self.root)
        self.assertEqual(code, 1)
        self.assertIn("alpha.arg_code", out)
        self.assertIn("missing", out)

    def test_a_duplicate_key_is_a_finding_unless_declared(self) -> None:
        keys = [
            "alpha.literal_code",
            "alpha.inline_code",
            "alpha.conflict",
            "alpha.throttled",
            "alpha.bad_request",
            "alpha.arg_code",
        ]
        write_bundle(self.root, "en-US", keys)
        write_bundle(self.root, "zh-CN", keys)
        path = self.root / CATALOG
        document = path.read_text(encoding="utf-8")
        document = document.replace(
            '"alpha.literal_code": "en-US:alpha.literal_code"',
            '"alpha.literal_code": "first",\n    "alpha.literal_code": "second"',
        )
        path.write_text(document, encoding="utf-8")
        path = self.root / CATALOG_ZH
        document = path.read_text(encoding="utf-8").replace(
            '"alpha.literal_code": "zh-CN:alpha.literal_code"',
            '"alpha.literal_code": "first",\n    "alpha.literal_code": "second"',
        )
        path.write_text(document, encoding="utf-8")

        code, out, _ = run(self.root)
        self.assertEqual(code, 1)
        self.assertIn("errors.alpha.literal_code is declared twice", out)

        m.DUPLICATES = {"errors.alpha.literal_code": "deliberate"}
        code, _, err = run(self.root)
        self.assertEqual(code, 0, err)

    def test_a_missing_catalog_is_infrastructure(self) -> None:
        code, _, err = run(self.root)
        self.assertEqual(code, 2)
        self.assertIn("is missing", err)


if __name__ == "__main__":
    unittest.main()
