/**
 * ui-kit test configuration: jsdom, the shared setup and the workspace
 * source aliases, so tests resolve siblings' live `src` entries.
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
    // The fallback-chain cases need a browser zone that differs from the terminal tier.
    env: { TZ: TEST_BROWSER_TIME_ZONE },
  }),
})
