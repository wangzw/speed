/**
 * The app's own route paths, in one place.
 *
 * The route table (src/routes.tsx) decides which pages live where; the
 * chrome (src/chrome.tsx) links into the account page and the admin, and
 * the start page (src/pages/home-page.tsx) links into every capability.
 * Keeping the path strings here lets them all read the same constant instead
 * of each spelling the URL, and keeps a module the route table imports from
 * importing the route table back (which would form a cycle). One path comes
 * from the surface that declares it rather than being spelled here: the
 * notification centre's, whose value lives in @speed/notification-ui.
 */

import { notificationRoutes } from '@speed/notification-ui'

/** The path prefix the platform admin's pages mount under. */
export const ADMIN_PREFIX = '/admin'

/**
 * The path prefix the organization admin's pages mount under: the tenant-scoped
 * management surface, which renders inside the tenant shell and acts on the
 * current tenant rather than on platform assets. It sits outside ADMIN_PREFIX
 * so the two management surfaces have disjoint path spaces.
 */
export const ORGANIZATION_ADMIN_PREFIX = '/organization/admin'

/**
 * The organization profile page: the organization management surface's
 * fifth page, where an administrator edits the organization's own profile.
 * It is the landing after a self-service creation, so the new
 * organization's details can be completed before anything else.
 */
export const ORGANIZATION_ADMIN_PROFILE_PATH = `${ORGANIZATION_ADMIN_PREFIX}/profile`

/**
 * The self-service organization-creation page. It sits outside the
 * organization management prefix: the caller may create an organization
 * before belonging to one, so the page is a tenant-area destination, not a
 * page of the organization surface.
 */
export const ORGANIZATIONS_NEW_PATH = '/organizations/new'

/** Whether a declared path belongs to the organization admin area. */
export function isOrganizationAdminPath(pathname: string): boolean {
  return (
    pathname === ORGANIZATION_ADMIN_PREFIX ||
    pathname.startsWith(`${ORGANIZATION_ADMIN_PREFIX}/`)
  )
}

/** The route a social or enterprise provider's round trip lands on. The
 * app renders its page outside the tenant shell (src/app.tsx).
 *
 * The path sits outside every prefix the backend endpoints claim
 * (backend-prefixes.json). A page path under a claimed prefix is answered
 * by that endpoint -- an anonymous navigation gets its 401 -- instead of
 * being served the entry document. */
export const AUTH_CALLBACK_PATH = '/sign-in/callback'

/**
 * The anonymous password reset page: where the sign-in surface's "forgot
 * password" entry sends a viewer. It is not a navigation destination (it
 * renders outside both shells, src/app.tsx) and sits outside every prefix the
 * backend endpoints claim (backend-prefixes.json), like the callback route.
 */
export const PASSWORD_RESET_PATH = '/password-reset'

/** The tenant area's account settings: the group's own address. It carries no
 * page of its own -- it lands on the first pane -- and is what the chrome and
 * the redirects link into. */
export const ACCOUNT_PATH = '/account'

/**
 * The account settings' panes, in navigation order: one page each. The
 * navigation's account entry links into these (src/app.tsx); the pane a URL
 * names is the declaration that matched, not a query on a shared route.
 */
export const ACCOUNT_SECTIONS = [
  'profile',
  'security',
  'sessions',
  'preferences',
] as const

/** One pane of the account settings. */
export type AccountSection = (typeof ACCOUNT_SECTIONS)[number]

/** Each pane's page path. */
export const ACCOUNT_SECTION_PATHS: Record<AccountSection, string> = {
  profile: `${ACCOUNT_PATH}/profile`,
  security: `${ACCOUNT_PATH}/security`,
  sessions: `${ACCOUNT_PATH}/sessions`,
  preferences: `${ACCOUNT_PATH}/preferences`,
}

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
 * The ownership transfer requests' account-level page: the destination
 * every owner-transfer notification links to. It sits outside both shells'
 * tenant scope because the request is addressed to the signed-in person,
 * not to an organization, and the target needs no organization permission
 * to open it.
 */
export const TRANSFER_REQUESTS_PATH = '/transfer-requests'

/** One request's detail: the deep link a notification carries. */
export const TRANSFER_REQUEST_PATH = `${TRANSFER_REQUESTS_PATH}/:request`

/** The detail address for one request. */
export function transferRequestPath(id: string): string {
  return `${TRANSFER_REQUESTS_PATH}/${encodeURIComponent(id)}`
}

/**
 * The account-level pages: the routes that read no tenant data, so they
 * stay usable whatever the tenant context's state. The account settings
 * page, where the email verification runs and a first password is set, is
 * the one the design names; the binding callback is the same family (it
 * completes a write of the account's own), and the tenant frame renders both
 * inside the tenant area. The ownership transfer requests read the
 * caller's own requests and join the same family.
 */
const ACCOUNT_LEVEL_PATHS: ReadonlySet<string> = new Set([
  ACCOUNT_PATH,
  ...Object.values(ACCOUNT_SECTION_PATHS),
  ACCOUNT_CALLBACK_PATH,
  TRANSFER_REQUESTS_PATH,
])

/** Whether the path is an account-level page (no tenant-scoped reads). */
export function isAccountLevelPath(pathname: string): boolean {
  return (
    ACCOUNT_LEVEL_PATHS.has(pathname) ||
    pathname.startsWith(`${TRANSFER_REQUESTS_PATH}/`)
  )
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
