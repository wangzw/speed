#!/usr/bin/env python3
"""Structural checker for the document-standards skill.

The skill (.claude/skills/document-standards/) is the authoritative
standard for the documentation trees declared in CLAUDE.md's
"## Documentation" block. Most of that standard is a judgement call --
whether a sentence is subjective, whether a requirement traces to a real
source, whether an option's rejection reason is honest. This script
checks the part that is not: the STRUCTURE. Section presence and order,
file naming, index coverage, status vocabulary, diagram bodies and the
metadata a document must never carry.

A green run means the documents are shaped like the standard says. It
does not mean they are correct; review owns the rest.

Trees, paths and language come from CLAUDE.md's "## Documentation" block
(the keys the skill defines: design, requirements, adr, glossary,
references, language). Fenced code in CLAUDE.md is not configuration. A
tree the block declares but that does not exist on disk is skipped, not
failed -- a project using only design documents and ADRs is a normal
configuration -- but a configuration under which no tree exists at all,
or a value that is not a single path, is a usage error.

Usage:
    python3 .claude/skills/document-standards/scripts/check_documents.py [--root PATH]

--root defaults to the current directory and must hold CLAUDE.md. Paths
in the report are relative to --root. Exit code is 0 when every check
passes, 1 on any finding, 2 on a usage or configuration error.

WHAT IS CHECKED

  Common to every document (SKILL.md section 2):
    * the file decodes as UTF-8 (a byte-order mark is tolerated)
    * exactly one H1, and it opens the document
    * no process metadata: change history, version, author, date, review
      record headings or field lines
    * no project management: TODO / FIXME / TBD markers
    * no code reference carrying a line number (path.go:123)
    * every mermaid block has a diagram type and a non-empty body
    * a section holding a diagram carries prose as well
    * headings are written in the document's configured language
    * the configured glossary file exists and is an entry table in the
      standard's column order, every entry carrying an English
      counterpart when the documentation is not written in English

  Project references (SKILL.md section 2):
    * the configured references file exists, and is shaped like every
      other document (one H1, no process metadata, no task markers)
    * every entry is a table row keyed REF-<slug>, lowercase kebab-case
      ASCII, registered once, and carries a URL -- the file registers
      public sources only
    * every REF-<slug> citation in a checked tree or in the glossary
      resolves to an entry, and every entry is cited at least once

  Design tree (references/design-documents.md):
    * README.md and architecture.md are present
    * modules/ file names match design-<slug>[-<aspect>], kebab-case
      ASCII, with no sequence number anywhere; a module subdirectory is
      named for its slug and every file inside carries the slug prefix
    * a flat design-<slug>-<aspect>.md beside design-<slug>.md is an
      aspect split: indexed like any entry point and held to the common
      rules and the status vocabulary, but to neither the module section
      set nor a status row of its own
    * a module whose sub-modules have their own documents lives in
      modules/<slug>/, holding its own design-<slug>.md, the sub-module
      documents and a README.md index
    * the tree's README index covers each entry point exactly once; a
      module subdirectory's README index covers its files exactly once;
      every index link resolves
    * architecture.md carries its required sections in the fixed order,
      and the module-dependency and logical-deployment sections each
      carry a mermaid diagram
    * when the Implementation Status roll-up lists more than one top-level
      module, the Overview opens with Core Concepts -- carrying a mermaid
      concept map and no second glossary table -- and continues with Design
      Mainline carrying its problem/mechanism/location table
    * a module's Worked Example and State at a Glance, when present, sit
      under Core Algorithms and Flows and Core Data Structures respectively
    * a module document carries its mandatory sections in the fixed
      order, including the Non-Responsibilities subsection, and carries
      no section outside the standard's set
    * every implementation-status entry -- a row of any table in the
      section, with or without leading pipes, or a list item -- holds
      exactly one of the three canonical values in a cell of its own, and
      carries a code anchor when it claims any implementation
    * architecture.md's status roll-up is one row per top-level module
      and carries no sub-module row; a module subdirectory's README.md
      roll-up is one row per sub-module document

  ADR tree (references/decision-records.md):
    * file names match adr-<slug>, with no date and no sequence number
    * the fixed section order, with Invalidation Conditions optional and
      no status field or heading
    * Options Considered lists at least two options
    * README index, in table or list form, covers every record exactly
      once, carries neither a date column nor a status column, and names
      each record by its title

  Requirements tree (references/requirements-documents.md):
    * README.md and overview.md are present; journeys/journey-<slug> and
      features/prd-<slug> naming, no sequence numbers
    * README index covers every file exactly once
    * a capability document carries Functional Requirements, and the
      sections it does carry follow the fixed order
    * requirement ids match REQ-<doc slug>-NNN, are unique within the
      document and carry both a status and an end-to-end test field
    * requirement status is Implemented or Not implemented -- the
      standard has no Partially implemented here
    * a journey records who, the precondition, the steps, the outcome
      and its end-to-end test

WHAT IS DELIBERATELY NOT CHECKED

  Judgement, not structure. SKILL.md section 5, "What it does not check",
  is the authoritative list; keeping a second itemised copy here only
  drifts from it. In short: whether a sentence is true, sourced, testable
  or well written; whether a list states a criterion or enumerates an
  exhaustive set; whether a cited source is one the reader would have
  attributed correctly unaided. Rendering is not checked either -- this
  script checks that a mermaid block has a type and a body, while running
  a renderer belongs to the editing workflow the skill mandates.

  Timeliness: whether a document still matches the code it asserts
  things about. No mechanical check can know.
"""

from __future__ import annotations

import argparse
import os
import re
import sys

SKILL_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CONFIG_KEYS = ("design", "requirements", "adr", "glossary", "references", "language")
CONFIG_DEFAULTS = {
    "design": "docs/design",
    "requirements": "docs/prd",
    "adr": "docs/adr",
    "glossary": "docs/glossary.md",
    "references": "docs/references.md",
}
LANGUAGES = ("en", "zh-CN")


def names(en: str, zh: str) -> dict[str, str]:
    """A canonical section name in each supported document language.

    One name per language, matching the templates: a section written
    under a synonym is a finding, because documents that name the same
    section differently cannot be read against each other."""
    return {"en": en, "zh-CN": zh}


def canonical(name: dict[str, str], language: str) -> str:
    return name[language]


# Section tables. Each row is (key, names, mandatory) in the standard's
# fixed order; the order of the list IS the order rule.
ARCHITECTURE_SECTIONS = [
    ("overview", names("Overview", "概览"), True),
    ("module-dependency-graph", names("Module Dependency Graph", "模块依赖图"), True),
    ("logical-deployment", names("Logical Deployment Architecture", "逻辑部署架构"), True),
    ("physical-deployment", names("Physical Deployment Architecture", "物理部署架构"), False),
    ("inter-module-calls", names("Inter-Module Call Relationships", "模块间调用关系"), False),
    ("data-flow", names("Data Flow", "数据流"), False),
    ("cross-cutting", names("Cross-Cutting Design", "跨切面设计"), True),
    ("invariants", names("Architecture Invariants", "架构不变量"), True),
    ("status", names("Implementation Status", "实现状态"), True),
]
CORE_CONCEPTS = names("Core Concepts", "核心概念")
DESIGN_MAINLINE = names("Design Mainline", "设计主线")
CORE_CONCEPTS_SECTION = [("core-concepts", CORE_CONCEPTS, True)]
DESIGN_MAINLINE_SECTION = [("design-mainline", DESIGN_MAINLINE, True)]
DESIGN_MAINLINE_COLUMNS = [
    names("Problem", "问题"),
    names("Mechanism", "机制"),
    names("Where it is specified", "展开位置"),
]
WORKED_EXAMPLE = names("Worked Example", "走查")
STATE_AT_A_GLANCE = names("State at a Glance", "状态一览")
MODULE_SECTIONS = [
    ("responsibilities", names("Responsibilities", "职责"), True),
    ("glossary", names("Glossary", "术语表"), False),
    ("external-interface", names("External Interface Contract", "外部接口契约"), True),
    ("dependencies", names("Dependencies", "依赖"), True),
    ("diagrams", names("UML Diagrams", "UML 图"), False),
    ("data-structures", names("Core Data Structures", "核心数据结构"), False),
    ("algorithms", names("Core Algorithms and Flows", "核心算法与流程"), False),
    ("errors", names("Error and Failure Semantics", "错误与失败语义"), False),
    ("concurrency", names("Concurrency and Transaction Boundaries", "并发与事务边界"), False),
    ("extension-points", names("Extension Points", "扩展点"), False),
    ("status", names("Implementation Status", "实现状态"), True),
]
MODULE_SECTION_NAMES = {key: name for key, name, _ in MODULE_SECTIONS}
NON_RESPONSIBILITIES = names("Non-Responsibilities", "非职责")
ADR_SECTIONS = [
    ("context", names("Context", "背景"), True),
    ("options", names("Options Considered", "考虑过的选项"), True),
    ("decision", names("Decision", "决策"), True),
    ("consequences", names("Consequences", "后果"), True),
    ("invalidation", names("Invalidation Conditions", "失效条件"), False),
]
FEATURE_SECTIONS = [
    ("problem", names("Problem Statement", "问题陈述"), False),
    ("scope", names("Scope", "范围"), False),
    ("actors", names("Actors", "参与者"), False),
    ("glossary", names("Glossary", "术语表"), False),
    ("functional", names("Functional Requirements", "功能需求"), True),
    ("non-functional", names("Non-Functional Requirements", "非功能需求"), False),
    ("data", names("Data Requirements", "数据需求"), False),
    ("external-interface", names("External Interface Requirements", "外部接口需求"), False),
    ("constraints", names("Constraints", "约束"), False),
    ("assumptions", names("Assumptions and Dependencies", "假设与依赖"), False),
]
JOURNEY_SECTIONS = [
    ("who", names("Who", "参与者"), True),
    ("precondition", names("Precondition", "前置条件"), True),
    ("steps", names("Steps", "步骤"), True),
    ("outcome", names("Outcome", "结果"), True),
    ("alternate-paths", names("Alternate Paths", "备选路径"), False),
    ("requirements", names("Requirements Exercised", "覆盖的需求"), False),
    ("e2e", names("End-to-End Test", "端到端测试"), True),
]
OVERVIEW_SECTIONS = [
    ("what", names("What the System Is", "系统是什么"), True),
    ("capabilities", names("Capabilities", "能力"), True),
    ("scope", names("Scope Boundary", "范围边界"), True),
    ("actors", names("Actors", "参与者"), False),
]
INDEX_SECTION = names("Index", "索引")
# A module subdirectory's README carries exactly these two H2s.
SUBINDEX_SECTIONS = [
    ("index", INDEX_SECTION, True),
    ("status", names("Implementation Status", "实现状态"), True),
]

