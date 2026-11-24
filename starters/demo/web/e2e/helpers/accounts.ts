import type { Page } from '@playwright/test'

import { waitForVerificationCode } from '../fixtures/log'
import type { E2EServer } from '../fixtures/server'
import { copy } from './i18n'

export interface PasswordAccount {
  readonly username: string
  readonly email: string
  readonly password: string
}

export interface RegisterResult {
  readonly verificationAfter: number
}

export interface Accounts {
  openSignIn(): Promise<void>
  openRegistration(): Promise<void>
  register(account: PasswordAccount): Promise<RegisterResult>
  fillRegistration(account: PasswordAccount): Promise<void>
  submitRegistration(): Promise<void>
  verifyEmail(
    address: string,
    options?: { readonly after?: number },
  ): Promise<void>
  registerAndVerify(account: PasswordAccount): Promise<void>
  signIn(input: {
    readonly identifier: string
    readonly password: string
  }): Promise<void>
  answerSignupUsername(username: string): Promise<void>
  answerPhoneSignup(input: {
    readonly username: string
    readonly password: string
    readonly displayName?: string
  }): Promise<void>
  answerSocialSignup(input: {
    readonly username: string
    readonly password: string
  }): Promise<void>
  signOut(): Promise<void>
  unique(prefix: string): PasswordAccount
}

const PASSWORD = 'e2e-Password-123'

const EMAIL_DOMAIN = 'e2e.test'

const SIGNUP_USERNAME_FIELD = 'username'

const SIGNUP_PASSWORD_FIELD = 'password'
const SIGNUP_CONFIRM_FIELD = 'confirmPassword'

function freshIdentifier(prefix: string): string {
  const stamp = Date.now().toString(36)
  const random = Math.random().toString(36).slice(2, 8)
  return `${prefix}-${stamp}-${random}`
}

