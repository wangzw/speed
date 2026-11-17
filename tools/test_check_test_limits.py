#!/usr/bin/env python3
"""Unit tests for check_test_limits.py's criteria and exit codes.

Stdlib-only (unittest + tempfile). Run directly:

    python3 tools/test_check_test_limits.py

Two rulings are pinned here, each with a case that would otherwise pass it.
A test may not derive a width from the host's core count -- `runtime.NumCPU`
under any `runtime` alias, `runtime.GOMAXPROCS(0)`, a negative argument or a
variable argument, `os.cpus` and `os.availableParallelism` under any import,
destructuring, inline-`require` or dynamic-`import` form,
`navigator.hardwareConcurrency`, and a `maxWorkers`/`minWorkers`/`workers`
percentage -- and a declared case or wait budget may not sit below the shared
default, read from `test.setTimeout`, `testInfo.setTimeout`,
`test.info().setTimeout`, `configure({ asyncUtilTimeout })`, an `it.each`
table or tagged template, and the usual options objects. The exceptions are
pinned too: saving and restoring the process width is not a probe (whatever
its argument; two saves that share a name inside one top-level function are
judged together, so a clean one is reported when its sibling escapes), a
fixed width or a range-table width is not a probe, a case budget of 0 means
no limit, a token named in a comment or a string is prose, an apostrophe
inside a regex
is not a string delimiter, a division is not a regex, an `it.each` table is
not its first argument read as a budget, and a wait that is not a Testing
Library query is not a case budget. The project's own `waitForCondition`
wrapper is the one exempt wait: it takes the larger of the caller's timeout
and the shared async budget, so its timeout is not a limit, and the gate
asserts that premise every run -- a wrapper that used the caller's value
directly, or a missing `wait.ts`, exits 2 instead of passing silently. The
shared defaults themselves are held to their floors, because a lowered
default would make every per-case limit pass; the floors live in their own
file, are read at a raised value, and a missing file or key, invalid JSON,
or a non-finite value is an infrastructure error rather than a silent pass.

Cases run over a scratch tree holding one clean Vitest package, its preset,
its floors file, its async budget, one Playwright spec and its config. The
last case scans this repository.
"""

from __future__ import annotations

import contextlib
import io
import os
import shutil
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)

sys.path.insert(0, HERE)

import check_test_limits as m  # noqa: E402

PRESET = """\
/** The workspace's one test budget. */
export const SHARED_TIMEOUT_MS = 60_000
"""

PRESET_WITH = """\
/** The workspace's one test budget. */
export const SHARED_TIMEOUT_MS = {default_ms}
"""

FLOORS = """\
{
  "vitestCaseTimeoutMs": 60000,
  "playwrightCaseTimeoutMs": 120000,
  "asyncUtilTimeoutMs": 30000
}
"""

FLOORS_WITH = """\
{
  "vitestCaseTimeoutMs": %s,
  "playwrightCaseTimeoutMs": 120000,
  "asyncUtilTimeoutMs": 30000
}
"""

PLAYWRIGHT_FLOORS_WITH = """\
{
  "vitestCaseTimeoutMs": 60000,
  "playwrightCaseTimeoutMs": %s,
  "asyncUtilTimeoutMs": 30000
}
"""

ASYNC_FLOORS_WITH = """\
{
  "vitestCaseTimeoutMs": 60000,
  "playwrightCaseTimeoutMs": 120000,
  "asyncUtilTimeoutMs": %s
}
"""

GATE_TIME = """\
/** The shared async wait budget. */
export const ASYNC_UTIL_TIMEOUT_MS = 30_000
"""

WAIT_CONDITION = """\
import { getConfig } from '@testing-library/react'
import { vi } from 'vitest'

export interface WaitForConditionOptions {
  readonly interval?: number
  readonly timeout?: number
}

export function waitForCondition<T>(
  callback: () => T | Promise<T>,
  options: WaitForConditionOptions = {},
): Promise<T> {
  return vi.waitFor(callback, {
    ...options,
    timeout: Math.max(options.timeout ?? 0, getConfig().asyncUtilTimeout),
  })
}
"""

VITEST_CASE = """\
import { describe, expect, it } from 'vitest'

describe('alpha', () => {
  it('adds', () => {
    expect(1 + 1).toBe(2)
  })
})
"""

PLAYWRIGHT_CONFIG = """\
import { defineConfig } from '@playwright/test'
import { scaledTimeout } from './fixtures/time-scale'

export default defineConfig({
  testDir: './specs',
  timeout: scaledTimeout(180_000),
  expect: { timeout: scaledTimeout(30_000) },
})
"""

PLAYWRIGHT_SPEC = """\
import { expect, test } from '@playwright/test'

test('alpha journey', async ({ page }) => {
  await expect(page).toHaveTitle(/alpha/)
})
"""


