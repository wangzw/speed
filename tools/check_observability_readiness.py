#!/usr/bin/env python3
"""Observability-readiness gate over pkg/."""

from __future__ import annotations

import argparse
import os
import re
import sys

# Directories whose contents are not this repository's own Go source.
SKIPPED_DIRS = {".git", "node_modules", "testdata", "vendor"}


# A resolution of the logging capability: a helper name, the capability, and a
# call. Matching the element type rather than the helper name keeps a further
# helper under the same rule.
RESOLVES_LOGGER = re.compile(r"\b\w+\[log\.Logger\]\s*\(")

# The declaration, written as the requirement token. A module descriptor
# carries it as `{Token: (*log.Logger)(nil), Optional: true}`.
DECLARES_LOGGER = re.compile(r"\(\*log\.Logger\)\(nil\)")

# Packages known to resolve and declare the logging capability, so the positive
# control can refuse a scan that read none of them.
LOGGER_ANCHORS = ("pkg/settings", "pkg/db", "pkg/http", "pkg/authn/login")


# One sensitive item of the configuration schema. The flag is read as an item
# only where the composite literal it opens is one of the two the tree declares
# an item in; see ITEM_KEY_ENTRY and ITEM_TYPED_LITERAL below.
SENSITIVE_ITEM = re.compile(r"\bSensitive:\s*true\b")

# The two shapes a configuration item is written in, read just before the brace
# of its literal: a keyed map entry (`KEY: { ... }`) or a literal of the item
# type (`config.Item{...}`). A `Sensitive: true` in another type is not an item.
ITEM_KEY_ENTRY = re.compile(r'("[^"]+"|[A-Za-z_]\w*)\s*:\s*\Z')
ITEM_TYPED_LITERAL = re.compile(r"(?:[A-Za-z_]\w*\.)?Item\s*\Z")

# A string constant, which is how a schema names its item keys. The single
# line form and the entry of a `const (...)` block are both matched.
STRING_CONST = re.compile(r'^\s*(?:const\s+)?([A-Za-z_]\w*)\s*=\s*"([^"]*)"', re.MULTILINE)

# A `literal.Register(` call. Its argument list is read to count the values
# the package hands over: the redaction rule, a label, then the values.
LITERAL_REGISTER = re.compile(r"\bliteral\.Register\(")

# The Secrets hook that hands secret values to the factory: the Spec field typed
# `func(cfg *C) []string`, its func literal, or an identifier naming such a
# function. A field merely named Secrets hands no value over.
SECRETS_FUNC_FIELD = re.compile(r"\bSecrets\s+func\b[^\n]*\[\]string")
SECRETS_FUNC_VALUE = re.compile(r"\bSecrets\s*:\s*func\b[^\n]*\[\]string")
SECRETS_REF = re.compile(r"\bSecrets\s*:\s*([A-Za-z_]\w*)\b")

# A function declared in the package that returns a `[]string`, which is what
# a `Secrets:` identifier reference must name to be a handoff.
_DECLARES_STRING_FUNC = r"\bfunc\s+{name}\s*\([^)]*\)\s*\[\]string"

# The module-prefixed credential-name token a redaction label carries, written
# `ModuleName+".x"`; a registration without it is not this package's own.
MODULE_NAME = re.compile(r"\bModuleName\b")

# An `Items()` config provider, and a module descriptor. A package that
# provides items and declares no module is a shared config unit.
PROVIDES_ITEMS = re.compile(r"\bfunc\s+Items\(")
DECLARES_MODULE = re.compile(r"\bfunc\s+Module\(|\bNewModule\(")

# Sensitive items whose value is a filesystem path, named by package directory
# and operator-facing key: a named allow-list, not a `-file` key-shape scan.
PATH_ITEMS = {
    ("pkg/authn/sso/saml", "sp-key-file"),
    ("pkg/authn/token", "signing-key-file"),
}

