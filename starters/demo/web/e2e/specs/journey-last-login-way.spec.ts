/**
 * The last-sign-in-way journey: an account opened by a phone-code first login
 * holds one way to sign in that is not a passkey -- a social identity it bound
 * afterwards. A passkey is enrolled, which the journey proves is not one of the
 * three ways the rule counts (a password credential, a proved phone number, an
 * external identity). Unlinking the external identity while it is the only such
 * way asks for fresh proof; the owner answers the challenge in the page with
 * the passkey just enrolled, and the unlink is then refused: the dedicated
 * sentence is rendered, and the binding stays listed. The owner then sets a
 * password from the same account page; the external identity is no longer the
 * only non-passkey way, and the same unlink now succeeds.
 *
 * WHY THE ACCOUNT IS OPENED BY A PHONE CODE. A social first login now opens an
 * account with a password and the provider's address, so it can no longer
 * produce the state this journey starts from: no password, no email, one
 * external identity. A phone-code first login opens it instead: the verified
 * number is a sign-in way and no contact, the account asks for a username
 * before it exists, and it writes neither a password nor an email address.
 *
 * THE PHONE IS THE LAST WAY IN UNTIL THE SOCIAL IDENTITY IS BOUND. At the
 * account page the first removal attempt names the phone. The account module
 * refuses it as the last way that is not a passkey, but it answers the freshness
 * check first, so the page opens the step-up dialog; the owner proves with the
 * passkey the sign-in enrolled. Binding the social identity then makes the same
 * removal allowed, which is the conditional the journey starts from.
 *
 * THE DEPLOYMENT REQUIRES A FACTOR, AND THAT IS LOAD-BEARING. A step-up accepts
 * only a factor whose activation predates the session's authentication time, and
 * only one whose channel differs from the channels the session already proved.
 * A login that proves an existing factor as its second factor cannot be
 * strengthened by that same factor afterwards. The journey therefore needs a
 * login whose session records one factor: the sign-in flow registers the
 * passkey during the login (an enrolling login), and a factor registered during
 * the authentication does not strengthen it. The sign-in that opens the account
 * is such a login; so is the later social sign-in, which is why the passkey
 * enrolled at the first login is removed before it -- the social sign-in must
 * enroll the journey's passkey itself. Nothing signs the session out and back in
 * after that; the unlink's step-up happens inside the page.
 *
 * EVERYTHING IS DRIVEN THROUGH THE UI. The single non-UI read is the SMS
 * verification code from the review process log (fixtures/log.ts): the review
 * carrier reaches no vendor, so the process log is where a person running the
 * review reads it. The social round trip is the harness's real OpenID Connect
 * provider: the browser leaves for its authorization page and clicks its
 * control, so no route is intercepted and no response is faked. The passkey is
 * Chromium's virtual authenticator (helpers/virtual-authenticator.ts): the page
 * still calls `navigator.credentials.create`/`.get`, and the fixture answers as
 * a platform authenticator would.
 *
 * WHAT IS ASSERTED. The refusal is the dedicated last-login-method answer, and
 * this spec asserts its shipped sentence: it says the account has no other way
 * to sign in and names setting a password or linking another account as the
 * remedy. It also asserts that the binding survives the refusal, that the
 * account's contact list stays empty, and that the provider's address is not
 * written into the account.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts.
 */
// @journey journey-last-login-way.md
// @covers REQ-security-040, REQ-security-013, REQ-account-061, REQ-account-067
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { identityProviderUser } from '../fixtures/identity-provider'
import { waitForVerificationCode } from '../fixtures/log'
import {
  expandAccountRows,
  openAccountSection,
  selectAccountSection,
} from '../helpers/account-sections'
import { copy } from '../helpers/i18n'
import { approveSocialSignIn } from '../helpers/social'
import {
  addVirtualAuthenticator,
  type VirtualAuthenticator,
} from '../helpers/virtual-authenticator'

// The phone-code round trip, two social round trips, the passkey ceremonies and
// the step-up run inside this file's per-test bound; fixture assembly included.
test.describe.configure({ timeout: 300_000 })

/** The number the first sign-in uses: a Chinese mobile number in E.164 form. */
const PHONE_NATIONAL = '13800138000'
const PHONE_E164 = '+8613800138000'

/** The password the account sets after the refused unlink. */
const NEW_PASSWORD = 'e2e-Password-789'

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

