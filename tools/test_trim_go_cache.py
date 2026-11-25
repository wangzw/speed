#!/usr/bin/env python3
"""Cases for tools/trim_go_cache.py, the Go build cache trimmer."""

from __future__ import annotations

import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

from trim_go_cache import MARGIN_SECONDS

ROOT = pathlib.Path(__file__).resolve().parent.parent
TOOL = ROOT / "tools" / "trim_go_cache.py"

README_FIRST_LINE = "This directory holds cached build artifacts from the Go build system."

SUMMARY = re.compile(r"deleted (\d+) entries \((\d+) bytes\), kept (\d+) entries \((\d+) bytes\)")

STARTED = 1_700_000_000
NOW = STARTED + 600
CUTOFF = STARTED - 3600
OLD = STARTED - 7200
RECENT = STARTED - 1800
FUTURE = STARTED + 60

MODULE = """module {module}

go 1.21
"""

SOURCE = """package {package}

import (
\t"fmt"
\t"os"
)

func main() {{
\tfmt.Fprintln(os.Stdout, "{marker}")
}}
"""


def write_file(root: pathlib.Path, relative: str, mtime: int, content: str = "") -> pathlib.Path:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")
    os.utime(path, (mtime, mtime))
    return path


def write_directory_entry(
    root: pathlib.Path, relative: str, mtime: int, content: str = ""
) -> pathlib.Path:
    path = root / relative
    path.mkdir(parents=True, exist_ok=True)
    (path / "content").write_text(content, encoding="utf-8")
    os.utime(path, (mtime, mtime))
    return path


def write_readme(root: pathlib.Path, first_line: str = README_FIRST_LINE) -> pathlib.Path:
    path = root / "README"
    path.write_text(first_line + "\n", encoding="utf-8")
    return path


def action_entry(output_id: str) -> str:
    """A Go cache action entry, in the format Go writes its -a files in."""
    return f"v1 {'0' * 64} {output_id} {0:20d} {0:20d}\n"


def listing(root: pathlib.Path) -> list[str]:
    return sorted(str(path.relative_to(root)) for path in root.rglob("*"))


def run_tool(*arguments: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(TOOL), *arguments],
        capture_output=True,
        text=True,
        timeout=600,
    )


def summary(completed: subprocess.CompletedProcess[str]) -> tuple[int, int, int, int]:
    match = SUMMARY.search(completed.stdout)
    if match is None:
        raise AssertionError(f"the run printed no summary:\nstdout:\n{completed.stdout}\nstderr:\n{completed.stderr}")
    return tuple(int(group) for group in match.groups())


class DeleteOldKeepNewTests(unittest.TestCase):
    def test_removes_entries_older_than_the_margin_and_keeps_the_rest(self) -> None:
        """Checks that entries before the cutoff go and the rest stay, with their sizes."""
        with tempfile.TemporaryDirectory() as scratch:
            cache = pathlib.Path(scratch) / "cache"
            cache.mkdir()
            write_readme(cache)
            old_index = write_file(cache, f"aa/{'1' * 32}-a", OLD, "0123456789")
            old_data = write_file(cache, f"aa/{'2' * 64}-d", OLD, "01234567890123456789")
            old_executable = write_directory_entry(cache, f"bb/{'3' * 64}-d", OLD, "1234567")
            recent_index = write_file(cache, f"cc/{'4' * 32}-a", RECENT, "abc")
            future_data = write_file(cache, f"dd/{'5' * 64}-d", FUTURE, "abcd")
            at_cutoff = write_file(cache, f"ee/{'6' * 32}-a", CUTOFF, "12345")

            completed = run_tool(
                "--cache-dir", str(cache), "--started", str(STARTED), "--now", str(NOW)
            )

            self.assertEqual(0, completed.returncode, completed.stderr)
            for removed in (old_index, old_data, old_executable):
                self.assertFalse(
                    removed.exists(),
                    f"{removed.relative_to(cache)} is older than the cutoff and should be gone",
                )
            for kept in (recent_index, future_data, at_cutoff):
                self.assertTrue(
                    kept.exists(),
                    f"{kept.relative_to(cache)} is not older than the cutoff and should stay",
                )
            self.assertTrue((cache / "README").exists())
            self.assertTrue((cache / "aa").is_dir(), "the trim removed an entry's subdirectory")
            self.assertEqual((3, 37, 3, 12), summary(completed))


