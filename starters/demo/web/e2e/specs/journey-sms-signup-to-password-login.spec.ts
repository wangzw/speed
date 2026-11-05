/**
 * The SMS first sign-in journey: an unregistered phone number signs in with a
 * code from the review log. A first login asks for the account it is about to
 * open -- a username, an optional display name, and a password with its
 * confirmation -- before it creates anything, so the account exists with that
 * username, password, display name and verified phone and lands in a personal
 * organization. The account holds two ways to sign in from the start: the phone
 * and the username with the password chosen at the step. Signing out and
 * signing back in with the username and password reaches the same personal
 * organization.
 *
 * Everything is driven through the rendered product with mouse and keyboard:
 * channel tabs, the country/number control, headings, labels, buttons, the
 * account entry and the standing cards. The single non-UI read is the SMS
 * verification code from the review process log (fixtures/log.ts): the review
 * carrier reaches no vendor, so the process log is where a person running the
 * review reads it.
 *
 * The username step also offers an optional display name, so the spec runs
 * both branches. This walkthrough types one, and the account entry names the
 * new account by it. A second test starts from a fresh review server and
 * leaves the field empty, and the account entry names that account after its
 * username.
 *
 * The spec does not claim the session-ending requirements: the account opens
 * with its password and the journey performs no such write.
 * REQ-account-023, REQ-account-026 and REQ-security-029 are covered by the
 * account-security-hardening journey instead. REQ-signin-012 and
 * REQ-signin-014 are left out for the reason the earlier walkthrough gave:
 * the spec only sends to an allowed prefix and never observes a refused region
 * or compares two numbers. REQ-signin-030 is left out because the spec's only
 * unauthenticated entry is the deployment root, which is the sign-in surface
 * itself.
 *
 * REQ-signup-047 is claimed for its positive branch only: this walkthrough
 * submits a legal, unused username with a password that meets the policy, and
 * never the absent, illegal or mismatched branches.
 *
 * Expected copy is read from the shipped en-US locale bundles through
 * helpers/i18n.ts, so a sentence that changes in the product changes here with
 * it rather than leaving a stale literal that still passes.
 */
// @journey journey-sms-signup-to-password-login.md
// @covers REQ-account-007, REQ-account-060, REQ-account-061, REQ-security-030, REQ-signin-006, REQ-signin-011, REQ-signup-012, REQ-signup-014, REQ-signup-015, REQ-signup-047, REQ-signup-051, REQ-signup-052, REQ-tenancy-022, REQ-tenancy-026
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import type { Accounts } from '../helpers/accounts'
import { expectAccountMenuName } from '../helpers/account-menu'
import {
  expandAccountRows,
  openAccountSection,
} from '../helpers/account-sections'
import { copy } from '../helpers/i18n'

/** The number the walkthrough uses: a Chinese mobile number in E.164 form. */
const PHONE_NATIONAL = '13800138000'
const PHONE_E164 = '+8613800138000'

/** The display name the main walkthrough types at the username step. */
const SIGNUP_DISPLAY_NAME = 'Phone Signup Person'

/** A password the review policy accepts and that matches no identifier. */
const SIGNUP_PASSWORD = 'e2e-Password-123'

/** The SMS channel's tab. */
function smsTab(page: Page) {
  return page.getByRole('tab', {
    name: copy('authUi', 'smsSignIn.title'),
    exact: true,
  })
}

/** The two-part phone control's country selector. */
function countryField(page: Page) {
  return page.getByRole('combobox', {
    name: copy('authUi', 'smsSignIn.countryLabel'),
    exact: true,
  })
}

/** The two-part phone control's local number field. */
function phoneField(page: Page) {
  return page.getByRole('textbox', {
    name: copy('authUi', 'smsSignIn.phoneLabel'),
    exact: true,
  })
}

/** The code step's verification-code field. */
function codeField(page: Page) {
  return page.getByRole('textbox', {
    name: copy('authUi', 'smsSignIn.codeLabel'),
    exact: true,
  })
}

/** The "code was sent" notice, naming the receiving number. */
function sentNotice(page: Page) {
  return page.getByText(
    copy('authUi', 'smsSignIn.sentNotice', { phone: PHONE_E164 }),
    { exact: true },
  )
}

/** The product's sign-in surface heading. */
function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

