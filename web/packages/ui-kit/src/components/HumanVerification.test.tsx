/**
 * HumanVerification: the presentational block of the human-verification
 * gate.
 *
 * Pins the three mechanisms -- off renders nothing, Turnstile renders the
 * provider widget and reports its token (and resets once the host
 * consumes it), proof of work shows the work and offers a retry -- plus
 * the accessibility of the states a viewer waits on. Text expectations
 * read the bundle values, never inline language.
 */

import { useState } from 'react'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { HumanVerification } from './HumanVerification.js'

afterEach(() => {
  delete (window as unknown as Record<string, unknown>).turnstile
})

describe('HumanVerification', () => {
  it('renders nothing when the gate is absent, off, or requires nothing', () => {
    const { container, rerender } = renderWithProviders(
      <HumanVerification onToken={() => {}} />,
    )
    expect(container).toBeEmptyDOMElement()
    rerender(
      <HumanVerification
        gate={{ required: false, provider: 'off' }}
        onToken={() => {}}
      />,
    )
    expect(container).toBeEmptyDOMElement()
    rerender(
      <HumanVerification
        gate={{ required: false, provider: 'turnstile', site_key: 'site-1' }}
        onToken={() => {}}
      />,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('shows the proof-of-work work and its outcome', () => {
    const { rerender } = renderWithProviders(
      <HumanVerification
        gate={{ required: true, provider: 'pow' }}
        status="working"
        onToken={() => {}}
      />,
    )
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.humanVerification.working,
    )
    rerender(
      <HumanVerification
        gate={{ required: true, provider: 'pow' }}
        status="ready"
        proof="challenge.nonce"
        onToken={() => {}}
      />,
    )
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.humanVerification.verified,
    )
  })

  it('offers a retry after a failed proof of work', async () => {
    const onRetry = vi.fn()
    renderWithProviders(
      <HumanVerification
        gate={{ required: true, provider: 'pow' }}
        status="failed"
        onToken={() => {}}
        onRetry={onRetry}
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.humanVerification.failed,
    )
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.humanVerification.retry }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('renders the Turnstile widget, yields its token, and resets it once consumed', async () => {
    const fake = installFakeTurnstile()
    const seen: (string | null)[] = []

    function Host() {
      const [proof, setProof] = useState<string | null>(null)
      return (
        <div>
          <HumanVerification
            gate={{ required: true, provider: 'turnstile', site_key: 'site-1' }}
            proof={proof}
            onToken={(token) => {
              seen.push(token)
              setProof(token)
            }}
          />
          <button type="button" onClick={() => setProof(null)}>
            consume
          </button>
        </div>
      )
    }

    const { unmount } = renderWithProviders(<Host />)
    await waitFor(() => expect(fake.widgets).toHaveLength(1))
    expect(fake.widgets[0]?.siteKey).toBe('site-1')

    act(() => {
      fake.token('token-1')
    })
    expect(seen).toEqual(['token-1'])

    await userEvent.setup().click(screen.getByRole('button', { name: 'consume' }))
    await waitFor(() => expect(fake.resets).toHaveLength(1))
    expect(fake.resets[0]).toBe(fake.widgets[0]!.id)

    unmount()
    expect(fake.removed).toHaveLength(1)
    fake.restore()
  })

  it('passes axe for the proof-of-work states', async () => {
    renderWithProviders(
      <div>
        <h1>Account</h1>
        <HumanVerification
          gate={{ required: true, provider: 'pow' }}
          status="ready"
          onToken={() => {}}
        />
      </div>,
    )
    await expectNoAxeViolations()
  })
})
