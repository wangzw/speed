/**
 * The app's own route paths, in one place.
 *
 * The route table (src/routes.tsx) decides which pages live where; the
 * chrome (src/chrome.tsx) links into the account page and the console.
 * Keeping the path strings here lets both read the same constant instead
 * of each spelling the URL, and keeps the chrome from importing the route
 * table (which would form a cycle, the table importing the pages the
 * chrome wraps).
 */

/** The path prefix the platform console's pages mount under. */
export const CONSOLE_PREFIX = '/console'

/** The tenant area's account page: the account-management surfaces. */
export const ACCOUNT_PATH = '/account'

/**
 * The social-binding callback route the account page's bind flow returns
 * to and whose completion the handler owns.
 */
export const ACCOUNT_CALLBACK_PATH = '/account/callback'