/** The security pane's sign-in-ways card. */
function identifiersSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.loginWays'))
}

/** The security pane's two-step-verification card. */
function mfaSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.mfa'))
}

/** The security pane's social accounts card. */
function bindingsSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.social'))
}

/** The profile pane's phone card, where a bound number is managed. */
function phoneCard(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.phone'))
}

/** The bound Google row. */
function googleRow(page: Page): Locator {
  return bindingsSection(page)
    .getByRole('listitem')
    .filter({ hasText: copy('accountUi', 'bindings.provider.google') })
}

/** The row's unlink action, named after the provider it unbinds. */
function googleUnlink(page: Page): Locator {
  return bindingsSection(page).getByRole('button', {
    name: copy('accountUi', 'bindings.unbindAriaWithProvider', {
      provider: copy('accountUi', 'bindings.provider.google'),
    }),
    exact: true,
  })
}

/** The sign-in surface's Google channel. */
function googleButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'social.provider.google'),
    exact: true,
  })
}

/** The SMS channel's tab. */
function smsTab(page: Page): Locator {
  return page.getByRole('tab', {
    name: copy('authUi', 'smsSignIn.title'),
    exact: true,
  })
}

/** The two-part phone control's country selector. */
function countryField(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('authUi', 'smsSignIn.countryLabel'),
    exact: true,
  })
}

/** The two-part phone control's local number field. */
function phoneField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'smsSignIn.phoneLabel'),
    exact: true,
  })
}

/** The code step's verification-code field. */
function codeField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'smsSignIn.codeLabel'),
    exact: true,
  })
}

/** The "code was sent" notice, naming the receiving number. */
function sentNotice(page: Page): Locator {
  return page.getByText(
    copy('authUi', 'smsSignIn.sentNotice', { phone: PHONE_E164 }),
    { exact: true },
  )
}

/** The SMS channel's mounted panel. */
function smsPanel(page: Page): Locator {
  return page.getByRole('tabpanel', {
    name: copy('authUi', 'smsSignIn.title'),
    exact: true,
  })
}

