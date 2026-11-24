/**
 * The single Vitest process the frontend gate's unit-test leg runs.
 *
 * Not merged into web/vitest.config.ts, which backs the rule-test leg
 * (`vitest run eslint-rules/ scripts/`): a config that declares
 * `test.projects` runs no tests of its own.
 *
 * The reporters are pinned here on the root config.
 * Vitest honours a project's reporters only from the root.
 * The a11y cases stay out through the demo host's own hand-written
 * `src/a11y/**` exclude, the browser cases through the shared preset's
 * `*.browser.test.tsx` exclude. Coverage is not enabled here; it stays
 * the opt-in per-package run.
 *
 * The projects are config file paths because the configuration guard
 * compares each entry against a member's `vitest.config.ts` path.
 */
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'
import { sharedTestOptions } from './vitest.shared.ts'

const webRoot = dirname(fileURLToPath(import.meta.url))
const demoWebRoot = join(webRoot, '..', 'starters', 'demo', 'web')

export default defineConfig({
  server: {
    fs: {
      // The allow list covers only web/ by default; Vitest appends its
      // own entries, and the demo host (a member outside web/) is not one
      // of them. Without this entry that project loads but its files are
      // refused, its setup file first.
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
