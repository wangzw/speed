/**
 * The one time multiplier the gate's caller hands down to the end-to-end tier.
 *
 * The tier waits on real work -- a browser lays a page out, a built binary
 * seeds sqlite and starts serving -- so on a loaded machine, or with several
 * gates running at once, a bound that is ample when the box is idle can expire
 * before the work finishes, and a case fails on the clock rather than on an
 * assertion. The caller passes `GATE_TIME_SCALE` (a number of at least 1, or 1
 * when absent) to widen every bound at once; the tier never probes the machine's
 * load or counts its cores to choose a bound for itself.
 *
 * The value is read once per process. The Playwright config process and each
 * fixture worker are separate processes but read the same environment, so they
 * agree on the multiplier. A value that is not a decimal number of at least 1
 * is refused loudly rather than treated as 1: a mistyped scale cannot quietly
 * leave the tier on its idle-machine bounds, and a value below 1 would shrink
 * the bounds past the ceiling this tier exists to keep.
 */

/** The environment variable that carries the run's time multiplier. */
export const GATE_TIME_SCALE_ENV = 'GATE_TIME_SCALE'

/**
 * A decimal numeric literal: digits with an optional fraction and exponent.
 * The variable means a decimal number, so this excludes the other literal
 * forms `Number` would otherwise take -- hex (`0x10`), binary (`0b101`),
 * octal (`0o17`) and digit-separating underscores (`1_000`) -- as well as the
 * non-numeric names (`Infinity`, `NaN`).
 */
const DECIMAL = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/

/**
 * Parse the scale out of the environment; unset, empty or nothing but
 * whitespace is 1. Any other value is trimmed and must then be a decimal
 * number of at least 1, fractions and exponents included.
 */
export function parseTimeScale(raw: string | undefined): number {
  const trimmed = raw?.trim() ?? ''
  if (trimmed === '') return 1
  const parsed = Number(trimmed)
  if (!DECIMAL.test(trimmed) || !Number.isFinite(parsed) || parsed < 1) {
    throw new Error(
      `${GATE_TIME_SCALE_ENV} must be a finite number of at least 1; got ${JSON.stringify(trimmed)}`,
    )
  }
  return parsed
}

/** This process's time multiplier: `GATE_TIME_SCALE`, or 1 when unset. */
export const GATE_TIME_SCALE = parseTimeScale(process.env[GATE_TIME_SCALE_ENV])

/**
 * Multiply one base bound by the run's scale, rounding a fractional millisecond
 * up so a sub-millisecond base still grows.
 */
export function scaledTimeout(
  baseMs: number,
  scale: number = GATE_TIME_SCALE,
): number {
  return Math.ceil(baseMs * scale)
}
