import { describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '@speed/api-client'
import { PasswordRegisterForm } from './PasswordRegisterForm.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
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
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const REGISTER = 'POST /api/v1/auth/register'
const USERNAME = 'ada'
const MAIL = 'ada@example.com'
const PASSWORD = 's3cret-pass'

function waitSentence(seconds: number): string {
  const [before = '', after = ''] = zhCN.throttle.retryAfter.split('{{seconds}}')
  return `${before}${seconds}${after}`
}

function visibleSentence(alert: HTMLElement): HTMLElement | null {
  return alert.querySelector<HTMLElement>('span[aria-hidden="true"]')
}

function stableSentence(alert: HTMLElement): HTMLElement | null {
  return alert.querySelector<HTMLElement>('span:not([aria-hidden])')
}

interface RegisterLabels {
  readonly username: string
  readonly displayName: string
  readonly email: string
  readonly password: string
  readonly confirmPassword: string
}

const LABELS: RegisterLabels = {
  username: zhCN.register.usernameLabel,
  displayName: zhCN.register.displayNameLabel,
  email: zhCN.register.emailLabel,
  password: zhCN.register.passwordLabel,
  confirmPassword: zhCN.register.confirmPasswordLabel,
}

function registration(): unknown {
  return {
    state: 'registered',
    account_id: 'account-1',
    verification: { state: 'pending', email: MAIL },
  }
}

interface RegisterValues {
  readonly username?: string
  readonly displayName?: string
  readonly email?: string
  readonly password?: string
  readonly confirmPassword?: string
}

function registerCalls(harness: ReturnType<typeof makeHarness>) {
  return harness.calls.filter((call) => call.path === '/api/v1/auth/register')
}

function loginStartCalls(harness: ReturnType<typeof makeHarness>) {
  return harness.calls.filter(
    (call) => `${call.method} ${call.path}` === LOGIN_START,
  )
}

async function fillFields(values: RegisterValues = {}): Promise<void> {
  const user = userEvent.setup()
  await user.type(
    screen.getByLabelText(LABELS.username),
    values.username ?? USERNAME,
  )
  if (values.displayName !== undefined) {
    await user.type(
      screen.getByLabelText(LABELS.displayName),
      values.displayName,
    )
  }
  await user.type(screen.getByLabelText(LABELS.email), values.email ?? MAIL)
  await user.type(
    screen.getByLabelText(LABELS.password),
    values.password ?? PASSWORD,
  )
  await user.type(
    screen.getByLabelText(LABELS.confirmPassword),
    values.confirmPassword ?? values.password ?? PASSWORD,
  )
}

async function fillAndSubmit(values: RegisterValues = {}): Promise<void> {
  await fillFields(values)
  await userEvent
    .setup()
    .click(screen.getByRole('button', { name: zhCN.register.submit }))
}

describe('PasswordRegisterForm', () => {
  it('open an account, then sign in with the same credentials', async () => {
    const harness = makeHarness({
      [REGISTER]: (call) => {
        expect(call.options?.body).toEqual({
          username: USERNAME,
          email: MAIL,
          password: PASSWORD,
        })
        return registration()
      },
      [LOGIN_START]: (call) => {
        expect(call.options?.body).toMatchObject({
          method: 'password',
          input: { identifier: USERNAME, password: PASSWORD },
        })
        return completeFlow(1)
      },
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <PasswordRegisterForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    await fillAndSubmit()

    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(onSignedIn).toHaveBeenCalledWith({
      mode: 'register',
      channel: 'password',
    })
    expect(screen.getByRole('status')).toHaveTextContent(zhCN.register.signedIn)
    expect(screen.queryByText(zhCN.register.signInRequired)).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.register.retrySignIn }),
    ).not.toBeInTheDocument()
    expect(screen.queryByLabelText(LABELS.username)).not.toBeInTheDocument()
    expect(harness.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
      'POST /api/v1/auth/register',
      'POST /api/v1/auth/flows',
      'POST /api/v1/auth/flows/answer',
      'GET /api/v1/auth/me',
      'GET /api/v1/features',
    ])
    expect(harness.calls[0]?.options?.body).not.toHaveProperty('confirmPassword')
    expect(harness.store.get()).toBe('access-1')
  })

  it('send the display name trimmed when one is given', async () => {
    const harness = makeHarness({
      [REGISTER]: (call) => {
        expect(call.options?.body).toEqual({
          username: USERNAME,
          email: MAIL,
          password: PASSWORD,
          display_name: 'Ada Lovelace',
        })
        return registration()
      },
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <PasswordRegisterForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    await fillAndSubmit({ displayName: '  Ada Lovelace  ' })

    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(
      harness.calls.find((call) => call.path === '/api/v1/auth/register')
        ?.options?.body,
    ).toHaveProperty('display_name', 'Ada Lovelace')
  })

  it('omit the display name key when the field is left blank', async () => {
    const harness = makeHarness({
      [REGISTER]: (call) => {
        expect(call.options?.body).not.toHaveProperty('display_name')
        return registration()
      },
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <PasswordRegisterForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    await fillAndSubmit({ displayName: '   ' })

    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(
      harness.calls.find((call) => call.path === '/api/v1/auth/register')
        ?.options?.body,
    ).not.toHaveProperty('display_name')
  })

  it('render the display-name label and help from the bundle', async () => {
    const harness = makeHarness({})
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)

    expect(screen.getByLabelText(zhCN.register.displayNameLabel)).toBeInTheDocument()
    expect(screen.getByText(zhCN.register.displayNameHelp)).toBeInTheDocument()
  })

  it('refuse a confirmation that differs without reaching the network', async () => {
    const harness = makeHarness({})
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit({ confirmPassword: 'a-different-pass' })

    expect(
      await screen.findByText(zhCN.register.passwordMismatch),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(LABELS.confirmPassword),
    ).toHaveAttribute('aria-invalid', 'true')
    expect(harness.calls).toHaveLength(0)
  })

  it('refuse a malformed email without reaching the network', async () => {
    const harness = makeHarness({})
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit({ email: 'ada@example@com' })

    expect(
      await screen.findByText(zhCN.register.invalidEmail),
    ).toBeInTheDocument()
    expect(screen.getByLabelText(LABELS.email)).toHaveAttribute(
      'aria-invalid',
      'true',
    )
    expect(harness.calls).toHaveLength(0)
  })

  it.each([
    ['authn.username_taken', 409, zhCN.register.usernameTaken],
    ['authn.username_reserved', 400, zhCN.register.usernameReserved],
    ['authn.username_invalid', 400, zhCN.register.usernameInvalid],
  ])(
    'attach %s to the username field and focus it',
    async (code, status, text) => {
      const harness = makeHarness({
        [REGISTER]: () => {
          throw apiError(status, code)
        },
      })
      renderWithProviders(<PasswordRegisterForm session={harness.session} />)
      await fillAndSubmit()

      const username = screen.getByLabelText(LABELS.username)
      await waitFor(() =>
        expect(username).toHaveAttribute('aria-invalid', 'true'),
      )
      const message = screen.getByText(text)
      expect(message.id).not.toBe('')
      expect(username.getAttribute('aria-describedby')).toContain(message.id)
      expect(username).toHaveFocus()
      expect(username).toHaveValue(USERNAME)
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(harness.calls).toHaveLength(1)
    },
  )

  it('attach a taken email to the email field and focus it', async () => {
    const harness = makeHarness({
      [REGISTER]: () => {
        throw apiError(409, 'authn.email_taken')
      },
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit()

    const email = screen.getByLabelText(LABELS.email)
    await waitFor(() => expect(email).toHaveAttribute('aria-invalid', 'true'))
    const message = screen.getByText(zhCN.register.emailTaken)
    expect(message.id).not.toBe('')
    expect(email.getAttribute('aria-describedby')).toContain(message.id)
    expect(email).toHaveFocus()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByLabelText(LABELS.username)).toHaveAttribute(
      'aria-invalid',
      'false',
    )
    expect(harness.calls).toHaveLength(1)
  })

  it.each([
    ['authn.signup_closed', 403, zhCN.register.signupClosed],
    ['authn.throttled', 429, zhCN.register.throttled],
    ['authn.unavailable', 503, zhCN.register.unavailable],
  ])(
    'render %s as its register-section text',
    async (code, status, text) => {
      const harness = makeHarness({
        [REGISTER]: () => {
          throw apiError(status, code)
        },
      })
      renderWithProviders(<PasswordRegisterForm session={harness.session} />)
      await fillAndSubmit()

      expect(await screen.findByRole('alert')).toHaveTextContent(text)
      expect(harness.calls).toHaveLength(1)
      expect(
        screen.getByRole('button', { name: zhCN.register.submit }),
      ).not.toHaveAttribute('aria-disabled', 'true')
    },
  )

  it('render a 400 through the generic bad-request text, not a policy claim', async () => {
    const harness = makeHarness({
      [REGISTER]: () => {
        throw apiError(400, 'authn.bad_request')
      },
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit()

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.bad_request,
    )
    expect(harness.calls).toHaveLength(1)
  })

  it('render a refusal with no register-section text through the shared banner', async () => {
    const harness = makeHarness({
      [REGISTER]: () => {
        throw apiError(409, 'authn.conflict')
      },
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit()

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.conflict,
    )
    expect(harness.calls).toHaveLength(1)
  })

  it('keep the submit focused and busy while the registration is in flight', async () => {
    let resolveRegister: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [REGISTER]: () =>
        new Promise((resolve) => {
          resolveRegister = resolve
        }),
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit()
    const submit = screen.getByRole('button', { name: zhCN.register.submit })
    await waitFor(() => expect(registerCalls(harness)).toHaveLength(1))
    expect(submit).toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toBeDisabled()
    expect(submit).toHaveFocus()
    resolveRegister(registration())
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.register.signedIn,
      ),
    )
  })

  it('refuse a second submission while the registration is in flight', async () => {
    let resolveRegister: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [REGISTER]: () =>
        new Promise((resolve) => {
          resolveRegister = resolve
        }),
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit()
    const submit = screen.getByRole('button', { name: zhCN.register.submit })
    await waitFor(() => expect(registerCalls(harness)).toHaveLength(1))
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    const form = submit.closest('form')
    expect(form).not.toBeNull()
    fireEvent.submit(form as HTMLFormElement)
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/register'),
    ).toHaveLength(1)
    await act(async () => {
      resolveRegister(registration())
    })
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.register.signedIn,
      ),
    )
  })

  it('hold the submit unavailable for a stated wait, with the wait as its description', async () => {
    const harness = makeHarness({
      [REGISTER]: () => {
        throw new ApiError({
          status: 429,
          code: 'authn.throttled',
          retryAfterMs: 3000,
          attempts: 1,
        })
      },
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit()
    const submit = screen.getByRole('button', { name: zhCN.register.submit })
    await screen.findByRole('alert')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toBeDisabled()
    expect(submit).not.toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveFocus()
    expect(submit).toHaveAccessibleDescription(waitSentence(3))
    await userEvent.setup().click(submit)
    fireEvent.submit(submit.closest('form') as HTMLFormElement)
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/register'),
    ).toHaveLength(1)
  })

  it('switch the submit to the retry action in place, keeping the element and focus', async () => {
    const harness = makeHarness({
      [REGISTER]: () => registration(),
      [LOGIN_START]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillFields()
    const submit = screen.getByRole('button', { name: zhCN.register.submit })
    await userEvent.setup().click(submit)
    const retry = await screen.findByRole('button', {
      name: zhCN.register.retrySignIn,
    })
    expect(retry).toBe(submit)
    expect(retry).toHaveFocus()
    expect(retry).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('keep the retry action busy and refuse a second activation while its login is in flight', async () => {
    let releaseRetry: (value: unknown) => void = () => {}
    const retryGate = new Promise((resolve) => {
      releaseRetry = resolve
    })
    let starts = 0
    const harness = makeHarness({
      [REGISTER]: () => registration(),
      [LOGIN_START]: () => {
        starts += 1
        if (starts === 1) {
          throw apiError(401, 'authn.login_failed')
        }
        return retryGate
      },
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit()
    const retry = await screen.findByRole('button', {
      name: zhCN.register.retrySignIn,
    })
    await userEvent.setup().click(retry)
    await waitFor(() => expect(loginStartCalls(harness)).toHaveLength(2))
    expect(retry).toHaveAttribute('aria-busy', 'true')
    expect(retry).toHaveAttribute('aria-disabled', 'true')
    expect(retry).not.toBeDisabled()
    expect(retry).toHaveFocus()
    await userEvent.setup().click(retry)
    expect(loginStartCalls(harness)).toHaveLength(2)

    await act(async () => {
      releaseRetry(completeFlow(1))
    })
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.register.signedIn,
      ),
    )
  })

  it('keep the registration fields focusable and read-only once the auto-login is refused', async () => {
    const harness = makeHarness({
      [REGISTER]: () => registration(),
      [LOGIN_START]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit()
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.register.signInRequired,
      ),
    )
    for (const label of Object.values(LABELS)) {
      const field = screen.getByLabelText(label)
      expect(field).toHaveAttribute('readonly')
      expect(field).not.toBeDisabled()
    }
    const confirmPassword = screen.getByLabelText(LABELS.confirmPassword)
    confirmPassword.focus()
    expect(confirmPassword).toHaveFocus()
  })

  it('refuse a form submission once the auto-login was refused', async () => {
    const harness = makeHarness({
      [REGISTER]: () => registration(),
      [LOGIN_START]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit()
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.register.signInRequired,
      ),
    )
    const form = screen.getByLabelText(LABELS.username).closest('form')
    expect(form).not.toBeNull()
    await act(async () => {
      fireEvent.submit(form as HTMLFormElement)
    })
    expect(registerCalls(harness)).toHaveLength(1)
  })

  it('refuse a form submission once the session is signed in', async () => {
    const harness = makeHarness({
      [REGISTER]: () => registration(),
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit()
    const status = screen.getByRole('status')
    await waitFor(() =>
      expect(status).toHaveTextContent(zhCN.register.signedIn),
    )
    const form = status.closest('form')
    expect(form).not.toBeNull()
    await act(async () => {
      fireEvent.submit(form as HTMLFormElement)
    })
    expect(registerCalls(harness)).toHaveLength(1)
  })

  it('keep the opened account when the auto-login is refused, and retry only the login', async () => {
    let starts = 0
    const harness = makeHarness({
      [REGISTER]: () => registration(),
      [LOGIN_START]: () => {
        starts += 1
        if (starts === 1) {
          throw apiError(401, 'authn.login_failed')
        }
        return completeFlow(1)
      },
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    const onSignInRequired = vi.fn()
    renderWithProviders(
      <PasswordRegisterForm
        session={harness.session}
        onSignedIn={onSignedIn}
        onSignInRequired={onSignInRequired}
      />,
    )
    await fillAndSubmit()

    await waitFor(() =>
      expect(onSignInRequired).toHaveBeenCalledWith(USERNAME),
    )
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.register.signInRequired,
    )
    expect(screen.getByLabelText(LABELS.username)).toHaveValue(USERNAME)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(onSignedIn).not.toHaveBeenCalled()
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/register'),
    ).toHaveLength(1)

    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.register.retrySignIn }))
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(screen.getByRole('status')).toHaveTextContent(zhCN.register.signedIn)
    expect(
      screen.queryByRole('button', { name: zhCN.register.retrySignIn }),
    ).not.toBeInTheDocument()
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/register'),
    ).toHaveLength(1)
    expect(starts).toBe(2)
  })

  it('show the account-created notice without a host sign-in handoff', async () => {
    const harness = makeHarness({
      [REGISTER]: () => registration(),
      [LOGIN_START]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit()

    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.register.signInRequired,
      ),
    )
    expect(screen.getByLabelText(LABELS.username)).toHaveValue(USERNAME)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(harness.calls).toHaveLength(2)
  })

  it('render a parked step after the auto-login instead of the registration fields', async () => {
    const harness = makeHarness({
      [REGISTER]: () => registration(),
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'new_password', kind: 'password', secret: true }],
          },
        }),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <PasswordRegisterForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    await fillAndSubmit()

    expect(
      await screen.findByLabelText(zhCN.passwordSignIn.newPasswordLabel),
    ).toBeInTheDocument()
    expect(screen.queryByLabelText(LABELS.username)).not.toBeInTheDocument()
    expect(onSignedIn).not.toHaveBeenCalled()
  })

  it('treat a superseded auto-login as the lost race it is', async () => {
    let releaseFirst: (value: unknown) => void = () => {}
    const firstGate = new Promise((resolve) => {
      releaseFirst = resolve
    })
    let answers = 0
    const harness = makeHarness({
      [REGISTER]: () => registration(),
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
      <PasswordRegisterForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    await fillAndSubmit()
    const submit = screen.getByRole('button', { name: zhCN.register.submit })
    await waitFor(() => expect(registerCalls(harness)).toHaveLength(1))
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    await act(async () => {
      await harness.session.answerFlow({ seq: 1, step: 'complete', input: {} })
    })
    expect(harness.store.get()).toBe('access-race')
    await act(async () => {
      releaseFirst(authenticatedCompletion())
    })
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.register.signInRequired,
      ),
    )
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(onSignedIn).not.toHaveBeenCalled()
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/register'),
    ).toHaveLength(1)
  })

  it('contain a throwing start gate without opening an account', async () => {
    const harness = makeHarness({})
    const beforeStart = vi.fn(() => {
      throw new Error('consent wall failed')
    })
    renderWithProviders(
      <PasswordRegisterForm session={harness.session} beforeStart={beforeStart} />,
    )
    await fillAndSubmit()

    await waitFor(() => expect(beforeStart).toHaveBeenCalledTimes(1))
    expect(harness.calls).toHaveLength(0)
    expect(
      screen.getByRole('button', { name: zhCN.register.submit }),
    ).not.toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByLabelText(LABELS.username)).toHaveValue(USERNAME)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('count a stated Retry-After wait down, re-enable the submit and keep the banner', async () => {
    vi.useFakeTimers()
    try {
      const harness = makeHarness({
        [REGISTER]: () => {
          throw new ApiError({
            status: 429,
            code: 'authn.throttled',
            retryAfterMs: 3000,
            attempts: 1,
          })
        },
      })
      renderWithProviders(<PasswordRegisterForm session={harness.session} />)
      for (const [label, value] of [
        [LABELS.username, USERNAME],
        [LABELS.email, MAIL],
        [LABELS.password, PASSWORD],
        [LABELS.confirmPassword, PASSWORD],
      ] as const) {
        fireEvent.change(screen.getByLabelText(label), { target: { value } })
      }
      await act(async () => {
        fireEvent.click(
          screen.getByRole('button', { name: zhCN.register.submit }),
        )
      })

      const submit = screen.getByRole('button', {
        name: zhCN.register.submit,
      })
      expect(submit).toHaveAttribute('aria-disabled', 'true')
      expect(submit).not.toBeDisabled()

      const alert = screen.getByRole('alert')
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(3))
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(3))

      act(() => {
        vi.advanceTimersByTime(1000)
      })
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(2))
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(3))
      expect(submit).toHaveAttribute('aria-disabled', 'true')
      expect(submit).toHaveAccessibleDescription(waitSentence(3))

      act(() => {
        vi.advanceTimersByTime(1999)
      })
      expect(submit).toHaveAttribute('aria-disabled', 'true')

      act(() => {
        vi.advanceTimersByTime(1)
      })
      expect(submit).not.toHaveAttribute('aria-disabled', 'true')
      expect(submit).toHaveAccessibleDescription('')
      expect(visibleSentence(alert)).toHaveTextContent(
        zhCN.errors.authn.throttled,
      )
      expect(visibleSentence(alert)).not.toHaveTextContent('0')
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(3))
      expect(screen.getByRole('alert')).toBe(alert)
      expect(harness.calls).toHaveLength(1)
    } finally {
      await restoreRealTimers()
    }
  })

  it('consult the host start gate before opening an account', async () => {
    const harness = makeHarness({})
    const beforeStart = vi.fn(() => false)
    renderWithProviders(
      <PasswordRegisterForm session={harness.session} beforeStart={beforeStart} />,
    )
    await fillAndSubmit()

    await waitFor(() => expect(beforeStart).toHaveBeenCalledTimes(1))
    expect(beforeStart).toHaveBeenCalledWith({
      method: 'password',
      mode: 'register',
    })
    expect(harness.calls).toHaveLength(0)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('pass axe with no violations', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <div>
        <h1>Create an account</h1>
        <PasswordRegisterForm session={harness.session} />
      </div>,
    )
    await expectNoAxeViolations()
  })
})
