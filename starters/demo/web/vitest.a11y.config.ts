/**
 * The demo host's route-level accessibility sweep configuration.
 *
 * The sweep runs as its own frontend-gate leg (the root Makefile's
 * `web-a11y`, alongside lint / typecheck / test), not inside the ordinary
 * `test` run: it renders the whole app at every route in three data states and
 * runs axe over each, which is slower than the unit suites and produces the
 * gate's own report. `vitest.config.ts` therefore excludes `src/a11y/**` and
 * this config includes only it, so the same suites never run in both legs.
 *
 * Everything else -- the jsdom environment, the shared setup, the workspace
 * source aliases -- is identical to the default config; only the file set
 * differs.
 */
import { defineConfig } from 'vitest/config'
import { speedAliases } from '../../../web/scripts/speed-aliases.mjs'
import {
  sharedReporters,
  sharedTimeoutMs,
  TEST_BROWSER_TIME_ZONE,
} from '../../../web/vitest.shared.ts'

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-utils/setup.ts'],
    // The sweep renders every route of the app, times included; pinning the
    // browser zone keeps a rendered time independent of the runner, the
    // same as the demo's unit leg (vitest.config.ts). See
    // TEST_BROWSER_TIME_ZONE.
    env: { TZ: TEST_BROWSER_TIME_ZONE },
    include: ['src/a11y/**/*.test.tsx'],
    // Vitest's own defaults are restated because naming `exclude` replaces
    // them rather than extending them. e2e/** is the Playwright tier and is
    // run by `make e2e`, never by this sweep.
    exclude: ['**/node_modules/**', '**/dist/**', 'e2e/**', '**/*.browser.test.tsx'],
    // The workspace's scaled per-case budget (web/vitest.shared.ts, the
    // caller's GATE_TIME_SCALE applied): a sweep renders every route of the
    // area it scans, so vitest's 5s default would fail it on the clock under
    // parallel load. The hook budget is the same value.
    testTimeout: sharedTimeoutMs(),
    hookTimeout: sharedTimeoutMs(),
    // The verbose reporter is deliberate: it names every swept route, so the
    // report the gate promises shows which routes were scanned, and it prints
    // each case's annotations beside its name. The default reporter prints a
    // passing case's console output too (its `silent` default is false), but
    // it names only the failing cases, so a green sweep's routes would not
    // appear in the log. The configuration key is `reporters` (plural); the
    // singular `reporter` is the CLI alias, which Vitest merges into the
    // options, and this file keeps the plural spelling. `sharedReporters`
    // adds the GitHub Actions annotation reporter on CI.
    reporters: sharedReporters('verbose'),
  },
})
