// Separate from web/vitest.config.ts: a config that declares test.projects
// runs no tests of its own. Reporters are pinned here because Vitest honours
// them only from the root config.
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'
import { sharedTestOptions } from './vitest.shared.ts'

const webRoot = dirname(fileURLToPath(import.meta.url))
const demoWebRoot = join(webRoot, '..', 'starters', 'demo', 'web')

export default defineConfig({
  server: {
    fs: {
      // The demo host lives outside web/, which the default allow list
      // covers; without this its project loads but its files are refused.
      allow: [demoWebRoot],
    },
  },
  test: sharedTestOptions({
    projects: [
      'packages/*/vitest.config.ts',
      'test-utils/vitest.config.ts',
      '../starters/demo/web/vitest.config.ts',
    ],
  }),
})
