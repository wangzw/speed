#!/usr/bin/env python3
"""Planted-violation suite for check_documents.py.

Stdlib-only (unittest + tempfile), matching the repository's "plain
executables with no third-party dependencies" convention. Run directly:

    python3 .claude/skills/document-standards/scripts/test_check_documents.py

Every test builds a complete, conforming documentation tree in a
temporary directory, plants exactly one structural violation in it, and
asserts that the checker goes red naming that violation. The conforming
baseline is asserted green first, in both supported languages -- without
it a checker that reports everything would pass the whole suite.
"""

from __future__ import annotations

import contextlib
import io
import os
import pathlib
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import check_documents as m  # noqa: E402

CLAUDE_MD = """# Project

## Documentation

- design: docs/design
- adr: docs/adr
- glossary: docs/glossary.md
- references: docs/references.md
- language: en
"""

GLOSSARY = "# Glossary\n\nComponent — an implementation of a module interface.\n"

REFERENCES = """# References

| Id | Name | What it is | Source | Version |
|---|---|---|---|---|
| REF-widgetlib | widgetlib | open-source registry library | https://example.org/widgetlib | 2.1.0 |
"""

DESIGN_README = """# Widget System

A system that does one thing.

## Index

| Document | Content |
|---|---|
| [Architecture](architecture.md) | shape and invariants |
| [core](modules/design-core.md) | registration and lifecycle |
"""

ARCHITECTURE = """# Architecture

## Overview

One binary, composed of modules. The `core` module embeds the widgetlib
registry ([REF-widgetlib](../references.md)).

## Module Dependency Graph

Dependencies flow bottom-up.

```mermaid
graph TD
  host --> core
```

## Logical Deployment Architecture

A single process.

```mermaid
graph TD
  process --> database
```

## Cross-Cutting Design

### Error Model

Errors carry a code.

## Architecture Invariants

- A module never imports a module above it.

## Implementation Status

| Module | Status |
|---|---|
| [core](modules/design-core.md) | Not implemented |
"""

MODULE = """# core

## Responsibilities

Owns registration.

### Non-Responsibilities

Does not own assembly; the host owns it.

## External Interface Contract

`Register` is stable across a major version.

## Dependencies

Nothing below it.

## Implementation Status

| Capability | Status | Code |
|---|---|---|
| registration | Not implemented | — |
"""

ADR_README = """# Architecture Decision Records

Every record states the constraints, the options and the price paid.

## Core

| Record |
|---|
| [core owns registration](adr-core-scope.md) |
"""

ADR_RECORD = """# core owns registration

## Context

A host needs one place to register components.

## Options Considered

### A registry in core

Adopting it would put registration below every module.

### A registry in the host

Rejected: every host would reimplement it.

## Decision

Adopt a registry in core.

## Consequences

Registration is uniform. The price is that core carries the registry's
own compatibility burden.
"""

BASELINE = {
    "CLAUDE.md": CLAUDE_MD,
    "docs/glossary.md": GLOSSARY,
    "docs/references.md": REFERENCES,
    "docs/design/README.md": DESIGN_README,
    "docs/design/architecture.md": ARCHITECTURE,
    "docs/design/modules/design-core.md": MODULE,
    "docs/adr/README.md": ADR_README,
    "docs/adr/adr-core-scope.md": ADR_RECORD,
}


class CheckerCase(unittest.TestCase):
    """Builds a tree, runs the checker on it, returns (exit code, report)."""

    def run_on(self, overrides: dict[str, str | None] | None = None,
               base: dict[str, str] | None = None) -> tuple[int, str]:
        files = dict(base if base is not None else BASELINE)
        for path, content in (overrides or {}).items():
            if content is None:
                files.pop(path, None)
            else:
                files[path] = content
        with tempfile.TemporaryDirectory() as td:
            for path, content in files.items():
                target = pathlib.Path(td) / path
                target.parent.mkdir(parents=True, exist_ok=True)
                if isinstance(content, bytes):
                    target.write_bytes(content)
                else:
                    target.write_text(content, encoding="utf-8")
            out, err = io.StringIO(), io.StringIO()
            with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
                code = m.main(["--root", td])
            return code, out.getvalue() + err.getvalue()

    def assert_green(self, overrides=None, base=None):
        code, report = self.run_on(overrides, base)
        self.assertEqual(code, 0, f"expected a clean tree, got:\n{report}")

    def assert_red(self, overrides, needle: str, base=None):
        code, report = self.run_on(overrides, base)
        self.assertEqual(code, 1, f"expected a finding, got exit {code}:\n{report}")
        self.assertIn(needle, report)


class BaselineTests(CheckerCase):
    def test_conforming_tree_passes(self):
        self.assert_green()

    def test_missing_documentation_block_is_a_usage_error(self):
        code, report = self.run_on({"CLAUDE.md": "# Project\n"})
        self.assertEqual(code, 2)
        self.assertIn("Documentation", report)

    def test_unknown_language_is_a_usage_error(self):
        code, report = self.run_on({"CLAUDE.md": CLAUDE_MD.replace("language: en",
                                                                   "language: fr")})
        self.assertEqual(code, 2)
        self.assertIn("unsupported documentation language", report)

    def test_absent_trees_are_skipped_not_failed(self):
        # The design tree carried the only citation of REF-widgetlib; a
        # project without it registers nothing, or the entry is an orphan.
        code, report = self.run_on(
            {"docs/design/README.md": None, "docs/design/architecture.md": None,
             "docs/design/modules/design-core.md": None,
             "docs/references.md": "# References\n"},
        )
        self.assertEqual(code, 0, report)
        self.assertNotIn("docs/design", report.replace("design: docs/design", ""))

    def test_missing_glossary_is_reported(self):
        self.assert_red({"docs/glossary.md": None}, "glossary does not exist")


class CommonRuleTests(CheckerCase):
    def test_metadata_field_line(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         MODULE.replace("# core\n", "# core\n\n- **Author**: a person\n")},
                        "process metadata field")

    def test_change_history_section(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         MODULE + "\n## Change History\n\nFirst version.\n"},
                        "process metadata section")

    def test_task_marker(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         MODULE.replace("Owns registration.", "Owns registration. TODO: split.")},
                        "project-management marker")

    def test_code_reference_with_line_number(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         MODULE.replace("`Register` is stable",
                                        "`registry.go:42` is stable")},
                        "line number")

    def test_second_h1(self):
        self.assert_red({"docs/design/modules/design-core.md": MODULE + "\n# core again\n"},
                        "second H1")

    def test_empty_mermaid_body(self):
        broken = ARCHITECTURE.replace("graph TD\n  host --> core\n", "")
        self.assert_red({"docs/design/architecture.md": broken}, "empty mermaid block")

    def test_mermaid_with_only_a_type_keyword(self):
        broken = ARCHITECTURE.replace("graph TD\n  host --> core\n", "classDiagram\n")
        self.assert_red({"docs/design/architecture.md": broken}, "renders blank")

    def test_diagram_without_prose(self):
        broken = ARCHITECTURE.replace("Dependencies flow bottom-up.\n\n", "")
        self.assert_red({"docs/design/architecture.md": broken}, "and no prose")

    def test_template_comments_are_not_document_content(self):
        self.assert_green({"docs/design/modules/design-core.md":
                           "<!-- TODO: delete this comment. Author: nobody. -->\n" + MODULE})


