/**
 * The caller's `GATE_TIME_SCALE`, as the demo host's setup reads it, and
 * the rule every reader of the variable applies.
 *
 * The demo host is a copyable skeleton and does not import the workspace's
 * private test package, so it carries this copy; it applies the same rule
 * as `web/test-utils/src/gate-time.ts` and `web/vitest.shared.ts`. The
 * variable multiplies the limits that bound a wait (Testing Library's
 * `findBy*`/`waitFor` budgets here, the preset's test and hook budgets).
 * Nothing inspects the machine's load or core count.
 *
 * This program carries no Node type definitions, so the process global is
 * read through its shape rather than through `process` itself.
 */

/**
 * The shared async budget before the caller's scale: how long a wait for a
 * condition to settle may take. The setup applies it to Testing Library's
 * `asyncUtilTimeout`, the same value `web/test-utils/src/gate-time.ts`
 * carries.
 */
export const ASYNC_UTIL_TIMEOUT_MS = 30_000

/** The async budget a wait gets: the shared value times the caller's scale. */
export function asyncBudgetMs(scale: number): number {
  return ASYNC_UTIL_TIMEOUT_MS * scale
}

export function gateTimeScale(): number {
  const env = (
    globalThis as {
      process?: { env?: Record<string, string | undefined> }
    }
  ).process?.env
  return parseGateTimeScale(env?.GATE_TIME_SCALE)
}

/**
 * A decimal numeric literal: digits with an optional fraction and exponent.
 * The variable means a decimal number, so this excludes the other literal
 * forms `Number` would otherwise take -- hex (`0x10`), binary (`0b101`),
 * octal (`0o17`) and digit-separating underscores (`1_000`) -- as well as
 * the non-numeric names (`Infinity`, `NaN`).
 */
const DECIMAL = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/

/**
 * The rule: unset, empty or nothing but whitespace is 1. Any other value is
 * trimmed and must then be a decimal number of at least 1, fractions and
 * exponents included; a hexadecimal literal, a non-finite value or one below
 * 1 fails here, naming the requirement, instead of silently becoming 1. A
 * silent 1 would let a caller's typo reintroduce the load failures the scale
 * exists to prevent, and a value below 1 would push the wait under the
 * default the gate is built around.
 */
export function parseGateTimeScale(raw: string | undefined): number {
  const trimmed = raw?.trim() ?? ''
  if (trimmed === '') {
    return 1
  }
  const scale = Number(trimmed)
  if (!DECIMAL.test(trimmed) || !Number.isFinite(scale) || scale < 1) {
    throw new Error(
      `GATE_TIME_SCALE must be a finite number of at least 1; got ${JSON.stringify(trimmed)}`,
    )
  }
  return scale
}
