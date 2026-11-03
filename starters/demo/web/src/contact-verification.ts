/**
 * Whether an account still awaits registration verification: the one
 * account-readiness fact the host keys a notice on.
 *
 * The profile this predicate reads is GET /api/v1/auth/me, whose
 * `verification.state` is `pending` while the account holds an email address
 * that has not been proved, however that address arrived (self-registration,
 * administrator provisioning or an import). It does not depend on how the
 * account was opened.
 */

/** The part of GET /api/v1/auth/me the registration-verification question
 * needs. */
export interface VerificationProfile {
  readonly verification?: {
    readonly state?: string
  }
}

/**
 * Whether the account still awaits email verification. `none` (an account
 * with no email address at all) and `verified` answer false; only `pending`
 * answers true.
 */
export function contactVerificationPending(
  profile: VerificationProfile,
): boolean {
  return profile.verification?.state === 'pending'
}
