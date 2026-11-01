/**
 * The passkey user-verification journey -- an account that holds a passkey and
 * a second factor signs in with one user-verifying passkey ceremony and is not
 * asked for the second factor; an attempt made without user verification is
 * refused and leaves the browser on the sign-in surface; and an account whose
 * only factor is a passkey still signs in with a single ceremony.
 *
 * THE PASSKEY IS A BROWSER-DEVICE FIXTURE. Chromium's virtual authenticator
 * answers the product's own WebAuthn ceremony (the page still calls
 * `navigator.credentials.create`/`.get`); the harness attaches it over the one
 * CDP family the honesty gate allows. The same device enrols both factors'
 * passkey and answers the login assertion, and a fresh device serves the
 * second account so its resident credential is the only one the browser can
 * offer.
 *
 * THE SECOND FACTOR IS A TOTP AUTHENTICATOR, as in the account-security
 * hardening journey: a TOTP code is single use per thirty-second window
 * (REQ-security-012), so the spec waits for the next window before presenting
 * a second code rather than re-submitting one.
 *
 * WHAT THE NON-USER-VERIFIED ATTEMPT CAN AND CANNOT DRIVE. The product requires
 * user verification on the discoverable login assertion, so the refusing
 * behaviour has two halves: the browser will not produce an assertion whose
 * user-verified flag is clear against a relying party that requires the flag,
 * and the server refuses such an assertion if one is presented. The virtual
 * authenticator cannot produce the second half's input: with its user-verified
 * flag cleared it fails the ceremony itself with `NotAllowedError`, and no
 * assertion ever exists to submit. This spec therefore drives the rendered
 * outcome a person sees -- the login does not complete, the sign-in surface
 * stays up and shows the channel's failure text -- and the clause that a
 * submitted user-verification-less assertion is refused stays out of the
 * browser tier; the Go case `TestAPasskeyWithoutUserVerificationIsRefused`
 * holds that half. Nothing here is faked to stand in for it.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts.
 */
// @journey journey-passkey-user-verification.md
// @covers REQ-security-039, REQ-security-008, REQ-signin-024
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { createAccounts, type Accounts } from '../helpers/accounts'
import { expect, test } from '../fixtures/demo'
import {
  expandAccountRows,
  openAccountSection,
  selectAccountSection,
} from '../helpers/account-sections'
import { copy } from '../helpers/i18n'
import { totpCode } from '../helpers/totp'
import {
  addVirtualAuthenticator,
  type VirtualAuthenticator,
} from '../helpers/virtual-authenticator'

// Registration, two factor enrolments, several sign-ins and a second account
// assembly; the file-level bound covers fixture assembly as well as the
// journey, so a slow assembly reports the harness's own diagnostics.
test.describe.configure({ timeout: 300_000 })

/** The home page's heading, the landing after a completed sign-in. */
function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

/** The sign-in page's heading. */
function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

/** The account settings' profile pane heading, the page's one h1. */
function accountHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'account.sections.profile'),
    exact: true,
  })
}

/** One account section card, named by its own h2 title. */
function section(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

/** The security pane's two-step-verification card. */
function mfaSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.mfa'))
}

/** The sign-in surface's passkey entry. */
function passkeyEntry(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'signInFlow.factorKind.webauthn'),
    exact: true,
  })
}

/** The second-factor step's title, which a user-verifying passkey must skip. */
function factorStepTitle(page: Page): Locator {
  return page.getByText(copy('authUi', 'signInFlow.factorTitle'), {
    exact: true,
  })
}

/** The passkey channel's failure banner on the sign-in surface. */
function passkeyFailure(page: Page): Locator {
  return page.getByText(copy('authUi', 'signInFlow.passkeyCancelled'), {
    exact: true,
  })
}

/**
 * The TOTP windows this spec has presented a code in. A code accepted in one
 * window cannot be presented again in the same window (REQ-security-012).
 */
