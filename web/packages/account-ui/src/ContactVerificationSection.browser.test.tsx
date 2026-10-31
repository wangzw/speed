/**
 * ContactVerificationSection's action row in a real browser: the send,
 * change-address and verify controls share one row -- one top edge, the
 * primary verify at the row's right end -- and the verify control stays
 * disabled until a code is entered. jsdom lays nothing out, so only the
 * browser tier can see the boxes' geometry.
 */
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { profileBody } from '../test-utils/fixtures.js'
import {
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { ContactVerificationSection } from './ContactVerificationSection.js'

const PROFILE_PATH = '/api/v1/auth/me'

describe('ContactVerificationSection verification actions in Chromium', () => {
  it('keeps the three actions on one row, with verify rightmost and disabled while empty', async () => {
    // A desktop width keeps the row from wrapping, so the three boxes are
    // measured on the one line the rule describes.
    await page.viewport(900, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'pending', email: 'ada@x.test', since: '' },
          }),
        )
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<ContactVerificationSection session={rig.session} />)

    const send = await screen.findByRole('button', {
      name: zhCN.verification.send,
    })
    const change = screen.getByRole('button', {
      name: zhCN.verification.change,
    })
    const verify = screen.getByRole('button', {
      name: zhCN.verification.verify,
    })

    // The verify control is the primary action: no code, nothing to submit.
    expect(verify).toBeDisabled()

    const sendBox = send.getBoundingClientRect()
    const changeBox = change.getBoundingClientRect()
    const verifyBox = verify.getBoundingClientRect()

    // One row: all three boxes share a top edge.
    expect(Math.abs(sendBox.top - changeBox.top)).toBeLessThanOrEqual(1)
    expect(Math.abs(verifyBox.top - sendBox.top)).toBeLessThanOrEqual(1)

    // Same baseline: equal heights put the text on one line.
    expect(Math.abs(sendBox.bottom - changeBox.bottom)).toBeLessThanOrEqual(1)
    expect(Math.abs(verifyBox.bottom - sendBox.bottom)).toBeLessThanOrEqual(1)

    // The primary action closes the row: it starts to the right of both
    // secondary actions.
    expect(verifyBox.left).toBeGreaterThan(changeBox.right)
    expect(verifyBox.left).toBeGreaterThan(sendBox.right)
  })
})
