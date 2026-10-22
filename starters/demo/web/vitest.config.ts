/**
 * The demo host's vitest configuration: a jsdom environment with the shared
 * jest-dom matchers and RTL cleanup (src/test-utils/setup.ts), over the
 * workspace source aliases.
 *
 * The aliases come from the workspace's single package map
 * (web/scripts/speed-aliases.mjs), the same map the vite config imports. The
 * tsconfig paths section carries that map's projection for this app -- the
 * workspace's private test-support entries are absent because the app carries
 * its own copies under src/test-utils -- and src/alias-manifest.test.ts pins
 * the projection. Every @speed/* specifier resolves onto its sibling's live
 * src, because a sibling's dist/ is never committed and is not guaranteed to
 * exist when tests run.
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
    // The same per-case budget the workspace preset gives every package
    // (web/vitest.shared.ts). These cases drive the assembled program --
    // route changes, settles and announcements -- and vitest's 5s default
    // fails them on the clock when the workspace's suites run in parallel,
    // not on an assertion.
    testTimeout: 20_000,
    // Vitest's own defaults are restated because naming `exclude` replaces
    // them rather than extending them. src/a11y/** (the route-level
    // accessibility sweep) is excluded here and run by its own config
    // (vitest.a11y.config.ts) as a separate frontend-gate leg, so the two
    // never scan the same routes twice.
    exclude: ['**/node_modules/**', '**/dist/**', 'src/a11y/**', '**/*.browser.test.tsx'],
  },
})
