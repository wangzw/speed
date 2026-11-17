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
 *
 * `make web-browser` also runs a second, short pass
 * (web/vitest.browser.injected-scale.config.ts) that injects a fixed scale
 * of 2.5 instead of the caller's value, so the setup's scale handling is
 * exercised at a value other than the default; see that file's header.
 */
import { fileURLToPath } from 'node:url'
import { configDefaults, defineConfig } from 'vitest/config'
import { playwright } from '@vitest/browser-playwright'
import { speedAliases } from './scripts/speed-aliases.mjs'
import { gateTimeScale, sharedTestOptions } from './vitest.shared.ts'

/** This package-library root; the tier's own setup and aliases live under it. */
const webRoot = fileURLToPath(new URL('.', import.meta.url))
/**
 * The demo host's directory. It is a workspace member (web/pnpm-workspace.yaml)
 * but lives outside web/, exactly where a delivered consumer project lives, so
 * the tier's include and Vite's file access must name it. Its route-level
 * Label in Name sweep (src/a11y/routes.browser.test.tsx) runs here, in the same
 * Chromium instance, rather than in a second browser entry point.
 */
const demoWebRoot = fileURLToPath(
  new URL('../starters/demo/web', import.meta.url),
)

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  // The browser page has no process global, so the caller's scale cannot be
  // read there. It is parsed here, in Node, where the environment is real,
  // and handed to the browser setup as a compile-time constant; a bad value
  // fails the config load before a browser starts.
  define: {
    __SPEED_GATE_TIME_SCALE__: JSON.stringify(gateTimeScale()),
  },
  // The tier must not hot-reload. Vite's dev server watches the tree, so a
  // write anywhere under it while the tier runs -- a parallel session's edit,
  // or a regeneration of a generated file -- reloads the page under a case
  // that is mid-assertion. The symptoms are a detached frame or "failed to
  // fetch dynamically imported module" in whichever file was importing at
  // that instant, which reads as a flaky case in an unrelated package. A test
  // server has nothing to serve newer than the tree it started from.
  server: {
    watch: null,
    // Naming fs.allow replaces Vite's workspace-root default, so the demo host
    // (outside web/) is named beside it. Vite serves the demo test files and
    // its src through this allowance.
    fs: {
      allow: [webRoot, demoWebRoot],
    },
  },
  test: sharedTestOptions({
    // The browser cases of every package, the shared setup's own browser
    // case, plus the demo host's route-level sweep, from the root config:
    // one Chromium instance for the whole tier rather than one per package.
    // The injected-scale case runs in its own config, not here: this pass
    // uses the caller's scale, and that case asserts a value other than it.
    include: [
      'packages/*/src/**/*.browser.test.tsx',
      'test-utils/src/**/*.browser.test.tsx',
      '../starters/demo/web/src/**/*.browser.test.tsx',
    ],
    // The shared preset excludes browser cases from the unit legs; the
    // tier that owns them keeps Vitest's ordinary excludes, plus the file
    // that asserts a fixed non-default scale and runs under the separate
    // injected-scale config.
    exclude: [
      ...configDefaults.exclude,
      'test-utils/src/gate-time.injected-scale.browser.test.tsx',
    ],
    setupFiles: ['./test-utils/src/browser-setup.ts'],
    // The workspace's one testTimeout and hookTimeout (web/vitest.shared.ts)
    // apply here too; the cases wait on events, so they only bound a hang.
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
