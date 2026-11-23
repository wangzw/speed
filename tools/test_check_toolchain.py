#!/usr/bin/env python3
"""Unit tests for check_toolchain.py's exit-code contract and its mirror
reads (go.work, web/.nvmrc, web/package.json, starters/demo/Dockerfile).

Stdlib-only (unittest + tempfile), matching this directory's own "plain
executables with no third-party dependencies" convention (tools/README.md's
"Running in CI and locally" section). Run directly:

    python3 tools/test_check_toolchain.py

The contract under test is the gate's exit codes: an infrastructure error
-- a source file missing or unparsable, or an expected tool absent from
.mise.toml -- exits 2, while genuine version drift stays a content error
exiting 1. The readers must not fall through sys.exit("error: ..."),
which exits 1 and would make a missing input file indistinguishable from
drift.
"""

from __future__ import annotations

import contextlib
import io
import pathlib
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import check_toolchain as m  # noqa: E402

# A coherent fixture tree: every mirror equals its source (values copied
# from the real tree), so a test can drift or delete one piece and know
# which failure it caused.
MISE = """\
[tools]
go = "1.27.4"
node = "26"
pnpm = "12.21.0"
golangci-lint = "2.15.0"
python = "3.14.7"
"""

DOCKERFILE = """\
# syntax=docker/dockerfile:1
FROM node:26-bookworm-slim AS web
RUN true
FROM golang:1.27.4-bookworm AS build
RUN true
FROM alpine:3.24
"""


def write_fixture(root: pathlib.Path) -> None:
    """Write the fully-consistent fixture tree under root."""
    (root / ".mise.toml").write_text(MISE, encoding="utf-8")
    (root / "go.work").write_text("go 1.27.4\n", encoding="utf-8")
    web = root / "web"
    web.mkdir()
    (web / ".nvmrc").write_text("26\n", encoding="utf-8")
    (web / "package.json").write_text(
        '{"packageManager": "pnpm@12.21.0"}\n', encoding="utf-8"
    )
    demo = root / "starters" / "demo"
    demo.mkdir(parents=True)
    (demo / "Dockerfile").write_text(DOCKERFILE, encoding="utf-8")