class DesignTreeTests(CheckerCase):
    def test_orphan_module_document(self):
        self.assert_red({"docs/design/modules/design-extra.md":
                         MODULE.replace("# core", "# extra")},
                        "absent from the design index")

    def test_index_link_to_nothing(self):
        self.assert_red({"docs/design/README.md":
                         DESIGN_README + "| [gone](modules/design-gone.md) | gone |\n"},
                        "points at no file")

    def test_duplicate_index_entry(self):
        self.assert_red({"docs/design/README.md":
                         DESIGN_README + "| [core again](modules/design-core.md) | again |\n"},
                        "more than once")

    def test_tree_index_missing_the_architecture_row(self):
        self.assert_red({"docs/design/README.md":
                         DESIGN_README.replace(
                             "| [Architecture](architecture.md) | shape and invariants |\n", "")},
                        "absent from the design index")

    def test_sequence_number_in_file_name(self):
        self.assert_red({"docs/design/modules/design-core.md": None,
                         "docs/design/modules/design-01-core.md": MODULE,
                         "docs/design/README.md":
                         DESIGN_README.replace("modules/design-core.md",
                                               "modules/design-01-core.md"),
                         "docs/design/architecture.md":
                         ARCHITECTURE.replace("modules/design-core.md",
                                              "modules/design-01-core.md")},
                        "sequence number")

    def test_missing_non_responsibilities(self):
        broken = MODULE.replace("### Non-Responsibilities\n\nDoes not own assembly; "
                                "the host owns it.\n\n", "")
        self.assert_red({"docs/design/modules/design-core.md": broken},
                        "Non-Responsibilities")

    def test_module_section_out_of_order(self):
        broken = MODULE.replace(
            "## External Interface Contract\n\n`Register` is stable across a major version.\n\n"
            "## Dependencies\n\nNothing below it.\n",
            "## Dependencies\n\nNothing below it.\n\n"
            "## External Interface Contract\n\n`Register` is stable across a major version.\n")
        self.assert_red({"docs/design/modules/design-core.md": broken},
                        "the section order is fixed")

    def test_module_section_outside_the_standard_set(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         MODULE + "\n## Roadmap\n\nLater.\n"},
                        "not part of this document type's fixed section set")

    def test_local_glossary_section_in_its_seat(self):
        glossary = """## Glossary

| Term | Scope | Definition |
|---|---|---|
| seat | this module | a declared position in the fixed section order |

"""
        self.assert_green({"docs/design/modules/design-core.md":
                           MODULE.replace("## External Interface Contract",
                                          glossary + "## External Interface Contract")})
        self.assert_red({"docs/design/modules/design-core.md":
                         MODULE.replace("## Implementation Status",
                                        glossary + "## Implementation Status")},
                        "the section order is fixed")

    def test_missing_mandatory_architecture_section(self):
        broken = ARCHITECTURE.replace("## Architecture Invariants\n\n"
                                      "- A module never imports a module above it.\n\n", "")
        self.assert_red({"docs/design/architecture.md": broken},
                        "missing mandatory section 'Architecture Invariants'")

    def test_mandatory_diagram_missing(self):
        broken = ARCHITECTURE.replace("```mermaid\ngraph TD\n  host --> core\n```\n\n", "")
        self.assert_red({"docs/design/architecture.md": broken},
                        "this diagram is mandatory")

    def test_status_value_outside_the_vocabulary(self):
        broken = MODULE.replace("| registration | Not implemented | — |",
                                "| registration | mostly complete | — |")
        self.assert_red({"docs/design/modules/design-core.md": broken}, "vague status")

    def test_implemented_without_code_anchor(self):
        broken = MODULE.replace("| registration | Not implemented | — |",
                                "| registration | Implemented | — |")
        self.assert_red({"docs/design/modules/design-core.md": broken}, "no code anchor")

    def test_partial_status_with_a_code_anchor_passes(self):
        self.assert_green({"docs/design/modules/design-core.md": MODULE.replace(
            "| registration | Not implemented | — |",
            "| registration | Partially implemented | `registry.go` `Register` |")})

    def test_roll_up_missing_a_module(self):
        broken = ARCHITECTURE.replace("| [core](modules/design-core.md) | Not implemented |\n", "")
        self.assert_red({"docs/design/architecture.md": broken}, "has no row for")


SUBDIR_FILES = dict(BASELINE)
SUBDIR_FILES["docs/design/README.md"] = """# Widget System

A system that does one thing.

## Index

| Document | Content |
|---|---|
| [Architecture](architecture.md) | shape and invariants |
| [core](modules/design-core.md) | registration and lifecycle |
| [api](modules/api/README.md) | the api module and its sub-modules |
"""
SUBDIR_FILES["docs/design/architecture.md"] = ARCHITECTURE.replace(
    "| [core](modules/design-core.md) | Not implemented |",
    "| [core](modules/design-core.md) | Not implemented |\n"
    "| [api](modules/api/design-api.md) | Not implemented |")
SUBDIR_FILES["docs/design/modules/api/README.md"] = """# api

The api module.

## Index

| Document | Content |
|---|---|
| [api](design-api.md) | the module itself |
| [api-gate](design-api-gate.md) | the gate sub-module |

## Implementation Status

| Sub-module | Status |
|---|---|
| [api-gate](design-api-gate.md) | Not implemented |
"""
SUBDIR_FILES["docs/design/modules/api/design-api.md"] = MODULE.replace("# core", "# api")
SUBDIR_FILES["docs/design/modules/api/design-api-gate.md"] = MODULE.replace("# core", "# api-gate")


