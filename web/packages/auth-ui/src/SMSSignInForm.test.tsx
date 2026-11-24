import { describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '@speed/api-client'
import { switchLanguage } from '@speed/i18n'
import { SMSSignInForm, SMS_NOTICE_ID } from './SMSSignInForm.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  HUMAN_CHALLENGE,
  LOGIN_ANSWER,
  LOGIN_METHODS,
  LOGIN_START,
  PROFILE_GET,
  apiError,
  authenticatedCompletion,
  challengeResponse,
  completeFlow,
  makeHarness,
  methodList,
  openFlow,
  principal,
} from '@speed/test-utils/session-harness'
import type { Harness } from '@speed/test-utils/session-harness'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { uiKitResources, PhoneCountryDefaultProvider } from '@speed/ui-kit'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const CN_NATIONAL = '13800000001'
const CN_E164 = '+8613800000001'
const US_NATIONAL = '4155552671'
const US_E164 = '+14155552671'

const NO_HUMAN_GATE = {
  phone: { required: false, provider: 'off' },
  other: { required: false, provider: 'off' },
} as const

function sends(harness: Harness) {
  return harness.calls.filter((call) => call.path === '/api/v1/auth/flows')
}
const UI_KIT_ZH = uiKitResources['zh-CN'] as {
  readonly phoneNumber: { readonly incomplete: string; readonly invalid: string }
  readonly form: { readonly required: string }
}

async function selectCountry(
  user: ReturnType<typeof userEvent.setup>,
  code: string,
  callingCode: string,
): Promise<void> {
  const selector = screen.getByRole('combobox', {
    name: zhCN.smsSignIn.countryLabel,
  })
  await user.type(selector, code)
  await user.click(
    await screen.findByRole('option', {
      name: new RegExp(`\\+${callingCode}$`),
    }),
  )
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

function waitSentence(seconds: number): string {
  const [before = '', after = ''] = zhCN.throttle.retryAfter.split('{{seconds}}')
  return `${before}${seconds}${after}`
}

function visibleSentence(alert: HTMLElement): HTMLElement | null {
  return alert.querySelector<HTMLElement>('span[aria-hidden="true"]')
}

async function sendCode(national: string = CN_NATIONAL): Promise<void> {
  const user = userEvent.setup()
  await selectCountry(user, 'CN', '86')
  await user.type(screen.getByLabelText(zhCN.smsSignIn.phoneLabel), national)
  await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }))
}

async function settleMicrotasks(turns = 20): Promise<void> {
  await act(async () => {
    for (let turn = 0; turn < turns; turn += 1) {
      await Promise.resolve()
    }
  })
}

function stubNavigatorLanguage(language: string): () => void {
  const own = Object.getOwnPropertyDescriptor(window.navigator, 'language')
  Object.defineProperty(window.navigator, 'language', {
    configurable: true,
    value: language,
  })
  return () => {
    if (own !== undefined) {
      Object.defineProperty(window.navigator, 'language', own)
    } else {
      delete (window.navigator as unknown as Record<string, unknown>).language
    }
  }
}

