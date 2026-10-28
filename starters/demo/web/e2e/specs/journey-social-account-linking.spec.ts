/**
 * The social account-linking journey: an account that already signs in with a
 * password adds a Google identity to itself.
 *
 * The participant already holds a password account whose email address its
 * owner proved through the product's own self-service verification. The local
 * provider serves one fixed Google user, so the account is opened here with
 * that provider user's address: registration writes the account and the
 * verification code from the review mail carrier proves the address, which is
 * the precondition the link purpose is gated on. The whole account is the
 * product's own writes, never a row the fixture inserts.
 *
 * The journey's order is load-bearing:
 *
 *   - Before any link, the same Google identity is refused with the
 *     link-required sentence (automatic linking no longer exists, so a social
 *     login never enters an account that holds the address) and the browser
 *     lands back on the sign-in surface: the refusal takes over nothing and
 *     opens nothing.
 *   - The participant signs in with the username and password, opens the
 *     account page, and finds no Google row while the add area offers Google --
 *     a deployment method that is not bound yet.
 *   - Choosing Google leaves for the provider's authorization page, comes back
 *     through the product's own callback, and lands on the account page with
 *     the Google row present. The row names the provider's address. Google
 *     leaves the add area, while another enabled channel stays offered. The
 *     participant is still in the same signed-in shell: a binding is not a
 *     login, so no session was created and none was switched.
 *   - A later Google sign-in now enters the original account directly, with no
 *     link-required notice, and the account page still lists the binding and
 *     the email identifier.
 *   - The password sign-in still enters that same account after the binding.
 *
 * The provider is the fixture's real OpenID Connect server
 * (fixtures/identity-provider.ts): the browser navigates to its authorization
 * page and clicks its control, so no route is intercepted and no response is
 * faked. The server-to-server half is TLS against the private CA the review
 * configuration names. The callback page's pending notice is not asserted:
 * against the local provider the completion and return to the account page can
 * happen before a locator observes the intermediate document, and the settled
 * bound row is the outcome the journey claims.
 *
 * Expected product copy is read from the shipped en-US bundles through
 * helpers/i18n.ts. The provider's own page copy belongs to the provider, so it
 * is the fixture's constant and helper/social.ts asserts it. The browser is
 * pinned to en-US and UTC by the Playwright config.
 */
// @journey journey-social-account-linking.md
// @covers REQ-account-027, REQ-account-028, REQ-account-053, REQ-account-067, REQ-signin-006, REQ-signin-015, REQ-signin-017, REQ-signin-027, REQ-signin-028, REQ-signup-049
import type { Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { identityProviderUser } from '../fixtures/identity-provider'
import { copy } from '../helpers/i18n'
import { approveSocialSignIn } from '../helpers/social'

/**
 * The existing account: a username and a password, with the provider's own
 * address proved as a contact. Registration writes both, and the review mail
 * carrier's code proves the address, so the link purpose is not refused for a
 * contact that is unproved.
 */
const linkerAccount = {
  username: 'social-linker',
  email: identityProviderUser.email,
  password: 'e2e-Password-123',
} as const

/** The Google channel on the sign-in surface. */
function signInGoogleButton(page: Page) {
  return page.getByRole('button', {
    name: copy('authUi', 'social.provider.google'),
    exact: true,
  })
}

/** The product's sign-in surface heading. */
function signInHeading(page: Page) {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

/** The tenant area's home heading, the rendered proof of a landed session. */
function homeHeading(page: Page) {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

/** The account page's heading. */
function accountHeading(page: Page) {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'account.title'),
    exact: true,
  })
}

/** Open the account page through the account menu. */
async function openAccountSettings(page: Page): Promise<void> {
  await page.getByRole('banner').getByTestId('user-menu-trigger').click()
  await page
    .getByRole('menuitem', {
      name: copy('demo', 'userMenu.accountSettings'),
      exact: true,
    })
    .click()
  await expect(accountHeading(page)).toBeVisible()
}

/** The account page's bound-and-bindable social accounts section. */
function bindingsSection(page: Page) {
  return page.getByRole('region', {
    name: copy('accountUi', 'bindings.title'),
    exact: true,
  })
}

/** The bound Google row: the provider's label, which is also the add button's. */
function googleRow(page: Page) {
  return bindingsSection(page)
    .getByRole('listitem')
    .filter({ hasText: copy('accountUi', 'bindings.provider.google') })
}

// This journey drives the provider's real round trip, so its server starts the
// local identity provider; no other spec pays for openssl or its two ports.
test.use({ socialIdentityProvider: true })