/** The sign-in flow's passkey kind, on the factor and the enroll steps alike. */
function passkeyKind(scope: Page | Locator): Locator {
  return scope.getByRole('button', {
    name: copy('authUi', 'signInFlow.factorKind.webauthn'),
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

/**
 * Register a passkey on the enroll step the sign-in flow parks on. An enrolling
 * login's registration step stays parked until the viewer clicks, so the prompt
 * and then the flow's own submit are the two interactions.
 */
async function registerPasskeyOnSignIn(scope: Page | Locator): Promise<void> {
  const choice = passkeyKind(scope)
  await expect(choice).toBeVisible({ timeout: 30_000 })
  await choice.click()
  const prompt = scope.getByText(
    copy('authUi', 'signInFlow.passkeyRegisterPrompt'),
    { exact: true },
  )
  await expect(prompt).toBeVisible({ timeout: 15_000 })
  await scope
    .getByRole('button', {
      name: copy('authUi', 'signInFlow.submit'),
      exact: true,
    })
    .click()
  await expect(prompt).toHaveCount(0, { timeout: 20_000 })
}

/** Confirm one danger dialog by the label the caller's section supplies. */
async function confirmDialog(
  page: Page,
  title: string,
  confirmLabel: string,
): Promise<void> {
  const dialog = page.getByRole('dialog', { name: title, exact: true })
  await expect(dialog).toBeVisible()
  await dialog
    .getByRole('button', { name: confirmLabel, exact: true })
    .click()
}

/** Clear the shared step-up challenge with a passkey the account holds. */
async function proveWithPasskey(page: Page): Promise<void> {
  const dialog = page.getByRole('dialog', {
    name: copy('accountUi', 'mfa.stepUp.title'),
    exact: true,
  })
  await expect(dialog).toBeVisible({ timeout: 15_000 })
  const passkey = dialog.getByRole('button', {
    name: copy('accountUi', 'mfa.factor.kind.webauthn'),
    exact: true,
  })
  await expect(passkey).toBeVisible({ timeout: 15_000 })
  await passkey.click()

  // Choosing the passkey starts the assertion ceremony on arrival, so there
  // is no start control. The prompt leaving is what distinguishes the parked
  // `complete` step from the ceremony still running.
  await expect(
    dialog.getByText(copy('accountUi', 'flowStep.passkeyPrompt'), {
      exact: true,
    }),
  ).toHaveCount(0, { timeout: 30_000 })
  const parked = dialog.getByRole('button', {
    name: copy('accountUi', 'mfa.stepUp.confirmLabel'),
    exact: true,
  })
  await expect(parked).toHaveCount(1)
  await parked.click()
  await expect(dialog).toHaveCount(0, { timeout: 30_000 })
}

/** The sign-in-ways card's sentence naming the phone it signs in with. */
function phoneSignInSentence(page: Page): Locator {
  return identifiersSection(page).getByText(
    copy('accountUi', 'identifiers.usage.loginAs', {
      kind: copy('accountUi', 'identifiers.kind.phone'),
      value: PHONE_E164,
    }),
    { exact: true },
  )
}

/**
 * Remove the bound phone number from the profile pane's phone card. The card
 * owns the number's lifecycle: its Delete action opens the confirmation, and a
 * refused removal renders the card's failure sentence beside it.
 */
async function deletePhone(page: Page): Promise<void> {
  await openAccountSection(page, 'profile')
  await expandAccountRows(page)
  await phoneCard(page)
    .getByRole('button', {
      name: copy('demo', 'account.action.delete'),
      exact: true,
    })
    .click()
  await confirmDialog(
    page,
    copy('demo', 'account.phoneRemove.removeTitle'),
    copy('demo', 'account.phoneRemove.removeConfirm'),
  )
}

/** The passkey row's remove action, named after the kind it removes. */
function passkeyRemove(page: Page): Locator {
  return mfaSection(page).getByRole('button', {
    name: copy('accountUi', 'mfa.removeAria', {
      factor: copy('accountUi', 'mfa.factor.kind.webauthn'),
    }),
    exact: true,
  })
}

// The journey drives the provider's real round trip, so its server starts the
// local identity provider; the deployment requires a factor, which is what makes
// both sign-ins park at a registration step and keeps their sessions
// single-factor.
test.use({ socialIdentityProvider: true, mfaRequired: true })

test('last-login-way: removing the last way that is not a passkey is refused', async ({
  demo,
}) => {
  const { page, server, accounts } = demo
  const username = accounts.unique('lastway').username
  const googleLabel = copy('accountUi', 'bindings.provider.google')
  let authenticator: VirtualAuthenticator | undefined

  try {
    await test.step('a phone-code first login opens the account and registers the required passkey', async () => {
      await accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()
      await fillSmsPhone(page)

      // The cursor is taken before the send: only a code written for this
      // request answers this step.
      const codeAfter = server.log.mark()
      await requestCode(page)
      await expect(codeField(page)).toBeVisible()
      const { code } = await waitForVerificationCode(
        server.log,
        'sms',
        PHONE_E164,
        { after: codeAfter },
      )
      await submitCode(page, code)

      // A first login that would open an account asks for the username the
      // account is opened with before anything is written.
      await accounts.answerSignupUsername(username)

      // The deployment requires a factor, so the flow parks at the enroll step
      // inside the SMS channel. The passkey registered here belongs to the
      // login, so it does not raise the session's level: the session records
      // the phone code alone, which is what lets this passkey answer the later
      // step-up.
      const panel = smsPanel(page)
      await expect(
        panel.getByText(copy('authUi', 'signInFlow.enrollTitle'), {
          exact: true,
        }),
      ).toBeVisible({ timeout: 30_000 })
      authenticator = await addVirtualAuthenticator(page.context(), page)
      await registerPasskeyOnSignIn(panel)
      await expect(homeHeading(page)).toBeVisible({ timeout: 30_000 })

      await openAccountSection(page, 'security')
      await expandAccountRows(page)
      // The phone-code first login wrote the number as a sign-in way and no
      // contact, and the account holds no password.
      await expect(phoneSignInSentence(page)).toBeVisible()
      const password = section(page, copy('accountUi', 'password.title'))
      await expect(
        password.getByRole('textbox', {
          name: copy('accountUi', 'password.current.label'),
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(
        password.getByRole('textbox', {
          name: copy('accountUi', 'password.new.label'),
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('removing the phone is refused while it is the last way in, after the passkey answers the step-up', async () => {
      await deletePhone(page)

      // The account holds an active factor, so the removal asks for fresh local
      // multi-factor proof. The session's phone-code factor and the passkey's
      // channel differ, and the passkey was active before the session, so the
      // dialog offers it and the owner answers in the page.
      await proveWithPasskey(page)

      // The won challenge replays the removal, which the account module then
      // refuses: the username is not a login name without a password and the
      // passkey is not counted, so the phone is the last way that is not a
      // passkey. The phone card reports the refusal and the number stays.
      await expect(
        phoneCard(page)
          .getByRole('alert')
          .filter({
            hasText: copy('demo', 'account.phoneRemove.removeFailed'),
          }),
      ).toBeVisible({ timeout: 15_000 })
      await expect(
        phoneCard(page).getByText(PHONE_E164, { exact: true }),
      ).toBeVisible()
    })

    await test.step('the social identity is bound, and the phone can then be removed', async () => {
      // The refused removal left the browser on the profile pane; the bind
      // action lives on the security pane.
      await selectAccountSection(page, 'security')
      await expandAccountRows(page)
      await bindingsSection(page)
        .getByRole('button', {
          name: copy('accountUi', 'bindings.bindAriaWithProvider', {
            provider: googleLabel,
          }),
          exact: true,
        })
        .click()
      await approveSocialSignIn(page)

      // The callback completes the link and the host returns to the account
      // page. A binding is not a login, so the session stays where it was.
      await expect(accountHeading(page)).toBeVisible()
      await selectAccountSection(page, 'security')
      await expandAccountRows(page)
      await expect(googleRow(page)).toBeVisible()
      await expect(
        googleRow(page).getByText(identityProviderUser.email, { exact: true }),
      ).toBeVisible()

      // The phone is no longer the last way that is not a passkey: the social
      // identity is another. The removal is allowed and leaves the card
      // unbound and no phone in the sign-in ways.
      await deletePhone(page)
      await expect(
        phoneCard(page).getByText(
          copy('demo', 'account.rows.value.notBound'),
          { exact: true },
        ),
      ).toBeVisible({ timeout: 15_000 })
      await selectAccountSection(page, 'security')
      await expandAccountRows(page)
      await expect(phoneSignInSentence(page)).toHaveCount(0)
    })

    await test.step("the signup passkey is removed so the social sign-in can enrol the journey's passkey", async () => {
      // The passkey enrolled at the first login must not be the factor the
      // social sign-in proves: a step-up accepts only a channel the session has
      // not already proved, so the journey's passkey has to be registered by
      // the social login itself.
      await passkeyRemove(page).click()
      await confirmDialog(
        page,
        copy('accountUi', 'mfa.removeConfirm.title'),
        copy('accountUi', 'mfa.removeConfirm.confirmLabel'),
      )
      await expect(
        mfaSection(page).getByText(
          copy('accountUi', 'mfa.removed', {
            factor: copy('accountUi', 'mfa.factor.kind.webauthn'),
          }),
          { exact: true },
        ),
      ).toBeVisible({ timeout: 15_000 })
      // No factor is active now; the section still lists each method the
      // deployment serves, all with their state off.
      await expect(
        mfaSection(page).getByText(
          copy('accountUi', 'mfa.factor.status.active'),
          { exact: true },
        ),
      ).toHaveCount(0)
    })

    await test.step("a social sign-in registers the journey's passkey before the session exists", async () => {
      await accounts.signOut()
      // The removed credential is gone server-side; a device of its own keeps
      // it from answering the journey's ceremonies.
      await authenticator?.close().catch(() => undefined)
      authenticator = undefined

      await accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()
      await googleButton(page).click()
      await approveSocialSignIn(page)

      // The account holds no factor and the deployment requires one, so the
      // callback parks at the enroll step before the session is made. The
      // passkey registered here is the journey's, and the session records the
      // social identity alone.
      authenticator = await addVirtualAuthenticator(page.context(), page)
      await registerPasskeyOnSignIn(page)
      await expect(homeHeading(page)).toBeVisible({ timeout: 30_000 })

      await openAccountSection(page, 'security')
      await expandAccountRows(page)
    })

    await test.step('the account page lists the username and the social identity, no contact, and one passkey', async () => {
      // The account's sign-in names are the username it chose alone: the phone
      // was removed, the provider's address was not written to it, and no
      // email address is offered to sign in with.
      const identifiers = identifiersSection(page)
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
        identifiers.getByText(PHONE_E164, { exact: true }),
      ).toHaveCount(0)
      await expect(
        identifiers.getByText(identityProviderUser.email, { exact: true }),
      ).toHaveCount(0)
      await expect(
        identifiers.getByText(copy('accountUi', 'identifiers.usage.emailNone'), {
          exact: true,
        }),
      ).toBeVisible()

      // The social identity is listed as a login way of its own.
      await expect(googleRow(page)).toBeVisible()

      // No password is set, so the password section offers the first-password
      // form and no current-password field.
      const password = section(page, copy('accountUi', 'password.title'))
      await expect(
        password.getByRole('textbox', {
          name: copy('accountUi', 'password.current.label'),
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(
        password.getByRole('textbox', {
          name: copy('accountUi', 'password.new.label'),
          exact: true,
        }),
      ).toBeVisible()

      // The passkey registered during the sign-in is the account's one active
      // factor.
      await expect(
        mfaSection(page).getByText(
          copy('accountUi', 'mfa.factor.status.active'),
          { exact: true },
        ),
      ).toHaveCount(1)

      // The contact list is empty: the profile pane's email card lists no
      // address row (the only remaining identifier is the username, which has
      // no contact axis).
      await selectAccountSection(page, 'profile')
      await expandAccountRows(page)
      await expect(
        page
          .getByRole('region', {
            name: copy('demo', 'account.rows.label.email'),
            exact: true,
          })
          .getByRole('row'),
      ).toHaveCount(0)
      // The unlink step drives the security pane's social card.
      await selectAccountSection(page, 'security')
      await expandAccountRows(page)
    })

    await test.step('unlinking the external identity asks for fresh proof and is then refused', async () => {
      await googleUnlink(page).click()
      await confirmDialog(
        page,
        copy('accountUi', 'bindings.confirmTitle'),
        copy('accountUi', 'bindings.confirmLabel'),
      )

      // The unlink asks for fresh local multi-factor proof. The session proved
      // the social identity alone (the passkey registered during the login does
      // not count), and the passkey's channel differs from it, so the shared
      // step-up dialog offers the passkey and the owner answers it in the page.
      await proveWithPasskey(page)

      // The won challenge replays the unlink, which the account module then
      // refuses to remove as the last non-passkey way: the refusal renders in
      // the bindings section's result region, and the row is still listed.
      await expect(
        bindingsSection(page)
          .getByRole('alert')
          .filter({ hasText: copy('accountUi', 'errors.authn.last_login_method') }),
      ).toBeVisible({ timeout: 15_000 })
      await expect(googleRow(page)).toBeVisible()
      await expect(
        bindingsSection(page).getByText(
          copy('accountUi', 'bindings.unlinked', { provider: googleLabel }),
          { exact: true },
        ),
      ).toHaveCount(0)
    })

    await test.step('the owner sets a password from the account page', async () => {
      const password = section(page, copy('accountUi', 'password.title'))
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.new.label'),
          exact: true,
        })
        .fill(NEW_PASSWORD)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.confirm.label'),
          exact: true,
        })
        .fill(NEW_PASSWORD)
      await password
        .getByRole('button', {
          name: copy('accountUi', 'password.submitFirst'),
          exact: true,
        })
        .click()
      await expect(
        page.getByText(copy('accountUi', 'password.changedFirst'), {
          exact: true,
        }),
      ).toBeVisible({ timeout: 15_000 })
      // The write keeps the page on the security pane that started it.
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('demo', 'account.sections.security'),
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('the same unlink now succeeds', async () => {
      await googleUnlink(page).click()
      await confirmDialog(
        page,
        copy('accountUi', 'bindings.confirmTitle'),
        copy('accountUi', 'bindings.confirmLabel'),
      )
      await expect(
        bindingsSection(page).getByText(
          copy('accountUi', 'bindings.unlinked', { provider: googleLabel }),
          { exact: true },
        ),
      ).toBeVisible({ timeout: 15_000 })
      // The bound row is gone and the channel is offered to link again.
      await expect(googleUnlink(page)).toHaveCount(0)
      await expect(
        bindingsSection(page).getByRole('button', {
          name: copy('accountUi', 'bindings.bindAriaWithProvider', {
            provider: googleLabel,
          }),
          exact: true,
        }),
      ).toBeVisible()
    })
  } finally {
    await authenticator?.close().catch(() => undefined)
  }
})
