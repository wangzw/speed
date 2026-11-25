/** The part of a TanStack Query the placeholder function reads. */
export interface PreviousQueryKey {
  readonly queryKey: readonly unknown[]
}

/**
 * Returns the previous page while the key still names the same tenant, and
 * undefined once the value at `tenantSlot` differs. `tenantSlot` is the key
 * position carrying the tenant.
 */
export function keepPreviousPageWithinTenant<TData>(
  queryKey: readonly unknown[],
  tenantSlot: number,
): (
  previousData: TData | undefined,
  previousQuery: PreviousQueryKey | undefined,
) => TData | undefined {
  const tenant = queryKey[tenantSlot]
  return (previousData, previousQuery) =>
    previousQuery !== undefined &&
    previousQuery.queryKey[tenantSlot] === tenant
      ? previousData
      : undefined
}
