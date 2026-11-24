// Aliases must resolve @speed/ specifiers onto sibling src entries: dist/ is
// never committed and may not exist when tests run.
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
