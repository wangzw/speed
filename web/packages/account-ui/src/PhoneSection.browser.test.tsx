/**
 * PhoneSection in a real browser: a write in flight keeps the control that
 * started it focused.
 *
 * jsdom keeps a focused button focused after it turns natively disabled, so
 * the focus loss a native `disabled` causes is invisible there; only the
 * browser tier sees it. The same holds for the number and code fields: a
 * write in flight locks them read-only, so they stay focusable, where a
 * native `disabled` would drop the focus of whoever was typing in them.
 *
 * The verify case also pins the focus landing: a successful verify removes
 * the step's controls, and focus goes to the control group rather than the
 * document body.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { PhoneCountryDefaultProvider } from '@speed/ui-kit'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
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