let lastCodeStep = -1

test.beforeEach(() => {
  lastCodeStep = -1
})

/**
 * A code for the next window this spec has not used, computed at the moment
 * it is typed. The wait is not a render state: it is the authenticator's own
 * thirty-second window, which a second submission in the same window would be
 * refused for.
 */
async function nextTotpCode(secret: string): Promise<string> {
  const stepMs = 30_000
  let step = Math.floor(Date.now() / stepMs)
  if (step <= lastCodeStep) {
    const resumeAt = (lastCodeStep + 1) * stepMs + 500
    const wait = resumeAt - Date.now()
    if (wait > 0) {
      await new Promise<void>((resolve) => setTimeout(resolve, wait))
    }
    step = Math.floor(Date.now() / stepMs)
  }
  lastCodeStep = step
  return totpCode(secret, new Date(step * stepMs))
}

/** Complete a password sign-in's second factor with the authenticator app. */
async function completeTotpFactor(page: Page, secret: string): Promise<void> {
  // The step belongs to the password channel, which renders in its own panel.
  // The enterprise entry sits on the same sign-in page and carries its own
  // "Continue", so every control of the step is addressed within that panel.
  const panel = page.getByRole('tabpanel', {
    name: copy('authUi', 'passwordSignIn.title'),
    exact: true,
  })
  const choice = panel.getByRole('button', {
    name: copy('authUi', 'signInFlow.factorKind.totp'),
    exact: true,
  })
  await expect(choice).toBeVisible()
  await choice.click()

  const codeField = panel.getByRole('textbox', {
    name: copy('authUi', 'smsSignIn.codeLabel'),
    exact: true,
  })
  await expect(codeField).toBeVisible()
  await codeField.fill(await nextTotpCode(secret))
  await panel
    .getByRole('button', {
      name: copy('authUi', 'signInFlow.submit'),
      exact: true,
    })
    .click()
  await expect(homeHeading(page)).toBeVisible({ timeout: 15_000 })
}

/**
 * Click the enrolment wizard's confirmation when a settled answer parks the
 * flow's ready step as one further confirmation. The wizard closes itself when
 * the answer settles `enrolled`, so the call is a no-op then.
 */
async function confirmSettledEnrolment(mfa: Locator): Promise<void> {
  const confirm = mfa.getByRole('button', {
    name: copy('accountUi', 'mfa.enroll.confirmLabel'),
    exact: true,
  })
  if ((await confirm.count()) > 0 && (await confirm.isVisible())) {
    await confirm.click()
  }
}

/** The shared secret the rendered authenticator setup URI carries. */
function secretOf(setupUri: string): string {
  const secret = new URL(setupUri).searchParams.get('secret')
  if (secret === null || secret === '') {
    throw new Error(
      'the authenticator setup URI the page rendered carries no secret',
    )
  }
  return secret
}

/**
 * Enrol the authenticator app on the open account page and answer with its
 * shared secret. The account must hold no active factor for this to run
 * without a step-up.
 */
async function enrolTotp(page: Page): Promise<string> {
  const mfa = mfaSection(page)
  await mfa
    .getByRole('button', {
      name: copy('accountUi', 'mfa.addAria', {
        method: copy('accountUi', 'mfa.factor.kind.totp'),
      }),
      exact: true,
    })
    .click()

  // The wizard shows the setup URI as a read-only field beside the code
  // field: the URI is the value an authenticator app would import.
  const uriField = mfa.getByRole('textbox', {
    name: copy('accountUi', 'flowStep.enrollTotpUriLabel'),
    exact: true,
  })
  await expect(uriField).toBeVisible()
  const secret = secretOf(await uriField.inputValue())

  const codeField = mfa.getByRole('textbox', {
    name: copy('accountUi', 'flowStep.codeLabel'),
    exact: true,
  })
  await expect(codeField).toBeVisible()
  await codeField.fill(await nextTotpCode(secret))
  await mfa
    .getByRole('button', {
      name: copy('accountUi', 'mfa.enroll.confirmLabel'),
      exact: true,
    })
    .click()
  // The accepted code leaves the code form; a settled flow then parks its
  // ready step as one further confirmation of the same control.
  await expect(codeField).toHaveCount(0, { timeout: 15_000 })
  await confirmSettledEnrolment(mfa)

  await expect(
    mfa.getByText(
      copy('accountUi', 'mfa.enroll.enrolled', {
        factor: copy('accountUi', 'mfa.factor.kind.totp'),
      }),
      { exact: true },
    ),
  ).toBeVisible()
  return secret
}