SENSITIVE_ANCHORS = ("pkg/db", "pkg/event/nats", "pkg/authn/login/github")


EGRESS_NEW = re.compile(r"\begress\.New\s*\(")
EGRESS_TRANSPORT = re.compile(r"\bobservability\.Transport\(")

EGRESS_EXEMPT = "pkg/authn/login/internal/wechat"

EGRESS_ANCHORS = ("pkg/authn/login/github", "pkg/authn/sso/oidc")


GLOBAL_OTEL = re.compile(
    r"\botel\.(?:Tracer|Meter|SetTracerProvider|GetTracerProvider|"
    r"SetMeterProvider|GetMeterProvider|Handle)\s*\("
)

# Any import of the upstream telemetry library. The positive control requires
# one, so the absence of `otel.Tracer`/`otel.Meter` is the absence of a call
# in a tree that uses otel at all, not the absence of otel.
UPSTREAM_OTEL = re.compile(r'"go\.opentelemetry\.io/otel')


class _InfrastructureError(Exception):
    """Raised where the scan cannot run or drew an empty set; main turns it into
    exit 2."""


def _infra(message: str) -> None:
    """Fail the scan because it did not read the tree: exit 2, where only 1 is a
    finding."""
    raise _InfrastructureError(message)


# One Go source file, read once: `text` as written, and `code` with comments and
# literal contents blanked. Equal length and newlines keep offsets and line
# numbers aligned.
class Source:
    __slots__ = ("rel", "pkgdir", "text", "code", "is_test")

    def __init__(self, rel: str, pkgdir: str, text: str, is_test: bool) -> None:
        self.rel = rel
        self.pkgdir = pkgdir
        self.text = text
        self.code = _mask_code(text)
        self.is_test = is_test


def _mask_code(text: str) -> str:
    """Blank out comments and literal contents, keeping length and lines.

    A presence match reads the code and not the prose around it: a call named
    in a line or block comment, or quoted inside a string or rune literal, is
    not a call site. Every character a comment or a literal covers is replaced
    by a space, except a newline, which is kept so the masked text has the same
    length and the same line breaks as the source and offsets stay aligned.
    Backtick raw strings are covered like the other literals.
    """
    chars = list(text)
    end = len(text)

    def blank(start: int, stop: int) -> None:
        for pos in range(start, stop):
            if chars[pos] != "\n":
                chars[pos] = " "

    index = 0
    while index < end:
        char = text[index]
        if char == "/" and index + 1 < end and text[index + 1] == "/":
            stop = text.find("\n", index)
            stop = end if stop < 0 else stop
            blank(index, stop)
            index = stop
        elif char == "/" and index + 1 < end and text[index + 1] == "*":
            stop = text.find("*/", index + 2)
            stop = end if stop < 0 else stop + 2
            blank(index, stop)
            index = stop
        elif char == '"':
            stop = index + 1
            while stop < end:
                if text[stop] == "\\":
                    stop += 2
                    continue
                if text[stop] == '"':
                    stop += 1
                    break
                stop += 1
            blank(index, stop)
            index = stop
        elif char == "`":
            stop = text.find("`", index + 1)
            stop = end if stop < 0 else stop + 1
            blank(index, stop)
            index = stop
        elif char == "'":
            stop = index + 1
            while stop < end:
                if text[stop] == "\\":
                    stop += 2
                    continue
                if text[stop] == "'":
                    stop += 1
                    break
                stop += 1
            blank(index, stop)
            index = stop
        else:
            index += 1
    return "".join(chars)


def _collect(pkg_root: str, root: str) -> list[Source]:
    """Read every Go file under pkg_root, in a stable order."""
    sources: list[Source] = []
    for directory, subdirs, files in os.walk(pkg_root):
        subdirs[:] = sorted(d for d in subdirs if d not in SKIPPED_DIRS)
        for name in sorted(files):
            if not name.endswith(".go"):
                continue
            path = os.path.join(directory, name)
            if os.path.islink(path):
                continue
            try:
                with open(path, encoding="utf-8") as handle:
                    text = handle.read()
            except (OSError, UnicodeDecodeError):
                continue
            rel = os.path.relpath(path, root)
            pkgdir = os.path.relpath(directory, root)
            sources.append(Source(rel, pkgdir, text, name.endswith("_test.go")))
    return sources


