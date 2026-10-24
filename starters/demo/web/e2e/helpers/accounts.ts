/**
 * Account journeys, driven the way a person drives them.
 *
 * The helpers fill and click the rendered form through accessible labels and
 * roles; they never call the API. The one non-UI read they make is the email
 * verification code, which the review mail carrier writes to the process log:
 * the mail reaches no relay, so a person running the review reads it there.
 *
 * Registration with a password lands on the account page's verification
 * section, which sends the first code by itself. `register` therefore answers
 * with a log cursor taken before the form was submitted; passing it to
 * `verifyEmail` makes the helper accept only a code written for this
 * registration, not an earlier one sent to the same address.
 */
import type { Page } from '@playwright/test'

import { waitForVerificationCode } from '../fixtures/log'
import type { ReviewServer } from '../fixtures/server'
import { copy } from './i18n'

/** A password-signup account. */
export interface PasswordAccount {
  readonly username: string
  readonly email: string
  readonly password: string
}

export interface RegisterResult {
  /** The log position before the registration was submitted. */
  readonly verificationAfter: number
}

export interface Accounts {
  /** Open the sign-in surface at the deployment root. */
  openSignIn(): Promise<void>
  /** Switch the sign-in surface to its registration mode. */
  openRegistration(): Promise<void>
  /** Open the surface, switch to registration, and submit the form. */
  register(account: PasswordAccount): Promise<RegisterResult>
  /** Fill the registration fields on the already-open form. */
  fillRegistration(account: PasswordAccount): Promise<void>
  /**
   * Submit the registration form without waiting for the landing. The caller
   * observes the in-place refusal or the verification section itself.
   */
  submitRegistration(): Promise<void>
  /** Prove the email address from the code in the process log. */
  verifyEmail(
    address: string,
    options?: { readonly after?: number },
  ): Promise<void>
  /** Register and then prove the email address in one call. */
  registerAndVerify(account: PasswordAccount): Promise<void>
  /** Sign in with a password. */
  signIn(input: {
    readonly identifier: string
    readonly password: string
  }): Promise<void>
  /** Sign out through the account menu. */
  signOut(): Promise<void>
  /** A fresh account whose identifiers do not collide with another run. */
  unique(prefix: string): PasswordAccount
}

/** A password the review policy accepts and that matches no identifier. */
const PASSWORD = 'e2e-Password-123'

/** The email domain the review mail carrier accepts (it reaches no relay). */
const EMAIL_DOMAIN = 'e2e.test'

/** The registered account's identifiers carry a run-scoped suffix. */
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
  readonly server: ReviewServer
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

  /** The account page's email verification section, before it is proved. */
  const emailSection = () =>
    page.getByRole('region', {
      name: copy('accountUi', 'verification.title'),
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

    // The cursor is taken before submission: the verification section sends
    // its first code after the form is accepted, and only that code may be
    // used.
    const verificationAfter = server.log.mark()
    await submitRegistration()

    // The password flow lands on the account page's verification section.
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
    await page
      .getByRole('region', {
        name: copy('accountUi', 'verification.verifiedTitle'),
        exact: true,
      })
      .waitFor()
  }

  const signIn = async (input: {
    readonly identifier: string
    readonly password: string
  }): Promise<void> => {
    await openSignIn()
    // The channel's tab shares its name with the password field, so the
    // inputs are addressed as textboxes rather than by label alone.
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
    // The sign-in form leaves the document when the session flips. A refused
    // sign-in leaves it in place, and this wait then reports the failure.
    await identifier.waitFor({ state: 'detached' })
  }

  const signOut = async (): Promise<void> => {
    // The trigger's accessible name is the display name when the account has
    // one and the host's account-menu label otherwise. The menu triggers in
    // the bar are the tenant switcher, then the account menu; the account menu
    // is the last one, which is the fallback when the display name is set.
    const bar = page.getByRole('banner')
    const named = bar.getByRole('button', {
      name: copy('demo', 'userMenu.accountMenu'),
      exact: true,
    })
    const fallback = bar.locator('button[aria-haspopup="menu"]').last()
    await fallback.waitFor()
    if ((await named.count()) > 0) {
      await named.click()
    } else {
      await fallback.click()
    }
    await page
      .getByRole('menuitem', {
        name: copy('demo', 'userMenu.signOut'),
        exact: true,
      })
      .click()
    // Signing out is terminal: the shell replaces the authenticated area with
    // its session-ended surface, whose action returns to the sign-in form.
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
