/**
 * The workspace's shared Vitest test options.
 *
 * Every member's vitest.config.ts takes its `test` block from here
 * instead of restating the timeout and the coverage block, so a package
 * cannot fall back to Vitest's 5s default by omission: the workspace's
 * suites run concurrently under `pnpm -r test`, and that load pushes
 * individual cases past 5s on a machine running every package at once,
 * failing the gate on the clock rather than on an assertion. The budget
 * is the wait, not the work: a genuine hang still fails, a minute later.
 *
 * The caller sets `GATE_TIME_SCALE` (a multiplier it decides on, never a
 * value measured from the machine) when it runs the gate beside other
 * heavy work; the budget is multiplied by it. The wait bounds scale;
 * nothing else about a case changes.
 *
 * What stays package-specific is what actually differs: the environment
 * (`jsdom` for DOM-rendering packages, node otherwise), the setup file,
 * the aliases from the workspace's single package map
 * (web/scripts/speed-aliases.mjs), and any coverage exclusion.
 */
import { configDefaults } from 'vitest/config'
import type { ViteUserConfig } from 'vitest/config'

/** A Vitest `test` block. */
export type TestOptions = NonNullable<ViteUserConfig['test']>

/**
 * The browser time zone the packages that exercise the display-time-zone
 * chain's middle tier pin. GitHub's runners set TZ=UTC, where "an absent
 * or unusable preference falls to the browser zone" and "it falls to UTC"
 * produce the same resolved zone and the same rendered time, so a test
 * asserting the fallback cannot tell a correct implementation from one
 * that skips the middle tier. A non-UTC zone makes the two outcomes
 * distinguishable; the suites derive the expected zone from
 * `Intl.DateTimeFormat().resolvedOptions()` at run time and assert it is
 * not UTC, so a config that stopped pinning this value fails loudly
 * instead of passing vacuously.
 */
export const TEST_BROWSER_TIME_ZONE = 'Asia/Shanghai'

/**
 * Coverage: v8 over src/** only, so aliased sibling sources never count
 * toward a package's numbers; text-summary for the console and json-summary
 * for a machine-readable total beside it. The floors are 80% statements and
 * 80% branches, and they gate exactly one thing: a run that requests coverage
 * (`pnpm -C web -r test --coverage`). The repository's automated gate --
 * `make web-check`, the only thing CI calls -- runs every suite WITHOUT
 * coverage, so a package below a floor is reported by that opt-in run, never
 * failed by CI. Instrumentation makes every suite more expensive, which is
 * why it is not on the per-commit gate.
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
 * The one timeout a case and each of its hooks get, before the caller's
 * `GATE_TIME_SCALE` is applied. Wide enough that a case running beside a
 * saturated machine fails on its assertion rather than on the clock, and a
 * finite value rather than `0`, which Vitest reads as no limit: a case that
 * keeps the default still reports a genuine hang instead of holding the
 * gate.
 *
 * The floor this default must clear lives outside this file, in
 * `web/scripts/test-time-floors.json`, so the value being guarded and the
 * floor guarding it are never edited in one place: a lowered default that
 * lowered its own floor with it would pass a guard that read both here.
 */
export const SHARED_TIMEOUT_MS = 60_000

/**
 * A decimal numeric literal: digits with an optional fraction and exponent.
 * The variable means a decimal number, so this excludes the other literal
 * forms `Number` would otherwise take -- hex (`0x10`), binary (`0b101`),
 * octal (`0o17`) and digit-separating underscores (`1_000`) -- as well as
 * the non-numeric names (`Infinity`, `NaN`).
 */
const DECIMAL = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/