# Glossary columns, by header name, in the fixed order the standard requires.
# A local glossary adds the scope column; an English-language document omits
# the counterpart column, because its term column is that form.
GLOSSARY_COLUMNS = {
    "term": frozenset({"term", "术语"}),
    "english": frozenset({"english", "english term", "英文", "英文术语", "英文对照"}),
    "scope": frozenset({"scope", "适用范围", "作用域"}),
    "definition": frozenset({"definition", "meaning", "定义", "含义"}),
}
GLOSSARY_COLUMN_NAMES = {
    "term": names("Term", "术语"),
    "english": names("English", "英文"),
    "scope": names("Scope", "适用范围"),
    "definition": names("Definition", "定义"),
}
CJK_RE = re.compile(r"[㐀-䶿一-鿿]")

# Status vocabularies.
DESIGN_STATUS = {
    "implemented": names("Implemented", "已实现"),
    "partial": names("Partially implemented", "部分实现"),
    "not-implemented": names("Not implemented", "未实现"),
}
REQUIREMENT_STATUS = {
    "implemented": names("Implemented", "已实现"),
    "not-implemented": names("Not implemented", "未实现"),
}
VAGUE_STATUS = ("mostly complete", "basically working", "almost done",
                "基本完成", "大部分完成", "基本可用", "大致完成", "基本实现")
ADR_DATE_TOKEN_RE = re.compile(r"(?:^|-)\d{4}-\d{2}-\d{2}(?:-|$)")
ADR_STATUS_HEADINGS = ("status", "状态", "state", "acceptance", "接受状态",
                       "acceptance status")
ADR_STATUS_FIELD_RE = re.compile(
    r"^\s*(?:[-*+]\s+)?\**\s*"
    r"(Status|State|Acceptance|Accepted|Deprecated|接受状态|状态|接受|验收)"
    r"\**\s*[:：]", re.IGNORECASE)
ADR_SUPERSEDED_RE = re.compile(
    r"^\s*(?:[-*+]\s+)?\**\s*(?:Superseded by\s+ADR-[^\s，。]{1,40}"
    r"|已被\s*ADR-[^\s，。]{1,40}?\s*取代)", re.IGNORECASE)
# A status field names a verdict only when its value is one. "- **State**:
# kept in Redis" describes a data field; "- **Status**: Accepted" records the
# record's status, which an ADR never carries.
VERDICT_RE = re.compile(
    r"accepted|rejected|supersed|deprecat|approved|proposed|"
    r"已接受|已拒绝|已废弃|已采纳|已批准|已取代|待定", re.IGNORECASE)
# An index link to a record, in a table row or a list item, with or without
# a leading "./".
ADR_INDEX_LINK_RE = re.compile(r"\[([^\]]*)\]\((?:\./)?(adr-[^)/]+\.md)\)")


def forbidden_column(cell: str) -> bool:
    text = cell.casefold().strip("*` ")
    for token in ("status", "date", "state", "acceptance", "accepted", "deprecated"):
        if re.search(rf"\b{token}\b", text):
            return True
    return any(token in text for token in ("状态", "日期", "接受", "已接受", "已废弃"))

# Metadata a document never carries (SKILL.md section 2).
METADATA_HEADINGS = ("change history", "revision history", "changelog", "version history",
                     "document history", "authors", "author", "review record", "reviewers",
                     "变更历史", "修订历史", "变更记录", "修订记录", "版本历史", "文档历史",
                     "作者", "审阅记录", "评审记录")
METADATA_FIELD_RE = re.compile(
    r"^\s*(?:[-*+]\s*)?\**\s*"
    r"(Version|Author|Authors|Date|Last updated|Last modified|Revision|Reviewed by|Reviewers|Owner|Priority"
    r"|版本|作者|日期|更新日期|最后更新|修订|审阅人|评审人|负责人|优先级)"
    r"\**\s*[:：]", re.IGNORECASE)
# A Chinese marker is a word of its own only when it ends where the sentence
# does: 待定。 is a marker, 待定义 and 期待定稿 are ordinary words. The ASCII
# markers need no such rule -- CJK is a word boundary for them, so 此处TODO is
# a marker and so is TODO：.
TASK_MARKER_RE = re.compile(
    r"(?<![A-Za-z0-9_-])(TODO|FIXME|TBD)(?![A-Za-z0-9_-])"
    r"|(?:待办(?:事项)?|待定)(?=$|[^\u4e00-\u9fff])")
# A path with a source extension followed by a line number: pkg/registry.go:42
# or registry.go#L42. The lookbehind refuses a start inside a word or a path
# segment, which also keeps every URL out: after "://" each segment begins
# behind a "/". The "#" form requires "L", and the digits end the token, so a
# heading anchor such as design-core.md#3-dependencies is not a line.
LINE_NUMBER_REF_RE = re.compile(
    r"(?<![\w/.-])(?:[\w.-]+/)*[\w.-]*[\w-]\.(?:go|ts|tsx|js|jsx|py|sql|yaml|yml|toml|json|md|sh)"
    r"(?::L?\d+|#L\d+)(?!\w)")
# A mermaid body that is one line holding only a diagram type and, at most, a
# direction: "graph TD", "sequenceDiagram", "stateDiagram-v2". Such a block
# renders blank. "graph LR; a-->b" is a one-line diagram with content.
DIAGRAM_TYPE_ONLY_RE = re.compile(r"^\s*[\w-]+(?:\s+[\w-]+)?\s*;?\s*$")
HEADING_RE = re.compile(r"^(#{1,6})\s+(.*?)\s*#*\s*$")
FENCE_RE = re.compile(r"^\s{0,3}(`{3,}|~{3,})\s*(\S*)")
LINK_RE = re.compile(r"\[[^\]]*\]\(([^)\s]+)(?:\s+\"[^\"]*\")?\)")
# GFM's delimiter row takes one or more hyphens per cell, not three.
TABLE_SEPARATOR_RE = re.compile(
    r"^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)*\|?\s*$")
SLUG_RE = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
REQ_ID_RE = re.compile(r"^req-([a-z0-9]+(?:-[a-z0-9]+)*)-(\d+)$")
CODE_ANCHOR_RE = re.compile(r"`[^`]*[./][^`]*`")
# A citation of the project references file: REF- plus whatever slug the
# author wrote, so a wrongly cased slug is reported rather than missed. CJK
# is a boundary: Chinese prose does not space 依据REF-rfc7519.
REF_CITATION_RE = re.compile(r"(?<![A-Za-z0-9_-])REF-([A-Za-z0-9_-]+)")
# An inline code span: a run of backticks closed by a run of the same length,
# so `` ` `` holds a literal backtick rather than opening two empty spans.
CODE_SPAN_RE = re.compile(r"(?<!`)(`+)(?!`).+?(?<!`)\1(?!`)")
URL_RE = re.compile(r"https?://\S+")


class Finding:
    """One structural defect, reported as path:line: message."""

    def __init__(self, path: str, line: int, message: str):
        self.path = path
        self.line = line
        self.message = message

    def render(self) -> str:
        where = f"{self.path}:{self.line}" if self.line else self.path
        return f"{where}: {self.message}"

    def sort_key(self) -> tuple:
        return (self.path, self.line, self.message)


class Section:
    """A heading and the lines below it, up to the next heading of the
    same or a shallower level. The level-0 section is the preamble."""

    def __init__(self, level: int, title: str, line: int):
        self.level = level
        self.title = title
        self.line = line
        self.lines: list[tuple[int, str, bool, str]] = []

    def prose_lines(self) -> list[tuple[int, str]]:
        return [(n, t) for n, t, in_fence, _ in self.lines
                if not in_fence and t.strip() and not HEADING_RE.match(t)
                and not FENCE_RE.match(t)]

    def fences(self) -> list[tuple[int, str, list[str]]]:
        """(start line, language, body lines) for every fenced block."""
        out: list[tuple[int, str, list[str]]] = []
        current: list[str] | None = None
        start = 0
        lang = ""
        delimiter = ""
        for n, text, in_fence, fence_lang in self.lines:
            match = FENCE_RE.match(text)
            opening = match and not in_fence
            closing = (match and in_fence and current is not None
                       and match.group(1)[0] == delimiter)
            if opening:
                current, start, lang = [], n, fence_lang
                delimiter = match.group(1)[0]
            elif closing:
                out.append((start, lang, current or []))
                current = None
            elif current is not None:
                current.append(text)
        if current is not None:
            out.append((start, lang, current))
        return out


def scan_lines(text: str) -> list[tuple[int, str, bool, str]]:
    """(line number, text, inside-a-fence, fence language) for every line.

    A fence delimiter line itself is reported with the in_fence state it
    establishes: the opening delimiter is outside, the closing one is
    inside, so a section's prose scan never reads either as prose."""
    out: list[tuple[int, str, bool, str]] = []
    fence: str | None = None
    lang = ""
    for n, raw in enumerate(text.splitlines(), start=1):
        match = FENCE_RE.match(raw)
        if fence is None and match:
            out.append((n, raw, False, match.group(2)))
            fence, lang = match.group(1)[0], match.group(2)
            continue
        if fence is not None and match and match.group(1)[0] == fence:
            out.append((n, raw, True, lang))
            fence, lang = None, ""
            continue
        out.append((n, raw, fence is not None, lang))
    return out


SETEXT_UNDERLINE_RE = re.compile(r"^\s*(?:=+|-{3,})\s*$")


