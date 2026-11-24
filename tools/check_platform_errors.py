#!/usr/bin/env python3
"""Platform error-copy gate: the client catalog mirrors the backend's codes."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys

# The two modules `go.work` lists, relative to the repository root. A module
# added there but not here would have its codes reported as extras.
BACKEND_ROOTS = ("pkg", "starters")

# The platform error-copy catalog: one directory, one JSON file per language.
LOCALE_DIR = os.path.join(
    "web", "packages", "i18n", "src", "platform-errors", "locales"
)
LANGUAGES = ("zh-CN", "en-US")

# The bundle section the resolver navigates; the gate reads the keys under it.
SECTION = "errors"

# A code part: lowercase, digits, dashes and underscores. The client
# normalizes a dash to an underscore, so both spellings name one key.
CODE_RE = re.compile(r"^[a-z][a-z0-9_-]*$")

# A whole URN string literal. The closing quote has to follow the code
# directly, so a sentence that merely mentions a URN is not a hit.
URN_LITERAL_RE = re.compile(r'"urn:speed:([a-z0-9_-]+):([a-z0-9_-]+)"')

# `name = "value"`, the one-line value form of a constant declaration.
CONST_RE = re.compile(
    r'^\s*(?:const\s+)?([A-Za-z_]\w*)(?:\s+string)?\s*=\s*"([^"\\]*)"\s*(?://.*)?$'
)

# One import inside an import block: an optional alias, then the path.
IMPORT_RE = re.compile(r'^\s*(?:([A-Za-z_]\w*)\s+)?"([^"]+)"\s*$')

# The repository's own import prefix: only these resolve to a package
# directory this gate can read constants from.
MODULE_PREFIX = "github.com/vislake/speed/"

# `base + rhs` where both sides name a value.
CONCAT_IDENT_RE = re.compile(r"\b([A-Za-z_][\w.]*)\s*\+\s*([A-Za-z_][\w.]*)\b")
# `base + "literal"`.
CONCAT_LITERAL_RE = re.compile(r'\b([A-Za-z_][\w.]*)\s*\+\s*"([^"\\]*)"')
# A literal prefix plus a constant: `"urn:speed:<module>:" + mount.CodeX`.
LITERAL_PREFIX_CONCAT_RE = re.compile(
    r'"urn:speed:([a-z0-9_-]+):"\s*\+\s*([A-Za-z_][\w.]*)'
)
# A function declaration; the receiver group of a method is skipped.
FUNC_RE = re.compile(r"^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*\(")
# A call with a single-line argument list.
CALL_RE = re.compile(r"\b([A-Za-z_]\w*)\s*\(([^()]*)\)")

# The directories that are not this repository's Go source at any depth.
SKIPPED_DIRS = frozenset({"node_modules", ".git", ".claude"})

# The directories that are not frontend source or tests, for the reference
# scan behind the kept-entry reasons. The reasons name files; this set is
# what the scan walks.
FRONTEND_SKIPPED_DIRS = frozenset(
    {"node_modules", "dist", "build", "coverage", ".git", "site", ".cache"}
)

# Codes the backend does not write but that stay, each naming the files that
# refer to it. The gate re-scans those references, so a stale reason is a
# finding and the list cannot rot.
KEPT = {
    "admin.impersonation_reason_required": (
        "referenced by web/packages/product-shell/src/bootstrap.test.tsx as a "
        "fallback-namespace example; no writer in this tree"
    ),
    "authn.internal_error": (
        "referenced by web/packages/auth-ui/src/SignOutButton.test.tsx; no "
        "writer in this tree"
    ),
    "authn.invalid_credentials": (
        "referenced by web/packages/i18n/src/platform-errors.test.ts and "
        "web/packages/ui-kit/src/components/InlineError.test.tsx; no writer in "
        "this tree"
    ),
    "authn.invalid_email": (
        "referenced by web/packages/auth-ui/src/internal/error-text.test.ts; "
        "no writer in this tree"
    ),
    "authn.invalid_locale": (
        "referenced by web/packages/i18n/src/create.ts and "
        "web/packages/i18n/src/supported-languages-alignment.test.ts; no "
        "writer in this tree"
    ),
    "authn.password_too_short": (
        "referenced by web/packages/api-client/src/client.test.ts; no writer "
        "in this tree"
    ),
    "authn.refresh_token_invalid": (
        "one of @speed/i18n's SESSION_LIFECYCLE_ERROR_CODES "
        "(web/packages/i18n/src/error-text.ts); no writer in this tree"
    ),
    "authn.refresh_token_reused": (
        "one of @speed/i18n's SESSION_LIFECYCLE_ERROR_CODES "
        "(web/packages/i18n/src/error-text.ts); no writer in this tree"
    ),
    "authn.session_not_found": (
        "one of @speed/i18n's SESSION_LIFECYCLE_ERROR_CODES "
        "(web/packages/i18n/src/error-text.ts); no writer in this tree"
    ),
    "authn.session_revoked": (
        "one of @speed/i18n's SESSION_LIFECYCLE_ERROR_CODES "
        "(web/packages/i18n/src/error-text.ts); no writer in this tree"
    ),
    "authn.token_expired": (
        "one of @speed/i18n's SESSION_LIFECYCLE_ERROR_CODES "
        "(web/packages/i18n/src/error-text.ts); no writer in this tree"
    ),
    "authn.token_invalid": (
        "referenced by web/packages/product-shell/src/bootstrap.test.tsx; no "
        "writer in this tree"
    ),
    "billing.insufficient_credits": (
        "referenced by web/packages/api-client ('billing.insufficient_credits' "
        "is its module-code example) and its tests; no writer in this tree"
    ),
    "billing.invalid_amount": (
        "referenced by web/packages/api-client/src/client.test.ts; no writer "
        "in this tree"
    ),
    "billing.invoice_not_found": (
        "referenced by web/packages/i18n/README.md; no writer in this tree"
    ),
    "billing.plan_not_found": (
        "referenced by web/packages/api-client/src/errors.test.ts; no writer "
        "in this tree"
    ),
    "notes.text_required": (
        "referenced by web/packages/api-client's tests as their sample code; "
        "no writer in this tree"
    ),
    "rbac.permission_denied": (
        "referenced by web/packages/product-shell/src/bootstrap.tsx and "
        "starters/demo/web/src/impersonation.test.tsx; no writer in this tree"
    ),
}

# Duplicate keys a language file may carry, by dotted path, with the reason
# the lookup table still means one entry. Empty: an entry has to be argued for.
DUPLICATES: dict[str, str] = {}


class Source:
    """One Go file, with the reads the derivation makes of it."""

    def __init__(self, path: str):
        self.path = path
        self.dir = os.path.dirname(path)
        try:
            self.text = _read(path)
        except OSError as exc:
            _infra(f"error: {path} could not be read: {exc}")

    def consts(self) -> dict[str, str]:
        """The plain string constants declared on one line each."""
        found: dict[str, str] = {}
        for line in self.text.splitlines():
            match = CONST_RE.match(line)
            if match:
                found[match.group(1)] = match.group(2)
        return found

    def imports(self, root: str) -> dict[str, str]:
        """The repository packages this file imports, alias -> directory."""
        found: dict[str, str] = {}
        in_block = False
        for line in self.text.splitlines():
            stripped = line.strip()
            if stripped.startswith("import ("):
                in_block = True
                continue
            if in_block and stripped == ")":
                in_block = False
                continue
            if stripped.startswith("import "):
                candidate = stripped[len("import "):]
            elif in_block:
                candidate = stripped
            else:
                continue
            match = IMPORT_RE.match(candidate)
            if not match:
                continue
            alias, path = match.group(1), match.group(2)
            if not path.startswith(MODULE_PREFIX):
                continue
            relative = path[len(MODULE_PREFIX):]
            directory = os.path.join(root, relative)
            if not os.path.isdir(directory):
                continue
            found.setdefault(alias or os.path.basename(directory), directory)
        return found


def _read(path: str) -> str:
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        return fh.read()


def _infra(message: str) -> None:
    """Report an infrastructure failure and exit 2."""
    print(message, file=sys.stderr)
    raise SystemExit(2)


def go_files(root: str) -> list[str]:
    """Every non-test Go file under the backend roots, sorted.

    A root the workspace names but that is not a directory is an
    infrastructure error, not an empty module.
    """
    found: list[str] = []
    for name in BACKEND_ROOTS:
        base = os.path.join(root, name)
        if not os.path.isdir(base):
            _infra(f"error: the backend root {name}/ is missing, so the backend's "
                   "writable code set has no subject")
        for current, dirs, files in os.walk(base):
            dirs[:] = [d for d in dirs if d not in SKIPPED_DIRS]
            for filename in files:
                if filename.endswith(".go") and not filename.endswith("_test.go"):
                    found.append(os.path.join(current, filename))
    return sorted(found)


def _prefix_value(value: str) -> str | None:
    """The module of a `urn:speed:<module>:` prefix, or None."""
    match = re.fullmatch(r"urn:speed:([a-z0-9_-]+):", value)
    return None if match is None else match.group(1)


def derive(root: str) -> tuple[set[str], list[str], list[str]]:
    """Return the writable keys, unresolved helper reports, and notes: keys are
    `<module>.<code>` with dashes as underscores. A helper with no readable
    candidate is an infrastructure error in main."""
    paths = go_files(root)
    if not paths:
        _infra(f"error: no Go file under {', '.join(BACKEND_ROOTS)}/, so the backend's "
               "writable code set has no subject")

    sources = [Source(path) for path in paths]
    consts_by_dir: dict[str, dict[str, str]] = {}
    imports_by_file: dict[str, dict[str, str]] = {}
    for source in sources:
        consts_by_dir.setdefault(source.dir, {}).update(source.consts())
        imports_by_file[source.path] = source.imports(root)

    # The prefix each package declares, when it declares one.
    prefix_of_dir: dict[str, tuple[str, str]] = {}
    for source in sources:
        for name, value in consts_by_dir[source.dir].items():
            module = _prefix_value(value)
            if module is not None:
                prefix_of_dir[source.dir] = (name, module)

    derived: set[str] = set()
    unresolved: list[tuple[str, int, str, str]] = []

    def add(module: str, code: str) -> None:
        if CODE_RE.match(code):
            derived.add(f"{module}.{code.replace('-', '_')}")

    def resolve(identifier: str, source: Source) -> str | None:
        """A constant's value, from this package or an imported one."""
        if "." in identifier:
            alias, name = identifier.split(".", 1)
            directory = imports_by_file.get(source.path, {}).get(alias)
            if directory is None:
                return None
            return consts_by_dir.get(directory, {}).get(name)
        return consts_by_dir.get(source.dir, {}).get(identifier)

    for source in sources:
        for match in URN_LITERAL_RE.finditer(source.text):
            add(match.group(1), match.group(2))

        for match in CONCAT_LITERAL_RE.finditer(source.text):
            module = _prefix_value(resolve(match.group(1), source) or "")
            if module is not None:
                add(module, match.group(2))

        for match in LITERAL_PREFIX_CONCAT_RE.finditer(source.text):
            value = resolve(match.group(2), source)
            if value is not None:
                add(match.group(1), value)

        for match in CONCAT_IDENT_RE.finditer(source.text):
            base, rhs = match.group(1), match.group(2)
            module = _prefix_value(resolve(base, source) or "")
            if module is None:
                continue
            value = resolve(rhs, source)
            if value is None:
                line = source.text.count("\n", 0, match.start()) + 1
                unresolved.append((source.path, line, module, rhs))
                continue
            add(module, value)

    # A helper that takes the code as a parameter: the call sites in the same
    # package name the values it can write.
    notes: list[str] = []
    for path, line_number, module, _ in unresolved:
        source = next(item for item in sources if item.path == path)
        function = None
        for line in reversed(source.text.splitlines()[:line_number]):
            match = FUNC_RE.match(line)
            if match:
                function = match.group(1)
                break
        if function is None:
            _infra(f"error: {os.path.relpath(path, root)}:{line_number}: the helper "
                   "around a `prefix + code` concatenation could not be named")
        candidates: set[str] = set()
        for sibling in sources:
            if sibling.dir != source.dir:
                continue
            for call in CALL_RE.finditer(sibling.text):
                if call.group(1) != function:
                    continue
                for argument in call.group(2).split(","):
                    argument = argument.strip()
                    literal = re.fullmatch(r'"(.*)"', argument)
                    if literal is not None:
                        if CODE_RE.match(literal.group(1)):
                            candidates.add(literal.group(1))
                        continue
                    value = resolve(argument, sibling)
                    if value is not None and CODE_RE.match(value):
                        candidates.add(value)
        if not candidates:
            _infra(f"error: {os.path.relpath(path, root)}:{line_number}: {function} "
                   "takes its code as a parameter and no call site named one this gate "
                   "could read, so the derivation checked nothing there")
        notes.append(
            f"{os.path.relpath(path, root)}:{line_number}: {function} takes its code "
            f"from its call sites ({len(candidates)} candidate code(s) read)"
        )
        for code in candidates:
            add(module, code)

    # A mount-style closed table: a package with a prefix and `Code…`
    # constants answers every one of them through its checked writers.
    for source in sources:
        entry = prefix_of_dir.get(source.dir)
        if entry is None:
            continue
        _, module = entry
        for name, value in consts_by_dir[source.dir].items():
            if name.startswith("Code"):
                add(module, value)

    if not derived:
        _infra("error: the derivation produced no writable code, so this gate has no "
               "subject -- either the backend writes none or the reader broke")

    return derived, [f"{os.path.relpath(p, root)}:{line}: + {rhs}" for p, line, _, rhs in unresolved], notes


