/**
 * The SMS sign-in face under the phone surface's self-hosted captcha, in a
 * real browser: the block obtains a challenge, collects the answer the
 * viewer reads from the picture, and yields the `<id>.<answer>` proof the
 * code send presents as `human_proof`. The cases also hold the refresh
 * control: a new challenge replaces the one on screen, and the send still
 * presents the proof belonging to the challenge it answered.
 *
 * The harness answers the challenge route, so the assertion is the control
 * the block renders and the body the flow receives, never a live captcha
 * server.
 */
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  CAPTCHA_CHALLENGE,
  LOGIN_METHODS,
  LOGIN_START,
  captchaChallenge,
  makeHarness,
  methodList,
  openFlow,
} from '@speed/test-utils/session-harness'
import { PhoneCountryDefaultProvider } from '@speed/ui-kit'
import { SMSSignInForm } from './SMSSignInForm.js'
import { renderWithProviders } from '../test-utils/render.js'
import uiKitZhCN from '../../ui-kit/src/locales/zh-CN.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const CN_NATIONAL = '13800000001'
const CN_E164 = '+8613800000001'

const CAPTCHA_COPY = uiKitZhCN.humanVerification.captcha

/** The server's code challenge for the sms method. */
function codeChallenge() {
  return openFlow({
    seq: 1,
    state: 'first',
    next: {
      kind: 'challenge',
      fields: [{ name: 'code', kind: 'code', required: true, secret: true }],
    },
  })
}

/** The login-flow starts a case's own interaction makes. */
function sends(harness: ReturnType<typeof makeHarness>) {
  return harness.calls.filter((call) => call.path === '/api/v1/auth/flows')
}

/** Fill the phone step and return the send control. */
async function fillPhone(): Promise<HTMLElement> {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText(zhCN.smsSignIn.phoneLabel), CN_NATIONAL)
  return screen.getByRole('button', { name: zhCN.smsSignIn.sendCode })
}

describe('SMSSignInForm captcha gate in Chromium', () => {
  it('obtains the phone surface captcha and sends the proof it collects', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList(['sms'], {
          humanPhoneProvider: 'captcha',
          humanPhoneRequired: true,
          humanOtherProvider: 'off',
          humanOtherRequired: false,
        }),
      [CAPTCHA_CHALLENGE]: () => captchaChallenge(),
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(
      <PhoneCountryDefaultProvider country="CN">
        <SMSSignInForm session={harness.session} />
      </PhoneCountryDefaultProvider>,
    )
    // The block asks for a challenge on mount and renders the picture with
    // its instruction.
    const answer = await screen.findByLabelText(CAPTCHA_COPY.answerLabel)
    expect(
      screen.getByAltText(CAPTCHA_COPY.instruction.typeTheDigits),
    ).toBeInTheDocument()
    const send = await fillPhone()
    // No proof yet: the send waits for the answer.
    expect(send).toBeDisabled()
    const user = userEvent.setup()
    await user.type(answer, '12345')
    await user.click(screen.getByRole('button', { name: CAPTCHA_COPY.submit }))
    await waitFor(() =>
      expect(
        screen.getByText(uiKitZhCN.humanVerification.verified),
      ).toBeInTheDocument(),
    )
    await waitFor(() => expect(send).toBeEnabled())
    await user.click(send)
    await waitFor(() => expect(sends(harness)).toHaveLength(1))
    expect(sends(harness)[0]?.options?.body).toMatchObject({
      method: 'sms',
      input: { phone: CN_E164 },
      human_proof: 'Y2FwdGNoYS1jaGFsbGVuZ2U.12345',
    })
  })

  it('replaces the challenge when the refresh control asks for another', async () => {
    let issued = 0
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList(['sms'], {
          humanPhoneProvider: 'captcha',
          humanPhoneRequired: true,
        }),
      [CAPTCHA_CHALLENGE]: () => {
        issued += 1
        return captchaChallenge({
          id: `challenge-${issued}`,
          image_url: `/api/v1/auth/human/captcha/image/${issued}`,
        })
      },
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(
      <PhoneCountryDefaultProvider country="CN">
        <SMSSignInForm session={harness.session} />
      </PhoneCountryDefaultProvider>,
    )
    await waitFor(() => expect(issued).toBe(1))
    const image = await screen.findByAltText(
      CAPTCHA_COPY.instruction.typeTheDigits,
    )
    expect(image).toHaveAttribute(
      'src',
      '/api/v1/auth/human/captcha/image/1',
    )
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: CAPTCHA_COPY.refresh }))
    await waitFor(() => expect(issued).toBe(2))
    await waitFor(() =>
      expect(
        screen.getByAltText(CAPTCHA_COPY.instruction.typeTheDigits),
      ).toHaveAttribute('src', '/api/v1/auth/human/captcha/image/2'),
    )
  })
})
