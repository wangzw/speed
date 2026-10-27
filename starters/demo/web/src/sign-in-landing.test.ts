/**
 * The landing a refused social sign-in comes back on: the code is read out of
 * the URL fragment and the fragment is cleared, so the message survives one
 * render and a reload does not repeat it.
 */

import { describe, expect, it } from 'vitest'
import { takeCallbackError } from './sign-in-landing.js'

describe('takeCallbackError', () => {
  it('answers the code the fragment carries and clears the fragment', () => {
    window.history.replaceState({}, '', '/#authn_error=link-required')

    expect(takeCallbackError()).toEqual({ code: 'authn.link_required' })
    expect(window.location.hash).toBe('')
  })

  it('answers nothing when the URL carries no fragment', () => {
    window.history.replaceState({}, '', '/')

    expect(takeCallbackError()).toBeUndefined()
  })

  it('answers nothing for a fragment of another key', () => {
    window.history.replaceState({}, '', '/#something-else=1')

    expect(takeCallbackError()).toBeUndefined()
    expect(window.location.hash).toBe('#something-else=1')
  })

  it('normalizes the dash in the module code to an underscore', () => {
    window.history.replaceState({}, '', '/#authn_error=flow-invalid')

    expect(takeCallbackError()).toEqual({ code: 'authn.flow_invalid' })
  })

  it('answers nothing for an empty code', () => {
    window.history.replaceState({}, '', '/#authn_error=')

    expect(takeCallbackError()).toBeUndefined()
  })

  it('answers the masked address a link-required fragment carries', () => {
    window.history.replaceState(
      {},
      '',
      '/#authn_error=link-required&authn_email=a***@home.org',
    )

    expect(takeCallbackError()).toEqual({
      code: 'authn.link_required',
      email: 'a***@home.org',
    })
    expect(window.location.hash).toBe('')
  })

  it('decodes an encoded address', () => {
    window.history.replaceState(
      {},
      '',
      '/#authn_error=link-required&authn_email=a%2A%2A%2A%40home.org',
    )

    expect(takeCallbackError()).toEqual({
      code: 'authn.link_required',
      email: 'a***@home.org',
    })
  })

  it('answers only the code when the fragment carries no address', () => {
    window.history.replaceState({}, '', '/#authn_error=link-required')

    expect(takeCallbackError()).not.toHaveProperty('email')
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

    expect(takeCallbackError()).toEqual({ code: 'authn.login_failed' })
  })

  it('ignores an unrelated fragment with a malformed percent sequence', () => {
    // A marketing or analytics fragment can carry a bare %. It must not throw
    // out of the mount effect, and it must not be consumed as an error.
    window.history.replaceState({}, '', '/#discount=50%')

    expect(takeCallbackError()).toBeUndefined()
    expect(window.location.hash).toBe('#discount=50%')
  })

  it('reads a malformed percent sequence in the callback fragment as text', () => {
    window.history.replaceState(
      {},
      '',
      '/#authn_error=link-required&authn_email=50%25',
    )

    expect(takeCallbackError()).toEqual({
      code: 'authn.link_required',
      email: '50%',
    })
  })
})
