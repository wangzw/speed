/**
 * org-create-ui test configuration: jsdom DOM environment with the shared
 * jest-dom matcher setup, plus the workspace source aliases. Timeout and
 * coverage come from the workspace preset (web/vitest.shared.ts).
 *
 * The aliases come from the workspace's single package map
 * (web/scripts/speed-aliases.mjs): every @speed/* specifier resolves onto
 * its sibling's live src entry, because a sibling's dist/ is never
 * committed and not guaranteed to exist when tests run.
 */
import { defineConfig } from 'vitest/config'
import { speedAliases } from '../../scripts/speed-aliases.mjs'
import { sharedTestOptions } from '../../vitest.shared.ts'

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  test: sharedTestOptions({
    environment: 'jsdom',
    setupFiles: ['./test-utils/setup.ts'],
  }),
})
