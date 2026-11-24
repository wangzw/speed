#!/usr/bin/env python3
"""Unit tests for check_e2e_honesty.py's criteria and exit codes."""

from __future__ import annotations

import contextlib
import io
import pathlib
import shutil
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent

sys.path.insert(0, str(HERE))

import check_e2e_honesty as m  # noqa: E402

JOURNEY_ALPHA = """\
# Alpha

## Covered requirements

- `REQ-alpha-001`
- `REQ-alpha-002`
"""

JOURNEY_BETA = """\
# Beta

## Covered requirements

- `REQ-beta-001`
"""

GOOD_SPEC = """\
// @journey journey-alpha.md
// @covers REQ-alpha-001
import { expect, test } from '@playwright/test'

test('alpha', async ({ page }) => {
  await page.getByRole('button', { name: 'Continue' }).click()
  await expect(page.getByRole('heading')).toBeVisible()
})
"""


def registry(entries: str) -> str:
    return f"""\
export interface JourneyEntry {{
  journey: string
  status: 'covered' | 'pending' | 'unreachable'
  reason?: string
  covers: string[]
}}

export const journeys: JourneyEntry[] = [
{entries}
]
"""


GOOD_REGISTRY = registry(
    """\
  {
    journey: 'journey-beta.md',
    status: 'unreachable',
    reason: 'requires a mail carrier the demo configuration does not configure',
    covers: ['REQ-beta-001'],
  },"""
)

GOOD_CONFIG = """\
import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './specs',
  outputDir: '/tmp/speed-e2e/artifacts',
  reporter: [['html', { outputFolder: '/tmp/speed-e2e/report' }]],
})
"""

GOOD_MAKEFILE = """\
.PHONY: e2e e2e-install check

e2e-install:
\t@pnpm -C web exec playwright install chromium

e2e:
\t@pnpm -C web --filter demo-web exec playwright test --config e2e/playwright.config.ts

check:
\t@$(MAKE) build
\t@$(MAKE) test
"""


