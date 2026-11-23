/**
 * SignInScreen's register mode under a host page heading, in a real
 * browser: the host names the page and the screen heads its own register
 * block, so the two headings must not carry the same words. The host's
 * page heading here uses the shipped register-entry label -- the demo's
 * sign-in page heads its register mode with exactly those words -- and
 * the case requires the page to hold one heading carrying them.
 */
import { describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import {
  LOGIN_START,
  apiError,
  makeHarness,
  methodList,
} from '@speed/test-utils/session-harness'
import { renderWithProviders } from '../test-utils/render.js'
import { SignInScreen } from './SignInScreen.js'
import enUS from './locales/en-US.json' with { type: 'json' }

/** The words the host's page heading carries. Read from the bundle, never
 * inlined: the host shows the same label on its register entry. */
const HOST_PAGE_TITLE = enUS.mode.registerEntry

describe('SignInScreen register heading in Chromium', () => {
  it('keeps one heading carrying the host page title at 375', async () => {
    await page.viewport(375, 900)
    const harness = makeHarness({})
    renderWithProviders(
      <>
        <h1>{HOST_PAGE_TITLE}</h1>
        <SignInScreen
          session={harness.session}
          discovery={methodList(
            [{ name: 'sms', kind: 'form', opens_account: true }],
            { signup: true },
          )}
          mode="register"
        />
      </>,
      { language: 'en-US' },
    )

    expect(
      screen.getAllByRole('heading', { name: HOST_PAGE_TITLE }),
    ).toHaveLength(1)
  })
})

describe('SignInScreen password channel layout in Chromium', () => {
  it('space the submit key one flex gap below the last field', async () => {
    await page.viewport(420, 900)
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([{ name: 'password', kind: 'form' }])}
      />,
      { language: 'en-US' },
    )

    const password = screen.getByLabelText(enUS.passwordSignIn.passwordLabel)
    const field = password.closest('.MuiFormControl-root')
    const form = password.closest('form')
    expect(field).not.toBeNull()
    expect(form).not.toBeNull()
    const gap = Number.parseFloat(getComputedStyle(form!).gap)
    const submit = screen.getByRole('button', {
      name: enUS.passwordSignIn.submit,
    })

    // An empty feedback region sits between the fields and the actions
    // row; it must not add a row of its own -- one gap, not two.
    const distance =
      submit.getBoundingClientRect().top -
      field!.getBoundingClientRect().bottom
    expect(distance).toBeCloseTo(gap, 0)
  })

  it('place the forgot entry on the submit row, ahead of the submit key', async () => {
    await page.viewport(420, 900)
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([{ name: 'password', kind: 'form' }], {
          passwordReset: true,
          passwordResetMethods: ['email'],
        })}
        onForgotPassword={() => undefined}
      />,
      { language: 'en-US' },
    )

    const password = screen.getByLabelText(enUS.passwordSignIn.passwordLabel)
    const field = password.closest('.MuiFormControl-root')!
    const forgot = screen.getByRole('button', {
      name: enUS.passwordSignIn.forgotPassword,
    })
    const submit = screen.getByRole('button', {
      name: enUS.passwordSignIn.submit,
    })

    const forgotBox = forgot.getBoundingClientRect()
    const submitBox = submit.getBoundingClientRect()
    // One row: the entry's and the submit key's vertical spans overlap.
    expect(forgotBox.top).toBeLessThan(submitBox.bottom)
    expect(forgotBox.bottom).toBeGreaterThan(submitBox.top)
    // The row sits below the password field, and the entry leads it.
    expect(forgotBox.top).toBeGreaterThan(field.getBoundingClientRect().bottom)
    expect(forgotBox.right).toBeLessThan(submitBox.left)
    // One adjacent group, one spacing token apart -- not the two ends the
    // old space-between row left -- with the submit closing the row.
    expect(submitBox.left - forgotBox.right).toBeCloseTo(8, 0)
    const row = submit.closest<HTMLElement>('[data-action-row]')
    expect(row).not.toBeNull()
    expect(forgot.closest('[data-action-row]')).toBe(row)
    expect(
      Math.abs(submitBox.right - contentRightOf(row as HTMLElement)),
    ).toBeLessThanOrEqual(1)
  })
})

