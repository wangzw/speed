const TENANT_ADDRESSED_PREFIX = '/api/v1/tenants/'

const TENANT_HEADER = 'x-tenant'

export function tenantFromRequestURL(url: string): string | null {
  let pathname: string
  try {
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
    return null
  }
}

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

export const tenantAddressedFetch: typeof fetch = (input, init) =>
  fetchWithTenant(fetch, input, init)