class ModuleSubdirectoryTests(CheckerCase):
    def test_conforming_subdirectory_tree_passes(self):
        self.assert_green(base=SUBDIR_FILES)

    def test_architecture_row_for_a_sub_module(self):
        broken = SUBDIR_FILES["docs/design/architecture.md"].replace(
            "| [api](modules/api/design-api.md) | Not implemented |",
            "| [api](modules/api/design-api.md) | Not implemented |\n"
            "| [api-gate](modules/api/design-api-gate.md) | Not implemented |")
        self.assert_red({"docs/design/architecture.md": broken},
                        "carries a sub-module row", base=SUBDIR_FILES)

    def test_missing_sub_module_status_row(self):
        broken = SUBDIR_FILES["docs/design/modules/api/README.md"].replace(
            "| [api-gate](design-api-gate.md) | Not implemented |\n", "")
        self.assert_red({"docs/design/modules/api/README.md": broken},
                        "sub-module status has no row for", base=SUBDIR_FILES)

    def test_module_subdirectory_index_without_an_index_section(self):
        broken = SUBDIR_FILES["docs/design/modules/api/README.md"].replace("## Index", "## Contents")
        self.assert_red({"docs/design/modules/api/README.md": broken},
                        "no 'Index' section", base=SUBDIR_FILES)

    def test_document_without_the_module_slug_prefix(self):
        files = dict(SUBDIR_FILES)
        files["docs/design/modules/api/design-gate.md"] = MODULE.replace("# core", "# gate")
        files["docs/design/modules/api/design-api-gate.md"] = None
        files["docs/design/modules/api/README.md"] = (
            SUBDIR_FILES["docs/design/modules/api/README.md"]
            .replace("design-api-gate.md", "design-gate.md"))
        self.assert_red(files, "does not match design-api")

    def test_module_subdirectory_without_its_own_document(self):
        files = dict(SUBDIR_FILES)
        files["docs/design/modules/api/design-api.md"] = None
        files["docs/design/modules/api/README.md"] = (
            SUBDIR_FILES["docs/design/modules/api/README.md"]
            .replace("| [api](design-api.md) | the module itself |\n", ""))
        files["docs/design/architecture.md"] = ARCHITECTURE.replace(
            "| [core](modules/design-core.md) | Not implemented |",
            "| [core](modules/design-core.md) | Not implemented |\n"
            "| [api](modules/api/design-api.md) | Not implemented |")
        self.assert_red(files, "has no design-api.md")

    def test_orphan_inside_a_module_subdirectory(self):
        files = dict(SUBDIR_FILES)
        files["docs/design/modules/api/design-api-extra.md"] = MODULE.replace("# core", "# extra")
        self.assert_red(files, "absent from the module index")

    def test_document_name_beyond_the_slug_prefix(self):
        files = dict(SUBDIR_FILES)
        files["docs/design/modules/api/design-apix.md"] = MODULE.replace("# core", "# apix")
        self.assert_red(files, "does not match design-api")

    def test_sub_module_status_row_for_the_module_itself(self):
        broken = SUBDIR_FILES["docs/design/modules/api/README.md"].replace(
            "| [api-gate](design-api-gate.md) | Not implemented |",
            "| [api-gate](design-api-gate.md) | Not implemented |\n"
            "| [api](design-api.md) | Not implemented |")
        self.assert_red({"docs/design/modules/api/README.md": broken},
                        "module's own document", base=SUBDIR_FILES)

    def test_module_index_heading_in_the_other_language(self):
        broken = SUBDIR_FILES["docs/design/modules/api/README.md"].replace("## Index", "## 索引")
        self.assert_red({"docs/design/modules/api/README.md": broken},
                        "is written in zh-CN", base=SUBDIR_FILES)


