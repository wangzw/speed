/**
 * The backend listener's claimed path prefixes: the namespaces its api,
 * account, admin and content endpoints own.
 *
 * `backend-prefixes.json` beside this package is the list this app's dev proxy
 * generates from; it is a hand-kept mirror of the claims in the server
 * configuration, held to them by the server's
 * TestThePrefixManifestAgreesWithTheDifferentialTableAndTheClaims. The namespaces an endpoint
 * owns are a product fact -- they do not follow which modules a particular
 * build imports or runs -- so the list stands even when a deployment splits
 * the listener again.
 *
 * The web endpoint claims the root: it is the backend listener's fallback
 * endpoint. The root is deliberately absent from the manifest because every
 * path falls under it by definition, and a page path is required to fall
 * under no claimed prefix.
 */
import claimManifest from '../backend-prefixes.json' with { type: 'json' }

/** Endpoint name to the path prefixes it claims on the backend listener. */
export type BackendPrefixClaims = Readonly<Record<string, readonly string[]>>

export const BACKEND_PREFIX_CLAIMS: BackendPrefixClaims = claimManifest

/** Every claimed prefix, in manifest order. */
export const BACKEND_PREFIXES: readonly string[] =
  Object.values(BACKEND_PREFIX_CLAIMS).flat()

/**
 * Whether a path falls under a claimed prefix at a segment boundary: the path
 * is the prefix itself, or continues past it with `/`. This is what keeps a
 * page at `/api/v1/system` clear of the `/api/v1/auth` namespace.
 */
export function fallsUnderPrefix(pathname: string, prefix: string): boolean {
  return pathname === prefix || pathname.startsWith(`${prefix}/`)
}

/**
 * The dev server's proxy-table key for one prefix. Vite matches a key that
 * begins with `^` as a regular expression against the request URL, query
 * string included, and every other key with `startsWith`. The anchored form
 * makes the match end at a segment boundary, which a plain `/api/v1/auth` key does
 * not: that one would swallow `/api/v1/system` as well.
 */
export function proxyKeyFor(prefix: string): string {
  return `^${escapeRegExp(prefix)}(?:[/?]|$)`
}

/**
 * The dev server's proxy table: one segment-anchored entry per claimed
 * prefix, all forwarded to the backend origin. A deployment that splits the
 * endpoints again already has the per-prefix map in place.
 */
export function backendProxyRules(
  target: string,
): Record<string, { readonly target: string }> {
  return Object.fromEntries(
    BACKEND_PREFIXES.map((prefix) => [proxyKeyFor(prefix), { target }]),
  )
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
