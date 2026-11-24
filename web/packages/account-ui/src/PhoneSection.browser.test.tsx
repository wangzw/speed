/**
 * PhoneSection in a real browser: the in-flight focus a native disable would
 * lose, the verify's focus landing, and the captcha-held send's focus.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { PhoneCountryDefaultProvider } from '@speed/ui-kit'
import { captchaChallenge } from '@speed/test-utils/session-harness'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import uiKitZhCN from '../../ui-kit/src/locales/zh-CN.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { PhoneSection } from './PhoneSection.js'

const PHONE_PATH = '/api/v1/auth/phone'
const VERIFY_PATH = '/api/v1/auth/phone/verify'
const US_NATIONAL = '4155552671'
const CAPTCHA_PATH = '/api/v1/auth/human/captcha/challenge'
const CAPTCHA_ANSWER = uiKitZhCN.humanVerification.captcha.answerLabel

/** The phone surface gated by a self-hosted captcha. */
function captchaGate() {
  return {
    phone: { required: true, provider: 'captcha' },
    other: { required: false, provider: 'off' },
  }
}

/** A promise the case settles by hand, so one request is held in flight
 * across frames. */
function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
} {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

describe('PhoneSection writes in Chromium', () => {
  it('keeps focus on the send control and the number field usable while the send is in flight', async () => {
    await page.viewport(900, 900)
    const held = deferred<Response>()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return held.promise
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onCancel = vi.fn()
    renderWithProviders(
      <PhoneCountryDefaultProvider country="US">
        <PhoneSection
          session={rig.session}
          humanGate={{
            phone: { required: false, provider: 'off' },
            other: { required: false, provider: 'off' },
          }}
          onCancel={onCancel}
          onAdded={() => undefined}
        />
      </PhoneCountryDefaultProvider>,
    )
    const user = userEvent.setup()

    const phone = await screen.findByLabelText(zhCN.phone.phone.label, {
      selector: 'input',
    })
    await user.type(phone, US_NATIONAL)
    const send = screen.getByRole('button', { name: zhCN.phone.send })
    const close = screen.getByRole('button', { name: zhCN.phone.close })

    await user.click(send)
    // The control that started the write keeps focus through it. A native
    // `disabled` would have moved it to the document body already.
    await waitFor(() => expect(send).toHaveFocus())
    expect(send).toHaveAttribute('aria-busy', 'true')
    // Its sibling is held without a reason: the scope's write in flight is
    // what makes it unavailable.
    expect(close).toHaveAttribute('aria-disabled', 'true')
    expect(close).not.toHaveAttribute('aria-busy')
    expect(close).not.toHaveAccessibleDescription()

    // The number input is locked but stays focusable and unchangeable.
    expect(phone).toHaveAttribute('readonly')
    const before = (phone as HTMLInputElement).value
    phone.focus()
    expect(phone).toHaveFocus()
    await user.keyboard('9')
    expect((phone as HTMLInputElement).value).toBe(before)

    await act(async () => {
      held.resolve(jsonResponse(200, { expires_in: 300, resend_after: 60 }))
    })
    await screen.findByLabelText(zhCN.phone.code.label)
  })

  it('keeps focus on the verify control, the code field usable, and lands focus on the group afterwards', async () => {
    await page.viewport(900, 900)
    const held = deferred<Response>()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return held.promise
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    // The host owns the form's visibility, so a landed verify keeps its
    // confirmation and does not fall back to the number step: the verify
    // control itself leaves the document.
    renderWithProviders(
      <PhoneCountryDefaultProvider country="US">
        <PhoneSection
          session={rig.session}
          humanGate={{
            phone: { required: false, provider: 'off' },
            other: { required: false, provider: 'off' },
          }}
          onCancel={() => undefined}
          onAdded={() => undefined}
        />
      </PhoneCountryDefaultProvider>,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    const verify = screen.getByRole('button', { name: zhCN.phone.verify })
    const close = screen.getByRole('button', { name: zhCN.phone.close })

    await user.click(verify)
    // The control that started the write keeps focus through it.
    await waitFor(() => expect(verify).toHaveFocus())
    expect(verify).toHaveAttribute('aria-busy', 'true')
    expect(close).toHaveAttribute('aria-disabled', 'true')
    expect(close).not.toHaveAccessibleDescription()

    const code = screen.getByLabelText(zhCN.phone.code.label)
    expect(code).toHaveAttribute('readonly')
    code.focus()
    expect(code).toHaveFocus()

    await act(async () => {
      held.resolve(new Response(null, { status: 204 }))
    })

    // The verify control leaves the document, so focus lands on the control
    // group instead of the body.
    await waitFor(() => expect(verify).not.toBeInTheDocument())
    expect(document.activeElement).not.toBe(document.body)
    const group = screen.getByRole('form', { name: zhCN.phone.title })
    await waitFor(() => expect(group).toHaveFocus())
  })
})

describe('PhoneSection while a captcha proof is outstanding', () => {
  /** A responder whose captcha route issues one challenge and whose send
   * is held until the case releases it. */
  function gatedRig(send: Promise<Response>) {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === CAPTCHA_PATH) {
        return jsonResponse(200, captchaChallenge())
      }
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return send
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    return rig
  }

  it('keeps focus on the send control and keeps it mounted, standalone host', async () => {
    await page.viewport(900, 900)
    const rig = gatedRig(
      Promise.resolve(
        jsonResponse(200, { expires_in: 300, resend_after: 60 }),
      ),
    )
    renderWithProviders(
      <PhoneCountryDefaultProvider country="US">
        <PhoneSection session={rig.session} humanGate={captchaGate()} />
      </PhoneCountryDefaultProvider>,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    const send = screen.getByRole('button', { name: zhCN.phone.send })
    // The keyboard is the path this defect belongs to: the pointer would
    // have put focus on the button anyway.
    send.focus()
    await user.keyboard('{Enter}')

    // The block collects the answer; the control that asked stays mounted,
    // focused and reports the wait. A native `disabled` -- or removing the
    // control for the block -- would have moved focus to the document body.
    await screen.findByLabelText(CAPTCHA_ANSWER)
    await waitFor(() => expect(send).toHaveFocus())
    expect(send).toHaveAttribute('aria-busy', 'true')
  })

  it('keeps focus on the send control and its close sibling usable, host-owned form', async () => {
    await page.viewport(900, 900)
    const rig = gatedRig(
      Promise.resolve(
        jsonResponse(200, { expires_in: 300, resend_after: 60 }),
      ),
    )
    const onCancel = vi.fn()
    renderWithProviders(
      <PhoneCountryDefaultProvider country="US">
        <PhoneSection
          session={rig.session}
          humanGate={captchaGate()}
          onCancel={onCancel}
          onAdded={() => undefined}
        />
      </PhoneCountryDefaultProvider>,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    const send = screen.getByRole('button', { name: zhCN.phone.send })
    const close = screen.getByRole('button', { name: zhCN.phone.close })
    send.focus()
    await user.keyboard('{Enter}')

    await screen.findByLabelText(CAPTCHA_ANSWER)
    await waitFor(() => expect(send).toHaveFocus())
    expect(send).toHaveAttribute('aria-busy', 'true')
    // Abandoning the wait is not a write in flight: the exit stays usable.
    expect(close).not.toHaveAttribute('aria-disabled', 'true')
    expect(close).not.toHaveAttribute('aria-busy')
  })

  it('returns focus to the send control when the answered proof lets the send leave', async () => {
    await page.viewport(900, 900)
    const held = deferred<Response>()
    const rig = gatedRig(held.promise)
    renderWithProviders(
      <PhoneCountryDefaultProvider country="US">
        <PhoneSection session={rig.session} humanGate={captchaGate()} />
      </PhoneCountryDefaultProvider>,
    )
    const user = userEvent.setup()

    const phone = await screen.findByLabelText(zhCN.phone.phone.label, {
      selector: 'input',
    })
    await user.type(phone, US_NATIONAL)
    const send = screen.getByRole('button', { name: zhCN.phone.send })
    send.focus()
    await user.keyboard('{Enter}')

    const answer = await screen.findByLabelText(CAPTCHA_ANSWER)
    answer.focus()
    await user.type(answer, '12345')
    await user.keyboard('{Enter}')

    // The proof settled: the block disappears while the send it asked for
    // is still in the air. Focus was in the block, so it comes back to the
    // control that started the attempt rather than falling to the body.
    await waitFor(() =>
      expect(screen.queryByLabelText(CAPTCHA_ANSWER)).toBeNull(),
    )
    expect(send).toHaveFocus()

    await act(async () => {
      held.resolve(jsonResponse(200, { expires_in: 300, resend_after: 60 }))
    })
    // The code step replaces the number step and takes focus with it.
    const code = await screen.findByLabelText(zhCN.phone.code.label)
    await waitFor(() => expect(code).toHaveFocus())
  })
})
