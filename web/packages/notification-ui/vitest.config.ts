/**
 * notification-ui test configuration: jsdom DOM environment with the
 * shared jest-dom matcher setup, plus the workspace source aliases.
 *
 * The aliases come from the workspace's single package map
 * (web/scripts/speed-aliases.mjs) instead of a list hand-copied here:
 * every @speed/* specifier -- subpaths included -- resolves onto its
 * sibling's live src entry, because a sibling's dist/ is never
 * committed and not guaranteed to exist when tests run, and the map is
 * the one place a specifier is added or moved, so this list cannot
 * drift from what src/ and test-utils/ import.
 */
import { defineConfig } from 'vitest/config'
import { speedAliases } from '../../scripts/speed-aliases.mjs'

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./test-utils/setup.ts'],
    // The axe-scanning cases take ~4s on their own; when the workspace's
    // test legs run concurrently (`pnpm -r test` alongside `-r lint`)
    // they exceed vitest's 5s default and fail on the clock rather than
    // on a violation. The larger budget keeps the gate repeatable.
    testTimeout: 10_000,
    coverage: {
      provider: 'v8',
      include: ['src/**'],
      reporter: ['text-summary', 'json-summary'],
      thresholds: {
        statements: 80,
        branches: 80,
      },
    },
  },
})