def _pairs_hook(pairs: list[tuple[str, object]]) -> dict[str, object]:
    """Keep every member of an object, duplicates included, under a marker."""
    result: dict[str, object] = {}
    duplicates: list[str] = []
    for key, value in pairs:
        if key in result:
            duplicates.append(key)
        result[key] = value
    if duplicates:
        result.setdefault("__duplicates__", duplicates)
    return result


def leaf_keys(value: object, prefix: str = "") -> set[str]:
    """The dot-joined leaf paths under the `errors` section."""
    found: set[str] = set()
    if isinstance(value, dict):
        for key, child in value.items():
            if key == "__duplicates__":
                continue
            path = f"{prefix}.{key}" if prefix else key
            found.update(leaf_keys(child, path))
    else:
        found.add(prefix)
    return found


def duplicate_paths(value: object, prefix: str = "") -> list[str]:
    """The dotted paths of objects that declared a key twice, at any depth."""
    found: list[str] = []
    if isinstance(value, dict):
        for key, child in value.items():
            if key == "__duplicates__":
                for duplicate in child:
                    found.append(f"{prefix}.{duplicate}" if prefix else duplicate)
                continue
            path = f"{prefix}.{key}" if prefix else key
            found.extend(duplicate_paths(child, path))
    return found


def load_bundle(root: str, language: str) -> tuple[set[str], list[str], str]:
    """Return one language's leaf keys, duplicate paths, and its path."""
    path = os.path.join(root, LOCALE_DIR, f"{language}.json")
    if not os.path.isfile(path):
        _infra(f"error: {os.path.relpath(path, root)} is missing -- the catalog this "
               "gate holds to the backend")
    try:
        with open(path, "r", encoding="utf-8") as fh:
            document = json.load(fh, object_pairs_hook=_pairs_hook)
    except (OSError, json.JSONDecodeError) as exc:
        _infra(f"error: {os.path.relpath(path, root)} could not be read: {exc}")
    if not isinstance(document, dict) or not isinstance(document.get(SECTION), dict):
        _infra(f"error: {os.path.relpath(path, root)} carries no {SECTION!r} object, so "
               "the lookup table has no subject")
    section = document[SECTION]
    return leaf_keys(section), duplicate_paths(section, SECTION), path


