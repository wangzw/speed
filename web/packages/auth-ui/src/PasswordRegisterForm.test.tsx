/**
 * PasswordRegisterForm behaviour: the form opens an account with one
 * `session.register` request and then signs in with the same credentials
 * through the ordinary password flow, the confirmation is compared in the
 * form and never travels, an optional display name travels trimmed only
 * when filled and is omitted when blank, each username refusal (taken,
 * reserved, malformed)
 * lands on the username field with its own message, a
 * taken email address lands on the email field,
 * the mapped refusals render their register-section text while every other
 * code resolves through the shared error banner, an auto-login refusal
 * keeps the opened account (notice plus a retry that re-runs only the
 * login), a settled auto-login shows the signed-in line, a parked step
 * replaces the fields, a superseded answer stays quiet, a throwing start
 * gate opens nothing, a stated Retry-After counts down while it holds the
 * submit and leaves the banner after it elapses, the submit stays focused
 * and busy for the flight rather than being natively disabled, a stated
 * wait holds the submit unavailable with the wait as its accessible
 * description, the form refuses a second submission at either layer, the
 * retry action replaces the submit in place, the fields fall back to
 * read-only once the auto-login is refused, an open account closes the
 * form's submit path, the retry reports its own login's flight and
 * refuses a second activation, and the tree passes axe.
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

/** The failure banner's sentence naming a whole-second wait. */
function waitSentence(seconds: number): string {
  const [before = '', after = ''] = zhCN.throttle.retryAfter.split('{{seconds}}')
  return `${before}${seconds}${after}`
}

/** The alert's painted (aria-hidden) sentence, which follows the countdown. */
function visibleSentence(alert: HTMLElement): HTMLElement | null {
  return alert.querySelector<HTMLElement>('span[aria-hidden="true"]')
}

/** The alert's accessible (clipped) sentence, which never changes. */
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

/** The registration requests the form has sent. */
function registerCalls(harness: ReturnType<typeof makeHarness>) {
  return harness.calls.filter((call) => call.path === '/api/v1/auth/register')
}

/** The login-flow starts the form has sent. */
function loginStartCalls(harness: ReturnType<typeof makeHarness>) {
  return harness.calls.filter(
    (call) => `${call.method} ${call.path}` === LOGIN_START,
  )
}

/** Type the registration values, leaving focus in the last field typed. */
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
    // Optional: a blank field must not travel as an empty string, which
    // the server would read as a name of nothing rather than no name.
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
      // The filled value stays for correction.
      expect(username).toHaveValue(USERNAME)
      // The refusal is the field's, not a whole-attempt banner.
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(harness.calls).toHaveLength(1)
    },
  )

  it('attach a taken email to the email field and focus it', async () => {
    // The registration reserves the address, so another account's claim is
    // refused as that field's value -- never as a whole-attempt banner the
    // viewer cannot act on.
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
    // The refusal is the field's, not a whole-attempt banner, and the
    // username -- which the server accepted -- carries no error.
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
      // No auto-login began: the account was not opened. A refusal that
      // stated no wait leaves the submit available at once.
      expect(harness.calls).toHaveLength(1)
      expect(
        screen.getByRole('button', { name: zhCN.register.submit }),
      ).not.toHaveAttribute('aria-disabled', 'true')
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

  it('keep the submit focused and busy while the registration is in flight', async () => {
    // The submit started the write, so it reports the flight and stays
    // natively enabled: a native `disabled` would drop the focus it holds
    // to the document body the moment the request is sent.
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
    // The request leaving the page is the signal both the focusable and
    // the natively disabled implementation produce; the assertions below
    // then name what the control must report, so a regression fails on the
    // attribute instead of waiting for it.
    await waitFor(() => expect(registerCalls(harness)).toHaveLength(1))
    expect(submit).toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toBeDisabled()
    expect(submit).toHaveFocus()
    resolveRegister(registration())
    // The auto-login settles, so the registration surface gives way to
    // the signed-in line rather than re-enabling its submit.
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.register.signedIn,
      ),
    )
  })

  it('refuse a second submission while the registration is in flight', async () => {
    // Enter in a field and the form's own submit event both reach the
    // registration; an in-flight attempt must be refused at the form layer
    // even when the submit key is not the thing the viewer activated.
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
    // A 429 that names a wait is a precondition: the key cannot run, stays
    // focusable, is announced as unavailable, and the countdown sentence
    // already on the page explains why without being duplicated.
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
    // The refusal's banner is the signal both implementations render; the
    // control's state is then asserted directly, so a regression fails on
    // the attribute rather than on a wait that never settles.
    await screen.findByRole('alert')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toBeDisabled()
    expect(submit).not.toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveFocus()
    expect(submit).toHaveAccessibleDescription(waitSentence(3))
    // Neither the key nor the form sends anything while the wait holds.
    await userEvent.setup().click(submit)
    fireEvent.submit(submit.closest('form') as HTMLFormElement)
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/register'),
    ).toHaveLength(1)
  })

  it('switch the submit to the retry action in place, keeping the element and focus', async () => {
    // The two actions are the same slot: React reconciles the retry onto
    // the element the viewer activated, so neither the node nor the focus
    // it holds is lost when the auto-login is refused.
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
    // The retry runs the same password login the auto-login did. It
    // reports that flight and swallows a second activation, so a viewer
    // who clicks it again cannot start another flow.
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
    // The retry's request leaving the page is the signal both
    // implementations produce; the state assertions then name what the
    // control must report, so a regression fails on the attribute instead
    // of waiting for it.
    await waitFor(() => expect(loginStartCalls(harness)).toHaveLength(2))
    expect(retry).toHaveAttribute('aria-busy', 'true')
    expect(retry).toHaveAttribute('aria-disabled', 'true')
    expect(retry).not.toBeDisabled()
    expect(retry).toHaveFocus()
    // A second activation on the busy control starts nothing.
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
    // The account exists, so the fields cannot edit it into a second one.
    // Read-only locks the values without taking focus from the field a
    // viewer submitted from.
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
    // The account exists, so the submit key is gone; the form element is
    // still in the page, and an Enter press or a synthetic submit reaches
    // onSubmit through it. That path must be closed too, or the form opens
    // a second account on the same values.
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
    // A settled auto-login removes the fields and the submit key. The
    // form element stays, and a synthetic submit would otherwise call the
    // registration with no fields at all -- opening a second account on
    // an already authenticated surface.
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
    await waitFor(() => expect(registerCalls(harness)).toHaveLength(1))
    expect(submit).toHaveAttribute('aria-disabled', 'true')
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
      expect(submit).toHaveAttribute('aria-disabled', 'true')
      expect(submit).not.toBeDisabled()

      const alert = screen.getByRole('alert')
      // The banner names the wait the answer stated; the sentence assistive
      // technology reads is fixed at that starting point.
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(3))
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(3))

      // The painted sentence follows the clock; the announced one does not.
      act(() => {
        vi.advanceTimersByTime(1000)
      })
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(2))
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(3))
      expect(submit).toHaveAttribute('aria-disabled', 'true')
      // The wait sentence on the page is the key's description, so the
      // reason is announced with the control instead of duplicated.
      expect(submit).toHaveAccessibleDescription(waitSentence(3))

      // One millisecond before the stated boundary the control is still
      // unavailable, so an implementation that waits less than the header
      // says cannot pass.
      act(() => {
        vi.advanceTimersByTime(1999)
      })
      expect(submit).toHaveAttribute('aria-disabled', 'true')

      // At the boundary the key re-enables and the painted sentence drops
      // the number -- with no stale "0 seconds" -- while the banner itself
      // stays (no timer clears a result).
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
