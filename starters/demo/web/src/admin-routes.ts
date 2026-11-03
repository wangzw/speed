/**
 * The admin area's route facts, in one place shared by the route table, the
 * shell switch, the header entry and the start page.
 *
 * The admin's declarations arrive from @speed/admin-ui with paths relative
 * to the admin root; the app mounts them under ADMIN_PREFIX so the two
 * shells' pages live in disjoint path spaces. The dashboard declares itself at
 * '/', so it mounts at the prefix itself. These derivations live here, apart
 * from src/routes.tsx, because the start page reads them too and the route
 * table imports the start page -- placing them there would form a cycle.
 */

import { useAuthState } from '@speed/auth-core'
import { adminRoutes } from '@speed/admin-ui'
import type { AdminRoute, AdminRouteTable } from '@speed/admin-ui'
import { ADMIN_PREFIX } from './paths.js'
import { ReviewSmsPage } from './review-sms-page.js'

/**
 * The admin's declarations mapped onto their prefixed URLs. The
 * dashboard declares itself at the admin root (path '/'), so it mounts
 * at the prefix itself; every other page keeps its path below the prefix.
 */
export const adminShellRoutes: AdminRouteTable = adminRoutes.map(
  (route) => ({
    ...route,
    path: route.path === '/' ? ADMIN_PREFIX : `${ADMIN_PREFIX}${route.path}`,
    // The SMS page is the host's composition of the package page and the
    // review build's verdict control; every other page is the package's own.
    ...(route.id === 'sms' ? { view: ReviewSmsPage } : {}),
  }),
)

/** The path the admin root serves: the dashboard. */
export const ADMIN_LANDING_PATH =
  adminShellRoutes[0]?.path ?? ADMIN_PREFIX

/**
 * Whether a declared path belongs to the admin area. The admin's pages are
 * all labelled, so a URL under the prefix selects one of them and the app can
 * tell the admin area from the tenant area by the path alone -- the area is
 * a fact of the URL, never of which pages the feature document leaves in
 * force.
 */
export function isAdminPath(path: string): boolean {
  return path === ADMIN_PREFIX || path.startsWith(`${ADMIN_PREFIX}/`)
}

/**
 * The first admin navigation entry the principal may open: the first
 * labelled declaration, in table order, with no permission code or one the
 * platform permission set holds. Null while the set is unknown (null), while
 * it is empty (an ordinary member), and when no labelled declaration is left
 * in force.
 *
 * Callers pass the declarations their presence predicate left in force, so
 * "the first entry" is always one whose page exists in this deployment. The
 * header entry and the start page's admin card share this one derivation,
 * which is why they never disagree about where the admin is or whether it
 * is offered at all.
 */
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

/**
 * The admin destination the current viewer may open, or null: the one
 * derivation the header entry (src/app.tsx) and the start page's admin
 * card (src/pages/home-page.tsx) share, so the two never disagree.
 *
 * An impersonating identity has no admin destination: the impersonation is
 * the identity fact that closes the admin, decided before any permission
 * set is consulted, so the entry hides whatever the target's permission read
 * answers. For every other viewer the criterion is the effective account's
 * platform permission set alone. The set may still be unknown for a moment
 * after an identity change; the entry stays hidden then, as it does for any
 * viewer whose set has not landed.
 */
export function useAdminTarget(
  routes: readonly AdminRoute[],
): string | null {
  const snapshot = useAuthState()
  if (snapshot.impersonating) {
    return null
  }
  return firstAdminTarget(routes, snapshot.permissionSets.system)
}
