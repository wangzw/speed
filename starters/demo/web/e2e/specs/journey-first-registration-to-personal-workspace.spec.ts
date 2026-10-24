/**
 * J1 -- a new visitor registers with a username, an email address and a
 * password, proves the address from the code the review mail carrier writes to
 * the process log, and reaches the tenant area with a personal workspace as the
 * current organization. The journey's occupancy branches then close the test:
 * the same username and the same email are each refused on the registration
 * form, in place, without creating an account, and the username a refused
 * submission used is still available afterwards. Between them the test proves
 * that the new credentials work on the password sign-in form, and an
 * incomplete registration is refused in place.
 *
 * Everything is driven through the rendered product with mouse and keyboard:
 * headings, labels, buttons, focus, visible text and the standing
 * pending-verification notice. The single non-UI read is the verification code
 * from the review process log (fixtures/log.ts): the review mail carrier reaches
 * no relay, so the process log is where a person running the review reads it.
 *
 * Expected copy is read from the shipped en-US locale bundles through
 * helpers/i18n.ts, so a sentence that changes in the product changes here with
 * it rather than leaving a stale literal that still passes.
 *
 * The PRD's per-requirement end-to-end field names this spec for
 * REQ-account-015, REQ-presentation-010, REQ-tenancy-027, REQ-tenancy-028,
 * REQ-signin-001, REQ-signin-004 and REQ-signup-024, but this spec does not
 * assert them: account verification status by source, locale-formatted
 * times, the personal owner's permission allow-list, the unverified-account
 * restriction, the anonymous discovery document, a surface derived from it,
 * and unverified sign-in are all outside the steps below. Those fields are
 * inaccurate; the ids this spec does exercise are the `@covers` line.
 */
// @journey journey-first-registration-to-personal-workspace.md
// @covers REQ-signin-006, REQ-signup-001, REQ-signup-002, REQ-signup-003, REQ-signup-004, REQ-signup-005, REQ-signup-008, REQ-signup-009, REQ-signup-010, REQ-signup-028, REQ-signup-029, REQ-account-010, REQ-account-016, REQ-account-017, REQ-account-019, REQ-account-020, REQ-tenancy-022, REQ-tenancy-026
import type { Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import type { Accounts } from '../helpers/accounts'
import { copy, copyPattern } from '../helpers/i18n'

/** The account page's email verification section. */
function verificationSection(page: Page) {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
    exact: true,
  })
}

/**
 * The standing pending-verification notice in the bar. Its accessible name is
 * its purpose followed by the visible message, so the address it names is part
 * of the name.
 */
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

/** Leave the session the way the account menu does, then return to sign-in. */
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

/** The registration form's four labelled fields. */
function registrationField(page: Page, key: string) {
  return page.getByLabel(copy('authUi', key), { exact: true })
}

test('first-registration: a new visitor reaches a personal workspace, and occupancy is refused', async ({
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
    // The host's page heading follows the mode; the form itself is the
    // password registration surface the deployment offers for an unknown
    // identifier.
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
    // The form requires username, email and password together. Submitting it
    // empty surfaces the required-field message on each of the three, and the
    // surface stays on registration: no account is created.
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

  // Taken before the form is submitted: the verification section sends its
  // first code after the account is accepted, and only that code may be used.
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

    // The section sends one code by itself and hands focus to the field the
    // code belongs in; the resend control is held for the interval and states
    // the remaining seconds.
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

    // The standing notice under the bar is present while the address is
    // unproved.
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
    // Proving the address lifts the restriction, so the standing notice goes.
    await expect(verificationBanner(page, account.email)).toHaveCount(0)
  })

  await test.step('the tenant home opens with the personal workspace as the current organization', async () => {
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
    const switcher = page.getByRole('button', {
      name: copy('demo', 'workspace.personal'),
      exact: true,
    })
    await expect(switcher).toBeVisible()
    await expect(switcher).toBeEnabled()
  })

  await test.step('the new credentials sign in through the password form', async () => {
    // Proving the address is not needed to sign in, and this is the one
    // password login the journey drives: sign out, then submit the username
    // and password on the sign-in surface. The session flip opens the tenant
    // area, so the home heading is the rendered proof that the enabled
    // account's password credential works.
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
    // The refusal stays on the registration surface; no account was created
    // and no session was started.
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'signIn.registerHeading'),
        exact: true,
      }),
    ).toBeVisible()
  })

  // The refused submission's username is reused below, so it is taken here.
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
    // The email conflict rolls the whole registration back: the username the
    // refused submission tried is not left reserved, and a later registration
    // may use it.
    const fresh = accounts.unique('reuse')
    await accounts.fillRegistration({
      ...fresh,
      username: refusedByEmail.username,
    })
    await accounts.submitRegistration()
    // A successful registration lands on the account page's verification
    // section, which is the rendered proof that a new account was created.
    await expect(verificationSection(page)).toBeVisible()
  })
})
