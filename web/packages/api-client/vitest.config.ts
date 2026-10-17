/**
 * api-client test configuration: every suite runs in the default node
 * environment over scripted fetch. src/ imports only relative files, so
 * no aliases are needed. Timeout and coverage come from the workspace
 * preset (web/vitest.shared.ts).
 */
import { defineConfig } from 'vitest/config'
import { sharedTestOptions } from '../../vitest.shared.ts'

export default defineConfig({
  test: sharedTestOptions(),
})
