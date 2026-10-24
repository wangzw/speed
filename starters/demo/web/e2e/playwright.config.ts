/**
 * Playwright configuration for the demo starter's end-to-end tier.
 *
 * The tier drives the built demo binary -- a real sqlite file, real HTTP, the
 * review assembly's seeded operator -- through Chromium with nothing but mouse
 * and keyboard input. Its honesty rules live in the specs' README and are
 * enforced twice: by the eslint rule `speed/no-e2e-bypass` over this directory
 * and by `tools/check_e2e_honesty.py` over the tree.
 *
 * One worker and no parallelism: every spec starts its own backend on free
 * ports and signs its own accounts in, so journeys never share state and two
 * workers would race for the ports and for the clock a journey depends on.
 *
 * Artifacts land under the run's own /tmp root, never beside the source: a
 * failure screenshot or trace is for the operator running the tier, and the
 * honesty gate holds every artifact path to /tmp so a passing run cannot
 * leave a file in the tree. The root carries a per-run id, so two concurrent
 * runs never share an output directory (Playwright clears it on startup) or
 * the harness binary the fixtures start.
 */
import { defineConfig } from '@playwright/test'

import { ARTIFACT_ROOT } from './fixtures/paths'

export default defineConfig({
  testDir: './specs',
  globalSetup: './global-setup.ts',
  // The build runs once in the global setup, outside any test's bound. This
  // bound therefore covers one server's seed and startup, the operations
  // probes and a journey's clicks; the fixture bounds below stay strictly
  // inside it so the harness's own diagnostics (log tail, stderr) win over a
  // bare Playwright timeout. The tier stays serial for the reason in the
  // header.
  timeout: 120_000,
  workers: 1,
  fullyParallel: false,
  outputDir: `${ARTIFACT_ROOT}/artifacts`,
  reporter: [
    ['list'],
    [
      'html',
      { outputFolder: `${ARTIFACT_ROOT}/report`, open: 'never' },
    ],
  ],
  use: {
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    // A journey's expected copy is read from the en-US locale bundle, so the
    // browser is pinned to that language and to UTC rather than inheriting
    // whatever the machine running the tier happens to use.
    locale: 'en-US',
    timezoneId: 'UTC',
  },
  // Chromium is the only browser the tier needs: the journeys exercise the
  // rendered product, and the bundled Chromium (installed by `make e2e-install`)
  // is the one the browser tier already pins.
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
})