describe('SignInScreen registration auto-login handoff in Chromium', () => {
  const REGISTRATION = {
    state: 'registered',
    account_id: 'account-1',
    verification: { state: 'pending', email: 'ada@example.com' },
  }

  async function fillAndSubmitRegistration(
    user: ReturnType<typeof userEvent.setup>,
  ): Promise<void> {
    await user.click(
      screen.getByRole('button', { name: enUS.mode.registerEntry }),
    )
    await user.type(screen.getByLabelText(enUS.register.usernameLabel), 'ada')
    await user.type(
      screen.getByLabelText(enUS.register.emailLabel),
      'ada@example.com',
    )
    await user.type(screen.getByLabelText(enUS.register.passwordLabel), 's3cret-pass')
    await user.type(
      screen.getByLabelText(enUS.register.confirmPasswordLabel),
      's3cret-pass',
    )
    await user.click(
      screen.getByRole('button', { name: enUS.register.submit }),
    )
  }

  it("lands focus on the sign-in form's first field when a refused auto-login replaces the registration form", async () => {
    await page.viewport(420, 900)
    const harness = makeHarness({
      'POST /api/v1/auth/register': () => REGISTRATION,
      [LOGIN_START]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'password', kind: 'form' }],
          { signup: true, signupPassword: true },
        )}
      />,
      { language: 'en-US' },
    )
    const user = userEvent.setup()
    await fillAndSubmitRegistration(user)

    const identifier = await screen.findByLabelText(
      enUS.passwordSignIn.identifierLabel,
    )
    // The submit key left with the registration form; focus lands on the
    // sign-in form's first field, never on the document body.
    expect(document.activeElement).toBe(identifier)
  })

  it('leaves the focus the operator moved during the auto-login where it is', async () => {
    await page.viewport(420, 900)
    let rejectLogin: (reason?: unknown) => void = () => {}
    const loginGate = new Promise((_resolve, reject) => {
      rejectLogin = reject
    })
    const harness = makeHarness({
      'POST /api/v1/auth/register': () => REGISTRATION,
      [LOGIN_START]: () => loginGate,
    })
    renderWithProviders(
      <>
        <SignInScreen
          session={harness.session}
          discovery={methodList(
            [{ name: 'password', kind: 'form' }],
            { signup: true, signupPassword: true },
          )}
        />
        <button type="button">outside</button>
      </>,
      { language: 'en-US' },
    )
    const user = userEvent.setup()
    await fillAndSubmitRegistration(user)
    await waitFor(() =>
      expect(
        harness.calls.some(
          (call) => `${call.method} ${call.path}` === LOGIN_START,
        ),
      ).toBe(true),
    )
    // The auto-login is still in flight; the operator moves focus out of
    // the registration form while it runs.
    const outside = screen.getByRole('button', { name: 'outside' })
    await user.click(outside)
    expect(document.activeElement).toBe(outside)

    await act(async () => {
      rejectLogin(apiError(401, 'authn.login_failed'))
      await Promise.resolve()
    })
    const identifier = await screen.findByLabelText(
      enUS.passwordSignIn.identifierLabel,
    )
    // The removal did not take the focus, so the landing must not take it
    // back from the place the operator chose.
    expect(document.activeElement).toBe(outside)
    expect(document.activeElement).not.toBe(identifier)
  })
})

/** The row's right content edge (the row may carry its own padding). */
function contentRightOf(row: HTMLElement): number {
  const rowBox = row.getBoundingClientRect()
  return rowBox.right - Number.parseFloat(getComputedStyle(row).paddingRight)
}
