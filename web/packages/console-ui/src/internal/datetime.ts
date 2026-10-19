/**
 * The zero-instant sentinel the administration views carry.
 *
 * The views write Go's zero time (`0001-01-01T00:00:00Z`) for "never" -- an
 * impersonation still in force, an invitation not yet accepted -- or leave
 * the field empty. Timestamp rendering itself is @speed/i18n's
 * `formatInstant` with an explicit UTC zone; this module carries only the
 * sentinel test, which must not go through a timezone conversion.
 */

/**
 * Whether an instant is the zero instant (0001-01-01T00:00:00Z), the
 * sentinel the administration views use for "never" (an impersonation
 * still in force, an invitation not yet accepted). The literal the Go
 * time package writes for the zero time is matched directly, so no
 * timezone conversion can mistake it for a real instant.
 */
export function isZeroInstant(iso: string | undefined): boolean {
  return iso === undefined || iso === '' || iso.startsWith('0001-01-01')
}