/**
 * Enrol a passkey on the open account page. The wizard's confirmation carries
 * the user activation the creation ceremony needs, and the virtual
 * authenticator answers it.
 */
async function enrolPasskey(page: Page): Promise<void> {
  const mfa = mfaSection(page)
  await mfa
    .getByRole('button', {
      name: copy('accountUi', 'mfa.addAria', {
        method: copy('accountUi', 'mfa.factor.kind.webauthn'),
      }),
      exact: true,
    })
    .click()
  await expect(
    mfa.getByText(copy('accountUi', 'flowStep.passkeyRegisterPrompt'), {
      exact: true,
    }),
  ).toBeVisible()
  await mfa
    .getByRole('button', {
      name: copy('accountUi', 'mfa.enroll.confirmLabel'),
      exact: true,
    })
    .click()
  // The ceremony settles and the step leaves; a parked ready step is one
  // further confirmation of the same control.
  await expect(
    mfa.getByText(copy('accountUi', 'flowStep.passkeyRegisterPrompt'), {
      exact: true,
    }),
  ).toHaveCount(0, { timeout: 20_000 })
  await confirmSettledEnrolment(mfa)
  await expect(
    mfa.getByText(
      copy('accountUi', 'mfa.enroll.enrolled', {
        factor: copy('accountUi', 'mfa.factor.kind.webauthn'),
      }),
      { exact: true },
    ),
  ).toBeVisible({ timeout: 20_000 })
  await expect(
    mfa
      .getByText(copy('accountUi', 'mfa.factor.kind.webauthn'), {
        exact: true,
      })
      .first(),
  ).toBeVisible()
}

/**
 * Choose the passkey entry on the sign-in surface. The assertion ceremony
 * starts as soon as its step arrives, so this click is the whole interaction;
 * the caller observes where the login lands.
 */
async function choosePasskeySignIn(page: Page): Promise<void> {
  const entry = passkeyEntry(page)
  await expect(entry).toBeVisible()
  await entry.click()
}

