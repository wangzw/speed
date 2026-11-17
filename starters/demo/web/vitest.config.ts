/**
 * The demo host's vitest configuration: a jsdom environment with the shared
 * jest-dom matchers and RTL cleanup (src/test-utils/setup.ts), over the
 * workspace source aliases.
 *
 * The aliases come from the workspace's single package map
 * (web/scripts/speed-aliases.mjs), the same map the vite config imports. The
 * tsconfig paths section carries that map's projection for this app -- the
 * workspace's private test-support entries are absent because the app carries
 * its own copies under src/test-utils -- and src/alias-manifest.test.ts pins
 * the projection. Every @speed/* specifier resolves onto its sibling's live
 * src, because a sibling's dist/ is never committed and is not guaranteed to
 * exist when tests run.
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
    // This host builds its own test block rather than taking the workspace
    // preset, so the preset's fixed reporters (web/vitest.shared.ts) are
    // taken from the same helper here: without them Vitest 4 falls back to
    // MinimalReporter under an agent environment and drops a passing case's
    // console output, making the log depend on who started the run; under
    // GitHub Actions the helper also appends the annotation reporter.
    reporters: sharedReporters(),
    // The assembled app renders times under the account's display zone and
    // falls to the browser zone before a profile arrives; pinning the
    // browser zone keeps every rendered time independent of the runner.
    // See TEST_BROWSER_TIME_ZONE.
    env: { TZ: TEST_BROWSER_TIME_ZONE },
    // The workspace's scaled per-case budget (web/vitest.shared.ts, the
    // caller's GATE_TIME_SCALE applied). These cases drive the assembled
    // program -- route changes, settles and announcements -- and vitest's
    // 5s default fails them on the clock when the workspace's suites run
    // in parallel, not on an assertion. The hook budget is the same value.
    testTimeout: sharedTimeoutMs(),
    hookTimeout: sharedTimeoutMs(),
    // Vitest's own defaults are restated because naming `exclude` replaces
    // them rather than extending them. src/a11y/** (the route-level
    // accessibility sweep) is excluded here and run by its own config
    // (vitest.a11y.config.ts) as a separate frontend-gate leg, so the two
    // never scan the same routes twice. e2e/specs/** is the Playwright tier
    // (e2e/playwright.config.ts): its `*.spec.ts` cases drive the built
    // backend in a real Chromium and are run by `make e2e`, not by vitest.
    // e2e/fixtures/** stays in scope for its `*.test.ts` suites -- the
    // harness's own path and retention logic is plain Node code that needs
    // no browser -- while its Playwright-only sources carry no test suffix.
    exclude: [
      '**/node_modules/**',
      '**/dist/**',
      'src/a11y/**',
      'e2e/specs/**',
      '**/*.browser.test.tsx',
    ],
  },
})