class GateCase(unittest.TestCase):
    def setUp(self) -> None:
        self.root = tempfile.mkdtemp(prefix="e2e-honesty-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.build_baseline()

    def write(self, relative: str, text: str) -> str:
        path = pathlib.Path(self.root) / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        return str(path)

    def build_baseline(self) -> None:
        self.write("docs/prd/journeys/journey-alpha.md", JOURNEY_ALPHA)
        self.write("docs/prd/journeys/journey-beta.md", JOURNEY_BETA)
        self.write("starters/demo/web/e2e/specs/journey-alpha.spec.ts", GOOD_SPEC)
        self.write("starters/demo/web/e2e/journeys.ts", GOOD_REGISTRY)
        self.write("starters/demo/web/e2e/playwright.config.ts", GOOD_CONFIG)
        self.write("Makefile", GOOD_MAKEFILE)

    def run_gate(self, root: str | None = None) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", root if root is not None else self.root])
        return code, out.getvalue(), err.getvalue()


class TestBaseline(GateCase):
    def test_the_baseline_tree_passes(self) -> None:
        """Test that the baseline tree passes."""
        code, out, err = self.run_gate()
        self.assertEqual(code, 0, f"{out}{err}")
        self.assertEqual(
            out,
            "e2e-honesty: 2 journey document(s): "
            "1 covered, 0 pending, 1 unreachable, 0 unimplemented\n",
        )


class TestCoverage(GateCase):
    def test_a_journey_with_neither_spec_nor_registry_is_a_finding(self) -> None:
        """Test that a journey with neither spec nor registry is a finding."""
        self.write("docs/prd/journeys/journey-gamma.md", "# Gamma\n\n- `REQ-gamma-001`\n")
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("journey-gamma.md", out)
        self.assertIn(
            "neither covered by a spec nor registered pending, unreachable or "
            "unimplemented with a reason",
            out,
        )

    def test_an_absent_registry_is_valid_when_specs_cover_every_journey(self) -> None:
        """Test that an absent registry is valid when specs cover every journey."""
        self.write(
            "starters/demo/web/e2e/specs/journey-beta.spec.ts",
            "// @journey journey-beta.md\n// @covers REQ-beta-001\n"
            "import { test } from '@playwright/test'\n"
            "test('beta', async ({ page }) => { await page.click('button') })\n",
        )
        pathlib.Path(self.root, "starters/demo/web/e2e/journeys.ts").unlink()
        code, out, err = self.run_gate()
        self.assertEqual(code, 0, f"{out}{err}")
        self.assertIn(
            "2 journey document(s): 2 covered, 0 pending, 0 unreachable, 0 unimplemented", out
        )

    def test_a_reachable_journey_labelled_unreachable_with_no_reason_fails(self) -> None:
        """Test that a reachable journey labelled unreachable with no reason fails."""
        self.write(
            "starters/demo/web/e2e/journeys.ts",
            registry(
                """\
  {
    journey: 'journey-beta.md',
    status: 'unreachable',
    reason: '',
    covers: ['REQ-beta-001'],
  },"""
            ),
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("marked unreachable with no reason", out)

    def test_a_pending_journey_needs_a_reason(self) -> None:
        """Test that a pending journey needs a reason."""
        self.write(
            "starters/demo/web/e2e/journeys.ts",
            registry(
                """\
  {
    journey: 'journey-beta.md',
    status: 'pending',
    reason: '',
    covers: ['REQ-beta-001'],
  },"""
            ),
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("marked pending with no reason", out)

    def test_pending_is_an_honest_state_not_a_failure(self) -> None:
        """Test that pending is an honest state not a failure."""
        self.write(
            "starters/demo/web/e2e/journeys.ts",
            registry(
                """\
  {
    journey: 'journey-beta.md',
    status: 'pending',
    reason: 'browser-reachable; its spec is scheduled for a later round',
    covers: ['REQ-beta-001'],
  },"""
            ),
        )
        code, out, err = self.run_gate()
        self.assertEqual(code, 0, f"{out}{err}")
        self.assertIn(
            "2 journey document(s): 1 covered, 1 pending, 0 unreachable, 0 unimplemented", out
        )

    def test_unimplemented_is_an_honest_state_not_a_failure(self) -> None:
        """Test that unimplemented is an honest state not a failure."""
        self.write(
            "starters/demo/web/e2e/journeys.ts",
            registry(
                """\
  {
    journey: 'journey-beta.md',
    status: 'unimplemented',
    reason: 'the feature is designed but not built, so no entry is rendered',
    covers: ['REQ-beta-001'],
  },"""
            ),
        )
        code, out, err = self.run_gate()
        self.assertEqual(code, 0, f"{out}{err}")
        self.assertIn(
            "2 journey document(s): 1 covered, 0 pending, 0 unreachable, "
            "1 unimplemented",
            out,
        )

    def test_an_unimplemented_journey_needs_a_reason(self) -> None:
        """Test that an unimplemented journey needs a reason."""
        self.write(
            "starters/demo/web/e2e/journeys.ts",
            registry(
                """\
  {
    journey: 'journey-beta.md',
    status: 'unimplemented',
    reason: '',
    covers: ['REQ-beta-001'],
  },"""
            ),
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("marked unimplemented with no reason", out)

    def test_the_summary_reports_the_three_counts(self) -> None:
        """Test that the summary reports the three counts."""
        self.write(
            "docs/prd/journeys/journey-gamma.md", "# Gamma\n\n- `REQ-gamma-001`\n"
        )
        self.write(
            "starters/demo/web/e2e/journeys.ts",
            registry(
                """\
  {
    journey: 'journey-beta.md',
    status: 'unreachable',
    reason: 'requires a mail carrier the demo configuration does not configure',
    covers: ['REQ-beta-001'],
  },
  {
    journey: 'journey-gamma.md',
    status: 'pending',
    reason: 'browser-reachable; its spec is scheduled for a later round',
    covers: ['REQ-gamma-001'],
  },"""
            ),
        )
        code, out, err = self.run_gate()
        self.assertEqual(code, 0, f"{out}{err}")
        self.assertIn(
            "3 journey document(s): 1 covered, 1 pending, 1 unreachable, 0 unimplemented", out
        )

    def test_a_registry_covered_entry_still_needs_a_spec(self) -> None:
        """Test that a registry covered entry still needs a spec."""
        self.write(
            "starters/demo/web/e2e/journeys.ts",
            registry(
                """\
  {
    journey: 'journey-beta.md',
    status: 'covered',
    covers: ['REQ-beta-001'],
  },"""
            ),
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("marked covered but no spec declares it", out)

    def test_a_registry_entry_must_name_a_known_journey(self) -> None:
        """Test that a registry entry must name a known journey."""
        self.write(
            "starters/demo/web/e2e/journeys.ts",
            registry(
                """\
  {
    journey: 'journey-unknown.md',
    status: 'unreachable',
    reason: 'not a document',
    covers: [],
  },"""
            ),
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("unknown journey document", out)

    def test_an_unknown_status_is_a_finding(self) -> None:
        """Test that an unknown status is a finding."""
        self.write(
            "starters/demo/web/e2e/journeys.ts",
            registry(
                """\
  {
    journey: 'journey-beta.md',
    status: 'deferred',
    reason: 'later',
    covers: [],
  },"""
            ),
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("unknown status", out)
        self.assertIn(
            "expected 'covered', 'pending', 'unreachable' or 'unimplemented'", out
        )

    def test_a_comment_with_braces_in_the_registry_is_not_an_entry(self) -> None:
        """Test that a comment with braces in the registry is not an entry."""
        self.write(
            "starters/demo/web/e2e/journeys.ts",
            "export const journeys = [\n"
            "  // { this is prose, not an entry }\n"
            "  {\n"
            "    journey: 'journey-beta.md',\n"
            "    status: 'unreachable',\n"
            "    reason: 'not reachable through the UI',\n"
            "    covers: ['REQ-beta-001'],\n"
            "  },\n"
            "]\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(code, 0, f"{out}{err}")


class TestSpecDeclarations(GateCase):
    def test_a_spec_without_a_journey_declaration_is_a_finding(self) -> None:
        """Test that a spec without a journey declaration is a finding."""
        self.write(
            "starters/demo/web/e2e/specs/orphan.spec.ts",
            "// @covers REQ-alpha-001\nimport { test } from '@playwright/test'\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("no `@journey", out)

    def test_a_spec_without_a_requirement_declaration_is_a_finding(self) -> None:
        """Test that a spec without a requirement declaration is a finding."""
        self.write(
            "starters/demo/web/e2e/specs/quiet.spec.ts",
            "// @journey journey-alpha.md\nimport { test } from '@playwright/test'\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("no `@covers", out)

    def test_a_spec_cannot_cover_a_requirement_its_journey_does_not_list(self) -> None:
        """Test that a spec cannot cover a requirement its journey does not list."""
        self.write(
            "starters/demo/web/e2e/specs/overreach.spec.ts",
            "// @journey journey-alpha.md\n// @covers REQ-zeta-999\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("absent from `journey-alpha.md`", out)
        self.assertIn("REQ-zeta-999", out)

    def test_a_spec_cannot_declare_an_unknown_journey(self) -> None:
        """Test that a spec cannot declare an unknown journey."""
        self.write(
            "starters/demo/web/e2e/specs/wrong.spec.ts",
            "// @journey journey-nowhere.md\n// @covers REQ-alpha-001\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("unknown journey document", out)

    def test_a_spec_cannot_declare_two_journeys(self) -> None:
        """Test that a spec cannot declare two journeys."""
        self.write(
            "starters/demo/web/e2e/specs/two.spec.ts",
            "// @journey journey-alpha.md\n// @journey journey-beta.md\n"
            "// @covers REQ-alpha-001\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("more than one journey", out)

    def test_a_spec_with_only_a_header_covers_nothing(self) -> None:
        """Test that a spec with only a header covers nothing."""
        self.write(
            "starters/demo/web/e2e/specs/journey-beta.spec.ts",
            "// @journey journey-beta.md\n// @covers REQ-beta-001\n"
            "import { test } from '@playwright/test'\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("has no `test(...)` that runs", out)

    def test_a_skipped_test_does_not_cover_a_journey(self) -> None:
        """Test that a skipped test does not cover a journey."""
        self.write(
            "starters/demo/web/e2e/specs/journey-beta.spec.ts",
            "// @journey journey-beta.md\n// @covers REQ-beta-001\n"
            "import { test } from '@playwright/test'\n"
            "test.skip('beta', async ({ page }) => {})\n"
            "test.fixme('beta again', async ({ page }) => {})\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("has no `test(...)` that runs", out)

    def test_a_test_step_counts_as_an_executed_body(self) -> None:
        """Test that a test step counts as an executed body."""
        self.write(
            "starters/demo/web/e2e/specs/journey-beta.spec.ts",
            "// @journey journey-beta.md\n// @covers REQ-beta-001\n"
            "import { test } from '@playwright/test'\n"
            "test('beta', async ({ page }) => {\n"
            "  await test.step('one', async () => {})\n"
            "})\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(code, 0, f"{out}{err}")


class TestSourceScan(GateCase):
    def scan(self, content: str) -> tuple[int, str]:
        self.write("starters/demo/web/e2e/fixtures/probe.ts", content)
        code, out, _ = self.run_gate()
        return code, out

    def test_bare_fetch_is_a_finding(self) -> None:
        """Test that bare fetch is a finding."""
        code, out = self.scan("await fetch('/api/v1/auth/me')\n")
        self.assertEqual(code, 1)
        self.assertIn("direct HTTP", out)

    def test_environment_fetch_is_a_finding(self) -> None:
        """Test that environment fetch is a finding."""
        code, out = self.scan("await window.fetch('/api')\n")
        self.assertEqual(code, 1)
        self.assertIn("direct HTTP", out)

    def test_xml_http_request_is_a_finding(self) -> None:
        """Test that xml http request is a finding."""
        code, out = self.scan("const xhr = new XMLHttpRequest()\n")
        self.assertEqual(code, 1)
        self.assertIn("direct HTTP", out)

    def test_a_banned_module_import_is_a_finding(self) -> None:
        """Test that a banned module import is a finding."""
        code, out = self.scan("import axios from 'axios'\n")
        self.assertEqual(code, 1)
        self.assertIn("direct HTTP", out)

    def test_a_side_effect_banned_module_import_is_a_finding(self) -> None:
        """Test that a side effect banned module import is a finding."""
        code, out = self.scan("import 'node-fetch'\n")
        self.assertEqual(code, 1)
        self.assertIn("direct HTTP", out)

    def test_evaluate_is_a_finding(self) -> None:
        """Test that evaluate is a finding."""
        code, out = self.scan("await page.evaluate(() => document.body.innerHTML)\n")
        self.assertEqual(code, 1)
        self.assertIn("in-page script evaluation", out)

    def test_route_interception_is_a_finding(self) -> None:
        """Test that route interception is a finding."""
        code, out = self.scan("await page.route('**/*', (route) => route.abort())\n")
        self.assertEqual(code, 1)
        self.assertIn("route interception", out)

    def test_the_page_request_context_is_a_finding(self) -> None:
        """Test that the page request context is a finding."""
        code, out = self.scan("await page.request.get('/api/v1/auth/me')\n")
        self.assertEqual(code, 1)
        self.assertIn("API request context", out)

    def test_the_request_fixture_is_a_finding(self) -> None:
        """Test that the request fixture is a finding."""
        code, out = self.scan("await request.post('/api/v1/auth/login', { data: {} })\n")
        self.assertEqual(code, 1)
        self.assertIn("API request context", out)

    def test_connect_over_cdp_is_a_finding(self) -> None:
        """Test that connect over cdp is a finding."""
        code, out = self.scan("await chromium.connectOverCDP(process.env.CDP_URL)\n")
        self.assertEqual(code, 1)
        self.assertIn("CDP side channel", out)

    def test_a_non_webauthn_cdp_command_is_a_finding(self) -> None:
        """Test that a non webauthn cdp command is a finding."""
        code, out = self.scan("await session.send('Network.enable')\n")
        self.assertEqual(code, 1)
        self.assertIn("outside the WebAuthn.* family", out)
        self.assertIn("Network", out)

    def test_a_dynamic_cdp_command_is_a_finding(self) -> None:
        """Test that a dynamic cdp command is a finding."""
        code, out = self.scan("await cdpSession.send(command)\n")
        self.assertEqual(code, 1)
        self.assertIn("non-literal command", out)

    def test_the_webauthn_virtual_authenticator_family_is_allowed(self) -> None:
        """Test that the webauthn virtual authenticator family is allowed."""
        code, out = self.scan(
            "const session = await context.newCDPSession(page)\n"
            "await session.send('WebAuthn.enable', { enableUI: true })\n"
            "await session.send('WebAuthn.addVirtualAuthenticator', { options: {} })\n"
        )
        self.assertEqual(code, 0, out)

    def test_a_non_cdp_send_is_allowed(self) -> None:
        """Test that a non cdp send is allowed."""
        code, out = self.scan("ws.send('hello')\n")
        self.assertEqual(code, 0, out)

    def test_a_comment_mentioning_a_forbidden_token_is_not_a_finding(self) -> None:
        """Test that a comment mentioning a forbidden token is not a finding."""
        code, out = self.scan("// never call fetch('/api') or page.evaluate here\nconst x = 1\n")
        self.assertEqual(code, 0, out)

    def test_a_line_comment_inside_a_string_does_not_hide_the_rest(self) -> None:
        """Test that a line comment inside a string does not hide the rest."""
        code, out = self.scan(
            "const tag = 'a//b'\nawait fetch('/api/v1/auth/me')\n"
        )
        self.assertEqual(code, 1)
        self.assertIn("direct HTTP", out)

    def test_a_block_comment_inside_a_string_does_not_hide_the_middle(self) -> None:
        """Test that a block comment inside a string does not hide the middle."""
        code, out = self.scan(
            "const open = '/*'\nawait fetch('/api/v1/auth/me')\nconst close = '*/'\n"
        )
        self.assertEqual(code, 1)
        self.assertIn("direct HTTP", out)

    def test_a_websocket_construction_is_a_finding(self) -> None:
        """Test that a websocket construction is a finding."""
        code, out = self.scan("const socket = new WebSocket('ws://127.0.0.1:1')\n")
        self.assertEqual(code, 1)
        self.assertIn("direct HTTP", out)


class TestLayering(GateCase):
    def layer_scan(self, relative: str, content: str) -> tuple[int, str]:
        self.write(relative, content)
        code, out, _ = self.run_gate()
        return code, out

    def test_a_helper_may_not_spawn_a_process(self) -> None:
        """Test that a helper may not spawn a process."""
        code, out = self.layer_scan(
            "starters/demo/web/e2e/helpers/probe.ts",
            "import { execFile } from 'node:child_process'\nexport const x = execFile\n",
        )
        self.assertEqual(code, 1)
        self.assertIn("may not import `node:child_process`", out)

    def test_a_spec_may_not_write_a_file(self) -> None:
        """Test that a spec may not write a file."""
        code, out = self.layer_scan(
            "starters/demo/web/e2e/specs/writer.spec.ts",
            "import { writeFileSync } from 'node:fs'\n"
            "writeFileSync('/tmp/x', 'y')\n",
        )
        self.assertEqual(code, 1)
        self.assertIn("filesystem write", out)

    def test_a_helper_may_read_a_file(self) -> None:
        """Test that a helper may read a file."""
        code, out = self.layer_scan(
            "starters/demo/web/e2e/helpers/bundles.ts",
            "import { readFileSync } from 'node:fs'\n"
            "export const x = readFileSync('x', 'utf8')\n",
        )
        self.assertEqual(code, 0, out)

    def test_a_spec_may_not_open_a_database_client(self) -> None:
        """Test that a spec may not open a database client."""
        code, out = self.layer_scan(
            "starters/demo/web/e2e/specs/db.spec.ts",
            "import Database from 'better-sqlite3'\n",
        )
        self.assertEqual(code, 1)
        self.assertIn("may not import `better-sqlite3`", out)

    def test_a_helper_may_not_read_the_process_log_text(self) -> None:
        """Test that a helper may not read the process log text."""
        code, out = self.layer_scan(
            "starters/demo/web/e2e/helpers/probe.ts",
            "export function read(server: { log: { text: string } }) {\n"
            "  return server.log.lines\n"
            "}\n",
        )
        self.assertEqual(code, 1)
        self.assertIn("reads the process log", out)

    def test_a_helper_may_not_import_the_logfmt_parser(self) -> None:
        """Test that a helper may not import the logfmt parser."""
        code, out = self.layer_scan(
            "starters/demo/web/e2e/helpers/probe.ts",
            "import { parseLogfmt } from '../fixtures/log'\n"
            "export const x = parseLogfmt\n",
        )
        self.assertEqual(code, 1)
        self.assertIn("may not import parseLogfmt", out)

    def test_a_helper_may_import_the_verification_reader(self) -> None:
        """Test that a helper may import the verification reader."""
        code, out = self.layer_scan(
            "starters/demo/web/e2e/helpers/code.ts",
            "import { waitForVerificationCode } from '../fixtures/log'\n"
            "export const x = waitForVerificationCode\n",
        )
        self.assertEqual(code, 0, out)

    def test_fixtures_may_assemble_the_environment(self) -> None:
        """Test that fixtures may assemble the environment."""
        code, out = self.layer_scan(
            "starters/demo/web/e2e/fixtures/assemble.ts",
            "import { execFile } from 'node:child_process'\n"
            "import { mkdtemp, writeFile } from 'node:fs/promises'\n"
            "export const x = { execFile, mkdtemp, writeFile }\n",
        )
        self.assertEqual(code, 0, out)

    def test_only_the_log_fixture_may_parse_logfmt(self) -> None:
        """Test that only the log fixture may parse logfmt."""
        code, out = self.layer_scan(
            "starters/demo/web/e2e/fixtures/probe.ts",
            "export function parse(line: string) {\n"
            "  const match = /verification_code=(\\d+)/.exec(line)\n"
            "  return match\n"
            "}\n",
        )
        self.assertEqual(code, 1)
        self.assertIn("only fixtures/log.ts may read `verification_code`", out)


class TestArtifactsAndEntryPoint(GateCase):
    def test_an_output_dir_outside_tmp_is_a_finding(self) -> None:
        """Test that an output dir outside tmp is a finding."""
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            "import { defineConfig } from '@playwright/test'\n"
            "export default defineConfig({ outputDir: './test-results' })\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("not under /tmp", out)

    def test_a_missing_output_dir_is_a_finding(self) -> None:
        """Test that a missing output dir is a finding."""
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            "import { defineConfig } from '@playwright/test'\n"
            "export default defineConfig({ testDir: './specs' })\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("no literal outputDir", out)

    def test_an_html_output_folder_inside_the_tree_is_a_finding(self) -> None:
        """Test that an html output folder inside the tree is a finding."""
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            "import { defineConfig } from '@playwright/test'\n"
            "export default defineConfig({\n"
            "  outputDir: '/tmp/speed-e2e/artifacts',\n"
            "  reporter: [['html', { outputFolder: './playwright-report' }]],\n"
            "})\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("outputFolder `./playwright-report` is not under /tmp", out)

    def test_a_reporter_output_file_inside_the_tree_is_a_finding(self) -> None:
        """Test that a reporter output file inside the tree is a finding."""
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            "import { defineConfig } from '@playwright/test'\n"
            "export default defineConfig({\n"
            "  outputDir: '/tmp/speed-e2e/artifacts',\n"
            "  reporter: [['junit', { outputFile: 'results.xml' }]],\n"
            "})\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("outputFile `results.xml` is not under /tmp", out)

    def test_artifacts_may_compose_the_harness_per_run_root(self) -> None:
        """Test that artifacts may compose the harness per run root."""
        self.write(
            "starters/demo/web/e2e/fixtures/paths.ts",
            "export const ARTIFACT_ROOT = `/tmp/speed-e2e-${process.pid}`\n",
        )
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            "import { ARTIFACT_ROOT } from './fixtures/paths'\n"
            "import { defineConfig } from '@playwright/test'\n"
            "export default defineConfig({\n"
            "  outputDir: `${ARTIFACT_ROOT}/artifacts`,\n"
            "  reporter: [['html', { outputFolder: `${ARTIFACT_ROOT}/report` }]],\n"
            "})\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(code, 0, f"{out}{err}")

    def test_a_per_run_root_outside_tmp_is_a_finding(self) -> None:
        """Test that a per run root outside tmp is a finding."""
        self.write(
            "starters/demo/web/e2e/fixtures/paths.ts",
            "export const ARTIFACT_ROOT = './artifacts'\n",
        )
        self.write(
            "starters/demo/web/e2e/playwright.config.ts",
            "import { ARTIFACT_ROOT } from './fixtures/paths'\n"
            "import { defineConfig } from '@playwright/test'\n"
            "export default defineConfig({\n"
            "  outputDir: `${ARTIFACT_ROOT}/artifacts`,\n"
            "})\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("is not under /tmp", out)

    def test_a_missing_e2e_target_is_a_finding(self) -> None:
        """Test that a missing e2e target is a finding."""
        self.write(
            "Makefile",
            ".PHONY: e2e-install check\n\ne2e-install:\n\t@echo install\n\n"
            "check:\n\t@echo build\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("no `e2e` target", out)

    def test_a_missing_e2e_install_target_is_a_finding(self) -> None:
        """Test that a missing e2e install target is a finding."""
        self.write(
            "Makefile",
            ".PHONY: e2e check\n\ne2e:\n\t@echo run\n\ncheck:\n\t@echo build\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("no `e2e-install` target", out)

    def test_check_must_not_invoke_the_e2e_tier(self) -> None:
        """Test that check must not invoke the e2e tier."""
        self.write(
            "Makefile",
            ".PHONY: e2e e2e-install check\n\ne2e-install:\n\t@echo install\n\n"
            "e2e:\n\t@echo run\n\ncheck:\n\t@echo build\n\t@$(MAKE) e2e\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("`check` target invokes the e2e tier", out)

    def test_check_must_not_reach_the_tier_through_playwright(self) -> None:
        """Test that check must not reach the tier through playwright."""
        self.write(
            "Makefile",
            ".PHONY: e2e e2e-install check\n\ne2e-install:\n\t@echo install\n\n"
            "e2e:\n\t@echo run\n\ncheck:\n\t@echo build\n"
            "\t@pnpm -C web --filter demo-web exec playwright test "
            "--config e2e/playwright.config.ts\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("`check` target invokes the e2e tier", out)

    def test_check_must_not_reach_the_type_check_either(self) -> None:
        """Test that check must not reach the type check either."""
        self.write(
            "Makefile",
            ".PHONY: e2e e2e-install check\n\ne2e-install:\n\t@echo install\n\n"
            "e2e:\n\t@echo run\n\ncheck:\n\t@echo build\n"
            "\t@pnpm -C web --filter demo-web run typecheck:e2e\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("`check` target invokes the e2e tier", out)


class TestExitCodes(GateCase):
    def test_a_root_that_is_not_a_directory_is_an_infrastructure_error(self) -> None:
        """Test that a root that is not a directory is an infrastructure error."""
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", str(pathlib.Path(self.root, "absent"))])
        self.assertEqual(code, 2)
        self.assertIn("not a directory", err.getvalue())

    def test_a_missing_journeys_directory_is_an_infrastructure_error(self) -> None:
        """Test that a missing journeys directory is an infrastructure error."""
        self.root = tempfile.mkdtemp(prefix="e2e-honesty-empty-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("is missing", err)

    def test_a_journeys_directory_with_no_document_is_an_infrastructure_error(self) -> None:
        """Test that a journeys directory with no document is an infrastructure error."""
        for name in ("journey-alpha.md", "journey-beta.md"):
            pathlib.Path(self.root, "docs/prd/journeys", name).unlink()
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("checked nothing", err)

    def test_a_missing_e2e_directory_is_an_infrastructure_error(self) -> None:
        """Test that a missing e2e directory is an infrastructure error."""
        shutil.rmtree(pathlib.Path(self.root, "starters"))
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("harness is gone", err)

    def test_an_unparsable_registry_is_an_infrastructure_error(self) -> None:
        """Test that an unparsable registry is an infrastructure error."""
        self.write(
            "starters/demo/web/e2e/journeys.ts",
            "export const journeys = [\n  { journey: 'journey-beta.md'\n",
        )
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("never closes", err)

    def test_a_registry_without_a_declaration_is_an_infrastructure_error(self) -> None:
        """Test that a registry without a declaration is an infrastructure error."""
        self.write("starters/demo/web/e2e/journeys.ts", "export const other = []\n")
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("no `const journeys", err)


if __name__ == "__main__":
    unittest.main()
