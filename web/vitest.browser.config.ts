/**
 * The browser tier's Vitest configuration.
 *
 * The workspace's unit suites run under jsdom, which evaluates neither
 * media nor container queries, lays nothing out and cannot tell whether a
 * node is keyboard-focusable. The browser tier runs the cases whose
 * subject is real geometry, focus order or focusability in Chromium
 * instead, through Vitest's browser mode with the Playwright provider --
 * the fewest dependency families that reuse the existing workspace
 * aliases, theme and i18n fixtures.
 *
 * Cases live beside the component under test as `*.browser.test.tsx`.
 * They are excluded from every package's default `test` leg by the shared
 * preset (web/vitest.shared.ts) and from the root `make web-browser`
 * entry only -- never from `make check`. Run them with
 * `make web-browser` (see web/README.md for the one-line Chromium
 * install).
 */
import { configDefaults, defineConfig } from 'vitest/config'
import { playwright } from '@vitest/browser-playwright'
import { speedAliases } from './scripts/speed-aliases.mjs'
import { sharedTestOptions } from './vitest.shared.ts'

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  test: sharedTestOptions({
    // The browser cases of every package, from the root config: one
    // Chromium instance for the whole tier rather than one per package.
    include: ['packages/*/src/**/*.browser.test.tsx'],
    // The shared preset excludes browser cases from the unit legs; the
    // tier that owns them keeps Vitest's ordinary excludes only.
    exclude: configDefaults.exclude,
    setupFiles: ['./test-utils/src/browser-setup.ts'],
    // The workspace's one testTimeout (20s, web/vitest.shared.ts) applies
    // here too; the cases wait on events, so it only bounds a hang.
    // The browser client's dependency optimizer pre-bundles
    // @testing-library/react together with its own copy of React's
    // runtime, while the externalized packages under test (MUI, emotion)
    // import React from node_modules -- two instances in one page, and
    // every hook call in the second copy fails with a null dispatcher.
    // Serving the dependencies unbundled keeps one React for both.
    deps: {
      optimizer: {
        client: { enabled: false },
      },
    },
    browser: {
      enabled: true,
      provider: playwright(),
      headless: true,
      instances: [{ browser: 'chromium' }],
    },
  }),
})
