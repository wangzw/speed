/**
 * The SMS first sign-in journey: an unregistered phone number signs in with a
 * code from the review log, opens an account implicitly, and lands in a
 * personal workspace. A second device signs in to the same account with a code
 * of its own, so the account holds two active sessions. On the first device
 * the one-time setup notice opens the setup page, a username is added, and a
 * password is set without a current one; that write completes setup and ends
 * the account's other sessions, so the second device's next request is answered
 * as an ended session. The first device then signs out and signs back in with
 * the username and password it just set, reaching the same personal workspace
 * with no setup notice.
 *
 * Everything is driven through the rendered product with mouse and keyboard:
 * channel tabs, the country/number control, headings, labels, buttons, list
 * rows, chips, links and the standing notices. The single non-UI read is the
 * SMS verification code from the review process log (fixtures/log.ts): the
 * review carrier reaches no vendor, so the process log is where a person
 * running the review reads it. Each device reads its own code, through its own
 * log cursor.
 *
 * The two devices request codes for the same number. The review assembly's
 * configuration lowers the SMS resend interval to the floor its setting accepts
 * (as it does the email one), so the second device can ask for its code within
 * the same walkthrough; the request is still refused until that window has
 * passed, and the spec presses the send control again until the product's own
 * "Code sent" notice appears. That is the backend's window observed through the
 * send's outcome, never a fixed sleep.
 *
 * The account's setup completion makes two writes that each revoke the
 * account's other sessions: adding the username and changing the password. The
 * second device's end is observed after the username is added and before the
 * password is set, which is the one point where the assertion can fail on the
 * add's revocation alone. REQ-account-026 is left out of `@covers` for the same
 * reason: with the other session already gone, the password write's revocation
 * has nothing left to change here, so the spec cannot fail on it alone.
 * REQ-signin-012 and REQ-signin-014 are left out too: the spec only sends to an
 * allowed prefix and never observes a refused region or compares two numbers,
 * so those claims would be wider than what the assertions exercise.
 *
 * Expected copy is read from the shipped en-US locale bundles through
 * helpers/i18n.ts, so a sentence that changes in the product changes here with
 * it rather than leaving a stale literal that still passes.
 */
// @journey journey-sms-signup-to-password-login.md
// @covers REQ-account-007, REQ-account-008, REQ-account-012, REQ-account-013, REQ-account-021, REQ-account-022, REQ-account-023, REQ-security-029, REQ-security-030, REQ-signin-006, REQ-signin-011, REQ-signin-030, REQ-signup-012, REQ-signup-014, REQ-signup-015, REQ-tenancy-022, REQ-tenancy-026
import type { BrowserContext, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import type { Accounts } from '../helpers/accounts'
import { copy } from '../helpers/i18n'

/** The number the walkthrough uses: a Chinese mobile number in E.164 form. */
const PHONE_NATIONAL = '13800138000'
const PHONE_E164 = '+8613800138000'

/** A password the review policy accepts and that matches no identifier. */
const SETUP_PASSWORD = 'e2e-Password-123'

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

/**
 * Request a code, pressing the send control again until the product answers
 * with its sent notice.
 *
 * The first device's code has already been requested for this number, so the
 * second device's request lands inside the SMS resend window and the server
 * refuses it. A person would press Send code again; this repeats that until the
 * product's own notice appears, which is the window passing, never a fixed
 * delay. The retries add no challenge rows (a refused send is refused before
 * one is written), so they do not spend a budget.
 */
async function requestCodeThroughResendWindow(page: Page): Promise<void> {
  const send = page.getByRole('button', {
    name: copy('authUi', 'smsSignIn.sendCode'),
    exact: true,
  })
  await expect(async () => {
    await send.click()
    await expect(sentNotice(page)).toBeVisible({ timeout: 2_000 })
  }).toPass({ timeout: 60_000, intervals: [4_000] })
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
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('demo', 'signIn.heading'),
      exact: true,
    }),
  ).toBeVisible()
}

/** The setup page's sign-in-identifiers section. */
function identifiersSection(page: Page) {
  return page.getByRole('region', {
    name: copy('accountUi', 'identifiers.title'),
    exact: true,
  })
}

/** The setup page's password section. */
function passwordSection(page: Page) {
  return page.getByRole('region', {
    name: copy('accountUi', 'password.title'),
    exact: true,
  })
}

/** The tenant switcher's trigger, showing the current organization's name. */
function switcher(page: Page) {
  return page.getByRole('button', {
    name: copy('demo', 'workspace.personal'),
    exact: true,
  })
}

