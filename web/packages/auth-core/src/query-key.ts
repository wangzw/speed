import { useMemo } from 'react'
import type { QueryClient } from '@tanstack/react-query'
import { useCurrentTenant } from './hooks.js'

/**
 * The tenant-scoped query key: the tenant id prepended to the bare key's
 * elements. A null tenant id is a key like any other; reads built over it pass
 * `enabled: tenantId !== null`.
 */
export function tenantQueryKey(
  tenantId: string | null,
  ...tail: readonly unknown[]
): readonly unknown[] {
  return [tenantId, ...tail]
}

/** The tenant-namespaced read one hook call derives: the current tenant id and the query key built over it. */
export interface TenantQueryKey {
  /** The host-supplied tenant the principal is acting in, or null when no provider is mounted or the host passed null. */
  readonly tenantId: string | null
  /** tenantQueryKey(tenantId, ...bareKey). */
  readonly queryKey: readonly unknown[]
}

/**
 * Derives the current tenant id and the tenant-scoped key from one hook call;
 * the key is memoized on the tenant id and the bare key.
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
 * Evicts the departing tenant's whole subtree. Pass the tenant id read before
 * the switch; identity-domain rows under bare keys are outside its reach.
 */
export function evictTenantQueries(
  queryClient: QueryClient,
  tenantId: string,
): void {
  queryClient.removeQueries({ queryKey: tenantQueryKey(tenantId) })
}
