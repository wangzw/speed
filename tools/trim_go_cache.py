#!/usr/bin/env python3
"""Delete the Go build cache entries that this run did not use.

The command line, the deletion rule and the exit codes are in tools/README.md.
"""

from __future__ import annotations

import argparse
import os
import pathlib
import shutil
import stat
import sys
import time
from collections.abc import Iterator

# Go refreshes an entry's mtime on use at most once an hour, so an entry this
# run used can read as an hour older than the run's start.
MARGIN_SECONDS = 3600

GO_CACHE_README_FIRST_LINE = (
    "This directory holds cached build artifacts from the Go build system."
)

ENTRY_SUFFIXES = ("-a", "-d")

SUBDIRECTORIES = tuple(f"{index:02x}" for index in range(256))

# A Go action entry reads "v1 <hex action ID> <hex output ID> <size, 20 wide>
# <time, 20 wide>\n", and the -d entry it names carries the output ID as its name.
HASH_HEX_LENGTH = 64
ACTION_ENTRY_PREFIX = b"v1 "
OUTPUT_ID_OFFSET = len(ACTION_ENTRY_PREFIX) + HASH_HEX_LENGTH + 1
OUTPUT_ID_END = OUTPUT_ID_OFFSET + HASH_HEX_LENGTH


def _is_go_cache(cache_dir: pathlib.Path) -> bool:
    """Report whether cache_dir carries the README the Go command writes."""
    try:
        with open(cache_dir / "README", encoding="utf-8", errors="replace") as readme:
            first_line = readme.readline()
    except OSError:
        return False
    return first_line.rstrip("\r\n") == GO_CACHE_README_FIRST_LINE


def _entry_files(path: pathlib.Path, is_directory: bool) -> Iterator[pathlib.Path]:
    if not is_directory:
        yield path
        return
    for directory, _, names in os.walk(path):
        for name in names:
            yield pathlib.Path(directory) / name


def _entry_bytes(path: pathlib.Path, is_directory: bool) -> int:
    total = 0
    for file in _entry_files(path, is_directory):
        try:
            total += os.lstat(file).st_size
        except OSError:
            continue
    return total


def _remove(path: pathlib.Path, is_directory: bool) -> None:
    if is_directory:
        shutil.rmtree(path)
    else:
        path.unlink()


def _output_id(path: pathlib.Path) -> str | None:
    """The output ID a Go action entry records, lowercase as its -d entry is named."""
    with open(path, "rb") as entry:
        header = entry.read(OUTPUT_ID_END)
    if len(header) < OUTPUT_ID_END or not header.startswith(ACTION_ENTRY_PREFIX):
        return None
    if header[OUTPUT_ID_OFFSET - 1 : OUTPUT_ID_OFFSET] != b" ":
        return None
    try:
        value = header[OUTPUT_ID_OFFSET:OUTPUT_ID_END].decode("ascii")
    except UnicodeDecodeError:
        return None
    if any(character not in "0123456789abcdefABCDEF" for character in value):
        return None
    return value.lower()


def _collect(
    cache_dir: pathlib.Path,
) -> tuple[list[tuple[pathlib.Path, str, bool, float]], int]:
    """Scan and stat the candidate entries; return them with the number of failures."""
    entries: list[tuple[pathlib.Path, str, bool, float]] = []
    failures = 0
    for name in SUBDIRECTORIES:
        subdirectory = cache_dir / name
        try:
            with os.scandir(subdirectory) as scan:
                scanned = sorted(scan, key=lambda entry: entry.name)
        except FileNotFoundError:
            continue
        except OSError as error:
            print(f"trim_go_cache: cannot read {subdirectory}: {error}", file=sys.stderr)
            failures += 1
            continue
        for entry in scanned:
            if not entry.name.endswith(ENTRY_SUFFIXES):
                continue
            try:
                info = entry.stat(follow_symlinks=False)
            except OSError as error:
                print(f"trim_go_cache: cannot stat {entry.path}: {error}", file=sys.stderr)
                failures += 1
                continue
            entries.append(
                (pathlib.Path(entry.path), entry.name, stat.S_ISDIR(info.st_mode), info.st_mtime)
            )
    return entries, failures


def trim(cache_dir: pathlib.Path, started: int) -> int:
    """Delete the entries older than the cutoff that no kept action entry names."""
    cutoff = started - MARGIN_SECONDS
    print(f"trim_go_cache: trimming {cache_dir}; entries older than {cutoff} go")
    entries, failures = _collect(cache_dir)
    # Go leaves an existing output's mtime alone, so a fresh -a can name an old -d.
    needed: set[pathlib.Path] = set()
    if not failures:
        for path, name, is_directory, mtime in entries:
            if mtime < cutoff or is_directory or not name.endswith("-a"):
                continue
            try:
                value = _output_id(path)
            except OSError as error:
                print(f"trim_go_cache: cannot read {path}: {error}", file=sys.stderr)
                failures += 1
                continue
            if value is not None:
                needed.add(cache_dir / value[:2] / f"{value}-d")
    if failures:
        print(
            f"trim_go_cache: {failures} entries could not be collected, deleted nothing",
            file=sys.stderr,
        )
        return 1

    deleted_entries = deleted_bytes = kept_entries = kept_bytes = 0
    for path, name, is_directory, mtime in entries:
        if mtime >= cutoff or path in needed:
            kept_entries += 1
            kept_bytes += _entry_bytes(path, is_directory)
            continue
        size = _entry_bytes(path, is_directory)
        try:
            _remove(path, is_directory)
        except OSError as error:
            print(f"trim_go_cache: cannot remove {path}: {error}", file=sys.stderr)
            failures += 1
            continue
        deleted_entries += 1
        deleted_bytes += size

    print(
        f"trim_go_cache: deleted {deleted_entries} entries ({deleted_bytes} bytes), "
        f"kept {kept_entries} entries ({kept_bytes} bytes)"
    )
    if failures:
        print(f"trim_go_cache: {failures} entries could not be handled", file=sys.stderr)
        return 1
    return 0


def _refuse(reason: str) -> int:
    print(f"trim_go_cache: refusing to trim: {reason}", file=sys.stderr)
    return 2


def main(argv: list[str] | None = None) -> int:
    """Trim the cache at --cache-dir; 0 done, 2 refused, 1 on an error."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--cache-dir", type=pathlib.Path, help="the Go build cache to trim")
    parser.add_argument("--started", help="the Unix time, in seconds, this run started")
    parser.add_argument("--now", help="the current Unix time in seconds (default: the system clock)")
    args = parser.parse_args(argv)

    if args.cache_dir is None:
        return _refuse("no --cache-dir was given")
    if args.started is None:
        return _refuse("no --started timestamp was given")
    try:
        started = int(args.started)
    except ValueError:
        return _refuse(f"--started {args.started!r} is not an integer number of seconds")
    try:
        now = int(args.now) if args.now is not None else int(time.time())
    except ValueError:
        return _refuse(f"--now {args.now!r} is not an integer number of seconds")
    if started > now:
        return _refuse(f"--started {started} is later than the current time {now}")
    if not args.cache_dir.is_dir():
        return _refuse(f"{args.cache_dir} is not a directory")
    if not _is_go_cache(args.cache_dir):
        return _refuse(
            f"{args.cache_dir} has no README written by the Go command, so it is not a GOCACHE"
        )
    return trim(args.cache_dir, started)


if __name__ == "__main__":
    raise SystemExit(main())
