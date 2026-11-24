/**
 * The Retry-After a 429 carried, in milliseconds, read structurally so the
 * package stays independent of @speed/api-client's error class. An absent or
 * non-positive value reads as no interval.
 */
export function retryAfterMsOf(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null) {
    return undefined
  }
  const value = (error as { retryAfterMs?: unknown }).retryAfterMs
  return typeof value === 'number' && value > 0 ? value : undefined
}
