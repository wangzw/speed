/**
 * account-ui test configuration: jsdom with the shared setup and workspace source aliases.
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
    env: { TZ: TEST_BROWSER_TIME_ZONE },
  }),
})