/**
 * The caller's `GATE_TIME_SCALE`: a multiplier for the limits that bound
 * a wait, defaulting to 1. The caller -- the person or job scheduling the
 * gate -- decides it; nothing here inspects the machine's load or core
 * count. Unset, empty or nothing but whitespace is 1; any other value is
 * trimmed and must then be a decimal number of at least 1, fractions and
 * exponents included. A hexadecimal literal, a non-finite value or one
 * below 1 fails here rather than silently becoming 1. A silent 1 would let
 * a caller's typo reintroduce the load failures the scale exists to
 * prevent, and a value below 1 would push the wait under the default the
 * gate is built around.
 *
 * `web/test-utils/src/gate-time.ts` reads the same variable for the
 * Testing Library async budgets; that package cannot import this preset,
 * so the parse is restated there, and the demo host's own copy of the
 * test support -- a copyable skeleton that imports nothing private --
 * restates it once more. All three apply this one rule.
 */
export function gateTimeScale(): number {
  const raw = process.env.GATE_TIME_SCALE?.trim() ?? ''
  if (raw === '') {
    return 1
  }
  const scale = Number(raw)
  if (!DECIMAL.test(raw) || !Number.isFinite(scale) || scale < 1) {
    throw new Error(
      `GATE_TIME_SCALE must be a finite number of at least 1; got ${JSON.stringify(raw)}`,
    )
  }
  return scale
}

/**
 * The budget one case and each of its hooks get: the shared value above,
 * multiplied by the caller's scale. The preset applies it, and a config
 * that builds its own `test` block for a different environment (the demo
 * host's two) takes it from here rather than restating the number.
 */
export function sharedTimeoutMs(): number {
  return SHARED_TIMEOUT_MS * gateTimeScale()
}

/**
 * The test reporters for a run: the base reporter, plus the
 * `github-actions` reporter when the run is a GitHub Actions job. The
 * second one only turns a failure into an annotation; it prints no test
 * output of its own, so the base reporter's log is unchanged.
 *
 * Vitest appends that reporter itself only when it chose the reporters
 * (its default is `['default', 'github-actions']` under
 * `GITHUB_ACTIONS=true`). The workspace pins its reporters so an agent
 * environment cannot swap in the silent reporter, and a pinned list stops
 * that default from running; this helper appends the annotation reporter
 * explicitly so the CI failure still appears on the workflow run.
 *
 * The demo host builds its own test blocks and takes this helper too, so
 * both of its configs carry the same rule.
 */
export function sharedReporters(
  base: string = 'default',
  env: { GITHUB_ACTIONS?: string } = process.env,
): string | string[] {
  return env.GITHUB_ACTIONS === 'true' ? [base, 'github-actions'] : base
}

/**
 * The common test options with the package's own options merged in.
 * Package-level values win: `coverage` merges one level deep, so a
 * package replaces a field (a different `include`, a stricter threshold)
 * or adds an `exclude` or drops the thresholds without restating the
 * block. `testTimeout` and `hookTimeout` are applied after the
 * overrides: one budget for the whole workspace is the point, so a
 * package does not get to name its own. `hookTimeout` shares the value
 * because a hook waits on the same machine as the cases around it, so a
 * fixture gets the budget its case gets.
 */
export function sharedTestOptions(
  overrides: TestOptions = {},
): TestOptions {
  return {
    // The reporters are pinned rather than left to Vitest's choice, which
    // is not the same in every run: with no configured reporter Vitest 4
    // selects MinimalReporter (silent "passed-only") whenever std-env
    // reports an agent environment, and that reporter discards the
    // console output of passing cases. The gate would then show a
    // passing case's console.warn only when a person ran it. One fixed
    // base reporter makes the log independent of who starts the run, and
    // `sharedReporters` adds the GitHub Actions annotation on CI.
    reporters: sharedReporters(),
    // Vitest's own defaults are restated because naming `exclude`
    // replaces them rather than extending them. The browser cases
    // (`*.browser.test.tsx`) are measured in real Chromium by the
    // browser tier, which owns its own config (web/vitest.browser.config.ts)
    // and its own entry point (`make web-browser`); a package's unit
    // leg under jsdom must not pick them up, and without this line
    // Vitest's default include would.
    exclude: [...configDefaults.exclude, '**/*.browser.test.tsx'],
    ...overrides,
    testTimeout: sharedTimeoutMs(),
    hookTimeout: sharedTimeoutMs(),
    coverage: { ...sharedCoverage, ...overrides.coverage },
  }
}
