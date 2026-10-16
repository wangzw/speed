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
 *    surfaces -- the dashboard at the console root, the tenant roster and one
 *    tenant's detail, the audit query and one record's detail, settings and
 *    the rest -- and the app mounts them under CONSOLE_PREFIX so the two
 *    shells' pages live in disjoint path spaces. The dashboard sits at the
 *    prefix itself, so the console root renders the dashboard rather than
 *    redirecting to a page below it.
 *
 * One table, two shells: the app chooses the shell by which area the current
 * URL selects (src/app.tsx), while RouteView renders the matched declaration
 * for either. The console-ui declarations are structurally route-kit's
 * RouteDeclaration -- the two packages share the shape without an import edge
 * between them -- so they drop straight into the table.
 */

import { consoleRoutes } from '@speed/console-ui'
import type { ConsoleRouteTable } from '@speed/console-ui'
import { notificationRoutes } from '@speed/notification-ui'
import type { RouteTable } from '@speed/route-kit'
import { DemoNavLabel } from './nav-label.js'
import {
  AccountBindingCallbackPage,
  AccountSettingsPage,
} from './pages/account-page.js'
import { AcceptInvitationPage } from './pages/accept-invitation-page.js'
import { AuthCallbackPage } from './pages/auth-callback-page.js'
import { HomePage } from './pages/home-page.js'
import { ObjectStoreHostPage } from './pages/objects-page.js'
import {
  ACCOUNT_CALLBACK_PATH,
  ACCOUNT_PATH,
  CONSOLE_PREFIX,
  INVITATION_PREFIX,
  OBJECTS_PATH,
} from './paths.js'

// The path constants live in src/paths.ts, shared with the chrome that
// links into them; re-exported here so the route table stays the single
// import site for the app's paths.
export {
  ACCOUNT_CALLBACK_PATH,
  ACCOUNT_PATH,
  CONSOLE_PREFIX,
  OBJECTS_PATH,
} from './paths.js'

/** The route a social or enterprise provider's round trip lands on. The
 * app renders its page outside the tenant shell (src/app.tsx). */
export const AUTH_CALLBACK_PATH = '/auth/callback'

/**
 * Whether a declared path belongs to the console area. The console's pages are
 * all labelled, so a URL under the prefix selects one of them and the app can
 * tell the console area from the tenant area by the selected entry alone.
 */
export function isConsolePath(path: string): boolean {
  return path === CONSOLE_PREFIX || path.startsWith(`${CONSOLE_PREFIX}/`)
}

/**
 * The console's declarations mapped onto their prefixed URLs. The
 * dashboard declares itself at the console root (path '/'), so it mounts
 * at the prefix itself; every other page keeps its path below the prefix.
 */
export const consoleShellRoutes: ConsoleRouteTable = consoleRoutes.map(
  (route) => ({
    ...route,
    path: route.path === '/' ? CONSOLE_PREFIX : `${CONSOLE_PREFIX}${route.path}`,
  }),
)

/** The path the console root serves: the dashboard. */
export const CONSOLE_LANDING_PATH =
  consoleShellRoutes[0]?.path ?? CONSOLE_PREFIX

/** The business slot: the skeleton's placeholder page, its account page,
 * and the notification center (@speed/notification-ui's declaration, whose
 * view renders the header bell's destination). */
export const businessRoutes: RouteTable = [
  {
    path: '/',
    view: () => <HomePage />,
    label: <DemoNavLabel labelKey="nav.home" />,
  },
  {
    path: ACCOUNT_PATH,
    view: () => <AccountSettingsPage />,
    label: <DemoNavLabel labelKey="nav.account" />,
  },
  {
    path: OBJECTS_PATH,
    view: () => <ObjectStoreHostPage />,
    label: <DemoNavLabel labelKey="nav.objects" />,
  },
  // The notification surface: the same declarative seam as the console's
  // pages -- notification-ui exports a structurally route-kit declaration,
  // so it drops straight into the table.
  ...notificationRoutes,
  // Unlabelled: the binding callback is a completion point, not a
  // destination in the navigation.
  {
    path: ACCOUNT_CALLBACK_PATH,
    view: () => <AccountBindingCallbackPage />,
  },
]

/** The path the notification surface declares: the header bell's
 * navigation target, taken from the declaration so the two never drift. */
export const NOTIFICATIONS_PATH: string = notificationRoutes[0]?.path ?? '/'

/**
 * The auth callback route: where a redirect or enterprise provider's round
 * trip lands. Unlabelled (it adds no navigation entry); the app renders its
 * page outside the tenant shell (src/app.tsx). It is inert until a
 * deployment enables a redirect or enterprise method.
 */
export const authCallbackRoutes: RouteTable = [
  {
    path: AUTH_CALLBACK_PATH,
    view: () => <AuthCallbackPage />,
  },
]

/**
 * The invite-acceptance route: the public landing an invitation link points
 * at, `/invitations/<token>`. It is unlabelled (it adds no navigation entry)
 * and the app renders its page outside both shells (src/app.tsx), because an
 * invitee arrives before any session exists. It is inert until an
 * administrator issues an invitation.
 */
export const invitationRoutes: RouteTable = [
  {
    path: `${INVITATION_PREFIX}/:token`,
    view: (context) => (
      <AcceptInvitationPage token={context.params.token ?? ''} />
    ),
  },
]

/** The product's pages: the business slot, the callback and invitation
 * routes, and the mounted console area (the dashboard at the prefix
 * itself). */
export const routes: RouteTable = [
  ...businessRoutes,
  ...authCallbackRoutes,
  ...invitationRoutes,
  ...consoleShellRoutes,
]