def setext_headings(lines: list[tuple[int, str, bool, str]]) -> list[tuple[int, str]]:
    """(line, title) for every setext heading: a text line underlined by === or ---."""
    out = []
    for i in range(len(lines) - 1):
        n, raw, in_fence, _ = lines[i]
        following = lines[i + 1]
        if in_fence or following[2] or not raw.strip() or "|" in raw:
            continue
        if SETEXT_UNDERLINE_RE.match(following[1]):
            out.append((n, raw.strip()))
    return out


def table_blocks(lines: list[tuple[int, str, bool, str]]) -> list[tuple[tuple[int, list[str]], list]]:
    """((header line, header cells), body rows) for every pipe table.

    A table is a line containing `|` followed by a separator line; the
    leading pipe is optional, as GFM allows."""
    out = []
    i = 0
    while i < len(lines):
        n, raw, in_fence, _ = lines[i]
        if in_fence or "|" not in raw or TABLE_SEPARATOR_RE.match(raw.strip()):
            i += 1
            continue
        following = i + 1
        while following < len(lines) and not lines[following][1].strip():
            following += 1
        if (following >= len(lines) or lines[following][2]
                or not TABLE_SEPARATOR_RE.match(lines[following][1].strip())):
            i += 1
            continue
        body = []
        j = following + 1
        while j < len(lines) and not lines[j][2] and lines[j][1].strip() and "|" in lines[j][1]:
            body.append(lines[j])
            j += 1
        cells = [cell.strip() for cell in raw.strip().strip("|").split("|")]
        out.append(((n, cells), body))
        i = j
    return out


def parse_sections(text: str) -> list[Section]:
    """Every heading and the lines it owns. A section owns the lines up to
    the next heading of the same or a shallower level, its subsections
    included -- a table under `### Services` belongs to the
    `## Implementation Status` section that encloses it. The level-0
    section owns only what precedes the first heading."""
    sections = [Section(0, "", 0)]
    stack = [sections[0]]
    started = False
    for n, raw, in_fence, lang in scan_lines(text):
        heading = None if in_fence else HEADING_RE.match(raw)
        if heading:
            level = len(heading.group(1))
            while len(stack) > 1 and stack[-1].level >= level:
                stack.pop()
            section = Section(level, heading.group(2).strip(), n)
            sections.append(section)
            stack.append(section)
            started = True
        if not started:
            sections[0].lines.append((n, raw, in_fence, lang))
            continue
        for section in stack[1:]:
            section.lines.append((n, raw, in_fence, lang))
    return sections


def section_end(sections: list[Section], parent: Section) -> int:
    """The line of the next section at the same or a shallower level."""
    following = [s.line for s in sections
                 if s.level <= parent.level and s.line > parent.line]
    return min(following) if following else 1 << 60


def direct_children(sections: list[Section], parent: Section) -> list[Section]:
    """The sections nested immediately below `parent`, in document order."""
    end = section_end(sections, parent)
    return [s for s in sections
            if s.level == parent.level + 1 and parent.line < s.line < end]


def _strip_line_comments(raw: str, in_comment: bool) -> tuple[str, bool]:
    """One line, with comments removed. A `<!--` inside a code span is text
    the reader sees, not the start of a comment."""
    out: list[str] = []
    i = 0
    while i < len(raw):
        if in_comment:
            end = raw.find("-->", i)
            if end < 0:
                return "".join(out), True
            in_comment = False
            i = end + 3
            continue
        opening = raw.find("<!--", i)
        span = CODE_SPAN_RE.search(raw, i)
        if opening < 0 and span is None:
            out.append(raw[i:])
            break
        if span is not None and (opening < 0 or span.start() < opening):
            out.append(raw[i:span.end()])
            i = span.end()
            continue
        out.append(raw[i:opening])
        in_comment = True
        i = opening + 4
    return "".join(out), in_comment


def strip_comments(text: str) -> str:
    """Blank out HTML comment bodies, keeping every line number intact.

    Template comment blocks are instructions to the author, not document
    content, so no content rule applies inside one. A marker inside a
    fenced block or a code span is content, and is left alone."""
    out: list[str] = []
    fence: str | None = None
    in_comment = False
    for raw in text.split("\n"):
        match = FENCE_RE.match(raw)
        if fence is None and not in_comment and match:
            fence = match.group(1)[0]
            out.append(raw)
            continue
        if fence is not None:
            if match and match.group(1)[0] == fence:
                fence = None
            out.append(raw)
            continue
        stripped, in_comment = _strip_line_comments(raw, in_comment)
        out.append(stripped)
    return "\n".join(out)


HEADING_NUMBERING_RE = re.compile(
    r"^\s*(?:\d+(?:[.、]\d+)*[.、)）]?\s*)+")
HEADING_CJK_NUMBERING_RE = re.compile(r"^\s*[一二三四五六七八九十百千]+[、.．)）]\s*")


def normalize_heading(title: str) -> str:
    """Heading text without inline markup, link syntax, numbering or
    spacing, so `## **Status**`, `## 1. Status` and `## Status` compare
    equal, and `## UML图` names the same section as `## UML 图`."""
    title = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", title)
    title = title.replace("*", "").replace("`", "").replace("_", " ")
    title = HEADING_NUMBERING_RE.sub("", title)
    title = HEADING_CJK_NUMBERING_RE.sub("", title)
    title = title.strip().strip(":：。.").casefold()
    return re.sub(r"\s+", "", title)

# Sections that describe data rather than the document. "- **Version**: the
# optimistic-lock counter" there names a field of the data; the same line in
# a header block names the document's version, which never appears.
# Sections a README carries beside its index and that are never part of it.
# A heading with one of these names ends the index; any other heading is a
# group inside it, at any level. "Capabilities" is deliberately absent: it
# names an overview section and a PRD README group alike.
INDEX_STOP_TITLES = frozenset(
    normalize_heading(name[language])
    for name in (names("Implementation Status", "实现状态"),
                 names("Glossary", "术语表"),
                 INDEX_SECTION)
    for language in LANGUAGES)


DATA_SECTION_TITLES = frozenset(
    normalize_heading(name) for name in (
        "Core Data Structures", "核心数据结构",
        "Core Algorithms and Flows", "核心算法与流程",
        "Data Requirements", "数据需求"))


def match_section(title: str, table: list, language: str) -> tuple[str | None, str | None]:
    """(key, wrong-language) for a heading against a section table."""
    normalized = normalize_heading(title)
    for key, name, _ in table:
        if normalize_heading(name[language]) == normalized:
            return key, None
    for key, name, _ in table:
        for other in LANGUAGES:
            if other != language and normalize_heading(name[other]) == normalized:
                return key, other
    return None, None


# Files that did not decode as UTF-8, keyed by absolute path. read() is
# called from every checker, so the defect is collected here and reported
# once by main() rather than threaded through every signature.
UNDECODABLE: dict[str, str] = {}


def read(path: str) -> str:
    with open(path, "rb") as handle:
        data = handle.read()
    try:
        return data.decode("utf-8-sig")
    except UnicodeDecodeError as error:
        UNDECODABLE.setdefault(
            os.path.abspath(path),
            f"not valid UTF-8 (byte {error.start}); every document is UTF-8 text")
        return data.decode("utf-8-sig", errors="replace")


def markdown_files(tree: str) -> list[str]:
    out = []
    for dirpath, dirnames, filenames in os.walk(tree):
        dirnames[:] = sorted(d for d in dirnames if not d.startswith("."))
        for name in sorted(filenames):
            # A dotfile is editor or filesystem residue (._x.md from a
            # non-HFS volume, .#x.md from an Emacs lock), not a document.
            if name.endswith(".md") and not name.startswith("."):
                out.append(os.path.join(dirpath, name))
    return sorted(out)


def parse_documentation_config(root: str) -> tuple[dict[str, str], list[str]]:
    """The "## Documentation" block of CLAUDE.md, with the skill's defaults
    filled in. Errors are usage errors -- an unresolved language means the
    checker cannot know which vocabulary applies."""
    path = os.path.join(root, "CLAUDE.md")
    if not os.path.isfile(path):
        return {}, [f"CLAUDE.md not found at {path}"]
    config: dict[str, str] = {}
    errors: list[str] = []
    in_block = False
    fence: str | None = None
    for raw in read(path).splitlines():
        match = FENCE_RE.match(raw)
        if fence is None and match:
            fence = match.group(1)[0]
            continue
        if fence is not None:
            if match and match.group(1)[0] == fence:
                fence = None
            continue
        heading = HEADING_RE.match(raw)
        if heading:
            if in_block:
                break
            in_block = normalize_heading(heading.group(2)) == "documentation"
            continue
        if not in_block:
            continue
        entry = re.match(r"^\s*[-*+]\s*([A-Za-z-]+)\s*[:：]\s*(.*?)\s*$", raw)
        if entry and entry.group(1).lower() in CONFIG_KEYS:
            key, value = entry.group(1).lower(), entry.group(2).strip("`")
            if not value or re.search(r"\s", value):
                errors.append(f"config value for {key!r} is not a single path: {value!r}")
                continue
            config[key] = value
    if not config:
        errors.append("CLAUDE.md has no \"## Documentation\" block; the skill "
                      "requires one declaring design, requirements, adr, "
                      "glossary, references and language")
    for key, default in CONFIG_DEFAULTS.items():
        config.setdefault(key, default)
    language = config.get("language")
    if language and language not in LANGUAGES:
        errors.append(f"unsupported documentation language {language!r}; this "
                      f"checker knows {', '.join(LANGUAGES)}")
    elif not language:
        errors.append("CLAUDE.md's \"## Documentation\" block declares no "
                      "language; the checker cannot resolve the section "
                      "vocabulary without it")
    return config, errors


# --- checks common to every document -------------------------------------