class ToolchainGateTests(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = pathlib.Path(self._tmp.name)
        write_fixture(self.root)

    def _run(self) -> tuple[int, str]:
        buf = io.StringIO()
        with contextlib.redirect_stdout(buf):
            code = m.main(["--root", str(self.root)])
        return code, buf.getvalue()

    def _write_dockerfile(self, text: str) -> None:
        (self.root / "starters" / "demo" / "Dockerfile").write_text(
            text, encoding="utf-8"
        )

    def _run_expect_system_exit(self) -> SystemExit:
        buf = io.StringIO()
        with contextlib.redirect_stdout(buf):
            with self.assertRaises(SystemExit) as cm:
                m.main(["--root", str(self.root)])
        return cm.exception

    def test_consistent_tree_passes(self) -> None:
        code, out = self._run()
        self.assertEqual(code, 0)
        self.assertIn("toolchain: ok          go:", out)
        self.assertIn("toolchain: ok          node:", out)
        self.assertIn("toolchain: ok          pnpm:", out)
        self.assertIn("toolchain: ok          golang image:", out)
        self.assertIn("toolchain: ok          node image:", out)

    def test_unmirrored_tools_are_not_checked(self) -> None:
        # Only the tools in SOURCES are mirrors. Everything else
        # .mise.toml pins is pinned there alone, so changing it is not
        # drift and must not fail the gate.
        (self.root / ".mise.toml").write_text(
            MISE.replace('golangci-lint = "2.15.0"', 'golangci-lint = "9.9.9"'),
            encoding="utf-8",
        )
        code, _ = self._run()
        self.assertEqual(code, 0)

    def test_drift_stays_a_content_error_exit_1(self) -> None:
        # Genuine version drift is the gate's content finding: exit 1,
        # never 2.
        (self.root / ".mise.toml").write_text(
            MISE.replace('go = "1.27.4"', 'go = "9.9.9"'),
            encoding="utf-8",
        )
        code, out = self._run()
        self.assertEqual(code, 1)
        self.assertIn("MISMATCH", out)

    def test_node_drift_exits_1(self) -> None:
        # node mirrors web/.nvmrc; a pin that no longer matches it drifts.
        (self.root / ".mise.toml").write_text(
            MISE.replace('node = "26"', 'node = "9.9.9"'), encoding="utf-8"
        )
        code, out = self._run()
        self.assertEqual(code, 1)
        self.assertIn("node", out)
        self.assertIn("MISMATCH", out)

    def test_pnpm_drift_exits_1(self) -> None:
        # pnpm mirrors web/package.json's packageManager.
        (self.root / ".mise.toml").write_text(
            MISE.replace('pnpm = "12.21.0"', 'pnpm = "9.9.9"'), encoding="utf-8"
        )
        code, out = self._run()
        self.assertEqual(code, 1)
        self.assertIn("pnpm", out)
        self.assertIn("MISMATCH", out)

    def test_nvmrc_leading_v_is_normalised(self) -> None:
        # .nvmrc may carry a leading `v`; it still names the same version.
        (self.root / "web" / ".nvmrc").write_text("v26\n", encoding="utf-8")
        code, out = self._run()
        self.assertEqual(code, 0)
        self.assertIn("toolchain: ok          node:", out)

    def test_package_manager_may_carry_a_hash(self) -> None:
        # corepack allows `+<hash>` after the version; the pin is plain.
        (self.root / "web" / "package.json").write_text(
            '{"packageManager": "pnpm@12.21.0+sha512.deadbeef"}\n', encoding="utf-8"
        )
        code, out = self._run()
        self.assertEqual(code, 0)
        self.assertIn("toolchain: ok          pnpm:", out)

    def test_golang_image_drift_exits_1(self) -> None:
        # The golang image's tag version mirrors go.work's go directive; an
        # image left behind when the directive moves drifts.
        self._write_dockerfile(
            DOCKERFILE.replace("golang:1.27.4-bookworm", "golang:1.27.0-bookworm")
        )
        code, out = self._run()
        self.assertEqual(code, 1)
        self.assertIn("MISMATCH    golang image:", out)
        self.assertIn("golang:1.27.0-bookworm", out)

    def test_go_directive_moved_alone_drifts_against_the_image(self) -> None:
        # Moving go.work and .mise.toml together but not the image is drift
        # too: the image mirrors the same source.
        (self.root / "go.work").write_text("go 1.27.5\n", encoding="utf-8")
        (self.root / ".mise.toml").write_text(
            MISE.replace('go = "1.27.4"', 'go = "1.27.5"'), encoding="utf-8"
        )
        code, out = self._run()
        self.assertEqual(code, 1)
        self.assertIn("toolchain: ok          go:", out)
        self.assertIn("MISMATCH    golang image:", out)

    def test_golang_image_with_minor_only_tag_drifts(self) -> None:
        # The tag's version must equal the directive, not merely share its
        # minor: golang:1.27 resolves to whatever patch is newest.
        self._write_dockerfile(
            DOCKERFILE.replace("golang:1.27.4-bookworm", "golang:1.27-bookworm")
        )
        code, out = self._run()
        self.assertEqual(code, 1)
        self.assertIn("MISMATCH    golang image:", out)

    def test_every_golang_stage_is_checked(self) -> None:
        # A second stage on another golang tag drifts even though the first
        # one matches.
        self._write_dockerfile(
            DOCKERFILE + "FROM golang:1.26.10-bookworm AS tools\n"
        )
        code, out = self._run()
        self.assertEqual(code, 1)
        self.assertIn("toolchain: ok          golang image:", out)
        self.assertIn("MISMATCH    golang image:", out)

    def test_node_image_drift_exits_1(self) -> None:
        # The node image's major mirrors web/.nvmrc.
        self._write_dockerfile(
            DOCKERFILE.replace("node:26-bookworm-slim", "node:24-bookworm-slim")
        )
        code, out = self._run()
        self.assertEqual(code, 1)
        self.assertIn("MISMATCH    node image:", out)
        self.assertIn("node:24-bookworm-slim", out)

    def test_nvmrc_moved_alone_drifts_against_the_image(self) -> None:
        (self.root / "web" / ".nvmrc").write_text("27\n", encoding="utf-8")
        (self.root / ".mise.toml").write_text(
            MISE.replace('node = "26"', 'node = "27"'), encoding="utf-8"
        )
        code, out = self._run()
        self.assertEqual(code, 1)
        self.assertIn("toolchain: ok          node:", out)
        self.assertIn("MISMATCH    node image:", out)

    def test_node_image_full_version_matches_on_major(self) -> None:
        # A fully specified node tag is compared by its major.
        self._write_dockerfile(
            DOCKERFILE.replace("node:26-bookworm-slim", "node:26.13.1-bookworm-slim")
        )
        code, out = self._run()
        self.assertEqual(code, 0)
        self.assertIn("toolchain: ok          node image:", out)

    def test_from_flags_and_digest_are_read_through(self) -> None:
        # --platform and a pinned digest do not hide the tag.
        self._write_dockerfile(
            DOCKERFILE.replace(
                "FROM golang:1.27.4-bookworm",
                "FROM --platform=linux/amd64 docker.io/library/golang:1.26.10-bookworm@sha256:abc",
            )
        )
        code, out = self._run()
        self.assertEqual(code, 1)
        self.assertIn("golang:1.26.10-bookworm but", out)

    def test_runtime_image_is_not_checked(self) -> None:
        # The runtime base carries no toolchain version; moving it is not
        # drift.
        self._write_dockerfile(DOCKERFILE.replace("alpine:3.24", "alpine:9.9"))
        code, _ = self._run()
        self.assertEqual(code, 0)

    def test_missing_dockerfile_exits_2(self) -> None:
        (self.root / "starters" / "demo" / "Dockerfile").unlink()
        exc = self._run_expect_system_exit()
        self.assertEqual(exc.code, 2)

    def test_dockerfile_without_golang_image_exits_2(self) -> None:
        # A Dockerfile that no longer names the image has nothing to compare:
        # infrastructure, not drift.
        self._write_dockerfile(
            DOCKERFILE.replace("FROM golang:1.27.4-bookworm AS build\n", "")
        )
        exc = self._run_expect_system_exit()
        self.assertEqual(exc.code, 2)

    def test_dockerfile_without_node_image_exits_2(self) -> None:
        self._write_dockerfile(
            DOCKERFILE.replace("FROM node:26-bookworm-slim AS web\n", "")
        )
        exc = self._run_expect_system_exit()
        self.assertEqual(exc.code, 2)

    def test_missing_source_file_exits_2(self) -> None:
        # A missing source file is an infrastructure error: exit 2 (the
        # module docstring's contract). The reader must not fall through
        # sys.exit("error: ..."), which exits 1 like drift.
        (self.root / "go.work").unlink()
        exc = self._run_expect_system_exit()
        self.assertEqual(exc.code, 2)

    def test_missing_nvmrc_exits_2(self) -> None:
        (self.root / "web" / ".nvmrc").unlink()
        exc = self._run_expect_system_exit()
        self.assertEqual(exc.code, 2)

    def test_package_manager_not_pnpm_exits_2(self) -> None:
        # A packageManager naming another manager is an unparsable source
        # for the pnpm pin -- infrastructure, not drift.
        (self.root / "web" / "package.json").write_text(
            '{"packageManager": "yarn@4.0.0"}\n', encoding="utf-8"
        )
        exc = self._run_expect_system_exit()
        self.assertEqual(exc.code, 2)

    def test_missing_mise_exits_2(self) -> None:
        (self.root / ".mise.toml").unlink()
        exc = self._run_expect_system_exit()
        self.assertEqual(exc.code, 2)

    def test_absent_tool_exits_2(self) -> None:
        # An expected tool absent from .mise.toml is infrastructure
        # (nothing to compare a mirror against): exit 2.
        (self.root / ".mise.toml").write_text(
            MISE.replace('go = "1.27.4"\n', ""), encoding="utf-8"
        )
        code, out = self._run()
        self.assertEqual(code, 2)
        self.assertIn("go is absent from .mise.toml", out)

    def test_go_work_without_directive_exits_2(self) -> None:
        # A go.work that exists but carries no go directive is an
        # unparsable source: exit 2, not the drift code.
        (self.root / "go.work").write_text("use ./pkg/core\n", encoding="utf-8")
        exc = self._run_expect_system_exit()
        self.assertEqual(exc.code, 2)


if __name__ == "__main__":
    unittest.main()
