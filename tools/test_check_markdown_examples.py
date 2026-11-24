#!/usr/bin/env python3
"""Unit tests for check_markdown_examples.py."""

from __future__ import annotations

import os
import pathlib
import sys
import tempfile
import textwrap
import unittest
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import check_markdown_examples as m  # noqa: E402


ROSTER = frozenset({"pkg/core", "pkg/config", "starters/demo"})


def write_workspace(root: pathlib.Path, dirs, inline: bool = False) -> None:
    """Write a go.work listing dirs, in either legal spelling."""
    if inline:
        body = "".join(f"use ./{d}\n" for d in dirs)
    else:
        body = "use (\n" + "".join(f"\t./{d}\n" for d in dirs) + ")\n"
    (root / "go.work").write_text("go 1.25\n\n" + body, encoding="utf-8")


class InScopeTests(unittest.TestCase):
    def test_root_prose_is_in_scope(self):
        """Test that root prose is in scope."""
        self.assertTrue(m._in_scope("AGENTS.md", ROSTER))
        self.assertTrue(m._in_scope("README.md", ROSTER))

    def test_module_prose_is_in_scope(self):
        """Test that module prose is in scope."""
        self.assertTrue(m._in_scope("pkg/core/AGENTS.md", ROSTER))
        self.assertTrue(m._in_scope("pkg/core/README.md", ROSTER))
        self.assertTrue(m._in_scope("starters/demo/AGENTS.md", ROSTER))

    def test_prose_below_a_module_is_in_scope(self):
        """Test that prose below a module is in scope."""
        self.assertTrue(m._in_scope("pkg/config/source/file/AGENTS.md", ROSTER))

    def test_prose_outside_every_workspace_module_is_out_of_scope(self):
        """Test that prose outside every workspace module is out of scope."""
        self.assertFalse(m._in_scope("legacy/dbkit/AGENTS.md", ROSTER))
        self.assertFalse(m._in_scope("legacy/dbkit/README.md", ROSTER))
        self.assertFalse(m._in_scope("web/packages/ui-kit/README.md", ROSTER))
        self.assertFalse(m._in_scope("starters/other-app/README.md", ROSTER))

    def test_scope_follows_the_roster_rather_than_a_denylist(self):
        """Test that scope follows the roster rather than a denylist."""
        self.assertTrue(
            m._in_scope("legacy/dbkit/AGENTS.md", ROSTER | {"legacy/dbkit"})
        )

    def test_documentation_tree_is_out_of_scope(self):
        """Test that documentation tree is out of scope."""
        self.assertFalse(m._in_scope("docs/adr/0001-something.md", ROSTER))
        self.assertFalse(m._in_scope("docs/design/modules/design-core.md", ROSTER))
        self.assertFalse(m._in_scope("docs/site/AGENTS.md", ROSTER))

    def test_other_markdown_in_a_module_is_out_of_scope(self):
        """Test that other markdown in a module is out of scope."""
        self.assertFalse(m._in_scope("pkg/core/CHANGELOG.md", ROSTER))


class ReadWorkspaceTests(unittest.TestCase):
    def setUp(self):
        import shutil

        if shutil.which("go") is None:
            self.skipTest("go not on PATH")

    def test_block_and_inline_use_directives_yield_the_same_roster(self):
        """Test that block and inline use directives yield the same roster."""
        for inline in (False, True):
            with self.subTest(inline=inline), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                write_workspace(root, ["pkg/core", "pkg/config"], inline=inline)
                workspace = m.read_workspace(root)
                self.assertEqual(workspace.module_dirs, {"pkg/core", "pkg/config"})
                self.assertEqual(workspace.go_directive, "1.25")

    def test_workspace_listing_no_module_is_an_error(self):
        """Test that workspace listing no module is an error."""
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            (root / "go.work").write_text("go 1.25\n", encoding="utf-8")
            with self.assertRaises(m.WorkspaceError):
                m.read_workspace(root)

    def test_absent_workspace_is_an_error(self):
        """Test that absent workspace is an error."""
        with tempfile.TemporaryDirectory() as td:
            with self.assertRaises(m.WorkspaceError):
                m.read_workspace(pathlib.Path(td))