class GateCase(unittest.TestCase):
    """A scratch tree, written by the case and scanned by the gate."""

    def setUp(self) -> None:
        self.root = tempfile.mkdtemp(prefix="test-limits-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.write("web/vitest.shared.ts", PRESET)
        self.write("web/scripts/test-time-floors.json", FLOORS)
        self.write("web/test-utils/src/gate-time.ts", GATE_TIME)
        self.write("web/test-utils/src/wait.ts", WAIT_CONDITION)
        self.write("web/packages/alpha/src/alpha.test.ts", VITEST_CASE)
        self.write(
            "starters/demo/web/e2e/playwright.config.ts", PLAYWRIGHT_CONFIG
        )
        self.write(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts", PLAYWRIGHT_SPEC
        )

    def write(self, relative: str, text: str) -> None:
        path = os.path.join(self.root, relative)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(text)

    def run_gate(self, root: str | None = None) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            try:
                code = m.main(["--root", root if root is not None else self.root])
            except SystemExit as exc:
                code = exc.code if isinstance(exc.code, int) else 1
        return code, out.getvalue(), err.getvalue()

    def assert_clean(self) -> None:
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def assert_finding(self, path: str, *wanted: str) -> str:
        code, out, err = self.run_gate()
        self.assertEqual(1, code, f"stdout={out!r} stderr={err!r}")
        self.assertIn(path, out)
        for text in wanted:
            self.assertIn(text, out)
        return out


class TestCoreCountIsTheCallers(GateCase):
    def test_a_go_test_reading_num_cpu_is_a_finding(self) -> None:
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n"
            "\tworkers := runtime.NumCPU()\n"
            "\t_ = workers\n"
            "}\n",
        )
        self.assert_finding("pkg/alpha/alpha_test.go", "runtime.NumCPU", "GATE_JOBS")

    def test_gomaxprocs_taken_as_a_default_is_a_finding(self) -> None:
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n"
            "\tworkers := runtime.GOMAXPROCS(0)\n"
            "\t_ = workers\n"
            "}\n",
        )
        self.assert_finding("pkg/alpha/alpha_test.go", "runtime.GOMAXPROCS(0)")

    def test_a_saved_and_restored_width_is_not_a_default(self) -> None:
        # The governor test's idiom: read the current width only to restore
        # it, and set a width the test chooses in between.
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n"
            "\tprev := runtime.GOMAXPROCS(0)\n"
            "\tdefer runtime.GOMAXPROCS(prev)\n"
            "\truntime.GOMAXPROCS(5)\n"
            "}\n",
        )
        self.assert_clean()

    def test_a_saved_and_restored_width_with_a_positive_argument_is_clean(
        self,
    ) -> None:
        # `GOMAXPROCS(4)` sets a width the test chose and returns the previous
        # one; reading that return value only to restore it is the same idiom
        # as the `GOMAXPROCS(0)` form, so it must not be reported.
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n"
            "\told := runtime.GOMAXPROCS(4)\n"
            "\tdefer runtime.GOMAXPROCS(old)\n"
            "}\n",
        )
        self.assert_clean()

    def test_two_same_named_saved_widths_do_not_accuse_each_other(self) -> None:
        # Two functions in one file each save and restore their own `prev`.
        # The occurrences must be paired per save, not judged by name alone,
        # or each save's `prev` looks like a use of the other's.
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestOne(t *testing.T) {\n"
            "\tprev := runtime.GOMAXPROCS(0)\n"
            "\tdefer runtime.GOMAXPROCS(prev)\n"
            "}\n\n"
            "func TestTwo(t *testing.T) {\n"
            "\tprev := runtime.GOMAXPROCS(0)\n"
            "\tdefer runtime.GOMAXPROCS(prev)\n"
            "}\n",
        )
        self.assert_clean()

    def test_one_escaped_save_does_not_accuse_a_clean_same_named_save(
        self,
    ) -> None:
        # The first `prev` escapes into `t.Log`, so that save was a default.
        # The second `prev` is only saved, restored and never read beyond the
        # restore, so it is clean. Pairing by name outright would report both;
        # pairing within each function reports only the first (line 9).
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestOne(t *testing.T) {\n"
            "\tprev := runtime.GOMAXPROCS(0)\n"
            "\tdefer runtime.GOMAXPROCS(prev)\n"
            "\tt.Log(prev)\n"
            "}\n\n"
            "func TestTwo(t *testing.T) {\n"
            "\tprev := runtime.GOMAXPROCS(0)\n"
            "\tdefer runtime.GOMAXPROCS(prev)\n"
            "}\n",
        )
        out = self.assert_finding(
            "pkg/alpha/alpha_test.go", "runtime.GOMAXPROCS(0)"
        )
        self.assertIn("alpha_test.go:9:", out)
        self.assertNotIn("alpha_test.go:15:", out)
        self.assertEqual(1, out.count("runtime.GOMAXPROCS(0)"))

    def test_two_escaped_same_named_saves_are_both_findings(self) -> None:
        # Both saves escape into their own `t.Log`, so both reads were
        # defaults. A pairing that stopped at the first reported name would
        # miss the second (line 15).
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestOne(t *testing.T) {\n"
            "\tprev := runtime.GOMAXPROCS(0)\n"
            "\tdefer runtime.GOMAXPROCS(prev)\n"
            "\tt.Log(prev)\n"
            "}\n\n"
            "func TestTwo(t *testing.T) {\n"
            "\tprev := runtime.GOMAXPROCS(0)\n"
            "\tdefer runtime.GOMAXPROCS(prev)\n"
            "\tt.Log(prev)\n"
            "}\n",
        )
        out = self.assert_finding(
            "pkg/alpha/alpha_test.go", "runtime.GOMAXPROCS(0)"
        )
        self.assertIn("alpha_test.go:9:", out)
        self.assertIn("alpha_test.go:15:", out)
        self.assertEqual(2, out.count("runtime.GOMAXPROCS(0)"))

    def test_a_fixed_process_width_is_allowed(self) -> None:
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n"
            "\truntime.GOMAXPROCS(2)\n"
            "}\n",
        )
        self.assert_clean()

    def test_a_vitest_config_reading_cpus_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "import os from 'node:os'\n"
            "export default { test: { maxWorkers: os.cpus().length } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "os.cpus")

    def test_a_destructured_cpus_import_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "import { cpus } from 'node:os'\n"
            "export default { test: { maxWorkers: cpus().length } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "os.cpus")

    def test_a_playwright_spec_reading_available_parallelism_is_a_finding(self) -> None:
        spec = (
            "import os from 'node:os'\n"
            + PLAYWRIGHT_SPEC.replace(
                "test('alpha journey'",
                "const workers = os.availableParallelism()\n"
                "test('alpha journey'",
            )
        )
        self.write("starters/demo/web/e2e/specs/journey-alpha.spec.ts", spec)
        self.assert_finding(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts",
            "os.availableParallelism",
        )

    def test_a_browser_core_probe_is_a_finding(self) -> None:
        spec = PLAYWRIGHT_SPEC.replace(
            "test('alpha journey'",
            "const workers = navigator.hardwareConcurrency\n"
            "test('alpha journey'",
        )
        self.write("starters/demo/web/e2e/specs/journey-alpha.spec.ts", spec)
        self.assert_finding(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts",
            "navigator.hardwareConcurrency",
        )

    def test_a_token_in_a_comment_or_string_is_prose(self) -> None:
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport "testing"\n\n'
            "func TestProse(t *testing.T) {\n"
            "\t// runtime.NumCPU() would be a probe.\n"
            '\tt.Log("os.cpus() and GOMAXPROCS(0) are named here")\n'
            "}\n",
        )
        self.assert_clean()

    def test_a_go_test_support_file_that_imports_testing_is_scanned(self) -> None:
        # A package name that does not end in `test` (dbsuite, bussuite,
        # cutproxy, routefixture) still helps tests when it imports testing.
        self.write(
            "pkg/db/internal/dbsuite/dbsuite.go",
            'package dbsuite\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func Width(t *testing.T) int {\n\treturn runtime.NumCPU()\n}\n",
        )
        self.assert_finding("pkg/db/internal/dbsuite/dbsuite.go", "runtime.NumCPU")

    def test_a_go_test_helper_package_without_the_testing_import_is_scanned(
        self,
    ) -> None:
        # `coretest` is the Go convention's test-helper package: it helps
        # tests but imports no `testing`, so the union includes a package or
        # directory whose name ends in `test`.
        self.write(
            "pkg/core/coretest/width.go",
            'package coretest\n\nimport "runtime"\n\n'
            "func Width() int { return runtime.NumCPU() }\n",
        )
        self.assert_finding("pkg/core/coretest/width.go", "runtime.NumCPU")

    def test_a_package_name_ending_in_test_is_scanned_without_a_directory_rule(
        self,
    ) -> None:
        # The directory `helper` matches no rule; the package name `coretest`
        # alone puts this file in scope. An implementation that read only the
        # directory name would skip it.
        self.write(
            "pkg/core/helper/width.go",
            'package coretest\n\nimport "runtime"\n\n'
            "func Width() int { return runtime.NumCPU() }\n",
        )
        self.assert_finding("pkg/core/helper/width.go", "runtime.NumCPU")

    def test_a_directory_name_ending_in_test_is_scanned_without_a_package_rule(
        self,
    ) -> None:
        # The package `core` matches no rule; the directory `coretest` alone
        # puts this file in scope. An implementation that read only the
        # package name would skip it.
        self.write(
            "pkg/core/coretest/width.go",
            'package core\n\nimport "runtime"\n\n'
            "func Width() int { return runtime.NumCPU() }\n",
        )
        self.assert_finding("pkg/core/coretest/width.go", "runtime.NumCPU")

    def test_a_go_file_that_does_not_import_testing_is_out_of_scope(self) -> None:
        # A product file that is not `_test.go`, does not import `testing`,
        # and lives in neither a package nor a directory ending in `test` is
        # out of scope: product code chooses its own width.
        self.write(
            "pkg/alpha/runtime.go",
            'package alpha\n\nimport "runtime"\n\n'
            "func width() int { return runtime.NumCPU() }\n",
        )
        self.assert_clean()

    def test_a_test_utils_support_file_is_scanned(self) -> None:
        self.write(
            "web/test-utils/src/harness.ts",
            "import os from 'node:os'\n"
            "export const width = os.cpus().length\n",
        )
        self.assert_finding("web/test-utils/src/harness.ts", "os.cpus")

    def test_an_e2e_helper_file_is_scanned(self) -> None:
        self.write(
            "starters/demo/web/e2e/helpers/wait.ts",
            "import os from 'node:os'\n"
            "export const width = os.availableParallelism()\n",
        )
        self.assert_finding(
            "starters/demo/web/e2e/helpers/wait.ts", "os.availableParallelism"
        )

    def test_a_renamed_cpus_import_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "import { cpus as c } from 'node:os'\n"
            "export default { test: { maxWorkers: c().length } }\n",
        )
        self.assert_finding(
            "web/packages/beta/vitest.config.ts", "os.cpus (imported as c)"
        )

    def test_a_renamed_cpus_require_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "const { cpus: c } = require('os')\n"
            "export default { test: { maxWorkers: c().length } }\n",
        )
        self.assert_finding(
            "web/packages/beta/vitest.config.ts", "os.cpus (imported as c)"
        )

    def test_a_required_os_module_alias_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "const host = require('os')\n"
            "export default { test: { maxWorkers: host.cpus().length } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "os.cpus")

    def test_a_namespace_os_import_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "import * as host from 'node:os'\n"
            "export default { test: { maxWorkers: host.cpus().length } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "os.cpus")

    def test_an_inline_require_of_cpus_is_a_finding(self) -> None:
        # No module alias is bound, and the mask blanks the specifier, so the
        # binding scan cannot see this; the inline form is read on its own.
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default { test: { maxWorkers: require('os').cpus().length } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "os.cpus")

    def test_a_dynamic_import_of_cpus_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default async () => {\n"
            "  const { cpus } = await import('node:os')\n"
            "  return { test: { maxWorkers: cpus().length } }\n"
            "}\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "os.cpus")

    def test_a_dynamic_import_then_destructure_is_a_finding(self) -> None:
        # The destructuring happens inside `.then(({ ... }) => ...)`, which
        # binds no name the module scan can follow; the form is matched on
        # its own.
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default async () => {\n"
            "  const width = await import('node:os')\n"
            "    .then(({ availableParallelism }) => availableParallelism())\n"
            "  return { test: { maxWorkers: width } }\n"
            "}\n",
        )
        self.assert_finding(
            "web/packages/beta/vitest.config.ts", "os.availableParallelism"
        )

    def test_a_dynamic_import_member_read_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default async () => {\n"
            "  const width = (await import('node:os')).availableParallelism()\n"
            "  return { test: { maxWorkers: width } }\n"
            "}\n",
        )
        self.assert_finding(
            "web/packages/beta/vitest.config.ts", "os.availableParallelism"
        )

    def test_an_os_require_named_in_a_string_is_prose(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default { test: { name: \"require('os').cpus()\" } }\n",
        )
        self.assert_clean()

    def test_a_renamed_runtime_import_is_a_finding(self) -> None:
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\trt "runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n\t_ = rt.NumCPU()\n}\n",
        )
        self.assert_finding("pkg/alpha/alpha_test.go", "runtime.NumCPU")

    def test_gomaxprocs_with_a_negative_argument_is_a_finding(self) -> None:
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n"
            "\t_ = runtime.GOMAXPROCS(-1)\n"
            "}\n",
        )
        self.assert_finding("pkg/alpha/alpha_test.go", "runtime.GOMAXPROCS(0)")

    def test_gomaxprocs_with_a_variable_argument_is_a_finding(self) -> None:
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n"
            "\twidth := 4\n"
            "\truntime.GOMAXPROCS(width)\n"
            "}\n",
        )
        self.assert_finding("pkg/alpha/alpha_test.go", "runtime.GOMAXPROCS(width)")

    def test_a_positive_literal_save_used_beyond_restore_is_a_finding(self) -> None:
        # The save's argument is a positive literal, which the query scan does
        # not match, but the read value escapes as a worker count, so the
        # width was taken as a default. Without this report the positive
        # literal branch would be silent.
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n"
            "\told := runtime.GOMAXPROCS(4)\n"
            "\tdefer runtime.GOMAXPROCS(old)\n"
            "\tworkers := old\n"
            "\t_ = workers\n"
            "}\n",
        )
        self.assert_finding(
            "pkg/alpha/alpha_test.go", "used beyond the restore", "into old"
        )

    def test_a_saved_width_used_beyond_restore_is_a_finding(self) -> None:
        # The file contains a restore call, but the saved value also escapes
        # as a worker count, so the read was a default, not an idiom.
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n"
            "\tprev := runtime.GOMAXPROCS(0)\n"
            "\tdefer runtime.GOMAXPROCS(prev)\n"
            "\tworkers := prev\n"
            "\t_ = workers\n"
            "}\n",
        )
        self.assert_finding("pkg/alpha/alpha_test.go", "runtime.GOMAXPROCS(0)")

    def test_a_range_variable_width_is_the_tests_own_table(self) -> None:
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n"
            "\tprev := runtime.GOMAXPROCS(0)\n"
            "\tdefer runtime.GOMAXPROCS(prev)\n"
            "\tfor width := range map[int]int{1: 1, 4: 8} {\n"
            "\t\truntime.GOMAXPROCS(width)\n"
            "\t}\n"
            "}\n",
        )
        self.assert_clean()

    def test_a_vitest_worker_percentage_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default { test: { maxWorkers: '50%' } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "maxWorkers")

    def test_a_vitest_min_worker_percentage_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default { test: { minWorkers: '25%' } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "minWorkers")

    def test_a_playwright_worker_percentage_is_a_finding(self) -> None:
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            PLAYWRIGHT_CONFIG.replace(
                "testDir: './specs',", "testDir: './specs',\n  workers: '50%',"
            ),
        )
        self.assert_finding(
            "starters/demo/web/e2e/playwright.config.ts", "workers"
        )

    def test_a_worker_count_from_gate_jobs_is_clean(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default { test: { maxWorkers: process.env.GATE_JOBS } }\n",
        )
        self.assert_clean()

    def test_an_apostrophe_in_a_regex_is_not_a_string_delimiter(self) -> None:
        # An apostrophe inside a regex is masked with the regex, so a probe
        # named in a later case title stays prose rather than being read as
        # code.
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { expect, it } from 'vitest'\n"
            "it('throws', () => {\n"
            "  expect(() => f()).toThrow(/instance's/)\n"
            "})\n"
            "it('names os.cpus() in its title', () => {})\n",
        )
        self.assert_clean()


