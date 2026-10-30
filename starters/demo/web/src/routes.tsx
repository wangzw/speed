/**
 * The app's route table: the one place it names which pages it is made of.
 *
 * Two contributors share route-kit's declarative registration surface:
 *
 *  - The business slot. The skeleton carries no business domain, so it ships
 *    one placeholder declaration at the root; a product replaces it with its
 *    own pages, each a path / view / optional label / optional navigation
 *    icon / optional permission declaration.
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

import { useEffect } from 'react'
import type { ReactElement } from 'react'
import type { FeatureName } from '@speed/api-sdk/features'
import { notificationRoutes } from '@speed/notification-ui'
import { useLocationPath, useRedirect } from '@speed/route-kit'
import type { RouteDeclaration, RouteTable } from '@speed/route-kit'
import { consoleShellRoutes } from './console-routes.js'
import { DemoNavLabel } from './nav-label.js'
import { AccountNavIcon, HomeNavIcon, NotificationsNavIcon } from './nav-icons.js'
import {
  AccountBindingCallbackPage,
  AccountSettingsPage,
} from './pages/account-page.js'
import { AccountSetupPage } from './pages/account-setup-page.js'
import { AcceptInvitationPage } from './pages/accept-invitation-page.js'
import { AuthCallbackPage } from './pages/auth-callback-page.js'
import { HomePage } from './pages/home-page.js'
import { TENANT_HOME_PATH } from './pages/not-found-page.js'
import { PasswordResetPage } from './pages/password-reset-page.js'
import {
  ACCOUNT_CALLBACK_PATH,
  ACCOUNT_PATH,
  ACCOUNT_SECTION_PATHS,
  ACCOUNT_SECTIONS,
  ACCOUNT_SETUP_PATH,
  AUTH_CALLBACK_PATH,
  INVITATION_PREFIX,
  PASSWORD_RESET_PATH,
} from './paths.js'

// The path constants live in src/paths.ts, shared with the chrome that
// links into them; re-exported here so the route table stays the single
// import site for the app's paths.
export {
  ACCOUNT_CALLBACK_PATH,
  ACCOUNT_PATH,
  ACCOUNT_SETUP_PATH,
  AUTH_CALLBACK_PATH,
  CONSOLE_PREFIX,
  NOTIFICATIONS_PATH,
  PASSWORD_RESET_PATH,
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
const INVITATION_ROUTE_PATH = `${INVITATION_PREFIX}/:token`

/**
 * The declaration shape this host writes: route-kit's own plus a `requires`
 * narrowed to the generated `FeatureName`, and the navigation `icon`. The
 * `satisfies` on each table below makes a misspelled feature name a compile
 * error, not a test-time finding. The icon rides the declaration rather than
 * a parallel path-to-icon table, so an entry's destination and its mark are
 * declared once; the frame looks it up by the id `useNavItems` reports
 * (navIconFor below).
 */
type HostRouteDeclaration = RouteDeclaration & {
  readonly requires?: readonly FeatureName[]
  /** The mark the navigation shows for this entry; unlabelled declarations
   * carry none because they contribute no navigation entry. */
  readonly icon?: ReactElement
}

/**
 * The account settings' group address: it holds no page of its own, so it
 * replaces the history entry with a pane page. The visitor's other query
 * rides along -- an older `/account?verify=1` or `?change=email` link still
 * lands on the profile page that reads it -- and an older `?section=` pick
 * names the page it meant. Renders nothing on the way, like the app's other
 * redirects.
 */
function AccountLandingRedirect(): null {
  const redirect = useRedirect()
  const { search } = useLocationPath()
  useEffect(() => {
    const landing = new URLSearchParams(search)
    const picked = landing.get('section')
    const pane = ACCOUNT_SECTIONS.find((section) => section === picked)
    landing.delete('section')
    const rest = landing.toString()
    const target =
      pane === undefined ? ACCOUNT_SECTION_PATHS.profile : ACCOUNT_SECTION_PATHS[pane]
    redirect(rest === '' ? target : `${target}?${rest}`)
  }, [redirect, search])
  return null
}

