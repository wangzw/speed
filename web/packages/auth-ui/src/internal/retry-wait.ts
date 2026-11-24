/**
 * The wait a refused request stated, read structurally from its
 * `retryAfterMs`; only a 429 carrying a finite, non-negative value states one.
 */
export function retryWaitMs(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null) {
    return undefined
  }
  let status: unknown
  let wait: unknown
  try {
    status = (error as { status?: unknown }).status
    wait = (error as { retryAfterMs?: unknown }).retryAfterMs
  } catch {
    // A throwing getter or a revoked proxy states no wait.
    return undefined
  }
  if (status !== 429 || typeof wait !== 'number' || !Number.isFinite(wait)) {
    return undefined
  }
  return Math.max(0, wait)
}
