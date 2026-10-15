/**
 * The account surfaces' reads, one hook each over the generated
 * react-query hooks of @speed/api-sdk.
 *
 * The generated fragment declares no response schema, so orval types
 * every read as `void`; each hook selects its generated query through the
 * matching narrowing in models.ts, which retypes the answer to the shape
 * the surface renders without minting a QueryClient of its own -- the
 * query still runs on the host's client, under the generated query key,
 * so a caller can invalidate it with the same constructor the hook used.
 *
 * The `queryKey` passed beside `select` is the generated constructor, not
 * a hand-written key: orval's option type requires it next to `select`,
 * and reusing the constructor keeps the hook's cache entry and every
 * invalidation pointing at one key.
 */

import {
  getAuthnMfaFactorsQueryKey,
  getAuthnProfileGetQueryKey,
  getAuthnSessionListQueryKey,
  useAuthnMfaFactors,
  useAuthnProfileGet,
  useAuthnSessionList,
} from '@speed/api-sdk'
import {
  asFactorList,
  asProfile,
  asSessionList,
} from './models.js'
import type { AccountProfile, FactorList, SessionList } from './models.js'

/** The caller's own sessions (GET /auth/sessions). */
export function useSessionsQuery() {
  return useAuthnSessionList<SessionList>({
    query: {
      queryKey: getAuthnSessionListQueryKey(),
      select: asSessionList,
    },
  })
}

/** The caller's own registered factors (GET /auth/factors). */
export function useFactorsQuery() {
  return useAuthnMfaFactors<FactorList>({
    query: {
      queryKey: getAuthnMfaFactorsQueryKey(),
      select: asFactorList,
    },
  })
}

/** The caller's own profile, with its identifiers and links (GET /auth/me). */
export function useProfileQuery() {
  return useAuthnProfileGet<AccountProfile>({
    query: {
      queryKey: getAuthnProfileGetQueryKey(),
      select: asProfile,
    },
  })
}
