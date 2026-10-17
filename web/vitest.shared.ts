/**
 * The workspace's shared Vitest test options.
 *
 * Every member's vitest.config.ts takes its `test` block from here
 * instead of restating the timeout and the coverage block, so a package
 * cannot fall back to Vitest's 5s default by omission: the workspace's
 * suites run concurrently under `pnpm -r test`, and that load pushes
 * individual cases past 5s on a machine running every package at once,
 * failing the gate on the clock rather than on an assertion. The budget
 * is the wait, not the work: a genuine hang still fails, twenty seconds
 * later.
 *
 * What stays package-specific is what actually differs: the environment
 * (`jsdom` for DOM-rendering packages, node otherwise), the setup file,
 * the aliases from the workspace's single package map
 * (web/scripts/speed-aliases.mjs), and any coverage exclusion.
 */
import type { ViteUserConfig } from 'vitest/config'

/** A Vitest `test` block. */
export type TestOptions = NonNullable<ViteUserConfig['test']>

/**
 * Coverage: v8 over src/** only, so aliased sibling sources never count
 * toward a package's numbers; text-summary for local runs, json-summary
 * for the per-package coverage gate, whose floor is 80% statements and
 * 80% branches -- the CI test leg runs with --coverage, so dropping
 * below either fails that package's job.
 */
export const sharedCoverage = {
  provider: 'v8',
  include: ['src/**'],
  reporter: ['text-summary', 'json-summary'],
  thresholds: {
    statements: 80,
    branches: 80,
  },
} satisfies NonNullable<TestOptions['coverage']>

/**
 * The common test options with the package's own options merged in.
 * Package-level values win: `coverage` merges one level deep, so a
 * package replaces a field (a different `include`, a stricter threshold)
 * or adds an `exclude` or drops the thresholds without restating the
 * block. `testTimeout` is applied after the overrides: one budget for the
 * whole workspace is the point, so a package does not get to name its
 * own.
 */
export function sharedTestOptions(
  overrides: TestOptions = {},
): TestOptions {
  return {
    ...overrides,
    testTimeout: 20_000,
    coverage: { ...sharedCoverage, ...overrides.coverage },
  }
}
