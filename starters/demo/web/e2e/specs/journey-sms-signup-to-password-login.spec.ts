// @journey journey-sms-signup-to-password-login.md
// @covers REQ-account-007, REQ-account-060, REQ-account-061, REQ-security-030, REQ-signin-006, REQ-signin-011, REQ-signup-012, REQ-signup-014, REQ-signup-015, REQ-signup-047, REQ-signup-051, REQ-signup-052, REQ-tenancy-022, REQ-tenancy-026
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import type { Accounts } from '../helpers/accounts'
import {
  closeAccountMenu,
  expectAccountMenuName,
  openAccountMenu,
} from '../helpers/account-menu'
import {
  expandAccountRows,
  openAccountSection,
} from '../helpers/account-sections'
import { copy } from '../helpers/i18n'

const PHONE_NATIONAL = '13800138000'
const PHONE_E164 = '+8613800138000'

const SIGNUP_DISPLAY_NAME = 'Phone Signup Person'

const SIGNUP_PASSWORD = 'e2e-Password-123'

function smsTab(page: Page) {
  return page.getByRole('tab', {
    name: copy('authUi', 'smsSignIn.title'),
    exact: true,
  })
}

function countryField(page: Page) {
  return page.getByRole('combobox', {
    name: copy('authUi', 'smsSignIn.countryLabel'),
    exact: true,
  })
}

function phoneField(page: Page) {
  return page.getByRole('textbox', {
    name: copy('authUi', 'smsSignIn.phoneLabel'),
    exact: true,
  })
}

function codeField(page: Page) {
  return page.getByRole('textbox', {
    name: copy('authUi', 'smsSignIn.codeLabel'),
    exact: true,
  })
}

function sentNotice(page: Page) {
  return page.getByText(
    copy('authUi', 'smsSignIn.sentNotice', { phone: PHONE_E164 }),
    { exact: true },
  )
}

function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

function section(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

function identifiersSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.loginWays'))
}

function phoneCard(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.phone'))
}


async function fillSmsPhone(page: Page): Promise<void> {
  await smsTab(page).click()
  await expect(countryField(page)).toBeVisible()
  await countryField(page).fill('86')
  await page.getByRole('option', { name: /\+86$/ }).click()
  await phoneField(page).fill(PHONE_NATIONAL)
}

async function requestCode(page: Page): Promise<void> {
  await page
    .getByRole('button', {
      name: copy('authUi', 'smsSignIn.sendCode'),
      exact: true,
    })
    .click()
  await expect(sentNotice(page)).toBeVisible()
}

async function submitCode(page: Page, code: string): Promise<void> {
  await codeField(page).fill(code)
  await page
    .getByRole('button', {
      name: copy('authUi', 'smsSignIn.submit'),
      exact: true,
    })
    .click()
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
  await expect(signInHeading(page)).toBeVisible()
}

async function expectBothWaysToSignIn(
  page: Page,
  username: string,
): Promise<void> {
  await openAccountSection(page, 'profile')
  await expandAccountRows(page)
  const phone = phoneCard(page)
  await expect(phone.getByText(PHONE_E164, { exact: true })).toBeVisible()
  await expect(
    phone.getByText(copy('accountUi', 'identifiers.verified'), {
      exact: true,
    }),
  ).toBeVisible()

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
  const username = accounts.unique('smsuser').username

  await test.step('the sign-in surface offers the SMS channel with the account-opening sentence', async () => {
    await accounts.openSignIn()
    await expect(signInHeading(page)).toBeVisible()
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

    await expect(homeHeading(page)).toBeVisible()
    await expect(homeHeading(page)).toBeFocused()

    await expectAccountMenuName(
      page.getByTestId('account-entry-trigger'),
      SIGNUP_DISPLAY_NAME,
    )

    const menu = await openAccountMenu(page)
    await expect(
      menu.getByText(SIGNUP_DISPLAY_NAME, { exact: true }),
    ).toBeVisible()
    await expect(menu.getByRole('menuitem', { name: SIGNUP_DISPLAY_NAME })).toHaveCount(0)
    await closeAccountMenu(page)
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
    await expect(homeHeading(page)).toBeFocused()

    const menu = await openAccountMenu(page)
    await expect(
      menu.getByText(SIGNUP_DISPLAY_NAME, { exact: true }),
    ).toBeVisible()
    await closeAccountMenu(page)
  })
})

test('sms-signup: a phone-code first sign-in with no display name takes the username', async ({
  demo,
}) => {
  const { page, server, accounts } = demo
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

    await expectAccountMenuName(
      page.getByTestId('account-entry-trigger'),
      username,
    )
  })
})
