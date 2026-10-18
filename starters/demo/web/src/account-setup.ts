/**
 * Whether an account's setup is complete: the fact the setup page renders
 * and the once-only home notice keys on.
 *
 * The ruling (docs/adr/adr-authn-signup-2026-10-18) is that an SMS first
 * login opens the account with a verified phone, and setup adds the two
 * pieces password sign-in needs: a login name and a password. A login name
 * is a username or a verified email; a phone signs in but is not one. The
 * profile these predicates read is GET /api/v1/auth/me.
 */

/** The part of GET /api/v1/auth/me the readiness question needs. */
export interface SetupProfile {
  readonly password_set: boolean
  readonly identifiers: readonly {
    readonly kind: string
    readonly verified_at: string
  }[]
}

/**
 * Whether one identifier can serve as a login name: a username, or an
 * email address that has been verified. A phone is a way to sign in but not
 * a login name, so it does not unlock password sign-in.
 */
export function isLoginName(identifier: {
  readonly kind: string
  readonly verified_at: string
}): boolean {
  if (identifier.kind === 'username') {
    return true
  }
  return identifier.kind === 'email' && identifier.verified_at !== ''
}

/**
 * Whether the account still lacks a piece of setup: no identifier usable as
 * a login name, or no password. Both are required for password sign-in, so
 * either absence leaves setup incomplete.
 */
export function setupIncomplete(profile: SetupProfile): boolean {
  return (
    !profile.identifiers.some((identifier) => isLoginName(identifier)) ||
    !profile.password_set
  )
}
