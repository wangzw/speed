import { defineConfig } from 'vitest/config'
import { speedAliases } from '../scripts/speed-aliases.mjs'
import { sharedTestOptions, TEST_BROWSER_TIME_ZONE } from '../vitest.shared.ts'

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  test: sharedTestOptions({
    environment: 'node',
    // render.test.ts reads the engine's resolved zone; without the pin it
    // reads the host's zone and the assertion passes only by accident.
    env: { TZ: TEST_BROWSER_TIME_ZONE },
    // The consumers' suites cover this src; a threshold would demand a duplicate suite.
    coverage: {
      thresholds: undefined,
    },
  }),
})
