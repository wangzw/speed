/**
 * route-kit test configuration: jsdom for every suite, because the
 * package's whole surface is render-time -- a Router reads window.location
 * and a match resolves against it -- so even the pure path helpers are
 * exercised against the same environment the components run in. The
 * shared jest-dom matcher setup comes from the workspace's test-support
 * package (see test-utils/setup.ts). Timeout and coverage come from the
 * workspace preset (web/vitest.shared.ts).
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
