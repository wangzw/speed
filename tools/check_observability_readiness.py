#!/usr/bin/env python3
"""Observability-readiness gate over pkg/.

A round of work landed three observability obligations in pkg/ and guarded
each one with a Go test -- the logging dependency a module that resolves a
logger must declare, the redaction rule a module that holds a secret config
value must register, and the transport wrapper a module that opens an egress
client must apply. Every one of those tests carries a hand-written list of
the modules it covers, and design-log.md states the price of that shape: a
module that both omits the companion and is absent from the list passes
silently. No descriptor says a module logs, registers a rule or wraps a
client -- that lives inside its callbacks -- so the list cannot be derived by
the tests that read descriptors.

This gate is the backstop that admission calls for. It does not restate the
three assertions; it derives from the source which call sites those
assertions are about, so the scan is the source of truth and no list has to
be kept in step. A fourth check is the reverse assertion for the upstream
global provider a module must not install.

The four checks, each anchored on a pattern that occurs in the tree:

  1. Dependency. A package that takes log.Logger out of the registry must
     declare it. Resolution is written `core.Resolve[log.Logger](reg)` or
     through the package-local `optional[log.Logger](reg, ...)`; the
     declaration is the token `(*log.Logger)(nil)` in the module descriptor
     of the same package. Without it the module is ordered by name alone,
     may be constructed ahead of the logging module, and falls back to the
     process default logger.

  2. Redaction. A `config.Item{Sensitive: true}` whose value is the secret
     itself must have that value registered as a logging redaction rule. A
     package registers one either directly (`literal.Register(...)`) or by
     handing the value to the `Secrets` hook of a Spec (`Secrets func(cfg)
     []string`, `Secrets: func(cfg) []string {...}`, `Secrets: <ref>`), which
     the factory turns into a `literal.Register`; a shared config unit -- an
     `Items()` provider with no module -- hands the value on through the
     `Secrets` hook of the module that mounts it. The check is per item, not
     per package: the count of registered values must be at least the count of
     Sensitive items, since a static text cannot be tied to one item (the
     label is a module-prefixed credential name, not the item key). A
     `Secrets` hook or a variadic value list covers every item; a fixed value
     list covers its own number; a package that declares two Sensitive items
     and registers one value is a finding. A value is attributed to the items
     only when the registration that hands it over carries a module-prefixed
     label, and `Secrets` is read in its `[]string`-returning shapes rather
     than as any field merely named `Secrets`. That narrows the ways a
     registration can be missed; it does not close them -- see "What this gate
     is not" below for what a text scan cannot decide.
     The two items whose value is a filesystem path and not the secret
     (`sp-key-file` in authn.sso.saml, `signing-key-file` in authn.token)
     register nothing and are named by package and key in an allow-list;
     nothing else is exempt by the shape of its key.

  3. Egress. A package that calls `egress.New(` must wrap the client it gets
     back with `observability.Transport`. design-authn-sso.md states the
     caller wraps the client's Transport from outside (the egress package
     provides no wrapper of its own). The one exception is
     `pkg/authn/login/internal/wechat`: the same document states its URL
     carries the secret, so it does not wrap, and the package's docstring
     repeats it. The exception is a named allow-list, not a scan of the
     expression.

  4. No global provider. pkg/ must call none of the package-level entries of
     the upstream global: `otel.Tracer(`, `otel.Meter(` and the accessors
     that install or read the process-wide provider
     (`otel.SetTracerProvider(`, `otel.GetTracerProvider(`,
     `otel.SetMeterProvider(`, `otel.GetMeterProvider(`, `otel.Handle(`).
     adr-observability-provider-2026-09-22 records that this module installs
     no global provider, so a signal written through the global resolves to
     the upstream no-op and reaches no destination. The check reads non-test
     files, as the others do.

The presence patterns read the code with comments and the contents of string
and rune literals masked out, so a call named only in a comment or quoted in
a string is neither a registration nor a call site; the one constant that
legitimately lives inside a string -- an import path -- is read against the
raw text instead.

Every check carries a positive control. Before it reports the absence of a
companion it asserts that it actually scanned the call sites -- that the
derived set is non-empty and holds a package it knows resolves, declares,
marks sensitive or opens a client. A scan that drew an empty set has checked
nothing, and this gate exits 2 rather than pass on it, the same shape the
descriptor-level Go tests use.

What this gate is not: a type analysis. Every pattern here is a text scan, and
the redaction check in particular decides heuristically, from the shape a
package writes its registration in, whether it has given an item a rule. A
deliberately constructed spelling can defeat the shape -- a registration whose
label is dressed up to carry the module name, or a hook written to look like
the `[]string` one -- and pass a package that registers nothing. So a clean
run here is a backstop, not a guarantee: it catches a module that stayed off
the hand-written lists the descriptor-level tests carry, and it errs toward
reporting where it cannot tie a registration to an item, but the type-level
guarantee lives in those Go tests (`pkg/internal/logdeps` and the
`secrecy_test.go` files beside the packages), which a text scan cannot
replace. A reader must not take the absence of a finding as proof that every
obligation is met.

Exit codes: 0 = clean; 1 = at least one finding, each reported with file,
line and what is missing; 2 = infrastructure error -- the root is not a
directory, pkg/ is missing, no Go file was read, or a positive control saw
an empty set. Paths in the report are relative to --root.
"""