def _pkgdirs(sources: list[Source], include_tests: bool) -> dict[str, list[Source]]:
    """Group sources by package directory."""
    grouped: dict[str, list[Source]] = {}
    for source in sources:
        if source.is_test and not include_tests:
            continue
        grouped.setdefault(source.pkgdir, []).append(source)
    return grouped


def _line_of(text: str, match: re.Match[str]) -> int:
    """The 1-based line an offset falls on."""
    return text.count("\n", 0, match.start()) + 1


def _matches(
    sources: list[Source], pattern: re.Pattern[str], attr: str = "code"
) -> dict[str, list[tuple[str, int]]]:
    """Group every match of a pattern by package, as (file, line) pairs.

    The pattern reads `code` unless asked for "text", which the import-path
    control needs because a path lives in a string.
    """
    found: dict[str, list[tuple[str, int]]] = {}
    for source in sources:
        subject = getattr(source, attr)
        for match in pattern.finditer(subject):
            found.setdefault(source.pkgdir, []).append((source.rel, _line_of(subject, match)))
    return found


def _has_text(sources: list[Source], pattern: re.Pattern[str], attr: str = "code") -> bool:
    return any(pattern.search(getattr(source, attr)) for source in sources)


def _positive_control(
    name: str, derived: dict[str, list[tuple[str, int]]], anchors: tuple[str, ...]
) -> None:
    """Refuse to report over a set that did not come from this tree.

    No call sites at all, or none of the packages a fixed anchor names, is an
    infrastructure error, not a clean run.
    """
    if not derived:
        _infra(f"observability-readiness[{name}]: the scan found no call site, so the check "
               "has nothing to assert and would pass on an empty set")
    if not any(anchor in derived for anchor in anchors):
        _infra(f"observability-readiness[{name}]: the scan found {len(derived)} package(s) "
               f"but none of the anchors {anchors}, so its pattern no longer matches the tree")


def check_logging_dependency(sources: list[Source]) -> list[str]:
    """1. A package that takes log.Logger must declare the requirement."""
    non_test = [s for s in sources if not s.is_test]
    resolves = _matches(non_test, RESOLVES_LOGGER)
    _positive_control("logging-dependency", resolves, LOGGER_ANCHORS)

    findings: list[str] = []
    for pkgdir in sorted(resolves):
        members = [s for s in non_test if s.pkgdir == pkgdir]
        if _has_text(members, DECLARES_LOGGER):
            continue
        rel, line = resolves[pkgdir][0]
        findings.append(
            f"{rel}:{line}: {pkgdir}: resolves log.Logger here but this package's module "
            "descriptor declares no (*log.Logger)(nil) requirement, so the module is ordered by "
            "name alone and may be constructed before the logging module, falling back to the "
            "process default logger"
        )
    return findings


def _item_brace(source: Source, offset: int) -> int:
    """The index of the brace opening the composite literal that holds `offset`.

    Walk back over the masked text to the first brace that opens a composite
    literal; a brace written inside a string or comment is blanked and does
    not count. Reading backward from the flag rather than upward line by line
    also handles a flag written on the same line as its entry, or an entry
    whose key and brace are split across two lines. Returns -1 where no brace
    precedes the flag, which means the flag is in no composite literal at all.
    """
    masked = source.code
    depth = 0
    for index in range(offset - 1, -1, -1):
        char = masked[index]
        if char == "}":
            depth += 1
        elif char == "{":
            if depth == 0:
                return index
            depth -= 1
    return -1


