/**
 * The one-shot read of a refused callback's landing. The read clears the URL
 * fragment, so the effect that performs it is not idempotent; StrictMode's
 * development double mount must not reset the message the viewer came to see.
 */

import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { StrictMode } from 'react'
import { useCallbackLanding } from './use-callback-landing.js'

function Probe() {
  const landing = useCallbackLanding()
  return (
    <div data-testid="landing">
      {landing === undefined
        ? 'none'
        : `${landing.code}|${landing.email ?? ''}`}
    </div>
  )
}

describe('useCallbackLanding', () => {
  it('keeps the landing across the second StrictMode mount', () => {
    // StrictMode mounts the effect twice in development. The first run clears
    // the fragment; an unguarded second run would read nothing and reset the
    // landing to empty.
    window.history.replaceState(
      {},
      '',
      '/#authn_error=link-required&authn_email=a***@home.org',
    )

    render(
      <StrictMode>
        <Probe />
      </StrictMode>,
    )

    expect(screen.getByTestId('landing')).toHaveTextContent(
      'authn.link_required|a***@home.org',
    )
    expect(window.location.hash).toBe('')
  })

  it('answers no landing when the URL carries no fragment', () => {
    window.history.replaceState({}, '', '/')

    render(
      <StrictMode>
        <Probe />
      </StrictMode>,
    )

    expect(screen.getByTestId('landing')).toHaveTextContent('none')
  })
})