class NestedCheckoutTests(unittest.TestCase):
    def test_nested_worktree_corpus_is_not_discovered(self):
        """Test that nested worktree corpus is not discovered."""
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            (root / "AGENTS.md").write_text("# root\n", encoding="utf-8")
            wt = root / ".claude" / "worktrees" / "wt"
            wt.mkdir(parents=True)
            (wt / "AGENTS.md").write_text("# nested checkout\n", encoding="utf-8")
            (wt / "pkg" / "core").mkdir(parents=True)
            (wt / "pkg" / "core" / "README.md").write_text(
                "# nested readme\n", encoding="utf-8"
            )
            found = m.discover_markdown_files(root, ROSTER)
            self.assertEqual(found, ["AGENTS.md"])


class ExtractGoBlocksTests(unittest.TestCase):
    def test_single_block_extracted_with_line_numbers(self):
        """Test that single block extracted with line numbers."""
        text = "intro\n\n```go\npackage p\n```\n\nmore text\n"
        blocks = m.extract_go_blocks(text)
        self.assertEqual(len(blocks), 1)
        self.assertEqual(blocks[0].body, "package p")
        self.assertEqual(blocks[0].start_line, 3)
        self.assertEqual(blocks[0].end_line, 5)

    def test_multiple_blocks_extracted_independently(self):
        """Test that multiple blocks extracted independently."""
        text = "```go\npackage a\n```\ntext\n```go\npackage b\n```\n"
        blocks = m.extract_go_blocks(text)
        self.assertEqual(len(blocks), 2)
        self.assertEqual(blocks[0].body, "package a")
        self.assertEqual(blocks[1].body, "package b")

    def test_non_go_fence_is_ignored(self):
        """Test that non go fence is ignored."""
        text = "```python\nprint(1)\n```\n"
        self.assertEqual(m.extract_go_blocks(text), [])

    def test_gotemplate_fence_is_not_mistaken_for_go(self):
        """Test that gotemplate fence is not mistaken for go."""
        text = "```gotemplate\n{{ .Foo }}\n```\n"
        self.assertEqual(m.extract_go_blocks(text), [])

    def test_skip_marker_immediately_above_fence(self):
        """Test that skip marker immediately above fence."""
        text = "<!-- markdown-example: no-parse-check -->\n```go\nnonsense !!!\n```\n"
        blocks = m.extract_go_blocks(text)
        self.assertEqual(len(blocks), 1)
        self.assertTrue(blocks[0].skip)

    def test_skip_marker_tolerates_one_blank_line(self):
        """Test that skip marker tolerates one blank line."""
        text = "<!-- markdown-example: no-parse-check -->\n\n```go\nnonsense !!!\n```\n"
        blocks = m.extract_go_blocks(text)
        self.assertTrue(blocks[0].skip)

    def test_no_skip_marker_means_not_skipped(self):
        """Test that no skip marker means not skipped."""
        text = "some prose\n```go\npackage p\n```\n"
        blocks = m.extract_go_blocks(text)
        self.assertFalse(blocks[0].skip)

    def test_skip_marker_paragraphs_above_does_not_count(self):
        """Test that skip marker paragraphs above does not count."""
        text = (
            "<!-- markdown-example: no-parse-check -->\n"
            "\nSome unrelated paragraph in between.\n\n"
            "```go\npackage p\n```\n"
        )
        blocks = m.extract_go_blocks(text)
        self.assertFalse(blocks[0].skip)


