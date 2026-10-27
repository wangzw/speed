/**
 * query-key.ts -- the tenant-scoped query-key convention, built in one
 * place.
 *
 * Every cached row a signed-in principal reads is keyed under the tenant
 * that principal is acting in, so a tenant switch can never serve the
 * previous tenant's rows. tenantQueryKey(tenantId, ...tail) prepends the
 * tenant id as the key's outermost segment -- one prefix per tenant,
 * shared by all of that tenant's reads -- and evictTenantQueries removes
 * a departing tenant's whole subtree once a switch commits.
 *
 * The tenant id comes from useCurrentTenant (the host's own route, read
 * back through TenantProvider -- the server resolves the tenant per
 * request and the principal carries none), and useTenantQueryKey hands
 * back both the id and the key so a read derives its key and its
 * `enabled` gate from one hook call: a null tenant (no route tenant,
 * or no provider mounted) still yields a key, and the read passes
 * `enabled: tenantId !== null`, failing closed rather than inventing a
 * tenant.
 *
 * The convention lives here, in auth-core, because the tenant is this
 * package's own fact (ADR-web-tenant-query-key): api-client
 * only broadcasts the host's tenant as transport (the tenantProvider
 * value wired to the X-Tenant header), inferring and storing no tenant
 * identifier, and its hooks take and infer no tenant -- so routing the
 * factory through it would either import auth-core back, a cycle, or
 * split one concept across two packages. The factory evaluates nothing,
 * fetches nothing and decides nothing: whether a row should be fetched
 * or shown stays the host's and the server's.
 *
 * The bare key a caller passes is whatever the read is named by -- a
 * generated getXQueryKey(...) result or a literal tail. Query keys are
 * hashed structurally by TanStack Query, so a fresh-but-equal array is
 * one cache entry and a fresh-but-equal prefix matches the same eviction;
 * what a key is here is its contents, not its reference.
 */

import { useMemo } from 'react'
import type { QueryClient } from '@tanstack/react-query'
import { useCurrentTenant } from './hooks.js'

/**
 * The tenant-scoped query key over one bare key: the tenant id as the
 * key's outermost segment, then the bare key's own elements. Every read
 * under one tenant shares this prefix, and the tenant id is what
 * evictTenantQueries removes by. A null tenant id is a key like any
 * other (the outermost segment is null); a read built over it passes
 * `enabled: tenantId !== null`, so it never fetches.
 */
export function tenantQueryKey(
  tenantId: string | null,
  ...tail: readonly unknown[]
): readonly unknown[] {
  return [tenantId, ...tail]
}

/** The tenant-namespaced read one hook call derives: the current tenant
 * id (the `enabled` gate's operand) and the query key built over it. */
export interface TenantQueryKey {
  /** The host-supplied tenant the principal is acting in, or null when
   * no provider is mounted or the host passed null. */
  readonly tenantId: string | null
  /** tenantQueryKey(tenantId, ...bareKey). */
  readonly queryKey: readonly unknown[]
}

/**
 * Derives the tenant-namespaced key and its enablement operand from one
 * hook call: useCurrentTenant for the host's current tenant, the
 * factory for the key. The key is memoized on the tenant id and the
 * bare key, so a caller that hands a stable bare key sees a stable key
 * for the whole stay in a tenant -- the identity `useCurrentTenant`
 * already guarantees for a given tenant id. A null tenant still yields
 * a key.
 */
export function useTenantQueryKey(
  bareKey: readonly unknown[],
): TenantQueryKey {
  const currentTenant = useCurrentTenant()
  const tenantId = currentTenant?.tenantId ?? null
  const queryKey = useMemo(
    () => tenantQueryKey(tenantId, ...bareKey),
    [tenantId, bareKey],
  )
  return { tenantId, queryKey }
}

/**
 * Evicts the departing tenant's whole subtree once a switch has
 * committed. Call it with the tenant id read *before* the switch -- the
 * new tenant's id names nothing to remove. Removes every query whose key
 * begins with that tenant's prefix (the default prefix match of
 * removeQueries); identity-domain rows cached under bare keys carry no
 * tenant segment and are out of its reach, which is the whole-cache
 * eviction's job at session end.
 */
export function evictTenantQueries(
  queryClient: QueryClient,
  tenantId: string,
): void {
  queryClient.removeQueries({ queryKey: tenantQueryKey(tenantId) })
}
