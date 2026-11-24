import type { AccessTokenStore } from './access-token.js'

export const TENANT_HEADER = 'x-tenant'

export interface SeamProviders {
  readonly accessTokenStore?: AccessTokenStore
  readonly languageProvider?: () => string | null
  readonly tenantProvider?: () => string | null
  readonly tenantHeader?: string
}

function tenantHeaderOf(providers: SeamProviders): string {
  const configured = providers.tenantHeader
  return configured === undefined || configured === ''
    ? TENANT_HEADER
    : configured
}

/** Applies each seam header only when the caller did not set it; returns whether a bearer token was attached, the gate the 401 refresh path reads. */
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
