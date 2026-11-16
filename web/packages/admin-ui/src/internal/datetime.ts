/**
 * The absent-instant sentinel the administration views carry.
 *
 * The views write `null` for "never" -- an impersonation still in force --
 * and may leave the field out entirely. Timestamp rendering itself is
 * @speed/i18n's `formatInstant` with an explicit UTC zone; this module
 * carries only the sentinel test, which must not go through a timezone
 * conversion.
 */

/**
 * Whether an administration record carries no instant, the sentinel the
 * views use for "never" (an impersonation still in force). Both `null` and
 * an absent field read as "never"; any other value is a real instant.
 */
export function isNullInstant(value: string | null | undefined): boolean {
  return value === null || value === undefined
}
