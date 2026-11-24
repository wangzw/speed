/** Seeds and clears the `tenant` query parameter the tenant-addressed pages read. */

/** Put `tenant` on the current URL without navigating or reloading. */
export function seedTenant(tenant: string): void {
  const { pathname } = window.location
  window.history.replaceState(null, '', `${pathname}?tenant=${tenant}`)
}

/** Remove the seed, leaving the current path. */
export function clearTenant(): void {
  const { pathname } = window.location
  window.history.replaceState(null, '', pathname)
}