from __future__ import annotations

import argparse
import os
import re
import sys

# Directories whose contents are not this repository's own Go source.
SKIPPED_DIRS = {".git", "node_modules", "testdata", "vendor"}

# -- check 1 -----------------------------------------------------------------

# A resolution of the logging capability. Both spellings in the tree share the
# shape: a helper name, the capability, and a call. `optional[log.Logger]` is
# the package-local take-or-absent helper; `core.Resolve[log.Logger]` is the
# registry query. Matching the element type rather than the helper name keeps
# a further helper, added later, under the same rule.
RESOLVES_LOGGER = re.compile(r"\b\w+\[log\.Logger\]\s*\(")

# The declaration, written as the requirement token. A module descriptor
# carries it as `{Token: (*log.Logger)(nil), Optional: true}`.
DECLARES_LOGGER = re.compile(r"\(\*log\.Logger\)\(nil\)")

# Packages known to resolve and declare the logging capability. The positive
# control requires at least one, so a listing that lost every one of them came
# from a scan that did not read the tree and the checks below would pass on an
# empty set.
LOGGER_ANCHORS = ("pkg/settings", "pkg/db", "pkg/http", "pkg/authn/login")

# -- check 2 -----------------------------------------------------------------

# One sensitive item of the configuration schema.
SENSITIVE_ITEM = re.compile(r"\bSensitive:\s*true\b")

# A string constant, which is how a schema names its item keys. The single
# line form and the entry of a `const (...)` block are both matched.
STRING_CONST = re.compile(r'^\s*(?:const\s+)?([A-Za-z_]\w*)\s*=\s*"([^"]*)"', re.MULTILINE)

# A `literal.Register(` call. Its argument list is read to count the values
# the package hands over: the redaction rule, a label, then the values.
LITERAL_REGISTER = re.compile(r"\bliteral\.Register\(")

# The Secrets hook that actually hands secret values to the factory, in the
# shape the tree writes it: the Spec field typed `func(cfg *C) []string`, the
# func literal that fills it (`Secrets: func(cfg *C) []string { ... }`), or an
# identifier naming such a function (`Secrets: secrets`, where
# `func secrets(cfg *C) []string` is declared in the package). What is matched
# is the `[]string`-returning function type the value has to carry, not the
# bare name: a field merely *named* Secrets -- a `settings{Secrets: true}`
# decoy -- hands no value over and is not a hook.
SECRETS_FUNC_FIELD = re.compile(r"\bSecrets\s+func\b[^\n]*\[\]string")
SECRETS_FUNC_VALUE = re.compile(r"\bSecrets\s*:\s*func\b[^\n]*\[\]string")
SECRETS_REF = re.compile(r"\bSecrets\s*:\s*([A-Za-z_]\w*)\b")

# A function declared in the package that returns a `[]string`, which is what
# a `Secrets:` identifier reference must name to be a handoff.
_DECLARES_STRING_FUNC = r"\bfunc\s+{name}\s*\([^)]*\)\s*\[\]string"