/** The tenant area's home heading, the rendered proof of a landed session. */
function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

/** One account section card, named by its own h2 title. */
function section(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

/** The security pane's sign-in-ways card. */
function identifiersSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.loginWays'))
}

/** The profile pane's phone card, where the bound number is listed. */
function phoneCard(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.phone'))
}

/** The tenant switcher's trigger, showing the current organization's name. */
function switcher(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('demo', 'organization.personal'),
    exact: true,
  })
}

/**
 * Switch the sign-in surface to the SMS channel and fill the phone control:
 * the country is picked by its calling code, then the local number is typed.
 */
async function fillSmsPhone(page: Page): Promise<void> {
  await smsTab(page).click()
  await expect(countryField(page)).toBeVisible()
  // The selector searches by country name, ISO code and calling code. "86"
  // narrows the list to the one country whose calling code is +86, so the
  // option is named by that code rather than by a country name another entry
  // could also contain.
  await countryField(page).fill('86')
  await page.getByRole('option', { name: /\+86$/ }).click()
  await phoneField(page).fill(PHONE_NATIONAL)
}

/** Request one code on the already-filled phone step. */
async function requestCode(page: Page): Promise<void> {
  await page
    .getByRole('button', {
      name: copy('authUi', 'smsSignIn.sendCode'),
      exact: true,
    })
    .click()
  await expect(sentNotice(page)).toBeVisible()
}

/** Answer the code step with the given code and submit the login. */
async function submitCode(page: Page, code: string): Promise<void> {
  await codeField(page).fill(code)
  await page
    .getByRole('button', {
      name: copy('authUi', 'smsSignIn.submit'),
      exact: true,
    })
    .click()
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
  await expect(signInHeading(page)).toBeVisible()
}

/**
 * Assert the account page lists the phone as a contact and both the phone and
 * the username as ways to sign in, and that no email address is chosen.
 */
async function expectBothWaysToSignIn(
  page: Page,
  username: string,
): Promise<void> {
  // The phone stays a contact: the profile pane's phone card lists it as a
  // verified number.
  await openAccountSection(page, 'profile')
  await expandAccountRows(page)
  const phone = phoneCard(page)
  await expect(phone.getByText(PHONE_E164, { exact: true })).toBeVisible()
  await expect(
    phone.getByText(copy('accountUi', 'identifiers.verified'), {
      exact: true,
    }),
  ).toBeVisible()

  // The security pane's sign-in ways name the phone and the username, and the
  // email group states that no address was chosen.
  await openAccountSection(page, 'security')
  await expandAccountRows(page)
  const identifiers = identifiersSection(page)
  await expect(
    identifiers.getByText(
      copy('accountUi', 'identifiers.usage.loginAs', {
        kind: copy('accountUi', 'identifiers.kind.phone'),
        value: PHONE_E164,
      }),
      { exact: true },
    ),
  ).toBeVisible()
  await expect(
    identifiers.getByText(
      copy('accountUi', 'identifiers.usage.loginAs', {
        kind: copy('accountUi', 'identifiers.kind.username'),
        value: username,
      }),
      { exact: true },
    ),
  ).toBeVisible()
  await expect(
    identifiers.getByText(copy('accountUi', 'identifiers.usage.emailNone'), {
      exact: true,
    }),
  ).toBeVisible()
}