/** The business slot: the skeleton's placeholder page, its account page,
 * and the notification center (@speed/notification-ui's declaration, whose
 * view renders the header bell's destination). */
export const businessRoutes: readonly HostRouteDeclaration[] = [
  {
    path: TENANT_HOME_PATH,
    view: () => <HomePage />,
    label: <DemoNavLabel labelKey="nav.home" />,
    icon: <HomeNavIcon />,
  },
  // The account settings: the navigation's expandable entry, whose second
  // level is the pane pages below (the frame builds those links,
  // src/app.tsx). The group's own path carries no page: it lands on the
  // first pane with the visitor's query intact, so every older link into
  // `/account?verify=1` and friends still arrives where it meant to.
  {
    path: ACCOUNT_PATH,
    view: () => <AccountLandingRedirect />,
    label: <DemoNavLabel labelKey="nav.account" />,
    icon: <AccountNavIcon />,
  },
  // The panes: one page each, unlabelled because the navigation nests them
  // under the entry above rather than listing them at the top level.
  {
    path: ACCOUNT_SECTION_PATHS.profile,
    view: () => <AccountSettingsPage section="profile" />,
  },
  {
    path: ACCOUNT_SECTION_PATHS.security,
    view: () => <AccountSettingsPage section="security" />,
  },
  {
    path: ACCOUNT_SECTION_PATHS.sessions,
    view: () => <AccountSettingsPage section="sessions" />,
  },
  {
    path: ACCOUNT_SECTION_PATHS.preferences,
    view: () => <AccountSettingsPage section="preferences" />,
  },
  // Unlabelled: a registration's completion point, not a destination in the
  // navigation (reached from the sign-in surface's completion, or by URL).
  {
    path: ACCOUNT_SETUP_PATH,
    view: () => <AccountSetupPage />,
  },
  // The notification surface: the same declarative seam as the console's
  // pages -- notification-ui exports a structurally route-kit declaration,
  // so it drops straight into the table, with the host's icon added beside
  // the package's own path and label.
  ...notificationRoutes.map((route) => ({
    ...route,
    icon: <NotificationsNavIcon />,
  })),
  // Unlabelled: the binding callback is a completion point, not a
  // destination in the navigation.
  {
    path: ACCOUNT_CALLBACK_PATH,
    view: () => <AccountBindingCallbackPage />,
  },
] satisfies readonly HostRouteDeclaration[]

/**
 * The icon of each labelled business declaration, keyed by the declaration
 * path -- the id `useNavItems` reports. The tenant frame composes its
 * entries from those items and looks the mark up here, so a new page needs no
 * second table entry.
 */
const NAV_ICONS: ReadonlyMap<string, ReactElement> = new Map(
  businessRoutes.flatMap((route) =>
    route.icon === undefined
      ? []
      : ([[route.path, route.icon]] as const),
  ),
)

/** The icon the labelled declaration at `path` declares, if any. */
export function navIconFor(path: string): ReactElement | undefined {
  return NAV_ICONS.get(path)
}

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

/**
 * The anonymous password reset route: the public landing the sign-in
 * surface's "forgot password" entry points at, `/password-reset`. It is
 * unlabelled (it adds no navigation entry) and the app renders its page
 * outside both shells (src/app.tsx), because the viewer arrives without a
 * session. It is inert until the deployment publishes a reset channel; a
 * deployment without one still serves the page, whose surface states that no
 * channel is offered.
 */
export const passwordResetRoutes: RouteTable = [
  {
    path: PASSWORD_RESET_PATH,
    view: () => <PasswordResetPage />,
  },
] satisfies readonly HostRouteDeclaration[]

/** The product's pages: the business slot, the callback, invitation and
 * password reset routes, and the mounted console area (the dashboard at the
 * prefix itself). */
export const routes: RouteTable = [
  ...businessRoutes,
  ...authCallbackRoutes,
  ...invitationRoutes,
  ...passwordResetRoutes,
  ...consoleShellRoutes,
]
