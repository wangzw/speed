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
 * number fills the scope's region beneath the controls with a message
 * naming the number, while the sent notice stays outside the slot; a
 * refused send and a wrong code each render their code text in that
 * region; the way back to a different number returns to the number step;
 * the send carries no fresh authentication, so a step-up refusal of it
 * renders its code text in that region rather than opening a dialog, while a
 * refused verify opens the dialog and replays the same code.
 */

import { describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { PhoneCountryDefaultProvider, uiKitResources } from '@speed/ui-kit'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { profileBody } from '../test-utils/fixtures.js'
import { renderWithProviders } from '../test-utils/render.js'
import { captureRefreshWarning } from '../test-utils/refresh-warning.js'
import {
  AUTHENTICATED_SESSION,
  EMPTY_FEATURES,
  FEATURES_PATH,
  ProfileQueryObserver,
  SESSION_PATH,
  makeRotationRig,
} from '../test-utils/rotation.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { PhoneSection } from './PhoneSection.js'

const US_NATIONAL = '4155552671'
const US_E164 = '+14155552671'
const UI_KIT_ZH = uiKitResources['zh-CN'] as {
  readonly phoneNumber: { readonly incomplete: string }
}

const PHONE_PATH = '/api/v1/auth/phone'
const VERIFY_PATH = '/api/v1/auth/phone/verify'
const PROFILE_PATH = '/api/v1/auth/me'
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
      screen.getByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
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
    // The "code sent" notice is the step's own state, outside the outcome
    // slot: the slot renders no result for the intermediate answer, and the
    // notice is its own status region (the one that carries the text).
    expect(screen.queryByRole('alert')).toBeNull()
    expect(
      screen
        .getByText(zhCN.phone.sent.replace('{{phone}}', US_E164))
        .closest('[role="status"]'),
    ).not.toBeNull()
    // The send hands focus to the code field.
    expect(screen.getByLabelText(zhCN.phone.code.label)).toHaveFocus()

    await expectNoAxeViolations()

    await user.type(screen.getByLabelText(zhCN.phone.code.label), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    const added = zhCN.phone.added.replace('{{phone}}', US_E164)
    await waitFor(() => expect(screen.getByText(added)).toBeTruthy())
    // The confirmation names the number and sits beneath the control group.
    expect(zhCN.phone.added).toContain(zhCN.phone.title)
    expect(enUS.phone.added).toContain(enUS.phone.title)
    const successRegion = screen
      .getByText(added)
      .closest('[role="status"]') as HTMLElement
    expect(
      screen
        .getByRole('button', { name: zhCN.phone.send })
        .compareDocumentPosition(successRegion) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
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

    await user.type(screen.getByLabelText(zhCN.phone.phone.label, { selector: 'input' }), '7911123456')
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

    const phone = await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' })
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
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await screen.findByLabelText(zhCN.phone.code.label)

    await user.click(screen.getByRole('button', { name: zhCN.phone.cancel }))
    expect(screen.getByLabelText(zhCN.phone.phone.label, { selector: 'input' })).toBeTruthy()
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
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.conflict)
    // The refusal lands in the same place a success does: beneath the
    // control group.
    expect(
      screen
        .getByRole('button', { name: zhCN.phone.send })
        .compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('renders a refused verification beneath the code controls', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return errorResponse(401, 'authn.login_failed')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await screen.findByLabelText(zhCN.phone.code.label)
    await user.type(screen.getByLabelText(zhCN.phone.code.label), '000000')
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.login_failed)
    expect(
      screen
        .getByRole('button', { name: zhCN.phone.verify })
        .compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    // No success confirmation coexists with the failure in the outcome slot.
    expect(
      screen.queryByText(zhCN.phone.added.replace('{{phone}}', US_E164)),
    ).toBeNull()
  })

  it('renders a step-up refusal of the send as an ordinary failure, with no dialog', async () => {
    // Registering a phone number is not a change of what proves the account;
    // the route asks for no fresh authentication. A step-up refusal renders
    // its code text in the section's region instead of opening the dialog,
    // and the refused send is not replayed.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return errorResponse(401, 'authn.insufficient_user_authentication')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(
      zhCN.errors.authn.insufficient_user_authentication,
    )
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(
      rig.calls.filter((call) => call.path === PHONE_PATH),
    ).toHaveLength(1)
  })

  it('arms the step-up dialog on a refused verify and replays the same code', async () => {
    // A number that replaces the one an active SMS factor sends codes to
    // changes how the account signs in, so the server may refuse the proof
    // for a fresh authentication. The verify arms the dialog with the same
    // code, and the won challenge replays it exactly once.
    captureRefreshWarning()
    let elevated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        return new Response(null, { status: 204 })
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
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    // The 401 opens the dialog; winning it replays the same code.
    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    await user.click(screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() =>
      expect(
        screen.getByText(zhCN.phone.added.replace('{{phone}}', US_E164)),
      ).toBeTruthy(),
    )
    const verifies = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === VERIFY_PATH,
    )
    expect(verifies).toHaveLength(2)
    expect(verifies[0]?.body).toEqual({ code: '123456' })
    expect(verifies[1]?.body).toEqual({ code: '123456' })
  })

  it('proves a number without opening an identity-transition window', async () => {
    // Registering and proving a number rotates no credential and revokes no
    // other session, so the surface opens no window and the profile refetch
    // that follows the verify stays authenticated. The phone surface
    // invalidates the profile query but does not read it; the observer
    // stands in for the other surfaces on an account page.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody())
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(
      <>
        <PhoneSection session={rig.session} />
        <ProfileQueryObserver />
      </>,
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
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    await waitFor(() =>
      expect(
        screen.getByText(zhCN.phone.added.replace('{{phone}}', US_E164)),
      ).toBeTruthy(),
    )
    expect(rig.transitionBegins()).toBe(0)
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('opens no identity-transition window for a refused verification', async () => {
    // The wrong-code refusal is an ordinary failure: no window is opened, so
    // a later genuine anonymous read still ends the session with one edge.
    let refused = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return refused
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return refused
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, profileBody())
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        refused = true
        return errorResponse(401, 'authn.login_failed')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<PhoneSection session={rig.session} />)
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
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    await screen.findByText(zhCN.errors.authn.login_failed)
    expect(rig.transitionBegins()).toBe(0)

    // A fresh 401 read now settles the true state.
    await rig.session.restore()
    expect(rig.session.getSnapshot().state).toBe('anonymous')
    expect(rig.endedEdges()).toBe(1)
  })

  it('refuses the send until the required verifier holds a token, then sends it', async () => {
    const fake = installFakeTurnstile()
    try {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'POST' && call.path === PHONE_PATH) {
          return jsonResponse(200, { expires_in: 300, resend_after: 60 })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(
        <PhoneSection
          session={rig.session}
          humanGate={{
            required: true,
            provider: 'turnstile',
            site_key: 'site-1',
          }}
        />,
      )
      const user = userEvent.setup()

      const send = screen.getByRole('button', { name: zhCN.phone.send })
      await user.type(
        screen.getByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
        US_NATIONAL,
      )
      // A proof is required and the widget holds no token yet.
      expect(send).toBeDisabled()
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(send).toBeEnabled())
      await user.click(send)

      await waitFor(() =>
        expect(
          rig.calls.filter((call) => call.path === PHONE_PATH),
        ).toHaveLength(1),
      )
      expect(rig.calls.find((call) => call.path === PHONE_PATH)?.body).toEqual({
        phone: US_E164,
        human_proof: 'token-1',
      })
      await screen.findByLabelText(zhCN.phone.code.label)
    } finally {
      fake.restore()
    }
  })
})
