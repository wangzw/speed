/**
 * admin-ui test configuration: jsdom with the shared jest-dom setup and the
 * workspace source aliases. Timeout and coverage come from the workspace preset.
 */
import { defineConfig } from 'vitest/config'
import { speedAliases } from '../../scripts/speed-aliases.mjs'
import { sharedTestOptions, TEST_BROWSER_TIME_ZONE } from '../../vitest.shared.ts'

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  test: sharedTestOptions({
    environment: 'jsdom',
    setupFiles: ['./test-utils/setup.ts'],
    // A browser zone that differs from the zone the cases pass; see TEST_BROWSER_TIME_ZONE.
    env: { TZ: TEST_BROWSER_TIME_ZONE },
  }),
})
