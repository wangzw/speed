/**
 * SMSSignInForm behaviour: the phone step begins the sms login flow with
 * the country/local parts assembled into E.164 and the server's code
 * challenge flips the form to its code step (asserted on the request
 * bodies), the sent notice announces the assembled number, a pasted
 * international number switches the country and fills the local part, the
 * selector starts on the browser locale's region, the code answer takes
 * the ready step's result and fires onSignedIn (asserted on the store
 * token), a wrong code renders its text in one alert and keeps the code
 * step, resend repeats the send against the same number and clears the
 * code field, changing the phone returns to the first step, a number too
 * short for its country is refused locally naming the calling code, a
 * superseded code answer renders the used-code notice and refuses a
 * re-submission of that exact code locally, the en-US bundle renders on an
 * English-starting instance, and the tree passes axe. Text expectations
 * read the bundle values, never inline language.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '@speed/api-client'
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
import uiKitZhCN from '../../ui-kit/src/locales/zh-CN.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const CN_NATIONAL = '13800000001'
const CN_E164 = '+8613800000001'
const US_NATIONAL = '4155552671'
const US_E164 = '+14155552671'

/** The login-flow starts a case's own interaction makes. Discovery and the
 * proof-of-work challenge are separate calls, so they never inflate a
 * send count. */
function sends(harness: Harness) {
  return harness.calls.filter((call) => call.path === '/api/v1/auth/flows')
}
const UI_KIT_ZH = uiKitResources['zh-CN'] as {
  readonly phoneNumber: { readonly incomplete: string; readonly invalid: string }
  readonly form: { readonly required: string }
}

/** Pick a country in the selector, searching by its ISO code. */
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

/** The server's code challenge for the sms method, optionally stating how
 * long the client must wait before another code may be requested. */
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

/** The resend-countdown note's expected text for a whole second count. */
function resendNote(seconds: number): string {
  const [before = '', after = ''] = zhCN.smsSignIn.resendIn.split('{{seconds}}')
  return `${before}${seconds}${after}`
}

async function sendCode(national: string = CN_NATIONAL): Promise<void> {
  const user = userEvent.setup()
  await selectCountry(user, 'CN', '86')
  await user.type(screen.getByLabelText(zhCN.smsSignIn.phoneLabel), national)
  await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }))
}

