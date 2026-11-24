#!/usr/bin/env python3
"""Unit tests for check_test_limits.py's criteria and exit codes."""

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
        """Test that a go test reading num cpu is a finding."""
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
        """Test that gomaxprocs taken as a default is a finding."""
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
        """Test that a saved and restored width is not a default."""
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
        """Test that a saved and restored width with a positive argument is clean."""
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
        """Test that two same named saved widths do not accuse each other."""
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
        """Test that one escaped save does not accuse a clean same named save."""
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
        """Test that two escaped same named saves are both findings."""
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
        """Test that a fixed process width is allowed."""
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n"
            "\truntime.GOMAXPROCS(2)\n"
            "}\n",
        )
        self.assert_clean()

    def test_a_vitest_config_reading_cpus_is_a_finding(self) -> None:
        """Test that a vitest config reading cpus is a finding."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "import os from 'node:os'\n"
            "export default { test: { maxWorkers: os.cpus().length } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "os.cpus")

    def test_a_destructured_cpus_import_is_a_finding(self) -> None:
        """Test that a destructured cpus import is a finding."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "import { cpus } from 'node:os'\n"
            "export default { test: { maxWorkers: cpus().length } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "os.cpus")

    def test_a_playwright_spec_reading_available_parallelism_is_a_finding(self) -> None:
        """Test that a playwright spec reading available parallelism is a finding."""
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
        """Test that a browser core probe is a finding."""
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
        """Test that a token in a comment or string is prose."""
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
        """Test that a go test support file that imports testing is scanned."""
        self.write(
            "pkg/db/internal/dbsuite/dbsuite.go",
            'package dbsuite\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func Width(t *testing.T) int {\n\treturn runtime.NumCPU()\n}\n",
        )
        self.assert_finding("pkg/db/internal/dbsuite/dbsuite.go", "runtime.NumCPU")

    def test_a_go_test_helper_package_without_the_testing_import_is_scanned(
        self,
    ) -> None:
        """Test that a go test helper package without the testing import is scanned."""
        self.write(
            "pkg/core/coretest/width.go",
            'package coretest\n\nimport "runtime"\n\n'
            "func Width() int { return runtime.NumCPU() }\n",
        )
        self.assert_finding("pkg/core/coretest/width.go", "runtime.NumCPU")

    def test_a_package_name_ending_in_test_is_scanned_without_a_directory_rule(
        self,
    ) -> None:
        """Test that a package name ending in test is scanned without a directory rule."""
        self.write(
            "pkg/core/helper/width.go",
            'package coretest\n\nimport "runtime"\n\n'
            "func Width() int { return runtime.NumCPU() }\n",
        )
        self.assert_finding("pkg/core/helper/width.go", "runtime.NumCPU")

    def test_a_directory_name_ending_in_test_is_scanned_without_a_package_rule(
        self,
    ) -> None:
        """Test that a directory name ending in test is scanned without a package rule."""
        self.write(
            "pkg/core/coretest/width.go",
            'package core\n\nimport "runtime"\n\n'
            "func Width() int { return runtime.NumCPU() }\n",
        )
        self.assert_finding("pkg/core/coretest/width.go", "runtime.NumCPU")

    def test_a_go_file_that_does_not_import_testing_is_out_of_scope(self) -> None:
        """Test that a go file that does not import testing is out of scope."""
        self.write(
            "pkg/alpha/runtime.go",
            'package alpha\n\nimport "runtime"\n\n'
            "func width() int { return runtime.NumCPU() }\n",
        )
        self.assert_clean()

    def test_a_test_utils_support_file_is_scanned(self) -> None:
        """Test that a test utils support file is scanned."""
        self.write(
            "web/test-utils/src/harness.ts",
            "import os from 'node:os'\n"
            "export const width = os.cpus().length\n",
        )
        self.assert_finding("web/test-utils/src/harness.ts", "os.cpus")

    def test_an_e2e_helper_file_is_scanned(self) -> None:
        """Test that an e2e helper file is scanned."""
        self.write(
            "starters/demo/web/e2e/helpers/wait.ts",
            "import os from 'node:os'\n"
            "export const width = os.availableParallelism()\n",
        )
        self.assert_finding(
            "starters/demo/web/e2e/helpers/wait.ts", "os.availableParallelism"
        )

    def test_a_renamed_cpus_import_is_a_finding(self) -> None:
        """Test that a renamed cpus import is a finding."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "import { cpus as c } from 'node:os'\n"
            "export default { test: { maxWorkers: c().length } }\n",
        )
        self.assert_finding(
            "web/packages/beta/vitest.config.ts", "os.cpus (imported as c)"
        )

    def test_a_renamed_cpus_require_is_a_finding(self) -> None:
        """Test that a renamed cpus require is a finding."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "const { cpus: c } = require('os')\n"
            "export default { test: { maxWorkers: c().length } }\n",
        )
        self.assert_finding(
            "web/packages/beta/vitest.config.ts", "os.cpus (imported as c)"
        )

    def test_a_required_os_module_alias_is_a_finding(self) -> None:
        """Test that a required os module alias is a finding."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "const host = require('os')\n"
            "export default { test: { maxWorkers: host.cpus().length } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "os.cpus")

    def test_a_namespace_os_import_is_a_finding(self) -> None:
        """Test that a namespace os import is a finding."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "import * as host from 'node:os'\n"
            "export default { test: { maxWorkers: host.cpus().length } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "os.cpus")

    def test_an_inline_require_of_cpus_is_a_finding(self) -> None:
        """Test that an inline require of cpus is a finding."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default { test: { maxWorkers: require('os').cpus().length } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "os.cpus")

    def test_a_dynamic_import_of_cpus_is_a_finding(self) -> None:
        """Test that a dynamic import of cpus is a finding."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default async () => {\n"
            "  const { cpus } = await import('node:os')\n"
            "  return { test: { maxWorkers: cpus().length } }\n"
            "}\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "os.cpus")

    def test_a_dynamic_import_then_destructure_is_a_finding(self) -> None:
        """Test that a dynamic import then destructure is a finding."""
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
        """Test that a dynamic import member read is a finding."""
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
        """Test that an os require named in a string is prose."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default { test: { name: \"require('os').cpus()\" } }\n",
        )
        self.assert_clean()

    def test_a_renamed_runtime_import_is_a_finding(self) -> None:
        """Test that a renamed runtime import is a finding."""
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\trt "runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n\t_ = rt.NumCPU()\n}\n",
        )
        self.assert_finding("pkg/alpha/alpha_test.go", "runtime.NumCPU")

    def test_gomaxprocs_with_a_negative_argument_is_a_finding(self) -> None:
        """Test that gomaxprocs with a negative argument is a finding."""
        self.write(
            "pkg/alpha/alpha_test.go",
            'package alpha\n\nimport (\n\t"runtime"\n\t"testing"\n)\n\n'
            "func TestWidth(t *testing.T) {\n"
            "\t_ = runtime.GOMAXPROCS(-1)\n"
            "}\n",
        )
        self.assert_finding("pkg/alpha/alpha_test.go", "runtime.GOMAXPROCS(0)")

    def test_gomaxprocs_with_a_variable_argument_is_a_finding(self) -> None:
        """Test that gomaxprocs with a variable argument is a finding."""
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
        """Test that a positive literal save used beyond restore is a finding."""
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
        """Test that a saved width used beyond restore is a finding."""
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
        """Test that a range variable width is the tests own table."""
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
        """Test that a vitest worker percentage is a finding."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default { test: { maxWorkers: '50%' } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "maxWorkers")

    def test_a_vitest_min_worker_percentage_is_a_finding(self) -> None:
        """Test that a vitest min worker percentage is a finding."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default { test: { minWorkers: '25%' } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "minWorkers")

    def test_a_playwright_worker_percentage_is_a_finding(self) -> None:
        """Test that a playwright worker percentage is a finding."""
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
        """Test that a worker count from gate jobs is clean."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default { test: { maxWorkers: process.env.GATE_JOBS } }\n",
        )
        self.assert_clean()

    def test_an_apostrophe_in_a_regex_is_not_a_string_delimiter(self) -> None:
        """Test that an apostrophe in a regex is not a string delimiter."""
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
        """Test that a vitest option budget below the default is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "it('adds', () => {", "it('adds', { timeout: 5_000 }, () => {"
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms", "60000ms")

    def test_a_vitest_trailing_budget_below_the_default_is_a_finding(self) -> None:
        """Test that a vitest trailing budget below the default is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace("  })\n})", "  }, 5_000)\n})"),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_vitest_set_timeout_below_the_default_is_a_finding(self) -> None:
        """Test that a vitest set timeout below the default is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "expect(1 + 1).toBe(2)",
                "test.setTimeout(1_000)\n    expect(1 + 1).toBe(2)",
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "1000ms")

    def test_a_multiline_case_names_the_line_of_its_budget(self) -> None:
        """Test that a multiline case names the line of its budget."""
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
        """Test that an it each case budget below the default is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it.each([[1]])('adds', { timeout: 5_000 }, () => {})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts:2", "5000ms")

    def test_a_vitest_budget_at_the_default_is_clean(self) -> None:
        """Test that a vitest budget at the default is clean."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "it('adds', () => {", "it('adds', { timeout: 60_000 }, () => {"
            ),
        )
        self.assert_clean()

    def test_a_vitest_config_budget_below_the_default_is_a_finding(self) -> None:
        """Test that a vitest config budget below the default is a finding."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "export default { test: { testTimeout: 5_000, hookTimeout: 5_000 } }\n",
        )
        self.assert_finding("web/packages/beta/vitest.config.ts", "5000ms")

    def test_a_vitest_config_taking_the_shared_budget_is_clean(self) -> None:
        """Test that a vitest config taking the shared budget is clean."""
        self.write(
            "web/packages/beta/vitest.config.ts",
            "import { sharedTimeoutMs } from '../../vitest.shared.ts'\n"
            "export default { test: { testTimeout: sharedTimeoutMs() } }\n",
        )
        self.assert_clean()

    def test_a_testing_library_wait_below_the_async_default_is_a_finding(self) -> None:
        """Test that a testing library wait below the async default is a finding."""
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
        """Test that a find by wait below the async default is a finding."""
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
        """Test that a wait at the async default is clean."""
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
        """Test that a wait through the shared async budget is clean."""
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
        """Test that an e2e helper case budget is a finding."""
        self.write(
            "starters/demo/web/e2e/helpers/wait.ts",
            "import { test } from '@playwright/test'\n"
            "export function narrow() { test.setTimeout(1_000) }\n",
        )
        self.assert_finding(
            "starters/demo/web/e2e/helpers/wait.ts", "1000ms", "180000ms"
        )

    def test_a_playwright_case_budget_below_the_default_is_a_finding(self) -> None:
        """Test that a playwright case budget below the default is a finding."""
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
        """Test that a scaled playwright budget below the default is a finding."""
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
        """Test that a widened playwright budget is clean."""
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
        """Test that a playwright describe budget below the default is a finding."""
        self.write(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts",
            "test.describe.configure({ timeout: scaledTimeout(60_000) })\n"
            + PLAYWRIGHT_SPEC,
        )
        self.assert_finding(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts", "60000ms"
        )

    def test_playwright_waits_and_retries_are_not_case_budgets(self) -> None:
        """Test that playwright waits and retries are not case budgets."""
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
        """Test that a quoted budget is prose."""
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
        """Test that a trailing scaled budget below the default is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "  })\n})", "  }, scaledTimeout(5_000))\n})"
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_an_arithmetic_budget_below_the_default_is_a_finding(self) -> None:
        """Test that an arithmetic budget below the default is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "it('adds', () => {", "it('adds', { timeout: 5 * 1000 }, () => {"
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_an_arithmetic_budget_above_the_default_is_clean(self) -> None:
        """Test that an arithmetic budget above the default is clean."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "it('adds', () => {", "it('adds', { timeout: 2 * 60_000 }, () => {"
            ),
        )
        self.assert_clean()

    def test_a_third_argument_options_budget_is_a_finding(self) -> None:
        """Test that a third argument options budget is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => {}, { timeout: 5_000 })\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_vi_set_config_budget_is_a_finding(self) -> None:
        """Test that a vi set config budget is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "expect(1 + 1).toBe(2)",
                "vi.setConfig({ testTimeout: 5_000 })\n    expect(1 + 1).toBe(2)",
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_test_info_set_timeout_budget_is_a_finding(self) -> None:
        """Test that a test info set timeout budget is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "expect(1 + 1).toBe(2)",
                "testInfo.setTimeout(1_000)\n    expect(1 + 1).toBe(2)",
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "1000ms")

    def test_a_skip_if_chain_budget_is_a_finding(self) -> None:
        """Test that a skip if chain budget is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it.skipIf(false)('adds', { timeout: 5_000 }, () => {})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_test_for_chain_budget_is_a_finding(self) -> None:
        """Test that a test for chain budget is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { test } from 'vitest'\n"
            "test.for([1])('adds', { timeout: 5_000 }, () => {})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_playwright_test_info_set_timeout_is_a_finding(self) -> None:
        """Test that a playwright test info set timeout is a finding."""
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
        """Test that a playwright info set timeout is a finding."""
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
        """Test that a case budget of zero is unlimited."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', { timeout: 0 }, () => {})\n",
        )
        self.assert_clean()

    def test_a_vitest_set_timeout_of_zero_is_unlimited(self) -> None:
        """Test that a vitest set timeout of zero is unlimited."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => { test.setTimeout(0) })\n",
        )
        self.assert_clean()

    def test_a_playwright_set_timeout_of_zero_is_unlimited(self) -> None:
        """Test that a playwright set timeout of zero is unlimited."""
        self.write(
            "starters/demo/web/e2e/specs/journey-alpha.spec.ts",
            PLAYWRIGHT_SPEC.replace(
                "await expect(page).toHaveTitle(/alpha/)",
                "test.setTimeout(0)\n  await expect(page).toHaveTitle(/alpha/)",
            ),
        )
        self.assert_clean()

    def test_an_async_wait_timeout_of_zero_is_still_a_finding(self) -> None:
        """Test that an async wait timeout of zero is still a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', async () => { await waitFor(() => {}, { timeout: 0 }) })\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "0ms")

    def test_a_find_by_wait_timeout_of_zero_is_still_a_finding(self) -> None:
        """Test that a find by wait timeout of zero is still a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', async () => {\n"
            "  await screen.findByText('alpha', {}, { timeout: 0 })\n"
            "})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "0ms")

    def test_an_async_configure_timeout_is_a_finding(self) -> None:
        """Test that an async configure timeout is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { configure } from '@testing-library/react'\n"
            "configure({ asyncUtilTimeout: 500 })\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "500ms")

    def test_an_async_configure_through_the_shared_budget_is_clean(self) -> None:
        """Test that an async configure through the shared budget is clean."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { configure } from '@testing-library/react'\n"
            "configure({ asyncUtilTimeout: asyncBudgetMs(gateTimeScale()) })\n",
        )
        self.assert_clean()

    def test_a_non_timeout_configure_key_is_clean(self) -> None:
        """Test that a non timeout configure key is clean."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { configure } from '@testing-library/react'\n"
            "configure({ reactStrictMode: true })\n",
        )
        self.assert_clean()

    def test_a_hook_trailing_budget_is_a_finding(self) -> None:
        """Test that a hook trailing budget is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { beforeEach } from 'vitest'\n"
            "beforeEach(async () => {}, 2_000)\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "2000ms")

    def test_a_run_if_chain_budget_is_a_finding(self) -> None:
        """Test that a run if chain budget is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it.runIf(true)('adds', { timeout: 5_000 }, () => {})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_tagged_template_each_budget_is_a_finding(self) -> None:
        """Test that a tagged template each budget is a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it.each`a | b\n1 | 2`('adds', { timeout: 5_000 }, () => {})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "5000ms")

    def test_a_tagged_template_each_table_is_not_a_budget(self) -> None:
        """Test that a tagged template each table is not a budget."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it.each`a | b\n1 | 2`('adds', () => {})\n",
        )
        self.assert_clean()

    def test_an_each_table_call_is_not_read_as_a_budget(self) -> None:
        """Test that an each table call is not read as a budget."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it.each(range(5))('adds', () => {})\n",
        )
        self.assert_clean()

    def test_a_division_budget_is_read_as_division(self) -> None:
        """Test that a division budget is read as division."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', { timeout: 1000 / 60 }, () => {})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "16ms")

    def test_division_shapes_are_not_masked_as_a_regex(self) -> None:
        """Test that division shapes are not masked as a regex."""
        for source in (
            "const t = a / b / c",
            "const t = (1) / 2",
            "const t = 1000 / 60",
        ):
            self.assertEqual(source, m.mask_javascript(source))

    def test_config_budgets_on_separate_lines_are_each_a_finding(self) -> None:
        """Test that config budgets on separate lines are each a finding."""
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
        """Test that jsx self closing tag with attributes keeps scanning."""
        self.assert_shape_reports("render(<A p={x} />);\n")

    def test_jsx_self_closing_after_an_identifier_keeps_scanning(self) -> None:
        """Test that jsx self closing after an identifier keeps scanning."""
        self.assert_shape_reports("render(<Br />);\n")

    def test_jsx_self_closing_after_a_quoted_attribute_keeps_scanning(self) -> None:
        """Test that jsx self closing after a quoted attribute keeps scanning."""
        self.assert_shape_reports('render(<A p="x" />);\n')

    def test_a_regex_starting_with_a_self_closing_slash_is_masked(self) -> None:
        """Test that a regex starting with a self closing slash is masked."""
        self.assertEqual(
            ".replace(/ /g, '    ')\n",
            m.mask_javascript(".replace(/>/g, '&gt;')\n"),
        )

    def test_the_saml_replace_shape_keeps_scanning(self) -> None:
        """Test that the saml replace shape keeps scanning."""
        self.assert_shape_reports("value.replace(/>/g, '&gt;');\n")

    def test_jsx_text_apostrophe_keeps_scanning(self) -> None:
        """Test that jsx text apostrophe keeps scanning."""
        self.assert_shape_reports("render(<p>it's here</p>);\n")

    def test_jsx_closing_tag_before_a_template_keeps_scanning(self) -> None:
        """Test that jsx closing tag before a template keeps scanning."""
        self.assert_shape_reports("render(<Link>a</Link>); f(`/a/${id}`);\n")

    def test_jsx_self_closing_before_a_template_keeps_scanning(self) -> None:
        """Test that jsx self closing before a template keeps scanning."""
        self.assert_shape_reports("render(<A p={x} />); f(`/a/${id}`);\n")

    def test_a_regex_after_an_if_header_keeps_scanning(self) -> None:
        """Test that a regex after an if header keeps scanning."""
        self.assert_shape_reports("if (x) /`/.test(s);\n")

    def test_a_regex_after_a_comparison_slash_keeps_scanning(self) -> None:
        """Test that a regex after a comparison slash keeps scanning."""
        self.assert_shape_reports("x</`/.test(s);\n")

    def test_a_double_slash_in_jsx_text_is_not_a_line_comment(self) -> None:
        """Test that a double slash in jsx text is not a line comment."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it, screen } from 'vitest'\n"
            "it('adds', async () => {\n"
            "  render(<p>http://x</p>); await screen.findByText('x', "
            "{ timeout: 500 })\n"
            "})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "500ms")

    def test_a_regex_on_the_right_of_a_less_than_is_not_code(self) -> None:
        """Test that a regex on the right of a less than is not code."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => {\n"
            "  if (s</os.cpus()/.test(t)) {}\n"
            "})\n",
        )
        self.assert_clean()

    def test_a_closing_tag_after_jsx_text_is_still_markup(self) -> None:
        """Test that a closing tag after jsx text is still markup."""
        self.assert_shape_reports("render(<Link>a</Link>);\n")

    def test_a_comment_after_a_nested_closing_tag_is_masked(self) -> None:
        """Test that a comment after a nested closing tag is masked."""
        masked = m.mask_javascript(
            "render(<p><b>x</b> hello</p>) // names os.cpus()\n"
        )
        self.assertNotIn("os.cpus", masked)

    def test_a_wait_after_a_nested_closing_tag_is_read(self) -> None:
        """Test that a wait after a nested closing tag is read."""
        masked = m.mask_javascript(
            "render(<p><b>x</b> hello</p>); "
            "await screen.findByText('x', { timeout: 500 }); "
            "const d = a / b\n"
        )
        self.assertIn("500", masked)

    def test_code_after_a_multiline_element_close_is_read(self) -> None:
        """Test that code after a multiline element close is read."""
        masked = m.mask_javascript(
            "render(<p><b>x</b>\n hello</p>); const n = os.cpus() / 2\n"
        )
        self.assertIn("os.cpus", masked)

    def test_text_after_a_self_closing_child_keeps_its_slashes(self) -> None:
        """Test that text after a self closing child keeps its slashes."""
        masked = m.mask_javascript("render(<p><br/>http://y</p>)\n")
        self.assertIn("http://y", masked)

    def test_text_after_a_nested_open_tag_keeps_its_slashes(self) -> None:
        """Test that text after a nested open tag keeps its slashes."""
        masked = m.mask_javascript("render(<p>see <b>http://y</b></p>)\n")
        self.assertIn("http://y", masked)

    def assert_keyword_shape_keeps_text_and_wait(self, shape: str) -> None:
        self.assertIn("http://y", m.mask_javascript(shape))
        self.assert_shape_reports(shape)

    def test_a_return_before_jsx_keeps_its_text_and_wait(self) -> None:
        """Test that a return before jsx keeps its text and wait."""
        self.assert_keyword_shape_keeps_text_and_wait(
            "return <p>http://y</p>\n"
        )

    def test_a_return_before_jsx_text_with_an_apostrophe_keeps_the_wait(
        self,
    ) -> None:
        """Test that a return before jsx text with an apostrophe keeps the wait."""
        self.assert_keyword_shape_keeps_text_and_wait(
            "return <p>as'x http://y</p>\n"
        )

    def test_an_await_before_jsx_keeps_its_text_and_wait(self) -> None:
        """Test that an await before jsx keeps its text and wait."""
        self.assert_keyword_shape_keeps_text_and_wait("await <p>http://y</p>\n")

    def test_an_awaited_parenthesised_jsx_element_keeps_its_text(self) -> None:
        """Test that an awaited parenthesised jsx element keeps its text."""
        self.assert_keyword_shape_keeps_text_and_wait(
            "await (<p>http://y</p>)\n"
        )

    def test_a_ternary_branch_jsx_element_keeps_its_text_and_wait(self) -> None:
        """Test that a ternary branch jsx element keeps its text and wait."""
        self.assert_keyword_shape_keeps_text_and_wait(
            "const v = x ? <p>http://y</p> : y\n"
        )

    def test_a_comparison_after_a_keyword_is_still_code(self) -> None:
        """Test that a comparison after a keyword is still code."""
        masked = m.mask_javascript(
            "function f() { return a < b } // os.cpus()\n"
        )
        self.assertNotIn("os.cpus", masked)

    def test_a_string_in_an_expression_container_is_masked(self) -> None:
        """Test that a string in an expression container is masked."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => {\n"
            "  render(<p>{'os.cpus()'}</p>)\n"
            "})\n",
        )
        self.assert_clean()

    def test_a_comment_in_an_expression_container_is_masked(self) -> None:
        """Test that a comment in an expression container is masked."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => {\n"
            "  render(<p>{x // os.cpus()}</p>)\n"
            "})\n",
        )
        self.assert_clean()

    def test_text_resumes_after_an_expression_container_closes(self) -> None:
        """Test that text resumes after an expression container closes."""
        masked = m.mask_javascript("render(<p>{x} http://y</p>)\n")
        self.assertIn("http://y", masked)

    def test_a_type_parameter_misread_reports_the_name_between_the_tags(
        self,
    ) -> None:
        """Test that a type parameter misread reports the name between the tags."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => {\n"
            "  const y = foo(<T>bar) // os.cpus()\n"
            "  // </T>\n"
            "})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "os.cpus")

    def test_a_type_argument_in_a_tag_does_not_end_the_tag(self) -> None:
        """Test that a type argument in a tag does not end the tag."""
        masked = m.mask_javascript(
            "render(<FormField<FormValues, 'name'> value={x} />); "
            "const d = a / b\n"
        )
        self.assertIn("const d = a / b", masked)

    def test_a_less_than_chain_keeps_its_comment(self) -> None:
        """Test that a less than chain keeps its comment."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => {\n"
            "  const ok = a<b>c // names os.cpus()\n"
            "})\n",
        )
        self.assert_clean()

    def test_a_string_after_as_is_masked(self) -> None:
        """Test that a string after as is masked."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it, screen } from 'vitest'\n"
            "it('adds', async () => {\n"
            "  const k = x as'os.cpus()'\n"
            "  await screen.findByText('x', { timeout: 500 })\n"
            "})\n",
        )
        out = self.assert_finding("web/packages/alpha/src/alpha.test.ts", "500ms")
        self.assertNotIn("os.cpus", out)

    def test_a_string_after_as_does_not_fail_closed(self) -> None:
        """Test that a string after as does not fail closed."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => {\n"
            "  const k = x as'os.cpus()'\n"
            "})\n",
        )
        self.assert_clean()

    def test_a_comment_after_a_type_assertion_is_masked(self) -> None:
        """Test that a comment after a type assertion is masked."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => {\n"
            "  const y = foo(<T>bar) // os.cpus() <b>\n"
            "})\n",
        )
        self.assert_clean()

    def test_a_generic_arrow_does_not_turn_later_lines_into_text(self) -> None:
        """Test that a generic arrow does not turn later lines into text."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it } from 'vitest'\n"
            "it('adds', () => {\n"
            "  const empty = <T,>(): T[] => []\n"
            "  // names os.cpus()\n"
            "  const s = 'os.cpus()'\n"
            "})\n",
        )
        self.assert_clean()

    def test_an_apostrophe_in_jsx_text_does_not_hide_the_next_wait(self) -> None:
        """Test that an apostrophe in jsx text does not hide the next wait."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it, screen } from 'vitest'\n"
            "it('adds', async () => {\n"
            "  render(<p>as'x</p>);\n"
            "  await screen.findByText('x', { timeout: 500 })\n"
            "})\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "500ms")

    def test_an_unclosed_construct_raises_rather_than_blanking(self) -> None:
        """Test that an unclosed construct raises rather than blanking."""
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
        """Test that an unclosed template exits 2 naming the file."""
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
        """Test that a wait for condition timeout of zero passes."""
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
        """Test that a wait for condition timeout below the default passes."""
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
        """Test that a vi wait for timeout of zero is still a finding."""
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            "import { it, vi } from 'vitest'\n"
            "it('adds', async () => { await vi.waitFor(() => {}, { timeout: 0 }) })\n",
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "0ms")

    def test_a_wrapper_using_the_callers_value_directly_exits_2(self) -> None:
        """Test that a wrapper using the callers value directly exits 2."""
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
        """Test that a missing wait condition file exits 2."""
        os.remove(
            os.path.join(self.root, "web", "test-utils", "src", "wait.ts")
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("wait.ts", err)
        self.assertIn("cannot be checked", err)

    def test_an_unreadable_wait_condition_file_exits_2(self) -> None:
        """Test that an unreadable wait condition file exits 2."""
        path = os.path.join(self.root, "web", "test-utils", "src", "wait.ts")
        with open(path, "wb") as handle:
            handle.write(b"\xff\xfe\x00")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("wait.ts", err)
        self.assertIn("cannot be read", err)

    def test_the_premise_check_reads_the_wrappers_body(self) -> None:
        """Test that the premise check reads the wrappers body."""
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
        """Test that a lowered vitest default is a finding."""
        self.write("web/vitest.shared.ts", PRESET_WITH.format(default_ms="5_000"))
        self.assert_finding("web/vitest.shared.ts", "5000ms", "60000ms")

    def test_the_vitest_floor_comes_from_the_floors_file(self) -> None:
        """Test that the vitest floor comes from the floors file."""
        self.write(
            "web/scripts/test-time-floors.json",
            FLOORS_WITH % "90000",
        )
        self.write("web/vitest.shared.ts", PRESET_WITH.format(default_ms="80_000"))
        self.assert_finding("web/vitest.shared.ts", "80000ms", "90000ms")

    def test_a_raised_default_is_what_case_budgets_are_judged_against(self) -> None:
        """Test that a raised default is what case budgets are judged against."""
        self.write("web/vitest.shared.ts", PRESET_WITH.format(default_ms="90_000"))
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "it('adds', () => {", "it('adds', { timeout: 80_000 }, () => {"
            ),
        )
        self.assert_finding("web/packages/alpha/src/alpha.test.ts", "80000ms", "90000ms")

    def test_a_raised_default_still_passes_a_budget_above_it(self) -> None:
        """Test that a raised default still passes a budget above it."""
        self.write("web/vitest.shared.ts", PRESET_WITH.format(default_ms="90_000"))
        self.write(
            "web/packages/alpha/src/alpha.test.ts",
            VITEST_CASE.replace(
                "it('adds', () => {", "it('adds', { timeout: 120_000 }, () => {"
            ),
        )
        self.assert_clean()

    def test_the_playwright_floor_comes_from_the_floors_file(self) -> None:
        """Test that the playwright floor comes from the floors file."""
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
        """Test that the async floor comes from the floors file."""
        self.write("web/scripts/test-time-floors.json", ASYNC_FLOORS_WITH % "40000")
        self.assert_finding("web/test-utils/src/gate-time.ts", "30000ms", "40000ms")

    def test_a_lowered_playwright_default_is_a_finding(self) -> None:
        """Test that a lowered playwright default is a finding."""
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            PLAYWRIGHT_CONFIG.replace("scaledTimeout(180_000)", "scaledTimeout(60_000)"),
        )
        self.assert_finding(
            "starters/demo/web/e2e/playwright.config.ts", "60000ms", "120000ms"
        )

    def test_the_playwright_default_is_read_at_any_indentation(self) -> None:
        """Test that the playwright default is read at any indentation."""
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
        """Test that a nested only timeout is not the default."""
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
        """Test that a root that is not a directory exits 2."""
        code, _, err = self.run_gate(os.path.join(self.root, "nope"))
        self.assertEqual(2, code)
        self.assertIn("not a directory", err)

    def test_a_tree_with_no_test_file_exits_2(self) -> None:
        """Test that a tree with no test file exits 2."""
        empty = tempfile.mkdtemp(prefix="test-limits-empty-")
        self.addCleanup(shutil.rmtree, empty, ignore_errors=True)
        code, _, err = self.run_gate(empty)
        self.assertEqual(2, code)
        self.assertIn("nothing was checked", err)

    def test_a_vitest_tree_without_the_preset_exits_2(self) -> None:
        """Test that a vitest tree without the preset exits 2."""
        os.remove(os.path.join(self.root, "web", "vitest.shared.ts"))
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("vitest.shared.ts", err)

    def test_a_vitest_tree_without_the_gate_time_default_exits_2(self) -> None:
        """Test that a vitest tree without the gate time default exits 2."""
        os.remove(
            os.path.join(self.root, "web", "test-utils", "src", "gate-time.ts")
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("gate-time.ts", err)

    def test_a_vitest_tree_without_the_floors_file_exits_2(self) -> None:
        """Test that a vitest tree without the floors file exits 2."""
        os.remove(os.path.join(self.root, "web", "scripts", "test-time-floors.json"))
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("test-time-floors.json", err)

    def test_a_floors_file_without_a_key_exits_2(self) -> None:
        """Test that a floors file without a key exits 2."""
        self.write(
            "web/scripts/test-time-floors.json",
            '{"playwrightCaseTimeoutMs": 120000}\n',
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("vitestCaseTimeoutMs", err)

    def test_a_floors_file_without_the_async_key_exits_2(self) -> None:
        """Test that a floors file without the async key exits 2."""
        self.write(
            "web/scripts/test-time-floors.json",
            '{"vitestCaseTimeoutMs": 60000, "playwrightCaseTimeoutMs": 120000}\n',
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("asyncUtilTimeoutMs", err)

    def test_a_nan_floor_exits_2(self) -> None:
        """Test that a nan floor exits 2."""
        self.write(
            "web/scripts/test-time-floors.json",
            '{"vitestCaseTimeoutMs": NaN, "playwrightCaseTimeoutMs": 120000, '
            '"asyncUtilTimeoutMs": 30000}\n',
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("vitestCaseTimeoutMs", err)

    def test_an_infinite_floor_exits_2(self) -> None:
        """Test that an infinite floor exits 2."""
        self.write(
            "web/scripts/test-time-floors.json",
            '{"vitestCaseTimeoutMs": 60000, "playwrightCaseTimeoutMs": 120000, '
            '"asyncUtilTimeoutMs": 1e999}\n',
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("asyncUtilTimeoutMs", err)

    def test_a_floors_file_that_is_not_json_exits_2(self) -> None:
        """Test that a floors file that is not json exits 2."""
        self.write("web/scripts/test-time-floors.json", "{ not json\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("not readable JSON", err)

    def test_a_playwright_tree_without_the_config_exits_2(self) -> None:
        """Test that a playwright tree without the config exits 2."""
        os.remove(
            os.path.join(
                self.root, "starters", "demo", "web", "e2e", "playwright.config.ts"
            )
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("playwright.config.ts", err)

    def test_a_playwright_config_without_a_default_exits_2(self) -> None:
        """Test that a playwright config without a default exits 2."""
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            "export default defineConfig({ testDir: './specs' })\n",
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("no top-level timeout", err)


CORETEST_PATH = "github.com/vislake/speed/pkg/core/coretest"


class TestCoretestIsReadWhenTheCaseRuns(GateCase):
    """A coretest bound may not be evaluated before the test log starts.

    The Go test log opens at `M.Run`, so a `GATE_TIME_SCALE` read from a
    package-level declaration, an `init` body or `TestMain` never reaches
    the value `go test` hashes into its cache key. Reachability is by name
    across one directory, so a package-level call to a local helper that
    reaches `coretest` any number of layers down is a finding too.
    """

    def test_a_package_level_bound_is_a_finding(self) -> None:
        """A package-level var bound is a finding."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "var requestBound = coretest.Scale(3 * time.Second)\n\n"
            "func TestBound(t *testing.T) {\n\t_ = requestBound\n}\n",
        )
        out = self.assert_finding(
            "pkg/alpha/alpha_test.go", "alpha_test.go:10:", "coretest.Scale"
        )
        self.assertIn("cache key", out)
        self.assertIn("when the case runs", out)

    def test_a_parenthesized_var_block_is_a_finding(self) -> None:
        """A parenthesized var block is the same position as a single var."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "var (\n"
            "\trequestBound = coretest.Scale(3 * time.Second)\n"
            "\twaitBound    = coretest.Scale(time.Second)\n"
            ")\n\n"
            "func TestBound(t *testing.T) {\n"
            "\t_ = requestBound\n\t_ = waitBound\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("alpha_test.go:11:", out)
        self.assertIn("alpha_test.go:12:", out)
        self.assertEqual(2, out.count("coretest.Scale"))

    def test_an_aliased_import_is_a_finding(self) -> None:
        """A named import alias qualifies the call just as the package name does."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\tct \"{CORETEST_PATH}\"\n)\n\n"
            "var requestBound = ct.Declared(3 * time.Second)\n\n"
            "func TestBound(t *testing.T) {\n\t_ = requestBound\n}\n",
        )
        self.assert_finding(
            "pkg/alpha/alpha_test.go", "alpha_test.go:10:", "ct.Declared"
        )

    def test_a_package_level_function_literal_is_a_finding(self) -> None:
        """A package-level function literal is judged even when nothing calls it."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "var makeBound = func() time.Duration {\n"
            "\treturn coretest.Scale(3 * time.Second)\n"
            "}\n\n"
            "func TestBound(t *testing.T) { _ = makeBound() }\n",
        )
        out = self.assert_finding(
            "pkg/alpha/alpha_test.go", "alpha_test.go:11:", "coretest.Scale"
        )
        self.assertIn("named top-level function", out)

    def test_an_immediate_package_level_invocation_is_a_finding(self) -> None:
        """An immediately invoked literal at package level runs at initialization."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "var requestBound = func() time.Duration {\n"
            "\treturn coretest.Scale(3 * time.Second)\n"
            "}()\n\n"
            "func TestBound(t *testing.T) {\n\t_ = requestBound\n}\n",
        )
        out = self.assert_finding(
            "pkg/alpha/alpha_test.go", "alpha_test.go:11:", "coretest.Scale"
        )
        self.assertIn("named top-level function", out)

    def test_an_init_body_is_a_finding(self) -> None:
        """An init body that reads coretest is a finding."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"os\"\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "var requestBound time.Duration\n\n"
            "func init() {\n"
            "\trequestBound = coretest.Scale(3 * time.Second)\n"
            "}\n\n"
            "func TestBound(t *testing.T) {\n\t_ = requestBound\n}\n",
        )
        self.assert_finding(
            "pkg/alpha/alpha_test.go", "alpha_test.go:14:", "coretest.Scale"
        )

    def test_a_test_main_body_is_a_finding(self) -> None:
        """A TestMain body that reads coretest is a finding."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"os\"\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "func TestMain(m *testing.M) {\n"
            "\t_ = coretest.Scale(3 * time.Second)\n"
            "\tos.Exit(m.Run())\n"
            "}\n",
        )
        self.assert_finding(
            "pkg/alpha/alpha_test.go", "alpha_test.go:12:", "coretest.Scale"
        )

    def test_a_two_layer_helper_across_files_is_a_finding(self) -> None:
        """Reachability crosses files in a directory, two local functions deep."""
        self.write(
            "pkg/alpha/helpers_test.go",
            "package alpha\n\nimport (\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "func innerLimit() time.Duration {\n"
            "\treturn coretest.Scale(time.Second)\n"
            "}\n\n"
            "func limit() time.Duration {\n"
            "\treturn innerLimit()\n"
            "}\n",
        )
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n)\n\n"
            "var requestBound = 2 * limit()\n\n"
            "func TestBound(t *testing.T) {\n\t_ = requestBound\n}\n",
        )
        out = self.assert_finding(
            "pkg/alpha/alpha_test.go", "alpha_test.go:7:", "limit"
        )
        self.assertIn("coretest", out)

    def test_a_three_layer_helper_chain_is_a_finding(self) -> None:
        """A package-level call through three local helpers is a finding."""
        self.write(
            "pkg/alpha/helpers_test.go",
            "package alpha\n\nimport (\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "func topLimit() time.Duration {\n"
            "\treturn middleLimit()\n"
            "}\n\n"
            "func middleLimit() time.Duration {\n"
            "\treturn bottomLimit()\n"
            "}\n\n"
            "func bottomLimit() time.Duration {\n"
            "\treturn coretest.Scale(time.Second)\n"
            "}\n",
        )
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n)\n\n"
            "var requestBound = topLimit()\n\n"
            "func TestBound(t *testing.T) {\n\t_ = requestBound\n}\n",
        )
        out = self.assert_finding(
            "pkg/alpha/alpha_test.go", "alpha_test.go:7:", "topLimit"
        )
        self.assertIn("coretest", out)

    def test_a_test_main_calling_a_reaching_helper_is_a_finding(self) -> None:
        """A TestMain body that calls a helper reaching coretest is a finding."""
        self.write(
            "pkg/alpha/helpers_test.go",
            "package alpha\n\nimport (\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "func waitLimit() time.Duration {\n"
            "\treturn coretest.Scale(time.Second)\n"
            "}\n",
        )
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n"
            "func TestMain(m *testing.M) {\n"
            "\t_ = waitLimit()\n"
            "\tos.Exit(m.Run())\n"
            "}\n",
        )
        out = self.assert_finding(
            "pkg/alpha/alpha_test.go", "alpha_test.go:9:", "waitLimit"
        )
        self.assertIn("coretest", out)

    def test_a_same_named_helper_in_another_directory_is_clean(self) -> None:
        """A same-named helper in another directory does not make its calls reach coretest."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "func limit() time.Duration {\n"
            "\treturn coretest.Scale(time.Second)\n"
            "}\n\n"
            "func TestBound(t *testing.T) { _ = limit() }\n",
        )
        self.write(
            "pkg/beta/beta_test.go",
            "package beta\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "func limit() time.Duration { return 0 }\n\n"
            "var requestBound = limit()\n\n"
            "func TestBound(t *testing.T) {\n"
            "\t_ = requestBound\n"
            "\t_ = coretest.Declared(time.Second)\n"
            "}\n",
        )
        self.assert_clean()

    def test_an_interface_method_named_like_a_reaching_function_is_clean(self) -> None:
        """An interface method declaration is not a call to the same-named function."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "type limiter interface {\n"
            "\tlimit() time.Duration\n"
            "}\n\n"
            "func limit() time.Duration {\n"
            "\treturn coretest.Scale(time.Second)\n"
            "}\n\n"
            "func TestBound(t *testing.T) { _ = limit() }\n",
        )
        self.assert_clean()

    def test_a_dot_import_is_a_finding(self) -> None:
        """A dot import hides its calls from a qualifier scan, so it is the finding."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t. \"{CORETEST_PATH}\"\n)\n\n"
            "var requestBound = Scale(3 * time.Second)\n\n"
            "func TestBound(t *testing.T) {\n\t_ = requestBound\n}\n",
        )
        self.assert_finding(
            "pkg/alpha/alpha_test.go", "alpha_test.go:7:", CORETEST_PATH
        )

    def test_a_build_tagged_file_is_a_finding(self) -> None:
        """A build-tagged test file is still test-side code."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "//go:build integration\n\npackage alpha\n\nimport (\n"
            "\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "var requestBound = coretest.Scale(3 * time.Second)\n\n"
            "func TestBound(t *testing.T) {\n\t_ = requestBound\n}\n",
        )
        self.assert_finding(
            "pkg/alpha/alpha_test.go", "alpha_test.go:12:", "coretest.Scale"
        )

    def test_a_helper_package_ending_in_test_is_a_finding(self) -> None:
        """A non-_test.go file of a helper package is test-side too."""
        self.write(
            "pkg/alphatest/helper.go",
            "package alphatest\n\nimport (\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "var defaultWait = coretest.Scale(time.Second)\n",
        )
        self.assert_finding(
            "pkg/alphatest/helper.go", "helper.go:9:", "coretest.Scale"
        )

    def test_a_call_inside_a_case_is_clean(self) -> None:
        """A read inside a case is clean."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "func TestBound(t *testing.T) {\n"
            "\tlimit := coretest.Scale(3 * time.Second)\n"
            "\t_ = limit\n"
            "}\n",
        )
        self.assert_clean()

    def test_a_call_inside_a_helper_function_is_clean(self) -> None:
        """A helper called from a case evaluates its bound at run time."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "func waitLimit() time.Duration {\n"
            "\treturn coretest.Scale(time.Second)\n"
            "}\n\n"
            "func TestBound(t *testing.T) { _ = waitLimit() }\n",
        )
        self.assert_clean()

    def test_a_call_in_a_comment_or_string_is_prose(self) -> None:
        """A token in a comment or a string is prose, not a read."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "// var requestBound = coretest.Scale(3 * time.Second)\n"
            "var note = \"coretest.Scale(3 * time.Second)\"\n\n"
            "func TestBound(t *testing.T) { _ = note; _ = time.Second }\n",
        )
        self.assert_clean()

    def test_a_method_call_named_like_a_reaching_function_is_clean(self) -> None:
        """A method call is preceded by a dot and is not a package function."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "type clock struct{}\n\n"
            "func (clock) limit() time.Duration { return 0 }\n\n"
            "func limit() time.Duration {\n"
            "\treturn coretest.Scale(time.Second)\n"
            "}\n\n"
            "var now = clock{}\n"
            "var requestBound = now.limit()\n\n"
            "func TestBound(t *testing.T) { _ = requestBound; _ = limit }\n",
        )
        self.assert_clean()

    def test_a_method_body_is_a_documented_blind_spot(self) -> None:
        """A method called at a package-level position is a documented blind spot."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n\t\"time\"\n\n"
            f"\t\"{CORETEST_PATH}\"\n)\n\n"
            "type clock struct{}\n\n"
            "func (clock) limit() time.Duration {\n"
            "\treturn coretest.Scale(time.Second)\n"
            "}\n\n"
            "var now = clock{}.limit()\n\n"
            "func TestBound(t *testing.T) { _ = now }\n",
        )
        self.assert_clean()

    def test_a_file_without_the_import_is_clean(self) -> None:
        """A directory with no coretest import has no read to place."""
        self.write(
            "pkg/alpha/alpha_test.go",
            "package alpha\n\nimport (\n\t\"testing\"\n)\n\n"
            "func compute() int { return 1 }\n\n"
            "var derived = compute()\n\n"
            "func TestDerived(t *testing.T) { _ = derived }\n",
        )
        self.assert_clean()


class TestTheRealRepository(unittest.TestCase):
    def test_every_test_and_test_configuration_obeys_the_rulings(self) -> None:
        """Test that every test and test configuration obeys the rulings."""
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", REPO])
        self.assertEqual(
            0, code, f"stdout={out.getvalue()!r} stderr={err.getvalue()!r}"
        )


if __name__ == "__main__":
    unittest.main()
