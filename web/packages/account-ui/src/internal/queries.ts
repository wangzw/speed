/**
 * The account surfaces' reads, one hook each over the generated react-query
 * hooks of @speed/api-sdk.
 */

import {
  getAuthnMfaFactorsQueryKey,
  getAuthnProfileGetQueryKey,
  getAuthnSessionGetQueryKey,
  getAuthnSessionListQueryKey,
  useAuthnMfaFactors,
  useAuthnProfileGet,
  useAuthnSessionGet,
  useAuthnSessionList,
} from '@speed/api-sdk'
import type { AuthnSessionView } from '@speed/api-sdk'
import {
  asFactorList,
  asProfile,
  asSessionList,
} from './models.js'
import type { AccountProfile, FactorList, SessionList } from './models.js'

export function useSessionsQuery() {
  return useAuthnSessionList<SessionList>({
    query: {
      queryKey: getAuthnSessionListQueryKey(),
      select: asSessionList,
    },
  })
}

export function useSessionViewQuery(enabled = true) {
  return useAuthnSessionGet<AuthnSessionView>({
    query: {
      queryKey: getAuthnSessionGetQueryKey(),
      enabled,
    },
  })
}

export function useFactorsQuery(enabled = true) {
  return useAuthnMfaFactors<FactorList>({
    query: {
      queryKey: getAuthnMfaFactorsQueryKey(),
      select: asFactorList,
      enabled,
    },
  })
}

export function useProfileQuery() {
  return useAuthnProfileGet<AccountProfile>({
    query: {
      queryKey: getAuthnProfileGetQueryKey(),
      select: asProfile,
    },
  })
}
