/**
 * i18n test configuration: the i18n host's tests run in the default node
 * environment (react-i18next instance creation needs no DOM) and src/
 * imports only relative files, so no aliases are needed. Timeout and
 * coverage come from the workspace preset (web/vitest.shared.ts).
 */
import { defineConfig } from 'vitest/config'
import { sharedTestOptions } from '../../vitest.shared.ts'

export default defineConfig({
  test: sharedTestOptions(),
})