test('passkey-user-verification: a user-verifying passkey signs in without a second factor', async ({
  demo,
  browser,
}) => {
  const { page, server } = demo
  const accounts: Accounts = demo.accounts
  const account = accounts.unique('passkey-uv')
  const passkeyOnly = accounts.unique('passkey-only')
  let authenticator: VirtualAuthenticator | undefined
  let otherContext: BrowserContext | undefined
  let otherAuthenticator: VirtualAuthenticator | undefined
  let otherPage: Page | undefined
  let secret = ''

  try {
    await test.step('precondition: an ordinary account is registered and its email address is proved', async () => {
      await accounts.registerAndVerify(account)
      await expect(accountHeading(page)).toBeVisible()
      // The passkey ceremonies are answered by Chromium's virtual
      // authenticator, attached to this page's target.
      authenticator = await addVirtualAuthenticator(page.context(), page)
    })

    await test.step('step 1: the account enrols the authenticator app as its second factor', async () => {
      await selectAccountSection(page, 'security')
      await expandAccountRows(page)
      secret = await enrolTotp(page)
      // The one active factor puts the word on the method row and on the
      // factor row.
      await expect(
        mfaSection(page).getByText(
          copy('accountUi', 'mfa.factor.status.active'),
          { exact: true },
        ),
      ).toHaveCount(2)
    })

    await test.step('step 2: a fresh password sign-in proves the second factor, which makes the session recent enough to write the passkey', async () => {
      await accounts.signOut()
      await accounts.signIn({
        identifier: account.username,
        password: account.password,
      })
      await completeTotpFactor(page, secret)
      await openAccountSection(page, 'security')
      await expandAccountRows(page)
    })

    await test.step('step 3: the account enrols a discoverable passkey beside the second factor', async () => {
      await enrolPasskey(page)
      // Two active factors, each read on its method row and its factor row.
      await expect(
        mfaSection(page).getByText(
          copy('accountUi', 'mfa.factor.status.active'),
          { exact: true },
        ),
      ).toHaveCount(4)
    })

    await test.step('step 4: choosing the passkey entry completes the login with user verification and no second factor', async () => {
      await accounts.signOut()
      await accounts.openSignIn()
      await choosePasskeySignIn(page)
      // The user-verified assertion is two factors already, so the login
      // lands in the application without a factor step.
      await expect(homeHeading(page)).toBeVisible({ timeout: 30_000 })
      await expect(factorStepTitle(page)).toHaveCount(0)
      await expect(signInHeading(page)).toHaveCount(0)
    })

    await test.step('step 5: an attempt made without user verification is refused and stays on the sign-in surface', async () => {
      await accounts.signOut()
      await accounts.openSignIn()
      // Clearing the authenticator's user-verified flag is how the device
      // expresses an attempt that does not verify the user. The browser
      // refuses to produce the required assertion and the channel renders
      // its failure.
      await authenticator?.setUserVerified(false)
      await choosePasskeySignIn(page)
      await expect(passkeyFailure(page)).toBeVisible({ timeout: 30_000 })
      await expect(homeHeading(page)).toHaveCount(0)
      await expect(signInHeading(page)).toBeVisible()
      // The login must not fall back to a first factor whose second factor
      // is then asked for: that would complete the ceremony as a weaker
      // login instead of refusing it.
      await expect(factorStepTitle(page)).toHaveCount(0)
      await authenticator?.setUserVerified(true)
    })

    await test.step('step 6: an account whose only factor is a passkey signs in with one ceremony', async () => {
      // A device of its own, so the second account's resident credential is
      // the only one this authenticator can offer.
      const options = {
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      }
      otherContext = await browser.newContext(options)
      otherPage = await otherContext.newPage()
      const otherAccounts = createAccounts({ page: otherPage, server })
      await otherAccounts.registerAndVerify(passkeyOnly)
      await expect(accountHeading(otherPage)).toBeVisible()
      otherAuthenticator = await addVirtualAuthenticator(otherContext, otherPage)

      await selectAccountSection(otherPage, 'security')
      await expandAccountRows(otherPage)
      await enrolPasskey(otherPage)
      // The account holds exactly one factor, the passkey; its state reads on
      // the method row and on the factor row.
      await expect(
        mfaSection(otherPage).getByText(
          copy('accountUi', 'mfa.factor.status.active'),
          { exact: true },
        ),
      ).toHaveCount(2)

      await otherAccounts.signOut()
      await otherAccounts.openSignIn()
      await choosePasskeySignIn(otherPage)
      await expect(homeHeading(otherPage)).toBeVisible({ timeout: 30_000 })
      await expect(factorStepTitle(otherPage)).toHaveCount(0)
      await expect(signInHeading(otherPage)).toHaveCount(0)
    })
  } finally {
    // The passkey devices and the second context are the spec's own; closing
    // them keeps a failed run from holding the notification stream open.
    await otherAuthenticator?.close().catch(() => undefined)
    await otherContext?.close().catch(() => undefined)
    await authenticator?.close().catch(() => undefined)
  }
})