class ClassifyTests(unittest.TestCase):
    def test_package_clause_is_complete(self):
        """Test that package clause is complete."""
        self.assertEqual(m.classify("package foo\n\nfunc F() {}"), "complete")

    def test_leading_comment_then_package_is_still_complete(self):
        """Test that leading comment then package is still complete."""
        self.assertEqual(
            m.classify("// Package foo does things.\npackage foo\n"), "complete"
        )

    def test_bare_statement_is_fragment(self):
        """Test that bare statement is fragment."""
        self.assertEqual(m.classify("x := 1\nreturn x"), "fragment")

    def test_bare_signature_is_fragment(self):
        """Test that bare signature is fragment."""
        self.assertEqual(m.classify("Migrations() dbkit.MigrationSet"), "fragment")

    def test_empty_block_is_fragment(self):
        """Test that empty block is fragment."""
        self.assertEqual(m.classify(""), "fragment")

    def test_leading_comment_mentioning_package_word_is_not_complete(self):
        """Test that leading comment mentioning package word is not complete."""
        self.assertEqual(
            m.classify("// package pkgcore\ntype TenantID string\n"), "fragment"
        )


class ModuleDirsForBlockTests(unittest.TestCase):
    def test_direct_module_import(self):
        """Test that direct module import."""
        body = 'import "github.com/vislake/speed/pkg/core"\n'
        self.assertEqual(m._module_dirs_for_block(body, ROSTER), ({"pkg/core"}, set()))

    def test_subpackage_import_maps_to_owning_module(self):
        """Test that subpackage import maps to owning module."""
        body = 'import "github.com/vislake/speed/pkg/config/source/file"\n'
        self.assertEqual(
            m._module_dirs_for_block(body, ROSTER), ({"pkg/config"}, set())
        )

    def test_multiple_distinct_modules(self):
        """Test that multiple distinct modules."""
        body = (
            'import (\n'
            '\t"github.com/vislake/speed/pkg/core"\n'
            '\t"github.com/vislake/speed/pkg/config"\n'
            ')\n'
        )
        self.assertEqual(
            m._module_dirs_for_block(body, ROSTER),
            ({"pkg/core", "pkg/config"}, set()),
        )

    def test_example_host_import(self):
        """Test that example host import."""
        body = 'import "github.com/vislake/speed/starters/demo"\n'
        self.assertEqual(
            m._module_dirs_for_block(body, ROSTER), ({"starters/demo"}, set())
        )

    def test_import_outside_the_workspace_is_unresolved(self):
        """Test that import outside the workspace is unresolved."""
        body = 'import "github.com/vislake/speed/legacy/pkgcore/i18n"\n'
        self.assertEqual(
            m._module_dirs_for_block(body, ROSTER),
            (set(), {"legacy/pkgcore/i18n"}),
        )

    def test_longest_matching_module_wins(self):
        """Test that longest matching module wins."""
        roster = ROSTER | {"pkg/config/plugin"}
        body = 'import "github.com/vislake/speed/pkg/config/plugin/extra"\n'
        self.assertEqual(
            m._module_dirs_for_block(body, roster), ({"pkg/config/plugin"}, set())
        )

    def test_no_speed_imports_yields_nothing(self):
        """Test that no speed imports yields nothing."""
        body = 'import "context"\n'
        self.assertEqual(m._module_dirs_for_block(body, ROSTER), (set(), set()))


class LocalReplacementsTests(unittest.TestCase):
    """The scratch module re-states every in-repository replacement its
    dependencies rely on."""

    def _tree(self, root: pathlib.Path) -> None:
        (root / "pkg" / "core").mkdir(parents=True)
        (root / "pkg" / "core" / "go.mod").write_text(
            "module github.com/vislake/speed/pkg/core\n\ngo 1.26.0\n",
            encoding="utf-8",
        )
        (root / "pkg" / "leaf").mkdir(parents=True)
        (root / "pkg" / "leaf" / "go.mod").write_text(
            "module github.com/vislake/speed/pkg/leaf\n\ngo 1.26.0\n\n"
            "replace github.com/vislake/speed/pkg/core => ../core\n\n"
            "require github.com/vislake/speed/pkg/core v0.0.0\n",
            encoding="utf-8",
        )

    def test_replacement_is_followed_transitively(self):
        """Test that replacement is followed transitively."""
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            self._tree(root)
            found = m._local_replacements(root, {"pkg/leaf"})
        self.assertEqual(set(found), {"pkg/leaf", "pkg/core"})

    def test_module_without_go_mod_is_skipped(self):
        """Test that module without go mod is skipped."""
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            self._tree(root)
            found = m._local_replacements(root, {"pkg/absent"})
        self.assertEqual(found, {})


