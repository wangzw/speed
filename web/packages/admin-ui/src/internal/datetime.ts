/**
 * Whether an administration record carries no instant, the sentinel the
 * views use for "never" (an impersonation still in force). Both `null` and
 * an absent field read as "never"; any other value is a real instant.
 */
export function isNullInstant(value: string | null | undefined): boolean {
  return value === null || value === undefined
}
