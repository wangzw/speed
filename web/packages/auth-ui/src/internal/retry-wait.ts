/**
 * The wait a refused request stated, read structurally.
 *
 * This package consumes the session, not `@speed/api-client`, so the
 * ApiError shape is checked the way `errorCodeOf` checks the code. Only a
 * 429 carrying a finite, non-negative `retryAfterMs` states a wait.
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