class AdrTreeTests(CheckerCase):
    def test_status_section_is_refused(self):
        self.assert_red({"docs/adr/adr-core-scope.md":
                         ADR_RECORD.replace("## Context",
                                            "## Status\n\nAccepted\n\n## Context")},
                        "records a status")

    def test_status_heading_at_level_three_is_refused(self):
        self.assert_red({"docs/adr/adr-core-scope.md":
                         ADR_RECORD.replace("A host needs one place to register components.",
                                            "### Status\n\nAccepted\n\n"
                                            "A host needs one place to register components.")},
                        "records a status")

    def test_status_field_line_is_refused(self):
        self.assert_red({"docs/adr/adr-core-scope.md":
                         ADR_RECORD.replace("## Context\n",
                                            "## Context\n\n- **Status**: Accepted\n")},
                        "status field")

    def test_supersession_line_is_refused(self):
        self.assert_red({"docs/adr/adr-core-scope.md":
                         ADR_RECORD.replace("## Context\n",
                                            "## Context\n\nSuperseded by ADR-core-rights.\n")},
                        "supersession line")

    def test_single_option(self):
        broken = ADR_RECORD.replace("### A registry in the host\n\nRejected: every host "
                                    "would reimplement it.\n\n", "")
        self.assert_red({"docs/adr/adr-core-scope.md": broken},
                        "lists 1 option(s)")

    def test_file_name_with_a_date_suffix(self):
        self.assert_red({"docs/adr/adr-core-scope.md": None,
                         "docs/adr/adr-core-scope-2026-01-02.md": ADR_RECORD,
                         "docs/adr/README.md":
                         ADR_README.replace("adr-core-scope.md",
                                            "adr-core-scope-2026-01-02.md")},
                        "file name does not match adr-<slug>;")

    def test_date_file_name_is_not_reported_as_a_sequence_number(self):
        code, report = self.run_on({
            "docs/adr/adr-core-scope.md": None,
            "docs/adr/adr-core-scope-2026-01-02.md": ADR_RECORD,
            "docs/adr/README.md": ADR_README.replace(
                "adr-core-scope.md", "adr-core-scope-2026-01-02.md")})
        self.assertEqual(code, 1)
        self.assertIn("file name does not match adr-<slug>;", report)
        self.assertNotIn("carries a sequence number", report)

    def test_file_name_with_a_date_in_the_middle(self):
        self.assert_red({"docs/adr/adr-core-scope.md": None,
                         "docs/adr/adr-2026-01-02-core-scope.md": ADR_RECORD,
                         "docs/adr/README.md":
                         ADR_README.replace("adr-core-scope.md",
                                            "adr-2026-01-02-core-scope.md")},
                        "does not match adr-<slug>")

    def test_file_name_with_a_sequence_number(self):
        second = ADR_RECORD.replace("# core owns registration", "# second scope decision")
        self.assert_red({"docs/adr/adr-core-scope-2.md": second,
                         "docs/adr/README.md":
                         ADR_README.replace(
                             "| [core owns registration](adr-core-scope.md) |",
                             "| [core owns registration](adr-core-scope.md) |\n"
                             "| [second scope decision](adr-core-scope-2.md) |")},
                        "sequence number")

    def test_number_inside_a_name_is_not_a_sequence(self):
        record = ADR_RECORD.replace("# core owns registration", "# TLS 1.3 only")
        self.assert_green({"docs/adr/adr-tls-1-3.md": record,
                           "docs/adr/README.md":
                           ADR_README + "| [TLS 1.3 only](adr-tls-1-3.md) |\n"})

    def test_index_row_uses_the_record_title(self):
        self.assert_red({"docs/adr/README.md":
                         ADR_README.replace("[core owns registration]",
                                            "[a registry lives in core]")},
                        "does not use the record's own title")

    def test_two_records_may_share_a_topic(self):
        second = (ADR_RECORD.replace("# core owns registration", "# core owns nothing")
                  .replace("Adopt a registry in core.", "Adopt a registry in the host."))
        self.assert_green({
            "docs/adr/adr-core-rights.md": second,
            "docs/adr/README.md": ADR_README + (
                "| [core owns nothing](adr-core-rights.md) |\n"),
        })

    def test_index_with_a_date_column(self):
        self.assert_red({"docs/adr/README.md":
                         ADR_README.replace("| Record |\n|---|",
                                            "| Record | Date |\n|---|---|")
                                   .replace("(adr-core-scope.md) |",
                                            "(adr-core-scope.md) | 2026-01-02 |")},
                        "date or status column")

    def test_index_with_a_status_column(self):
        self.assert_red({"docs/adr/README.md":
                         ADR_README.replace("| Record |\n|---|",
                                            "| Record | Status |\n|---|---|")
                                   .replace("(adr-core-scope.md) |",
                                            "(adr-core-scope.md) | Accepted |")},
                        "date or status column")

    def test_index_with_a_state_column(self):
        self.assert_red({"docs/adr/README.md":
                         ADR_README.replace("| Record |\n|---|",
                                            "| Record | State |\n|---|---|")
                                   .replace("(adr-core-scope.md) |",
                                            "(adr-core-scope.md) | active |")},
                        "date or status column")

    def test_index_forbidden_column_vocabulary(self):
        for token in ("State", "Acceptance", "Deprecated"):
            with self.subTest(token=token):
                self.assert_red({"docs/adr/README.md":
                                 ADR_README.replace("| Record |\n|---|",
                                                    f"| Record | {token} |\n|---|---|")
                                           .replace("(adr-core-scope.md) |",
                                                    "(adr-core-scope.md) | x |")},
                                "date or status column")

    def test_index_without_leading_pipes_is_checked(self):
        self.assert_red({"docs/adr/README.md":
                         "# Architecture Decision Records\n\nRecords.\n\n## Core\n\n"
                         "Record | Date\n---|---\n"
                         "[core owns registration](adr-core-scope.md) | 2026-01-02\n"},
                        "date or status column")

    def test_non_index_table_is_not_checked_as_the_index(self):
        self.assert_green({"docs/adr/README.md":
                           ADR_README + "\n## Legend\n\n| Term | Status |\n|---|---|\n"
                                        "| core | stable |\n"})

    def test_index_without_leading_pipes_title_is_checked(self):
        self.assert_red({"docs/adr/README.md":
                         "# Architecture Decision Records\n\nRecords.\n\n## Core\n\n"
                         "Record | Note\n---|---\n"
                         "[a registry lives in core](adr-core-scope.md) | setup\n"},
                        "does not use the record's own title")

    def test_setext_status_heading_is_refused(self):
        self.assert_red({"docs/adr/adr-core-scope.md":
                         ADR_RECORD.replace("## Context",
                                            "Status\n------\n\nAccepted\n\n## Context")},
                        "records a status")

    def test_orphan_record(self):
        self.assert_red({"docs/adr/adr-config-source.md":
                         ADR_RECORD.replace("# core owns registration", "# one config source")},
                        "absent from the ADR index")

    def test_index_not_grouped_by_topic(self):
        self.assert_red({"docs/adr/README.md":
                         "# Architecture Decision Records\n\nRecords.\n\n"
                         "| [core owns registration](adr-core-scope.md) | Accepted |\n"},
                        "not grouped by topic")


ZH_FILES = {
    "CLAUDE.md": CLAUDE_MD.replace("language: en", "language: zh-CN"),
    "docs/glossary.md": "# 术语表\n\n组件 — 模块接口的一个实现。\n",
    "docs/references.md": """# 外部引用

| 编号 | 名称 | 说明 | 来源 | 版本 |
|---|---|---|---|---|
| REF-widgetlib | widgetlib | 开源注册表库 | https://example.org/widgetlib | 2.1.0 |
""",
    "docs/design/README.md": """# 组件系统

一个只做一件事的系统。

## 索引

| 文档 | 内容 |
|---|---|
| [架构](architecture.md) | 形状与不变量 |
| [core](modules/design-core.md) | 注册与生命周期 |
""",
    "docs/design/architecture.md": """# 架构

## 概览

单进程，由模块组成。`core` 模块内嵌 widgetlib 注册表（[REF-widgetlib](../references.md)）。

## 模块依赖图

依赖自下而上。

```mermaid
graph TD
  host --> core
```

## 逻辑部署架构

单进程。

```mermaid
graph TD
  process --> database
```

## 跨切面设计

### 错误模型

错误携带错误码。

## 架构不变量

- 模块不引用位于其上方的模块。

## 实现状态

| 模块 | 状态 |
|---|---|
| [core](modules/design-core.md) | 未实现 |
""",
    "docs/design/modules/design-core.md": """# core

## 职责

拥有注册。

### 非职责

不拥有装配，装配由宿主拥有。

## 外部接口契约

`Register` 在大版本内保持稳定。

## 依赖

不依赖任何模块。

## 实现状态

| 能力 | 状态 | 代码 |
|---|---|---|
| 注册 | 未实现 | — |
""",
    "docs/adr/README.md": """# 架构决策记录

每条记录说明约束、考虑过的选项以及付出的代价。

## 模块机制

| 记录 |
|---|
| [core 拥有注册](adr-core-scope.md) |
""",
    "docs/adr/adr-core-scope.md": """# core 拥有注册

## 背景

宿主需要一个统一的组件注册位置。

## 考虑过的选项

### 注册表放在 core

采纳它意味着注册位于所有模块之下。

### 注册表放在宿主

否决理由：每个宿主都要重新实现一遍。

## 决策

采纳注册表放在 core。

## 后果

注册方式统一。代价是 core 需要承担注册表自身的兼容负担。
""",
}


