/**
 * The landing a failed social sign-in comes back on.
 *
 * A social callback is a top-level navigation to the authentication
 * capability's callback route. When the login is refused, the server lands
 * the browser back in the product with the machine-readable problem code in
 * the URL fragment, and, for a link-required refusal that named one, the
 * masked address whose account collided:
 *
 *   /#authn_error=link-required&authn_email=a***@home.org
 *
 * The fragment rather than a query parameter keeps the code out of access
 * logs and out of the Referer; the code itself is the vocabulary the sign-in
 * surface already renders (`@speed/auth-ui`'s inline error).
 */

/** The fragment key a failed callback lands with. */
const LANDING_KEY = 'authn_error'

/** The fragment key of the masked address a link-required refusal names. */
const LANDING_EMAIL_KEY = 'authn_email'

/** The module the callback's problem codes belong to. */
const AUTHN_MODULE = 'authn'

/**
 * readCallbackError answers the error the current URL carries, in the
 * vocabulary the surfaces render, and leaves the URL untouched. It answers
 * undefined when the URL carries none.
 *
 * The fragment is a query string: `authn_error=<code>` names the problem as
 * the server names it (`link-required`), optionally followed by
 * `authn_email=<masked address>`. The code becomes the api-client's
 * derivation of it, the module and the code with every dash normalized to an
 * underscore (`authn.link_required`). Only `link-required` carries the
 * address; a fragment that pairs it with another code is ignored, because the
 * server never writes that pair and the fragment is the browser's to forge.
 *
 * `URLSearchParams` reads the fragment as the query string the server wrote
 * with `url.QueryEscape`: it turns `+` into a space and leaves a malformed
 * percent sequence as a replacement character instead of throwing, so an
 * unrelated fragment such as `#discount=50%` cannot take the surface down.
 */
export function readCallbackError():
  | { code: string; email?: string }
  | undefined {
  const params = fragmentParams()
  if (params === null) {
    return undefined
  }
  const code = params.get(LANDING_KEY)
  if (code === null || code === '') {
    return undefined
  }
  const error: { code: string; email?: string } = {
    code: `${AUTHN_MODULE}.${code.replace(/-/g, '_')}`,
  }
  if (code === 'link-required') {
    const email = params.get(LANDING_EMAIL_KEY)
    if (email !== null && email !== '') {
      error.email = email
    }
  }
  return error
}

/**
 * clearCallbackFragment removes the landing fragment from the URL so a
 * reload does not repeat the message. It leaves a fragment that carries no
 * landing key alone: a marketing or analytics fragment is not this
 * surface's to remove.
 */
export function clearCallbackFragment(): void {
  const params = fragmentParams()
  if (params === null || !params.has(LANDING_KEY)) {
    return
  }
  const url = new URL(window.location.href)
  url.hash = ''
  window.history.replaceState(window.history.state, '', url.toString())
}

/** The current fragment as query parameters, or null when there is none. */
function fragmentParams(): URLSearchParams | null {
  const { hash } = window.location
  if (!hash.startsWith('#')) {
    return null
  }
  return new URLSearchParams(hash.slice(1))
}
