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
    reporters: sharedReporters(),
    env: { TZ: TEST_BROWSER_TIME_ZONE },
    testTimeout: sharedTimeoutMs(),
    hookTimeout: sharedTimeoutMs(),
    exclude: [
      '**/node_modules/**',
      '**/dist/**',
      'src/a11y/**',
      'e2e/specs/**',
      '**/*.browser.test.tsx',
    ],
  },
})
