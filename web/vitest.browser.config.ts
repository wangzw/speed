import { fileURLToPath } from 'node:url'
import { configDefaults, defineConfig } from 'vitest/config'
import { playwright } from '@vitest/browser-playwright'
import { speedAliases } from './scripts/speed-aliases.mjs'
import { gateTimeScale, sharedTestOptions } from './vitest.shared.ts'

const webRoot = fileURLToPath(new URL('.', import.meta.url))
const demoWebRoot = fileURLToPath(
  new URL('../starters/demo/web', import.meta.url),
)

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  // Parsed in Node, where the environment is real, and injected as a
  // compile-time constant; the browser page has no process global.
  define: {
    __SPEED_GATE_TIME_SCALE__: JSON.stringify(gateTimeScale()),
  },
  server: {
    // No hot reload: a write under the tree mid-run reloads the page under a
    // case that is mid-assertion and reads as a flaky case elsewhere.
    watch: null,
    // Naming fs.allow replaces Vite's workspace-root default, so the demo
    // host outside web/ is named beside it.
    fs: {
      allow: [webRoot, demoWebRoot],
    },
  },
  test: sharedTestOptions({
    include: [
      'packages/*/src/**/*.browser.test.tsx',
      'test-utils/src/**/*.browser.test.tsx',
      '../starters/demo/web/src/**/*.browser.test.tsx',
    ],
    exclude: [
      ...configDefaults.exclude,
      'test-utils/src/gate-time.injected-scale.browser.test.tsx',
    ],
    setupFiles: ['./test-utils/src/browser-setup.ts'],
    // Keep dependencies unbundled: the optimizer would pre-bundle its own
    // React beside the one MUI and emotion import, and the second copy's
    // hook calls fail with a null dispatcher.
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
