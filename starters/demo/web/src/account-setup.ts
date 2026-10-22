/**
 * Whether an account's setup is complete, and whether its registration
 * contact is still awaiting verification: two separate facts the host keys
 * on, deliberately not folded together.
 *
 * The ruling (ADR-authn-signup-2026-10-22) splits registration from setup. A
 * password registration opens the account with a pending email address, and
 * the account proves that address after signing in; an SMS first login opens
 * the account with a verified phone. Setup then adds the two pieces password
 * sign-in needs: a login name and a password. A login name is a username or
 * a verified email; a phone signs in but is not one. The profile these
 * predicates read is GET /api/v1/auth/me.
 *
 * `contactVerificationPending` reads the profile's `verification.state`.
 * `setupIncomplete` reads the login name and password. A password
 * registration has already supplied a login name and password, so setup can
 * be complete while verification is still pending; the verification notice
 * must not be hidden by that.
 */

/** The part of GET /api/v1/auth/me the readiness question needs. */
export interface SetupProfile {
  readonly password_set: boolean
  readonly identifiers: readonly {
    readonly kind: string
    readonly verified_at: string
  }[]
}

/** The part of GET /api/v1/auth/me the registration-verification question
 * needs. */
export interface VerificationProfile {
  readonly verification?: {
    readonly state?: string
  }
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

/**
 * Whether the account's self-registration still awaits email verification.
 * `none` (an account that never self-registered) and `verified` answer false;
 * only `pending` answers true. This is independent of `setupIncomplete`: the
 * verification notice stays until the address is proved, whatever the login
 * name and password state.
 */
export function contactVerificationPending(
  profile: VerificationProfile,
): boolean {
  return profile.verification?.state === 'pending'
}
