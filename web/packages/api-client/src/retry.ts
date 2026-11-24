/** Timing and budget of transient retries. */
export interface RetryPolicy {
  /** Total HTTP attempts before giving up; 1 disables transient retries. */
  readonly maxAttempts: number
  /**
   * Delay before the first retry; doubles per retry with full jitter and is
   * never above maxDelayMs.
   */
  readonly initialDelayMs: number
  /** Absolute ceiling for any single retry delay. */
  readonly maxDelayMs: number
}

/**
 * The built-in budget: up to 3 attempts with 200ms doubling jittered delays
 * capped at 4s. Frozen so accidental mutation fails fast.
 */
export const DEFAULT_RETRY_POLICY: RetryPolicy = Object.freeze({
  maxAttempts: 3,
  initialDelayMs: 200,
  maxDelayMs: 4000,
})

/**
 * The full-jitter backoff delay before retry number `attempt` (0-based):
 * uniform over [0, min(maxDelayMs, initialDelayMs * 2**attempt)). The random
 * source is injected so tests are deterministic.
 */
export function retryDelayMs(
  attempt: number,
  policy: RetryPolicy,
  random: () => number = Math.random,
): number {
  const retry = Math.max(0, Math.floor(attempt))
  const cap = Math.min(
    policy.maxDelayMs,
    policy.initialDelayMs * 2 ** retry,
  )
  return Math.floor(random() * cap)
}

/**
 * Parses a Retry-After header (delta-seconds or HTTP-date) into milliseconds,
 * or null when the header is absent or unparseable.
 */
export function retryAfterDelayMs(
  header: string | null | undefined,
  now: number = Date.now(),
): number | null {
  if (header === null || header === undefined) {
    return null
  }
  const trimmed = header.trim()
  if (trimmed === '') {
    return null
  }
  if (/^[0-9]+$/.test(trimmed)) {
    return Number(trimmed) * 1000
  }
  const at = Date.parse(trimmed)
  if (Number.isNaN(at)) {
    return null
  }
  return Math.max(0, at - now)
}
