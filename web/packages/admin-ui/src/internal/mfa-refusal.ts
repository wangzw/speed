/**
 * The console's multi-factor refusal verdict.
 *
 * A tenant-addressed console read or write can be refused `401
 * authn.insufficient_user_authentication` when an organization the caller
 * is an active member of requires multi-factor authentication. What clears
 * that refusal depends on the account's own factors: with an enabled one a
 * fresh sign-in completes the second factor, while an account with none
 * must register one first -- signing in again produces the same
 * single-factor outcome. This module reads the caller's registered factors
 * (GET /api/v1/auth/factors) and answers that one question, so the read
 * surface's refusal exit and the write surface's refusal exit never
 * disagree about which exit to offer.
 *
 * The read is issued only while a surface is showing the refusal exit.
 * A read that has not settled, one that failed, and a surface that offers
 * no exit all read as "a sign-in can still clear it", so the exit is never
 * held back on an answer that cannot come.
 */

import { getAuthnMfaFactorsQueryKey, useAuthnMfaFactors } from '@speed/api-sdk'

/**
 * Whether the refused account has no enabled second factor. `enabled` is
 * false when the failure is not the multi-factor refusal or the surface
 * offers no exit; the factor read is then not issued at all.
 */
export function useNoMfaFactor(enabled: boolean): boolean {
  const factors = useAuthnMfaFactors({
    query: { queryKey: getAuthnMfaFactorsQueryKey(), enabled },
  })
  return enabled && factors.isSuccess && activeFactorCount(factors.data) === 0
}

/** The factors a factor read names as active: the ones that may verify. */
function activeFactorCount(
  page:
    | { readonly factors: readonly { readonly status: string }[] }
    | undefined,
): number {
  if (page === undefined) {
    return 0
  }
  return page.factors.filter((factor) => factor.status === 'active').length
}