class TestCaseLimitsStayAtTheSharedDefault(GateCase):
    def test_a_vitest_option_budget_below_the_default_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "it('adds', () => {", "it('adds', { timeout: 5_000 }, () => {"
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms", "60000ms")

    def test_a_vitest_trailing_budget_below_the_default_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace("  })\n})", "  }, 5_000)\n})"),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_vitest_set_timeout_below_the_default_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "expect(1 + 1).toBe(2)",
                "test.setTimeout(1_000)\n    expect(1 + 1).toBe(2)",
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "1000ms")

    def test_a_multiline_case_names_the_line_of_its_budget(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it(\n"
            "  'adds',\n"
            "  { timeout: 5_000 },\n"
            "  () => {},\n"
            ")\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts:4", "5000ms")

    def test_an_it_each_case_budget_below_the_default_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it.each([[1]])('adds', { timeout: 5_000 }, () => {})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts:2", "5000ms")

    def test_a_vitest_budget_at_the_default_is_clean(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "it('adds', () => {", "it('adds', { timeout: 60_000 }, () => {"
            ),
        )
        self.assert_clean()

    def test_a_vitest_config_budget_below_the_default_is_a_finding(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default { test: { testTimeout: 5_000, hookTimeout: 5_000 } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "5000ms")

    def test_a_vitest_config_taking_the_shared_budget_is_clean(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "import { sharedTimeoutMs } from '../../vitest.shared.ts'\n"
            "export default { test: { testTimeout: sharedTimeoutMs() } }\n",
        )
        self.assert_clean()

    def test_a_testing_library_wait_below_the_async_default_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "expect(1 + 1).toBe(2)",
                "await waitFor(() => expect(1).toBe(1), { timeout: 500 })\n"
                "    expect(1 + 1).toBe(2)",
            ).replace("it('adds', () => {", "it('adds', async () => {"),
        )
        self.assert_finding(
            "web/packages/alpha/src/alpha.test.ts", "500ms", "30000ms"
        )

    def test_a_find_by_wait_below_the_async_default_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "expect(1 + 1).toBe(2)",
                "await screen.findByText('alpha', {}, { timeout: 3_000 })\n"
                "    expect(1 + 1).toBe(2)",
            ).replace("it('adds', () => {", "it('adds', async () => {"),
        )
        self.assert_finding(
            "web/packages/alpha/src/alpha.test.ts", "3000ms", "30000ms"
        )

    def test_a_wait_at_the_async_default_is_clean(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "expect(1 + 1).toBe(2)",
                "await screen.findByText('alpha', {}, { timeout: 30_000 })\n"
                "    expect(1 + 1).toBe(2)",
            ).replace("it('adds', () => {", "it('adds', async () => {"),
        )
        self.assert_clean()

    def test_a_wait_through_the_shared_async_budget_is_clean(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "expect(1 + 1).toBe(2)",
                "await waitFor(() => expect(1).toBe(1), {\n"
                "      timeout: asyncBudgetMs(gateTimeScale()),\n"
                "    })\n"
                "    expect(1 + 1).toBe(2)",
            ).replace("it('adds', () => {", "it('adds', async () => {"),
        )
        self.assert_clean()

    def test_an_e2e_helper_case_budget_is_a_finding(self) -> None:
        # A fixture or helper that narrows a bound narrows every case that
        # uses it, so the e2e support files are read, not skipped.
        self.write(
            "starters/demo/web/e2e/helpers/wait.ts",
            "import { test } from '@playwright/test'\n"
            "export function narrow() { test.setTimeout(1_000) }\n",
        )
        self.assert_finding(
            "starters/demo/web/e2e/helpers/wait.ts", "1000ms", "180000ms"
        )

    def test_a_playwright_case_budget_below_the_default_is_a_finding(self) -> None:
        self.write(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts",
            PLAYWRIGHT_SPEC.replace(
                "test('alpha journey', async ({ page }) => {",
                "test('alpha journey', async ({ page }) => {\n  test.setTimeout(1_000)",
            ),
        )
        self.assert_finding(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts", "1000ms", "180000ms"
        )

    def test_a_scaled_playwright_budget_below_the_default_is_a_finding(self) -> None:
        self.write(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts",
            PLAYWRIGHT_SPEC.replace(
                "test('alpha journey', async ({ page }) => {",
                "test('alpha journey', async ({ page }) => {\n"
                "  test.setTimeout(scaledTimeout(120_000))",
            ),
        )
        self.assert_finding(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts", "120000ms"
        )

    def test_a_widened_playwright_budget_is_clean(self) -> None:
        self.write(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts",
            PLAYWRIGHT_SPEC.replace(
                "test('alpha journey', async ({ page }) => {",
                "test('alpha journey', async ({ page }) => {\n"
                "  test.setTimeout(scaledTimeout(300_000))",
            ),
        )
        self.assert_clean()

    def test_a_playwright_describe_budget_below_the_default_is_a_finding(self) -> None:
        self.write(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts",
            "test.describe.configure({ timeout: scaledTimeout(60_000) })\n"
            + PLAYWRIGHT_SPEC,
        )
        self.assert_finding(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts", "60000ms"
        )

    def test_playwright_waits_and_retries_are_not_case_budgets(self) -> None:
        self.write(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts",
            PLAYWRIGHT_SPEC.replace(
                "await expect(page).toHaveTitle(/alpha/)",
                "await page.getByRole('button').click({ timeout: 1_000 })\n"
                "  await expect(async () => {\n"
                "    await expect(page).toHaveTitle(/alpha/)\n"
                "  }).toPass({ timeout: 2_000 })",
            ),
        )
        self.assert_clean()

    def test_a_quoted_budget_is_prose(self) -> None:
        self.write(
            "web/scripts/guard.test.mjs",
            "import { it } from 'vitest'\n"
            "it('explains the rule', () => {\n"
            "  // test.setTimeout(1_000) is what this rejects.\n"
            "  expect(\"it('x', { timeout: 5_000 })\").toBeDefined()\n"
            "})\n",
        )
        self.assert_clean()

    def test_a_trailing_scaled_budget_below_the_default_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "  })\n})", "  }, scaledTimeout(5_000))\n})"
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_an_arithmetic_budget_below_the_default_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "it('adds', () => {", "it('adds', { timeout: 5 * 1000 }, () => {"
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_an_arithmetic_budget_above_the_default_is_clean(self) -> None:
        # Read as 2*60_000 = 120_000, not as a bare 2.
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "it('adds', () => {", "it('adds', { timeout: 2 * 60_000 }, () => {"
            ),
        )
        self.assert_clean()

    def test_a_third_argument_options_budget_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => {}, { timeout: 5_000 })\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_vi_set_config_budget_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "expect(1 + 1).toBe(2)",
                "vi.setConfig({ testTimeout: 5_000 })\n    expect(1 + 1).toBe(2)",
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_test_info_set_timeout_budget_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "expect(1 + 1).toBe(2)",
                "testInfo.setTimeout(1_000)\n    expect(1 + 1).toBe(2)",
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "1000ms")

    def test_a_skip_if_chain_budget_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it.skipIf(false)('adds', { timeout: 5_000 }, () => {})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_test_for_chain_budget_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { test } from 'vitest'\n"
            "test.for([1])('adds', { timeout: 5_000 }, () => {})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_playwright_test_info_set_timeout_is_a_finding(self) -> None:
        self.write(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts",
            PLAYWRIGHT_SPEC.replace(
                "test('alpha journey', async ({ page }) => {",
                "test('alpha journey', async ({ page }, testInfo) => {\n"
                "  testInfo.setTimeout(1_000)",
            ),
        )
        self.assert_finding(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts", "1000ms"
        )

    def test_a_playwright_info_set_timeout_is_a_finding(self) -> None:
        # Playwright's `test.info()` returns the TestInfo, so this is the
        # same case bound as `testInfo.setTimeout` in another shape.
        self.write(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts",
            PLAYWRIGHT_SPEC.replace(
                "test('alpha journey', async ({ page }) => {",
                "test('alpha journey', async ({ page }) => {\n"
                "  test.info().setTimeout(1_000)",
            ),
        )
        self.assert_finding(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts", "1000ms"
        )

    def test_a_case_budget_of_zero_is_unlimited(self) -> None:
        # Both Vitest and Playwright read a case budget of 0 as no limit.
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', { timeout: 0 }, () => {})\n",
        )
        self.assert_clean()

    def test_a_vitest_set_timeout_of_zero_is_unlimited(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => { test.setTimeout(0) })\n",
        )
        self.assert_clean()

    def test_a_playwright_set_timeout_of_zero_is_unlimited(self) -> None:
        # Playwright reads a case budget of 0 as no limit too, so the same
        # widening passes on that side.
        self.write(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts",
            PLAYWRIGHT_SPEC.replace(
                "await expect(page).toHaveTitle(/alpha/)",
                "test.setTimeout(0)\n  await expect(page).toHaveTitle(/alpha/)",
            ),
        )
        self.assert_clean()

    def test_an_async_wait_timeout_of_zero_is_still_a_finding(self) -> None:
        # 0 on a wait is not "unlimited": a wait for a condition that returns
        # at once buys nothing, so it is still below the shared async default.
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', async () => { await waitFor(() => {}, { timeout: 0 }) })\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "0ms")

    def test_a_find_by_wait_timeout_of_zero_is_still_a_finding(self) -> None:
        # The zero exception belongs to case budgets only; a query that
        # returns at once is still below the shared async default.
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', async () => {\n"
            "  await screen.findByText('alpha', {}, { timeout: 0 })\n"
            "})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "0ms")

    def test_an_async_configure_timeout_is_a_finding(self) -> None:
        # A test that narrows Testing Library's async budget lowers the same
        # wait the setup widened, so it is judged against the async default.
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { configure } from '@testing-library/react'\n"
            "configure({ asyncUtilTimeout: 500 })\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "500ms")

    def test_an_async_configure_through_the_shared_budget_is_clean(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { configure } from '@testing-library/react'\n"
            "configure({ asyncUtilTimeout: asyncBudgetMs(gateTimeScale()) })\n",
        )
        self.assert_clean()

    def test_a_non_timeout_configure_key_is_clean(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { configure } from '@testing-library/react'\n"
            "configure({ reactStrictMode: true })\n",
        )
        self.assert_clean()

    def test_a_hook_trailing_budget_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { beforeEach } from 'vitest'\n"
            "beforeEach(async () => {}, 2_000)\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "2000ms")

    def test_a_run_if_chain_budget_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it.runIf(true)('adds', { timeout: 5_000 }, () => {})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_tagged_template_each_budget_is_a_finding(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it.each`a | b\n1 | 2`('adds', { timeout: 5_000 }, () => {})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_tagged_template_each_table_is_not_a_budget(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it.each`a | b\n1 | 2`('adds', () => {})\n",
        )
        self.assert_clean()

    def test_an_each_table_call_is_not_read_as_a_budget(self) -> None:
        # `range(5)` is the table, not a 5ms limit; only a recognised scaling
        # wrapper is peeled, so the table's first argument is not read.
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it.each(range(5))('adds', () => {})\n",
        )
        self.assert_clean()

    def test_a_division_budget_is_read_as_division(self) -> None:
        # `1000 / 60` is division, not a regex literal: a mask that read it as
        # a regex would blank the value and miss the 16ms budget.
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', { timeout: 1000 / 60 }, () => {})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "16ms")

    def test_division_shapes_are_not_masked_as_a_regex(self) -> None:
        for source in (
            "const t = a / b / c",
            "const t = (1) / 2",
            "const t = 1000 / 60",
        ):
            self.assertEqual(source, m.mask_javascript(source))

    def test_config_budgets_on_separate_lines_are_each_a_finding(self) -> None:
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default {\n"
            "  test: {\n"
            "    testTimeout: 5_000,\n"
            "    hookTimeout: 5_000,\n"
            "  },\n"
            "}\n",
        )
        out = self.assert_finding("web/packages/beta/vitest.config.ts", "5000ms")
        self.assertIn("vitest.config.ts:3", out)
        self.assertIn("vitest.config.ts:4", out)


