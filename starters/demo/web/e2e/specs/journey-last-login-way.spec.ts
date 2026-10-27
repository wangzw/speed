/**
 * The last-sign-in-way journey: an account opened through a social provider
 * holds exactly one way to sign in that is not a passkey -- its external
 * identity. A passkey is enrolled, which the journey proves is not one of the
 * three ways the rule counts (a password credential, a proved phone number, an
 * external identity). Unlinking the external identity while it is the only such
 * way asks for fresh proof; the owner answers the challenge in the page with
 * the passkey just enrolled, and the unlink is then refused: the dedicated
 * sentence is rendered, and the binding stays listed. The owner then sets a
 * password from the same account page; the external identity is no longer the
 * only non-passkey way, and the same unlink now succeeds.
 *
 * THE DEPLOYMENT REQUIRES A FACTOR. A step-up accepts only a factor whose
 * activation predates the session's authentication time, so a factor enrolled
 * after the session cannot strengthen it. This journey's deployment requires
 * every account to register a factor, which makes the social sign-in that opens
 * the account stop at the registration step: the owner registers the passkey
 * there, before the login session exists, and the later unlink's step-up can
 * offer it. The owner still answers that step-up in the page; nothing signs the
 * session out and back in.
 *
 * The social round trip is the harness's real OpenID Connect provider: the
 * browser leaves for its authorization page and clicks its control, so no route
 * is intercepted and no response is faked. The passkey is Chromium's virtual
 * authenticator (helpers/virtual-authenticator.ts): the page still calls
 * `navigator.credentials.create`/`.get`, and the fixture answers as a platform
 * authenticator would.
 *
 * WHAT IS ASSERTED. The refusal is the dedicated last-login-method answer, and
 * this spec asserts its shipped sentence: it says the account has no other way
 * to sign in and names setting a password or linking another account as the
 * remedy. It also asserts that the binding survives the refusal.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts.
 */
// @journey journey-last-login-way.md
// @covers REQ-security-040, REQ-security-013, REQ-security-017
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { identityProviderUser } from '../fixtures/identity-provider'
import { copy } from '../helpers/i18n'
import { approveSocialSignIn } from '../helpers/social'
import {
  addVirtualAuthenticator,
  type VirtualAuthenticator,
} from '../helpers/virtual-authenticator'

// The provider round trip, the passkey ceremonies and the step-up run inside
// this file's per-test bound; fixture assembly included.
test.describe.configure({ timeout: 300_000 })

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

/** The account page's heading. */
function accountHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'account.title'),
    exact: true,
  })
}

/** The header's account-settings entry. */
function accountLink(page: Page): Locator {
  return page.getByRole('link', {
    name: copy('demo', 'nav.account'),
    exact: true,
  })
}

/** One account page section, named by its own title. */
function section(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

/** The linked-social-accounts section. */
function bindingsSection(page: Page): Locator {
  return section(page, copy('accountUi', 'bindings.title'))
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

/** Clear the unlink's step-up challenge with the passkey this page enrolled. */
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

// The journey drives the provider's real round trip, so its server starts the
// local identity provider; the deployment requires a factor, so the social
// sign-in registers the passkey before its session exists.
test.use({ socialIdentityProvider: true, mfaRequired: true })

test('last-login-way: removing the last way that is not a passkey is refused', async ({
  demo,
}) => {
  const { page } = demo
  const googleLabel = copy('accountUi', 'bindings.provider.google')
  let authenticator: VirtualAuthenticator | undefined

  try {
    await test.step('a social sign-in opens an account and registers the required passkey', async () => {
      await demo.accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()
      await googleButton(page).click()
      await approveSocialSignIn(page)

      // The deployment requires a factor, so the sign-in flow parks at the
      // registration step before any session exists. Registering the passkey
      // here makes its activation predate the session's authentication time,
      // which is what lets the later unlink's step-up offer it.
      const passkeyChoice = page.getByRole('button', {
        name: copy('authUi', 'signInFlow.factorKind.webauthn'),
        exact: true,
      })
      await expect(passkeyChoice).toBeVisible({ timeout: 30_000 })
      authenticator = await addVirtualAuthenticator(page.context(), page)
      await passkeyChoice.click()

      // Registration starts on the prompt's own control, not on arrival: the
      // virtual authenticator answers the creation ceremony.
      const registerPrompt = page.getByText(
        copy('authUi', 'signInFlow.passkeyRegisterPrompt'),
        { exact: true },
      )
      await expect(registerPrompt).toBeVisible({ timeout: 15_000 })
      await page
        .getByRole('button', {
          name: copy('authUi', 'signInFlow.submit'),
          exact: true,
        })
        .click()
      await expect(registerPrompt).toHaveCount(0, { timeout: 20_000 })
      await expect(homeHeading(page)).toBeVisible({ timeout: 30_000 })

      await accountLink(page).click()
      await expect(accountHeading(page)).toBeVisible()
      // The account's identifiers are the provider-declared address alone: no
      // phone number is listed, so no proved phone is one of its ways in.
      const identifiers = section(page, copy('accountUi', 'identifiers.title'))
      await expect(
        identifiers
          .getByRole('listitem')
          .filter({ hasText: identityProviderUser.email }),
      ).toBeVisible()
      await expect(
        identifiers.getByText(copy('accountUi', 'identifiers.kind.phone'), {
          exact: true,
        }),
      ).toHaveCount(0)
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
      // The passkey registered during the sign-in is the account's factor.
      await expect(
        section(page, copy('accountUi', 'mfa.title'))
          .getByText(copy('accountUi', 'mfa.factor.kind.webauthn'), {
            exact: true,
          })
          .first(),
      ).toBeVisible()
    })

    await test.step('unlinking the external identity asks for fresh proof and is then refused', async () => {
      await googleUnlink(page).click()
      await confirmDialog(
        page,
        copy('accountUi', 'bindings.confirmTitle'),
        copy('accountUi', 'bindings.confirmLabel'),
      )

      // The unlink asks for fresh local multi-factor proof. The bindings
      // surface opens the shared step-up dialog, which offers the passkey the
      // account holds, and the owner answers it in the page.
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
      await expect(accountHeading(page)).toBeVisible()
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
      await expect(googleRow(page)).toHaveCount(0)
    })
  } finally {
    await authenticator?.close().catch(() => undefined)
  }
})
