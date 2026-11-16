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
import { TEST_BROWSER_TIME_ZONE } from '../../../web/vitest.shared.ts'

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
    // The workspace preset's per-case budget (web/vitest.shared.ts): a sweep
    // renders every route of the area it scans, so vitest's 5s default would
    // fail it on the clock under parallel load.
    testTimeout: 20_000,
    // The verbose reporter is deliberate: the default reporter emits a test's
    // console output (the gate's advisory warnings and its closing report)
    // only when that test failed, so a green run would hide the registered
    // moderate/minor findings. Verbose surfaces every swept route and its
    // warnings, so the report the gate promises is actually printed.
    reporter: 'verbose',
  },
})
