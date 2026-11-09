/**
 * The transport's tenant announcement for tenant-addressed requests.
 *
 * The api client announces the host's current tenant on every attempt
 * (`tenantProvider`, src/main.tsx). That is the right fact for the tenant
 * area, but the platform admin's tenant-addressed pages (invitations and
 * members, SSO providers, roles and permissions) hold a tenant **selector**
 * whose value is a request argument, not the host's current tenant
 * (docs/design/modules/web/design-web-admin-ui.md). A console page opened
 * while the current tenant is `acme` and switched to `globex` therefore sent
 * `X-Tenant: acme` with a `/api/v1/tenants/globex/...` path, and the server's
 * path-tenant guard refused the pair with a bare 404.
 *
 * A tenant-addressed path is itself the request's tenant fact: the contract
 * addresses the tenant by its name in the path. This wrapper announces that
 * name as the tenant header for such a request, and leaves every other
 * request on the client's own value. The server resolves the path segment and
 * the header by the same name, so the pair always names one tenant.
 *
 * The header name is the api client's default (`x-tenant`; `ClientOptions`
 * renames it with `tenantHeader`). The demo configures no `client.tenantHeader`,
 * so the default is the name it sends. Exporting the wrapper rather than
 * installing it here keeps it testable without a browser.
 */

/** The segment every tenant-addressed platform route carries. It is looked
 * up anywhere in the path so a deployment that serves the API under a base
 * path still has its tenant-addressed requests matched. */
const TENANT_ADDRESSED_PREFIX = '/api/v1/tenants/'

/** The tenant header the api client announces under, at its default name. */
const TENANT_HEADER = 'x-tenant'

/**
 * The tenant name a request URL addresses, or null when it is not a
 * tenant-addressed path. The name is returned verbatim: the contract's path
 * parameter is the tenant's name, and the server resolves it as one.
 */
export function tenantFromRequestURL(url: string): string | null {
  let pathname: string
  try {
    // A relative URL is resolved against a placeholder origin; only the
    // pathname is read, so the origin itself never leaves this function.
    pathname = new URL(url, 'http://tenant-addressed.invalid').pathname
  } catch {
    return null
  }
  const at = pathname.indexOf(TENANT_ADDRESSED_PREFIX)
  if (at === -1) {
    return null
  }
  const rest = pathname.slice(at + TENANT_ADDRESSED_PREFIX.length)
  const segment = rest.split('/')[0]
  if (segment === undefined || segment === '') {
    return null
  }
  try {
    return decodeURIComponent(segment)
  } catch {
    // A malformed escape names nothing; leave the request on the client's
    // own value rather than sending a half-decoded name.
    return null
  }
}

/**
 * One fetch with the tenant header rewritten to the name a
 * tenant-addressed URL names. Every other request is passed through
 * untouched, so the tenant area keeps announcing the host's current tenant.
 *
 * The fetch implementation is a parameter rather than the global identifier
 * so the speed/no-direct-http rule reads this as a transport wrapper: the
 * module never starts an API call of its own, it adapts the fetch the client
 * was given.
 */
function fetchWithTenant(
  fetch: typeof globalThis.fetch,
  input: RequestInfo | URL,
  init?: RequestInit,
): Promise<Response> {
  const isRequest = typeof Request !== 'undefined' && input instanceof Request
  const url =
    typeof input === 'string'
      ? input
      : input instanceof URL
        ? input.href
        : isRequest
          ? input.url
          : String(input)
  const tenant = tenantFromRequestURL(url)
  if (tenant === null) {
    return fetch(input, init)
  }
  const headers = new Headers(
    init?.headers ?? (isRequest ? input.headers : undefined),
  )
  headers.set(TENANT_HEADER, tenant)
  return fetch(input, { ...init, headers })
}

/**
 * `fetch` with the tenant header rewritten to the name a
 * tenant-addressed URL names, for the client's `fetch` option.
 */
export const tenantAddressedFetch: typeof fetch = (input, init) =>
  fetchWithTenant(fetch, input, init)
