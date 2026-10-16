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

/** The tenant area's account page: the account-management surfaces. */
export const ACCOUNT_PATH = '/account'

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
 * The public invite-acceptance route's prefix: an invitee's link lands on
 * `/invitations/<token>`. The route is not a navigation destination (it is
 * unlabelled and renders outside both shells, src/app.tsx), so it carries no
 * entry in the app's navigation; the prefix and its matcher live here so the
 * route table and the shell switch read the same shape.
 */
export const INVITATION_PREFIX = '/invitations'

/** The path an invitation link points at for one token. */
export function invitationPath(token: string): string {
  return `${INVITATION_PREFIX}/${encodeURIComponent(token)}`
}

/**
 * The token a path addresses, or null when the path is not an invitation
 * landing (the prefix itself, a deeper path, or anything else). The matcher
 * takes exactly one segment after the prefix.
 */
export function matchInvitationToken(pathname: string): string | null {
  const prefix = `${INVITATION_PREFIX}/`
  if (!pathname.startsWith(prefix)) {
    return null
  }
  const rest = pathname.slice(prefix.length)
  if (rest === '' || rest.includes('/')) {
    return null
  }
  return decodeURIComponent(rest)
}