def referenced_frontend_files(root: str) -> list[tuple[str, str]]:
    """Return (relative path, text) for the frontend tree's source and tests."""
    found: list[tuple[str, str]] = []
    suffixes = {".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".json", ".md"}
    catalog_dir = os.path.join(root, LOCALE_DIR) + os.sep
    for base in ("web", "starters"):
        directory = os.path.join(root, base)
        if not os.path.isdir(directory):
            continue
        for current, dirs, files in os.walk(directory):
            dirs[:] = [d for d in dirs if d not in FRONTEND_SKIPPED_DIRS]
            for filename in files:
                if os.path.splitext(filename)[1] not in suffixes:
                    continue
                path = os.path.join(current, filename)
                if path.startswith(catalog_dir):
                    continue
                try:
                    found.append((os.path.relpath(path, root), _read(path)))
                except OSError:
                    continue
    return found


def report_kept_reasons(root: str) -> list[str]:
    """Check each kept entry against the tree and return the stale ones.

    A reason naming a file that no longer mentions the code is stale, like an
    exemption the backend now covers.
    """
    texts = referenced_frontend_files(root)
    stale: list[str] = []
    for key, reason in KEPT.items():
        if not any(key in text for _, text in texts):
            stale.append(f"{key}: KEPT says {reason}, but no frontend source or test "
                         "mentions the code any more")
    return stale


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--root",
        default=os.curdir,
        help="the tree to scan (default: the current directory)",
    )
    parser.add_argument(
        "--show-derived",
        action="store_true",
        help="print the derived writable code set and exit",
    )
    parser.add_argument(
        "--verbose",
        action="store_true",
        help="print how each helper's code parameter was read",
    )
    args = parser.parse_args(argv)

    root = os.path.abspath(args.root)
    if not os.path.isdir(root):
        print(f"platform-errors: {args.root} is not a directory", file=sys.stderr)
        return 2

    derived, unresolved, notes = derive(root)
    if args.show_derived:
        for key in sorted(derived):
            print(key)
        return 0

    bundles: dict[str, set[str]] = {}
    duplicates: dict[str, list[str]] = {}
    for language in LANGUAGES:
        keys, paths, _ = load_bundle(root, language)
        if not keys:
            _infra(f"error: the {language} catalog carries no entry under {SECTION!r}, "
                   "so this gate checked nothing")
        bundles[language] = keys
        duplicates[language] = paths

    findings: list[str] = []

    for language in LANGUAGES:
        for path in duplicates[language]:
            if path not in DUPLICATES:
                findings.append(
                    f"{os.path.relpath(os.path.join(root, LOCALE_DIR, language), root)}: "
                    f"{path} is declared twice; the last copy wins and the other is "
                    "silently gone (list it in DUPLICATES to keep it deliberately)"
                )

    first = LANGUAGES[0]
    for language in LANGUAGES[1:]:
        only_first = sorted(bundles[first] - bundles[language])
        only_this = sorted(bundles[language] - bundles[first])
        for key in only_first:
            findings.append(f"the {language} catalog is missing {key}, which {first} carries")
        for key in only_this:
            findings.append(f"the {first} catalog is missing {key}, which {language} carries")

    for key in sorted(derived - bundles[first]):
        findings.append(
            f"{key}: the backend writes this code and the catalog has no text for it, "
            "so a refusal renders as the raw key"
        )

    for key in sorted(bundles[first] - derived):
        if key in KEPT:
            continue
        findings.append(
            f"{key}: the catalog carries this code and no backend source writes it; "
            "delete it, or add it to KEPT with the frontend files that still refer to it"
        )

    for key in sorted(KEPT):
        if key not in bundles[first]:
            findings.append(f"{key}: KEPT names a key the catalog no longer carries")
        elif key in derived:
            findings.append(
                f"{key}: KEPT exempts a code the backend now writes; remove the exemption"
            )
    findings.extend(report_kept_reasons(root))

    for note in notes:
        if args.verbose:
            print(f"note: {note}", file=sys.stderr)
    for line in unresolved:
        if args.verbose:
            print(f"note: unresolved helper parameter {line}", file=sys.stderr)

    if findings:
        for finding in findings:
            print(finding)
        print(
            f"platform-errors: {len(findings)} finding(s) against "
            f"{len(derived)} backend code(s) and {len(bundles[first])} catalog key(s)",
            file=sys.stderr,
        )
        return 1
    kept = len(bundles[first] & set(KEPT))
    print(
        f"platform-errors: {len(derived)} backend code(s) and {kept} kept exemption(s) "
        f"match the {len(bundles[first])}-key catalog in both languages"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
