/**
 * The landing a refused social sign-in comes back on: the code is read out of
 * the URL fragment without changing the URL, and the fragment is cleared
 * separately so the message survives one render and a reload does not repeat
 * it.
 */

import { describe, expect, it } from 'vitest'
import { clearCallbackFragment, readCallbackError } from './sign-in-landing.js'

describe('readCallbackError', () => {
  it('answers the code the fragment carries and leaves the URL alone', () => {
    window.history.replaceState({}, '', '/#authn_error=link-required')

    expect(readCallbackError()).toEqual({ code: 'authn.link_required' })
    expect(window.location.hash).toBe('#authn_error=link-required')
  })

  it('answers nothing when the URL carries no fragment', () => {
    window.history.replaceState({}, '', '/')

    expect(readCallbackError()).toBeUndefined()
  })

  it('answers nothing for a fragment of another key', () => {
    window.history.replaceState({}, '', '/#something-else=1')

    expect(readCallbackError()).toBeUndefined()
  })

  it('normalizes the dash in the module code to an underscore', () => {
    window.history.replaceState({}, '', '/#authn_error=flow-invalid')

    expect(readCallbackError()).toEqual({ code: 'authn.flow_invalid' })
  })

  it('answers nothing for an empty code', () => {
    window.history.replaceState({}, '', '/#authn_error=')

    expect(readCallbackError()).toBeUndefined()
  })

  it('answers the masked address a link-required fragment carries', () => {
    window.history.replaceState(
      {},
      '',
      '/#authn_error=link-required&authn_email=a***@home.org',
    )

    expect(readCallbackError()).toEqual({
      code: 'authn.link_required',
      email: 'a***@home.org',
    })
  })

  it('decodes an encoded address', () => {
    window.history.replaceState(
      {},
      '',
      '/#authn_error=link-required&authn_email=a%2A%2A%2A%40home.org',
    )

    expect(readCallbackError()).toEqual({
      code: 'authn.link_required',
      email: 'a***@home.org',
    })
  })

  it('answers only the code when the fragment carries no address', () => {
    window.history.replaceState({}, '', '/#authn_error=link-required')

    expect(readCallbackError()).not.toHaveProperty('email')
  })

  it('ignores an address paired with a code that never names one', () => {
    // The fragment is the browser's to forge; the server writes authn_email
    // only for link-required, so any other pairing is dropped rather than
    // shown as if the server had sent it.
    window.history.replaceState(
      {},
      '',
      '/#authn_error=login-failed&authn_email=a***@home.org',
    )

    expect(readCallbackError()).toEqual({ code: 'authn.login_failed' })
  })

  it('ignores an unrelated fragment with a malformed percent sequence', () => {
    // A marketing or analytics fragment can carry a bare %. It must not throw
    // out of the mount effect, and it must not be consumed as an error.
    window.history.replaceState({}, '', '/#discount=50%')

    expect(readCallbackError()).toBeUndefined()
  })

  it('reads a malformed percent sequence in the callback fragment as text', () => {
    window.history.replaceState(
      {},
      '',
      '/#authn_error=link-required&authn_email=50%25',
    )

    expect(readCallbackError()).toEqual({
      code: 'authn.link_required',
      email: '50%',
    })
  })
})

describe('clearCallbackFragment', () => {
  it('removes a landing fragment', () => {
    window.history.replaceState(
      {},
      '',
      '/account/callback#authn_error=conflict',
    )

    clearCallbackFragment()

    expect(window.location.hash).toBe('')
    expect(window.location.pathname).toBe('/account/callback')
  })

  it('removes a landing fragment even when the code is empty', () => {
    window.history.replaceState({}, '', '/#authn_error=')

    clearCallbackFragment()

    expect(window.location.hash).toBe('')
  })

  it('leaves a fragment that carries no landing key', () => {
    window.history.replaceState({}, '', '/#discount=50%')

    clearCallbackFragment()

    expect(window.location.hash).toBe('#discount=50%')
  })

  it('leaves a URL with no fragment alone', () => {
    window.history.replaceState({}, '', '/')

    clearCallbackFragment()

    expect(window.location.hash).toBe('')
  })
})
