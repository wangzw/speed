/**
 * WriteFeedbackRegion checks the account binding of the shared region.
 */

import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useWriteFeedback } from '@speed/ui-kit'
import { WriteFeedbackRegion } from './write-feedback.js'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const SUCCESS = 'probe-success-message'

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
    expect(alert).not.toHaveTextContent('client.unknown')
  })

  it('renders success and failure in the one region, never side by side', async () => {
    const user = userEvent.setup()
    renderWithProviders(<TwoWayProbe />)

    await user.click(screen.getByRole('button', { name: 'succeed' }))
    expect(await screen.findByText(SUCCESS)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()

    await user.click(screen.getByRole('button', { name: 'fail' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent('')
    expect(screen.queryByText(SUCCESS)).toBeNull()
    expect(status.parentElement).toBe(alert.parentElement)
  })
})