# The token a redaction registration's label carries: per the module docstring
# the label is a module-prefixed credential name, written `ModuleName+".x"` (or
# `<recv>.ModuleName+".x"`). A registration that does not carry it is not this
# package's own credential registration and cannot be tied to its items.
MODULE_NAME = re.compile(r"\bModuleName\b")

# An `Items()` config provider, and a module descriptor. A package that
# provides items and declares no module is a shared config unit.
PROVIDES_ITEMS = re.compile(r"\bfunc\s+Items\(")
DECLARES_MODULE = re.compile(r"\bfunc\s+Module\(|\bNewModule\(")

# The sensitive items whose value is a filesystem path and not the secret
# itself, named by the package directory and the item's operator-facing key.
# This is a named allow-list, not a scan of the key's shape: a key that ends
# in `-file` is not exempt on its own, because a later item carrying such a
# key could still hold the secret.
PATH_ITEMS = {
    ("pkg/authn/sso/saml", "sp-key-file"),
    ("pkg/authn/token", "signing-key-file"),
}

SENSITIVE_ANCHORS = ("pkg/db", "pkg/event/nats", "pkg/authn/login/github")

# -- check 3 -----------------------------------------------------------------

EGRESS_NEW = re.compile(r"\begress\.New\s*\(")
EGRESS_TRANSPORT = re.compile(r"\bobservability\.Transport\(")

# The package that opens an egress client and does not wrap it, with its
# source. design-authn-sso.md: "the caller wraps the client's Transport with
# observability.Transport from outside; the WeChat client does not wrap, its
# URL carries the secret". Its query string carries the application secret, so
# the client is left unwrapped on purpose.
EGRESS_EXEMPT = "pkg/authn/login/internal/wechat"

EGRESS_ANCHORS = ("pkg/authn/login/github", "pkg/authn/sso/oidc")

# -- check 4 -----------------------------------------------------------------

# The package-level entries of the upstream global provider: the constructors
# and the accessors that install or read the process-wide provider. The
# installing entries are the ones the ADR forbids; the reading ones are named
# with them because a signal written through the global reaches the same
# no-op. A provider taken off the registry is a different call on a value, not
# on the package, and is not matched.
GLOBAL_OTEL = re.compile(
    r"\botel\.(?:Tracer|Meter|SetTracerProvider|GetTracerProvider|"
    r"SetMeterProvider|GetMeterProvider|Handle)\s*\("
)

# Any import of the upstream telemetry library. The positive control requires
# one, so the absence of `otel.Tracer`/`otel.Meter` is the absence of a call
# in a tree that uses otel at all, not the absence of otel.
UPSTREAM_OTEL = re.compile(r'"go\.opentelemetry\.io/otel')


class _InfrastructureError(Exception):
    """Raised where the scan cannot run or drew an empty set.

    Carried as an exception so a check deep in the call chain can report one
    without a return code threaded through every helper; main catches it and
    turns it into exit 2.
    """


def _infra(message: str) -> None:
    """Fail the scan because it did not read the tree it is about.

    The exit-code contract (module docstring): 2 = the scan could not run or
    drew an empty set, only 1 = a finding. The message names what is missing.
    """
    raise _InfrastructureError(message)


# One Go source file, read once. `text` is the file as written; `code` is the
# same text with comments and the contents of string and rune literals blanked
# out, so a call named in a comment or quoted in a string is not read as a
# call site. The two strings have equal length and the same newlines, so a
# match offset and a line number mean the same thing in either.
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

    The pattern reads `code` by default, so a call named in a comment or a
    string is not matched; an attribute of "text" reads the file as written,
    which the import-path control needs because an import path lives in a
    string.
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

    A scan that found no call site has nothing to check and would pass on an
    empty set. One that found call sites but none of the packages a fixed
    anchor names came from a pattern that no longer matches the tree. Either
    is an infrastructure error, not a clean run.
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


