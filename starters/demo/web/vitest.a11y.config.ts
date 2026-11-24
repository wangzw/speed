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
    env: { TZ: TEST_BROWSER_TIME_ZONE },
    include: ['src/a11y/**/*.test.tsx'],
    exclude: ['**/node_modules/**', '**/dist/**', 'e2e/**', '**/*.browser.test.tsx'],
    testTimeout: sharedTimeoutMs(),
    hookTimeout: sharedTimeoutMs(),
    reporters: sharedReporters('verbose'),
  },
})
