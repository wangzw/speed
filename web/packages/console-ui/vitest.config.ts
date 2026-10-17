/**
 * console-ui test configuration: jsdom DOM environment with the shared
 * jest-dom matcher setup, plus the workspace source aliases. Timeout and
 * coverage come from the workspace preset (web/vitest.shared.ts).
 *
 * The page cases drive userEvent through MUI menus, dialogs, debounced
 * search reads and data tables; the shared budget covers them under the
 * parallel workspace run. The budget is the wait, not the work: a genuine
 * hang still fails, twenty seconds later.
 *
 * The aliases come from the workspace's single package map
 * (web/scripts/speed-aliases.mjs): every @speed/* specifier -- subpaths
 * included -- resolves onto its sibling's live src entry, because a
 * sibling's dist/ is never committed and not guaranteed to exist when
 * tests run. The map is the one place a specifier is added or moved, so
 * this list cannot drift from what src/ and test-utils/ import.
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