def _item_key(source: Source, offset: int, consts: dict[str, str]) -> str:
    """The config key a `Sensitive: true` flag at `offset` belongs to.

    The flag sits inside the item's brace, `KEY: { ... }`. Walk back over the
    masked text to the brace that opens the item -- a brace written inside a
    string or comment is blanked and does not count -- then read the key
    written just before that brace, from the original text so a quoted key
    keeps its spelling. The key resolves through the package's string
    constants, so the key compares as its operator-facing value and matches
    the allow-list entries, which are written the operator-facing way, rather
    than as the Go identifier. Reading backward from the flag rather than
    upward line by line also handles a flag written on the same line as its
    entry, or an entry whose key and brace are split across two lines. An item
    that has no key at all -- a positional struct value -- yields the empty
    string, which the report renders as an unkeyed item.
    """
    masked = source.code
    depth = 0
    open_index = -1
    for index in range(offset - 1, -1, -1):
        char = masked[index]
        if char == "}":
            depth += 1
        elif char == "{":
            if depth == 0:
                open_index = index
                break
            depth -= 1
    if open_index < 0:
        return ""
    entry = re.search(r'("[^"]+"|[A-Za-z_]\w*)\s*:\s*\Z', source.text[:open_index])
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

    The hook is the shape the tree writes: the Spec field typed
    `func(cfg *C) []string`, the func literal that fills it, or an identifier
    naming such a function. A field merely *named* Secrets -- a
    `settings{Secrets: true}` decoy -- hands no value over and is not read as
    one.
    """
    code = source.code
    if SECRETS_FUNC_FIELD.search(code) or SECRETS_FUNC_VALUE.search(code):
        return True
    for ref in SECRETS_REF.finditer(code):
        if _declares_string_func(members, ref.group(1)):
            return True
    return False


def _registration_evidence(members: list[Source]) -> tuple[bool, int]:
    """What a package hands to redaction registration.

    Returns `(whole_set, explicit)`. `whole_set` is true when the package names
    its secret values in a Secrets hook, or registers a variadic value list
    (`values...`): both hand the whole set over and cover every item the
    package declares. `explicit` counts the fixed value arguments of the
    `literal.Register(rule, label, value, ...)` calls that are this package's
    own credential registrations.

    A registration is attributed to the items -- not merely summed -- by
    requiring its label to be the module-prefixed credential name the module
    docstring specifies: a label carrying `ModuleName`. An unrelated call in
    the same package, a `literal.Register(rule, label, x, y)` whose label is
    some other expression, registers some other credential and is not counted,
    so it cannot inflate the value count and mask an item that has no rule. A
    package with no such registration has registered nothing.
    """
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

    The check is per item, not per package. A static text cannot be tied to
    one item -- `literal.Register` takes the redaction rule, a module-prefixed
    credential label and a variadic value list, none of which spells the item
    key -- so the count is what is checked: a package must hand over at least
    as many values as it declares Sensitive items, a `Secrets` hook or a
    variadic registration covering every item and a fixed value list covering
    its own number. When the count falls short, the items past it are
    reported, because a value short of the count leaves an item no rule can be
    shown to cover -- the per-item gap the package-level reading missed. The
    trade-off is that the count is exact even where the identity of the
    covered item is not, so the reported items are the excess by declaration
    order and not a proven miss.
    """
    grouped = _pkgdirs(sources, include_tests=False)
    items: dict[str, list[tuple[str, int, str]]] = {}
    for source in sources:
        if source.is_test:
            continue
        consts = _package_consts(grouped.get(source.pkgdir, []))
        for match in SENSITIVE_ITEM.finditer(source.code):
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
    """The registration evidence of the package that mounts a shared unit.

    A shared unit holds no module of its own; the package that mounts it via
    `<unit>.Items()` is the one that registers its values. Returns that
    package's `(whole_set, explicit)` evidence, or None when no package mounts
    the unit with any registration of its own.
    """
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
    """4. pkg/ must not call a package-level entry of the upstream global.

    The entries are `otel.Tracer(`, `otel.Meter(` and the accessors that
    install or read the process-wide provider (`SetTracerProvider`,
    `GetTracerProvider`, `SetMeterProvider`, `GetMeterProvider`, `Handle`).
    Test files are out of scope, the same as the other checks: an assertion
    may name a call that production code must not make.
    """
    non_test = [s for s in sources if not s.is_test]
    # The control is the namespace being live: the tree reaches the upstream
    # library, so a global entry would be that call and not a reference to a
    # package that is never in scope. The import path lives in a string, so
    # this reads the file as written and not the masked code.
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