describe('SMSSignInForm', () => {
  it('begin the sms flow on the phone step and flip to the code step on the challenge', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    await waitFor(() =>
      expect(
        screen.getByLabelText(zhCN.smsSignIn.codeLabel),
      ).toBeInTheDocument(),
    )
    expect(sends(harness)).toHaveLength(1)
    expect(sends(harness)[0]?.method).toBe('POST')
    expect(sends(harness)[0]?.path).toBe('/api/v1/auth/flows')
    expect(sends(harness)[0]?.options?.body).toMatchObject({
      method: 'sms',
      delivery: 'session',
      binding: 'cookie',
      input: { phone: CN_E164 },
    })
    await waitFor(() =>
      expect(document.getElementById(SMS_NOTICE_ID)).toHaveTextContent(
        zhCN.smsSignIn.sentNotice.replace('{{phone}}', CN_E164),
      ),
    )
  })

  it('re-render the sent notice in the switched language', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    const { i18n } = renderWithProviders(
      <SMSSignInForm session={harness.session} />,
    )
    await sendCode()
    await waitFor(() =>
      expect(document.getElementById(SMS_NOTICE_ID)).toHaveTextContent(
        zhCN.smsSignIn.sentNotice.replace('{{phone}}', CN_E164),
      ),
    )

    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })
    await waitFor(() =>
      expect(document.getElementById(SMS_NOTICE_ID)).toHaveTextContent(
        enUS.smsSignIn.sentNotice.replace('{{phone}}', CN_E164),
      ),
    )
  })

  it('name no token delivery on the code request -- the session delivery is auth-core’s', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    const startFlow = vi.spyOn(harness.session, 'startFlow')
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    await waitFor(() => expect(startFlow).toHaveBeenCalledTimes(1))
    expect(startFlow.mock.calls[0]?.[0]?.delivery).not.toBe('token')
  })

  it('answer the code and fire onSignedIn on completion', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: (call) => {
        const step = (call.options?.body as { step?: string }).step
        return step === 'complete'
          ? authenticatedCompletion()
          : completeFlow(2)
      },
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <SMSSignInForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    await sendCode()
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText(zhCN.smsSignIn.codeLabel), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    const answerBodies = harness.calls
      .filter((call) => call.path === '/api/v1/auth/flows/answer')
      .map((call) => call.options?.body)
    expect(answerBodies[0]).toMatchObject({
      step: 'answer',
      input: { code: '123456' },
    })
    expect(answerBodies[1]).toMatchObject({ step: 'complete' })
    expect(harness.store.get()).toBe('access-1')
  })

  it('renders a username refusal under the field of the parked single-field step', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: (call) => {
        if ((call.options?.body as { seq?: number }).seq === 1) {
          return openFlow({
            seq: 2,
            state: 'signup',
            next: {
              kind: 'challenge',
              fields: [{ name: 'username', kind: 'text', required: true }],
            },
          })
        }
        throw apiError(409, 'authn.username_taken')
      },
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const user = userEvent.setup()
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(code, '123456')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    const username = await screen.findByLabelText(zhCN.signInFlow.usernameLabel)
    await user.type(username, 'ada')
    await user.click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))

    await waitFor(() =>
      expect(username).toHaveAccessibleDescription(
        zhCN.signInFlow.usernameTaken,
      ),
    )
    expect(username).toHaveAttribute('aria-invalid', 'true')
    expect(username).toHaveFocus()
    expect(username).toHaveValue('ada')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('replaces a username step whose flow is gone with a restart that starts a new flow', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: (call) => {
        const step = (call.options?.body as { step?: string }).step
        if (step === 'complete') {
          return authenticatedCompletion()
        }
        if (
          (call.options?.body as { seq?: number }).seq === 1
        ) {
          return openFlow({
            seq: 2,
            state: 'signup',
            next: {
              kind: 'challenge',
              fields: [{ name: 'username', kind: 'text', required: true }],
            },
          })
        }
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const user = userEvent.setup()
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(code, '123456')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    const username = await screen.findByLabelText(zhCN.signInFlow.usernameLabel)
    await user.type(username, 'ada')
    await user.click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))

    expect(
      await screen.findByText(zhCN.signInFlow.flowExpired),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(zhCN.signInFlow.usernameLabel),
    ).toBeNull()

    const answersBefore = harness.calls.filter(
      (call) => call.path === '/api/v1/auth/flows/answer',
    ).length
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.signInAgain }),
    )
    await waitFor(() => expect(sends(harness)).toHaveLength(2))
    expect(sends(harness)[1]?.options?.body).toMatchObject({
      method: 'sms',
      input: { phone: CN_E164 },
    })
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows/answer'),
    ).toHaveLength(answersBefore)
  })

  it('keep the send control busy and focused while the request is in flight', async () => {
    let resolveStart: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_START]: () =>
        new Promise((resolve) => {
          resolveStart = resolve
        }),
    })
    renderWithProviders(
      <SMSSignInForm
        session={harness.session}
        humanGate={NO_HUMAN_GATE}
      />,
    )
    const user = userEvent.setup()
    await selectCountry(user, 'CN', '86')
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
      CN_NATIONAL,
    )
    const send = screen.getByRole('button', { name: zhCN.smsSignIn.sendCode })
    await user.click(send)
    await waitFor(() => {
      const current = screen.getByRole('button', {
        name: zhCN.smsSignIn.sendCode,
      })
      expect(current).toHaveAttribute('aria-disabled', 'true')
      expect(current).toHaveAttribute('aria-busy', 'true')
      expect(current).toHaveFocus()
    })
    resolveStart(codeChallenge())
    await waitFor(() =>
      expect(screen.getByLabelText(zhCN.smsSignIn.codeLabel)).toBeInTheDocument(),
    )
  })

  it('refuse a wrong code with this step’s own copy, not the password sentence', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText(zhCN.smsSignIn.codeLabel), '000000')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.smsSignIn.loginFailed,
      ),
    )
    expect(screen.queryByText(zhCN.errors.authn.login_failed)).toBeNull()
    expect(screen.getByRole('alert')).not.toHaveTextContent(
      zhCN.passwordSignIn.passwordLabel,
    )
    expect(harness.store.get()).toBeNull()
    expect(screen.getByLabelText(zhCN.smsSignIn.codeLabel)).toBeInTheDocument()
  })

  it('keep the code submit busy and focused, and answer Enter only once', async () => {
    let resolveAnswer: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: () =>
        new Promise((resolve) => {
          resolveAnswer = resolve
        }),
    })
    renderWithProviders(
      <SMSSignInForm
        session={harness.session}
        humanGate={NO_HUMAN_GATE}
      />,
    )
    await sendCode()
    const user = userEvent.setup()
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(code, '123456')
    const submit = screen.getByRole('button', { name: zhCN.smsSignIn.submit })
    await user.click(submit)
    await waitFor(() => {
      expect(submit).toHaveAttribute('aria-disabled', 'true')
      expect(submit).toHaveAttribute('aria-busy', 'true')
      expect(submit).toHaveFocus()
    })
    const answers = () =>
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows/answer')
    const answersBefore = answers().length
    code.focus()
    await user.keyboard('{Enter}')
    await act(async () => {
      await Promise.resolve()
    })
    expect(answers()).toHaveLength(answersBefore)
    await act(async () => {
      resolveAnswer(authenticatedCompletion({ access_token: 'access-1' }))
    })
  })

  it('make the resend unavailable, without a reason, while the answer is in flight', async () => {
    let resolveAnswer: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: () =>
        new Promise((resolve) => {
          resolveAnswer = resolve
        }),
    })
    renderWithProviders(
      <SMSSignInForm
        session={harness.session}
        humanGate={NO_HUMAN_GATE}
      />,
    )
    await sendCode()
    const user = userEvent.setup()
    await user.type(
      await screen.findByLabelText(zhCN.smsSignIn.codeLabel),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: zhCN.smsSignIn.resendCode }),
      ).toHaveAttribute('aria-disabled', 'true'),
    )
    const resend = screen.getByRole('button', {
      name: zhCN.smsSignIn.resendCode,
    })
    expect(resend).toHaveAttribute('aria-disabled', 'true')
    expect(resend).not.toHaveAttribute('aria-busy')
    expect(resend).not.toHaveAttribute('aria-describedby')
    resend.focus()
    expect(resend).toHaveFocus()
    const startsBefore = sends(harness).length
    await user.click(resend)
    expect(sends(harness)).toHaveLength(startsBefore)
    await act(async () => {
      resolveAnswer(authenticatedCompletion({ access_token: 'access-1' }))
    })
  })

  it('make the send unavailable, without a reason, after the viewer changes the phone mid-answer', async () => {
    let resolveAnswer: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: () =>
        new Promise((resolve) => {
          resolveAnswer = resolve
        }),
    })
    renderWithProviders(
      <SMSSignInForm
        session={harness.session}
        humanGate={NO_HUMAN_GATE}
      />,
    )
    await sendCode()
    const user = userEvent.setup()
    await user.type(
      await screen.findByLabelText(zhCN.smsSignIn.codeLabel),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: zhCN.smsSignIn.submit }),
      ).toHaveAttribute('aria-busy', 'true'),
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.smsSignIn.editPhone }),
    )
    const send = screen.getByRole('button', { name: zhCN.smsSignIn.sendCode })
    expect(send).toHaveAttribute('aria-disabled', 'true')
    expect(send).not.toHaveAttribute('aria-busy')
    expect(send).not.toHaveAttribute('aria-describedby')
    send.focus()
    expect(send).toHaveFocus()
    const startsBefore = sends(harness).length
    await user.click(send)
    await act(async () => {
      await Promise.resolve()
    })
    expect(sends(harness)).toHaveLength(startsBefore)

    await act(async () => {
      resolveAnswer(authenticatedCompletion({ access_token: 'access-1' }))
    })
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }),
      ).not.toHaveAttribute('aria-disabled'),
    )
  })

  it('clear the code step’s refusal when the viewer begins a new attempt', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const user = userEvent.setup()
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(code, '000000')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.smsSignIn.loginFailed,
      ),
    )

    await user.type(code, '1')
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect(code).toHaveValue('0000001')
  })

  it('keep the generic refusal for a step after the code', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: (call) => {
        const step = (call.options?.body as { step?: string }).step
        if (step === 'answer') {
          return openFlow({
            seq: 2,
            state: 'second',
            next: {
              kind: 'challenge',
              fields: [{ name: 'code', kind: 'code', required: true }],
            },
          })
        }
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText(zhCN.smsSignIn.codeLabel), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    await user.type(
      await screen.findByLabelText(zhCN.smsSignIn.codeLabel),
      '000000',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.login_failed,
      ),
    )
    expect(screen.queryByText(zhCN.smsSignIn.loginFailed)).toBeNull()
  })

  it('focus the number and announce its own message when the local check refuses', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    const user = userEvent.setup()
    await selectCountry(user, 'CN', '86')
    const phone = screen.getByLabelText(zhCN.smsSignIn.phoneLabel)
    await user.type(phone, '138000')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }))

    const message = UI_KIT_ZH.phoneNumber.incomplete.replace('{{code}}', '86')
    await waitFor(() => expect(phone).toHaveFocus())
    await waitFor(() =>
      expect(document.getElementById(SMS_NOTICE_ID)).toHaveTextContent(message),
    )
    expect(sends(harness)).toHaveLength(0)
  })

  it('re-announce the same phone refusal on every refused submit', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    const user = userEvent.setup()
    await selectCountry(user, 'CN', '86')
    const phone = screen.getByLabelText(zhCN.smsSignIn.phoneLabel)
    await user.type(phone, '138000')
    const submit = screen.getByRole('button', { name: zhCN.smsSignIn.sendCode })
    const region = document.getElementById(SMS_NOTICE_ID)!
    const mutations: MutationRecord[] = []
    const observer = new MutationObserver((records) => {
      mutations.push(...records)
    })
    observer.observe(region, {
      characterData: true,
      characterDataOldValue: true,
      childList: true,
      subtree: true,
    })
    const message = UI_KIT_ZH.phoneNumber.incomplete.replace('{{code}}', '86')
    try {
      await user.click(submit)
      await waitFor(() => expect(region).toHaveTextContent(message))
      expect(mutations.length).toBeGreaterThan(0)

      mutations.length = 0
      await user.click(submit)
      await waitFor(() => expect(region).toHaveTextContent(message))
      expect(mutations.length).toBeGreaterThanOrEqual(2)
      expect(sends(harness)).toHaveLength(0)
    } finally {
      observer.disconnect()
    }
  })

  it('re-announce the sent notice when a resend of the same number succeeds', async () => {
    let releaseResend: (value: unknown) => void = () => {}
    let starts = 0
    const harness = makeHarness({
      [LOGIN_START]: () => {
        starts += 1
        if (starts === 1) {
          return codeChallenge()
        }
        return new Promise((resolve) => {
          releaseResend = resolve
        })
      },
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const sent = zhCN.smsSignIn.sentNotice.replace('{{phone}}', CN_E164)
    await waitFor(() =>
      expect(document.getElementById(SMS_NOTICE_ID)).toHaveTextContent(sent),
    )

    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.smsSignIn.resendCode }),
    )
    await waitFor(() =>
      expect(document.getElementById(SMS_NOTICE_ID)?.textContent).toBe(''),
    )
    const region = document.getElementById(SMS_NOTICE_ID)!
    const mutations: MutationRecord[] = []
    const observer = new MutationObserver((records) => {
      mutations.push(...records)
    })
    observer.observe(region, {
      characterData: true,
      childList: true,
      subtree: true,
    })
    try {
      await act(async () => {
        releaseResend(codeChallenge())
      })
      await waitFor(() => expect(sends(harness)).toHaveLength(2))
      await waitFor(() => expect(region).toHaveTextContent(sent))
      expect(mutations.length).toBeGreaterThan(0)
    } finally {
      observer.disconnect()
    }
  })

  it('return to the phone step when the flow is gone (flow-invalid)', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText(zhCN.smsSignIn.codeLabel), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.flow_invalid,
      ),
    )
    expect(screen.getByLabelText(zhCN.smsSignIn.phoneLabel)).toBeInTheDocument()
    expect(
      screen.queryByLabelText(zhCN.smsSignIn.codeLabel),
    ).not.toBeInTheDocument()
  })

  it('render a refused send in one alert', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => {
        throw apiError(400, 'authn.unsupported_region')
      },
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.unsupported_region,
      ),
    )
    expect(screen.getByLabelText(zhCN.smsSignIn.phoneLabel)).toBeInTheDocument()
  })

  it('never reach the network for an empty submit', async () => {
    const harness = makeHarness({})
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }))
    await waitFor(() =>
      expect(screen.queryByRole('alert')).not.toBeInTheDocument(),
    )
    expect(sends(harness)).toHaveLength(0)
  })

  it('refuse a number too short for its country locally, naming the calling code', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    expect(screen.getByText(zhCN.smsSignIn.phoneHint)).toBeInTheDocument()

    const user = userEvent.setup()
    await selectCountry(user, 'CN', '86')
    const phone = screen.getByLabelText(zhCN.smsSignIn.phoneLabel)
    await user.type(phone, '138000')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }))

    await waitFor(() =>
      expect(
        screen.getAllByText(
          UI_KIT_ZH.phoneNumber.incomplete.replace('{{code}}', '86'),
        ).length,
      ).toBeGreaterThan(0),
    )
    expect(phone).toHaveAttribute('aria-invalid', 'true')
    expect(sends(harness)).toHaveLength(0)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('refuse a legal-length number with an impossible number plan locally', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    const user = userEvent.setup()
    await selectCountry(user, 'CN', '86')
    const phone = screen.getByLabelText(zhCN.smsSignIn.phoneLabel)
    await user.type(phone, '12345678901')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }))

    await waitFor(() =>
      expect(
        screen.getAllByText(
          UI_KIT_ZH.phoneNumber.invalid.replace('{{code}}', '86'),
        ).length,
      ).toBeGreaterThan(0),
    )
    expect(phone).toHaveAttribute('aria-invalid', 'true')
    expect(sends(harness)).toHaveLength(0)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('assemble a picked US number into E.164 as well', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    expect(
      screen.getByRole('combobox', { name: zhCN.smsSignIn.countryLabel }),
    ).toHaveValue('🇺🇸 +1')
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }))
    await waitFor(() => expect(sends(harness)).toHaveLength(1))
    expect(sends(harness)[0]?.options?.body).toMatchObject({
      method: 'sms',
      input: { phone: US_E164 },
    })
  })

  it('switch the country and fill the local part from a pasted E.164 number', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    const user = userEvent.setup()
    const phone = screen.getByLabelText(zhCN.smsSignIn.phoneLabel)
    await user.click(phone)
    await user.paste(CN_E164)

    expect(
      screen.getByRole('combobox', { name: zhCN.smsSignIn.countryLabel }),
    ).toHaveValue('🇨🇳 +86')
    expect(phone).toHaveValue('138 0000 0001')

    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }))
    await waitFor(() => expect(sends(harness)).toHaveLength(1))
    expect(sends(harness)[0]?.options?.body).toMatchObject({
      method: 'sms',
      input: { phone: CN_E164 },
    })
  })

  it('start the selector on the country the browser locale names', () => {
    const restore = stubNavigatorLanguage('zh-CN')
    try {
      const harness = makeHarness({})
      renderWithProviders(<SMSSignInForm session={harness.session} />)
      expect(
        screen.getByRole('combobox', { name: zhCN.smsSignIn.countryLabel }),
      ).toHaveValue('🇨🇳 +86')
    } finally {
      restore()
    }
  })

  it('name the selector and the number field for assistive technology', () => {
    const harness = makeHarness({})
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    expect(
      screen.getByRole('combobox', { name: zhCN.smsSignIn.countryLabel }),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
    ).toBeInTheDocument()
  })

  it('resend against the same number and clear the typed code', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const user = userEvent.setup()
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(code, '123456')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.resendCode }))
    await waitFor(() => expect(sends(harness)).toHaveLength(2))
    expect(sends(harness)[1]?.options?.body).toMatchObject({
      method: 'sms',
      input: { phone: CN_E164 },
    })
    await waitFor(() =>
      expect(screen.getByLabelText(zhCN.smsSignIn.codeLabel)).toHaveValue(''),
    )
  })

  it('hold the resend control for the successful send’s stated interval and count the note down', async () => {
    vi.useFakeTimers()
    try {
      const harness = makeHarness({
        [LOGIN_START]: () => codeChallenge(60),
      })
      renderWithProviders(
        <SMSSignInForm
          session={harness.session}
          humanGate={{
            phone: { required: false, provider: 'off' },
            other: { required: false, provider: 'off' },
          }}
        />,
      )
      fireEvent.change(screen.getByLabelText(zhCN.smsSignIn.phoneLabel), {
        target: { value: US_NATIONAL },
      })
      await act(async () => {
        fireEvent.click(
          screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }),
        )
      })
      const resend = screen.getByRole('button', {
        name: zhCN.smsSignIn.resendCode,
      })
      const noteText = (seconds: number): string =>
        zhCN.smsSignIn.resendIn.replace('{{seconds}}', String(seconds))
      expect(resend).toHaveAttribute('aria-disabled', 'true')
      expect(resend).not.toHaveAttribute('aria-busy')
      expect(resend).toHaveAttribute(
        'aria-describedby',
        'sms-sign-in-resend-note',
      )
      expect(resend).toHaveAccessibleDescription(noteText(60))
      expect(
        document.getElementById('sms-sign-in-resend-note'),
      ).toHaveTextContent(noteText(60))
      expect(screen.queryByRole('alert')).toBeNull()

      resend.focus()
      expect(resend).toHaveFocus()
      const sendsBefore = sends(harness).length
      await act(async () => {
        fireEvent.click(resend)
        for (let turn = 0; turn < 10; turn += 1) {
          await Promise.resolve()
        }
      })
      expect(sends(harness)).toHaveLength(sendsBefore)
      expect(resend).toHaveFocus()

      act(() => {
        vi.advanceTimersByTime(1000)
      })
      expect(
        document.getElementById('sms-sign-in-resend-note'),
      ).toHaveTextContent(noteText(59))
      expect(resend).toHaveAttribute('aria-disabled', 'true')
      expect(resend).toHaveFocus()

      act(() => {
        vi.advanceTimersByTime(59_000)
      })
      expect(resend).not.toHaveAttribute('aria-disabled')
      expect(resend).toHaveFocus()
      expect(document.getElementById('sms-sign-in-resend-note')).toBeNull()
      expect(resend).not.toHaveAttribute('aria-describedby')
      expect(screen.queryByRole('alert')).toBeNull()
    } finally {
      await restoreRealTimers()
    }
  })

  it('keep the code submit usable while only the resend waits out its interval', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(60),
      [LOGIN_ANSWER]: () =>
        authenticatedCompletion({ access_token: 'access-1' }),
    })
    renderWithProviders(
      <SMSSignInForm session={harness.session} humanGate={NO_HUMAN_GATE} />,
    )
    await sendCode()
    const resend = await screen.findByRole('button', {
      name: zhCN.smsSignIn.resendCode,
    })
    expect(resend).toHaveAttribute('aria-disabled', 'true')
    const submit = screen.getByRole('button', { name: zhCN.smsSignIn.submit })
    expect(submit).not.toHaveAttribute('aria-disabled')
    expect(submit).not.toHaveAttribute('aria-busy')
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.codeLabel),
      '123456',
    )
    await user.keyboard('{Enter}')
    await waitFor(() =>
      expect(
        harness.calls.filter(
          (call) => call.path === '/api/v1/auth/flows/answer',
        ),
      ).toHaveLength(1),
    )
  })

  it('hold a throttled resend for the stated wait and count it down in the banner', async () => {
    let starts = 0
    const harness = makeHarness({
      [LOGIN_START]: () => {
        starts += 1
        if (starts === 1) {
          return codeChallenge()
        }
        throw new ApiError({
          status: 429,
          code: 'authn.throttled',
          retryAfterMs: 2000,
          attempts: 1,
        })
      },
    })
    renderWithProviders(
      <SMSSignInForm
        session={harness.session}
        humanGate={NO_HUMAN_GATE}
      />,
    )
    await sendCode()
    const resend = await screen.findByRole('button', {
      name: zhCN.smsSignIn.resendCode,
    })
    expect(resend).not.toHaveAttribute('aria-disabled')

    vi.useFakeTimers()
    try {
      resend.focus()
      expect(resend).toHaveFocus()
      await act(async () => {
        fireEvent.click(resend)
        for (let turn = 0; turn < 10; turn += 1) {
          await Promise.resolve()
        }
      })
      const held = screen.getByRole('button', {
        name: zhCN.smsSignIn.resendCode,
      })
      expect(held).toHaveAttribute('aria-disabled', 'true')
      expect(held).not.toHaveAttribute('aria-busy')
      expect(held).toHaveAccessibleDescription(
        zhCN.smsSignIn.resendIn.replace('{{seconds}}', '2'),
      )
      expect(held).toHaveFocus()
      expect(sends(harness)).toHaveLength(2)

      const alert = screen.getByRole('alert')
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(2))
      expect(document.getElementById('sms-sign-in-resend-note')).toBeNull()
      const naming = screen.getAllByText(waitSentence(2))
      expect(naming.every((node) => alert.contains(node))).toBe(true)

      act(() => {
        vi.advanceTimersByTime(1000)
      })
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(1))
      expect(screen.getByText(waitSentence(2))).toBeInTheDocument()
      expect(held).toHaveAccessibleDescription(
        zhCN.smsSignIn.resendIn.replace('{{seconds}}', '1'),
      )
      act(() => {
        vi.advanceTimersByTime(999)
      })
      expect(held).toHaveAttribute('aria-disabled', 'true')
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(1))
      act(() => {
        vi.advanceTimersByTime(1)
      })
      expect(held).not.toHaveAttribute('aria-disabled')
      expect(held).toHaveFocus()
      expect(visibleSentence(alert)).toHaveTextContent(
        zhCN.errors.authn.throttled,
      )
      expect(visibleSentence(alert)).not.toHaveTextContent('0')
      expect(screen.getByRole('alert')).toBe(alert)
      expect(screen.getByText(waitSentence(2))).toBeInTheDocument()
      expect(document.getElementById('sms-sign-in-resend-note')).toBeNull()
      expect(held).not.toHaveAttribute('aria-describedby')
    } finally {
      await restoreRealTimers()
    }
  })

  it('hold the send control for a throttled send’s wait and keep its focus', async () => {
    vi.useFakeTimers()
    try {
      const harness = makeHarness({
        [LOGIN_START]: () => {
          throw new ApiError({
            status: 429,
            code: 'authn.throttled',
            retryAfterMs: 2000,
            attempts: 1,
          })
        },
      })
      renderWithProviders(
        <SMSSignInForm
          session={harness.session}
          humanGate={NO_HUMAN_GATE}
        />,
      )
      fireEvent.change(screen.getByLabelText(zhCN.smsSignIn.phoneLabel), {
        target: { value: US_NATIONAL },
      })
      const send = screen.getByRole('button', {
        name: zhCN.smsSignIn.sendCode,
      })
      send.focus()
      expect(send).toHaveFocus()
      await act(async () => {
        fireEvent.click(send)
        for (let turn = 0; turn < 10; turn += 1) {
          await Promise.resolve()
        }
      })
      expect(send).toHaveAttribute('aria-disabled', 'true')
      expect(send).not.toHaveAttribute('aria-busy')
      expect(send).toHaveAccessibleDescription(
        zhCN.smsSignIn.resendIn.replace('{{seconds}}', '2'),
      )
      expect(send).toHaveFocus()
      expect(screen.getByRole('alert')).toHaveTextContent(waitSentence(2))
      await act(async () => {
        fireEvent.click(send)
        for (let turn = 0; turn < 10; turn += 1) {
          await Promise.resolve()
        }
      })
      expect(sends(harness)).toHaveLength(1)
      act(() => {
        vi.advanceTimersByTime(2000)
      })
      expect(send).not.toHaveAttribute('aria-disabled')
      expect(send).not.toHaveAttribute('aria-describedby')
      expect(send).toHaveFocus()
    } finally {
      await restoreRealTimers()
    }
  })

  it('make the code submit unavailable, without a reason, while a resend is in flight', async () => {
    let releaseResend: (value: unknown) => void = () => {}
    let starts = 0
    const harness = makeHarness({
      [LOGIN_START]: () => {
        starts += 1
        if (starts === 1) {
          return codeChallenge()
        }
        return new Promise((resolve) => {
          releaseResend = resolve
        })
      },
    })
    renderWithProviders(
      <SMSSignInForm
        session={harness.session}
        humanGate={NO_HUMAN_GATE}
      />,
    )
    await sendCode()
    const user = userEvent.setup()
    const resend = await screen.findByRole('button', {
      name: zhCN.smsSignIn.resendCode,
    })
    await user.click(resend)
    expect(resend).toHaveAttribute('aria-busy', 'true')
    const submit = screen.getByRole('button', { name: zhCN.smsSignIn.submit })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toHaveAttribute('aria-busy')
    expect(submit).not.toHaveAttribute('aria-describedby')
    const answers = () =>
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows/answer')
    const answersBefore = answers().length
    await user.click(submit)
    expect(answers()).toHaveLength(answersBefore)
    await act(async () => {
      releaseResend(codeChallenge())
    })
  })

  it('return to the phone step on change phone', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: zhCN.smsSignIn.editPhone }),
    )
    expect(screen.getByLabelText(zhCN.smsSignIn.phoneLabel)).toBeInTheDocument()
    expect(
      screen.queryByLabelText(zhCN.smsSignIn.codeLabel),
    ).not.toBeInTheDocument()
  })

  it('release the old number’s resend hold when the viewer changes the phone', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(60),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    expect(
      await screen.findByRole('button', { name: zhCN.smsSignIn.resendCode }),
    ).toHaveAttribute('aria-disabled', 'true')
    expect(
      document.getElementById('sms-sign-in-resend-note'),
    ).toBeInTheDocument()
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.smsSignIn.editPhone }),
    )
    expect(
      screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }),
    ).not.toHaveAttribute('aria-disabled')
    expect(document.getElementById('sms-sign-in-resend-note')).toBeNull()
  })

  it('return to the phone step from a step this channel cannot render', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: () =>
        openFlow({
          seq: 2,
          state: 'second',
          next: {
            kind: 'redirect',
            url: 'https://idp.example/authorize',
            method: 'oidc',
          },
        }),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const user = userEvent.setup()
    await user.type(
      await screen.findByLabelText(zhCN.smsSignIn.codeLabel),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))

    expect(
      await screen.findByText(zhCN.signInFlow.unsupportedStep),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', {
        name: zhCN.signInFlow.unsupportedStepAction,
      }),
    )

    expect(screen.getByLabelText(zhCN.smsSignIn.phoneLabel)).toBeInTheDocument()
    expect(
      screen.queryByLabelText(zhCN.smsSignIn.codeLabel),
    ).not.toBeInTheDocument()
  })

  it('raise the code-step actions to the 44px floor under a coarse pointer', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const resend = await screen.findByRole('button', {
      name: zhCN.smsSignIn.resendCode,
    })
    const edit = screen.getByRole('button', { name: zhCN.smsSignIn.editPhone })
    expect(hasCoarsePointerTouchTarget(resend)).toBe(true)
    expect(hasCoarsePointerTouchTarget(edit)).toBe(true)
  })

  it('render the used-code notice on a superseded answer and refuse the same code locally', async () => {
    let releaseAnswer!: (value: unknown) => void
    const answerGate = new Promise((resolve) => {
      releaseAnswer = resolve
    })
    let answers = 0
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: () => {
        answers += 1
        return answers === 1
          ? answerGate
          : authenticatedCompletion({ access_token: 'access-race' })
      },
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(<SMSSignInForm session={harness.session} onSignedIn={onSignedIn} />)
    await sendCode()
    const user = userEvent.setup()
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(code, '424242')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    await waitFor(() =>
      expect(
        harness.calls.filter((call) => call.path === '/api/v1/auth/flows/answer'),
      ).toHaveLength(1),
    )
    await act(async () => {
      await harness.session.answerFlow({ seq: 2, step: 'complete', input: {} })
    })
    await act(async () => {
      releaseAnswer(authenticatedCompletion({ access_token: 'access-social' }))
    })
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.smsSignIn.codeUsed,
      ),
    )
    expect(onSignedIn).not.toHaveBeenCalled()
    await waitFor(() => expect(code).toHaveValue(''))
    const callsAfterSupersede = harness.calls.length
    await user.type(code, '424242')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.smsSignIn.codeUsed,
      ),
    )
    expect(harness.calls).toHaveLength(callsAfterSupersede)
  })

  it('render the en-US bundle on an English-starting instance', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />, {
      language: 'en-US',
    })
    expect(
      screen.getByRole('combobox', { name: enUS.smsSignIn.countryLabel }),
    ).toHaveValue('🇺🇸 +1')
    expect(
      screen.getByLabelText(enUS.smsSignIn.phoneLabel),
    ).toBeInTheDocument()
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(enUS.smsSignIn.phoneLabel),
      US_NATIONAL,
    )
    await user.click(
      screen.getByRole('button', { name: enUS.smsSignIn.sendCode }),
    )
    expect(
      await screen.findByLabelText(enUS.smsSignIn.codeLabel),
    ).toBeInTheDocument()
  })

  it('pass axe with no violations', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(
      <div>
        <h1>Sign in</h1>
        <SMSSignInForm session={harness.session} />
      </div>,
    )
    await expectNoAxeViolations()
  })

  it('contain a throwing onSignedIn: the committed login never looks like a failure', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: (call) => {
        const step = (call.options?.body as { step?: string }).step
        return step === 'complete'
          ? authenticatedCompletion()
          : completeFlow(2)
      },
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn(() => {
      throw new Error('host navigation failed')
    })
    renderWithProviders(<SMSSignInForm session={harness.session} onSignedIn={onSignedIn} />)
    await sendCode()
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText(zhCN.smsSignIn.codeLabel), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(harness.store.get()).toBe('access-1')
  })

  it('start the selector on the host-scoped default country', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <PhoneCountryDefaultProvider country="CN">
        <SMSSignInForm session={harness.session} />
      </PhoneCountryDefaultProvider>,
    )
    expect(
      screen.getByRole('combobox', { name: zhCN.smsSignIn.countryLabel }),
    ).toHaveValue('🇨🇳 +86')
  })

  it('consult the host’s start gate before sending: a refusal sends nothing', async () => {
    const harness = makeHarness({})
    const beforeStart = vi.fn(() => false)
    renderWithProviders(
      <SMSSignInForm session={harness.session} beforeStart={beforeStart} />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
      US_NATIONAL,
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }),
    )
    await waitFor(() => expect(beforeStart).toHaveBeenCalledTimes(1))
    expect(beforeStart).toHaveBeenCalledWith({ method: 'sms' })
    expect(sends(harness)).toHaveLength(0)
    expect(
      screen.queryByLabelText(zhCN.smsSignIn.codeLabel),
    ).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('keep the send control disabled while an async start gate is pending', async () => {
    const harness = makeHarness({ [LOGIN_START]: () => codeChallenge() })
    let release: (allowed: boolean) => void = () => {}
    const beforeStart = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          release = resolve
        }),
    )
    renderWithProviders(
      <SMSSignInForm
        session={harness.session}
        beforeStart={beforeStart}
        humanGate={NO_HUMAN_GATE}
      />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
      US_NATIONAL,
    )
    const send = screen.getByRole('button', { name: zhCN.smsSignIn.sendCode })
    await user.click(send)
    await waitFor(() => expect(beforeStart).toHaveBeenCalledTimes(1))
    await waitFor(() => {
      const current = screen.getByRole('button', {
        name: zhCN.smsSignIn.sendCode,
      })
      expect(current).toHaveAttribute('aria-disabled', 'true')
      expect(current).toHaveAttribute('aria-busy', 'true')
      expect(current).toHaveFocus()
    })
    expect(sends(harness)).toHaveLength(0)
    await act(async () => {
      release(true)
    })
    await waitFor(() =>
      expect(screen.getByLabelText(zhCN.smsSignIn.codeLabel)).toBeInTheDocument(),
    )
    expect(sends(harness)).toHaveLength(1)
  })

  it('refuse to send until the required verifier holds a token, then send with it', async () => {
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
      renderWithProviders(<SMSSignInForm session={harness.session} />)
      const user = userEvent.setup()
      await selectCountry(user, 'CN', '86')
      await user.type(
        screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
        CN_NATIONAL,
      )
      expect(fake.widgets).toHaveLength(0)
      const send = screen.getByRole('button', { name: zhCN.smsSignIn.sendCode })
      const form = send.closest('form')!
      await user.click(send)
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(fake.widgets[0]?.siteKey).toBe('site-1')
      const held = screen.getByRole('button', { name: zhCN.smsSignIn.sendCode })
      expect(held).toHaveAttribute('aria-busy', 'true')
      expect(held).toHaveAttribute('aria-disabled', 'true')
      expect(held).not.toHaveAttribute('disabled')
      expect(sends(harness)).toHaveLength(0)
      fireEvent.submit(form)
      expect(sends(harness)).toHaveLength(0)
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(sends(harness)).toHaveLength(1))
      expect(sends(harness)[0]?.options?.body).toMatchObject({
        method: 'sms',
        input: { phone: CN_E164 },
        human_proof: 'token-1',
      })
    } finally {
      fake.restore()
    }
  })

  it('hold a resend on its proof and return to the code step when the viewer cancels', async () => {
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
        [LOGIN_ANSWER]: () =>
          authenticatedCompletion({ access_token: 'access-1' }),
      })
      renderWithProviders(<SMSSignInForm session={harness.session} />)
      const user = userEvent.setup()
      await selectCountry(user, 'CN', '86')
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
      await user.click(
        screen.getByRole('button', { name: zhCN.smsSignIn.resendCode }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(2))
      const cancel = screen.getByRole('button', {
        name: zhCN.smsSignIn.cancelResend,
      })
      expect(cancel).not.toHaveAttribute('aria-disabled')
      expect(cancel).not.toHaveAttribute('aria-busy')
      expect(cancel).toBeEnabled()
      expect(sends(harness)).toHaveLength(1)
      await user.click(cancel)
      const code = screen.getByLabelText(zhCN.smsSignIn.codeLabel)
      const resend = screen.getByRole('button', {
        name: zhCN.smsSignIn.resendCode,
      })
      expect(resend).toHaveFocus()
      expect(resend).not.toHaveAttribute('aria-disabled')
      expect(resend).not.toHaveAttribute('aria-busy')
      const submit = screen.getByRole('button', {
        name: zhCN.smsSignIn.submit,
      })
      expect(submit).not.toHaveAttribute('aria-disabled')
      expect(submit).not.toHaveAttribute('aria-busy')
      expect(sends(harness)).toHaveLength(1)
      await user.type(code, '123456')
      await user.click(submit)
      await waitFor(() =>
        expect(
          harness.calls.filter(
            (call) => call.path === '/api/v1/auth/flows/answer',
          ),
        ).toHaveLength(1),
      )
    } finally {
      fake.restore()
    }
  })

  it('abandon a held resend when the code in hand is submitted and keep the parked step it reaches', async () => {
    const fake = installFakeTurnstile()
    try {
      let starts = 0
      let resolveAnswer: (value: ReturnType<typeof openFlow>) => void = () => {}
      const harness = makeHarness({
        [LOGIN_METHODS]: () =>
          methodList(['sms'], {
            humanRequired: true,
            humanProvider: 'turnstile',
            humanSiteKey: 'site-1',
          }),
        [LOGIN_START]: () => {
          starts += 1
          return codeChallenge()
        },
        [LOGIN_ANSWER]: () =>
          new Promise<ReturnType<typeof openFlow>>((resolve) => {
            resolveAnswer = resolve
          }),
      })
      renderWithProviders(<SMSSignInForm session={harness.session} />)
      const user = userEvent.setup()
      await selectCountry(user, 'CN', '86')
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
      await waitFor(() => expect(starts).toBe(1))
      await screen.findByLabelText(zhCN.smsSignIn.codeLabel)

      await user.click(
        screen.getByRole('button', { name: zhCN.smsSignIn.resendCode }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(2))

      await user.type(
        screen.getByLabelText(zhCN.smsSignIn.codeLabel),
        '123456',
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.smsSignIn.submit }),
      )
      await waitFor(() =>
        expect(
          harness.calls.filter(
            (call) => call.path === '/api/v1/auth/flows/answer',
          ),
        ).toHaveLength(1),
      )

      act(() => {
        fake.token('token-2')
      })
      await settleMicrotasks()
      expect(starts).toBe(1)

      await act(async () => {
        resolveAnswer(
          openFlow({
            seq: 2,
            state: 'second',
            next: {
              kind: 'challenge',
              fields: [{ name: 'code', kind: 'code', required: true }],
            },
          }),
        )
        for (let turn = 0; turn < 20; turn += 1) {
          await Promise.resolve()
        }
      })
      await settleMicrotasks()
      expect(starts).toBe(1)
      expect(
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      ).toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: zhCN.smsSignIn.resendCode }),
      ).toBeNull()
      expect(
        harness.calls.filter((call) => call.path === '/api/v1/auth/flows'),
      ).toHaveLength(1)
    } finally {
      fake.restore()
    }
  })

  it('abandon a held resend when the code in hand is submitted and start nothing when the answer is refused', async () => {
    const fake = installFakeTurnstile()
    try {
      let starts = 0
      let rejectAnswer: (error: unknown) => void = () => {}
      const harness = makeHarness({
        [LOGIN_METHODS]: () =>
          methodList(['sms'], {
            humanRequired: true,
            humanProvider: 'turnstile',
            humanSiteKey: 'site-1',
          }),
        [LOGIN_START]: () => {
          starts += 1
          return codeChallenge()
        },
        [LOGIN_ANSWER]: () =>
          new Promise((_resolve, reject) => {
            rejectAnswer = reject
          }),
      })
      renderWithProviders(<SMSSignInForm session={harness.session} />)
      const user = userEvent.setup()
      await selectCountry(user, 'CN', '86')
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
      await waitFor(() => expect(starts).toBe(1))
      await screen.findByLabelText(zhCN.smsSignIn.codeLabel)

      await user.click(
        screen.getByRole('button', { name: zhCN.smsSignIn.resendCode }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(2))
      await user.type(
        screen.getByLabelText(zhCN.smsSignIn.codeLabel),
        '123456',
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.smsSignIn.submit }),
      )
      await waitFor(() =>
        expect(
          harness.calls.filter(
            (call) => call.path === '/api/v1/auth/flows/answer',
          ),
        ).toHaveLength(1),
      )
      act(() => {
        fake.token('token-2')
      })
      await settleMicrotasks()
      expect(starts).toBe(1)

      await act(async () => {
        rejectAnswer(apiError(401, 'authn.login_failed'))
        for (let turn = 0; turn < 20; turn += 1) {
          await Promise.resolve()
        }
      })
      await settleMicrotasks()
      expect(starts).toBe(1)
      const resend = screen.getByRole('button', {
        name: zhCN.smsSignIn.resendCode,
      })
      expect(resend).not.toHaveAttribute('aria-disabled')
      await user.click(resend)
      await waitFor(() => expect(fake.widgets).toHaveLength(3))
      expect(starts).toBe(1)
      act(() => {
        fake.token('token-3')
      })
      await waitFor(() => expect(starts).toBe(2))
      const startCalls = harness.calls.filter(
        (call) => call.path === '/api/v1/auth/flows',
      )
      expect(startCalls[1]?.options?.body).toMatchObject({
        method: 'sms',
        input: { phone: CN_E164 },
        human_proof: 'token-3',
      })
    } finally {
      fake.restore()
    }
  })

  it('let the viewer abandon a send held on its proof by editing the number', async () => {
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
      renderWithProviders(<SMSSignInForm session={harness.session} />)
      const user = userEvent.setup()
      await selectCountry(user, 'CN', '86')
      const phone = screen.getByLabelText(zhCN.smsSignIn.phoneLabel)
      await user.type(phone, CN_NATIONAL)
      await user.click(
        screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(sends(harness)).toHaveLength(0)
      await user.clear(phone)
      const send = await screen.findByRole('button', {
        name: zhCN.smsSignIn.sendCode,
      })
      expect(send).not.toHaveAttribute('aria-disabled')
      expect(send).not.toHaveAttribute('aria-busy')
      expect(sends(harness)).toHaveLength(0)
    } finally {
      fake.restore()
    }
  })

  it('solve the proof-of-work gate and send the proof it produces', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList(['sms'], {
          humanRequired: true,
          humanProvider: 'pow',
        }),
      [HUMAN_CHALLENGE]: () => challengeResponse({ difficulty: 4 }),
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    expect(
      harness.calls.filter(
        (call) => call.path === '/api/v1/auth/human/challenge',
      ),
    ).toHaveLength(0)
    await sendCode()
    await waitFor(() =>
      expect(
        harness.calls.filter(
          (call) => call.path === '/api/v1/auth/human/challenge',
        ),
      ).toHaveLength(1),
    )
    await waitFor(() => expect(sends(harness)).toHaveLength(1))
    const body = sends(harness)[0]?.options?.body as
      | { readonly human_proof?: string }
      | undefined
    expect(body?.human_proof).toMatch(/^[A-Za-z0-9_-]+\.\d+$/)
  })

  it('clear the failure banner when a step after the code is answered again', async () => {
    let answers = 0
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
      [LOGIN_ANSWER]: (call) => {
        const step = (call.options?.body as { step?: string }).step
        if (step === 'answer') {
          return openFlow({
            seq: 2,
            state: 'second',
            next: {
              kind: 'challenge',
              fields: [{ name: 'code', kind: 'code', required: true }],
            },
          })
        }
        answers += 1
        if (answers === 1) {
          throw apiError(401, 'authn.login_failed')
        }
        return openFlow({
          seq: 4,
          state: 'second',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', required: true }],
          },
        })
      },
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const user = userEvent.setup()
    await user.type(
      await screen.findByLabelText(zhCN.smsSignIn.codeLabel),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
    const second = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(second, '000000')
    await user.click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.login_failed,
      ),
    )
    await user.click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect(screen.getByLabelText(zhCN.smsSignIn.codeLabel)).toBeInTheDocument()
  })

  it('carry the proof on an answer that may send (an SMS second factor)', async () => {
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
        [LOGIN_ANSWER]: (call) => {
          const body = call.options?.body as { step?: string }
          if (body.step === 'answer') {
            return openFlow({
              seq: 2,
              state: 'second',
              next: {
                kind: 'factor',
                factors: [{ id: 'mfa-1', kind: 'sms', label: 'Phone' }],
              },
            })
          }
          return openFlow({
            seq: 3,
            state: 'second',
            next: {
              kind: 'challenge',
              fields: [{ name: 'code', kind: 'code' }],
            },
          })
        },
      })
      renderWithProviders(<SMSSignInForm session={harness.session} />)
      const user = userEvent.setup()
      await selectCountry(user, 'CN', '86')
      await user.type(
        screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
        CN_NATIONAL,
      )
      expect(fake.widgets).toHaveLength(0)
      await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }))
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(sends(harness)).toHaveLength(1))
      expect(sends(harness)[0]?.options?.body).toMatchObject({
        method: 'sms',
        input: { phone: CN_E164 },
        human_proof: 'token-1',
      })
      const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
      await user.type(code, '123456')
      await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
      const smsFactor = zhCN.signInFlow.factorKind.sms
      await waitFor(() =>
        expect(
          screen.getByRole('button', { name: smsFactor }),
        ).toBeInTheDocument(),
      )
      await user.click(screen.getByRole('button', { name: smsFactor }))
      await waitFor(() => expect(fake.widgets).toHaveLength(2))
      act(() => {
        fake.token('token-2')
      })
      await waitFor(() =>
        expect(
          harness.calls.filter(
            (call) =>
              call.path === '/api/v1/auth/flows/answer' &&
              (call.options?.body as { step?: string }).step === 'factor',
          ),
        ).toHaveLength(1),
      )
      const factorAnswer = harness.calls.find(
        (call) =>
          call.path === '/api/v1/auth/flows/answer' &&
          (call.options?.body as { step?: string }).step === 'factor',
      )
      expect(factorAnswer?.options?.body).toMatchObject({
        input: { factor: 'mfa-1' },
        human_proof: 'token-2',
      })
    } finally {
      fake.restore()
    }
  })
})