def check_common(rel_path: str, text: str) -> list[Finding]:
    found: list[Finding] = []
    sections = parse_sections(text)
    lines = scan_lines(text)

    titles = [s for s in sections if s.level == 1]
    if not titles:
        found.append(Finding(rel_path, 1, "no H1 title"))
    elif len(titles) > 1:
        for extra in titles[1:]:
            found.append(Finding(rel_path, extra.line,
                                 "second H1 title; a document has exactly one"))
    if titles:
        preamble = sections[0].prose_lines()
        if preamble:
            found.append(Finding(rel_path, preamble[0][0],
                                 "content above the H1 title"))

    for section in sections:
        if section.level == 0:
            continue
        if normalize_heading(section.title) in {normalize_heading(h) for h in METADATA_HEADINGS}:
            found.append(Finding(rel_path, section.line,
                                 f"process metadata section {section.title!r}; git is the "
                                 f"authority for history, versions, authors and review records"))

    data_lines: set[int] = set()
    for section in sections:
        if normalize_heading(section.title) in DATA_SECTION_TITLES:
            data_lines.update(n for n, _, _, _ in section.lines)
    for n, raw, in_fence, _ in lines:
        if in_fence or FENCE_RE.match(raw):
            continue
        field = None if n in data_lines else METADATA_FIELD_RE.match(raw)
        if field:
            found.append(Finding(rel_path, n,
                                 f"process metadata field {field.group(1)!r}; a document "
                                 f"states conclusions, not how it was produced"))
        marker = TASK_MARKER_RE.search(raw)
        if marker:
            found.append(Finding(rel_path, n,
                                 f"project-management marker {marker.group(0)!r}; TODOs, "
                                 f"schedules and owners belong to the tracker"))
        reference = LINE_NUMBER_REF_RE.search(raw)
        if reference:
            found.append(Finding(rel_path, n,
                                 f"code reference with a line number ({reference.group(0)}); "
                                 f"reference a path plus a symbol name"))

    # A section owns its subsections' lines, so each diagram is seen by the
    # section that holds it and by every section enclosing it. Report it
    # once, against the innermost section.
    owners: dict[int, tuple[Section, list[str]]] = {}
    for section in sections:
        for start, language, body in section.fences():
            if language.lower() != "mermaid":
                continue
            if start not in owners or section.level > owners[start][0].level:
                owners[start] = (section, body)
    for start, (section, body) in sorted(owners.items()):
        content = [line for line in body if line.strip()]
        if not content:
            found.append(Finding(rel_path, start, "empty mermaid block"))
            continue
        if len(content) == 1 and DIAGRAM_TYPE_ONLY_RE.match(content[0]):
            found.append(Finding(rel_path, start,
                                 f"mermaid block declares {content[0].strip()!r} and "
                                 f"nothing else; an empty diagram body renders blank"))
        if not section.prose_lines():
            found.append(Finding(rel_path, section.line,
                                 f"section {section.title!r} holds a diagram and no prose; "
                                 f"the text carries the constraints the diagram cannot"))
    return found


# --- glossary ------------------------------------------------------------

def glossary_columns(language: str, local: bool) -> list[str]:
    columns = ["term"]
    if language != "en":
        columns.append("english")
    if local:
        columns.append("scope")
    columns.append("definition")
    return columns


def glossary_header_key(cell: str) -> str | None:
    text = cell.strip("*` ").casefold()
    for key, aliases in GLOSSARY_COLUMNS.items():
        if text in aliases:
            return key
    return None


def glossary_column_hint(language: str, local: bool) -> str:
    """The table shape a finding asks for, in the document's language."""
    return " | ".join(canonical(GLOSSARY_COLUMN_NAMES[key], language)
                      for key in glossary_columns(language, local))


def table_row_cells(raw: str) -> list[str]:
    """Cells of one table row. GFM's escaped pipe (\\|) is a literal pipe
    inside its cell, not a delimiter."""
    text = raw.strip().strip("|")
    cells: list[str] = []
    current: list[str] = []
    i = 0
    while i < len(text):
        if text[i] == "\\" and i + 1 < len(text) and text[i + 1] == "|":
            current.append("|")
            i += 2
            continue
        if text[i] == "|":
            cells.append("".join(current).strip())
            current = []
        else:
            current.append(text[i])
        i += 1
    cells.append("".join(current).strip())
    return cells


def glossary_table_findings(rel: str, block, local: bool,
                            language: str) -> list[Finding]:
    """One glossary table against the fixed column order, and every entry
    against the English-counterpart requirement. The counterpart is what
    keeps one term from becoming two words in translation, so a row that
    leaves it out is the defect the glossary exists to prevent."""
    (header_line, header), body = block
    expected = glossary_columns(language, local)
    actual = [glossary_header_key(cell) for cell in header]
    if actual != expected:
        return [Finding(rel, header_line,
                        f"glossary columns are not the standard's; write "
                        f"{glossary_column_hint(language, local)}")]
    found: list[Finding] = []
    index = {key: position for position, key in enumerate(expected)}
    for n, raw, _, _ in body:
        cells = table_row_cells(raw)
        if len(cells) != len(expected):
            found.append(Finding(rel, n,
                                 f"glossary entry has {len(cells)} cells; the table "
                                 f"declares {len(expected)} columns"))
            continue
        term = cells[index["term"]].strip("*` ")
        if not term:
            found.append(Finding(rel, n, "glossary entry has no term"))
        if not cells[index["definition"]].strip("*` "):
            found.append(Finding(rel, n, f"glossary entry {term!r} has no definition"))
        if local and not cells[index["scope"]].strip("*` "):
            found.append(Finding(rel, n,
                                 f"glossary entry {term!r} states no scope; a local "
                                 f"glossary entry says where its meaning applies"))
        if "english" not in index:
            continue
        counterpart = cells[index["english"]].strip("*` ")
        if not counterpart:
            found.append(Finding(rel, n,
                                 f"glossary entry {term!r} carries no English "
                                 f"counterpart; it is what keeps the term the same "
                                 f"in translation and findable in the code"))
        elif CJK_RE.search(counterpart):
            found.append(Finding(rel, n,
                                 f"glossary entry {term!r} gives {counterpart!r} as its "
                                 f"English counterpart; the counterpart is English"))
        elif not re.search(r"[A-Za-z]", counterpart):
            found.append(Finding(rel, n,
                                 f"glossary entry {term!r} gives no English word as "
                                 f"its counterpart"))
    return found


def check_local_glossary(rel: str, text: str, language: str,
                         table: list) -> list[Finding]:
    """A document's optional Glossary section: when present, it is an entry
    table in the standard's column order."""
    found: list[Finding] = []
    for section in parse_sections(text):
        if section.level != 2:
            continue
        key, _ = match_section(section.title, table, language)
        if key != "glossary":
            continue
        blocks = table_blocks(section.lines)
        if not blocks:
            found.append(Finding(rel, section.line,
                                 f"section {section.title!r} holds no glossary table; "
                                 f"every entry is a row of "
                                 f"{glossary_column_hint(language, True)}"))
            continue
        for block in blocks:
            found += glossary_table_findings(rel, block, True, language)
    return found


def check_project_glossary(rel: str, text: str, language: str) -> list[Finding]:
    """The project glossary's entry table. It carries no scope column --
    every term in it holds project-wide by definition."""
    blocks = table_blocks(scan_lines(text))
    if not blocks:
        return [Finding(rel, 0,
                        "the project glossary holds no entry table; every entry is a "
                        f"row of {glossary_column_hint(language, False)}")]
    found: list[Finding] = []
    for block in blocks:
        found += glossary_table_findings(rel, block, False, language)
    return found


def check_heading_language(rel_path: str, text: str, language: str,
                           table: list) -> list[Finding]:
    found = []
    for section in parse_sections(text):
        if section.level != 2:
            continue
        _, wrong = match_section(section.title, table, language)
        if wrong:
            found.append(Finding(rel_path, section.line,
                                 f"section {section.title!r} is written in {wrong}; the "
                                 f"documentation language is {language}"))
    return found


def check_section_order(rel_path: str, text: str, language: str, table: list,
                        level: int = 2, allow_unknown: bool = False) -> list[Finding]:
    """Mandatory sections present, recognized sections in the fixed order,
    and (unless allow_unknown) no heading outside the standard's set."""
    found: list[Finding] = []
    order = [key for key, _, _ in table]
    seen: list[tuple[str, int]] = []
    for section in parse_sections(text):
        if section.level != level:
            continue
        key, wrong = match_section(section.title, table, language)
        if key is None:
            if not allow_unknown:
                found.append(Finding(rel_path, section.line,
                                     f"section {section.title!r} is not part of this document "
                                     f"type's fixed section set"))
            continue
        if wrong is None:
            seen.append((key, section.line))
    for key, name, mandatory in table:
        if mandatory and key not in [k for k, _ in seen]:
            found.append(Finding(rel_path, 0,
                                 f"missing mandatory section {canonical(name, language)!r}"))
    positions = [(order.index(key), key, line) for key, line in seen if key in order]
    for earlier, later in zip(positions, positions[1:]):
        if later[0] < earlier[0]:
            expected = dict((k, canonical(n, language)) for k, n, _ in table)
            found.append(Finding(rel_path, later[2],
                                 f"section {expected[later[1]]!r} comes after "
                                 f"{expected[earlier[1]]!r}; the section order is fixed"))
    duplicates = [k for k, _ in seen if [x for x, _ in seen].count(k) > 1]
    for key in sorted(set(duplicates)):
        name = dict((k, canonical(n, language)) for k, n, _ in table)[key]
        found.append(Finding(rel_path, 0, f"section {name!r} appears more than once"))
    return found


def check_filename_sequence(rel_path: str, siblings=()) -> list[Finding]:
    """A number in a file name is a sequence number when it orders the file
    among its siblings: zero-padded, or the only difference from a sibling
    (adr-core-scope.md beside adr-core-scope-2.md), or what a sibling's
    name lacks. A number that is part of the name -- prd-iso-27001,
    adr-tls-1-3 -- orders nothing and is not a sequence number."""
    name = os.path.basename(rel_path)
    stem = name[:-3]
    tokens = stem.split("-")
    sibling_stems = {other[:-3] for other in siblings
                     if other.endswith(".md") and other != name}
    for index, token in enumerate(tokens):
        if not token.isdigit():
            continue
        without = "-".join(tokens[:index] + tokens[index + 1:])
        peer = any(
            len(other) == len(tokens)
            and other[index].isdigit()
            and all(a == b for j, (a, b) in enumerate(zip(tokens, other)) if j != index)
            for other in (s.split("-") for s in sibling_stems))
        zero_padded = len(token) > 1 and token.startswith("0")
        if zero_padded or peer or without in sibling_stems:
            return [Finding(rel_path, 0,
                            f"file name carries a sequence number ({token}); ordering "
                            f"lives in the README index, not in file names")]
    return []