class ChineseTreeTests(CheckerCase):
    def test_conforming_chinese_tree_passes(self):
        self.assert_green(base=ZH_FILES)

    def test_heading_in_the_other_language(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         ZH_FILES["docs/design/modules/design-core.md"]
                         .replace("## 依赖", "## Dependencies")},
                        "is written in en", base=ZH_FILES)

    def test_diagrams_section_under_its_canonical_name(self):
        diagrams = """## UML 图

组件之间的引用关系如下。

```mermaid
classDiagram
  class Registry {
    +Register()
  }
```

"""
        module = ZH_FILES["docs/design/modules/design-core.md"]
        self.assert_green(
            {"docs/design/modules/design-core.md":
             module.replace("## 实现状态",
                            diagrams + "## 实现状态")},
            base=ZH_FILES)
        self.assert_red(
            {"docs/design/modules/design-core.md":
             module.replace("## 实现状态",
                            diagrams.replace("## UML 图", "## 图")
                            + "## 实现状态")},
            "not part of this document type's fixed section set", base=ZH_FILES)

    def test_chinese_status_vocabulary(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         ZH_FILES["docs/design/modules/design-core.md"]
                         .replace("| 注册 | 未实现 | — |", "| 注册 | 基本完成 | — |")},
                        "vague status", base=ZH_FILES)

    def test_chinese_supersession_line_is_refused(self):
        self.assert_red({"docs/adr/adr-core-scope.md":
                         ZH_FILES["docs/adr/adr-core-scope.md"]
                         .replace("## 背景\n", "## 背景\n\n已被 ADR-core-rights 取代\n")},
                        "supersession line", base=ZH_FILES)

    def test_chinese_status_heading_at_level_three_is_refused(self):
        self.assert_red({"docs/adr/adr-core-scope.md":
                         ZH_FILES["docs/adr/adr-core-scope.md"]
                         .replace("## 背景", "### 状态\n\n已接受\n\n## 背景")},
                        "records a status", base=ZH_FILES)

    def test_chinese_status_section_is_refused(self):
        self.assert_red({"docs/adr/adr-core-scope.md":
                         ZH_FILES["docs/adr/adr-core-scope.md"]
                         .replace("## 背景", "## 状态\n\n已接受\n\n## 背景")},
                        "records a status", base=ZH_FILES)

    def test_chinese_index_with_a_deprecated_column(self):
        self.assert_red({"docs/adr/README.md":
                         ZH_FILES["docs/adr/README.md"]
                         .replace("| 记录 |\n|---|", "| 记录 | 已废弃 |\n|---|---|")
                         .replace("(adr-core-scope.md) |", "(adr-core-scope.md) | x |")},
                        "date or status column", base=ZH_FILES)

    def test_chinese_index_with_a_status_column(self):
        self.assert_red({"docs/adr/README.md":
                         ZH_FILES["docs/adr/README.md"]
                         .replace("| 记录 |\n|---|", "| 记录 | 状态 |\n|---|---|")
                         .replace("(adr-core-scope.md) |", "(adr-core-scope.md) | 已接受 |")},
                        "date or status column", base=ZH_FILES)

    def test_chinese_index_with_a_date_column(self):
        self.assert_red({"docs/adr/README.md":
                         ZH_FILES["docs/adr/README.md"]
                         .replace("| 记录 |\n|---|", "| 记录 | 日期 |\n|---|---|")
                         .replace("(adr-core-scope.md) |", "(adr-core-scope.md) | 2026-01-02 |")},
                        "date or status column", base=ZH_FILES)


PRD_FILES = dict(BASELINE)
PRD_FILES["CLAUDE.md"] = CLAUDE_MD.replace("- design: docs/design",
                                           "- design: docs/design\n- requirements: docs/prd")
PRD_FILES["docs/prd/README.md"] = """# Widget System — Requirements

What the system must do.

## Index

| Document | Content |
|---|---|
| [Overview](overview.md) | scope boundary |

### Journeys

| Journey | Path |
|---|---|
| [Sign a document](journeys/journey-sign-document.md) | signing |

### Capabilities

| Capability | Content |
|---|---|
| [signing](features/prd-signing.md) | signing requirements |
"""
PRD_FILES["docs/prd/overview.md"] = """# System Overview

## What the System Is

A signing service.

## Capabilities

- Signing.

## Scope Boundary

Signing only.

### Out of Scope

Key custody.
"""
PRD_FILES["docs/prd/features/prd-signing.md"] = """# Signing

## Problem Statement

Documents must be signed.

## Functional Requirements

### REQ-signing-001

The system MUST sign a submitted document.

- **Acceptance**: the response carries a signature the public key verifies
- **Status**: Not implemented
- **E2E test**: Not implemented
"""
PRD_FILES["docs/prd/journeys/journey-sign-document.md"] = """# Sign a document

## Who

A signer holding an account.

## Precondition

The account holds a key pair.

## Steps

1. Submit a document.
2. Confirm the signature.

## Outcome

The signed document is downloadable.

## End-to-End Test

`e2e/signing.spec.ts` `signs a document`
"""


class RequirementsTreeTests(CheckerCase):
    def test_conforming_requirements_tree_passes(self):
        self.assert_green(base=PRD_FILES)

    def test_index_groups_may_be_h2(self):
        self.assert_green({"docs/prd/README.md":
                           PRD_FILES["docs/prd/README.md"]
                           .replace("### Journeys", "## Journeys")
                           .replace("### Capabilities", "## Capabilities")},
                          base=PRD_FILES)

    def test_local_glossary_section_in_its_seat(self):
        glossary = """## Glossary

| Term | Scope | Definition |
|---|---|---|
| signature | this capability | the detached bytes the verifier checks |

"""
        self.assert_green({"docs/prd/features/prd-signing.md":
                           PRD_FILES["docs/prd/features/prd-signing.md"].replace(
                               "## Functional Requirements",
                               glossary + "## Functional Requirements")},
                          base=PRD_FILES)

    def test_requirement_id_slug_mismatch(self):
        self.assert_red({"docs/prd/features/prd-signing.md":
                         PRD_FILES["docs/prd/features/prd-signing.md"]
                         .replace("REQ-signing-001", "REQ-sealing-001")},
                        "the document's slug is", base=PRD_FILES)

    def test_duplicate_requirement_id(self):
        self.assert_red({"docs/prd/features/prd-signing.md":
                         PRD_FILES["docs/prd/features/prd-signing.md"] + """
### REQ-signing-001

The system MUST reject an unsigned document.

- **Status**: Not implemented
- **E2E test**: Not implemented
"""},
                        "is used twice", base=PRD_FILES)

    def test_partially_implemented_is_refused(self):
        self.assert_red({"docs/prd/features/prd-signing.md":
                         PRD_FILES["docs/prd/features/prd-signing.md"]
                         .replace("- **Status**: Not implemented",
                                  "- **Status**: Partially implemented")},
                        "split the requirement", base=PRD_FILES)

    def test_requirement_without_an_e2e_axis(self):
        self.assert_red({"docs/prd/features/prd-signing.md":
                         PRD_FILES["docs/prd/features/prd-signing.md"]
                         .replace("- **E2E test**: Not implemented\n", "")},
                        "no end-to-end test status", base=PRD_FILES)

    def test_missing_functional_requirements_section(self):
        self.assert_red({"docs/prd/features/prd-signing.md":
                         """# Signing

## Problem Statement

Documents must be signed.
"""},
                        "missing mandatory section 'Functional Requirements'",
                        base=PRD_FILES)

    def test_journey_without_a_precondition(self):
        self.assert_red({"docs/prd/journeys/journey-sign-document.md":
                         PRD_FILES["docs/prd/journeys/journey-sign-document.md"]
                         .replace("## Precondition\n\nThe account holds a key pair.\n\n", "")},
                        "missing mandatory section 'Precondition'", base=PRD_FILES)

    def test_journey_without_an_e2e_test_section(self):
        self.assert_red({"docs/prd/journeys/journey-sign-document.md":
                         PRD_FILES["docs/prd/journeys/journey-sign-document.md"]
                         .replace("## End-to-End Test\n\n"
                                  "`e2e/signing.spec.ts` `signs a document`\n", "")},
                        "missing mandatory section 'End-to-End Test'", base=PRD_FILES)

    def test_orphan_requirements_file(self):
        self.assert_red({"docs/prd/features/prd-sealing.md":
                         PRD_FILES["docs/prd/features/prd-signing.md"]
                         .replace("# Signing", "# Sealing")
                         .replace("REQ-signing", "REQ-sealing")},
                        "absent from the requirements index", base=PRD_FILES)


