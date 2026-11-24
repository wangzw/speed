/**
 * ContactVerificationSection in a real browser: the action row's geometry
 * and the in-flight focus, neither of which jsdom can see.
 */
import { describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { captchaChallenge } from '@speed/test-utils/session-harness'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import uiKitZhCN from '../../ui-kit/src/locales/zh-CN.json' with { type: 'json' }
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
const VERIFY_PATH = '/api/v1/auth/identifiers/verify'
const ADD_PATH = '/api/v1/auth/identifiers'
const CAPTCHA_PATH = '/api/v1/auth/human/captcha/challenge'
const CAPTCHA_ANSWER = uiKitZhCN.humanVerification.captcha.answerLabel

/** One answer held open, so a case can inspect the in-flight control. */
function deferredResponse(): {
  readonly response: Promise<Response>
  readonly release: (response: Response) => void
} {
  let release: (response: Response) => void = () => {}
  const response = new Promise<Response>((resolve) => {
    release = resolve
  })
  return { response, release }
}

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
            verification: { state: 'pending', email: 'ada@x.test', since: null },
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
    expect(verify).toHaveAttribute('aria-disabled', 'true')

    const sendBox = send.getBoundingClientRect()
    const changeBox = change.getBoundingClientRect()
    const verifyBox = verify.getBoundingClientRect()

    // One row: all three boxes share a top edge.
    expect(Math.abs(sendBox.top - changeBox.top)).toBeLessThanOrEqual(1)
    expect(Math.abs(verifyBox.top - sendBox.top)).toBeLessThanOrEqual(1)

    // Same baseline: equal heights put the text on one line.
    expect(Math.abs(sendBox.bottom - changeBox.bottom)).toBeLessThanOrEqual(1)
    expect(Math.abs(verifyBox.bottom - sendBox.bottom)).toBeLessThanOrEqual(1)

    // One adjacent group, one spacing token apart, not two ends: the
    // secondary pair leads and the primary closes the row.
    expect(changeBox.left - sendBox.right).toBeCloseTo(8, 0)
    expect(verifyBox.left - changeBox.right).toBeCloseTo(8, 0)
    const row = verify.closest<HTMLElement>('[data-action-row]')
    expect(row).not.toBeNull()
    expect(send.closest('[data-action-row]')).toBe(row)
    expect(change.closest('[data-action-row]')).toBe(row)
    const rowBox = (row as HTMLElement).getBoundingClientRect()
    const contentRight =
      rowBox.right -
      Number.parseFloat(getComputedStyle(row as HTMLElement).paddingRight)
    expect(Math.abs(verifyBox.right - contentRight)).toBeLessThanOrEqual(1)
  })

  it('keeps the verify control focused while its write is in flight, then lands on the verified section', async () => {
    await page.viewport(900, 900)
    let verified = false
    const held = deferredResponse()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: {
              state: verified ? 'verified' : 'pending',
              email: 'ada@x.test',
              since: null,
            },
          }),
        )
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return held.response
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<ContactVerificationSection session={rig.session} />)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    const verify = screen.getByRole('button', {
      name: zhCN.verification.verify,
    })
    const boxBefore = verify.getBoundingClientRect()
    await user.click(verify)
    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) => call.method === 'POST' && call.path === VERIFY_PATH,
        ),
      ).toBe(true),
    )

    // The control that started the write keeps the focus it had when it was
    // activated, reports the busy state, and keeps its box.
    expect(verify).toHaveFocus()
    expect(verify).toHaveAttribute('aria-busy', 'true')
    const boxBusy = verify.getBoundingClientRect()
    expect(Math.abs(boxBusy.width - boxBefore.width)).toBeLessThanOrEqual(1)

    // The landed verification replaces the code step: the control leaves
    // the document, so focus lands on the section that stays.
    verified = true
    await act(async () => {
      held.release(jsonResponse(200, { state: 'verified' }))
    })
    const landing = await screen.findByRole('group', {
      name: zhCN.verification.verifiedTitle,
    })
    await waitFor(() => expect(landing).toHaveFocus())
  })

  it('returns focus to the add-form send when the answered proof lets the add leave', async () => {
    await page.viewport(900, 900)
    const held = deferredResponse()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        // No address yet: the add form is the surface that carries a send.
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'pending', email: '', since: null },
          }),
        )
      }
      if (call.method === 'POST' && call.path === CAPTCHA_PATH) {
        return jsonResponse(200, captchaChallenge())
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return held.response
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <ContactVerificationSection
        session={rig.session}
        humanGate={{
          phone: { required: false, provider: 'off' },
          other: { required: true, provider: 'captcha' },
        }}
      />,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.verification.addLabel),
      'ada@x.test',
    )
    const add = screen.getByRole('button', {
      name: zhCN.verification.addSubmit,
    })
    add.focus()
    await user.keyboard('{Enter}')

    const answer = await screen.findByLabelText(CAPTCHA_ANSWER)
    answer.focus()
    await user.type(answer, '12345')
    await user.keyboard('{Enter}')

    // The proof settled: the block disappears while the add it asked for is
    // still in the air. Focus was in the block, so it comes back to the
    // control that started the send rather than falling to the body.
    await waitFor(() =>
      expect(screen.queryByLabelText(CAPTCHA_ANSWER)).toBeNull(),
    )
    expect(add).toHaveFocus()

    await act(async () => {
      held.release(
        jsonResponse(200, { state: 'pending', resend_after: 0 }),
      )
    })
    await screen.findByLabelText(zhCN.verification.codeLabel)
  })
})
