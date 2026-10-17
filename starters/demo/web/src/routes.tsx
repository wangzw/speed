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

import type { FeatureName } from '@speed/api-sdk/features'
import { notificationRoutes } from '@speed/notification-ui'
import type { RouteDeclaration, RouteTable } from '@speed/route-kit'
import { consoleShellRoutes } from './console-routes.js'
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
  NOTIFICATIONS_PATH,
  OBJECTS_PATH,
} from './paths.js'
// The console area's prefixed declarations and the prefix test live in
// src/console-routes.ts, shared with the start page (which must not import
// this module back). Re-exported here so the table stays the single route
// import site.
export {
  CONSOLE_LANDING_PATH,
  consoleShellRoutes,
  isConsolePath,
} from './console-routes.js'

/**
 * The invitation landing's declaration path. The app renders its page outside
 * both shells, so the shell switch identifies the declaration by this path
 * rather than pre-empting every path that merely looks like one.
 */
export const INVITATION_ROUTE_PATH = `${INVITATION_PREFIX}/:token`

/**
 * The declaration shape this host writes: route-kit's own plus a `requires`
 * narrowed to the generated `FeatureName`. The `satisfies` on each table below
 * makes a misspelled feature name a compile error, not a test-time finding.
 */
type HostRouteDeclaration = RouteDeclaration & {
  readonly requires?: readonly FeatureName[]
}

/** The route a social or enterprise provider's round trip lands on. The
 * app renders its page outside the tenant shell (src/app.tsx).
 *
 * The path sits outside every prefix the backend endpoints claim
 * (backend-prefixes.json). A page path under a claimed prefix is answered by
 * that endpoint -- an anonymous navigation gets its 401 -- instead of being
 * served the entry document. */
export const AUTH_CALLBACK_PATH = '/sign-in/callback'

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
    // The object page's reads all land on the objectstore capability's
    // routes: a deployment without objectstore.metadata does not serve the
    // feature, so the page is a 404 and the navigation drops it.
    requires: ['objectstore.objects'],
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
] satisfies readonly HostRouteDeclaration[]

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
] satisfies readonly HostRouteDeclaration[]

/**
 * The invite-acceptance route: the public landing an invitation link points
 * at, `/invitations/<token>`. It is unlabelled (it adds no navigation entry)
 * and the app renders its page outside both shells (src/app.tsx), because an
 * invitee arrives before any session exists. It is inert until an
 * administrator issues an invitation.
 *
 * The declaration is gated on the invite module's feature: a deployment
 * without authn.invite serves no invitation operations, so the landing is a
 * 404 like any unregistered path (and the shell switch stops pre-empting it).
 */
export const invitationRoutes: RouteTable = [
  {
    path: INVITATION_ROUTE_PATH,
    view: (context) => (
      <AcceptInvitationPage token={context.params.token ?? ''} />
    ),
    requires: ['authn.invitations'],
  },
] satisfies readonly HostRouteDeclaration[]

/** The product's pages: the business slot, the callback and invitation
 * routes, and the mounted console area (the dashboard at the prefix
 * itself). */
export const routes: RouteTable = [
  ...businessRoutes,
  ...authCallbackRoutes,
  ...invitationRoutes,
  ...consoleShellRoutes,
]