class ReferencesTests(CheckerCase):
    """The project references file and the citations that point at it."""

    REFS = "docs/references.md"
    ARCH = "docs/design/architecture.md"
    GADGET_ROW = ("| REF-gadgetlib | gadgetlib | open-source host shim | "
                  "https://example.org/gadgetlib | — |\n")

    def test_missing_references_file(self):
        self.assert_red({self.REFS: None}, "references file does not exist")

    def test_default_path_when_the_key_is_omitted(self):
        self.assert_green({"CLAUDE.md":
                           CLAUDE_MD.replace("- references: docs/references.md\n", "")})

    def test_citation_of_an_unregistered_source(self):
        self.assert_red({self.ARCH: ARCHITECTURE.replace(
            "## Module Dependency Graph",
            "The host wraps gadgetlib ([REF-gadgetlib](../references.md)).\n\n"
            "## Module Dependency Graph")},
                        "does not register")

    def test_entry_cited_by_no_document(self):
        self.assert_red({self.REFS: REFERENCES + self.GADGET_ROW}, "cited by no document")

    def test_entry_registered_twice(self):
        self.assert_red({self.REFS: REFERENCES
                         + "| REF-widgetlib | widgetlib | the same library again | "
                           "https://example.org/widgetlib | 2.0.0 |\n"},
                        "registered more than once")

    def test_entry_without_a_url(self):
        self.assert_red({self.REFS: REFERENCES.replace("https://example.org/widgetlib",
                                                       "the vendor's portal")},
                        "carries no URL")

    def test_citation_slug_not_kebab_case(self):
        self.assert_red({self.ARCH: ARCHITECTURE.replace(
            "## Module Dependency Graph",
            "The host wraps WidgetLib ([REF-WidgetLib](../references.md)).\n\n"
            "## Module Dependency Graph")},
                        "lowercase kebab-case")

    def test_entry_id_not_kebab_case(self):
        self.assert_red({self.REFS: REFERENCES
                         + "| REF-Gadget_Lib | gadgetlib | shim | https://example.org/g | — |\n"},
                        "lowercase kebab-case")

    def test_permalink_with_a_line_anchor_is_a_url_not_a_line_reference(self):
        self.assert_green({self.REFS: REFERENCES.replace(
            "https://example.org/widgetlib",
            "https://example.org/widgetlib/blob/0a1b2c/registry.go#L12")})

    def test_references_section_in_a_document_is_refused(self):
        self.assert_red({self.ARCH: ARCHITECTURE + "\n## References\n\n- widgetlib\n"},
                        "not part of this document type's fixed section set")

    def test_citation_inside_a_fence_is_not_a_citation(self):
        self.assert_green({self.ARCH: ARCHITECTURE.replace(
            "## Architecture Invariants",
            "```\nREF-nothing\n```\n\n## Architecture Invariants")})

    def test_citation_from_the_glossary_counts(self):
        self.assert_green({
            "docs/glossary.md": GLOSSARY
            + "\nRegistry — the widgetlib registry ([REF-widgetlib](references.md)).\n",
            self.ARCH: ARCHITECTURE.replace(
                " The `core` module embeds the widgetlib\n"
                "registry ([REF-widgetlib](../references.md)).", "")})

    def test_conforming_chinese_tree_cites_and_registers(self):
        self.assert_red({self.REFS: ZH_FILES[self.REFS]
                         + "| REF-gadgetlib | gadgetlib | 宿主垫片 | https://example.org/gadgetlib | — |\n"},
                        "cited by no document", base=ZH_FILES)


CLAUDE_MD_WITH_FENCED_EXAMPLE = """# Project

The block looks like this:

```markdown
## Documentation

- design: docs/example
- language: zh-CN
```

## Documentation

- design: docs/design
- adr: docs/adr
- glossary: docs/glossary.md
- references: docs/references.md
- language: en
"""


class ConfigurationTests(CheckerCase):
    def test_fenced_example_block_is_not_the_configuration(self):
        self.assert_green({"CLAUDE.md": CLAUDE_MD_WITH_FENCED_EXAMPLE})

    def test_value_with_trailing_text_is_a_usage_error(self):
        code, report = self.run_on({"CLAUDE.md": CLAUDE_MD.replace(
            "- design: docs/design", "- design: docs/design (design docs)")})
        self.assertEqual(code, 2, report)
        self.assertIn("not a single path", report)

    def test_no_tree_at_all_is_a_usage_error(self):
        code, report = self.run_on({"CLAUDE.md": CLAUDE_MD
                                    .replace("docs/design", "docs/nowhere")
                                    .replace("docs/adr", "docs/nowhere-either")})
        self.assertEqual(code, 2, report)
        self.assertIn("no documentation tree found", report)

    def test_failure_summary_names_the_installed_skill_directory(self):
        skill_dir = os.path.dirname(os.path.dirname(os.path.abspath(m.__file__)))
        code, report = self.run_on({"docs/design/modules/design-core.md":
                                    MODULE.replace("### Non-Responsibilities\n\n", "")})
        self.assertEqual(code, 1, report)
        self.assertIn(skill_dir, report)


