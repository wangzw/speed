/**
 * SMSSignInForm behaviour: the phone step begins the sms login flow and
 * the server's code challenge flips the form to its code step (asserted on
 * the request bodies), the sent notice announces the receiving number, the
 * code answer takes the ready step's result and fires onSignedIn (asserted
 * on the store token), a wrong code renders its text in one alert and
 * keeps the code step, resend repeats the send against the same number and
 * clears the code field, changing the phone returns to the first step, a
 * superseded code answer renders the used-code notice and refuses a
 * re-submission of that exact code locally, the en-US bundle renders on an
 * English-starting instance, and the tree passes axe. Text expectations
 * read the bundle values, never inline language.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SMSSignInForm } from './SMSSignInForm.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  LOGIN_ANSWER,
  LOGIN_START,
  PROFILE_GET,
  apiError,
  authenticatedCompletion,
  completeFlow,
  makeHarness,
  openFlow,
  principal,
} from '@speed/test-utils/session-harness'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const PHONE = '+8613800138000'

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

async function sendCode(phone: string = PHONE): Promise<void> {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText(zhCN.smsSignIn.phoneLabel), phone)
  await user.click(screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }))
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
    expect(harness.calls).toHaveLength(1)
    expect(harness.calls[0]?.method).toBe('POST')
    expect(harness.calls[0]?.path).toBe('/auth/flows')
    expect(harness.calls[0]?.options?.body).toMatchObject({
      method: 'sms',
      delivery: 'token',
      binding: 'cookie',
      input: { phone: PHONE },
    })
    expect(
      screen.getByRole('status'),
    ).toHaveTextContent(zhCN.smsSignIn.sentNotice.replace('{{phone}}', PHONE))
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
      .filter((call) => call.path === '/auth/flows/answer')
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
    await user.type(screen.getByLabelText(zhCN.smsSignIn.phoneLabel), PHONE)
    const send = screen.getByRole('button', { name: zhCN.smsSignIn.sendCode })
    await user.click(send)
    await waitFor(() => expect(send).toBeDisabled())
    resolveStart(codeChallenge())
    await waitFor(() =>
      expect(screen.getByLabelText(zhCN.smsSignIn.codeLabel)).toBeInTheDocument(),
    )
  })

  it('render a wrong-code answer in one alert and keep the code step', async () => {
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
        zhCN.errors.authn.login_failed,
      ),
    )
    expect(harness.store.get()).toBeNull()
    expect(screen.getByLabelText(zhCN.smsSignIn.codeLabel)).toBeInTheDocument()
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
    expect(harness.calls).toHaveLength(0)
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
    await waitFor(() => expect(harness.calls).toHaveLength(2))
    expect(harness.calls[1]?.options?.body).toMatchObject({
      method: 'sms',
      input: { phone: PHONE },
    })
    await waitFor(() => expect(code).toHaveValue(''))
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
        harness.calls.filter((call) => call.path === '/auth/flows/answer'),
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
      screen.getByLabelText(enUS.smsSignIn.phoneLabel),
    ).toBeInTheDocument()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(enUS.smsSignIn.phoneLabel), PHONE)
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
})