class TestTheJavascriptMask(GateCase):
    """The hand-written mask reads JSX and fails closed at end of file.

    Each shape is a line a token scan can misread as an unterminated
    string, template or regex. The line below it is a wait that must still
    be seen: a mask that swallows the rest of the file passes the gate
    silently instead.
    """

    def assert_shape_reports(self, shape: str) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it, screen } from 'vitest'\n"
            "it('adds', async () => {\n"
            f"  {shape}"
            "  await screen.findByText('x', { timeout: 500 })\n"
            "})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "500ms")

    def test_jsx_self_closing_tag_with_attributes_keeps_scanning(self) -> None:
        self.assert_shape_reports("render(<A p={x} />);\n")

    def test_jsx_self_closing_after_an_identifier_keeps_scanning(self) -> None:
        self.assert_shape_reports("render(<Br />);\n")

    def test_jsx_self_closing_after_a_quoted_attribute_keeps_scanning(self) -> None:
        self.assert_shape_reports('render(<A p="x" />);\n')

    def test_a_regex_starting_with_a_self_closing_slash_is_masked(self) -> None:
        # `.replace(/>/g, ...)` is the shape in the demo's SAML fixture. The
        # slash after `(` opens a regex, so its `>` is masked with the regex
        # body. Reading `/>` as a tag end instead leaves the `>` in the mask
        # and lets the scan fall through to the next slash.
        self.assertEqual(
            ".replace(/ /g, '    ')\n",
            m.mask_javascript(".replace(/>/g, '&gt;')\n"),
        )

    def test_the_saml_replace_shape_keeps_scanning(self) -> None:
        self.assert_shape_reports("value.replace(/>/g, '&gt;');\n")

    def test_jsx_text_apostrophe_keeps_scanning(self) -> None:
        self.assert_shape_reports("render(<p>it's here</p>);\n")

    def test_jsx_closing_tag_before_a_template_keeps_scanning(self) -> None:
        self.assert_shape_reports("render(<Link>a</Link>); f(`/a/${id}`);\n")

    def test_jsx_self_closing_before_a_template_keeps_scanning(self) -> None:
        self.assert_shape_reports("render(<A p={x} />); f(`/a/${id}`);\n")

    def test_a_regex_after_an_if_header_keeps_scanning(self) -> None:
        self.assert_shape_reports("if (x) /`/.test(s);\n")

    def test_a_regex_after_a_comparison_slash_keeps_scanning(self) -> None:
        self.assert_shape_reports("x</`/.test(s);\n")

    def test_an_unclosed_construct_raises_rather_than_blanking(self) -> None:
        for source in (
            "const s = 'unclosed\n",
            'const s = "unclosed\n',
            "const s = `unclosed\n  still open\n",
            "const re = /unclosed",
            "/* unclosed\n",
        ):
            with self.subTest(source=source):
                with self.assertRaises(m.UnterminatedSource):
                    m.mask_javascript(source)

    def test_an_unclosed_template_exits_2_naming_the_file(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => {\n"
            "  const s = `unclosed\n"
            "})\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(2, code, f"stdout={out!r} stderr={err!r}")
        self.assertIn("web/packages/alpha/src/alpha.test.ts", err)


class TestTheWaitConditionExemption(GateCase):
    """The wrapper takes the larger value, so its timeout is not a limit.

    `waitForCondition` passes `Math.max(options.timeout ?? 0,
    asyncUtilTimeout)`, so a caller's 0 or 500 cannot lower the budget. The
    gate passes such a call, keeps reporting the raw Testing Library and
    `vi.waitFor` forms, and refuses to run (exit 2) when the premise no
    longer holds or the wrapper's file is missing.
    """

    def test_a_wait_for_condition_timeout_of_zero_passes(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "import { waitForCondition } from '../../../test-utils/src/wait'\n"
            "it('adds', async () => {\n"
            "  await waitForCondition(() => {}, { timeout: 0 })\n"
            "})\n",
        )
        self.assert_clean()

    def test_a_wait_for_condition_timeout_below_the_default_passes(self) -> None:
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "import { waitForCondition } from '../../../test-utils/src/wait'\n"
            "it('adds', async () => {\n"
            "  await waitForCondition(() => {}, { timeout: 500 })\n"
            "})\n",
        )
        self.assert_clean()

    def test_a_vi_wait_for_timeout_of_zero_is_still_a_finding(self) -> None:
        # `vi.waitFor` has no wrapper in front of it, so its 0 is a wait that
        # returns at once, not a widening.
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it, vi } from 'vitest'\n"
            "it('adds', async () => { await vi.waitFor(() => {}, { timeout: 0 }) })\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "0ms")

    def test_a_wrapper_using_the_callers_value_directly_exits_2(self) -> None:
        self.write(
            "web/test-utils/src/wait.ts",
            WAIT_CONDITION.replace(
                "timeout: Math.max(options.timeout ?? 0, "
                "getConfig().asyncUtilTimeout),",
                "timeout: options.timeout ?? 0,",
            ),
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("premise", err)
        self.assertIn("waitForCondition", err)
        self.assertIn("must", err)

    def test_a_missing_wait_condition_file_exits_2(self) -> None:
        os.remove(
            os.path.join(self.root, "web", "test-utils", "src", "wait.ts")
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("wait.ts", err)
        self.assertIn("cannot be checked", err)

    def test_an_unreadable_wait_condition_file_exits_2(self) -> None:
        # A file that exists but cannot be decoded is the same premise
        # failure as a missing one: the gate must not scan past it.
        path = os.path.join(self.root, "web", "test-utils", "src", "wait.ts")
        with open(path, "wb") as handle:
            handle.write(b"\xff\xfe\x00")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("wait.ts", err)
        self.assertIn("cannot be read", err)

    def test_the_premise_check_reads_the_wrappers_body(self) -> None:
        # A Math.max elsewhere in the file does not stand for the wrapper's
        # own; the function body is what the check reads.
        self.assertTrue(m.wait_condition_takes_larger_timeout(WAIT_CONDITION))
        self.assertFalse(
            m.wait_condition_takes_larger_timeout(
                WAIT_CONDITION.replace(
                    "timeout: Math.max(options.timeout ?? 0, "
                    "getConfig().asyncUtilTimeout),",
                    "timeout: options.timeout ?? 0,",
                )
                + "\nexport const unrelated = Math.max(a, b, asyncUtilTimeout)\n"
            )
        )


class TestTheSharedDefaultsHoldTheirFloors(GateCase):
    def test_a_lowered_vitest_default_is_a_finding(self) -> None:
        self.write("web/vitest.shared.ts", PRESET_WITH.format(default_ms="5_000"))
        self.assert_finding("web/vitest.shared.ts", "5000ms", "60000ms")

    def test_the_vitest_floor_comes_from_the_floors_file(self) -> None:
        # Raise the floor above a default that clears the file's current
        # 60_000. An implementation that read a fixed floor instead of the
        # file would pass it.
        self.write(
            "web/scripts/test-time-floors.json",
            FLOORS_WITH % "90000",
        )
        self.write("web/vitest.shared.ts", PRESET_WITH.format(default_ms="80_000"))
        self.assert_finding("web/vitest.shared.ts", "80000ms", "90000ms")

    def test_a_raised_default_is_what_case_budgets_are_judged_against(self) -> None:
        # The preset is raised to 90_000, so an 80_000 case budget is now
        # below it. An implementation that judged against a hardcoded 60_000
        # would pass this case.
        self.write("web/vitest.shared.ts", PRESET_WITH.format(default_ms="90_000"))
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "it('adds', () => {", "it('adds', { timeout: 80_000 }, () => {"
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "80000ms", "90000ms")

    def test_a_raised_default_still_passes_a_budget_above_it(self) -> None:
        self.write("web/vitest.shared.ts", PRESET_WITH.format(default_ms="90_000"))
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "it('adds', () => {", "it('adds', { timeout: 120_000 }, () => {"
            ),
        )
        self.assert_clean()

    def test_the_playwright_floor_comes_from_the_floors_file(self) -> None:
        # Raise the floor above a default that clears the file's current
        # 120_000. An implementation that read a fixed floor instead of the
        # file would pass it.
        self.write(
            "web/scripts/test-time-floors.json",
            PLAYWRIGHT_FLOORS_WITH % "200000",
        )
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            PLAYWRIGHT_CONFIG.replace("scaledTimeout(180_000)", "scaledTimeout(150_000)"),
        )
        self.assert_finding(
            "starters/demo/web/e2e/playwright.config.ts", "150000ms", "200000ms"
        )

    def test_the_async_floor_comes_from_the_floors_file(self) -> None:
        # Raise the async floor above the default that clears the file's
        # current 30_000. An implementation with a fixed 30_000 floor would
        # pass it.
        self.write("web/scripts/test-time-floors.json", ASYNC_FLOORS_WITH % "40000")
        self.assert_finding("web/test-utils/src/gate-time.ts", "30000ms", "40000ms")

    def test_a_lowered_playwright_default_is_a_finding(self) -> None:
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            PLAYWRIGHT_CONFIG.replace("scaledTimeout(180_000)", "scaledTimeout(60_000)"),
        )
        self.assert_finding(
            "starters/demo/web/e2e/playwright.config.ts", "60000ms", "120000ms"
        )

    def test_the_playwright_default_is_read_at_any_indentation(self) -> None:
        # Four-space properties are still the top-level object; the nested
        # `expect` timeout is at a deeper brace depth and is not the default.
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            "import { defineConfig } from '@playwright/test'\n"
            "export default defineConfig({\n"
            "    testDir: './specs',\n"
            "    timeout: 180_000,\n"
            "    expect: { timeout: 5_000 },\n"
            "})\n",
        )
        self.assert_clean()

    def test_a_nested_only_timeout_is_not_the_default(self) -> None:
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            "export default defineConfig({\n"
            "    testDir: './specs',\n"
            "    expect: {\n"
            "        timeout: 30_000,\n"
            "    },\n"
            "})\n",
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("no top-level timeout", err)