class ReferencedOutputTests(unittest.TestCase):
    def test_keeps_the_outputs_a_kept_action_entry_names(self) -> None:
        """Checks an old output entry stays when a kept action entry records its ID."""
        with tempfile.TemporaryDirectory() as scratch:
            cache = pathlib.Path(scratch) / "cache"
            cache.mkdir()
            write_readme(cache)
            output_id = "a" * 64
            executable_id = "b" * 64
            unused_id = "e" * 64
            kept_output = write_file(cache, f"{output_id[:2]}/{output_id}-d", OLD, "0123456789")
            kept_executable = write_directory_entry(
                cache, f"{executable_id[:2]}/{executable_id}-d", OLD, "1234567"
            )
            kept_action = write_file(cache, f"cc/{'c' * 64}-a", RECENT, action_entry(output_id))
            kept_executable_action = write_file(
                cache, f"dd/{'d' * 64}-a", RECENT, action_entry(executable_id)
            )
            unused_output = write_file(cache, f"{unused_id[:2]}/{unused_id}-d", OLD, "payload")
            old_action = write_file(cache, f"ff/{'f' * 64}-a", OLD, action_entry(unused_id))

            completed = run_tool(
                "--cache-dir", str(cache), "--started", str(STARTED), "--now", str(NOW)
            )

            self.assertEqual(0, completed.returncode, completed.stderr)
            for kept in (kept_action, kept_output, kept_executable_action, kept_executable):
                self.assertTrue(
                    kept.exists(),
                    f"{kept.relative_to(cache)} is named by a kept action entry and should stay",
                )
            for removed in (old_action, unused_output):
                self.assertFalse(
                    removed.exists(),
                    f"{removed.relative_to(cache)} is older than the cutoff and should be gone",
                )
            entry_size = len(action_entry(output_id))
            self.assertEqual(
                (2, entry_size + len("payload"), 4, 2 * entry_size + 10 + 7),
                summary(completed),
            )


class NonEntryTests(unittest.TestCase):
    def test_leaves_everything_that_is_not_a_cache_entry(self) -> None:
        """Checks that only -a and -d entries in the 256 hex subdirectories are candidates."""
        with tempfile.TemporaryDirectory() as scratch:
            cache = pathlib.Path(scratch) / "cache"
            cache.mkdir()
            write_readme(cache)
            write_file(cache, "trim.txt", OLD, str(OLD))
            write_file(cache, "testexpire.txt", OLD, str(OLD))
            write_file(cache, "fuzz/deadbeef", OLD, "fuzz corpus")
            write_file(cache, f"aa/{'7' * 32}-a.bak", OLD, "a backup")
            write_file(cache, "aa/notes.txt", OLD, "a note")
            write_file(cache, "aa/sub/" + f"{'8' * 32}-a", OLD, "one level too deep")
            write_file(cache, f"zz/{'9' * 64}-d", OLD, "not a hex subdirectory")
            write_file(cache, f"{'a' * 32}-a", OLD, "not in a subdirectory")
            before = listing(cache)

            completed = run_tool(
                "--cache-dir", str(cache), "--started", str(STARTED), "--now", str(NOW)
            )

            self.assertEqual(0, completed.returncode, completed.stderr)
            self.assertEqual(before, listing(cache))
            self.assertEqual((0, 0, 0, 0), summary(completed))


