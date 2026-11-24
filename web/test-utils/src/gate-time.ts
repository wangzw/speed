export const ASYNC_UTIL_TIMEOUT_MS = 30_000

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

// Decimal only: `Number` would also take hex, binary, octal, underscores,
// Infinity and NaN.
const DECIMAL = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/

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
