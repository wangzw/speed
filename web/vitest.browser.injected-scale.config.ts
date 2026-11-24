// A second pass at a non-default scale: at the caller's usual 1, a setup
// that hardcoded the unscaled budget would be indistinguishable from a
// correct one.
import { fileURLToPath } from 'node:url'
import { configDefaults, defineConfig } from 'vitest/config'
import { playwright } from '@vitest/browser-playwright'
import { speedAliases } from './scripts/speed-aliases.mjs'
import { sharedTestOptions } from './vitest.shared.ts'

const webRoot = fileURLToPath(new URL('.', import.meta.url))

const INJECTED_SCALE = 2.5

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  define: {
    __SPEED_GATE_TIME_SCALE__: JSON.stringify(INJECTED_SCALE),
  },
  server: {
    watch: null,
    fs: {
      allow: [webRoot],
    },
  },
  test: sharedTestOptions({
    include: ['test-utils/src/gate-time.injected-scale.browser.test.tsx'],
    exclude: configDefaults.exclude,
    setupFiles: ['./test-utils/src/browser-setup.ts'],
    deps: {
      optimizer: {
        client: { enabled: false },
      },
    },
    browser: {
      enabled: true,
      provider: playwright(),
      headless: true,
      instances: [{ browser: 'chromium' }],
    },
  }),
})
