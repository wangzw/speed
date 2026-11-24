import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import {
  ChannelFeedbackRegion,
  useChannelFeedback,
} from './write-feedback.js'

function Probe() {
  const feedback = useChannelFeedback()
  return (
    <div>
      <button type="button" onClick={() => feedback.fail('authn.login_failed')}>
        fail
      </button>
      <button
        type="button"
        onClick={() => feedback.failLocal('signInFlow.passkeyCancelled')}
      >
        ceremony
      </button>
      <button type="button" onClick={() => feedback.reset()}>
        reset
      </button>
      <ChannelFeedbackRegion feedback={feedback} />
    </div>
  )
}

describe('useChannelFeedback', () => {
  it('resolves a failure code through the auth-ui namespace', async () => {
    const user = userEvent.setup()
    renderWithProviders(<Probe />)
    await user.click(screen.getByRole('button', { name: 'fail' }))
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.login_failed,
    )
  })

  it('keeps the slot failure and the client failure mutually exclusive', async () => {
    const user = userEvent.setup()
    renderWithProviders(<Probe />)

    await user.click(screen.getByRole('button', { name: 'ceremony' }))
    const ceremony = screen.getByRole('alert')
    expect(ceremony).toHaveTextContent(zhCN.signInFlow.passkeyCancelled)
    expect(ceremony).not.toHaveTextContent(zhCN.errors.authn.login_failed)

    await user.click(screen.getByRole('button', { name: 'fail' }))
    const slot = screen.getByRole('alert')
    expect(slot).toHaveTextContent(zhCN.errors.authn.login_failed)
    expect(slot).not.toHaveTextContent(zhCN.signInFlow.passkeyCancelled)
    expect(screen.getAllByRole('alert')).toHaveLength(1)

    await user.click(screen.getByRole('button', { name: 'reset' }))
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
