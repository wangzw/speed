/**
 * The caller's `GATE_TIME_SCALE`, as the test-support setups read it, and
 * the one rule every reader of the variable applies.
 *
 * The variable multiplies the limits that bound a wait: the workspace
 * preset (`web/vitest.shared.ts`, which cannot import this package) gives
 * every case and hook its test budget, and the setups here give Testing
 * Library's `findBy*`/`waitFor` queries theirs. The caller -- the person or
 * job scheduling the gate -- sets the value when other heavy work shares
 * the machine; nothing inspects the machine's load or core count.
 *
 * `vi.waitFor` does not read Testing Library's `asyncUtilTimeout` and has
 * no global default, so `wait.ts`'s helper (`waitForCondition`) applies the
 * same budget to it.
 *
 * This package's TypeScript program carries no Node type definitions (its
 * sources run in jsdom), so the process global is read through its shape
 * rather than through `process` itself.
 */

/**
 * The shared async budget before the caller's scale: how long a wait for a
 * condition to settle may take. It is the wait, not the work -- a condition
 * that never arrives still fails, and a genuine hang is reported. The
 * setups apply it to Testing Library's `asyncUtilTimeout`, and
 * `waitForCondition` applies it to `vi.waitFor`, which has no global
 * default of its own.
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