test('social-account-linking: a password account links Google, then signs in with it', async ({
  demo,
}) => {
  const { page } = demo
  const google = copy('accountUi', 'bindings.provider.google')
  const github = copy('accountUi', 'bindings.provider.github')

  await test.step('an existing password account proves the provider address', async () => {
    // The registration signs the new account in; the verification code from
    // the review mail carrier proves the address, so the account is the
    // password holder with a proved email the journey starts from.
    await demo.accounts.registerAndVerify(linkerAccount)
    await demo.accounts.signOut()
  })

  await test.step('before linking, the same Google identity is refused', async () => {
    await demo.accounts.openSignIn()
    await expect(signInHeading(page)).toBeVisible()

    await signInGoogleButton(page).click()
    await approveSocialSignIn(page)

    // Automatic linking no longer exists, so the provider's verified address is
    // refused with the binding sentence and the browser stays on the sign-in
    // surface.
    await expect(
      page
        .getByRole('alert')
        .filter({ hasText: copy('authUi', 'errors.authn.link_required') }),
    ).toBeVisible()
    await expect(signInHeading(page)).toBeVisible()
    await expect(homeHeading(page)).toHaveCount(0)
  })

  await test.step('the user signs in with the password and opens the account page', async () => {
    await demo.accounts.signIn({
      identifier: linkerAccount.username,
      password: linkerAccount.password,
    })
    await expect(homeHeading(page)).toBeVisible()
    await openAccountSettings(page)
  })

  await test.step('the section lists no Google row and offers Google to add', async () => {
    const bindings = bindingsSection(page)
    await expect(bindings).toBeVisible()

    // Nothing is bound yet; the add area is what keeps the section open.
    await expect(bindings.getByRole('listitem')).toHaveCount(0)
    await expect(
      bindings.getByRole('button', { name: google, exact: true }),
    ).toBeVisible()
    // A second enabled redirect method is offered too, so the Google button is
    // the deployment's channel list rather than a single hard-wired one.
    await expect(
      bindings.getByRole('button', { name: github, exact: true }),
    ).toBeVisible()
  })

  await test.step('choosing Google binds it and returns to the account page', async () => {
    await bindingsSection(page)
      .getByRole('button', { name: google, exact: true })
      .click()
    await approveSocialSignIn(page)

    // The callback completes the link and the host returns to the account page.
    await expect(accountHeading(page)).toBeVisible()
    await expect(googleRow(page)).toBeVisible()
    // The row names the provider's address, so the binding is the identity the
    // provider proved, not a second account.
    await expect(
      googleRow(page).getByText(identityProviderUser.email, { exact: true }),
    ).toBeVisible()
  })

  await test.step('the bound channel leaves the add area and the session stays', async () => {
    const bindings = bindingsSection(page)
    // An already bound channel is no longer offered; the other one still is.
    await expect(
      bindings.getByRole('button', { name: google, exact: true }),
    ).toHaveCount(0)
    await expect(
      bindings.getByRole('button', { name: github, exact: true }),
    ).toBeVisible()
    // A binding is not a login: the browser is still in the signed-in shell,
    // with the account menu in the banner, and never visited the sign-in
    // surface for this step.
    await expect(
      page.getByRole('banner').getByTestId('user-menu-trigger'),
    ).toBeVisible()
    await expect(signInHeading(page)).toHaveCount(0)
  })

  await test.step('a later Google sign-in enters the same account directly', async () => {
    await demo.accounts.signOut()
    await demo.accounts.openSignIn()

    await signInGoogleButton(page).click()
    await approveSocialSignIn(page)

    // No link-required notice this time: the identity is bound, so the login
    // completes.
    await expect(homeHeading(page)).toBeVisible()
    await expect(
      page
        .getByRole('alert')
        .filter({ hasText: copy('authUi', 'errors.authn.link_required') }),
    ).toHaveCount(0)

    // The same account: it still holds the proved email identifier and the
    // Google binding.
    await openAccountSettings(page)
    await expect(googleRow(page)).toBeVisible()
    const identifiers = page.getByRole('region', {
      name: copy('accountUi', 'identifiers.title'),
      exact: true,
    })
    await expect(
      identifiers
        .getByRole('listitem')
        .filter({ hasText: identityProviderUser.email }),
    ).toBeVisible()
  })

  await test.step('the password sign-in still enters that same account', async () => {
    await demo.accounts.signOut()
    await demo.accounts.signIn({
      identifier: linkerAccount.username,
      password: linkerAccount.password,
    })
    await expect(homeHeading(page)).toBeVisible()

    await openAccountSettings(page)
    await expect(googleRow(page)).toBeVisible()
  })
})
