/**
 * The demo host's vitest configuration: a jsdom environment with the shared
 * jest-dom matchers and RTL cleanup (src/test-utils/setup.ts), over the
 * workspace source aliases.
 *
 * The aliases come from the workspace's single package map
 * (web/scripts/speed-aliases.mjs), the same map the vite config imports and
 * the tsconfig paths section mirrors (src/alias-manifest.test.ts pins that
 * leg): every @speed/* specifier resolves onto its sibling's live src, because
 * a sibling's dist/ is never committed and is not guaranteed to exist when
 * tests run.
 */
import { defineConfig } from 'vitest/config'
import { speedAliases } from '../../../web/scripts/speed-aliases.mjs'

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-utils/setup.ts'],
    // Vitest's own defaults are restated because naming `exclude` replaces
    // them rather than extending them.
    exclude: ['**/node_modules/**', '**/dist/**'],
  },
})
