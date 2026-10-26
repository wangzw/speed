/**
 * The console area's route facts, in one place shared by the route table, the
 * shell switch, the header entry and the start page.
 *
 * The console's declarations arrive from @speed/console-ui with paths relative
 * to the console root; the app mounts them under CONSOLE_PREFIX so the two
 * shells' pages live in disjoint path spaces. The dashboard declares itself at
 * '/', so it mounts at the prefix itself. These derivations live here, apart
 * from src/routes.tsx, because the start page reads them too and the route
 * table imports the start page -- placing them there would form a cycle.
 */

import { useAuthState } from '@speed/auth-core'
import { consoleRoutes } from '@speed/console-ui'
import type { ConsoleRoute, ConsoleRouteTable } from '@speed/console-ui'
import { CONSOLE_PREFIX } from './paths.js'
import { ReviewSmsPage } from './review-sms-page.js'

/**
 * The console's declarations mapped onto their prefixed URLs. The
 * dashboard declares itself at the console root (path '/'), so it mounts
 * at the prefix itself; every other page keeps its path below the prefix.
 */
export const consoleShellRoutes: ConsoleRouteTable = consoleRoutes.map(
  (route) => ({
    ...route,
    path: route.path === '/' ? CONSOLE_PREFIX : `${CONSOLE_PREFIX}${route.path}`,
    // The SMS page is the host's composition of the package page and the
    // review build's verdict control; every other page is the package's own.
    ...(route.id === 'sms' ? { view: ReviewSmsPage } : {}),
  }),
)

/** The path the console root serves: the dashboard. */
export const CONSOLE_LANDING_PATH =
  consoleShellRoutes[0]?.path ?? CONSOLE_PREFIX

/**
 * Whether a declared path belongs to the console area. The console's pages are
 * all labelled, so a URL under the prefix selects one of them and the app can
 * tell the console area from the tenant area by the path alone -- the area is
 * a fact of the URL, never of which pages the feature document leaves in
 * force.
 */
export function isConsolePath(path: string): boolean {
  return path === CONSOLE_PREFIX || path.startsWith(`${CONSOLE_PREFIX}/`)
}

/**
 * The first console navigation entry the principal may open: the first
 * labelled declaration, in table order, with no permission code or one the
 * platform permission set holds. Null while the set is unknown (null), while
 * it is empty (an ordinary member), and when no labelled declaration is left
 * in force.
 *
 * Callers pass the declarations their presence predicate left in force, so
 * "the first entry" is always one whose page exists in this deployment. The
 * header entry and the start page's console card share this one derivation,
 * which is why they never disagree about where the console is or whether it
 * is offered at all.
 */
export function firstConsoleTarget(
  routes: readonly ConsoleRoute[],
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
 * The console destination the current viewer may open, or null: the one
 * derivation the header entry (src/app.tsx) and the start page's console
 * card (src/pages/home-page.tsx) share, so the two never disagree.
 *
 * An impersonated session has no console to enter -- the area redirects it
 * home whatever the platform permission set says -- so the answer is null
 * there. The set may still be the non-empty one the viewer held before the
 * start (the identity change has not finished re-attaching the empty set),
 * which is exactly the window in which the two entry points would otherwise
 * disagree about whether the console is offered.
 */
export function useConsoleTarget(
  routes: readonly ConsoleRoute[],
): string | null {
  const snapshot = useAuthState()
  if (snapshot.state === 'authenticated' && snapshot.impersonating) {
    return null
  }
  return firstConsoleTarget(routes, snapshot.permissionSets.system)
}
