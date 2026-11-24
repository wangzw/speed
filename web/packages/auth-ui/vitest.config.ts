/** auth-ui test configuration: jsdom with the shared setup, plus the
 * workspace's single source alias map. */
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
    // The fallback-chain cases need a browser zone that differs from the
    // terminal tier; see TEST_BROWSER_TIME_ZONE.
    env: { TZ: TEST_BROWSER_TIME_ZONE },
  }),
})