class CheckFragmentTests(unittest.TestCase):
    def setUp(self):
        import shutil

        self.gofmt = shutil.which("gofmt")
        if self.gofmt is None:
            self.skipTest("gofmt not on PATH")

    def test_bare_method_signature_parses_under_toplevel(self):
        """Test that bare method signature parses under toplevel."""
        ok, strategy, _ = m.check_fragment(self.gofmt, "func Migrations() dbkit.MigrationSet")
        self.assertTrue(ok)
        self.assertEqual(strategy, "top-level")

    def test_interface_method_only_signature_needs_interface_wrap(self):
        """Test that interface method only signature needs interface wrap."""
        ok, strategy, _ = m.check_fragment(self.gofmt, "Migrations() dbkit.MigrationSet")
        self.assertTrue(ok)
        self.assertEqual(strategy, "interface-body")

    def test_statement_sequence_parses_under_func_body(self):
        """Test that statement sequence parses under func body."""
        body = textwrap.dedent(
            """\
            x, err := f()
            if err != nil {
                return err
            }
            _ = x
            """
        )
        ok, strategy, _ = m.check_fragment(self.gofmt, body)
        self.assertTrue(ok)
        self.assertEqual(strategy, "func-body")

    def test_bare_import_parses_under_toplevel(self):
        """Test that bare import parses under toplevel."""
        ok, strategy, _ = m.check_fragment(
            self.gofmt, 'import _ "github.com/vislake/speed/go/dbkit/dialect/sqlite"'
        )
        self.assertTrue(ok)
        self.assertEqual(strategy, "top-level")

    def test_ellipsis_placeholder_in_statement_position_is_a_violation(self):
        """Test that ellipsis placeholder in statement position is a violation."""
        body = (
            "if err := db.Where(cond).First(&got).Error; err != nil { ... }"
        )
        ok, strategy, err = m.check_fragment(self.gofmt, body)
        self.assertFalse(ok)
        self.assertEqual(strategy, "")
        self.assertTrue(err)

    def test_ellipsis_placeholder_in_call_position_is_a_violation(self):
        """Test that ellipsis placeholder in call position is a violation."""
        body = "tenancy.Middleware(authn.NewPrincipalResolver(), ...)(mux)"
        ok, strategy, err = m.check_fragment(self.gofmt, body)
        self.assertFalse(ok)
        self.assertEqual(strategy, "")

    def test_struct_field_list_needs_struct_wrap(self):
        """Test that struct field list needs struct wrap."""
        body = 'Email      string `gorm:"serializer:email_enc"`'
        ok, strategy, _ = m.check_fragment(self.gofmt, body)
        self.assertTrue(ok)
        self.assertIn(strategy, {"top-level", "struct-body", "const-block", "var-block"})

    def test_genuinely_unparseable_fragment_fails_everywhere(self):
        """Test that genuinely unparseable fragment fails everywhere."""
        ok, strategy, err = m.check_fragment(self.gofmt, "{{{ not go at all ]]]")
        self.assertFalse(ok)
        self.assertEqual(strategy, "")
        self.assertTrue(err)


