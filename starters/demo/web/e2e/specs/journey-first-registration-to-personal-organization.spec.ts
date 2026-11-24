// @journey journey-first-registration-to-personal-organization.md
// @covers REQ-signin-006, REQ-signup-001, REQ-signup-002, REQ-signup-003, REQ-signup-004, REQ-signup-005, REQ-signup-008, REQ-signup-009, REQ-signup-010, REQ-signup-028, REQ-signup-029, REQ-account-010, REQ-account-016, REQ-account-017, REQ-account-019, REQ-account-020, REQ-tenancy-022, REQ-tenancy-026, REQ-tenancy-059, REQ-tenancy-065
import type { Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import type { Accounts } from '../helpers/accounts'
import {
  closeAccountMenu,
  openAccountMenu,
} from '../helpers/account-menu'
import { copy, copyPattern } from '../helpers/i18n'

const ORGANIZATION_DISPLAY_NAME = 'E2E Team'

function verificationSection(page: Page) {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
    exact: true,
  })
}

function verificationBanner(page: Page, email: string) {
  return page.getByRole('region', {
    name: `${copy('demo', 'verificationBanner.region')}: ${copy(
      'demo',
      'verificationBanner.pending',
      { email },
    )}`,
    exact: true,
  })
}

async function signOutToSignIn(
  page: Page,
  accounts: Accounts,
): Promise<void> {
  await accounts.signOut()
  await page
    .getByRole('button', {
      name: copy('authUi', 'sessionEnded.signInAction'),
      exact: true,
    })
    .click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('demo', 'signIn.heading'),
      exact: true,
    }),
  ).toBeVisible()
}

function registrationField(page: Page, key: string) {
  return page.getByLabel(copy('authUi', key), { exact: true })
}

test('first-registration: a new visitor reaches a personal organization, and occupancy is refused', async ({
  demo,
}) => {
  const { page, server, accounts } = demo
  const account = accounts.unique('first')

  await test.step('the sign-in screen offers registration', async () => {
    await accounts.openSignIn()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'signIn.heading'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByRole('button', {
        name: copy('authUi', 'mode.registerEntry'),
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('registration mode renders the password form', async () => {
    await accounts.openRegistration()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'signIn.registerHeading'),
        exact: true,
      }),
    ).toBeVisible()
    for (const key of [
      'register.usernameLabel',
      'register.emailLabel',
      'register.passwordLabel',
      'register.confirmPasswordLabel',
    ]) {
      await expect(registrationField(page, key)).toBeVisible()
    }
  })

  await test.step('an incomplete submission is refused in place', async () => {
    await accounts.submitRegistration()
    for (const key of [
      'register.usernameLabel',
      'register.emailLabel',
      'register.passwordLabel',
    ]) {
      await expect(registrationField(page, key)).toHaveAccessibleDescription(
        copy('uiKit', 'form.required'),
      )
    }
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'signIn.registerHeading'),
        exact: true,
      }),
    ).toBeVisible()
  })

  let verificationAfter = 0
  await test.step('a valid submission creates the account and signs in', async () => {
    await accounts.fillRegistration(account)
    verificationAfter = server.log.mark()
    await accounts.submitRegistration()
  })

  await test.step('the account page names the address and focuses the code field', async () => {
    const section = verificationSection(page)
    await expect(section).toBeVisible()
    await expect(
      section.getByText(
        copy('accountUi', 'verification.target', { email: account.email }),
        { exact: true },
      ),
    ).toBeVisible()

    const codeField = section.getByLabel(
      copy('accountUi', 'verification.codeLabel'),
      { exact: true },
    )
    await expect(codeField).toBeFocused()
    await expect(
      section.getByRole('button', {
        name: copy('accountUi', 'verification.resend'),
        exact: true,
      }),
    ).toBeDisabled()
    await expect(
      section.getByText(
        copyPattern('accountUi', 'verification.resendIn', { seconds: '\\d+' }),
      ),
    ).toBeVisible()

    await expect(verificationBanner(page, account.email)).toBeVisible()
  })

  await test.step('the code from the process log proves the address', async () => {
    const { code } = await waitForVerificationCode(
      server.log,
      'mail',
      account.email,
      { after: verificationAfter },
    )
    const section = verificationSection(page)
    await section
      .getByLabel(copy('accountUi', 'verification.codeLabel'), { exact: true })
      .fill(code)
    await section
      .getByRole('button', {
        name: copy('accountUi', 'verification.verify'),
        exact: true,
      })
      .click()

    await expect(
      page.getByRole('region', {
        name: copy('accountUi', 'verification.verifiedTitle'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByText(
        copy('accountUi', 'verification.verifiedDescription', {
          email: account.email,
        }),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(verificationBanner(page, account.email)).toHaveCount(0)
  })

  await test.step('the tenant home opens with the personal organization as the current organization', async () => {
    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()

    await openAccountMenu(page)
    await expect(
      page.getByRole('menu').getByText(account.username, { exact: true }),
    ).toBeVisible()
    await closeAccountMenu(page)
  })

  await test.step('a verified account creates a business organization and lands in it', async () => {
    const organization = accounts.unique('team')
    await page
      .getByRole('heading', {
        level: 2,
        name: copy('demo', 'home.cards.createOrganization.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('orgCreateUi', 'page.title'),
        exact: true,
      }),
    ).toBeVisible()

    await page
      .getByLabel(copy('orgCreateUi', 'fields.name'), { exact: true })
      .fill(organization.username)
    await page
      .getByLabel(copy('orgCreateUi', 'fields.displayName'), { exact: true })
      .fill(ORGANIZATION_DISPLAY_NAME)
    await page
      .getByLabel(copy('orgCreateUi', 'fields.description'), { exact: true })
      .fill('An organization created from the self-service page.')
    await page
      .getByRole('button', {
        name: copy('orgCreateUi', 'submit'),
        exact: true,
      })
      .click()

    await expect(page).toHaveURL(/\/organization\/admin\/profile$/)
    await openAccountMenu(page)
    await expect(
      page.getByRole('menu').getByText(ORGANIZATION_DISPLAY_NAME, {
        exact: true,
      }),
    ).toBeVisible()
    await closeAccountMenu(page)
  })

  await test.step('the new credentials sign in through the password form', async () => {
    await signOutToSignIn(page, accounts)
    await accounts.signIn({
      identifier: account.username,
      password: account.password,
    })
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('a duplicate username is refused on the form, in place', async () => {
    await signOutToSignIn(page, accounts)
    await accounts.openRegistration()
    const attempted = accounts.unique('dupname')
    await accounts.fillRegistration({ ...attempted, username: account.username })
    await accounts.submitRegistration()

    await expect(
      registrationField(page, 'register.usernameLabel'),
    ).toHaveAccessibleDescription(copy('authUi', 'register.usernameTaken'))
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'signIn.registerHeading'),
        exact: true,
      }),
    ).toBeVisible()
  })

  const refusedByEmail = accounts.unique('dupemail')
  await test.step('a duplicate email is refused on the form, in place', async () => {
    await accounts.fillRegistration({ ...refusedByEmail, email: account.email })
    await accounts.submitRegistration()

    await expect(registrationField(page, 'register.emailLabel'))
      .toHaveAccessibleDescription(copy('authUi', 'register.emailTaken'))
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'signIn.registerHeading'),
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('the username from the refused submission can register again', async () => {
    const fresh = accounts.unique('reuse')
    await accounts.fillRegistration({
      ...fresh,
      username: refusedByEmail.username,
    })
    await accounts.submitRegistration()
    await expect(verificationSection(page)).toBeVisible()
  })
})
