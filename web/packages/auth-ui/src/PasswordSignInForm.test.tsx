/**
 * PasswordSignInForm behaviour: the happy path begins a password login
 * flow with the entered identifier and password, takes the ready step's
 * result and fires onSignedIn (scripted through the bindRequestFn harness,
 * asserted on the request bodies and the store token), a busy submit
 * disables the button for the flight's duration, a login-failed answer
 * attaches the password channel's own text to the password field (invalid,
 * described by the message, focused) and changes nothing, an empty
 * submit never reaches the network, a forced password change renders the
 * shared step renderer's new-password field and completes the flow, the
 * en-US bundle renders on an English-starting instance, and the tree
 * passes axe. Text expectations read the bundle values, never inline
 * language.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PasswordSignInForm } from './PasswordSignInForm.js'
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
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const SUBMIT_ZH = zhCN.passwordSignIn.submit
const SUBMIT_EN = enUS.passwordSignIn.submit

interface SignInLabels {
  readonly identifierLabel: string
  readonly passwordLabel: string
}

async function fillAndSubmit(
  identifier: string,
  password: string,
  labels: SignInLabels,
  submitName: string = SUBMIT_ZH,
) {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText(labels.identifierLabel), identifier)
  await user.type(screen.getByLabelText(labels.passwordLabel), password)
  await user.click(screen.getByRole('button', { name: submitName }))
}

const ZH_LABELS: SignInLabels = {
  identifierLabel: zhCN.passwordSignIn.identifierLabel,
  passwordLabel: zhCN.passwordSignIn.passwordLabel,
}

/** Installs a `navigator.credentials` stub, answering the restorer. */
function installCredentials(credentials: unknown): () => void {
  const original = Object.getOwnPropertyDescriptor(
    window.navigator,
    'credentials',
  )
  Object.defineProperty(window.navigator, 'credentials', {
    value: credentials,
    configurable: true,
  })
  return () => {
    if (original === undefined) {
      delete (window.navigator as { credentials?: unknown }).credentials
    } else {
      Object.defineProperty(window.navigator, 'credentials', original)
    }
  }
}

const OTPAUTH_URI =
  'otpauth://totp/Example%20Co:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example%20Co'

