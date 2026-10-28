/**
 * SignInScreen's register mode under a host page heading, in a real
 * browser: the host names the page and the screen heads its own register
 * block, so the two headings must not carry the same words. The host's
 * page heading here uses the shipped register-entry label -- the demo's
 * sign-in page heads its register mode with exactly those words -- and
 * the case requires the page to hold one heading carrying them.
 */
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { makeHarness, methodList } from '@speed/test-utils/session-harness'
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
  })
})
