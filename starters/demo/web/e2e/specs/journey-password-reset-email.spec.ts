// @journey journey-password-reset-email.md
// @covers REQ-security-020, REQ-security-034, REQ-security-036, REQ-signin-036
import type { BrowserContext, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
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

test('password-reset-email: an addressed code resets the password and ends the old sessions', async ({
  demo,
}) => {
  const { page, server, accounts } = demo
  const account = accounts.unique('reset-email')

  const browser = page.context().browser()
  if (browser === null) {
    throw new Error('the demo page is not attached to a browser')
  }
  let otherContext: BrowserContext | undefined
  server.beforeStop(async () => {
    await otherContext?.close().catch(() => undefined)
  })

  try {
    await test.step('an unregistered address gets the same notice and one code refusal', async () => {
      await accounts.openSignIn()
      await openReset(page, 'email')
      await requestResetCode(page, 'email', accounts.unique('ghost').email)
      await expectCodeRefused(page, WRONG_CODE, GHOST_PASSWORD)
      await backToSignInButton(page).click()
      await expect(signInHeading(page)).toBeVisible()
    })

    await test.step('the account registers and proves its address', async () => {
      await accounts.registerAndVerify(account)
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
      await openReset(page, 'email')
      await expect(resetHeading(page)).toBeVisible()
    })

    const resetAfter = server.log.mark()
    await test.step('the address is submitted and the code is read from the log', async () => {
      await requestResetCode(page, 'email', account.email)
      const code = await readResetCode(
        page,
        server.log,
        'mail',
        account.email,
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
