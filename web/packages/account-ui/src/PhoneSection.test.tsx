/**
 * PhoneSection behaviour: the two-step phone surface over POST /api/v1/auth/phone
 * (send a code) and POST /api/v1/auth/phone/verify (submit it).
 *
 * What this suite pins: the shared country/local control assembles the
 * number into E.164 and the send POSTs it under `phone`, advancing to the
 * code step whose sent notice names the assembled number; the host's
 * phone-country context sets the starting country, replacing the
 * browser-locale answer; an empty number
 * disables the send action; a number too short for its country is refused
 * locally with the calling code named and sends nothing; the verify POSTs
 * the code alone (the number is the server's own record); a verified
 * number fills the success live region; a refused send and a wrong code
 * each render their code text; the way back to a different number returns
 * to the number step; and each step refused with 401
 * authn.insufficient_user_authentication opens the step-up dialog, a won
 * step-up re-running that step once.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { PhoneCountryDefaultProvider, uiKitResources } from '@speed/ui-kit'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { captureRefreshWarning } from '../test-utils/refresh-warning.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { PhoneSection } from './PhoneSection.js'

const US_NATIONAL = '4155552671'
const US_E164 = '+14155552671'
const UI_KIT_ZH = uiKitResources['zh-CN'] as {
  readonly phoneNumber: { readonly incomplete: string }
}

const PHONE_PATH = '/api/v1/auth/phone'
const VERIFY_PATH = '/api/v1/auth/phone/verify'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

/** Mounts the section; `wrap` lets a case place its own context above it
 * (the host's preferred starting country). */
function mount(
  respond: RealResponder,
  wrap: (ui: ReactElement) => ReactElement = (ui) => ui,
) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(wrap(<PhoneSection session={rig.session} />))
  return { rig, ...rendered }
}

describe('PhoneSection', () => {
  it('sends a code, advances to the code step and verifies it, and passes axe', async () => {
    let verified = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        verified = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    expect(
      await screen.findByRole('heading', { name: zhCN.phone.title }),
    ).toBeTruthy()

    await user.type(
      screen.getByLabelText(zhCN.phone.phone.label),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))

    // The sent notice names the number and the code field appears.
    expect(
      await screen.findByText(
        zhCN.phone.sent.replace('{{phone}}', US_E164),
      ),
    ).toBeTruthy()
    expect(screen.getByLabelText(zhCN.phone.code.label)).toBeTruthy()
    expect(rig.calls.find((call) => call.path === PHONE_PATH)?.body).toEqual({
      phone: US_E164,
    })

    await expectNoAxeViolations()

    await user.type(screen.getByLabelText(zhCN.phone.code.label), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(zhCN.phone.added),
    )
    expect(verified).toBe(true)
    // The verify carries the code alone: the number is the server's record.
    expect(rig.calls.find((call) => call.path === VERIFY_PATH)?.body).toEqual({
      code: '123456',
    })
  })

  it('starts on the host\'s preferred country and assembles the number under it', async () => {
    // The host's phone-country context replaces the browser-locale answer:
    // the selector starts on GB and the E.164 string carries +44, not the
    // test environment's own region.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond, (ui) => (
      <PhoneCountryDefaultProvider country="GB">{ui}</PhoneCountryDefaultProvider>
    ))
    const user = userEvent.setup()

    const country = await screen.findByLabelText(zhCN.phone.country.label)
    expect((country as HTMLInputElement).value).toContain('+44')

    await user.type(screen.getByLabelText(zhCN.phone.phone.label), '7911123456')
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))

    await waitFor(() =>
      expect(rig.calls.find((call) => call.path === PHONE_PATH)?.body).toEqual({
        phone: '+447911123456',
      }),
    )
  })

  it('disables sending until a number is entered', async () => {
    const respond: RealResponder = () => {
      throw new Error('no request expected')
    }
    mount(respond)

    const send = await screen.findByRole('button', { name: zhCN.phone.send })
    expect(send).toBeDisabled()
  })

  it('refuses a too-short number locally, naming the calling code, and sends nothing', async () => {
    const respond: RealResponder = () => {
      throw new Error('no request expected')
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const phone = await screen.findByLabelText(zhCN.phone.phone.label)
    await user.type(phone, '415555')
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))

    expect(
      await screen.findByText(
        UI_KIT_ZH.phoneNumber.incomplete.replace('{{code}}', '1'),
      ),
    ).toBeTruthy()
    expect(phone).toHaveAttribute('aria-invalid', 'true')
    expect(rig.calls.filter((call) => call.path === PHONE_PATH)).toHaveLength(0)
  })

  it('returns to the number step on cancel', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await screen.findByLabelText(zhCN.phone.code.label)

    await user.click(screen.getByRole('button', { name: zhCN.phone.cancel }))
    expect(screen.getByLabelText(zhCN.phone.phone.label)).toBeTruthy()
    expect(screen.queryByLabelText(zhCN.phone.code.label)).toBeNull()
  })

  it('renders a refused send and a wrong code as their code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return errorResponse(409, 'authn.conflict')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.conflict)
  })

  it('gates a step_up_required send behind the dialog and re-runs it once', async () => {
    const warn = captureRefreshWarning()
    let elevated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, {
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'App',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number }
        if (body.seq === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'second',
            next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
          })
        }
        elevated = true
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))

    await screen.findByRole('button', { name: 'App' })
    await user.click(screen.getByRole('button', { name: 'App' }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    expect(
      await screen.findByText(
        zhCN.phone.sent.replace('{{phone}}', US_E164),
      ),
    ).toBeTruthy()
    expect(
      rig.calls.filter((call) => call.path === PHONE_PATH),
    ).toHaveLength(2)
    // The step-up shortfall's 401 is the expected refresh-failure diagnostic.
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({
        status: 401,
        code: 'authn.insufficient_user_authentication',
        trace_id: 'trace-1',
      }),
    )
    // One shortfall, one refresh failure: a duplicated answer would warn
    // again.
    expect(warn).toHaveBeenCalledTimes(1)
  })
})
