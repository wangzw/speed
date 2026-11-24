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
