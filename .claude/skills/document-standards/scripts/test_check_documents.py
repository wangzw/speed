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
- language: en
"""

GLOSSARY = "# Glossary\n\nComponent — an implementation of a module interface.\n"

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

One binary, composed of modules.

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
        code, report = self.run_on(
            {"docs/design/README.md": None, "docs/design/architecture.md": None,
             "docs/design/modules/design-core.md": None},
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
        self.assert_red({"docs/adr/adr-core-scope.md": None,
                         "docs/adr/adr-core-scope-2.md": ADR_RECORD,
                         "docs/adr/README.md":
                         ADR_README.replace("adr-core-scope.md", "adr-core-scope-2.md")},
                        "sequence number")

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

单进程，由模块组成。

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


if __name__ == "__main__":
    unittest.main(verbosity=2)