def _item_literal(source: Source, offset: int) -> bool:
    """Whether the `Sensitive: true` flag at `offset` marks a config item.

    An item is declared either as a keyed entry of a schema map (`KEY: { ... }`)
    or as a literal of the item type (`config.Item{ ... }`), so the flag marks
    an item only where the composite literal it sits in is written in one of
    those two shapes -- see ITEM_KEY_ENTRY and ITEM_TYPED_LITERAL. Anywhere
    else the flag is a field of some other struct: a view that reports whether
    a value is sensitive, or masks one, writes `Sensitive: true` without
    declaring an item, and a package whose only such flag is one of those
    declares no schema and owes the redaction registry nothing.

    The key probe reads the file as written, because a quoted key is a string
    literal whose contents the masking blanks; the type probe reads the masked
    code, because a type name is code and never prose.
    """
    open_index = _item_brace(source, offset)
    if open_index < 0:
        return False
    if ITEM_KEY_ENTRY.search(source.text[:open_index]) is not None:
        return True
    return ITEM_TYPED_LITERAL.search(source.code[:open_index]) is not None


def _item_key(source: Source, offset: int, consts: dict[str, str]) -> str:
    """The config key a `Sensitive: true` flag at `offset` belongs to.

    Read the key written just before the brace that opens the item, from the
    original text so a quoted key keeps its spelling. The key resolves through
    the package's string constants, so the key compares as its operator-facing
    value and matches the allow-list entries, which are written the
    operator-facing way, rather than as the Go identifier. An item that carries
    no key before its brace -- one added as `items[key] = config.Item{...}`,
    whose key sits in the index assignment -- yields the empty string, which
    the report renders as an unkeyed item.
    """
    open_index = _item_brace(source, offset)
    if open_index < 0:
        return ""
    entry = ITEM_KEY_ENTRY.search(source.text[:open_index])
    if entry is None:
        return ""
    raw = entry.group(1).strip('"')
    return consts.get(raw, raw)


def _call_arguments(masked: str, open_index: int) -> list[str]:
    """The top-level, comma-separated arguments of a call whose `(` is open_index.

    A string argument has been blanked by the masking, so an argument may come
    back empty (a blanked label); the empties are kept because a value's
    position is what the caller counts, and only a trailing empty left by a
    trailing comma is dropped.
    """
    depth = 0
    args: list[str] = []
    current: list[str] = []
    for index in range(open_index, len(masked)):
        char = masked[index]
        if char in "([{":
            depth += 1
            if depth == 1:
                continue
            current.append(char)
            continue
        if char in ")]}":
            depth -= 1
            if depth == 0:
                args.append("".join(current).strip())
                while args and args[-1] == "":
                    args.pop()
                return args
            current.append(char)
            continue
        if depth == 1 and char == ",":
            args.append("".join(current).strip())
            current = []
            continue
        if depth >= 1:
            current.append(char)
    args.append("".join(current).strip())
    while args and args[-1] == "":
        args.pop()
    return args


def _declares_string_func(members: list[Source], name: str) -> bool:
    """Whether the package declares `func <name>(...) []string`."""
    pattern = re.compile(_DECLARES_STRING_FUNC.format(name=re.escape(name)))
    return any(pattern.search(source.code) for source in members)


def _secrets_handoff(source: Source, members: list[Source]) -> bool:
    """Whether a source hands its secret values to the factory's Secrets hook.

    The hook is the `[]string`-returning shape above; a field merely named
    Secrets hands nothing over.
    """
    code = source.code
    if SECRETS_FUNC_FIELD.search(code) or SECRETS_FUNC_VALUE.search(code):
        return True
    for ref in SECRETS_REF.finditer(code):
        if _declares_string_func(members, ref.group(1)):
            return True
    return False