class RefusalTests(unittest.TestCase):
    @staticmethod
    def populated(root: pathlib.Path) -> pathlib.Path:
        cache = root / "cache"
        cache.mkdir()
        write_readme(cache)
        write_file(cache, f"aa/{'b' * 32}-a", OLD, "payload")
        write_file(cache, f"aa/{'c' * 64}-d", OLD, "payload")
        write_directory_entry(cache, f"bb/{'d' * 64}-d", OLD, "payload")
        write_file(cache, "trim.txt", OLD, str(OLD))
        write_file(cache, "fuzz/corpus", OLD, "payload")
        return cache

    @staticmethod
    def without_readme(root: pathlib.Path) -> pathlib.Path:
        cache = RefusalTests.populated(root)
        (cache / "README").unlink()
        return cache

    @staticmethod
    def with_foreign_readme(root: pathlib.Path) -> pathlib.Path:
        cache = RefusalTests.populated(root)
        write_readme(cache, "Some other directory entirely.")
        return cache

    def test_refuses_and_touches_nothing(self) -> None:
        """Checks that every refusal exits 2 with a reason and leaves the tree unchanged."""
        scenarios = {
            "missing timestamp": (
                self.populated,
                lambda cache: ["--cache-dir", str(cache)],
            ),
            "timestamp is not an integer": (
                self.populated,
                lambda cache: ["--cache-dir", str(cache), "--started", "soon"],
            ),
            "timestamp is later than the current time": (
                self.populated,
                lambda cache: [
                    "--cache-dir", str(cache), "--started", str(STARTED), "--now", str(STARTED - 1),
                ],
            ),
            "directory does not exist": (
                self.populated,
                lambda cache: [
                    "--cache-dir", str(cache.parent / "absent"), "--started", str(STARTED), "--now", str(NOW),
                ],
            ),
            "directory has no README": (
                self.without_readme,
                lambda cache: ["--cache-dir", str(cache), "--started", str(STARTED), "--now", str(NOW)],
            ),
            "README is not the Go one": (
                self.with_foreign_readme,
                lambda cache: ["--cache-dir", str(cache), "--started", str(STARTED), "--now", str(NOW)],
            ),
        }
        for name, (build, arguments) in scenarios.items():
            with self.subTest(name), tempfile.TemporaryDirectory() as scratch:
                root = pathlib.Path(scratch)
                cache = build(root)
                before = listing(root)
                completed = run_tool(*arguments(cache))
                self.assertEqual(
                    2, completed.returncode, f"{name}: expected a refusal, got {completed.returncode}:\n"
                    f"stdout:\n{completed.stdout}\nstderr:\n{completed.stderr}",
                )
                self.assertIn("refus", completed.stderr.lower(), f"{name}: the refusal said no reason")
                self.assertEqual(before, listing(root), f"{name}: the refusal changed the tree")


class ErrorTests(unittest.TestCase):
    def test_a_scan_error_deletes_nothing_and_exits_one(self) -> None:
        """Checks that a subdirectory it cannot read aborts before anything is deleted."""
        with tempfile.TemporaryDirectory() as scratch:
            cache = pathlib.Path(scratch) / "cache"
            cache.mkdir()
            write_readme(cache)
            write_file(cache, "00", OLD, "not a subdirectory")
            write_file(cache, f"aa/{'1' * 32}-a", OLD, "payload")
            before = listing(cache)

            completed = run_tool(
                "--cache-dir", str(cache), "--started", str(STARTED), "--now", str(NOW)
            )

            self.assertEqual(1, completed.returncode, f"stderr:\n{completed.stderr}")
            self.assertIn("cannot read", completed.stderr)
            self.assertEqual(
                before, listing(cache), "a scan error deleted entries instead of stopping"
            )

    def test_an_action_entry_it_cannot_read_deletes_nothing_and_exits_one(self) -> None:
        """Checks that an -a entry it cannot open aborts before anything is deleted."""
        with tempfile.TemporaryDirectory() as scratch:
            cache = pathlib.Path(scratch) / "cache"
            cache.mkdir()
            write_readme(cache)
            unreadable = cache / f"aa/{'2' * 64}-a"
            unreadable.parent.mkdir()
            unreadable.symlink_to(unreadable.parent / "absent")
            os.utime(unreadable, (RECENT, RECENT), follow_symlinks=False)
            write_file(cache, f"bb/{'3' * 64}-d", OLD, "payload")
            before = listing(cache)

            completed = run_tool(
                "--cache-dir", str(cache), "--started", str(STARTED), "--now", str(NOW)
            )

            self.assertEqual(1, completed.returncode, f"stderr:\n{completed.stderr}")
            self.assertIn("cannot read", completed.stderr)
            self.assertEqual(
                before, listing(cache), "an unreadable entry did not stop the trim"
            )


