#!/usr/bin/env python3
"""Toolchain drift gate for the root .mise.toml and the demo image.

The developer toolchain is pinned with mise; the root .mise.toml carries
the versions. Some of those versions are MIRRORS of an authoritative
source elsewhere in the repository -- the file a tool of its own already
reads -- and this gate fails when a mirror drifts from its source:

  go            go.work's `go` directive
  node          web/.nvmrc
  pnpm          web/package.json's `packageManager`

The other tools .mise.toml pins are pinned there and nowhere else, so
they have no source to drift from and this gate does not check them.

starters/demo/Dockerfile builds the demo image on base images that carry
the same toolchain, so two of its tags are mirrors as well:

  golang image  its tag's version (the part before the first `-`)
                equals go.work's `go` directive
  node image    its tag's major equals web/.nvmrc's major

Every `FROM` line naming that image is checked. The Dockerfile's other
images (the runtime base) are not toolchain versions and are not checked.

Bump the authoritative source and the mirror together; this gate exists
because they are separate files and will drift without it.

Exit codes: 0 = every mirror equals its source; 1 = drift (a mirror
differs from its source, or a report printed below that starts with
MISMATCH); 2 = infrastructure error (a source file or the Dockerfile
missing or unparsable, an expected tool absent from .mise.toml, or an
expected image absent from the Dockerfile). Paths in the report are
relative to --root.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import tomllib


def _infra(message: str) -> None:
    """Report an infrastructure failure and exit 2.

    The exit-code contract (module docstring): 2 = infrastructure error
    (a source file missing or unparsable, a tool absent from .mise.toml),
    and only 1 = drift. SystemExit alone cannot carry the distinction --
    sys.exit("message") exits 1 -- so the message is printed to stderr
    first and the bare code raised.
    """
    print(message, file=sys.stderr)
    raise SystemExit(2)


def _read_mise_tools(root: str) -> dict[str, str]:
    path = os.path.join(root, ".mise.toml")
    try:
        with open(path, "rb") as fh:
            data = tomllib.load(fh)
    except FileNotFoundError:
        _infra(f"error: {path} is missing -- the mise config this gate guards")
    except tomllib.TOMLDecodeError as exc:
        _infra(f"error: {path} is not parsable TOML: {exc}")
    tools = data.get("tools")
    if not isinstance(tools, dict):
        _infra(f"error: {path} has no [tools] table")
    return {str(k): str(v) for k, v in tools.items()}


def _read_go_version(root: str) -> str:
    path = os.path.join(root, "go.work")
    try:
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
    except FileNotFoundError:
        _infra(f"error: {path} is missing -- the source of the go pin")
    m = re.search(r"^go\s+(\d+\.\d+(?:\.\d+)?)", text, re.MULTILINE)
    if not m:
        _infra(f"error: no 'go' directive found in {path}")
    return m.group(1)


def _read_nvmrc_node_version(root: str) -> str:
    path = os.path.join(root, "web", ".nvmrc")
    try:
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
    except FileNotFoundError:
        _infra(f"error: {path} is missing -- the source of the node pin")
    # .nvmrc holds a bare version, with or without a leading `v`; mise's pin
    # carries neither, so trim the surrounding whitespace and that prefix.
    version = text.strip().removeprefix("v")
    if not version:
        _infra(f"error: {path} is empty -- the source of the node pin")
    return version


def _read_package_manager_pnpm_version(root: str) -> str:
    path = os.path.join(root, "web", "package.json")
    try:
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
    except FileNotFoundError:
        _infra(f"error: {path} is missing -- the source of the pnpm pin")
    except json.JSONDecodeError as exc:
        _infra(f"error: {path} is not parsable JSON: {exc}")
    manager = data.get("packageManager")
    if not isinstance(manager, str) or not manager:
        _infra(f"error: no 'packageManager' field in {path}")
    name, _, version = manager.partition("@")
    if name != "pnpm" or not version:
        _infra(f"error: {path}'s packageManager is {manager!r}, not 'pnpm@<version>'")
    # corepack allows a trailing `+<hash>` on the version; the pin carries
    # the plain version only.
    return version.split("+", 1)[0]


DOCKERFILE = os.path.join("starters", "demo", "Dockerfile")

# A FROM line's image reference: optional flags such as --platform=..., an
# optional Docker Hub prefix, then the image name and its tag. A digest
# after the tag (`@sha256:...`) is not part of the tag.
_FROM_RE = re.compile(
    r"^FROM\s+(?:--\S+\s+)*(?:docker\.io/)?(?:library/)?"
    r"(?P<image>[a-z0-9._/-]+):(?P<tag>[^\s@]+)",
    re.MULTILINE | re.IGNORECASE,
)


def _read_dockerfile_tags(root: str, image: str) -> list[str]:
    """Return the tag of every FROM line in the demo Dockerfile naming image."""
    path = os.path.join(root, DOCKERFILE)
    try:
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
    except FileNotFoundError:
        _infra(f"error: {path} is missing -- the demo image this gate guards")
    tags = [
        m.group("tag")
        for m in _FROM_RE.finditer(text)
        if m.group("image").lower() == image
    ]
    if not tags:
        _infra(f"error: no 'FROM {image}:<tag>' line found in {path}")
    return tags


def _major(version: str) -> str:
    return re.split(r"[.-]", version, maxsplit=1)[0]


def _golang_image_version(tag: str) -> str:
    # golang:1.27.4-bookworm -> 1.27.4; the suffix names the base distribution.
    return tag.split("-", 1)[0]


def _node_image_major(tag: str) -> str:
    # node:26-bookworm-slim -> 26; node:26.13.1-bookworm-slim -> 26.
    return _major(tag)


def _read_nvmrc_node_major(root: str) -> str:
    return _major(_read_nvmrc_node_version(root))


# Tools .mise.toml mirrors, with the authoritative source each one mirrors
# and the name the report calls that source.
SOURCES = [
    ("go", _read_go_version, "go.work's go directive"),
    ("node", _read_nvmrc_node_version, "web/.nvmrc"),
    ("pnpm", _read_package_manager_pnpm_version, "web/package.json's packageManager"),
]

# Images the demo Dockerfile mirrors: the image name, how its tag reduces to
# the compared version, the authoritative source and the name the report
# calls that source.
IMAGE_SOURCES = [
    ("golang", _golang_image_version, _read_go_version, "go.work's go directive"),
    ("node", _node_image_major, _read_nvmrc_node_major, "web/.nvmrc's major"),
]


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description=(
            "Fail when a version pinned in the root .mise.toml no longer "
            "mirrors its authoritative source (go.work, web/.nvmrc, "
            "web/package.json), or a toolchain image in "
            "starters/demo/Dockerfile no longer matches go.work or web/.nvmrc."
        )
    )
    parser.add_argument(
        "--root",
        default=".",
        help="repository root to check (default: current directory); "
        "paths in the report are relative to it",
    )
    args = parser.parse_args(argv)
    root = os.path.abspath(args.root)
    if not os.path.isdir(root):
        print(f"error: --root is not a directory: {args.root}", file=sys.stderr)
        return 2

    mise = _read_mise_tools(root)
    drifted = 0
    missing = 0
    for tool, reader, source_name in SOURCES:
        if tool not in mise:
            print(f"toolchain: MISSING     {tool} is absent from .mise.toml")
            missing += 1
            continue
        mirror = mise[tool]
        source = reader(root)
        if mirror == source:
            print(
                f"toolchain: ok          {tool}: .mise.toml {mirror} == "
                f"{source_name} ({source})"
            )
        else:
            print(
                f"toolchain: MISMATCH    {tool}: .mise.toml pins {mirror} but "
                f"{source_name} says {source}"
            )
            drifted += 1

    dockerfile = DOCKERFILE.replace(os.sep, "/")
    for image, reduce_tag, reader, source_name in IMAGE_SOURCES:
        source = reader(root)
        for tag in _read_dockerfile_tags(root, image):
            mirror = reduce_tag(tag)
            if mirror == source:
                print(
                    f"toolchain: ok          {image} image: {dockerfile} "
                    f"{image}:{tag} == {source_name} ({source})"
                )
            else:
                print(
                    f"toolchain: MISMATCH    {image} image: {dockerfile} pins "
                    f"{image}:{tag} but {source_name} says {source}"
                )
                drifted += 1

    if missing:
        # An expected tool absent from .mise.toml is an infrastructure
        # failure per the module docstring's exit-code contract: the gate
        # exists to prove the mirrors cannot drift, and a mirror that does
        # not exist cannot be compared.
        print(
            f"error: {missing} expected tool(s) absent from .mise.toml -- "
            "every tool this gate's SOURCES list pins must appear in the "
            "[tools] table (see the .mise.toml header)",
            file=sys.stderr,
        )
        return 2
    if drifted:
        print(
            f"error: {drifted} tool version(s) drifted -- bump the authoritative "
            "source and its mirror together (see the .mise.toml header)",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
