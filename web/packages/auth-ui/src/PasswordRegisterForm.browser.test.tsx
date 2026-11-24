import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { userEvent } from 'vitest/browser'
import { ApiError } from '@speed/api-client'
import { PasswordRegisterForm } from './PasswordRegisterForm.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  LOGIN_ANSWER,
  LOGIN_START,
  PROFILE_GET,
  apiError,
  authenticatedCompletion,
  completeFlow,
  makeHarness,
  principal,
} from '@speed/test-utils/session-harness'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const REGISTER = 'POST /api/v1/auth/register'
const USERNAME = 'ada'
const MAIL = 'ada@example.com'
const PASSWORD = 's3cret-pass'

const LABELS = {
  username: zhCN.register.usernameLabel,
  email: zhCN.register.emailLabel,
  password: zhCN.register.passwordLabel,
  confirmPassword: zhCN.register.confirmPasswordLabel,
} as const

function waitSentence(seconds: number): string {
  const [before = '', after = ''] = zhCN.throttle.retryAfter.split('{{seconds}}')
  return `${before}${seconds}${after}`
}

function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
  readonly reject: (reason: unknown) => void
} {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((settle, fail) => {
    resolve = settle
    reject = fail
  })
  return { promise, resolve, reject }
}

function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

function registerCalls(harness: ReturnType<typeof makeHarness>) {
  return harness.calls.filter((call) => call.path === '/api/v1/auth/register')
}

function loginStartCalls(harness: ReturnType<typeof makeHarness>) {
  return harness.calls.filter(
    (call) => `${call.method} ${call.path}` === LOGIN_START,
  )
}

function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const text = (active.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 80)
  return `${active.tagName.toLowerCase()} "${text}"`
}

function registration(): unknown {
  return {
    state: 'registered',
    account_id: 'account-1',
    verification: { state: 'pending', email: MAIL },
  }
}

async function fillFields(): Promise<void> {
  await userEvent.type(screen.getByLabelText(LABELS.username), USERNAME)
  await userEvent.type(screen.getByLabelText(LABELS.email), MAIL)
  await userEvent.type(screen.getByLabelText(LABELS.password), PASSWORD)
  await userEvent.type(screen.getByLabelText(LABELS.confirmPassword), PASSWORD)
}

async function fillAndSubmit(): Promise<HTMLElement> {
  await fillFields()
  const submit = screen.getByRole('button', { name: zhCN.register.submit })
  await userEvent.click(submit)
  return submit
}

describe('PasswordRegisterForm focus in Chromium', () => {
  it('keeps focus on the submit through the registration flight', async () => {
    const written = deferred<unknown>()
    const harness = makeHarness({
      [REGISTER]: () => written.promise,
      [LOGIN_START]: () => completeFlow(1),
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    const submit = await fillAndSubmit()

    await waitFor(() => expect(registerCalls(harness)).toHaveLength(1))
    await nextFrame()
    expect(
      submit,
      `focus while the write is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    await waitFor(() => {
      expect(submit).toHaveAttribute('aria-disabled', 'true')
      expect(submit).toHaveAttribute('aria-busy', 'true')
    })

    written.resolve(registration())
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: zhCN.register.submit }),
      ).not.toBeInTheDocument(),
    )
  })

  it('keeps focus on the submit and in the tab order while a stated wait holds', async () => {
    const written = deferred<unknown>()
    const harness = makeHarness({
      [REGISTER]: () => written.promise,
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    const submit = await fillAndSubmit()
    await waitFor(() => expect(registerCalls(harness)).toHaveLength(1))
    await nextFrame()
    expect(
      submit,
      `focus while the write is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    written.reject(
      new ApiError({
        status: 429,
        code: 'authn.throttled',
        retryAfterMs: 3000,
        attempts: 1,
      }),
    )

    await screen.findByRole('alert')
    await nextFrame()
    expect(
      submit,
      `focus when the wait landed; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    await waitFor(() =>
      expect(submit).toHaveAccessibleDescription(waitSentence(3)),
    )
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toHaveAttribute('aria-busy', 'true')

    screen.getByLabelText(LABELS.confirmPassword).focus()
    await userEvent.tab()
    expect(
      document.activeElement,
      `focus after Tab from the last field; document.activeElement is ${describeActiveElement()}`,
    ).toBe(submit)
  })

  it('keeps the retry action on the submit element when the auto-login is refused', async () => {
    const harness = makeHarness({
      [REGISTER]: () => registration(),
      [LOGIN_START]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    const submit = await fillAndSubmit()

    const retry = await screen.findByRole('button', {
      name: zhCN.register.retrySignIn,
    })
    expect(retry).toBe(submit)
    expect(
      retry,
      `focus after the retry replaced the submit; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })

  it('keeps focus on the retry action while its login is in flight', async () => {
    const retryReply = deferred<unknown>()
    let starts = 0
    const harness = makeHarness({
      [REGISTER]: () => registration(),
      [LOGIN_START]: () => {
        starts += 1
        if (starts === 1) {
          throw apiError(401, 'authn.login_failed')
        }
        return retryReply.promise
      },
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    const submit = await fillAndSubmit()
    const retry = await screen.findByRole('button', {
      name: zhCN.register.retrySignIn,
    })
    expect(retry).toBe(submit)

    await userEvent.click(retry)
    await waitFor(() => expect(loginStartCalls(harness)).toHaveLength(2))
    await nextFrame()
    expect(
      retry,
      `focus while the retry's login is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    expect(retry).toHaveAttribute('aria-busy', 'true')
    expect(retry).toHaveAttribute('aria-disabled', 'true')

    retryReply.resolve(completeFlow(1))
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.register.signedIn,
      ),
    )
  })

  it('keeps focus in the field an Enter submission started from when the fields go read-only', async () => {
    const harness = makeHarness({
      [REGISTER]: () => registration(),
      [LOGIN_START]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(<PasswordRegisterForm session={harness.session} />)
    await fillFields()
    const confirmPassword = screen.getByLabelText(LABELS.confirmPassword)
    confirmPassword.focus()

    await userEvent.keyboard('{Enter}')

    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.register.signInRequired,
      ),
    )
    await nextFrame()
    expect(
      document.activeElement,
      `focus after the fields went read-only; document.activeElement is ${describeActiveElement()}`,
    ).toBe(confirmPassword)
    expect(confirmPassword).toHaveAttribute('readonly')
  })
})
