/**
 * The channel feedback scope: one slot per channel, its failure resolved
 * through the auth-ui namespace, and the client-side (passkey ceremony)
 * source kept mutually exclusive with the slot.
 *
 * The cases drive the real scope hook and region: a failure code settles
 * the slot and renders the namespace's text, a bundle-key failure raises
 * the client source alone, and either one clears the other, so a channel
 * never shows two whole-attempt failure banners.
 */

import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import {
  ChannelFeedbackRegion,
  useChannelFeedback,
} from './write-feedback.js'

/** A probe exposing the scope's three settlement paths as buttons. */
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

    // The ceremony failure is bundle copy, not an API code: it renders its
    // own text and takes the one failure position from the slot.
    await user.click(screen.getByRole('button', { name: 'ceremony' }))
    const ceremony = screen.getByRole('alert')
    expect(ceremony).toHaveTextContent(zhCN.signInFlow.passkeyCancelled)
    expect(ceremony).not.toHaveTextContent(zhCN.errors.authn.login_failed)

    // A slot failure takes it back the other way round.
    await user.click(screen.getByRole('button', { name: 'fail' }))
    const slot = screen.getByRole('alert')
    expect(slot).toHaveTextContent(zhCN.errors.authn.login_failed)
    expect(slot).not.toHaveTextContent(zhCN.signInFlow.passkeyCancelled)
    expect(screen.getAllByRole('alert')).toHaveLength(1)

    // A new attempt clears every failure source of the scope.
    await user.click(screen.getByRole('button', { name: 'reset' }))
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