/** A navigator.language override for the duration of one render. */
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
    expect(document.getElementById(SMS_NOTICE_ID)).toHaveTextContent(
      zhCN.smsSignIn.sentNotice.replace('{{phone}}', CN_E164),
    )
  })

  it('name no token delivery on the code request -- the session delivery is auth-core’s', async () => {
    // The phone step starts the flow; auth-core forces the cookie session
    // (`delivery: 'session'`), so the request names no delivery of its own.
    // A `'token'` here would be dead today and a live contradiction to the
    // cookie model the moment that override lifted.
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
    // The method answers a correct code with a ready step (taken with
    // `complete`), and that completion issues the tokens.
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

  it('disable the send button while the request is in flight', async () => {
    let resolveStart: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_START]: () =>
        new Promise((resolve) => {
          resolveStart = resolve
        }),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    const user = userEvent.setup()
    await selectCountry(user, 'CN', '86')
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
      CN_NATIONAL,
    )
    const send = screen.getByRole('button', { name: zhCN.smsSignIn.sendCode })
    await user.click(send)
    await waitFor(() => expect(send).toBeDisabled())
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
    // The server collapses a wrong, expired, spent or exhausted code into
    // this one answer, so the step says what it can: enter it again or ask
    // for a new code. It never names the password this surface has no field
    // for.
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.smsSignIn.loginFailed,
      ),
    )
    expect(screen.queryByText(zhCN.errors.authn.login_failed)).toBeNull()
    // The password channel's own label is the bundle's word for a
    // password; a code refusal must not name it.
    expect(screen.getByRole('alert')).not.toHaveTextContent(
      zhCN.passwordSignIn.passwordLabel,
    )
    expect(harness.store.get()).toBeNull()
    expect(screen.getByLabelText(zhCN.smsSignIn.codeLabel)).toBeInTheDocument()
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
    // A second factor the flow parks after the code is not this form's code
    // step: its refusal keeps the family's sentence for that step.
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
    // The second factor's own challenge renders through the shared step
    // renderer; answering it wrong keeps the generic sentence.
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
    // The refusal belongs to the field: focus lands on the number, and the
    // same message the control shows enters the form's status region, so a
    // viewer who submitted from the button hears it.
    await waitFor(() => expect(phone).toHaveFocus())
    await waitFor(() =>
      expect(document.getElementById(SMS_NOTICE_ID)).toHaveTextContent(message),
    )
    expect(sends(harness)).toHaveLength(0)
  })

  it('re-announce the same phone refusal on every refused submit', async () => {
    // A live region announces a text change, not a value written again. The
    // second submit repeats the same rejected number, so the region must
    // pass through empty between the two announcements for a screen reader
    // to speak the rejection again.
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
      // The first rejection filled the region once.
      expect(mutations.length).toBeGreaterThan(0)

      mutations.length = 0
      await user.click(submit)
      await waitFor(() => expect(region).toHaveTextContent(message))
      // Clearing the region and writing the message again are two changes:
      // at least a removal and a new insertion (or two text-node rewrites)
      // reached the DOM, so the repeated rejection is spoken again.
      expect(mutations.length).toBeGreaterThanOrEqual(2)
      expect(sends(harness)).toHaveLength(0)
    } finally {
      observer.disconnect()
    }
  })

  it('re-announce the sent notice when a resend of the same number succeeds', async () => {
    // A live region announces a text change, not a value written again. A
    // resend produces the same sentence as the first send, so the region
    // must pass through empty between the two announcements for a screen
    // reader to speak the successful resend, not only a refused one.
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const sent = zhCN.smsSignIn.sentNotice.replace('{{phone}}', CN_E164)
    const region = document.getElementById(SMS_NOTICE_ID)!
    await waitFor(() => expect(region).toHaveTextContent(sent))

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
      const user = userEvent.setup()
      await user.click(
        screen.getByRole('button', { name: zhCN.smsSignIn.resendCode }),
      )
      await waitFor(() => expect(sends(harness)).toHaveLength(2))
      // The same sentence is on screen again, and the region reached it
      // through an empty frame: a removal and a new insertion (or two text
      // rewrites), never one unchanged value.
      await waitFor(() => expect(region).toHaveTextContent(sent))
      expect(mutations.length).toBeGreaterThanOrEqual(2)
    } finally {
      observer.disconnect()
    }
  })

  it('return to the phone step when the flow is gone (flow-invalid)', async () => {
    // A step answered after the flow expired, was consumed, or was bound
    // to another browser answers flow-invalid: the flow cannot continue,
    // so the form returns to its first step to start a fresh one while
    // its banner carries the code.
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
    // The hint is on screen before a submit.
    expect(screen.getByText(zhCN.smsSignIn.phoneHint)).toBeInTheDocument()

    const user = userEvent.setup()
    await selectCountry(user, 'CN', '86')
    const phone = screen.getByLabelText(zhCN.smsSignIn.phoneLabel)
    await user.type(phone, '138000')
    await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }))

    // The message names the calling code the typed length fails under. It
    // appears under the field and in the form's status region (announced,
    // not visible), so the assertion allows both.
    await waitFor(() =>
      expect(
        screen.getAllByText(
          UI_KIT_ZH.phoneNumber.incomplete.replace('{{code}}', '86'),
        ).length,
      ).toBeGreaterThan(0),
    )
    expect(phone).toHaveAttribute('aria-invalid', 'true')
    // The invalid number never reaches the server, so its generic refusal
    // copy is not what the viewer meets.
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
    // Eleven digits is a possible CN length, but `12` starts no CN number
    // plan, so the number must be refused rather than sent.
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
    // The impossible number never reaches the server.
    expect(sends(harness)).toHaveLength(0)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('assemble a picked US number into E.164 as well', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    // jsdom starts on en-US, so the selector starts on the US.
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

    // China is selected and the local field holds the local part,
    // grouped as it would be if typed.
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
    await waitFor(() => expect(code).toHaveValue(''))
  })

  it('hold the resend control for the server interval and state why', async () => {
    // The send's challenge states the wait before another code may be
    // asked for: the resend control is disabled and says how long.
    const harness = makeHarness({
      [LOGIN_START]: () => codeChallenge(60),
    })
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const resend = await screen.findByRole('button', {
      name: zhCN.smsSignIn.resendCode,
    })
    expect(resend).toBeDisabled()
    expect(resend).toHaveAttribute('aria-describedby')
    expect(screen.getByText(resendNote(60))).toBeInTheDocument()
  })

  it('hold a throttled resend for the stated wait and release it when the wait ends', async () => {
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
    renderWithProviders(<SMSSignInForm session={harness.session} />)
    await sendCode()
    const resend = await screen.findByRole('button', {
      name: zhCN.smsSignIn.resendCode,
    })
    expect(resend).toBeEnabled()

    // The first send settled on the real clock; the resend's stated wait
    // runs on the fake one so the two seconds pass instantly.
    vi.useFakeTimers()
    try {
      await act(async () => {
        fireEvent.click(resend)
        for (let turn = 0; turn < 10; turn += 1) {
          await Promise.resolve()
        }
      })
      expect(resend).toBeDisabled()
      expect(screen.getByText(resendNote(2))).toBeInTheDocument()
      expect(sends(harness)).toHaveLength(2)
      // One tick before the stated wait it is still held, so an
      // implementation that waits less than the answer said cannot pass.
      act(() => {
        vi.advanceTimersByTime(1999)
      })
      expect(resend).toBeDisabled()
      // At the boundary it is usable again and the note is gone.
      act(() => {
        vi.advanceTimersByTime(1)
      })
      expect(resend).toBeEnabled()
      expect(screen.queryByText(resendNote(2))).toBeNull()
    } finally {
      vi.useRealTimers()
    }
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

  it('return to the phone step from a step this channel cannot render', async () => {
    // A redirect step is the host's to navigate, so the SMS form cannot
    // complete it: the notice carries the exit back to this channel's own
    // first step -- the phone number.
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
    // Resend and change-phone are compact action buttons; on a
    // finger-driven device each one gates up to the 44x44 floor.
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
    // A concurrent sign-in commits while this code answer is in flight:
    // auth-core rejects it as superseded, but its own 2xx spent the
    // single-use code. The form renders the used-code notice, clears the
    // code field, and refuses a re-submission of that exact code locally
    // rather than spending a round trip on the server's collapsed
    // wrong-code answer.
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
    // The winning sign-in commits while this answer is in flight.
    await act(async () => {
      await harness.session.answerFlow({ seq: 2, step: 'complete', input: {} })
    })
    // The released answer is this submit's own token-issuing completion:
    // it 2xx'd, but the winner committed first, so auth-core rejects it
    // as superseded -- which is exactly the case whose single-use code
    // was spent by this very answer.
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
    // Re-submitting the very code the superseded answer spent is refused
    // locally: the notice stays and no request goes out.
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
    // Rendered under the page h1 a real host page supplies: the form
    // renders no heading of its own, and page-has-heading-one is
    // determinate in jsdom now (see the axe helper header) -- a scan
    // without that context fails instead of passing by indeterminacy.
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
    // jsdom's browser locale is en-US (the US); the host default wins.
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
    // A standalone form has no presentational mode to report.
    expect(beforeStart).toHaveBeenCalledWith({ method: 'sms' })
    expect(sends(harness)).toHaveLength(0)
    // No code step opened and no error rendered: the attempt never left.
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
      <SMSSignInForm session={harness.session} beforeStart={beforeStart} />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
      US_NATIONAL,
    )
    const send = screen.getByRole('button', { name: zhCN.smsSignIn.sendCode })
    await user.click(send)
    await waitFor(() => expect(beforeStart).toHaveBeenCalledTimes(1))
    // The gate has not answered yet: the control is already busy, so a
    // second click cannot pass the same gate and start another flow.
    expect(send).toBeDisabled()
    expect(sends(harness)).toHaveLength(0)
    await act(async () => {
      release(true)
    })
    await waitFor(() =>
      expect(screen.getByLabelText(zhCN.smsSignIn.codeLabel)).toBeInTheDocument(),
    )
    // The flow began once, not once per click.
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
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(fake.widgets[0]?.siteKey).toBe('site-1')
      const send = screen.getByRole('button', { name: zhCN.smsSignIn.sendCode })
      // The gate requires a proof and the widget holds no token yet.
      expect(send).toBeDisabled()
      // A submit that bypasses the disabled control is still refused: the
      // handler checks the proof before it starts a flow.
      fireEvent.submit(send.closest('form')!)
      expect(sends(harness)).toHaveLength(0)
      // The widget yields its token: the same control now sends it.
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(send).toBeEnabled())
      await user.click(send)
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
    // The block solves on mount; the send waits for it.
    await waitFor(() =>
      expect(
        screen.getByText(uiKitZhCN.humanVerification.verified),
      ).toBeInTheDocument(),
    )
    await sendCode()
    await waitFor(() => expect(sends(harness)).toHaveLength(1))
    const body = sends(harness)[0]?.options?.body as
      | { readonly human_proof?: string }
      | undefined
    expect(body?.human_proof).toMatch(/^[A-Za-z0-9_-]+\.\d+$/)
  })

  it('clear the failure banner when a step after the code is answered again', async () => {
    // Each answer is a new attempt, including a parked second factor: the
    // previous refusal's banner goes when the next answer begins.
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
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('token-1')
      })
      await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }))
      // The send consumed the first token; the same block has reset and
      // takes another for the next send.
      const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
      act(() => {
        fake.token('token-2')
      })
      await user.type(code, '123456')
      await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.submit }))
      // The flow parks on the second factor; choosing the SMS factor sends
      // a code, so the answer carries the proof.
      await waitFor(() =>
        expect(
          screen.getByRole('button', { name: 'Phone' }),
        ).toBeInTheDocument(),
      )
      await user.click(screen.getByRole('button', { name: 'Phone' }))
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