test('sms-signup: a phone-code first sign-in becomes a username-and-password login', async ({
  demo,
}) => {
  const { page, server, accounts } = demo
  // The username the account adds on the setup page and later signs in with.
  const username = accounts.unique('smsuser').username

  // The second device: a browser context of its own, hence its own cookie jar
  // and its own session. It is opened at the review origin with the same
  // language and clock the config pins, and it is closed with the server so its
  // notification stream does not hold shutdown open.
  const browser = page.context().browser()
  if (browser === null) {
    throw new Error('the demo page is not attached to a browser')
  }
  let second: BrowserContext | undefined
  server.beforeStop(async () => {
    await second?.close().catch(() => undefined)
  })

  try {
    second = await browser.newContext({
      locale: 'en-US',
      timezoneId: 'UTC',
    })
    const other = await second.newPage()

    await test.step('the sign-in surface offers the SMS channel with the account-opening sentence', async () => {
      await accounts.openSignIn()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('demo', 'signIn.heading'),
          exact: true,
        }),
      ).toBeVisible()
      // The sentence is offered in sign-in mode where an SMS channel opens an
      // account; it names a phone number and the account that number creates.
      await expect(
        page.getByText(copy('authUi', 'mode.signInAccountHint'), {
          exact: true,
        }),
      ).toBeVisible()
    })

    let firstCodeAfter = 0
    await test.step('a country and number are chosen and the code is sent', async () => {
      await fillSmsPhone(page)
      firstCodeAfter = server.log.mark()
      await requestCode(page)
      await expect(codeField(page)).toBeVisible()
    })

    await test.step('the code opens the account and lands in the personal workspace', async () => {
      const { code } = await waitForVerificationCode(
        server.log,
        'sms',
        PHONE_E164,
        { after: firstCodeAfter },
      )
      await submitCode(page, code)

      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toBeVisible()

      // The header switcher names the personal workspace as the current
      // organization, and its list holds that one row, disabled because it is
      // the current one.
      const trigger = switcher(page)
      await expect(trigger).toBeEnabled()
      await trigger.click()
      const rows = page.getByRole('menuitem')
      await expect(rows).toHaveCount(1)
      await expect(rows.first()).toHaveAccessibleName(
        copy('demo', 'workspace.personal'),
      )
      await expect(rows.first()).toBeDisabled()
      await page.keyboard.press('Escape')

      // The one-time setup notice: a close control and the way to the setup
      // page.
      await expect(
        page.getByText(copy('demo', 'home.setupNotice.title'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        page.getByRole('link', {
          name: copy('demo', 'home.setupNotice.action'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        page.getByRole('button', {
          name: copy('demo', 'home.setupNotice.dismiss'),
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('the other device signs in with the same number and its own code', async () => {
      await other.goto(`${server.url}/`)
      await expect(
        other.getByRole('heading', {
          level: 1,
          name: copy('demo', 'signIn.heading'),
          exact: true,
        }),
      ).toBeVisible()
      await fillSmsPhone(other)

      // The second device's code is requested only after the first one, and
      // its cursor keeps the first device's code from answering this read.
      const secondCodeAfter = server.log.mark()
      await requestCodeThroughResendWindow(other)
      const { code } = await waitForVerificationCode(
        server.log,
        'sms',
        PHONE_E164,
        { after: secondCodeAfter },
      )
      await submitCode(other, code)

      await expect(
        other.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('the setup page lists only the verified phone and explains the password needs a login name', async () => {
      await page
        .getByRole('link', {
          name: copy('demo', 'home.setupNotice.action'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('demo', 'accountSetup.title'),
          exact: true,
        }),
      ).toBeVisible()

      // One identifier: the phone, named as verified. The add face offers a
      // username.
      const identifiers = identifiersSection(page)
      await expect(identifiers.getByRole('listitem')).toHaveCount(1)
      const phoneRow = identifiers
        .getByRole('listitem')
        .filter({ hasText: PHONE_E164 })
      await expect(phoneRow).toBeVisible()
      await expect(
        phoneRow.getByText(copy('accountUi', 'identifiers.kind.phone'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        phoneRow.getByText(copy('accountUi', 'identifiers.verified'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        identifiers.getByRole('button', {
          name: copy('accountUi', 'identifiers.add.open'),
          exact: true,
        }),
      ).toBeVisible()

      // The password section explains why no password can be set yet and
      // renders no form.
      const password = passwordSection(page)
      await expect(
        password.getByText(copy('accountUi', 'password.loginNameRequired'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        password.getByRole('textbox', {
          name: copy('accountUi', 'password.new.label'),
          exact: true,
        }),
      ).toHaveCount(0)
    })

    await test.step('adding a username lists it and reveals the password form', async () => {
      const identifiers = identifiersSection(page)
      await identifiers
        .getByRole('button', {
          name: copy('accountUi', 'identifiers.add.open'),
          exact: true,
        })
        .click()
      await page
        .getByRole('textbox', {
          name: copy('accountUi', 'identifiers.add.value.labelUsername'),
          exact: true,
        })
        .fill(username)
      await identifiers
        .getByRole('button', {
          name: copy('accountUi', 'identifiers.add.submitUsername'),
          exact: true,
        })
        .click()

      // The write is confirmed, the list gains the username row, and a
      // username carries no ownership chip.
      await expect(
        page.getByText(copy('accountUi', 'identifiers.add.added'), {
          exact: true,
        }),
      ).toBeVisible()
      const usernameRow = identifiers
        .getByRole('listitem')
        .filter({ hasText: username })
      await expect(usernameRow).toBeVisible()
      await expect(
        usernameRow.getByText(copy('accountUi', 'identifiers.kind.username'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        usernameRow.getByText(copy('accountUi', 'identifiers.verified'), {
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(
        usernameRow.getByText(copy('accountUi', 'identifiers.unverified'), {
          exact: true,
        }),
      ).toHaveCount(0)

      // The password form follows without a page reload: the same profile
      // read now names a login name.
      await expect(
        passwordSection(page).getByRole('textbox', {
          name: copy('accountUi', 'password.new.label'),
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('the identifier write ends the other device session', async () => {
      // Adding a login identifier rotates this session's credential and ends
      // the account's other sessions. The other device is still showing the
      // tenant area, so its next request is what discovers the end: opening
      // the account settings asks the server, which answers as an ended
      // session and swaps the shell for the sign-in-again screen.
      const bar = other.getByRole('banner')
      const named = bar.getByRole('button', {
        name: copy('demo', 'userMenu.accountMenu'),
        exact: true,
      })
      if ((await named.count()) > 0) {
        await named.click()
      } else {
        // The trigger names the display name when the account has one; the
        // account menu is the last menu trigger in the bar.
        await bar.locator('button[aria-haspopup="menu"]').last().click()
      }
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

    await test.step('a new password is set without a current one, and setup completes', async () => {
      const password = passwordSection(page)
      // The account holds no password yet, so no current password is asked
      // for.
      await expect(
        password.getByRole('textbox', {
          name: copy('accountUi', 'password.current.label'),
          exact: true,
        }),
      ).toHaveCount(0)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.new.label'),
          exact: true,
        })
        .fill(SETUP_PASSWORD)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.confirm.label'),
          exact: true,
        })
        .fill(SETUP_PASSWORD)
      await password
        .getByRole('button', {
          name: copy('accountUi', 'password.submitFirst'),
          exact: true,
        })
        .click()

      // The confirmation names the first password and the sign-out of the
      // other devices, and the page states setup is complete with the way
      // home.
      await expect(
        page.getByText(copy('accountUi', 'password.changedFirst'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('demo', 'accountSetup.completeTitle'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        page.getByText(copy('demo', 'accountSetup.complete'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        page.getByRole('link', {
          name: copy('demo', 'accountSetup.home'),
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('the home page no longer offers setup', async () => {
      await page
        .getByRole('link', {
          name: copy('demo', 'accountSetup.home'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        page.getByText(copy('demo', 'home.setupNotice.title'), {
          exact: true,
        }),
      ).toHaveCount(0)
    })

    await test.step('the account signs out from this device', async () => {
      await signOutToSignIn(page, accounts)
    })

    await test.step('the username and password sign in to the same personal workspace', async () => {
      await accounts.signIn({
        identifier: username,
        password: SETUP_PASSWORD,
      })
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toBeVisible()

      // The password sign-in reaches the account's one personal workspace,
      // and the setup notice does not return.
      const trigger = switcher(page)
      await expect(trigger).toBeEnabled()
      await expect(
        page.getByText(copy('demo', 'home.setupNotice.title'), {
          exact: true,
        }),
      ).toHaveCount(0)
      await trigger.click()
      await expect(page.getByRole('menuitem')).toHaveCount(1)
      await expect(page.getByRole('menuitem').first()).toHaveAccessibleName(
        copy('demo', 'workspace.personal'),
      )
      await page.keyboard.press('Escape')
    })
  } finally {
    await second?.close().catch(() => undefined)
  }
})
