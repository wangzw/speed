/**
 * The SMS first sign-in journey: an unregistered phone number signs in with a
 * code from the review log. A first login asks for the username every account is
 * opened with before it creates the account, so the account exists with that
 * username and the verified phone and lands in a personal workspace. A second
 * device signs in to the same account with a code of its own, so the account
 * holds two active sessions. On the first device the one-time setup notice opens
 * the setup page, where a password is set without a current one; that write
 * completes setup and ends the account's other sessions, so the second device's
 * next request is answered as an ended session. The first device then signs out
 * and signs back in with the username and password, reaching the same personal
 * workspace with no setup notice.
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
 * Setting the first password rotates this device's session and revokes the
 * account's other sessions, which is the one write this spec observes ending
 * the second device. REQ-signin-012 and REQ-signin-014 are left out of
 * `@covers`: the spec only sends to an allowed prefix and never observes a
 * refused region or compares two numbers, so those claims would be wider than
 * what the assertions exercise. REQ-signin-030 (an unauthenticated visit to a
 * protected page renders the sign-in surface and continues in place after
 * sign-in) is left out for the same reason: the spec's only unauthenticated
 * entry is the deployment root, which is the sign-in surface itself, and after
 * a session ends it asserts the ended-session surface, not an in-place
 * continuation. A product that rendered an error page for a protected route, or
 * that stopped at the sign-in surface after signing in, would leave every
 * observation here unchanged.
 *
 * Expected copy is read from the shipped en-US locale bundles through
 * helpers/i18n.ts, so a sentence that changes in the product changes here with
 * it rather than leaving a stale literal that still passes.
 */
// @journey journey-sms-signup-to-password-login.md
// @covers REQ-account-007, REQ-account-021, REQ-account-023, REQ-account-026, REQ-security-029, REQ-security-030, REQ-signin-006, REQ-signin-011, REQ-signup-012, REQ-signup-014, REQ-signup-015, REQ-tenancy-022, REQ-tenancy-026
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
 *
 * The retry stop condition is the surface's own state transition, not the
 * notice alone: once an accepted send replaces the phone step with the code
 * step, the send control is gone, so a later attempt must not click a detached
 * node. Each attempt first checks whether the code step or the notice has
 * already arrived and stops; otherwise it clicks the send control and waits the
 * tier's uniform five seconds for the notice. A refusal leaves the phone step
 * in place with no notice, which is what the next attempt retries.
 */
async function requestCodeThroughResendWindow(page: Page): Promise<void> {
  const send = page.getByRole('button', {
    name: copy('authUi', 'smsSignIn.sendCode'),
    exact: true,
  })
  const step = codeField(page)
  await expect(async () => {
    // A successful send has already moved the surface on; stop rather than
    // click the control it unmounted.
    if (await step.isVisible()) {
      return
    }
    if (!(await send.isVisible())) {
      // The control vanished between polls: the transition is under way, so
      // let the state assertion after the loop observe it.
      return
    }
    await send.click()
    await expect(sentNotice(page)).toBeVisible({ timeout: 5_000 })
  }).toPass({ timeout: 60_000, intervals: [4_000] })
  // A resolved retry means an accepted send; the code step is the visible
  // consequence of it.
  await expect(step).toBeVisible()
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
  // The username the first login chooses and later signs in with.
  const username = accounts.unique('smsuser').username

  // The second device: a browser context of its own, hence its own cookie jar
  // and its own session. It is opened at the review origin with the same
  // language and clock the config pins and the running page's viewport, so the
  // two devices share every pin a context created by hand must repeat; it is
  // closed with the server so its notification stream does not hold shutdown
  // open.
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
      viewport: page.viewportSize(),
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
      // The sentence names a phone number, so it belongs under the SMS
      // channel: it appears once that channel is the one on screen. It says
      // the number's first sign-in creates an account.
      await smsTab(page).click()
      await expect(countryField(page)).toBeVisible()
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

    await test.step('the code asks for a username, then the account opens in the personal workspace', async () => {
      const { code } = await waitForVerificationCode(
        server.log,
        'sms',
        PHONE_E164,
        { after: firstCodeAfter },
      )
      await submitCode(page, code)

      // A first login that would open an account stops at the username step:
      // nothing is written before the name the account will carry arrives.
      await accounts.answerSignupUsername(username)

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

    await test.step('the setup page lists the verified phone and the chosen username, and offers the password form', async () => {
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

      // Three rows: the verified phone and the username the first sign-in
      // chose are sign-in ways, and the phone is a contact as well --
      // registering it is what makes it one, and the same record appears in
      // both lists while it signs in.
      const identifiers = identifiersSection(page)
      await expect(identifiers.getByRole('listitem')).toHaveCount(3)
      const signInWays = identifiers.getByRole('region', {
        name: copy('accountUi', 'identifiers.groups.signIn'),
        exact: true,
      })
      const contacts = identifiers.getByRole('region', {
        name: copy('accountUi', 'identifiers.groups.contact'),
        exact: true,
      })

      const phoneRow = signInWays
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

      // The same number is a contact too, one row in that list.
      const contactPhoneRow = contacts
        .getByRole('listitem')
        .filter({ hasText: PHONE_E164 })
      await expect(contactPhoneRow).toBeVisible()
      await expect(
        contactPhoneRow.getByText(copy('accountUi', 'identifiers.verified'), {
          exact: true,
        }),
      ).toBeVisible()

      const usernameRow = signInWays
        .getByRole('listitem')
        .filter({ hasText: username })
      await expect(usernameRow).toBeVisible()
      await expect(
        usernameRow.getByText(copy('accountUi', 'identifiers.kind.username'), {
          exact: true,
        }),
      ).toBeVisible()
      // A username proves no ownership, so it carries no ownership chip, and
      // it is never a contact.
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
      await expect(contacts.getByText(username, { exact: true })).toHaveCount(0)

      // The username is a login name, so the password form is offered without
      // a page reload or a further identifier write.
      await expect(
        passwordSection(page).getByRole('textbox', {
          name: copy('accountUi', 'password.new.label'),
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

      // The password write rotates this session's credential and ends the
      // account's other sessions. The other device is still showing the tenant
      // area, so its next request is what discovers the end: opening the
      // account settings asks the server, which answers as an ended session and
      // swaps the shell for the sign-in-again screen.
      //
      // The account menu trigger is addressed by its own stable identifier,
      // not by its accessible name (which is the display name when the account
      // has one) and not by its position among the bar's other menus.
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
