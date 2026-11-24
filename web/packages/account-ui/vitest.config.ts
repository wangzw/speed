/**
 * account-ui test configuration: jsdom with the shared setup and the
 * workspace source aliases.
 *
 * Aliases resolve onto a sibling's live src entry, because a sibling's
 * dist/ is not committed and not guaranteed to exist when tests run.
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
    // A browser zone that differs from the terminal tier.
    env: { TZ: TEST_BROWSER_TIME_ZONE },
  }),
})
