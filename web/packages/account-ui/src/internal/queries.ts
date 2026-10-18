/**
 * The account surfaces' reads, one hook each over the generated
 * react-query hooks of @speed/api-sdk.
 *
 * The generated operations carry their response types (the fragments
 * declare the 2xx bodies), so the types the surface renders come from
 * @speed/api-sdk; each hook selects its generated query through the
 * matching narrowing in models.ts, which carries the one runtime rule the
 * generated surface cannot (an absent body reads as an empty object, not
 * an empty collection) -- the query still runs on the host's client,
 * under the generated query key, so a caller can invalidate it with the
 * same constructor the hook used.
 *
 * The `queryKey` passed beside `select` is the generated constructor, not
 * a hand-written key: orval's option type requires it next to `select`,
 * and reusing the constructor keeps the hook's cache entry and every
 * invalidation pointing at one key.
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

/** The caller's own sessions (GET /api/v1/auth/sessions). */
export function useSessionsQuery() {
  return useAuthnSessionList<SessionList>({
    query: {
      queryKey: getAuthnSessionListQueryKey(),
      select: asSessionList,
    },
  })
}

/** The caller's own session (GET /api/v1/auth/session): the methods that
 * established it, its assurance and its last interactive authentication
 * time. `enabled` holds the read back while the surface that needs it is
 * closed. */
export function useSessionViewQuery(enabled = true) {
  return useAuthnSessionGet<AuthnSessionView>({
    query: {
      queryKey: getAuthnSessionGetQueryKey(),
      enabled,
    },
  })
}

/** The caller's own registered factors (GET /api/v1/auth/factors). `enabled` holds
 * the read back while the surface that needs it is closed. */
export function useFactorsQuery(enabled = true) {
  return useAuthnMfaFactors<FactorList>({
    query: {
      queryKey: getAuthnMfaFactorsQueryKey(),
      select: asFactorList,
      enabled,
    },
  })
}

/** The caller's own profile, with its identifiers and links (GET /api/v1/auth/me). */
export function useProfileQuery() {
  return useAuthnProfileGet<AccountProfile>({
    query: {
      queryKey: getAuthnProfileGetQueryKey(),
      select: asProfile,
    },
  })
}
