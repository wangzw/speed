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

    expect(takeCallbackError()).toBe('authn.link_required')
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

    expect(takeCallbackError()).toBe('authn.flow_invalid')
  })

  it('answers nothing for an empty code', () => {
    window.history.replaceState({}, '', '/#authn_error=')

    expect(takeCallbackError()).toBeUndefined()
  })
})
