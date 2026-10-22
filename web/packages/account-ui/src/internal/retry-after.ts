/**
 * The Retry-After a 429 carried, in milliseconds.
 *
 * Read structurally so the package stays independent of @speed/api-client's
 * concrete error class. A value that is absent or not a positive number means
 * the answer carried no interval, and the caller starts no countdown.
 */
export function retryAfterMsOf(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null) {
    return undefined
  }
  const value = (error as { retryAfterMs?: unknown }).retryAfterMs
  return typeof value === 'number' && value > 0 ? value : undefined
}
