/**
 * The email password-reset journey: a person who forgot their password proves
 * their account's address with a code from the review mail carrier, writes a
 * new password without ever holding a session, and signs in with it.
 *
 * Everything is driven through the rendered product with mouse and keyboard:
 * the sign-in surface's forgot-password entry, the reset page's email channel,
 * its identifier, code, new-password and confirmation fields, and the account
 * menu's sign-out. The single non-UI read is the verification code from the
 * review process log (fixtures/log.ts): the review carrier reaches no relay, so
 * the process log is where a person running the review reads it.
 *
 * The account's own address is proved through the registration's verification
 * step, which sends a code to that address. The reset reuses the inbox resend
 * window that send holds (the window is per destination and shared across
 * purposes), so the first reset send is suppressed and the surface's resend
 * control is what releases it; `readResetCode` waits out that control rather
 * than a fixed sleep, and reads the code the resend delivered.
 *
 * An unregistered address is driven first: it gets the same "a code has been
 * sent if these details match an account" notice and, for any code, the one
 * collapsed refusal. That is the same-shape answer, observed without reading
 * whether a message left.
 *
 * REQ-security-035 is not claimed: the spec does not enrol a second factor, so
 * it cannot observe that the factor list is unchanged and the next login still
 * stops at the factor. REQ-security-037 is not claimed either: this deployment
 * publishes the reset, so the spec never meets the closed one. REQ-abuse-011 is
 * not claimed: the specs observe the shared resend window's suppression, not a
 * budget being exhausted.
 *
 * Expected copy is read from the shipped en-US locale bundles through
 * helpers/i18n.ts, so a sentence that changes in the product changes here with
 * it rather than leaving a stale literal that still passes.
 */
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

/** The password the reset writes. */
const NEW_PASSWORD = 'reset-Password-456'

/** The password an unregistered target's attempt tries to write. */
const GHOST_PASSWORD = 'ghost-Password-456'

/** Any code fails against a suppressed record; the value need only be shaped
 * like a code. */
const WRONG_CODE = '000000'

/** The sign-in surface's page heading. */
function signInHeading(page: Page) {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

/** The tenant area's home heading, reached after a successful sign-in. */
function homeHeading(page: Page) {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

/** The password sign-in form's identifier field. */
function signInIdentifierField(page: Page) {
  return page.getByRole('textbox', {
    name: copy('authUi', 'passwordSignIn.identifierLabel'),
    exact: true,
  })
}

/** The password sign-in form's password field. */
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

  // A second device holds an active session across the reset; its context is
  // the spec's own and is closed with the server.
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
      // The same sentence every identifier gets; no message was sent and the
      // test read none.
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
        identifier: account.email,
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
        .getByRole('banner')
        .getByTestId('user-menu-trigger')
        .click()
      await other
        .getByRole('menuitem', {
          name: copy('demo', 'userMenu.accountSettings'),
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
        identifier: account.email,
        password: NEW_PASSWORD,
      })
      await expect(homeHeading(page)).toBeVisible()

      // A device with no session is refused the old password.
      await accounts.signOut()
      await accounts.openSignIn()
      await signInIdentifierField(page).fill(account.email)
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
      ).toBeVisible({ timeout: 15_000 })
    })
  } finally {
    await otherContext?.close().catch(() => undefined)
  }
})