class RealToolchainTests(unittest.TestCase):
    def test_the_rebuilt_package_hits_the_cache_after_trimming(self) -> None:
        """Checks that a rebuild refreshes the entries it uses and the trim keeps them."""
        if shutil.which("go") is None:
            self.skipTest("go is not on PATH")
        with tempfile.TemporaryDirectory() as scratch:
            scratch_path = pathlib.Path(scratch)
            cache = scratch_path / "cache"
            cache.mkdir()
            modules = {}
            for name in ("a", "b"):
                module = scratch_path / name
                module.mkdir()
                (module / "go.mod").write_text(
                    MODULE.format(module=name), encoding="utf-8"
                )
                (module / "main.go").write_text(
                    SOURCE.format(package=name, marker=f"{name}-only"),
                    encoding="utf-8",
                )
                modules[name] = module
            self.env = self.go_env(cache, scratch_path / "gopath")

            for name in modules:
                self.go("build", "./...", cwd=modules[name])
            a_export = self.export_of(modules["a"])
            b_export = self.export_of(modules["b"])
            self.assertNotEqual(a_export, b_export)

            # Go refreshes an entry's mtime only once it is an hour old, so age
            # just past the margin: a smaller margin stops the rebuild below
            # from refreshing what it uses, and the case then fails.
            aged = time.time() - (MARGIN_SECONDS + 300)
            for directory, directories, files in os.walk(cache):
                for name in files:
                    os.utime(os.path.join(directory, name), (aged, aged))
                for name in directories:
                    os.utime(os.path.join(directory, name), (aged, aged))

            started = int(time.time())
            self.go("build", "./...", cwd=modules["a"])
            completed = run_tool(
                "--cache-dir", str(cache), "--started", str(started), "--now", str(int(time.time()))
            )
            self.assertEqual(0, completed.returncode, completed.stderr)

            self.assertTrue(
                os.path.exists(a_export),
                "the trim removed an entry the rebuild of a just used",
            )
            self.assertFalse(
                os.path.exists(b_export),
                "b's own entry was not deleted; the rebuild of a did not refresh every entry "
                f"it used, or the cutoff kept entries older than the margin:\n{completed.stdout}",
            )
            self.assertLess(0, summary(completed)[0], "the trim deleted nothing")

            rebuilt = self.go("build", "-x", "./...", cwd=modules["a"])
            calls = compile_calls(rebuilt.stdout + rebuilt.stderr)
            self.assertEqual(
                [],
                calls,
                "the rebuild after the trim compiled something, so the cache did not keep "
                "what the run used:\n" + "\n".join(calls),
            )

    def go_env(self, cache: pathlib.Path, gopath: pathlib.Path) -> dict[str, str]:
        env = dict(os.environ)
        flags = [
            flag
            for flag in env.get("GOFLAGS", "").split()
            if not flag.startswith("-count=")
        ]
        env.update(
            {
                "GOCACHE": str(cache),
                "GOPATH": str(gopath),
                "GOWORK": "off",
                "GOTOOLCHAIN": "local",
                "GOPROXY": "off",
                "GOFLAGS": " ".join(flags),
            }
        )
        return env

    def go(self, *arguments: str, cwd: pathlib.Path) -> subprocess.CompletedProcess[str]:
        completed = subprocess.run(
            ["go", *arguments],
            cwd=cwd,
            env=self.env,
            capture_output=True,
            text=True,
            timeout=600,
        )
        self.assertEqual(
            0,
            completed.returncode,
            f"go {' '.join(arguments)} failed:\n{completed.stdout}\n{completed.stderr}",
        )
        return completed

    def export_of(self, module: pathlib.Path) -> str:
        return self.go("list", "-export", "-f", "{{.Export}}", ".", cwd=module).stdout.strip()


def compile_calls(output: str) -> list[str]:
    """Every compile invocation in a `go build -x` transcript."""
    calls = []
    for line in output.splitlines():
        tokens = line.split()
        if tokens and os.path.basename(tokens[0]) == "compile":
            calls.append(line)
    return calls


if __name__ == "__main__":
    unittest.main()
