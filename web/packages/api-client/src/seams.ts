/**
 * The transport seams every outbound exchange announces -- the bearer
 * token, the tenant and the language -- read per attempt and applied to
 * a header set in one place, so the request function (client.ts) and
 * the event-stream opener (event-stream.ts) observe the identical
 * discipline instead of each maintaining a copy.
 *
 * Per attempt, like the request loop: a connection re-attempted after a
 * token refresh, a tenant switch or a language switch announces the
 * value current at that attempt. A caller-supplied header is never
 * overwritten.
 */

import type { AccessTokenStore } from './access-token.js'

/** The default header every tenant-scoped request announces the tenant
 * under. Lower case, the canonical form a `Headers` lookup and a Go
 * `r.Header.Get` both fold to. `ClientOptions.tenantHeader` renames it
 * for a deployment whose gateway reads the tenant from another header. */
export const TENANT_HEADER = 'x-tenant'

/** The providers the seams read, per attempt. Any may be absent: an
 * absent provider means "nothing to announce" for its header. */
export interface SeamProviders {
  /** The bearer-token store; absent, no Authorization is attached. */
  readonly accessTokenStore?: AccessTokenStore
  /** The language the exchange announces as `accept-language`, or
   * null/empty for "none to announce". */
  readonly languageProvider?: () => string | null
  /** The tenant the exchange announces (a request-time routing fact,
   * never a claim of the access token), or null/empty for "none to
   * announce". */
  readonly tenantProvider?: () => string | null
  /** The header the tenant travels under; absent or empty means the
   * `TENANT_HEADER` default (`x-tenant`). */
  readonly tenantHeader?: string
}

/**
 * The header set the tenant seam announces under: the configured name,
 * falling back to `TENANT_HEADER` for an absent or empty one. */
function tenantHeaderOf(providers: SeamProviders): string {
  const configured = providers.tenantHeader
  return configured === undefined || configured === ''
    ? TENANT_HEADER
    : configured
}

/**
 * Applies the seam headers to a header set: `accept-language` and the
 * tenant header (`x-tenant` by default, `SeamProviders.tenantHeader`
 * when configured) when their provider answers a non-empty value, and
 * `Authorization: Bearer <token>` from the store -- each only when the
 * caller did not already set the header, so a caller-supplied value
 * survives. `omitAccessToken` skips the store entirely: no token is
 * read, so a caller-supplied `authorization` (when given) is the
 * caller's own and travels untouched.
 *
 * Returns whether a bearer token was attached -- the gate the 401
 * refresh path reads (only a refused attempt that presented one may
 * refresh).
 */
export function applySeamHeaders(
  headers: Headers,
  providers: SeamProviders,
  options: { omitAccessToken?: boolean } = {},
): boolean {
  if (!headers.has('accept-language')) {
    const language = providers.languageProvider?.() ?? null
    if (language !== null && language !== '') {
      headers.set('accept-language', language)
    }
  }
  const tenantHeader = tenantHeaderOf(providers)
  if (!headers.has(tenantHeader)) {
    const tenant = providers.tenantProvider?.() ?? null
    if (tenant !== null && tenant !== '') {
      headers.set(tenantHeader, tenant)
    }
  }
  if (options.omitAccessToken === true) {
    return false
  }
  const token = providers.accessTokenStore?.get() ?? null
  if (token !== null && token !== '') {
    headers.set('authorization', `Bearer ${token}`)
    return true
  }
  return false
}