test('sms-signup: a phone-code first sign-in becomes a username-and-password login', async ({
  demo,
}) => {
  const { page, server, accounts } = demo
  // The username the first login chooses and later signs in with.
  const username = accounts.unique('smsuser').username

  await test.step('the sign-in surface offers the SMS channel with the account-opening sentence', async () => {
    await accounts.openSignIn()
    await expect(signInHeading(page)).toBeVisible()
    // The sentence names a phone number, so it belongs under the SMS channel:
    // it appears once that channel is the one on screen. It says the number's
    // first sign-in creates an account.
    await smsTab(page).click()
    await expect(countryField(page)).toBeVisible()
    await expect(
      page.getByText(copy('authUi', 'mode.signInAccountHint'), {
        exact: true,
      }),
    ).toBeVisible()
  })

  let codeAfter = 0
  await test.step('a country and number are chosen and the code is sent', async () => {
    await fillSmsPhone(page)
    codeAfter = server.log.mark()
    await requestCode(page)
    await expect(codeField(page)).toBeVisible()
  })

  await test.step('the code asks for a username, an optional display name and a password, then the account opens', async () => {
    const { code } = await waitForVerificationCode(
      server.log,
      'sms',
      PHONE_E164,
      { after: codeAfter },
    )
    await submitCode(page, code)

    // The account-opening step asks for the username, the optional display
    // name and the password with its confirmation. Everything is answered in
    // one submission; the account exists only after it.
    const displayName = page.getByLabel(
      copy('authUi', 'register.displayNameLabel'),
      { exact: true },
    )
    await expect(displayName).toBeVisible()
    await expect(
      page.getByLabel(copy('authUi', 'passwordSignIn.passwordLabel'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByLabel(copy('authUi', 'register.confirmPasswordLabel'), {
        exact: true,
      }),
    ).toBeVisible()

    await accounts.answerPhoneSignup({
      username,
      password: SIGNUP_PASSWORD,
      displayName: SIGNUP_DISPLAY_NAME,
    })

    // The account is established and the session lands straight in the tenant
    // area.
    await expect(homeHeading(page)).toBeVisible()

    // The account entry is where the display name first shows: its accessible
    // name is the avatar's initials and the account's display name, so the
    // typed name is what the new account carries.
    await expectAccountMenuName(
      page.getByTestId('account-entry-trigger'),
      SIGNUP_DISPLAY_NAME,
    )

    // The header switcher names the personal organization as the current
    // organization, and its list holds that one row, disabled because it is
    // the current one.
    const trigger = switcher(page)
    await expect(trigger).toBeEnabled()
    await trigger.click()
    const rows = page.getByRole('menuitem')
    await expect(rows).toHaveCount(1)
    await expect(rows.first()).toHaveAccessibleName(
      copy('demo', 'organization.personal'),
    )
    await expect(rows.first()).toBeDisabled()
    await page.keyboard.press('Escape')
  })

  await test.step('the account lists the phone as a contact and both the phone and the username as sign-in ways', async () => {
    await expectBothWaysToSignIn(page, username)
  })

  await test.step('the account signs out from this device', async () => {
    await signOutToSignIn(page, accounts)
  })

  await test.step('the username and password sign in to the same personal organization', async () => {
    await accounts.signIn({
      identifier: username,
      password: SIGNUP_PASSWORD,
    })
    await expect(homeHeading(page)).toBeVisible()

    // The password sign-in reaches the account's one personal organization.
    const trigger = switcher(page)
    await expect(trigger).toBeEnabled()
    await trigger.click()
    await expect(page.getByRole('menuitem')).toHaveCount(1)
    await expect(page.getByRole('menuitem').first()).toHaveAccessibleName(
      copy('demo', 'organization.personal'),
    )
    await page.keyboard.press('Escape')
  })
})

test('sms-signup: a phone-code first sign-in with no display name takes the username', async ({
  demo,
}) => {
  const { page, server, accounts } = demo
  // Each test gets a review server of its own, so the walkthrough's phone
  // number is unregistered here too.
  const username = accounts.unique('smsunnamed').username

  let codeAfter = 0
  await test.step('a phone number is entered and its code requested', async () => {
    await accounts.openSignIn()
    await fillSmsPhone(page)
    codeAfter = server.log.mark()
    await requestCode(page)
    await expect(codeField(page)).toBeVisible()
  })

  await test.step('the account-opening step offers the optional display name, and it is left empty', async () => {
    const { code } = await waitForVerificationCode(
      server.log,
      'sms',
      PHONE_E164,
      { after: codeAfter },
    )
    await submitCode(page, code)

    // The step offers the display-name field, and this branch leaves it as it
    // found it: empty.
    const displayName = page.getByLabel(
      copy('authUi', 'register.displayNameLabel'),
      { exact: true },
    )
    await expect(displayName).toBeVisible()
    await expect(displayName).toHaveValue('')

    await accounts.answerPhoneSignup({
      username,
      password: SIGNUP_PASSWORD,
    })
  })

  await test.step('the account opens with the username as its display name', async () => {
    await expect(homeHeading(page)).toBeVisible()

    // The account entry is where the account's display name first shows; with
    // the field left empty it names the account after the chosen username.
    await expectAccountMenuName(
      page.getByTestId('account-entry-trigger'),
      username,
    )
  })
})
