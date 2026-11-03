/**
 * Seeds the `tenant` query parameter the tenant-addressed console pages
 * open on (see src/internal/tenant-scope.tsx). The pages read it through
 * window.location at first render, so a case sets it before rendering and
 * clears it afterwards.
 */

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
