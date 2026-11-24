/**
 * The multi-factor refusal verdict: a fresh sign-in clears it only with an enabled
 * factor; an unsettled or failed read reads as "a sign-in can still clear it".
 */

import { getAuthnMfaFactorsQueryKey, useAuthnMfaFactors } from '@speed/api-sdk'

/** Whether the refused account has no enabled second factor; `enabled` gates the read. */
export function useNoMfaFactor(enabled: boolean): boolean {
  const factors = useAuthnMfaFactors({
    query: { queryKey: getAuthnMfaFactorsQueryKey(), enabled },
  })
  return enabled && factors.isSuccess && activeFactorCount(factors.data) === 0
}

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
