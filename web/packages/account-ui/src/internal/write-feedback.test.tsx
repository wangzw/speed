/**
 * WriteFeedbackRegion: the account surface's binding of the shared region.
 * A failure code resolves through the account-ui namespace's whitelist;
 * anything else collapses to the shared unknown fallback, never a raw key.
 */

import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useWriteFeedback } from '@speed/ui-kit'
import { WriteFeedbackRegion } from './write-feedback.js'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

/** The success message the scripted attempt settles; the caller owns this copy. */
const SUCCESS = 'probe-success-message'

/** A probe that settles the scope's slot with a scripted attempt. */
function Probe({ attempt }: { readonly attempt: () => Promise<string | null> }) {
  const feedback = useWriteFeedback()
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          void feedback.run(attempt)
        }}
      >
        write
      </button>
      <WriteFeedbackRegion outcome={feedback.outcome} />
    </div>
  )
}

/** A probe whose two buttons settle the same slot, one per outcome. */
function TwoWayProbe() {
  const feedback = useWriteFeedback()
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          void feedback.run(async () => SUCCESS)
        }}
      >
        succeed
      </button>
      <button
        type="button"
        onClick={() => {
          void feedback.run(async () => {
            throw { code: 'authn.bad_request' }
          })
        }}
      >
        fail
      </button>
      <WriteFeedbackRegion outcome={feedback.outcome} />
    </div>
  )
}

describe('WriteFeedbackRegion', () => {
  it('resolves a whitelisted failure code through the account namespace', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <Probe attempt={() => Promise.reject({ code: 'authn.bad_request' })} />,
    )
    await user.click(screen.getByRole('button', { name: 'write' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.bad_request,
    )
  })

  it('renders the unknown fallback for a plain throw, never a raw key', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <Probe attempt={() => Promise.reject(new Error('boom'))} />,
    )
    await user.click(screen.getByRole('button', { name: 'write' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.unknown)
    // `errorCodeOf` collapses the throw to client.unknown; the resolver
    // carries no leaf for it, so the fallback stands in -- the raw code
    // must never reach the banner.
    expect(alert).not.toHaveTextContent('client.unknown')
  })

  it('renders success and failure in the one region, never side by side', async () => {
    const user = userEvent.setup()
    renderWithProviders(<TwoWayProbe />)

    // A settled success fills the standing polite live region; no alert
    // is mounted for the same slot.
    await user.click(screen.getByRole('button', { name: 'succeed' }))
    expect(await screen.findByText(SUCCESS)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()

    // A settled failure replaces it: the success region empties and the
    // alert mounts as its sibling in the same container, so the two never
    // stand beside each other.
    await user.click(screen.getByRole('button', { name: 'fail' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent('')
    expect(screen.queryByText(SUCCESS)).toBeNull()
    expect(status.parentElement).toBe(alert.parentElement)
  })
})
