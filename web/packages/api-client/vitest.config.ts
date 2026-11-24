/**
 * api-client test configuration: every suite runs in the default node
 * environment over scripted fetch. src/ imports only relative files; the
 * one workspace specifier is the suite's shared restoreRealTimers helper,
 * which the workspace's single package map (web/scripts/speed-aliases.mjs)
 * resolves onto its live source, because @speed/test-utils publishes no
 * dist. Timeout and coverage come from the workspace preset
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
