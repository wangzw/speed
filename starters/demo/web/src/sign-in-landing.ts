/**
 * The landing a failed social sign-in comes back on.
 *
 * A social callback is a top-level navigation to the authentication
 * capability's callback route. When the login is refused, the server lands
 * the browser back in the product with the machine-readable problem code in
 * the URL fragment:
 *
 *   /#authn_error=link-required
 *
 * The fragment rather than a query parameter keeps the code out of access
 * logs and out of the Referer; the code itself is the vocabulary the sign-in
 * surface already renders (`@speed/auth-ui`'s inline error).
 */

/** The fragment key a failed callback lands with. */
const LANDING_KEY = '#authn_error='

/** The module the callback's problem codes belong to. */
const AUTHN_MODULE = 'authn'

/**
 * takeCallbackError answers the error code the current URL carries, in the
 * vocabulary the surfaces render, and clears the fragment from the URL so a
 * reload does not repeat the message. It answers undefined when the URL
 * carries none.
 *
 * The fragment carries the problem code as the server names it
 * (`link-required`); the surfaces render the api-client's derivation of it,
 * the module and the code with every dash normalized to an underscore
 * (`authn.link_required`).
 */
export function takeCallbackError(): string | undefined {
  const { hash } = window.location
  if (!hash.startsWith(LANDING_KEY)) {
    return undefined
  }
  const code = decodeURIComponent(hash.slice(LANDING_KEY.length))
  const url = new URL(window.location.href)
  url.hash = ''
  window.history.replaceState(window.history.state, '', url.toString())
  if (code === '') {
    return undefined
  }
  return `${AUTHN_MODULE}.${code.replace(/-/g, '_')}`
}