class TestExitCodes(GateCase):
    def test_a_root_that_is_not_a_directory_exits_2(self) -> None:
        code, _, err = self.run_gate(os.path.join(self.root, "nope"))
        self.assertEqual(2, code)
        self.assertIn("not a directory", err)

    def test_a_tree_with_no_test_file_exits_2(self) -> None:
        empty = tempfile.mkdtemp(prefix="test-limits-empty-")
        self.addCleanup(shutil.rmtree, empty, ignore_errors=True)
        code, _, err = self.run_gate(empty)
        self.assertEqual(2, code)
        self.assertIn("nothing was checked", err)

    def test_a_vitest_tree_without_the_preset_exits_2(self) -> None:
        os.remove(os.path.join(self.root, "web", "vitest.shared.ts"))
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("vitest.shared.ts", err)

    def test_a_vitest_tree_without_the_gate_time_default_exits_2(self) -> None:
        os.remove(
            os.path.join(self.root, "web", "test-utils", "src", "gate-time.ts")
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("gate-time.ts", err)

    def test_a_vitest_tree_without_the_floors_file_exits_2(self) -> None:
        os.remove(os.path.join(self.root, "web", "scripts", "test-time-floors.json"))
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("test-time-floors.json", err)

    def test_a_floors_file_without_a_key_exits_2(self) -> None:
        self.write(
            "web/scripts/test-time-floors.json",
            '{"playwrightCaseTimeoutMs": 120000}\n',
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("vitestCaseTimeoutMs", err)

    def test_a_floors_file_without_the_async_key_exits_2(self) -> None:
        self.write(
            "web/scripts/test-time-floors.json",
            '{"vitestCaseTimeoutMs": 60000, "playwrightCaseTimeoutMs": 120000}\n',
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("asyncUtilTimeoutMs", err)

    def test_a_nan_floor_exits_2(self) -> None:
        # `json` parses the NaN literal into a float that no comparison
        # rejects, so the value must be refused before it is converted.
        self.write(
            "web/scripts/test-time-floors.json",
            '{"vitestCaseTimeoutMs": NaN, "playwrightCaseTimeoutMs": 120000, '
            '"asyncUtilTimeoutMs": 30000}\n',
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("vitestCaseTimeoutMs", err)

    def test_an_infinite_floor_exits_2(self) -> None:
        self.write(
            "web/scripts/test-time-floors.json",
            '{"vitestCaseTimeoutMs": 60000, "playwrightCaseTimeoutMs": 120000, '
            '"asyncUtilTimeoutMs": 1e999}\n',
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("asyncUtilTimeoutMs", err)

    def test_a_floors_file_that_is_not_json_exits_2(self) -> None:
        self.write("web/scripts/test-time-floors.json", "{ not json\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("not readable JSON", err)

    def test_a_playwright_tree_without_the_config_exits_2(self) -> None:
        os.remove(
            os.path.join(
                self.root, "starters", "demo", "web", "e2e", "playwright.config.ts"
            )
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("playwright.config.ts", err)

    def test_a_playwright_config_without_a_default_exits_2(self) -> None:
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            "export default defineConfig({ testDir: './specs' })\n",
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("no top-level timeout", err)


class TestTheRealRepository(unittest.TestCase):
    def test_every_test_and_test_configuration_obeys_the_rulings(self) -> None:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", REPO])
        self.assertEqual(
            0, code, f"stdout={out.getvalue()!r} stderr={err.getvalue()!r}"
        )


if __name__ == "__main__":
    unittest.main()
