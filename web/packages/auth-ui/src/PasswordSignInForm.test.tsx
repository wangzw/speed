import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
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
    expect(harness.calls[1]?.options?.body).toMatchObject({
      step: 'complete',
    })
    expect(harness.store.get()).toBe('access-1')
  })

  it('name no token delivery on the login start -- the session delivery is auth-core’s', async () => {
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

  it('mark the submit button busy while the login is in flight, idle after', async () => {
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
    await waitFor(() =>
      expect(submit).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(submit).toHaveAttribute('aria-busy', 'true')
    resolveStart(completeFlow(1))
    await waitFor(() =>
      expect(submit).not.toHaveAttribute('aria-disabled', 'true'),
    )
    expect(submit).not.toHaveAttribute('aria-busy', 'true')
  })

  it('keep focus on the submit button while the login is in flight and after a refusal lands', async () => {
    let rejectStart: (reason: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_START]: () =>
        new Promise((_resolve, reject) => {
          rejectStart = reject
        }),
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
    await waitFor(() => expect(harness.calls).toHaveLength(1))

    expect(submit).toHaveFocus()
    expect(submit).toHaveAttribute('aria-busy', 'true')

    await act(async () => {
      rejectStart(apiError(429, 'client.http.429'))
    })
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(submit).not.toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveFocus()
  })

  it('refuse an implicit resubmission while the login is in flight', async () => {
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
    await waitFor(() => expect(harness.calls).toHaveLength(1))

    const form = submit.closest('form')
    expect(form).not.toBeNull()
    fireEvent.submit(form as HTMLFormElement)
    await act(async () => {})

    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows'),
    ).toHaveLength(1)

    await act(async () => {
      resolveStart(completeFlow(1))
    })
    await waitFor(() =>
      expect(submit).not.toHaveAttribute('aria-disabled', 'true'),
    )
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
    expect(message).not.toContain(zhCN.smsSignIn.codeLabel)
    const password = screen.getByLabelText(zhCN.passwordSignIn.passwordLabel)
    await waitFor(() => expect(password).toHaveAttribute('aria-invalid', 'true'))
    const description = screen.getByText(message)
    expect(description.id).not.toBe('')
    expect(password.getAttribute('aria-describedby')).toContain(description.id)
    expect(password).toHaveFocus()
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

  it('register a required factor, prove the password again, and finish with the new factor', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'enroll',
          next: { kind: 'enroll', kinds: ['totp'] },
        }),
      [LOGIN_ANSWER]: (call) => {
        const body = call.options?.body as {
          step?: string
          input?: Record<string, string>
        }
        const input = body.input ?? {}
        switch (body.step) {
          case 'enroll':
            return input.code === undefined
              ? openFlow({
                  seq: 2,
                  state: 'enroll',
                  next: {
                    kind: 'webauthn',
                    options: { otpauth_uri: OTPAUTH_URI },
                  },
                })
              : openFlow({
                  seq: 3,
                  state: 'reprove',
                  next: {
                    kind: 'challenge',
                    fields: [
                      { name: 'identifier', kind: 'text', required: true },
                      {
                        name: 'password',
                        kind: 'password',
                        required: true,
                        secret: true,
                      },
                    ],
                  },
                })
          case 'answer':
            return openFlow({
              seq: 4,
              state: 'second',
              next: {
                kind: 'factor',
                factors: [{ id: 'f1', kind: 'totp' }],
              },
            })
          case 'factor':
            return input.code === undefined
              ? openFlow({
                  seq: 5,
                  state: 'second',
                  next: {
                    kind: 'challenge',
                    fields: [{ name: 'code', kind: 'code', required: true }],
                  },
                })
              : completeFlow(6)
          case 'complete':
            return authenticatedCompletion()
          default:
            throw new Error(`unexpected step ${String(body.step)}`)
        }
      },
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <PasswordSignInForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    const user = userEvent.setup()
    await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.signInFlow.factorKind.totp,
      }),
    )
    await user.type(
      await screen.findByLabelText(zhCN.signInFlow.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )

    await screen.findByLabelText(zhCN.passwordSignIn.identifierLabel)
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      'alice@example.com',
    )
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      's3cret-pass',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.signInFlow.factorKind.totp,
      }),
    )
    await user.type(
      await screen.findByLabelText(zhCN.smsSignIn.codeLabel),
      '654321',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )

    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(harness.store.get()).toBe('access-1')
    const answerBodies = harness.calls
      .filter((call) => call.path === '/api/v1/auth/flows/answer')
      .map((call) => call.options?.body)
    expect(answerBodies.map((body) => (body as { step?: string }).step)).toEqual([
      'enroll',
      'enroll',
      'answer',
      'factor',
      'factor',
      'complete',
    ])
    expect(answerBodies[1]).toMatchObject({
      step: 'enroll',
      input: { kind: 'totp', code: '123456' },
    })
    expect(answerBodies[2]).toMatchObject({
      step: 'answer',
      input: { identifier: 'alice@example.com', password: 's3cret-pass' },
    })
    expect(answerBodies[3]).toMatchObject({
      step: 'factor',
      input: { factor: 'f1' },
    })
    expect(answerBodies[4]).toMatchObject({
      step: 'factor',
      input: { factor: 'f1', code: '654321' },
    })
  })

  it('return to the credentials fields from a step this channel cannot render', async () => {
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
    renderWithProviders(
      <div>
        <h1>Sign in</h1>
        <PasswordSignInForm session={harness.session} />
      </div>,
    )
    await expectNoAxeViolations()
  })

  it('contain a throwing onSignedIn: the committed login never looks like a failure', async () => {
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
    await waitFor(() =>
      expect(submit).toHaveAttribute('aria-disabled', 'true'),
    )
    await act(async () => {
      await harness.session.answerFlow({ seq: 1, step: 'complete', input: {} })
    })
    expect(harness.store.get()).toBe('access-race')
    await act(async () => {
      releaseFirst(authenticatedCompletion())
    })
    await waitFor(() =>
      expect(submit).not.toHaveAttribute('aria-disabled', 'true'),
    )
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