export function createAccounts({
  page,
  server,
}: {
  readonly page: Page
  readonly server: E2EServer
}): Accounts {
  const openSignIn = async (): Promise<void> => {
    await page.goto(`${server.url}/`)
    await page
      .getByRole('heading', {
        name: copy('demo', 'signIn.heading'),
        exact: true,
      })
      .waitFor()
  }

  const openRegistration = async (): Promise<void> => {
    await page
      .getByRole('button', {
        name: copy('authUi', 'mode.registerEntry'),
        exact: true,
      })
      .click()
    await page
      .getByLabel(copy('authUi', 'register.usernameLabel'), { exact: true })
      .waitFor()
  }

  // The verification section mounts inside the pane's email card; this is the region the code field sits in.
  const emailSection = () =>
    page.getByRole('region', {
      name: copy('demo', 'account.rows.label.email'),
      exact: true,
    })

  const fillRegistration = async (
    account: PasswordAccount,
  ): Promise<void> => {
    await page
      .getByLabel(copy('authUi', 'register.usernameLabel'), { exact: true })
      .fill(account.username)
    await page
      .getByLabel(copy('authUi', 'register.emailLabel'), { exact: true })
      .fill(account.email)
    await page
      .getByLabel(copy('authUi', 'register.passwordLabel'), { exact: true })
      .fill(account.password)
    await page
      .getByLabel(copy('authUi', 'register.confirmPasswordLabel'), {
        exact: true,
      })
      .fill(account.password)
  }

  const submitRegistration = async (): Promise<void> => {
    await page
      .getByRole('button', {
        name: copy('authUi', 'register.submit'),
        exact: true,
      })
      .click()
  }

  const register = async (
    account: PasswordAccount,
  ): Promise<RegisterResult> => {
    await openSignIn()
    await openRegistration()
    await fillRegistration(account)

    // Take the cursor before submission so only this registration's code can be accepted.
    const verificationAfter = server.log.mark()
    await submitRegistration()

    await emailSection().waitFor()
    return { verificationAfter }
  }

  const verifyEmail = async (
    address: string,
    options: { readonly after?: number } = {},
  ): Promise<void> => {
    const section = emailSection()
    await section.waitFor()
    const { code } = await waitForVerificationCode(server.log, 'mail', address, {
      after: options.after ?? 0,
    })
    const codeInput = section.getByLabel(
      copy('accountUi', 'verification.codeLabel'),
      { exact: true },
    )
    await codeInput.fill(code)
    await section
      .getByRole('button', {
        name: copy('accountUi', 'verification.verify'),
        exact: true,
      })
      .click()
    await section
      .getByText(
        copy('accountUi', 'verification.verifiedDescription', {
          email: address,
        }),
        { exact: true },
      )
      .waitFor()
  }

  const signIn = async (input: {
    readonly identifier: string
    readonly password: string
  }): Promise<void> => {
    await openSignIn()
    // The channel's tab shares its name with the password field, so address the inputs as textboxes.
    const identifier = page.getByRole('textbox', {
      name: copy('authUi', 'passwordSignIn.identifierLabel'),
      exact: true,
    })
    await identifier.fill(input.identifier)
    await page
      .getByRole('textbox', {
        name: copy('authUi', 'passwordSignIn.passwordLabel'),
        exact: true,
      })
      .fill(input.password)
    await page
      .getByRole('button', {
        name: copy('authUi', 'passwordSignIn.submit'),
        exact: true,
      })
      .click()
    await identifier.waitFor({ state: 'detached' })
  }

  const signUpUsername = async (username: string): Promise<void> => {
    // Scope the submit to the form the username input belongs to, so another Continue on the page is not matched.
    const field = page.locator(`input[name="${SIGNUP_USERNAME_FIELD}"]`)
    await field.waitFor()
    await field.fill(username)
    const form = field.locator('xpath=ancestor::form[1]')
    await form
      .getByRole('button', {
        name: copy('authUi', 'signInFlow.submit'),
        exact: true,
      })
      .click()
    await field.waitFor({ state: 'detached' })
  }

  const signUpPhone = async (input: {
    readonly username: string
    readonly password: string
    readonly displayName?: string
  }): Promise<void> => {
    // Scope every lookup to the username input's form; the sign-in page has a field with the same label.
    const field = page.locator(`input[name="${SIGNUP_USERNAME_FIELD}"]`)
    await field.waitFor()
    const form = field.locator('xpath=ancestor::form[1]')
    await field.fill(input.username)
    if (input.displayName !== undefined) {
      await form
        .getByLabel(copy('authUi', 'register.displayNameLabel'), {
          exact: true,
        })
        .fill(input.displayName)
    }
    await form
      .getByLabel(copy('authUi', 'passwordSignIn.passwordLabel'), {
        exact: true,
      })
      .fill(input.password)
    await form
      .getByLabel(copy('authUi', 'register.confirmPasswordLabel'), {
        exact: true,
      })
      .fill(input.password)
    await form
      .getByRole('button', {
        name: copy('authUi', 'signInFlow.signupAccountSubmit'),
        exact: true,
      })
      .click()
    await field.waitFor({ state: 'detached' })
  }

  const signUpSocial = async (input: {
    readonly username: string
    readonly password: string
  }): Promise<void> => {
    // Scope every lookup to the username's own form so the sign-in page's password field is never touched.
    const field = page.locator(`input[name="${SIGNUP_USERNAME_FIELD}"]`)
    await field.waitFor()
    const form = field.locator('xpath=ancestor::form[1]')
    await field.fill(input.username)
    await form.locator(`input[name="${SIGNUP_PASSWORD_FIELD}"]`).fill(
      input.password,
    )
    await form.locator(`input[name="${SIGNUP_CONFIRM_FIELD}"]`).fill(
      input.password,
    )
    await form
      .getByRole('button', {
        name: copy('authUi', 'signInFlow.signupAccountSubmit'),
        exact: true,
      })
      .click()
    await field.waitFor({ state: 'detached' })
  }

  const signOut = async (): Promise<void> => {
    // The trigger's accessible name varies with the account; its test id is stable.
    await page
      .getByTestId('account-entry-trigger')
      .click()
    await page
      .getByRole('menuitem', {
        name: copy('demo', 'userMenu.signOut'),
        exact: true,
      })
      .click()
    await page
      .getByRole('heading', {
        name: copy('authUi', 'sessionEnded.title'),
        exact: true,
      })
      .waitFor()
  }

  return {
    openSignIn,
    openRegistration,
    fillRegistration,
    submitRegistration,
    register,
    verifyEmail,
    async registerAndVerify(account: PasswordAccount): Promise<void> {
      const { verificationAfter } = await register(account)
      await verifyEmail(account.email, { after: verificationAfter })
    },
    signIn,
    answerSignupUsername: signUpUsername,
    answerPhoneSignup: signUpPhone,
    answerSocialSignup: signUpSocial,
    signOut,
    unique(prefix: string): PasswordAccount {
      const identifier = freshIdentifier(prefix)
      return {
        username: identifier,
        email: `${identifier}@${EMAIL_DOMAIN}`,
        password: PASSWORD,
      }
    },
  }
}