class ParsingTests(CheckerCase):
    def test_code_reference_with_a_directory_prefix(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         MODULE.replace("`Register` is stable",
                                        "`Register` (pkg/registry.go:42) is stable")},
                        "code reference with a line number")

    def test_code_reference_with_a_hash_line_anchor_and_directory(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         MODULE.replace("`Register` is stable",
                                        "`Register` (internal/core/registry.go#L42) is stable")},
                        "code reference with a line number")

    def test_heading_anchor_is_not_a_line_reference(self):
        self.assert_green({"docs/design/modules/design-core.md":
                           MODULE.replace("Nothing below it.",
                                          "Nothing below it; see "
                                          "[the contract](design-core.md#3).")})

    def test_single_line_diagram_with_content_passes(self):
        self.assert_green({"docs/design/architecture.md":
                           ARCHITECTURE.replace("graph TD\n  host --> core",
                                                "graph LR; host-->core")})

    def test_fence_closes_only_on_its_own_delimiter(self):
        text = "# T\n\n## S\n\nprose\n\n```mermaid\ngraph TD\n~~~\n  a --> b\n```\n"
        section = [s for s in m.parse_sections(text) if s.title == "S"][0]
        self.assertEqual(section.fences()[0][2], ["graph TD", "~~~", "  a --> b"])

    def test_bom_does_not_hide_the_title(self):
        self.assert_green({"docs/design/modules/design-core.md": "\ufeff" + MODULE})

    def test_non_utf8_file_is_a_finding_not_a_crash(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         MODULE.encode("utf-8").replace(b"Owns registration.",
                                                        b"Owns registration \xe9.")},
                        "not valid UTF-8")

    def test_chinese_task_marker_needs_a_boundary(self):
        self.assert_green({"docs/design/modules/design-core.md":
                           ZH_FILES["docs/design/modules/design-core.md"]
                           .replace("`Register` 在大版本内保持稳定。",
                                    "`Register` 在大版本内保持稳定，其语义待定义于契约节。")},
                          base=ZH_FILES)

    def test_chinese_task_marker_alone_is_reported(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         ZH_FILES["docs/design/modules/design-core.md"]
                         .replace("`Register` 在大版本内保持稳定。", "`Register` 的语义待定。")},
                        "project-management marker", base=ZH_FILES)


class SkippedDocumentTests(CheckerCase):
    def test_adr_readme_gets_the_common_checks(self):
        self.assert_red({"docs/adr/README.md": ADR_README + "\n- **Author**: me\n"},
                        "process metadata field")

    def test_misnamed_record_still_gets_its_content_checked(self):
        self.assert_red({"docs/adr/adr-scope-2026-01-01.md":
                         ADR_RECORD + "\n- **Status**: Accepted\n"},
                        "status field")

    def test_overview_heading_in_the_other_language(self):
        self.assert_red({"docs/prd/overview.md":
                         PRD_FILES["docs/prd/overview.md"].replace("## Capabilities", "## 能力")},
                        "is written in zh-CN", base=PRD_FILES)


ASPECT = """# core storage

## Core Data Structures

A map from component name to component.
"""
ASPECT_INDEX_ROW = "| [core storage](modules/design-core-storage.md) | the storage aspect of core |\n"


class StatusVocabularyTests(CheckerCase):
    MOD = "docs/design/modules/design-core.md"

    def test_unimplemented_is_not_implemented(self):
        self.assert_red({self.MOD: MODULE.replace("| registration | Not implemented | — |",
                                                  "| registration | Unimplemented | — |")},
                        "carries none of")

    def test_status_word_in_the_capability_cell_is_not_the_status(self):
        self.assert_red({self.MOD: MODULE.replace("| registration | Not implemented | — |",
                                                  "| Not implemented warning | Implemented | — |")},
                        "no code anchor")

    def test_qualified_status_value_is_refused(self):
        self.assert_red({self.MOD: MODULE.replace("| registration | Not implemented | — |",
                                                  "| registration | Implemented (partially) | `a.go` `F` |")},
                        "carries none of")

    def test_status_table_without_leading_pipes(self):
        self.assert_green({self.MOD: MODULE.replace(
            "| Capability | Status | Code |\n|---|---|---|\n| registration | Not implemented | — |",
            "Capability | Status | Code\n---|---|---\nregistration | Not implemented | —")})

    def test_second_status_table_header_is_not_an_entry(self):
        self.assert_green({self.MOD: MODULE
                           + "\n| Capability | Status | Code |\n|---|---|---|\n"
                             "| assembly | Not implemented | — |\n"})


class AdrIndexFormTests(CheckerCase):
    def test_list_form_index_row_uses_the_record_title(self):
        self.assert_red({"docs/adr/README.md":
                         "# Architecture Decision Records\n\nRecords.\n\n## Core\n\n"
                         "- [a registry lives in core](adr-core-scope.md)\n"},
                        "does not use the record's own title")

    def test_dot_slash_link_does_not_escape_the_column_check(self):
        self.assert_red({"docs/adr/README.md":
                         "# Architecture Decision Records\n\nRecords.\n\n## Core\n\n"
                         "| Record | Date |\n|---|---|\n"
                         "| [core owns registration](./adr-core-scope.md) | 2026-01-02 |\n"},
                        "date or status column")


class AspectSplitTests(CheckerCase):
    ASPECT_PATH = "docs/design/modules/design-core-storage.md"

    def test_aspect_split_is_not_a_module_document(self):
        self.assert_green({self.ASPECT_PATH: ASPECT,
                           "docs/design/README.md": DESIGN_README + ASPECT_INDEX_ROW})

    def test_aspect_row_in_the_architecture_roll_up_is_refused(self):
        self.assert_red({self.ASPECT_PATH: ASPECT,
                         "docs/design/README.md": DESIGN_README + ASPECT_INDEX_ROW,
                         "docs/design/architecture.md": ARCHITECTURE
                         + "| [core storage](modules/design-core-storage.md) | Not implemented |\n"},
                        "aspect document")

    def test_numeric_sibling_is_not_an_aspect(self):
        code, report = self.run_on({
            "docs/design/modules/design-core-2.md": MODULE,
            "docs/design/README.md": DESIGN_README
            + "| [core 2](modules/design-core-2.md) | duplicate |\n",
        })
        self.assertEqual(code, 1, report)
        self.assertIn("sequence number", report)
        self.assertNotIn("aspect document", report)

    def test_aspect_document_absent_from_the_index_is_an_orphan(self):
        self.assert_red({self.ASPECT_PATH: ASPECT}, "absent from the design index")

    def test_aspect_status_entries_are_still_checked(self):
        self.assert_red({self.ASPECT_PATH: ASPECT
                         + "\n## Implementation Status\n\n| Capability | Status | Code |\n"
                           "|---|---|---|\n| storage | Implemented | — |\n",
                         "docs/design/README.md": DESIGN_README + ASPECT_INDEX_ROW},
                        "no code anchor")