def _registration_evidence(members: list[Source]) -> tuple[bool, int]:
    """What a package hands to registration: `(whole_set, explicit)`, where
    `whole_set` covers every item via a Secrets hook or variadic registration and
    `explicit` counts values in this package's `ModuleName` registrations."""
    whole = False
    explicit = 0
    for source in members:
        if _secrets_handoff(source, members):
            whole = True
        for match in LITERAL_REGISTER.finditer(source.code):
            args = _call_arguments(source.code, match.end() - 1)
            if len(args) < 3:
                continue
            if not MODULE_NAME.search(args[1]):
                # Not this package's credential registration: its label is not
                # the module-prefixed name this tree writes, so it cannot be
                # tied to an accountable item.
                continue
            values = args[2:]
            if any(value.endswith("...") for value in values):
                whole = True
            else:
                explicit += len(values)
    return whole, explicit


def _package_consts(sources: list[Source]) -> dict[str, str]:
    consts: dict[str, str] = {}
    for source in sources:
        for match in STRING_CONST.finditer(source.text):
            consts[match.group(1)] = match.group(2)
    return consts


def check_sensitive_redaction(sources: list[Source]) -> list[str]:
    """2. A Sensitive item whose value is the secret must be redacted.

    A package must hand over at least as many values as it declares Sensitive
    items, and items past the count are reported; only the two item shapes count."""
    grouped = _pkgdirs(sources, include_tests=False)
    items: dict[str, list[tuple[str, int, str]]] = {}
    for source in sources:
        if source.is_test:
            continue
        consts = _package_consts(grouped.get(source.pkgdir, []))
        for match in SENSITIVE_ITEM.finditer(source.code):
            if not _item_literal(source, match.start()):
                continue
            key = _item_key(source, match.start(), consts)
            items.setdefault(source.pkgdir, []).append(
                (source.rel, _line_of(source.code, match), key)
            )

    _positive_control(
        "sensitive-redaction",
        {pkgdir: [(rel, line) for rel, line, _ in sites] for pkgdir, sites in items.items()},
        SENSITIVE_ANCHORS,
    )

    findings: list[str] = []
    for pkgdir in sorted(items):
        # The items whose value is a path and not the secret are named in the
        # allow-list and need no rule.
        accountable = [entry for entry in items[pkgdir] if (pkgdir, entry[2]) not in PATH_ITEMS]
        if not accountable:
            continue
        members = grouped.get(pkgdir, [])
        whole, explicit = _registration_evidence(members)
        if not whole and explicit < len(accountable) and _is_shared_config_unit(members):
            # A shared unit holds no module of its own; the package that mounts
            # its Items() registers the values, so borrow that package's
            # evidence.
            borrowed = _registrar_registration(pkgdir, grouped)
            if borrowed is not None:
                whole = whole or borrowed[0]
                explicit = max(explicit, borrowed[1])
        covered = len(accountable) if whole else explicit
        for rel, line, key in accountable[covered:]:
            where = f"config item {key!r}" if key else "a config item whose key cannot be read"
            findings.append(
                f"{rel}:{line}: {pkgdir}: {where} is marked Sensitive and its value is the "
                f"secret, but the package hands over only {covered} registered value(s) for its "
                f"{len(accountable)} such item(s) -- neither a direct literal.Register nor a "
                "Secrets hook covers it"
            )
    return findings


def _is_shared_config_unit(members: list[Source]) -> bool:
    """A package that provides config items and declares no module."""
    return _has_text(members, PROVIDES_ITEMS) and not _has_text(members, DECLARES_MODULE)


def _registrar_registration(
    pkgdir: str,
    grouped: dict[str, list[Source]],
) -> tuple[bool, int] | None:
    """The registration evidence of the package that mounts a shared unit, as
    `(whole_set, explicit)`, or None when no package mounts it with one."""
    unit = os.path.basename(pkgdir)
    reference = re.compile(r"\b" + re.escape(unit) + r"\.Items\(")
    for other, members in grouped.items():
        if other == pkgdir:
            continue
        if not _has_text(members, reference):
            continue
        whole, explicit = _registration_evidence(members)
        if whole or explicit:
            return whole, explicit
    return None


