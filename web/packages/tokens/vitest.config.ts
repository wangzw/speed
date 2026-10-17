/**
 * tokens test configuration: the design tokens are dependency-free pure
 * data and their tests run in the default node environment with no
 * aliases, since src/ imports only relative files. Timeout and coverage
 * come from the workspace preset (web/vitest.shared.ts).
 */
import { defineConfig } from 'vitest/config'
import { sharedTestOptions } from '../../vitest.shared.ts'

export default defineConfig({
  test: sharedTestOptions(),
})
