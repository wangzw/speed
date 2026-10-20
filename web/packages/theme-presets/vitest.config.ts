/**
 * theme-presets test configuration: the presets are dependency-free pure
 * data and their tests run in the default node environment. The one
 * aliased sibling is @speed/tokens, whose runtime symbols (defaultTokens,
 * deepMerge) the tests merge each preset onto; the alias comes from the
 * workspace's single package map so it cannot drift from the other legs.
 * Timeout and coverage come from the workspace preset
 * (web/vitest.shared.ts).
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
