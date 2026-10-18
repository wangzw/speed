/**
 * The sign-in surface's navigation for a redirect step.
 *
 * A social or enterprise sign-in begins a server-side flow whose first step
 * carries the provider's authorization URL. The provider's authorization
 * request must replace the current document -- a popup or a client-side route
 * change does not carry the provider's redirect back to this origin's callback
 * route -- so the host navigates the current window to the URL.
 *
 * Kept in its own module so a suite can observe the navigation without a
 * browser: jsdom's `location.assign` is not replaceable.
 */

/** Navigates the current window to the provider's authorization URL. */
export function navigateToAuthorizationUrl(url: string): void {
  window.location.assign(url)
}
