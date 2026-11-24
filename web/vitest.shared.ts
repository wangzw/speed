import { configDefaults } from 'vitest/config'
import type { ViteUserConfig } from 'vitest/config'

export type TestOptions = NonNullable<ViteUserConfig['test']>

export const TEST_BROWSER_TIME_ZONE = 'Asia/Shanghai'

export const sharedCoverage = {
  provider: 'v8',
  include: ['src/**'],
  reporter: ['text-summary', 'json-summary'],
  thresholds: {
    statements: 80,
    branches: 80,
  },
} satisfies NonNullable<TestOptions['coverage']>

// Must stay finite: Vitest reads 0 as no limit.
export const SHARED_TIMEOUT_MS = 60_000

// Decimal only: `Number` would also take hex, binary, octal, underscores,
// Infinity and NaN.
const DECIMAL = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/

// The parse is restated in web/test-utils/src/gate-time.ts and in the demo
// host's copy, which cannot import this preset; all three apply one rule.
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

export function sharedTimeoutMs(): number {
  return SHARED_TIMEOUT_MS * gateTimeScale()
}

// A pinned reporter list turns off Vitest's own default, which adds the
// github-actions annotation only when Vitest chose the reporters; append it
// explicitly so CI still gets the annotation.
export function sharedReporters(
  base: string = 'default',
  env: { GITHUB_ACTIONS?: string } = process.env,
): string | string[] {
  return env.GITHUB_ACTIONS === 'true' ? [base, 'github-actions'] : base
}

export function sharedTestOptions(
  overrides: TestOptions = {},
): TestOptions {
  return {
    // Pinned so an agent environment cannot swap in the silent reporter that
    // discards a passing case's console output.
    reporters: sharedReporters(),
    // Naming `exclude` replaces Vitest's defaults, so they are restated.
    exclude: [...configDefaults.exclude, '**/*.browser.test.tsx'],
    ...overrides,
    testTimeout: sharedTimeoutMs(),
    hookTimeout: sharedTimeoutMs(),
    coverage: { ...sharedCoverage, ...overrides.coverage },
  }
}
