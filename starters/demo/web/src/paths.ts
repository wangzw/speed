/**
 * The app's own route paths, in one place.
 *
 * The route table (src/routes.tsx) decides which pages live where; the
 * chrome (src/chrome.tsx) links into the account page and the console, and
 * the start page (src/pages/home-page.tsx) links into every capability.
 * Keeping the path strings here lets them all read the same constant instead
 * of each spelling the URL, and keeps a module the route table imports from
 * importing the route table back (which would form a cycle). One path comes
 * from the surface that declares it rather than being spelled here: the
 * notification centre's, whose value lives in @speed/notification-ui.
 */

import { notificationRoutes } from '@speed/notification-ui'

/** The path prefix the platform console's pages mount under. */
export const CONSOLE_PREFIX = '/console'

/** The route a social or enterprise provider's round trip lands on. The
 * app renders its page outside the tenant shell (src/app.tsx).
 *
 * The path sits outside every prefix the backend endpoints claim
 * (backend-prefixes.json). A page path under a claimed prefix is answered
 * by that endpoint -- an anonymous navigation gets its 401 -- instead of
 * being served the entry document. */
export const AUTH_CALLBACK_PATH = '/sign-in/callback'

/** The tenant area's account page: the account-management surfaces. */
export const ACCOUNT_PATH = '/account'

/**
 * The account setup page: where a registration completed through the
 * sign-in surface continues, adding a username and then a password. It is
 * a completion point, not a navigation destination.
 */
export const ACCOUNT_SETUP_PATH = '/account/setup'

/**
 * The registration verification page: where a password registration lands so
 * the new account can prove its email address. Like the setup page it is a
 * completion point, not a navigation destination.
 */
export const ACCOUNT_VERIFY_PATH = '/account/verify'

/** The tenant area's object storage page: the objectstore capability's face. */
export const OBJECTS_PATH = '/objects'

/**
 * The notification centre's path, taken from the declaration the surface
 * mounts from (@speed/notification-ui) -- the one source the route table, the
 * header bell and the start page all read, so the three never drift.
 */
export const NOTIFICATIONS_PATH: string = notificationRoutes[0]?.path ?? '/'

/**
 * The social-binding callback route the account page's bind flow returns
 * to and whose completion the handler owns.
 */
export const ACCOUNT_CALLBACK_PATH = '/account/callback'

/**
 * The account-level pages: the routes that read no tenant data, so they
 * stay usable whatever the tenant context's state. The account settings
 * page, the setup page a registration completes on and the verification page
 * that registration lands on are the ones the design names; the binding
 * callback is the same family (it completes a write of the account's own),
 * and the tenant frame renders all four inside the tenant area.
 */
const ACCOUNT_LEVEL_PATHS: ReadonlySet<string> = new Set([
  ACCOUNT_PATH,
  ACCOUNT_SETUP_PATH,
  ACCOUNT_VERIFY_PATH,
  ACCOUNT_CALLBACK_PATH,
])

/** Whether the path is an account-level page (no tenant-scoped reads). */
export function isAccountLevelPath(pathname: string): boolean {
  return ACCOUNT_LEVEL_PATHS.has(pathname)
}

/**
 * The public invite-acceptance route's prefix: an invitee's link lands on
 * `/invitations/<token>`. The route is not a navigation destination (it is
 * unlabelled and renders outside both shells, src/app.tsx), so it carries no
 * entry in the app's navigation; the prefix and its matcher live here so the
 * route table and the shell switch read the same shape.
 */
export const INVITATION_PREFIX = '/invitations'

/** The path an invitation link points at for one token. The landing route's
 * own declaration (`${INVITATION_PREFIX}/:token`) is what matches the URL and
 * hands the page its token, so this builder is the only side that spells the
 * shape. */
export function invitationPath(token: string): string {
  return `${INVITATION_PREFIX}/${encodeURIComponent(token)}`
}