class CheckCompleteBlockTests(unittest.TestCase):
    def setUp(self):
        import shutil

        if shutil.which("go") is None:
            self.skipTest("go not on PATH")

    workspace = m.Workspace("1.25", ROSTER)

    def test_self_contained_valid_program_builds_clean(self):
        """Test that self contained valid program builds clean."""
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            block = m.GoBlock(1, 3, "package p\n\nfunc F() int { return 1 }\n", False)
            violations = m.check_complete_block(
                root, "fake.md", block, self.workspace, keep_temp=False
            )
        self.assertEqual(violations, [])

    def test_broken_program_is_reported_as_a_violation(self):
        """Test that broken program is reported as a violation."""
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            block = m.GoBlock(
                1, 3, "package p\n\nfunc F() int { return undefinedThing() }\n", False
            )
            violations = m.check_complete_block(
                root, "fake.md", block, self.workspace, keep_temp=False
            )
        self.assertEqual(len(violations), 1)
        self.assertIn("fake.md:1", violations[0])
        self.assertIn("undefinedThing", violations[0])

    def test_import_outside_the_workspace_is_reported_without_a_build(self):
        """Test that import outside the workspace is reported without a build."""
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            body = (
                'package p\n\n'
                'import "github.com/vislake/speed/legacy/dbkit"\n\n'
                'func F() { dbkit.Foo() }\n'
            )
            block = m.GoBlock(1, 5, body, False)
            violations = m.check_complete_block(
                root, "fake.md", block, self.workspace, keep_temp=False
            )
        self.assertEqual(len(violations), 1)
        self.assertIn("legacy/dbkit", violations[0])
        self.assertIn("go.work", violations[0])

    def test_roster_entry_without_a_module_is_reported(self):
        """Test that roster entry without a module is reported."""
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            body = (
                'package p\n\n'
                'import "github.com/vislake/speed/pkg/core"\n\n'
                'func F() { core.Foo() }\n'
            )
            block = m.GoBlock(1, 5, body, False)
            violations = m.check_complete_block(
                root, "fake.md", block, self.workspace, keep_temp=False
            )
        self.assertEqual(len(violations), 1)
        self.assertIn("pkg/core/go.mod", violations[0])


class CheckCompleteBlockWidthTests(unittest.TestCase):
    """The go commands check_complete_block starts take their width from the caller."""

    workspace = m.Workspace("1.25", ROSTER)

    def _run(self) -> list:
        block = m.GoBlock(1, 3, "package p\n\nfunc F() int { return 1 }\n", False)
        with tempfile.TemporaryDirectory() as td:
            with mock.patch.object(m.subprocess, "run") as run:
                run.return_value = mock.Mock(returncode=0, stdout="", stderr="")
                violations = m.check_complete_block(
                    pathlib.Path(td), "fake.md", block, self.workspace, keep_temp=False
                )
        self.assertEqual(violations, [])
        self.assertEqual(len(run.call_args_list), 3, "tidy, build and vet")
        return run.call_args_list

    def test_the_go_commands_forward_the_width_and_keep_the_callers_flags(self):
        """Test that the go commands forward the width and keep the callers flags."""
        with mock.patch.dict(
            os.environ, {"GOMAXPROCS": "3", "GOFLAGS": "-p=3 -tags=probe"}
        ):
            calls = self._run()
        for call in calls:
            argv = call.args[0]
            env = call.kwargs["env"]
            self.assertEqual(
                env["GOMAXPROCS"], "3", f"{argv}: GOMAXPROCS is not forwarded"
            )
            flags = env["GOFLAGS"].split()
            p_items = [flag for flag in flags if flag.startswith("-p=")]
            self.assertEqual(
                p_items, ["-p=3"], f"{argv}: the caller's -p was rewritten"
            )
            self.assertIn("-p=3", flags, f"{argv}: the caller's -p is lost")
            self.assertIn("-tags=probe", flags, f"{argv}: the caller's -tags is lost")
            self.assertIn("-mod=mod", flags, f"{argv}: -mod=mod is missing")
            self.assertGreater(
                flags.index("-mod=mod"),
                flags.index("-p=3"),
                f"{argv}: -mod=mod must come after the caller's -p",
            )
            self.assertGreater(
                flags.index("-mod=mod"),
                flags.index("-tags=probe"),
                f"{argv}: -mod=mod must come after the caller's -tags",
            )
            self.assertFalse(
                [arg for arg in argv if arg == "-p" or arg.startswith("-p=")],
                f"{argv}: the script must not supply its own -p",
            )

    def test_the_script_does_not_invent_a_width_when_the_caller_set_none(self):
        """Test that the script does not invent a width when the caller set none."""
        with mock.patch.dict(os.environ, {}):
            os.environ.pop("GOMAXPROCS", None)
            os.environ.pop("GOFLAGS", None)
            calls = self._run()
        for call in calls:
            env = call.kwargs["env"]
            self.assertNotIn(
                "GOMAXPROCS",
                env,
                f"{call.args[0]}: the script set a width nobody gave it",
            )
            self.assertEqual(
                env["GOFLAGS"],
                "-mod=mod",
                f"{call.args[0]}: the script's GOFLAGS is not exactly -mod=mod",
            )