def check_index(readme_path: str, rel_readme: str, tree: str, root: str,
                language: str, label: str, expected: list[str] | None = None,
                require_index: bool = False) -> list[Finding]:
    """Every expected file appears in the README index exactly once, and
    every index link resolves.

    `expected` is the set of files the index must cover, as absolute
    paths; None means every markdown file of the tree. A tree's own
    README covers the whole tree; a module subdirectory's README covers
    that subdirectory, so its `tree` is still the design tree (links may
    point within it) while `expected` lists only the subdirectory."""
    found: list[Finding] = []
    text = strip_comments(read(readme_path))
    sections = parse_sections(text)
    lines = scan_lines(text)
    index_section = next(
        (s for s in sections if s.level == 2
         and match_section(s.title, [("index", INDEX_SECTION, True)], language)[0]),
        None)
    # The index runs from its heading to the next section the standard
    # recognises -- Implementation Status, a glossary, another index.
    # Grouping the entries is the README's business, at any heading level: a
    # tree's index groups under H3, an ADR index under H2 topics, and either
    # is an index. Truncating at the next H2 would turn every entry in an
    # H2-grouped index into an orphan; running to the end of the file would
    # count the status roll-up's links as duplicate index entries.
    if index_section is None:
        region = lines
    else:
        start = index_section.line
        end = next((s.line for s in sections
                    if s.line > start and s.level <= 2
                    and normalize_heading(s.title) in INDEX_STOP_TITLES), len(lines) + 1)
        region = [line for line in lines if start <= line[0] < end]
    linked: dict[str, list[int]] = {}
    for n, raw, in_fence, _ in region:
        if in_fence:
            continue
        for target in LINK_RE.findall(raw):
            if re.match(r"^[a-z][a-z0-9+.-]*:", target) or target.startswith("#"):
                continue
            clean = target.split("#", 1)[0]
            if not clean.endswith(".md"):
                continue
            resolved = os.path.normpath(os.path.join(os.path.dirname(readme_path), clean))
            if not os.path.isfile(resolved):
                found.append(Finding(rel_readme, n,
                                     f"index link {target!r} points at no file"))
                continue
            if os.path.commonpath([os.path.abspath(resolved), os.path.abspath(tree)]) != os.path.abspath(tree):
                continue
            linked.setdefault(os.path.relpath(resolved, root), []).append(n)

    for rel, occurrences in sorted(linked.items()):
        if len(occurrences) > 1:
            found.append(Finding(rel_readme, occurrences[1],
                                 f"{rel} appears in the index more than once"))
    for path in (expected if expected is not None else markdown_files(tree)):
        rel = os.path.relpath(path, root)
        if os.path.abspath(path) == os.path.abspath(readme_path):
            continue
        if rel not in linked:
            found.append(Finding(rel, 0,
                                 f"absent from the {label} index ({rel_readme}); an orphan "
                                 f"is an error"))
    if require_index and not any(
            match_section(s.title, [("index", INDEX_SECTION, True)], language)[0]
            for s in sections if s.level == 2):
        found.append(Finding(rel_readme, 0,
                             f"no {canonical(INDEX_SECTION, language)!r} section"))
    return found


# --- design tree ---------------------------------------------------------

def status_of_row(row: str, vocabulary: dict, language: str) -> list[str]:
    """The status values a row carries. A table row carries a value only
    in a cell that is exactly that value, so "Unimplemented" is not
    "Implemented" and a capability named "Not implemented warning" is not
    a status. A list item or a field value carries it as a whole phrase."""
    stripped = row.strip()
    if "|" in stripped:
        cells = [cell.strip().strip("*`_ ").casefold()
                 for cell in stripped.strip("|").split("|")]
        return [key for key, name in vocabulary.items()
                if name[language].casefold() in cells]
    keys = []
    for key, name in vocabulary.items():
        if re.search(r"(?<!\w)" + re.escape(name[language]) + r"(?!\w)",
                     stripped, re.IGNORECASE):
            keys.append(key)
    if "implemented" in keys and "partial" in keys:
        keys.remove("implemented")  # "Partially implemented" holds "implemented"
    if "not-implemented" in keys and "implemented" in keys:
        keys.remove("implemented")
    return keys


def status_entries(section: Section) -> list[tuple[int, str]]:
    """The status section's data rows: the body rows of every table in
    the section, with or without leading pipes, and list items."""
    entries = []
    for _, body in table_blocks(section.lines):
        entries.extend((n, raw.strip()) for n, raw, _, _ in body)
    for n, raw in section.prose_lines():
        stripped = raw.strip()
        if re.match(r"^[-*+]\s+", stripped):
            entries.append((n, stripped))
    return sorted(entries)


def status_module_count(section: Section) -> int:
    """The number of distinct top-level modules the status roll-up names.

    A linked row counts by its link target, so a duplicate table row or a list
    item for the same module counts once. A row with no link counts once on its
    own; the missing link is already a finding, and dropping the row would make
    the multi-module trigger vanish for a document that needs fixing anyway."""
    linked: set[str] = set()
    unlinked = 0
    for _, row in status_entries(section):
        targets = LINK_RE.findall(row)
        if targets:
            linked.add(normalize_heading(targets[0].split("#")[0]))
        else:
            unlinked += 1
    return len(linked) if linked else unlinked


def check_design_status(rel_path: str, sections: list[Section], language: str,
                        roll_up: bool) -> list[Finding]:
    found: list[Finding] = []
    for section in sections:
        if section.level != 2:
            continue
        key, _ = match_section(section.title, MODULE_SECTIONS, language)
        if key != "status":
            continue
        entries = status_entries(section)
        if not entries:
            found.append(Finding(rel_path, section.line,
                                 "implementation status section carries no entry"))
        for n, row in entries:
            for vague in VAGUE_STATUS:
                if vague.casefold() in row.casefold():
                    found.append(Finding(rel_path, n,
                                         f"vague status {vague!r}; the value is one of "
                                         f"{'/'.join(canonical(v, language) for v in DESIGN_STATUS.values())}"))
            keys = status_of_row(row, DESIGN_STATUS, language)
            if not keys:
                found.append(Finding(rel_path, n,
                                     f"status entry carries none of "
                                     f"{'/'.join(canonical(v, language) for v in DESIGN_STATUS.values())}"))
                continue
            if len(keys) > 1:
                found.append(Finding(rel_path, n, "status entry carries more than one status value"))
                continue
            if roll_up:
                if not LINK_RE.search(row):
                    found.append(Finding(rel_path, n,
                                         "roll-up row does not link to the module document"))
                continue
            if keys[0] in ("implemented", "partial") and not CODE_ANCHOR_RE.search(row):
                found.append(Finding(rel_path, n,
                                     "status entry claims implementation with no code anchor "
                                     "(path plus symbol name)"))
    return found


def aspect_parent(slug: str, slugs: set[str]) -> str | None:
    """The module a flat design-<slug>-<aspect>.md belongs to: the longest
    module slug beside it that this slug extends by "-<aspect>", or None
    when there is none and the file is a module document of its own."""
    parts = slug.split("-")
    for cut in range(len(parts) - 1, 0, -1):
        candidate = "-".join(parts[:cut])
        suffix = "-".join(parts[cut:])
        # An aspect names an aspect; a bare number is a sequence number.
        if candidate in slugs and re.search(r"[A-Za-z]", suffix):
            return candidate
    return None


def nearest_h2(sections: list[Section], child: Section) -> Section | None:
    return next((s for s in reversed(sections)
                 if s.level == 2 and s.line < child.line), None)


def check_module_readability_sections(rel: str, sections: list[Section],
                                      language: str) -> list[Finding]:
    """Worked Example and State at a Glance sit under their parent sections."""
    found: list[Finding] = []
    checks = [
        (WORKED_EXAMPLE, "algorithms"),
        (STATE_AT_A_GLANCE, "data-structures"),
    ]
    for section in sections:
        if section.level != 3:
            continue
        for name, parent_key in checks:
            key, wrong = match_section(section.title, [("readability", name, True)],
                                       language)
            if key is None:
                continue
            if wrong is not None:
                found.append(Finding(rel, section.line,
                                     f"section {section.title!r} is written in {wrong}; "
                                     f"the document language is {language}"))
                continue
            parent = nearest_h2(sections, section)
            if (parent is None or
                    match_section(parent.title, MODULE_SECTIONS, language)[0] != parent_key):
                parent_name = canonical(MODULE_SECTION_NAMES[parent_key], language)
                found.append(Finding(rel, section.line,
                                     f"section {section.title!r} must sit under "
                                     f"{parent_name!r}"))
    return found


def check_module_document(rel: str, text: str, language: str) -> list[Finding]:
    """The fixed section set a module document obeys, wherever it lives --
    flat under modules/ or inside a module subdirectory."""
    found: list[Finding] = []
    sections = parse_sections(text)
    found += check_heading_language(rel, text, language, MODULE_SECTIONS)
    found += check_section_order(rel, text, language, MODULE_SECTIONS)
    found += check_local_glossary(rel, text, language, MODULE_SECTIONS)
    found += check_design_status(rel, sections, language, roll_up=False)
    found += check_module_readability_sections(rel, sections, language)
    responsibilities = [s for s in sections if s.level == 3
                        and normalize_heading(s.title)
                        == normalize_heading(canonical(NON_RESPONSIBILITIES, language))]
    if not responsibilities:
        found.append(Finding(rel, 0,
                             f"no {canonical(NON_RESPONSIBILITIES, language)!r} subsection; what a "
                             f"module does not own is written down"))
    return found


def overview_section(sections: list[Section], language: str) -> Section | None:
    return next((s for s in sections if s.level == 2 and
                 match_section(s.title, ARCHITECTURE_SECTIONS, language)[0] == "overview"), None)


