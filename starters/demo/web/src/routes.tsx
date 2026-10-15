/**
 * The app's route table: the one place it names which pages it is made of.
 *
 * Two contributors share route-kit's declarative registration surface:
 *
 *  - The business slot. The skeleton carries no business domain, so it ships
 *    one placeholder declaration at the root; a product replaces it with its
 *    own pages, each a path / view / optional label / optional permission
 *    declaration.
 *
 *  - The console area. @speed/console-ui declares the platform administration
 *    surfaces (the tenant roster, settings, the audit query) at /tenants,
 *    /settings and /audit; the app mounts them under CONSOLE_PREFIX so the two
 *    shells' pages live in disjoint path spaces.
 *
 * One table, two shells: the app chooses the shell by which area the current
 * URL selects (src/app.tsx), while RouteView renders the matched declaration
 * for either. The console-ui declarations are structurally route-kit's
 * RouteDeclaration -- the two packages share the shape without an import edge
 * between them -- so they drop straight into the table.
 */

import { consoleRoutes } from '@speed/console-ui'
import type { ConsoleRouteTable } from '@speed/console-ui'
import type { RouteTable } from '@speed/route-kit'
import { DemoNavLabel } from './nav-label.js'
import { HomePage } from './pages/home-page.js'

/** The path prefix the platform console's pages mount under. */
export const CONSOLE_PREFIX = '/console'

/**
 * Whether a declared path belongs to the console area. The console's pages are
 * all labelled, so a URL under the prefix selects one of them and the app can
 * tell the console area from the tenant area by the selected entry alone.
 */
export function isConsolePath(path: string): boolean {
  return path === CONSOLE_PREFIX || path.startsWith(`${CONSOLE_PREFIX}/`)
}

/** The console's declarations mapped onto their prefixed URLs. */
export const consoleShellRoutes: ConsoleRouteTable = consoleRoutes.map(
  (route) => ({ ...route, path: `${CONSOLE_PREFIX}${route.path}` }),
)

/** The business slot: the demo's own pages, which it does not have yet. */
export const businessRoutes: RouteTable = [
  {
    path: '/',
    view: () => <HomePage />,
    label: <DemoNavLabel labelKey="nav.home" />,
  },
]

/** The product's pages: the business slot plus the mounted console area. */
export const routes: RouteTable = [...businessRoutes, ...consoleShellRoutes]
