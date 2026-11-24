import { describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page, userEvent as browserUserEvent } from 'vitest/browser'
import {
  CAPTCHA_CHALLENGE,
  LOGIN_ANSWER,
  LOGIN_METHODS,
  LOGIN_START,
  apiError,
  captchaChallenge,
  makeHarness,
  methodList,
  openFlow,
} from '@speed/test-utils/session-harness'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import { PhoneCountryDefaultProvider } from '@speed/ui-kit'
import { SMSSignInForm, SMS_NOTICE_ID } from './SMSSignInForm.js'
import { renderWithProviders } from '../test-utils/render.js'
import uiKitZhCN from '../../ui-kit/src/locales/zh-CN.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const CN_NATIONAL = '13800000001'
const CN_E164 = '+8613800000001'

const CAPTCHA_COPY = uiKitZhCN.humanVerification.captcha

function svg(width: number, height: number): string {
  return `data:image/svg+xml,${encodeURIComponent(
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}"><rect width="100%" height="100%" fill="#246"/></svg>`,
  )}`
}

function sliderChallenge() {
  return captchaChallenge({
    form: 'slider',
    background_url: svg(320, 160),
    piece_url: svg(48, 48),
  })
}

function codeChallenge(resendAfter?: number) {
  return openFlow({
    seq: 1,
    state: 'first',
    next: {
      kind: 'challenge',
      fields: [{ name: 'code', kind: 'code', required: true, secret: true }],
      ...(resendAfter === undefined ? {} : { resend_after: resendAfter }),
    },
  })
}

const NO_HUMAN_GATE = {
  phone: { required: false, provider: 'off' },
  other: { required: false, provider: 'off' },
} as const

function sends(harness: ReturnType<typeof makeHarness>) {
  return harness.calls.filter((call) => call.path === '/api/v1/auth/flows')
}

function captchaRequests(harness: ReturnType<typeof makeHarness>) {
  return harness.calls.filter(
    (call) => call.path === '/api/v1/auth/human/captcha/challenge',
  )
}

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
    const send = await fillPhone()
    await userEvent.setup().click(send)
    const answer = await screen.findByLabelText(CAPTCHA_COPY.answerLabel)
    expect(
      screen.getByAltText(CAPTCHA_COPY.instruction.typeTheDigits),
    ).toBeInTheDocument()
    const user = userEvent.setup()
    await user.type(answer, '12345')
    await user.click(screen.getByRole('button', { name: CAPTCHA_COPY.submit }))
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
    const send = await fillPhone()
    await userEvent.setup().click(send)
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

  it('renders the slider the server chose, solves it by keyboard and sends', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList(['sms'], {
          humanPhoneProvider: 'captcha',
          humanPhoneRequired: true,
          humanOtherProvider: 'off',
          humanOtherRequired: false,
        }),
      [CAPTCHA_CHALLENGE]: () => sliderChallenge(),
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(
      <PhoneCountryDefaultProvider country="CN">
        <SMSSignInForm session={harness.session} />
      </PhoneCountryDefaultProvider>,
    )
    const send = await fillPhone()
    await userEvent.setup().click(send)
    await waitFor(() => expect(captchaRequests(harness)).toHaveLength(1))
    expect(captchaRequests(harness)[0]?.options?.query?.form).toBeUndefined()
    const range = (await screen.findByLabelText(
      CAPTCHA_COPY.sliderLabel,
    )) as HTMLInputElement
    await waitFor(() => expect(range.max).toBe('272'))
    range.focus()
    await browserUserEvent.keyboard('{End}')
    expect(range.value).toBe('272')
    await browserUserEvent.keyboard('{Enter}')
    await waitFor(() => expect(sends(harness)).toHaveLength(1))
    expect(sends(harness)[0]?.options?.body).toMatchObject({
      method: 'sms',
      human_proof: 'Y2FwdGNoYS1jaGFsbGVuZ2U.272',
    })
  })

  it('asks for audio through the accessibility entry, focuses and marks it, and sends', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList(['sms'], {
          humanPhoneProvider: 'captcha',
          humanPhoneRequired: true,
          humanOtherProvider: 'off',
          humanOtherRequired: false,
        }),
      [CAPTCHA_CHALLENGE]: ({ options }) =>
        captchaChallenge({
          form: options?.query?.form === 'audio' ? 'audio' : 'image',
        }),
      [LOGIN_START]: () => codeChallenge(),
    })
    const { container } = renderWithProviders(
      <PhoneCountryDefaultProvider country="CN">
        <SMSSignInForm session={harness.session} />
      </PhoneCountryDefaultProvider>,
    )
    const send = await fillPhone()
    await userEvent.setup().click(send)
    await screen.findByAltText(CAPTCHA_COPY.instruction.typeTheDigits)
    expect(captchaRequests(harness)[0]?.options?.query?.form).toBeUndefined()
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: CAPTCHA_COPY.useAudio }))
    await waitFor(() =>
      expect(
        captchaRequests(harness).map((call) => call.options?.query?.form),
      ).toEqual([undefined, 'audio']),
    )
    const audio = container.querySelector('audio')
    await waitFor(() =>
      expect(audio).toHaveAttribute(
        'src',
        '/api/v1/auth/human/captcha/audio/1',
      ),
    )
    expect(audio).toHaveAttribute('lang', 'zh-CN')
    expect(document.activeElement).toBe(audio)
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(CAPTCHA_COPY.answerLabel),
      '65432',
    )
    await user.click(screen.getByRole('button', { name: CAPTCHA_COPY.submit }))
    await waitFor(() => expect(sends(harness)).toHaveLength(1))
    expect(sends(harness)[0]?.options?.body).toMatchObject({
      method: 'sms',
      human_proof: 'Y2FwdGNoYS1jaGFsbGVuZ2U.65432',
    })
  })

  it('holds the accessibility entry and states the wait when it is refused', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList(['sms'], {
          humanPhoneProvider: 'captcha',
          humanPhoneRequired: true,
          humanOtherProvider: 'off',
          humanOtherRequired: false,
        }),
      [CAPTCHA_CHALLENGE]: ({ options }) => {
        if (options?.query?.form === 'audio') {
          throw apiError(429, 'authn.throttled', 42_000)
        }
        return captchaChallenge({ form: 'image' })
      },
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(
      <PhoneCountryDefaultProvider country="CN">
        <SMSSignInForm session={harness.session} />
      </PhoneCountryDefaultProvider>,
    )
    const send = await fillPhone()
    await userEvent.setup().click(send)
    await screen.findByAltText(CAPTCHA_COPY.instruction.typeTheDigits)
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: CAPTCHA_COPY.useAudio }))
    await waitFor(() =>
      expect(
        screen.getByText(
          CAPTCHA_COPY.audioWait.replace('{{seconds}}', '42'),
        ),
      ).toBeInTheDocument(),
    )
    expect(
      screen.getByRole('button', { name: CAPTCHA_COPY.useAudio }),
    ).toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.getByAltText(CAPTCHA_COPY.instruction.typeTheDigits),
    ).toBeInTheDocument()
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(CAPTCHA_COPY.answerLabel),
      '12345',
    )
    await user.click(screen.getByRole('button', { name: CAPTCHA_COPY.submit }))
    await waitFor(() => expect(sends(harness)).toHaveLength(1))
    expect(sends(harness)[0]?.options?.body).toMatchObject({
      human_proof: 'Y2FwdGNoYS1jaGFsbGVuZ2U.12345',
    })
  })

  it('keeps the sent-notice skin clipped until its text is painted', async () => {
    await page.viewport(900, 900)
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(
      <PhoneCountryDefaultProvider country="CN">
        <SMSSignInForm session={harness.session} />
      </PhoneCountryDefaultProvider>,
    )

    const frames: { readonly text: string; readonly clip: string }[] = []
    let sampling = true
    const onCodeStep = () =>
      document.querySelector('input[autocomplete="one-time-code"]') !== null
    const sample = () => {
      if (!sampling) {
        return
      }
      const node = document.getElementById(SMS_NOTICE_ID)
      const alert = node?.closest('.MuiAlert-root')
      if (
        onCodeStep() &&
        node !== null &&
        node !== undefined &&
        alert instanceof HTMLElement
      ) {
        frames.push({
          text: node.textContent ?? '',
          clip: getComputedStyle(alert).clipPath,
        })
      }
      requestAnimationFrame(sample)
    }
    requestAnimationFrame(sample)

    const sent = zhCN.smsSignIn.sentNotice.replace('{{phone}}', CN_E164)
    try {
      const send = await fillPhone()
      await userEvent.setup().click(send)
      await waitFor(() =>
        expect(document.getElementById(SMS_NOTICE_ID)).toHaveTextContent(sent),
      )
    } finally {
      sampling = false
    }

    expect(
      frames.some(
        (frame) => frame.text === '' && frame.clip === 'inset(50%)',
      ),
    ).toBe(true)
    expect(
      frames.some(
        (frame) => frame.text === '' && frame.clip !== 'inset(50%)',
      ),
    ).toBe(false)

    const written = document.getElementById(SMS_NOTICE_ID)!
    const writtenAlert = written.closest('.MuiAlert-root') as HTMLElement
    expect(written).toHaveTextContent(sent)
    expect(getComputedStyle(writtenAlert).clipPath).toBe('none')
  })
})

describe('SMSSignInForm focus in Chromium', () => {
  it('keeps the send control focused and busy while its captcha proof is outstanding', async () => {
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
    const send = await fillPhone()
    send.focus()
    expect(document.activeElement).toBe(send)
    await browserUserEvent.keyboard('{Enter}')
    await screen.findByLabelText(CAPTCHA_COPY.answerLabel)
    expect(document.activeElement).toBe(send)
    await waitFor(() => {
      expect(send).toHaveAttribute('aria-busy', 'true')
      expect(send).toHaveAttribute('aria-disabled', 'true')
    })
    expect(send).not.toHaveAttribute('disabled')
    expect(sends(harness)).toHaveLength(0)
  })

  it('keeps the send control focused while the code request is in flight', async () => {
    let releaseStart: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_START]: () =>
        new Promise((resolve) => {
          releaseStart = resolve
        }),
    })
    renderWithProviders(
      <PhoneCountryDefaultProvider country="CN">
        <SMSSignInForm session={harness.session} humanGate={NO_HUMAN_GATE} />
      </PhoneCountryDefaultProvider>,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
      CN_NATIONAL,
    )
    const send = screen.getByRole('button', { name: zhCN.smsSignIn.sendCode })
    await user.click(send)
    expect(send).toHaveFocus()
    await waitFor(() => {
      expect(send).toHaveAttribute('aria-disabled', 'true')
      expect(send).toHaveAttribute('aria-busy', 'true')
    })
    expect(send).toHaveFocus()
    releaseStart(codeChallenge())
    await waitFor(() =>
      expect(
        screen.getByLabelText(zhCN.smsSignIn.codeLabel),
      ).toBeInTheDocument(),
    )
  })

  it('keeps a held resend reachable, explained and focused while its wait runs', async () => {
    const harness = makeHarness({ [LOGIN_START]: () => codeChallenge(60) })
    renderWithProviders(
      <PhoneCountryDefaultProvider country="CN">
        <SMSSignInForm session={harness.session} humanGate={NO_HUMAN_GATE} />
      </PhoneCountryDefaultProvider>,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
      CN_NATIONAL,
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }),
    )
    const resend = await screen.findByRole('button', {
      name: zhCN.smsSignIn.resendCode,
    })
    resend.focus()
    expect(resend).toHaveFocus()
    await waitFor(() =>
      expect(resend).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(resend).not.toHaveAttribute('disabled')
    expect(resend).not.toHaveAttribute('aria-busy')
    expect(resend).toHaveAccessibleDescription(
      zhCN.smsSignIn.resendIn.replace('{{seconds}}', '60'),
    )
    const sendsBefore = sends(harness).length
    await user.click(resend)
    expect(sends(harness)).toHaveLength(sendsBefore)
    expect(resend).toHaveFocus()
  })

  it('keeps a held resend in place and focused, and returns focus to it when the wait is cancelled', async () => {
    const fake = installFakeTurnstile()
    try {
      const harness = makeHarness({
        [LOGIN_METHODS]: () =>
          methodList(['sms'], {
            humanRequired: true,
            humanProvider: 'turnstile',
            humanSiteKey: 'site-1',
          }),
        [LOGIN_START]: () => codeChallenge(),
      })
      renderWithProviders(
        <PhoneCountryDefaultProvider country="CN">
          <SMSSignInForm session={harness.session} />
        </PhoneCountryDefaultProvider>,
      )
      const user = userEvent.setup()
      await user.type(
        screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
        CN_NATIONAL,
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(sends(harness)).toHaveLength(1))
      await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
      const resend = screen.getByRole('button', {
        name: zhCN.smsSignIn.resendCode,
      })
      resend.focus()
      expect(document.activeElement).toBe(resend)
      await browserUserEvent.keyboard('{Enter}')
      await waitFor(() => expect(fake.widgets).toHaveLength(2))
      expect(
        screen.getByRole('group', {
          name: uiKitZhCN.humanVerification.groupLabel,
        }),
      ).toBeInTheDocument()
      expect(resend).toHaveFocus()
      await waitFor(() => expect(resend).toHaveAttribute('aria-busy', 'true'))
      expect(resend).not.toHaveAttribute('disabled')
      const codeInput = screen.getByLabelText(zhCN.smsSignIn.codeLabel)
      expect(codeInput).toBeInTheDocument()
      expect(codeInput).not.toHaveAttribute('disabled')
      const submit = screen.getByRole('button', {
        name: zhCN.smsSignIn.submit,
      })
      expect(submit).not.toHaveAttribute('aria-disabled')
      expect(submit).not.toHaveAttribute('disabled')
      const cancel = screen.getByRole('button', {
        name: zhCN.smsSignIn.cancelResend,
      })
      cancel.focus()
      expect(cancel).toHaveFocus()
      expect(cancel).not.toHaveAttribute('aria-disabled')
      expect(cancel).not.toHaveAttribute('aria-busy')
      await browserUserEvent.keyboard('{Enter}')
      await waitFor(() => expect(document.activeElement).toBe(resend))
      expect(sends(harness)).toHaveLength(1)
      expect(
        screen.queryByRole('button', { name: zhCN.smsSignIn.cancelResend }),
      ).toBeNull()
      expect(
        screen.getByLabelText(zhCN.smsSignIn.codeLabel),
      ).toBeInTheDocument()
    } finally {
      fake.restore()
    }
  })

  it('keeps the code submit focused after its answer is refused', async () => {
    let rejectAnswer: (error: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: () =>
        new Promise((_, reject) => {
          rejectAnswer = reject
        }),
    })
    renderWithProviders(
      <PhoneCountryDefaultProvider country="CN">
        <SMSSignInForm session={harness.session} humanGate={NO_HUMAN_GATE} />
      </PhoneCountryDefaultProvider>,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
      CN_NATIONAL,
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }),
    )
    await user.type(
      await screen.findByLabelText(zhCN.smsSignIn.codeLabel),
      '000000',
    )
    const submit = screen.getByRole('button', { name: zhCN.smsSignIn.submit })
    await user.click(submit)
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 50)
    })
    expect(submit).toHaveFocus()
    expect(submit).toHaveAttribute('aria-busy', 'true')
    await act(async () => {
      rejectAnswer(apiError(401, 'authn.login_failed'))
    })
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.smsSignIn.loginFailed,
      ),
    )
    expect(submit).toHaveFocus()
    expect(submit).not.toHaveAttribute('aria-busy')
  })
})