def check_core_concepts(rel_path: str, sections: list[Section], overview: Section,
                        children: list[Section], language: str,
                        module_count: int) -> list[Finding]:
    """Core Concepts is the first Overview H3 when required, and whenever it
    appears it carries a concept map and no second glossary table."""
    required = module_count > 1
    found: list[Finding] = []
    core_sections: list[Section] = []
    wrong_core: list[Section] = []
    for section in sections:
        if section.level != 3:
            continue
        key, wrong = match_section(section.title, CORE_CONCEPTS_SECTION, language)
        if wrong is not None:
            wrong_core.append(section)
            found.append(Finding(rel_path, section.line,
                                 f"section {section.title!r} is written in {wrong}; "
                                 f"the document language is {language}"))
        elif key is not None:
            core_sections.append(section)
    if len(core_sections) > 1:
        found.append(Finding(rel_path, core_sections[1].line,
                             "second Core Concepts subsection; the concept map has one owner"))
    overview_core = [s for s in core_sections if s in children]
    if overview_core:
        first = children[0]
        if first is not overview_core[0]:
            found.append(Finding(rel_path, first.line,
                                 f"Overview opens with {first.title!r}; Core Concepts is its "
                                 f"first subsection"))
    elif core_sections:
        found.append(Finding(rel_path, core_sections[0].line,
                             "Core Concepts must be the first H3 of the Overview"))
    elif required and not wrong_core:
        found.append(Finding(rel_path, overview.line,
                             "Overview carries no Core Concepts subsection; a system with "
                             "more than one top-level module states its core concepts"))
    if core_sections:
        core = core_sections[0]
        if not any(lang.lower() == "mermaid" for _, lang, _ in core.fences()):
            found.append(Finding(rel_path, core.line,
                                 "Core Concepts carries no mermaid concept map"))
        for (header_line, cells), _ in table_blocks(core.lines):
            header_keys = {glossary_header_key(cell) for cell in cells}
            normalized = {normalize_heading(cell) for cell in cells}
            if "definition" in header_keys and (
                    "term" in header_keys or
                    "concept" in normalized or "概念" in normalized):
                found.append(Finding(rel_path, header_line,
                                     "Core Concepts carries a second glossary table; "
                                     "definitions belong to the project glossary"))
    return found


def mainline_header_ok(cells: list[str]) -> bool:
    """Whether a table header names the three Design Mainline concepts."""
    normalized = {normalize_heading(cell) for cell in cells}
    return all(
        any(normalize_heading(column[lang]) in normalized for lang in LANGUAGES)
        for column in DESIGN_MAINLINE_COLUMNS
    )


def check_design_mainline(rel_path: str, sections: list[Section], overview: Section,
                          children: list[Section], language: str,
                          module_count: int) -> list[Finding]:
    """Design Mainline follows Core Concepts when required, and whenever it
    appears it carries the problem/mechanism/location table."""
    required = module_count > 1
    found: list[Finding] = []
    all_mainlines: list[Section] = []
    overview_mainlines: list[Section] = []
    wrong_in_overview = False
    for section in sections:
        if section.level != 3:
            continue
        key, wrong = match_section(section.title, DESIGN_MAINLINE_SECTION, language)
        if wrong is not None:
            found.append(Finding(rel_path, section.line,
                                 f"section {section.title!r} is written in {wrong}; "
                                 f"the document language is {language}"))
            if section in children:
                wrong_in_overview = True
        elif key is not None:
            all_mainlines.append(section)
            if section in children:
                overview_mainlines.append(section)
    if len(all_mainlines) > 1:
        found.append(Finding(rel_path, all_mainlines[1].line,
                             "second Design Mainline subsection; the mainline has one owner"))
    if not overview_mainlines:
        if required and not wrong_in_overview:
            found.append(Finding(rel_path, overview.line,
                                 "Overview carries no Design Mainline subsection; a system with "
                                 "more than one top-level module states its mainline"))
        return found
    mainline = overview_mainlines[0]
    mainline_index = children.index(mainline)
    core_index = next((index for index, section in enumerate(children)
                       if match_section(section.title, CORE_CONCEPTS_SECTION,
                                        language)[0] is not None), -1)
    if core_index != -1 and mainline_index < core_index:
        found.append(Finding(rel_path, mainline.line,
                             "Design Mainline comes before Core Concepts; the concept map "
                             "comes first"))
    tables = table_blocks(mainline.lines)
    if not tables:
        found.append(Finding(rel_path, mainline.line,
                             "Design Mainline carries no table; state the problem, the "
                             "mechanism and where it is specified"))
    elif not any(body for (_, _), body in tables):
        found.append(Finding(rel_path, mainline.line,
                             "Design Mainline table carries no data row; state at least "
                             "one problem, mechanism and location"))
    elif not any(mainline_header_ok(cells) for (_, cells), _ in tables):
        found.append(Finding(rel_path, mainline.line,
                             "Design Mainline table must carry the problem, mechanism "
                             "and location columns"))
    return found


def check_design_tree(tree: str, root: str, language: str) -> list[Finding]:
    found: list[Finding] = []
    rel_tree = os.path.relpath(tree, root)
    readme = os.path.join(tree, "README.md")
    architecture = os.path.join(tree, "architecture.md")
    for required in (readme, architecture):
        if not os.path.isfile(required):
            found.append(Finding(os.path.relpath(required, root), 0,
                                 "required design document is missing"))
    modules_dir = os.path.join(tree, "modules")
    flat_slugs: set[str] = set()       # slugs of the files directly under modules/
    if os.path.isdir(modules_dir):
        for entry in os.listdir(modules_dir):
            if (entry.startswith("design-") and entry.endswith(".md")
                    and os.path.isfile(os.path.join(modules_dir, entry))):
                flat_slugs.add(entry[len("design-"):-len(".md")])

    top_entries: list[str] = []        # what the tree's README index must cover
    roll_up_docs: list[str] = []       # module documents architecture.md must roll up
    aspect_docs: list[str] = []        # aspect splits, covered by their module's row
    subdir_files: dict[str, list[str]] = {}   # module subdirectory -> its design-*.md files
    subdir_readmes: dict[str, str] = {}       # module subdirectory -> its README.md
    submodule_docs: dict[str, list[str]] = {}  # module subdirectory -> its sub-module documents

    for path in markdown_files(tree):
        rel = os.path.relpath(path, root)
        name = os.path.basename(path)
        text = strip_comments(read(path))
        found += check_common(rel, text)
        parent = os.path.dirname(path)
        if parent == tree:
            if name == "architecture.md":
                sections = parse_sections(text)
                found += check_heading_language(rel, text, language, ARCHITECTURE_SECTIONS)
                found += check_section_order(rel, text, language, ARCHITECTURE_SECTIONS)
                found += check_design_status(rel, sections, language, roll_up=True)
                for key in ("module-dependency-graph", "logical-deployment"):
                    targets = [s for s in sections if s.level == 2
                               and match_section(s.title, ARCHITECTURE_SECTIONS, language)[0] == key]
                    for section in targets:
                        if not any(f[1].lower() == "mermaid" for f in section.fences()):
                            found.append(Finding(rel, section.line,
                                                 f"section {section.title!r} carries no mermaid "
                                                 f"diagram; this diagram is mandatory"))
                top_entries.append(path)
            elif name != "README.md":
                found.append(Finding(rel, 0,
                                     f"unexpected document at the design tree root; a module "
                                     f"document lives in {rel_tree}/modules/"))
            continue
        if parent == modules_dir:
            found += check_filename_sequence(rel, os.listdir(modules_dir))
            if not name.startswith("design-"):
                found.append(Finding(rel, 0,
                                     "module document name does not match design-<slug>"))
            else:
                slug = name[len("design-"):-len(".md")]
                if not SLUG_RE.match(slug):
                    found.append(Finding(rel, 0,
                                         f"slug {slug!r} is not lowercase kebab-case ASCII"))
                if aspect_parent(slug, flat_slugs):
                    # design-<slug>-<aspect>.md beside design-<slug>.md is an
                    # aspect split of that module, not a module of its own.
                    found += check_heading_language(rel, text, language, MODULE_SECTIONS)
                    found += check_design_status(rel, parse_sections(text), language,
                                                 roll_up=False)
                    top_entries.append(path)
                    aspect_docs.append(path)
                    continue
            found += check_module_document(rel, text, language)
            top_entries.append(path)
            roll_up_docs.append(path)
            continue
        if os.path.dirname(parent) == modules_dir:
            slug = os.path.basename(parent)
            if not SLUG_RE.match(slug):
                found.append(Finding(rel, 0,
                                     f"module subdirectory {slug!r} is not lowercase kebab-case ASCII"))
            if name == "README.md":
                subdir_readmes[parent] = path
                top_entries.append(path)
                continue
            stem = re.fullmatch(re.escape("design-" + slug) + r"(?:-(.+))?\.md", name)
            if stem is None or (stem.group(1) is not None and not SLUG_RE.match(stem.group(1))):
                found.append(Finding(rel, 0,
                                     f"document in module subdirectory {slug!r} does not match "
                                     f"design-{slug}[-<sub>]"))
                continue
            found += check_filename_sequence(rel, os.listdir(parent))
            found += check_module_document(rel, text, language)
            subdir_files.setdefault(parent, []).append(path)
            if stem.group(1) is None:
                roll_up_docs.append(path)
            else:
                submodule_docs.setdefault(parent, []).append(path)
            continue
        found.append(Finding(rel, 0, "design tree has no directory below modules/<slug>/"))

    for parent in sorted(subdir_files):
        slug = os.path.basename(parent)
        if not os.path.isfile(os.path.join(parent, "design-" + slug + ".md")):
            found.append(Finding(os.path.relpath(parent, root), 0,
                                 f"module subdirectory has no design-{slug}.md; the module's "
                                 f"own document belongs there"))
        if parent not in subdir_readmes:
            found.append(Finding(os.path.relpath(parent, root), 0,
                                 "module subdirectory has no README.md index"))

    if os.path.isfile(readme):
        found += check_index(readme, os.path.relpath(readme, root), tree, root,
                             language, "design", expected=top_entries, require_index=True)
    for parent, index_path in sorted(subdir_readmes.items()):
        rel_index = os.path.relpath(index_path, root)
        index_text = strip_comments(read(index_path))
        found += check_index(index_path, rel_index, tree, root, language,
                             "module", expected=subdir_files.get(parent, []),
                             require_index=True)
        sections = parse_sections(index_text)
        found += check_heading_language(rel_index, index_text, language, SUBINDEX_SECTIONS)
        found += check_design_status(rel_index, sections, language, roll_up=True)
        referenced: set[str] = set()
        for section in sections:
            if section.level == 2 and match_section(
                    section.title, MODULE_SECTIONS, language)[0] == "status":
                for _, row in status_entries(section):
                    for target in LINK_RE.findall(row):
                        referenced.add(os.path.normpath(os.path.join(parent, target.split("#")[0])))
        for doc in submodule_docs.get(parent, []):
            if os.path.normpath(doc) not in referenced:
                found.append(Finding(rel_index, 0,
                                     f"sub-module status has no row for "
                                     f"{os.path.relpath(doc, root)}"))
        own_doc = os.path.normpath(
            os.path.join(parent, "design-" + os.path.basename(parent) + ".md"))
        if own_doc in referenced:
            found.append(Finding(rel_index, 0,
                                 "sub-module status carries a row for the module's own document; "
                                 "architecture.md already carries it"))
    if os.path.isfile(architecture):
        text = strip_comments(read(architecture))
        rel_architecture = os.path.relpath(architecture, root)
        sections = parse_sections(text)
        overview = overview_section(sections, language)
        children = direct_children(sections, overview) if overview is not None else []
        status_count = 0
        for section in sections:
            if (section.level == 2 and
                    match_section(section.title, ARCHITECTURE_SECTIONS, language)[0] == "status"):
                status_count = status_module_count(section)
        module_count = status_count
        if overview is not None:
            found += check_core_concepts(rel_architecture, sections, overview, children,
                                         language, module_count)
            found += check_design_mainline(rel_architecture, sections, overview, children,
                                           language, module_count)
        rolled: set[str] = set()
        for section in sections:
            if section.level == 2 and match_section(
                    section.title, ARCHITECTURE_SECTIONS, language)[0] == "status":
                for n, row in status_entries(section):
                    for target in LINK_RE.findall(row):
                        rolled.add(os.path.normpath(
                            os.path.join(os.path.dirname(architecture), target.split("#")[0])))
        for doc in roll_up_docs:
            if os.path.normpath(doc) not in rolled:
                found.append(Finding(os.path.relpath(architecture, root), 0,
                                     f"implementation status has no row for "
                                     f"{os.path.relpath(doc, root)}"))
        for doc in (d for docs in submodule_docs.values() for d in docs):
            if os.path.normpath(doc) in rolled:
                found.append(Finding(os.path.relpath(architecture, root), 0,
                                     f"implementation status carries a sub-module row for "
                                     f"{os.path.relpath(doc, root)}; only the top-level module "
                                     f"appears here, the sub-module in the module subdirectory"))
        for doc in aspect_docs:
            if os.path.normpath(doc) in rolled:
                found.append(Finding(os.path.relpath(architecture, root), 0,
                                     f"implementation status carries a row for aspect document "
                                     f"{os.path.relpath(doc, root)}; the module's own row covers "
                                     f"its aspects"))
    return found


