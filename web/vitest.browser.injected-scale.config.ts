/**
 * The browser tier's injected-scale pass.
 *
 * The main tier (web/vitest.browser.config.ts) injects the caller's
 * `GATE_TIME_SCALE`, which is normally 1. At that value a setup that
 * hardcoded the unscaled async budget would configure exactly what a
 * correct setup does, so no case there can tell the two apart. This pass
 * runs `test-utils/src/gate-time.injected-scale.browser.test.tsx` with a
 * fixed 2.5 injected instead, and the case asserts the setup configured
 * the budget 2.5 yields; a setup that ignores the injection, or reads the
 * environment (where the browser page has no process global), leaves the
 * budget at the unscaled value and fails.
 *
 * The fixed scale reaches nothing else: this config's include names one
 * file, and its test and hook budgets still come from `sharedTestOptions`
 * at the caller's scale, so the main tier's cases keep their own limits.
 * `make web-browser` runs this pass after the main one.
 */
import { fileURLToPath } from 'node:url'
import { configDefaults, defineConfig } from 'vitest/config'
import { playwright } from '@vitest/browser-playwright'
import { speedAliases } from './scripts/speed-aliases.mjs'
import { sharedTestOptions } from './vitest.shared.ts'

/** This package-library root. */
const webRoot = fileURLToPath(new URL('.', import.meta.url))

/** The scale this pass injects, deliberately not the default of 1. */
const INJECTED_SCALE = 2.5

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  define: {
    __SPEED_GATE_TIME_SCALE__: JSON.stringify(INJECTED_SCALE),
  },
  server: {
    watch: null,
    fs: {
      allow: [webRoot],
    },
  },
  test: sharedTestOptions({
    include: ['test-utils/src/gate-time.injected-scale.browser.test.tsx'],
    exclude: configDefaults.exclude,
    setupFiles: ['./test-utils/src/browser-setup.ts'],
    deps: {
      optimizer: {
        client: { enabled: false },
      },
    },
    browser: {
      enabled: true,
      provider: playwright(),
      headless: true,
      instances: [{ browser: 'chromium' }],
    },
  }),
})
