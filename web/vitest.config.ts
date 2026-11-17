/**
 * The root Vitest config for the leg that runs the workspace's own tests.
 *
 * The eslint-rules unit tests and the workspace configuration guards
 * (web/scripts/*.test.mjs) live outside every package, so no package's
 * config covers them. Without a config here, `vitest run eslint-rules/
 * scripts/` runs on Vitest's fixed 5s default, which the repository's own
 * structure guard cannot see and which does not scale with the caller's
 * GATE_TIME_SCALE. This config gives that leg the same shared preset every
 * package takes (web/vitest.shared.ts), so the whole frontend gate's wait
 * budgets come from one place.
 */
import { defineConfig } from 'vitest/config'
import { sharedTestOptions } from './vitest.shared.ts'

export default defineConfig({
  test: sharedTestOptions(),
})