class ParserRobustnessTests(CheckerCase):
    """Inputs a real markdown file can contain, which the checker must read
    the way a renderer does."""

    MOD = "docs/design/modules/design-core.md"
    ARCH = "docs/design/architecture.md"

    def test_config_path_with_a_trailing_slash(self):
        self.assert_green({"CLAUDE.md": CLAUDE_MD
                           .replace("docs/design\n", "docs/design/\n")
                           .replace("docs/adr\n", "docs/adr/\n")})

    def test_dotfile_beside_a_module_is_ignored(self):
        self.assert_green({"docs/design/modules/._design-core.md": "junk\n"})

    def test_table_separator_with_single_hyphens(self):
        self.assert_green({self.MOD: MODULE.replace(
            "| Capability | Status | Code |\n|---|---|---|",
            "| Capability | Status | Code |\n|-|-|-|")})

    def test_multilevel_numbered_heading(self):
        self.assert_green({self.MOD: MODULE.replace(
            "### Non-Responsibilities", "### 1.1 Non-Responsibilities")})

    def test_heading_ignores_the_space_between_cjk_and_latin(self):
        self.assert_green({"docs/design/modules/design-core.md":
                           ZH_FILES["docs/design/modules/design-core.md"].replace(
                               "## 实现状态", """
## UML图

组件之间的引用关系如下。

```mermaid
classDiagram
  class Registry {
    +Register()
  }
```

## 实现状态""")},
                          base=ZH_FILES)

    def test_section_spans_its_subsections(self):
        self.assert_green({self.ARCH: ARCHITECTURE.replace(
            "A single process.\n\n```mermaid\ngraph TD\n  process --> database\n```",
            "A single process.\n\n### Production\n\nOne replica per region.\n\n"
            "```mermaid\ngraph TD\n  process --> database\n```")})

    def test_inline_code_comment_marker_does_not_swallow_the_document(self):
        self.assert_green({self.MOD: MODULE.replace(
            "Owns registration.",
            "Owns registration; a template comment opens with `<!--`.")
            + "\n<!-- reviewed -->\n"})

    def test_data_field_named_like_metadata_is_content(self):
        self.assert_green({self.MOD: MODULE.replace(
            "## Implementation Status",
            "## Core Data Structures\n\n"
            "- **Version**: the optimistic-lock counter\n"
            "- **Owner**: the tenant that registered the component\n\n"
            "## Implementation Status")})

    def test_adr_field_with_a_content_value_is_not_a_status(self):
        self.assert_green({"docs/adr/adr-core-scope.md": ADR_RECORD.replace(
            "Registration is uniform.", "Registration is uniform.\n\n- **State**: kept in Redis.")})

    def test_chinese_sentence_about_replacement_is_not_supersession(self):
        self.assert_green({"docs/adr/adr-core-scope.md":
                           ZH_FILES["docs/adr/adr-core-scope.md"].replace(
                               "宿主需要一个统一的组件注册位置。",
                               "宿主需要一个统一的组件注册位置。\n\n"
                               "已被事件推送取代，因此改用轮询。")},
                          base=ZH_FILES)

    def test_todo_adjacent_to_chinese_is_still_a_marker(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         ZH_FILES["docs/design/modules/design-core.md"].replace(
                             "拥有注册。", "拥有注册，此处TODO补充边界。")},
                        "project-management marker", base=ZH_FILES)

    def test_reference_citation_adjacent_to_chinese_is_counted(self):
        self.assert_green({"docs/design/architecture.md":
                           ZH_FILES["docs/design/architecture.md"].replace(
                               "（[REF-widgetlib](../references.md)）", "，依据REF-widgetlib 实现")},
                          base=ZH_FILES)


class RegressionTests(CheckerCase):
    """Defects the fix round itself introduced, each reproduced first."""

    ADR = "docs/adr/adr-core-scope.md"
    MOD = "docs/design/modules/design-core.md"

    def test_status_field_without_a_verdict_verb(self):
        self.assert_red({self.ADR: ADR_RECORD.replace(
            "## Context\n", "## Context\n\n- **Status**: Draft\n")}, "status field")

    def test_accepted_field_carrying_a_date(self):
        self.assert_red({self.ADR: ADR_RECORD.replace(
            "## Context\n", "## Context\n\n- **Accepted**: 2026-01-02\n")}, "status field")

    def test_chinese_status_field_with_a_plain_value(self):
        self.assert_red({"docs/adr/adr-core-scope.md":
                         ZH_FILES["docs/adr/adr-core-scope.md"].replace(
                             "宿主需要一个统一的组件注册位置。",
                             "宿主需要一个统一的组件注册位置。\n\n- **状态**：废弃")},
                        "status field", base=ZH_FILES)

    def test_chinese_marker_before_ascii_punctuation(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         ZH_FILES["docs/design/modules/design-core.md"].replace(
                             "拥有注册。", "待办: 补测试。")},
                        "project-management marker", base=ZH_FILES)

    def test_chinese_marker_before_a_full_width_bracket(self):
        self.assert_red({"docs/design/modules/design-core.md":
                         ZH_FILES["docs/design/modules/design-core.md"].replace(
                             "拥有注册。", "待定（暂缓）。")},
                        "project-management marker", base=ZH_FILES)

    def test_line_range_reference_is_still_a_line_reference(self):
        self.assert_red({self.MOD: MODULE.replace(
            "`Register` is stable", "`Register` (pkg/registry.go:42-45) is stable")},
            "code reference with a line number")

    def test_comment_after_a_multi_backtick_span_is_stripped(self):
        self.assert_green({self.MOD: MODULE.replace(
            "Owns registration.",
            "Owns registration; `` ` `` is a literal backtick. <!-- TODO: finish -->")})

    def test_english_sentence_about_replacement_is_not_supersession(self):
        self.assert_green({self.ADR: ADR_RECORD.replace(
            "A host needs one place to register components.",
            "A host needs one place to register components.\n\n"
            "Superseded by event push, so polling replaced it.")})

    def test_diagram_without_prose_is_reported_once(self):
        code, report = self.run_on({self.MOD: MODULE.replace(
            "## Implementation Status",
            "## UML Diagrams\n\n### Component Diagram\n\n```mermaid\nclassDiagram\n"
            "  class Registry {\n    +Register()\n  }\n```\n\n## Implementation Status")})
        self.assertEqual(code, 1, report)
        self.assertEqual(report.count("holds a diagram and no prose"), 1, report)

    def test_second_status_table_body_is_checked(self):
        self.assert_red({self.MOD: MODULE
                         + "\n| Capability | Status | Code |\n|---|---|---|\n"
                           "| assembly | Implemented | — |\n"},
                        "no code anchor")


if __name__ == "__main__":
    unittest.main(verbosity=2)
