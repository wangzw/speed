import { useAuthState } from '@speed/auth-core'
import { adminRoutes } from '@speed/admin-ui'
import type { AdminRoute, AdminRouteTable } from '@speed/admin-ui'
import { ADMIN_PREFIX } from './paths.js'

export const adminShellRoutes: AdminRouteTable = adminRoutes.map(
  (route) => ({
    ...route,
    path: route.path === '/' ? ADMIN_PREFIX : `${ADMIN_PREFIX}${route.path}`,
  }),
)

export const ADMIN_LANDING_PATH =
  adminShellRoutes[0]?.path ?? ADMIN_PREFIX

export function isAdminPath(path: string): boolean {
  return path === ADMIN_PREFIX || path.startsWith(`${ADMIN_PREFIX}/`)
}

export function firstAdminTarget(
  routes: readonly AdminRoute[],
  platformPermissions: readonly string[] | null,
): string | null {
  if (platformPermissions === null || platformPermissions.length === 0) {
    return null
  }
  const entry = routes.find(
    (route) =>
      route.label !== undefined &&
      (route.permission === undefined ||
        platformPermissions.includes(route.permission)),
  )
  return entry?.path ?? null
}

export function useAdminTarget(
  routes: readonly AdminRoute[],
): string | null {
  const snapshot = useAuthState()
  if (snapshot.impersonating) {
    return null
  }
  return firstAdminTarget(routes, snapshot.permissionSets.system)
}