# --- adr tree ------------------------------------------------------------

def adr_id(name: str) -> str | None:
    stem = name[:-3] if name.endswith(".md") else name
    if not stem.startswith("adr-"):
        return None
    slug = stem[len("adr-"):]
    if ADR_DATE_TOKEN_RE.search(slug) or not SLUG_RE.match(slug):
        return None
    return f"ADR-{slug}"


def check_adr_record(path: str, rel: str, text: str, language: str) -> tuple[list[Finding], str | None]:
    """Findings and the record's own id."""
    found: list[Finding] = []
    name = os.path.basename(path)
    record_id = adr_id(name)
    if record_id is None:
        found.append(Finding(rel, 0,
                             "file name does not match adr-<slug>; an ADR's name carries "
                             "no date and no sequence number"))
        if not ADR_DATE_TOKEN_RE.search(name[:-3] if name.endswith(".md") else name):
            found += check_filename_sequence(rel, os.listdir(os.path.dirname(path)))
    else:
        found += check_filename_sequence(rel, os.listdir(os.path.dirname(path)))
    found += check_heading_language(rel, text, language, ADR_SECTIONS)
    found += check_section_order(rel, text, language, ADR_SECTIONS)

    sections = parse_sections(text)
    for section in sections:
        if section.level and normalize_heading(section.title) in ADR_STATUS_HEADINGS:
            found.append(Finding(rel, section.line,
                                 f"section {section.title!r} records a status; an ADR carries "
                                 f"no status, state or acceptance field"))
    lines = scan_lines(text)
    for n, title in setext_headings(lines):
        if normalize_heading(title) in ADR_STATUS_HEADINGS:
            found.append(Finding(rel, n,
                                 f"section {title!r} records a status; an ADR carries no "
                                 f"status, state or acceptance field"))
    for n, raw, in_fence, _ in lines:
        if in_fence or FENCE_RE.match(raw):
            continue
        field = ADR_STATUS_FIELD_RE.match(raw)
        # Status, Acceptance, Accepted, Deprecated, 状态, 接受, 验收, 接受状态
        # name a record's status wherever they appear. Only "State" is also
        # an ordinary data field name ("- **State**: kept in Redis"), and
        # there the value tells the two apart.
        if field and (field.group(1).casefold() != "state"
                      or VERDICT_RE.search(raw[field.end():])):
            found.append(Finding(rel, n,
                                 f"status field {field.group(1)!r}; an ADR carries no status, "
                                 f"state or acceptance field"))
        if ADR_SUPERSEDED_RE.match(raw):
            found.append(Finding(rel, n,
                                 "supersession line; a changed decision is revised into its "
                                 "record, not marked as replaced"))
    for section in sections:
        if section.level != 2:
            continue
        key, _ = match_section(section.title, ADR_SECTIONS, language)
        if key == "options":
            options = [s for s in sections if s.level == 3 and s.line > section.line
                       and s.line < next((x.line for x in sections
                                          if x.level == 2 and x.line > section.line), 1 << 30)]
            if len(options) < 2:
                found.append(Finding(rel, section.line,
                                     f"options considered lists {len(options)} option(s); a "
                                     f"decision weighs alternatives, each an H3"))
    return found, record_id


def check_adr_tree(tree: str, root: str, language: str) -> list[Finding]:
    found: list[Finding] = []
    readme = os.path.join(tree, "README.md")
    if not os.path.isfile(readme):
        found.append(Finding(os.path.relpath(readme, root), 0,
                             "the ADR tree has no README index"))
    titles: dict[str, str] = {}
    for path in markdown_files(tree):
        rel = os.path.relpath(path, root)
        if os.path.basename(path) == "README.md":
            found += check_common(rel, strip_comments(read(path)))
            continue
        if os.path.dirname(path) != tree:
            found.append(Finding(rel, 0, "the ADR tree is flat; records live beside README.md"))
            continue
        text = strip_comments(read(path))
        found += check_common(rel, text)
        record_findings, record_id = check_adr_record(path, rel, text, language)
        found += record_findings
        if record_id:
            for section in parse_sections(text):
                if section.level == 1:
                    titles[record_id] = section.title
                    break

    if os.path.isfile(readme):
        rel_readme = os.path.relpath(readme, root)
        found += check_index(readme, rel_readme, tree, root, language, "ADR")
        text = strip_comments(read(readme))
        sections = parse_sections(text)
        topics = [s for s in sections if s.level == 2]
        if not topics:
            found.append(Finding(rel_readme, 0,
                                 "the index is not grouped by topic; each topic is an H2"))
        first_topic = min((s.line for s in sections if s.level == 2), default=0)
        lines = scan_lines(text)
        for (header_line, header_cells), body in table_blocks(lines):
            if header_line < first_topic:
                continue
            if not any(ADR_INDEX_LINK_RE.search(raw) for _, raw, _, _ in body):
                continue
            if any(forbidden_column(cell) for cell in header_cells):
                found.append(Finding(rel_readme, header_line,
                                     "the index carries a date or status column; an ADR's "
                                     "identity is its decision, nothing else"))
        for n, raw, in_fence, _ in lines:
            if in_fence or n < first_topic:
                continue
            for title, target in ADR_INDEX_LINK_RE.findall(raw):
                record_id = adr_id(os.path.basename(target))
                if record_id is None or record_id not in titles:
                    continue
                if normalize_heading(title) != normalize_heading(titles[record_id]):
                    found.append(Finding(rel_readme, n,
                                         f"index row for {record_id} does not use the record's "
                                         f"own title ({titles[record_id]!r})"))
    return found


# --- requirements tree ---------------------------------------------------

def check_requirement_entries(rel: str, text: str, language: str, slug: str) -> list[Finding]:
    found: list[Finding] = []
    sections = parse_sections(text)
    seen: dict[str, int] = {}
    status_label = {"en": "status", "zh-CN": "状态"}[language]
    e2e_label = {"en": "e2e test", "zh-CN": "端到端测试"}[language]
    for index, section in enumerate(sections):
        if section.level != 3:
            continue
        title = normalize_heading(section.title)
        if not title.startswith("req-"):
            continue
        match = REQ_ID_RE.match(title)
        if not match:
            found.append(Finding(rel, section.line,
                                 f"requirement id {section.title!r} does not match "
                                 f"REQ-<slug>-NNN"))
            continue
        if match.group(1) != slug:
            found.append(Finding(rel, section.line,
                                 f"requirement id carries slug {match.group(1)!r}; the "
                                 f"document's slug is {slug!r}"))
        if len(match.group(2)) != 3:
            found.append(Finding(rel, section.line,
                                 f"requirement number {match.group(2)!r} is not three digits"))
        if title in seen:
            found.append(Finding(rel, section.line,
                                 f"requirement id {title} is used twice; an id is unique "
                                 f"within its document"))
        seen[title] = section.line

        body = [t for _, t in section.prose_lines()]
        joined = "\n".join(body)
        if any(v.casefold() in joined.casefold() for v in VAGUE_STATUS):
            found.append(Finding(rel, section.line, "vague status wording"))
        if canonical(DESIGN_STATUS["partial"], language).casefold() in joined.casefold():
            found.append(Finding(rel, section.line,
                                 f"{canonical(DESIGN_STATUS['partial'], language)!r} does not exist in a "
                                 f"requirements document; split the requirement"))
        has_status = False
        has_e2e = False
        for line in body:
            label = re.match(r"^\s*[-*+]\s*\**\s*([^*:：]+?)\s*\**\s*[:：]", line)
            if not label:
                continue
            name = label.group(1).strip().casefold()
            if name == status_label:
                has_status = True
                if not status_of_row(line.split(":", 1)[-1], REQUIREMENT_STATUS, language):
                    found.append(Finding(rel, section.line,
                                         f"status is neither "
                                         f"{canonical(REQUIREMENT_STATUS['implemented'], language)!r} nor "
                                         f"{canonical(REQUIREMENT_STATUS['not-implemented'], language)!r}"))
            if name == e2e_label:
                has_e2e = True
        if not has_status:
            found.append(Finding(rel, section.line, "requirement records no implementation status"))
        if not has_e2e:
            found.append(Finding(rel, section.line, "requirement records no end-to-end test status"))
    return found


