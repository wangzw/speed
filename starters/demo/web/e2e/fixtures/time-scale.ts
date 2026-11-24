export const GATE_TIME_SCALE_ENV = 'GATE_TIME_SCALE'

// A decimal literal only: this excludes hex, binary, octal, `_` separators and `Infinity`/`NaN`.
const DECIMAL = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/

// Unset, empty or whitespace is 1; any other value must be a decimal number of at least 1.
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

export const GATE_TIME_SCALE = parseTimeScale(process.env[GATE_TIME_SCALE_ENV])

// Rounds a fractional millisecond up so a sub-millisecond base still grows.
export function scaledTimeout(
  baseMs: number,
  scale: number = GATE_TIME_SCALE,
): number {
  return Math.ceil(baseMs * scale)
}