class MainExitCodeTests(unittest.TestCase):
    def _tree(self, root: pathlib.Path, prose: str, rel: str = "AGENTS.md") -> None:
        write_workspace(root, ["pkg/core"])
        target = root / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(prose, encoding="utf-8")

    def test_missing_root_is_infrastructure_error(self):
        """Test that missing root is infrastructure error."""
        rc = m.main(["--root", "/this/path/does/not/exist/at/all"])
        self.assertEqual(rc, 2)

    def test_unreadable_roster_is_infrastructure_error(self):
        """Test that unreadable roster is infrastructure error."""
        with tempfile.TemporaryDirectory() as td:
            rc = m.main(["--root", td])
        self.assertEqual(rc, 2)

    def test_survey_mode_exits_zero(self):
        """Test that survey mode exits zero."""
        with tempfile.TemporaryDirectory() as td:
            self._tree(pathlib.Path(td), "# Fake\n")
            rc = m.main(["--root", td, "--survey"])
        self.assertEqual(rc, 0)

    def test_clean_tree_exits_zero(self):
        """Test that clean tree exits zero."""
        with tempfile.TemporaryDirectory() as td:
            self._tree(
                pathlib.Path(td),
                "# Fake\n\n```go\npackage p\n\nfunc F() {}\n```\n",
            )
            rc = m.main(["--root", td])
        self.assertEqual(rc, 0)

    def test_tree_with_a_broken_fragment_exits_one(self):
        """Test that tree with a broken fragment exits one."""
        with tempfile.TemporaryDirectory() as td:
            self._tree(pathlib.Path(td), "# Fake\n\n```go\nnot go at all {{{ ]]]\n```\n")
            rc = m.main(["--root", td])
        self.assertEqual(rc, 1)

    def test_broken_block_outside_the_workspace_is_not_checked(self):
        """Test that broken block outside the workspace is not checked."""
        with tempfile.TemporaryDirectory() as td:
            self._tree(
                pathlib.Path(td),
                "# Fake\n\n```go\nnot go at all {{{ ]]]\n```\n",
                rel="legacy/dbkit/AGENTS.md",
            )
            rc = m.main(["--root", td])
        self.assertEqual(rc, 0)

    def test_broken_block_inside_the_workspace_is_checked(self):
        """Test that broken block inside the workspace is checked."""
        with tempfile.TemporaryDirectory() as td:
            self._tree(
                pathlib.Path(td),
                "# Fake\n\n```go\nnot go at all {{{ ]]]\n```\n",
                rel="pkg/core/AGENTS.md",
            )
            rc = m.main(["--root", td])
        self.assertEqual(rc, 1)

    def test_skip_marker_lets_a_broken_fragment_pass(self):
        """Test that skip marker lets a broken fragment pass."""
        with tempfile.TemporaryDirectory() as td:
            self._tree(
                pathlib.Path(td),
                "<!-- markdown-example: no-parse-check -->\n"
                "```go\nnot go at all {{{ ]]]\n```\n",
            )
            rc = m.main(["--root", td])
        self.assertEqual(rc, 0)


if __name__ == "__main__":
    unittest.main()