def check_requirements_tree(tree: str, root: str, language: str) -> list[Finding]:
    found: list[Finding] = []
    readme = os.path.join(tree, "README.md")
    overview = os.path.join(tree, "overview.md")
    for required in (readme, overview):
        if not os.path.isfile(required):
            found.append(Finding(os.path.relpath(required, root), 0,
                                 "required requirements document is missing"))
    journeys = os.path.join(tree, "journeys")
    features = os.path.join(tree, "features")

    for path in markdown_files(tree):
        rel = os.path.relpath(path, root)
        name = os.path.basename(path)
        text = strip_comments(read(path))
        found += check_common(rel, text)
        parent = os.path.dirname(path)
        if name not in ("README.md", "overview.md"):
            found += check_filename_sequence(rel, os.listdir(parent))
        if parent == features:
            if not name.startswith("prd-"):
                found.append(Finding(rel, 0, "capability document name does not match prd-<slug>"))
                continue
            slug = name[len("prd-"):-len(".md")]
            if not SLUG_RE.match(slug):
                found.append(Finding(rel, 0, f"slug {slug!r} is not lowercase kebab-case ASCII"))
            found += check_heading_language(rel, text, language, FEATURE_SECTIONS)
            found += check_section_order(rel, text, language, FEATURE_SECTIONS)
            found += check_local_glossary(rel, text, language, FEATURE_SECTIONS)
            found += check_requirement_entries(rel, text, language, slug)
        elif parent == journeys:
            if not name.startswith("journey-"):
                found.append(Finding(rel, 0, "journey document name does not match journey-<slug>"))
                continue
            slug = name[len("journey-"):-len(".md")]
            if not SLUG_RE.match(slug):
                found.append(Finding(rel, 0, f"slug {slug!r} is not lowercase kebab-case ASCII"))
            found += check_heading_language(rel, text, language, JOURNEY_SECTIONS)
            found += check_section_order(rel, text, language, JOURNEY_SECTIONS)
        elif parent == tree and name == "overview.md":
            found += check_heading_language(rel, text, language, OVERVIEW_SECTIONS)
            found += check_section_order(rel, text, language, OVERVIEW_SECTIONS,
                                         allow_unknown=True)
        elif parent == tree and name != "README.md":
            found.append(Finding(rel, 0,
                                 "unexpected document at the requirements tree root; a "
                                 "capability lives in features/, a journey in journeys/"))
        elif parent not in (tree, journeys, features):
            found.append(Finding(rel, 0,
                                 "requirements tree has no directory below journeys/ and features/"))

    if os.path.isfile(readme):
        found += check_index(readme, os.path.relpath(readme, root), tree, root,
                             language, "requirements")
    return found


# --- project references --------------------------------------------------

def reference_entries(rel_refs: str, text: str) -> tuple[list[Finding], dict[str, int]]:
    """Findings on the references file's own rows, and slug -> line for
    every well-formed entry. An entry is a table row whose first cell is
    REF-<slug>; any other row is prose or a header and is not an entry."""
    found: list[Finding] = []
    entries: dict[str, int] = {}
    for _, body in table_blocks(scan_lines(text)):
        for n, raw, _, _ in body:
            cells = [cell.strip() for cell in raw.strip().strip("|").split("|")]
            first = cells[0].strip("*` ") if cells else ""
            if not first.upper().startswith("REF-"):
                continue
            slug = first[len("REF-"):]
            if not first.startswith("REF-") or not SLUG_RE.match(slug):
                found.append(Finding(rel_refs, n,
                                     f"entry {first!r} is not REF-<slug> with a lowercase "
                                     f"kebab-case ASCII slug"))
                continue
            if slug in entries:
                found.append(Finding(rel_refs, n,
                                     f"REF-{slug} is registered more than once; one source, "
                                     f"one entry"))
                continue
            entries[slug] = n
            if not URL_RE.search(raw):
                found.append(Finding(rel_refs, n,
                                     f"entry REF-{slug} carries no URL; the references file "
                                     f"registers public sources, and the source column is a "
                                     f"URL any reader can open"))
    return found, entries


def check_references(root: str, config: dict[str, str], trees: list[str],
                     language: str) -> list[Finding]:
    """The project references file and every citation of it.

    One file at the configured path, shared by every document type: one
    row per public external source, keyed REF-<slug>. Every citation in
    a checked tree or in the glossary resolves to a row, and every row
    is cited at least once -- an uncited entry is an orphan, like a
    document absent from its index."""
    found: list[Finding] = []
    rel_refs = config["references"]
    refs_path = os.path.join(root, rel_refs)

    citations: list[tuple[str, int, str]] = []
    scanned = [path for tree in trees for path in markdown_files(tree)]
    glossary = os.path.join(root, config["glossary"])
    if os.path.isfile(glossary):
        scanned.append(glossary)
    for path in scanned:
        if os.path.abspath(path) == os.path.abspath(refs_path):
            continue
        rel = os.path.relpath(path, root)
        for n, raw, in_fence, _ in scan_lines(strip_comments(read(path))):
            if in_fence or FENCE_RE.match(raw):
                continue
            for slug in REF_CITATION_RE.findall(raw):
                citations.append((rel, n, slug))

    if not os.path.isfile(refs_path):
        found.append(Finding(rel_refs, 0,
                             "the configured references file does not exist; the project "
                             "has exactly one, shared by every document type"))
        return found

    text = strip_comments(read(refs_path))
    found += check_common(rel_refs, text)
    entry_findings, entries = reference_entries(rel_refs, text)
    found += entry_findings

    cited: set[str] = set()
    for rel, n, slug in citations:
        if not SLUG_RE.match(slug):
            found.append(Finding(rel, n,
                                 f"citation REF-{slug} is not REF-<slug> with a lowercase "
                                 f"kebab-case ASCII slug"))
            continue
        cited.add(slug)
        if slug not in entries:
            found.append(Finding(rel, n,
                                 f"cites REF-{slug}, which the project references file "
                                 f"({rel_refs}) does not register"))
    for slug, n in sorted(entries.items(), key=lambda item: item[1]):
        if slug not in cited:
            found.append(Finding(rel_refs, n,
                                 f"REF-{slug} is cited by no document; an uncited entry is "
                                 f"an orphan, and an orphan is an error"))
    return found


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Structural checker for the document-standards skill.")
    parser.add_argument("--root", default=".",
                        help="repository root holding CLAUDE.md (default: .)")
    args = parser.parse_args(argv)
    root = os.path.abspath(args.root)
    if not os.path.isdir(root):
        print(f"error: --root is not a directory: {args.root}", file=sys.stderr)
        return 2

    UNDECODABLE.clear()
    config, errors = parse_documentation_config(root)
    for key in ("design", "requirements", "adr", "glossary", "references"):
        if key in config:
            # "docs/adr/" and "docs/adr" name the same tree; trailing
            # separators would defeat the string comparison against walk.
            config[key] = os.path.normpath(config[key])
    if errors:
        for error in errors:
            print(f"error: {error}", file=sys.stderr)
        return 2
    language = config["language"]

    found: list[Finding] = []
    checked = []
    trees: list[str] = []
    for key, checker, label in (("design", check_design_tree, "design"),
                                ("adr", check_adr_tree, "ADR"),
                                ("requirements", check_requirements_tree, "requirements")):
        tree = os.path.join(root, config[key])
        if not os.path.isdir(tree):
            continue
        checked.append(f"{label} ({os.path.relpath(tree, root)})")
        trees.append(tree)
        found += checker(tree, root, language)

    if not checked:
        print("error: no documentation tree found at the configured paths "
              f"(design: {config['design']}, adr: {config['adr']}, "
              f"requirements: {config['requirements']}); nothing to check is a "
              "configuration error, not a pass", file=sys.stderr)
        return 2

    glossary = os.path.join(root, config["glossary"])
    if not os.path.isfile(glossary):
        found.append(Finding(config["glossary"], 0,
                             "the configured glossary does not exist; the project has "
                             "exactly one, shared by every document type"))
    else:
        rel_glossary = os.path.relpath(glossary, root)
        found += check_project_glossary(rel_glossary,
                                        strip_comments(read(glossary)), language)
    found += check_references(root, config, trees, language)
    for path, message in sorted(UNDECODABLE.items()):
        found.append(Finding(os.path.relpath(path, root), 0, message))

    # A diagram or a table is seen by its own section and by every section
    # enclosing it; report it once.
    unique = {(f.path, f.line, f.message): f for f in found}
    for finding in sorted(unique.values(), key=Finding.sort_key):
        print(finding.render())
    found = list(unique.values())
    if found:
        print(f"FAILED: {len(found)} structural problem(s) in {', '.join(checked)}; "
              f"the standard is {SKILL_DIR}/")
        return 1
    print(f"OK: {', '.join(checked)} structurally conform to document-standards "
          f"(language: {language})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
