/**
 * The one-shot read of a refused callback's landing. The landing is read on
 * the first render without changing the URL, so StrictMode's development
 * double render sees the same message; the fragment is cleared after mount.
 */

import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode } from 'react'
import { useCallbackLanding } from './use-callback-landing.js'

function Probe() {
  const { landing, dismiss } = useCallbackLanding()
  return (
    <div>
      <div data-testid="landing">
        {landing === undefined
          ? 'none'
          : `${landing.code}|${landing.email ?? ''}`}
      </div>
      <button type="button" onClick={dismiss}>
        dismiss
      </button>
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

  it('answers no landing once the surface dismisses it', async () => {
    // The sign-in screen clears the refusal when the viewer starts something
    // else; the host drops the landing then, so a re-render does not hand
    // the same message back.
    window.history.replaceState(
      {},
      '',
      '/#authn_error=link-required&authn_email=a***@home.org',
    )
    const user = userEvent.setup()

    render(
      <StrictMode>
        <Probe />
      </StrictMode>,
    )

    expect(screen.getByTestId('landing')).toHaveTextContent(
      'authn.link_required|a***@home.org',
    )
    await user.click(screen.getByRole('button', { name: 'dismiss' }))
    expect(screen.getByTestId('landing')).toHaveTextContent('none')
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