describe('PasswordSignInForm', () => {
  it('begin a password flow with the entered credentials and fire onSignedIn on completion', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <PasswordSignInForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(harness.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
      'POST /api/v1/auth/flows',
      'POST /api/v1/auth/flows/answer',
      'GET /api/v1/auth/me',
      'GET /api/v1/features',
    ])
    expect(harness.calls[0]?.options?.body).toMatchObject({
      method: 'password',
      delivery: 'session',
      binding: 'cookie',
      input: { identifier: 'alice@example.com', password: 's3cret-pass' },
    })
    // The ready step's result is taken with step: 'complete'.
    expect(harness.calls[1]?.options?.body).toMatchObject({
      step: 'complete',
    })
    expect(harness.store.get()).toBe('access-1')
  })

  it('name no token delivery on the login start -- the session delivery is auth-core’s', async () => {
    // auth-core forces the cookie session (`delivery: 'session'`) on
    // startFlow, so this surface names no delivery of its own. A `'token'`
    // here would be dead today (masked by that override) and a live
    // contradiction to the cookie model the moment the override lifted.
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const startFlow = vi.spyOn(harness.session, 'startFlow')
    renderWithProviders(<PasswordSignInForm session={harness.session} />)
    await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)
    await waitFor(() => expect(startFlow).toHaveBeenCalledTimes(1))
    expect(startFlow.mock.calls[0]?.[0]?.delivery).not.toBe('token')
  })

  it('disable the submit button while the login is in flight, re-enable after', async () => {
    let resolveStart: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_START]: () =>
        new Promise((resolve) => {
          resolveStart = resolve
        }),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    renderWithProviders(<PasswordSignInForm session={harness.session} />)
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      'alice@example.com',
    )
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      's3cret-pass',
    )
    const submit = screen.getByRole('button', { name: SUBMIT_ZH })
    await user.click(submit)
    await waitFor(() => expect(submit).toBeDisabled())
    resolveStart(completeFlow(1))
    await waitFor(() => expect(submit).toBeEnabled())
  })

  it('attach a login-failed refusal to the password field and focus it', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(<PasswordSignInForm session={harness.session} />)
    await fillAndSubmit('alice@example.com', 'wrong-password', ZH_LABELS)

    const message = zhCN.passwordSignIn.loginFailed
    // This surface has no code field, so its refusal never names one: the
    // code label the SMS step uses is the bundle's own word for it.
    expect(message).not.toContain(zhCN.smsSignIn.codeLabel)
    const password = screen.getByLabelText(zhCN.passwordSignIn.passwordLabel)
    await waitFor(() => expect(password).toHaveAttribute('aria-invalid', 'true'))
    // The message is the field's description, and the field holds focus, so
    // the refusal is announced with the field it is about.
    const description = screen.getByText(message)
    expect(description.id).not.toBe('')
    expect(password.getAttribute('aria-describedby')).toContain(description.id)
    expect(password).toHaveFocus()
    // The refusal is the field's, not a second whole-attempt banner.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(harness.store.get()).toBeNull()
    expect(harness.calls).toHaveLength(1)
  })

  it('clear a whole-attempt banner when the viewer begins a new attempt', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => {
        throw apiError(429, 'client.http.429')
      },
    })
    renderWithProviders(<PasswordSignInForm session={harness.session} />)
    await fillAndSubmit('alice@example.com', 'wrong-password', ZH_LABELS)

    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())

    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      'x',
    )
    await waitFor(() =>
      expect(screen.queryByRole('alert')).not.toBeInTheDocument(),
    )
  })

  it('advertise only the identifiers a password login accepts', () => {
    // The account contract defines a password login name as a username or
    // email; a phone is not one (it is the SMS channel's identifier), and a
    // phone plus a password always ends in the collapsed refusal. The
    // identifier label and the refusal copy must not offer the SMS
    // channel's phone label.
    expect(zhCN.passwordSignIn.identifierLabel).not.toContain(
      zhCN.smsSignIn.phoneLabel,
    )
    expect(zhCN.passwordSignIn.loginFailed).not.toContain(
      zhCN.smsSignIn.phoneLabel,
    )
    expect(enUS.passwordSignIn.identifierLabel).not.toMatch(/phone/i)
    expect(enUS.passwordSignIn.loginFailed).not.toMatch(/phone/i)
  })

  it('never reach the network for an empty submit', async () => {
    const harness = makeHarness({})
    renderWithProviders(<PasswordSignInForm session={harness.session} />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: SUBMIT_ZH }))
    await waitFor(() =>
      expect(screen.queryByRole('alert')).not.toBeInTheDocument(),
    )
    expect(harness.calls).toHaveLength(0)
  })

  it('render a forced password change step and complete the flow through it', async () => {
    // The password method answers a must-change account with a challenge
    // step asking for a new password; the shared step renderer renders it
    // and the answer advances the same flow to completion.
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'new_password', kind: 'password', secret: true }],
          },
        }),
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
      <PasswordSignInForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    await fillAndSubmit('alice@example.com', 'temp-pass', ZH_LABELS)
    const newPassword = await screen.findByLabelText(
      zhCN.passwordSignIn.newPasswordLabel,
    )
    await userEvent.setup().type(newPassword, 'brand-new-pass')
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    const answerBodies = harness.calls
      .filter((call) => call.path === '/api/v1/auth/flows/answer')
      .map((call) => call.options?.body)
    expect(answerBodies[0]).toMatchObject({
      step: 'answer',
      input: { new_password: 'brand-new-pass' },
    })
    expect(answerBodies[1]).toMatchObject({ step: 'complete' })
    expect(harness.store.get()).toBe('access-1')
  })

  it('return to the credentials fields from a step this channel cannot render', async () => {
    // A redirect step belongs to the host's navigation, so a channel form
    // cannot complete it: the notice carries an exit back to this
    // channel's own first fields instead of leaving the viewer stranded.
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'redirect',
            url: 'https://idp.example/authorize',
            method: 'oidc',
          },
        }),
    })
    renderWithProviders(<PasswordSignInForm session={harness.session} />)
    await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)

    expect(
      await screen.findByText(zhCN.signInFlow.unsupportedStep),
    ).toBeInTheDocument()
    await userEvent.setup().click(
      screen.getByRole('button', {
        name: zhCN.signInFlow.unsupportedStepAction,
      }),
    )

    expect(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
    expect(screen.queryByText(zhCN.signInFlow.unsupportedStep)).toBeNull()
  })

  it('render the en-US bundle on an English-starting instance', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    renderWithProviders(
      <PasswordSignInForm session={harness.session} />,
      { language: 'en-US' },
    )
    expect(
      screen.getByLabelText(enUS.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(enUS.passwordSignIn.passwordLabel),
    ).toBeInTheDocument()
    await fillAndSubmit('alice@example.com', 's3cret-pass', {
      identifierLabel: enUS.passwordSignIn.identifierLabel,
      passwordLabel: enUS.passwordSignIn.passwordLabel,
    }, SUBMIT_EN)
    await waitFor(() =>
      expect(
        harness.calls.filter((call) => call.path === '/api/v1/auth/flows'),
      ).toHaveLength(1),
    )
  })

  it('pass axe with no violations', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    // Rendered under the page h1 a real host page supplies: the form
    // renders no heading of its own (the page above it is host content),
    // and page-has-heading-one is determinate in jsdom now (see the axe
    // helper header) -- a scan without that context fails instead of
    // passing by indeterminacy.
    renderWithProviders(
      <div>
        <h1>Sign in</h1>
        <PasswordSignInForm session={harness.session} />
      </div>,
    )
    await expectNoAxeViolations()
  })

  it('contain a throwing onSignedIn: the committed login never looks like a failure', async () => {
    // A host callback that throws is not a login failure (the login
    // committed -- the store holds the issued token): it must not paint
    // the failure banner, and the containment keeps the throw out of the
    // submit promise as an unhandled rejection.
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn(() => {
      throw new Error('host navigation failed')
    })
    renderWithProviders(
      <PasswordSignInForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(harness.store.get()).toBe('access-1')
  })

  it('treat a superseded submit as the lost race it is, rendering no error', async () => {
    // A concurrent sign-in commits to the session while this form's own
    // login is still in flight, so auth-core rejects this submit with
    // OperationSupersededError when its answer arrives: the losing submit
    // must not render the generic failure banner -- it is not a failure,
    // and the session being authenticated now is the host's own snapshot
    // to observe.
    let releaseFirst: (value: unknown) => void = () => {}
    const firstGate = new Promise((resolve) => {
      releaseFirst = resolve
    })
    let answers = 0
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => {
        answers += 1
        return answers === 1
          ? firstGate
          : authenticatedCompletion({ access_token: 'access-race' })
      },
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <PasswordSignInForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      'alice@example.com',
    )
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      's3cret-pass',
    )
    const submit = screen.getByRole('button', { name: SUBMIT_ZH })
    await user.click(submit)
    await waitFor(() => expect(submit).toBeDisabled())
    // The winning sign-in commits while the first answer is in flight.
    await act(async () => {
      await harness.session.answerFlow({ seq: 1, step: 'complete', input: {} })
    })
    expect(harness.store.get()).toBe('access-race')
    // The password answer arrives after the winner committed: auth-core
    // rejects it as superseded, and the form treats that as the lost race
    // it is -- quiet, no error banner.
    await act(async () => {
      releaseFirst(authenticatedCompletion())
    })
    await waitFor(() => expect(submit).toBeEnabled())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(onSignedIn).not.toHaveBeenCalled()
    expect(harness.store.get()).toBe('access-race')
  })

  it('consult the host’s start gate before beginning: a refusal sends nothing', async () => {
    const harness = makeHarness({})
    const beforeStart = vi.fn(() => false)
    renderWithProviders(
      <PasswordSignInForm session={harness.session} beforeStart={beforeStart} />,
    )
    await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)
    await waitFor(() => expect(beforeStart).toHaveBeenCalledTimes(1))
    // A standalone form has no presentational mode to report.
    expect(beforeStart).toHaveBeenCalledWith({ method: 'password' })
    expect(harness.calls).toHaveLength(0)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('carry the human proof on an SMS second-factor answer', async () => {
    const fake = installFakeTurnstile()
    try {
      const harness = makeHarness({
        [LOGIN_START]: () =>
          openFlow({
            seq: 1,
            state: 'second',
            next: {
              kind: 'factor',
              factors: [{ id: 'mfa-1', kind: 'sms' }],
            },
          }),
        [LOGIN_ANSWER]: () =>
          openFlow({
            seq: 2,
            state: 'second',
            next: {
              kind: 'challenge',
              fields: [{ name: 'code', kind: 'code' }],
            },
          }),
      })
      renderWithProviders(
        <PasswordSignInForm
          session={harness.session}
          humanGate={{
            phone: { required: true, provider: 'turnstile', site_key: 'site-1' },
            other: { required: true, provider: 'turnstile', site_key: 'site-1' },
          }}
        />,
      )
      const user = userEvent.setup()
      await user.type(
        screen.getByLabelText(ZH_LABELS.identifierLabel),
        'alice@example.com',
      )
      await user.type(
        screen.getByLabelText(ZH_LABELS.passwordLabel),
        's3cret-pass',
      )
      await user.click(screen.getByRole('button', { name: SUBMIT_ZH }))
      // The flow parks on an SMS second factor. Choosing that factor is an
      // answer that may send, so the choice is held while the gate is read
      // and its proof obtained; the block appears only for that held answer.
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.factorKind.sms }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(fake.widgets[0]?.siteKey).toBe('site-1')
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() =>
        expect(
          harness.calls.find(
            (call) =>
              call.path === '/api/v1/auth/flows/answer' &&
              (call.options?.body as { step?: string }).step === 'factor',
          ),
        ).toBeDefined(),
      )
      const factorAnswer = harness.calls.find(
        (call) =>
          call.path === '/api/v1/auth/flows/answer' &&
          (call.options?.body as { step?: string }).step === 'factor',
      )
      expect(factorAnswer?.options?.body).toMatchObject({
        input: { factor: 'mfa-1' },
        human_proof: 'token-1',
      })
    } finally {
      fake.restore()
    }
  })

  it('render an SMS second factor refused for a missing phone with its own sentence', async () => {
    // The account's phone was removed elsewhere while the sign-in was in
    // flight: choosing the SMS factor cannot send, and the refusal names the
    // missing verified phone instead of falling back to the unknown sentence.
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'mfa-1', kind: 'sms' }],
          },
        }),
      [LOGIN_ANSWER]: () => {
        throw apiError(403, 'authn.phone_required')
      },
    })
    renderWithProviders(<PasswordSignInForm session={harness.session} />)
    await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)
    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.signInFlow.factorKind.sms,
      }),
    )
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.phone_required)
    expect(alert).not.toHaveTextContent(zhCN.errors.unknown)
  })

  it('clear the failure banner when a parked step is answered again', async () => {
    // Answering a step is a new attempt: the previous refusal's banner must
    // not stay beside the step the next answer parks.
    let answers = 0
    const challenge = openFlow({
      seq: 1,
      state: 'second',
      next: {
        kind: 'challenge',
        fields: [{ name: 'code', kind: 'code', required: true }],
      },
    })
    const harness = makeHarness({
      [LOGIN_START]: () => challenge,
      [LOGIN_ANSWER]: () => {
        answers += 1
        if (answers === 1) {
          throw apiError(401, 'authn.login_failed')
        }
        return openFlow({
          seq: 3,
          state: 'second',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', required: true }],
          },
        })
      },
    })
    renderWithProviders(<PasswordSignInForm session={harness.session} />)
    await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)
    const user = userEvent.setup()
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(code, '000000')
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

  it('render the TOTP enrollment as a QR symbol, never as text', async () => {
    // The enrollment step's otpauth_uri feeds the shared enrolment panel:
    // the viewer scans the symbol or enters the code by hand, and the URI
    // itself is never spilled into the page.
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'enroll',
          next: {
            kind: 'webauthn',
            options: { otpauth_uri: OTPAUTH_URI },
          },
        }),
    })
    renderWithProviders(<PasswordSignInForm session={harness.session} />)
    await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)

    expect(
      await screen.findByRole('img', { name: zhCN.signInFlow.totpQrLabel }),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.signInFlow.codeLabel),
    ).toBeInTheDocument()
    expect(screen.queryByText(/otpauth:\/\//)).toBeNull()
  })

  it('word an already-registered passkey as its own remedy', async () => {
    // The creation ceremony rejects with InvalidStateError when the
    // authenticator already holds one of the account's excluded credentials;
    // the banner names the remedy instead of the generic failure.
    const create = vi
      .fn()
      .mockRejectedValue(
        new DOMException(
          'the authenticator already holds this credential',
          'InvalidStateError',
        ),
      )
    const restore = installCredentials({ create, get: vi.fn() })
    try {
      const harness = makeHarness({
        [LOGIN_START]: () =>
          openFlow({
            seq: 1,
            state: 'enroll',
            next: {
              kind: 'webauthn',
              options: {
                challenge: 'Y2hhbGxlbmdl',
                user: { id: 'AQID', name: 'ada' },
              },
            },
          }),
      })
      renderWithProviders(<PasswordSignInForm session={harness.session} />)
      await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)
      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))

      expect(await screen.findByRole('alert')).toHaveTextContent(
        zhCN.signInFlow.passkeyAlreadyRegistered,
      )
    } finally {
      restore()
    }
  })

  it('name the ceremony a generic passkey failure interrupted', async () => {
    // A registration and a sign-in that fail the same way read differently:
    // the sentence names the ceremony that did not complete.
    const boom = (): Promise<never> => Promise.reject(new Error('boom'))
    const restore = installCredentials({
      create: vi.fn().mockImplementation(boom),
      get: vi.fn().mockImplementation(boom),
    })
    try {
      const registerHarness = makeHarness({
        [LOGIN_START]: () =>
          openFlow({
            seq: 1,
            state: 'enroll',
            next: {
              kind: 'webauthn',
              options: {
                challenge: 'Y2hhbGxlbmdl',
                user: { id: 'AQID', name: 'ada' },
              },
            },
          }),
      })
      const register = renderWithProviders(
        <PasswordSignInForm session={registerHarness.session} />,
      )
      await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)
      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))
      expect(await screen.findByRole('alert')).toHaveTextContent(
        zhCN.signInFlow.passkeyFailedRegister,
      )
      register.unmount()

      // An assertion step starts its ceremony on arrival; a passkey second
      // factor the password flow parks on is that sign-in.
      const signInHarness = makeHarness({
        [LOGIN_START]: () =>
          openFlow({
            seq: 1,
            state: 'second',
            next: {
              kind: 'webauthn',
              options: { challenge: 'Y2hhbGxlbmdl' },
            },
          }),
      })
      renderWithProviders(
        <PasswordSignInForm session={signInHarness.session} />,
      )
      await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)
      expect(await screen.findByRole('alert')).toHaveTextContent(
        zhCN.signInFlow.passkeyFailedSignIn,
      )
    } finally {
      restore()
    }
  })
})
