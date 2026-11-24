// @journey journey-password-reset-sms.md
// @covers REQ-security-020, REQ-security-034, REQ-security-036, REQ-signin-036, REQ-deployment-039
import type { BrowserContext, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import { createAccounts, type Accounts } from '../helpers/accounts'
import { copy } from '../helpers/i18n'
import {
  backToSignInButton,
  completeReset,
  expectCodeRefused,
  openReset,
  readResetCode,
  requestResetCode,
  resetHeading,
  waitOutTheResetSecond,
} from '../helpers/password-reset'

const PHONE_NATIONAL = '13800138000'
const PHONE_E164 = '+8613800138000'

const GHOST_PHONE_E164 = '+8613900139000'

const NEW_PASSWORD = 'reset-Password-456'

const GHOST_PASSWORD = 'ghost-Password-456'

const WRONG_CODE = '000000'

function signInHeading(page: Page) {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

function homeHeading(page: Page) {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

function signInIdentifierField(page: Page) {
  return page.getByRole('textbox', {
    name: copy('authUi', 'passwordSignIn.identifierLabel'),
    exact: true,
  })
}

function signInPasswordField(page: Page) {
  return page.getByRole('textbox', {
    name: copy('authUi', 'passwordSignIn.passwordLabel'),
    exact: true,
  })
}

function phoneSection(page: Page) {
  return page.getByRole('region', {
    name: copy('accountUi', 'phone.title'),
    exact: true,
  })
}

async function provePhone(
  page: Page,
  log: Parameters<typeof waitForVerificationCode>[0],
): Promise<void> {
  await page
    .getByRole('region', {
      name: copy('demo', 'account.rows.label.phone'),
      exact: true,
    })
    .getByRole('button', { name: copy('demo', 'account.action.add'), exact: true })
    .click()
  const phone = phoneSection(page)
  const country = phone.getByRole('combobox', {
    name: copy('accountUi', 'phone.country.label'),
    exact: true,
  })
  await country.fill('86')
  await page.getByRole('option', { name: /\+86$/ }).click()
  await phone
    .getByRole('textbox', {
      name: copy('accountUi', 'phone.phone.label'),
      exact: true,
    })
    .fill(PHONE_NATIONAL)
  const phoneAfter = log.mark()
  await phone
    .getByRole('button', {
      name: copy('accountUi', 'phone.send'),
      exact: true,
    })
    .click()
  await expect(
    page.getByText(copy('accountUi', 'phone.sent', { phone: PHONE_E164 }), {
      exact: true,
    }),
  ).toBeVisible()
  const phoneCode = await waitForVerificationCode(log, 'sms', PHONE_E164, {
    after: phoneAfter,
  })
  await phone
    .getByRole('textbox', {
      name: copy('accountUi', 'phone.code.label'),
      exact: true,
    })
    .fill(phoneCode.code)
  await phone
    .getByRole('button', {
      name: copy('accountUi', 'phone.verify'),
      exact: true,
    })
    .click()
  await expect(
    page.getByText(copy('accountUi', 'phone.added', { phone: PHONE_E164 }), {
      exact: true,
    }),
  ).toBeVisible()
}

test('password-reset-sms: a phone-code reset writes a new password and ends the old sessions', async ({
  demo,
}) => {
  const { page, server, accounts } = demo
  const account = accounts.unique('reset-sms')

  const browser = page.context().browser()
  if (browser === null) {
    throw new Error('the demo page is not attached to a browser')
  }
  let otherContext: BrowserContext | undefined
  server.beforeStop(async () => {
    await otherContext?.close().catch(() => undefined)
  })

  try {
    await test.step('an unregistered number gets the same notice and one code refusal', async () => {
      await accounts.openSignIn()
      await openReset(page, 'sms')
      await requestResetCode(page, 'sms', GHOST_PHONE_E164)
      await expectCodeRefused(page, WRONG_CODE, GHOST_PASSWORD)
      await backToSignInButton(page).click()
      await expect(signInHeading(page)).toBeVisible()
    })

    await test.step('the account registers, proves its address and proves the phone', async () => {
      await accounts.registerAndVerify(account)
      await provePhone(page, server.log)
    })

    await test.step('a second device signs in and holds a session', async () => {
      otherContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      const other = await otherContext.newPage()
      await other.goto(`${server.url}/`)
      const otherAccounts: Accounts = createAccounts({ page: other, server })
      await otherAccounts.signIn({
        identifier: account.username,
        password: account.password,
      })
      await expect(homeHeading(other)).toBeVisible()
    })

    await test.step('the first device leaves its session and opens the reset surface', async () => {
      await accounts.signOut()
      await accounts.openSignIn()
      await openReset(page, 'sms')
      await expect(resetHeading(page)).toBeVisible()
    })

    const resetAfter = server.log.mark()
    await test.step('the number is submitted and the code is read from the log', async () => {
      await requestResetCode(page, 'sms', PHONE_E164)
      const code = await readResetCode(
        page,
        server.log,
        'sms',
        PHONE_E164,
        resetAfter,
      )
      await completeReset(page, code, NEW_PASSWORD)
    })

    await test.step('the other device session has ended', async () => {
      if (otherContext === undefined) {
        throw new Error('the second device was never opened')
      }
      const other = otherContext.pages()[0]
      if (other === undefined) {
        throw new Error('the second device has no page')
      }
      await other
        .getByTestId('account-entry-trigger')
        .click()
      await other
        .getByRole('menuitem', {
          name: copy('demo', 'account.sections.profile'),
          exact: true,
        })
        .click()
      await expect(
        other.getByRole('heading', {
          name: copy('authUi', 'sessionEnded.title'),
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('the new password signs in and the old one is refused', async () => {
      await waitOutTheResetSecond(page)
      await backToSignInButton(page).click()
      await expect(signInHeading(page)).toBeVisible()

      await accounts.signIn({
        identifier: account.username,
        password: NEW_PASSWORD,
      })
      await expect(homeHeading(page)).toBeVisible()

      await accounts.signOut()
      await accounts.openSignIn()
      await signInIdentifierField(page).fill(account.username)
      await signInPasswordField(page).fill(account.password)
      await page
        .getByRole('button', {
          name: copy('authUi', 'passwordSignIn.submit'),
          exact: true,
        })
        .click()
      await expect(
        page.getByText(copy('authUi', 'passwordSignIn.loginFailed'), {
          exact: true,
        }),
      ).toBeVisible()
    })
  } finally {
    await otherContext?.close().catch(() => undefined)
  }
})
