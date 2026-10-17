/**
 * auth-core test configuration: the plain node environment by default
 * (the session state machine is pure async logic -- no DOM); the DOM
 * suites opt in per file with the @vitest-environment jsdom pragma.
 * Timeout and coverage come from the workspace preset
 * (web/vitest.shared.ts).
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
  test: sharedTestOptions(),
})
