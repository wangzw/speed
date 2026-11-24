import claimManifest from '../backend-prefixes.json' with { type: 'json' }

export type BackendPrefixClaims = Readonly<Record<string, readonly string[]>>

export const BACKEND_PREFIX_CLAIMS: BackendPrefixClaims = claimManifest

export const BACKEND_PREFIXES: readonly string[] =
  Object.values(BACKEND_PREFIX_CLAIMS).flat()

export function fallsUnderPrefix(pathname: string, prefix: string): boolean {
  return pathname === prefix || pathname.startsWith(`${prefix}/`)
}

export function proxyKeyFor(prefix: string): string {
  return `^${escapeRegExp(prefix)}(?:[/?]|$)`
}

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
