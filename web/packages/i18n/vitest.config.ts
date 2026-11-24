import { defineConfig } from 'vitest/config'
import { sharedTestOptions, TEST_BROWSER_TIME_ZONE } from '../../vitest.shared.ts'

export default defineConfig({
  test: sharedTestOptions({
    env: { TZ: TEST_BROWSER_TIME_ZONE },
  }),
})