def check_egress_transport(sources: list[Source]) -> list[str]:
    """3. A package that opens an egress client must wrap its transport."""
    non_test = [s for s in sources if not s.is_test]
    opened = _matches(non_test, EGRESS_NEW)
    _positive_control("egress-transport", opened, EGRESS_ANCHORS)

    if EGRESS_EXEMPT not in opened:
        _infra(f"observability-readiness[egress-transport]: the allow-listed package "
               f"{EGRESS_EXEMPT} was not seen calling egress.New, so the exemption no longer "
               "matches the tree and may be hiding a violation")

    findings: list[str] = []
    for pkgdir in sorted(opened):
        if pkgdir == EGRESS_EXEMPT:
            continue
        members = [s for s in non_test if s.pkgdir == pkgdir]
        if _has_text(members, EGRESS_TRANSPORT):
            continue
        rel, line = opened[pkgdir][0]
        findings.append(
            f"{rel}:{line}: {pkgdir}: calls egress.New but never wraps the client with "
            "observability.Transport, so this hop drops the trace; the design states the caller "
            "wraps the transport from outside"
        )
    return findings


def check_no_global_provider(sources: list[Source]) -> list[str]:
    """4. pkg/ must not call a package-level entry of the upstream otel global.

    The entries are `otel.Tracer(`, `otel.Meter(` and the provider
    accessors. Test files are out of scope.
    """
    non_test = [s for s in sources if not s.is_test]
    # The control is the namespace being live: a global entry would be that
    # call, not a reference to an uncalled package. The import path lives in a
    # string, so this reads the file as written.
    if not _has_text(non_test, UPSTREAM_OTEL, attr="text"):
        _infra("observability-readiness[no-global-provider]: no file under pkg/ imports the "
               "upstream otel library, so the absence of a global entry is vacuous")

    findings: list[str] = []
    for source in non_test:
        for match in GLOBAL_OTEL.finditer(source.code):
            line = _line_of(source.code, match)
            findings.append(
                f"{source.rel}:{line}: {source.pkgdir}: calls {match.group(0)[:-1]} -- the "
                "package-level entry of the upstream global; this module installs no global "
                "provider, so it resolves to the upstream no-op and the signal reaches nothing"
            )
    return findings


CHECKS = (
    ("logging-dependency", check_logging_dependency),
    ("sensitive-redaction", check_sensitive_redaction),
    ("egress-transport", check_egress_transport),
    ("no-global-provider", check_no_global_provider),
)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--root",
        default=os.curdir,
        help="the tree to scan (default: the current directory); paths in the report are "
        "relative to it",
    )
    args = parser.parse_args(argv)

    root = os.path.abspath(args.root)
    try:
        findings = _run(root, args.root)
    except _InfrastructureError as exc:
        print(str(exc), file=sys.stderr)
        return 2

    if findings:
        for finding in findings:
            print(finding)
        print(
            f"observability-readiness: {len(findings)} finding(s); each names the file, the "
            "line and the companion that is missing",
            file=sys.stderr,
        )
        return 1
    return 0


def _run(root: str, given: str) -> list[str]:
    """Collect the findings over the tree, or raise on an empty subject."""
    if not os.path.isdir(root):
        _infra(f"observability-readiness: {given} is not a directory")

    pkg_root = os.path.join(root, "pkg")
    if not os.path.isdir(pkg_root):
        _infra(f"observability-readiness: {os.path.relpath(pkg_root, root)} is missing")

    sources = _collect(pkg_root, root)
    if not sources:
        _infra(f"observability-readiness: no Go file under {os.path.relpath(pkg_root, root)}, "
               "so the scan read nothing")

    findings: list[str] = []
    for _, check in CHECKS:
        findings.extend(check(sources))
    return findings


if __name__ == "__main__":
    sys.exit(main())
