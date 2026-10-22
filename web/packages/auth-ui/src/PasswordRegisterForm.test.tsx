/**
 * PasswordRegisterForm behaviour: the form opens an account with one
 * `session.register` request and then signs in with the same credentials
 * through the ordinary password flow, the confirmation is compared in the
 * form and never travels, a taken username lands on the username field,
 * the mapped refusals render their register-section text while every other
 * code resolves through the shared error banner, an auto-login refusal
 * keeps the opened account (notice plus a retry that re-runs only the
 * login), a settled auto-login shows the signed-in line, a parked step
 * replaces the fields, a superseded answer stays quiet, a throwing start
 * gate opens nothing, a stated Retry-After holds the submit until it
 * elapses, the submit is busy for the flight, and the tree passes axe.
 * Text expectations read the bundle values, never inline language.
 */

import { describe, expect, it, vi } from 'vitest'
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

interface RegisterLabels {
  readonly username: string
  readonly email: string
  readonly password: string
  readonly confirmPassword: string
}

const LABELS: RegisterLabels = {
  username: zhCN.register.usernameLabel,
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

async function fillAndSubmit(
  values: {
    username?: string
    email?: string
    password?: string
    confirmPassword?: string
  } = {},
): Promise<void> {
  const user = userEvent.setup()
  await user.type(
    screen.getByLabelText(LABELS.username),
    values.username ?? USERNAME,
  )
  await user.type(screen.getByLabelText(LABELS.email), values.email ?? MAIL)
  await user.type(
    screen.getByLabelText(LABELS.password),
    values.password ?? PASSWORD,
  )
  await user.type(
    screen.getByLabelText(LABELS.confirmPassword),
    values.confirmPassword ?? values.password ?? PASSWORD,
  )
  await user.click(
    screen.getByRole('button', { name: zhCN.register.submit }),
  )
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
    // A settled auto-login replaces the registration surface: the
    // signed-in line, never the stale "created, please sign in" pair a
    // host that does not navigate would otherwise leave on screen.
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
    // The confirmation is client-only: it never enters the request.
    expect(harness.calls[0]?.options?.body).not.toHaveProperty('confirmPassword')
    expect(harness.store.get()).toBe('access-1')
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
    // The server's address rule restated in the field: two @ signs would
    // come back as the generic bad-request, with no clue which field.
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

  it('attach a taken username to the username field and focus it', async () => {
    const harness = makeHarness({
      [REGISTER]: () => {
        throw apiError(409, 'authn.username_taken')
      },
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillAndSubmit()

    const username = screen.getByLabelText(LABELS.username)
    await waitFor(() => expect(username).toHaveAttribute('aria-invalid', 'true'))
    const message = screen.getByText(zhCN.register.usernameTaken)
    expect(message.id).not.toBe('')
    expect(username.getAttribute('aria-describedby')).toContain(message.id)
    expect(username).toHaveFocus()
    // The refusal is the field's, not a whole-attempt banner.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
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
      // No auto-login began: the account was not opened. A refusal that
      // stated no wait leaves the submit available at once.
      expect(harness.calls).toHaveLength(1)
      expect(
        screen.getByRole('button', { name: zhCN.register.submit }),
      ).toBeEnabled()
    },
  )

  it('render a 400 through the generic bad-request text, not a policy claim', async () => {
    // A malformed registration body is a 400 the register surface has no
    // dedicated wording for; it must not be recast as "the password
    // does not meet the policy".
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
    // authn.conflict has no register.* text of its own; it resolves
    // through the family's error-code banner instead of being dropped or
    // collapsed onto the unknown line.
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

  it('disable the submit while the registration is in flight', async () => {
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
    await waitFor(() => expect(submit).toBeDisabled())
    resolveRegister(registration())
    // The auto-login settles, so the registration surface gives way to
    // the signed-in line rather than re-enabling its submit.
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.register.signedIn,
      ),
    )
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

    // The account was opened; the failed login is not a registration
    // failure, so no error banner paints and the username stays in place.
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

    // The retry re-runs the login only; the account is never re-opened.
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.register.retrySignIn }))
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    // The retry settled: the sign-in-required notice and the retry action
    // give way to the signed-in line.
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
    // The auto-login answers a forced password change: the shared step
    // renderer takes over, and the registration fields are gone until the
    // flow settles.
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
    // A concurrent sign-in commits while this form's auto-login is in
    // flight, so auth-core rejects the answer with
    // OperationSupersededError: not a registration failure, no error
    // banner, and no onSignedIn from this losing submit.
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
    await waitFor(() => expect(submit).toBeDisabled())
    // The winning sign-in commits while the first answer is in flight.
    await act(async () => {
      await harness.session.answerFlow({ seq: 1, step: 'complete', input: {} })
    })
    expect(harness.store.get()).toBe('access-race')
    await act(async () => {
      releaseFirst(authenticatedCompletion())
    })
    // The account exists and the session belongs to the winner: the form
    // asks for a sign-in rather than reporting a registration failure.
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
    // A throwing gate is a host bug, not a refusal: the registration
    // starts nothing, the form stays mounted and its submit becomes
    // available again instead of staying busy forever.
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
    ).toBeEnabled()
    expect(screen.getByLabelText(LABELS.username)).toHaveValue(USERNAME)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('disable the submit for a stated Retry-After wait and re-enable it when it elapses', async () => {
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
      // Values go in through plain input events: user-event's own
      // scheduling would need the fake clock advanced for every
      // keystroke, which would also move the wait this test measures.
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
      expect(submit).toBeDisabled()
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.register.retryAfter.replace('{{seconds}}', '3'),
      )
      // The wait ends on its own: the key re-enables and the stale
      // throttle banner goes away. One millisecond before the stated
      // boundary the control is still disabled, so an implementation
      // that waits less than the header says cannot pass.
      act(() => {
        vi.advanceTimersByTime(2999)
      })
      expect(submit).toBeDisabled()
      act(() => {
        vi.advanceTimersByTime(1)
      })
      expect(submit).toBeEnabled()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(harness.calls).toHaveLength(1)
    } finally {
      vi.useRealTimers()
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
