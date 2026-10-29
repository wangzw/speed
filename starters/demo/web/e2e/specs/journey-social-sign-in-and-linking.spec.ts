/**
 * The social first-sign-in journey: a visitor picks Google on the sign-in
 * surface, leaves for the provider's own authorization page, approves it, and
 * comes back to the product. The journey has two branches, and each is driven
 * on a server of its own because the provider's fixed address is what decides
 * which branch the product takes.
 *
 *   - The address belongs to no account. The first social sign-in opens the
 *     account-opening face: the provider's verified address shown read-only,
 *     and a username and password (with its confirmation) to submit. The
 *     account opens with that username and password and the provider's display
 *     name; the address is written as a contact and the default contact, never
 *     as a sign-in way. The account page names the verified address, the
 *     identifiers list carries the address under Contacts and the username
 *     under Sign-in ways, and the password set here signs in afterwards.
 *   - The address already belongs to an account. Automatic linking no longer
 *     exists, so a social login never enters the account that holds the
 *     address: the product refuses it with the link-required refusal and lands
 *     the browser back on the sign-in surface, which renders the refusal's own
 *     sentence. Nothing is signed in and no second account is created; the
 *     existing account's sign-in ways are untouched.
 *
 * The provider is a real OpenID Connect server the fixtures start
 * (fixtures/identity-provider.ts): discovery, a key set, a token exchange and a
 * real authorization page. The browser performs the round trip -- it navigates
 * to that page and clicks its control -- so no route is intercepted and no
 * response is faked. The server-to-server half is TLS with a private CA the
 * configuration names, which is why the address in the ID token is trusted.
 *
 * Expected product copy is read from the shipped en-US bundles through
 * helpers/i18n.ts. The provider's own page copy belongs to the provider, not
 * the product, so it is the fixture's constant and the helper asserts it; the
 * browser is pinned to en-US and UTC by the Playwright config.
 */
// @journey journey-social-sign-in-and-linking.md
// @covers REQ-account-015, REQ-account-016, REQ-account-060, REQ-signin-015, REQ-signin-017, REQ-signin-027, REQ-signin-028, REQ-signin-038, REQ-signup-047, REQ-signup-048, REQ-signup-049
import type { Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { reviewSocialAccount } from '../fixtures/config'
import { identityProviderUser } from '../fixtures/identity-provider'
import { copy } from '../helpers/i18n'
import { approveSocialSignIn } from '../helpers/social'

/** The username the first social sign-in chooses for the account it opens. */
const FIRST_SIGN_IN_USERNAME = 'social-first-signin'

/** The password the account-opening face sets, which signs in afterwards. */
const FIRST_SIGN_IN_PASSWORD = 'e2e-Social-Password-123'

/** The social block's Google channel. */
function googleButton(page: Page) {
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

/** Open the account page through the account menu. */
async function openAccountSettings(page: Page): Promise<void> {
  await page.getByRole('banner').getByTestId('user-menu-trigger').click()
  await page
    .getByRole('menuitem', {
      name: copy('demo', 'userMenu.accountSettings'),
      exact: true,
    })
    .click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('demo', 'account.title'),
      exact: true,
    }),
  ).toBeVisible()
}

/** The account page's identifier section. */
function identifiersSection(page: Page) {
  return page.getByRole('region', {
    name: copy('accountUi', 'identifiers.title'),
    exact: true,
  })
}

/** The account page's bound-and-bindable social accounts section. */
function bindingsSection(page: Page) {
  return page.getByRole('region', {
    name: copy('accountUi', 'bindings.title'),
    exact: true,
  })
}

// This journey drives the provider's real round trip, so its server starts the
// local identity provider; no other spec pays for openssl or its two ports.
test.use({ socialIdentityProvider: true })

test.describe('the provider address belongs to no account', () => {
  test('social-sign-in: a first Google sign-in opens the account face and keeps the provider address as a contact', async ({
    demo,
  }) => {
    const { page } = demo

    await test.step('the sign-in surface offers the Google channel', async () => {
      await demo.accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()
      await expect(googleButton(page)).toBeVisible()
    })

    await test.step('the provider authorizes the fixed user', async () => {
      await googleButton(page).click()
      await approveSocialSignIn(page)
    })

    await test.step('the product opens the account face before it creates the account', async () => {
      // A first login that would open an account stops at the account-opening
      // face: the provider's verified address read-only, and the username and
      // password the account will carry.
      await expect(
        page.getByRole('heading', {
          level: 2,
          name: copy('authUi', 'signInFlow.signupAccountTitle'),
          exact: true,
        }),
      ).toBeVisible()
      const address = page.getByLabel(
        copy('authUi', 'signInFlow.signupEmailLabel'),
        { exact: true },
      )
      await expect(address).toHaveValue(identityProviderUser.email)
      await expect(address).toHaveAttribute('readonly', '')
      await demo.accounts.answerSocialSignup({
        username: FIRST_SIGN_IN_USERNAME,
        password: FIRST_SIGN_IN_PASSWORD,
      })
    })

    await test.step('the browser comes back signed in to the tenant area', async () => {
      await expect(homeHeading(page)).toBeVisible()
    })

    await test.step('the account holds the username and the display name, and the provider address as a contact', async () => {
      await openAccountSettings(page)

      // The provider's display name is the name the account took from it.
      await expect(
        page.getByLabel(copy('accountUi', 'profile.displayName.label'), {
          exact: true,
        }),
      ).toHaveValue(identityProviderUser.name)

      // The verified provider address is written as a contact and is the
      // default one, so the verification section names it as complete.
      const verified = page.getByRole('region', {
        name: copy('accountUi', 'verification.verifiedTitle'),
        exact: true,
      })
      await expect(verified).toBeVisible()
      await expect(verified).toContainText(identityProviderUser.email)

      // The identifiers list carries the address under Contacts -- default
      // contact marked -- and the username under Sign-in ways. The address is
      // not a sign-in way.
      const identifiers = identifiersSection(page)
      const contacts = identifiers.getByRole('region', {
        name: copy('accountUi', 'identifiers.groups.contact'),
        exact: true,
      })
      await expect(
        contacts.getByText(identityProviderUser.email, { exact: true }),
      ).toBeVisible()
      await expect(
        contacts.getByRole('radio', {
          name: copy('accountUi', 'identifiers.axes.defaultAria', {
            value: identityProviderUser.email,
          }),
        }),
      ).toBeChecked()

      const signInWays = identifiers.getByRole('region', {
        name: copy('accountUi', 'identifiers.groups.signIn'),
        exact: true,
      })
      const usernameRow = signInWays
        .getByRole('listitem')
        .filter({ hasText: FIRST_SIGN_IN_USERNAME })
      await expect(usernameRow).toBeVisible()
      await expect(
        usernameRow.getByText(copy('accountUi', 'identifiers.kind.username'), {
          exact: true,
        }),
      ).toBeVisible()
      // A username proves no ownership, so it carries no ownership chip.
      await expect(
        usernameRow.getByText(copy('accountUi', 'identifiers.verified'), {
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(
        signInWays.getByText(identityProviderUser.email, { exact: true }),
      ).toHaveCount(0)
    })

    await test.step('the password set at signup signs in to the account', async () => {
      // The account-opening face took a password, so the account is usable
      // with it from the moment it exists.
      await demo.accounts.signOut()
      await demo.accounts.signIn({
        identifier: FIRST_SIGN_IN_USERNAME,
        password: FIRST_SIGN_IN_PASSWORD,
      })
      await expect(homeHeading(page)).toBeVisible()

      await openAccountSettings(page)
      await expect(
        identifiersSection(page)
          .getByRole('listitem')
          .filter({ hasText: FIRST_SIGN_IN_USERNAME }),
      ).toBeVisible()
    })

    await test.step('the same Google account signs in again to the same account', async () => {
      // The result section says the account opened here stays reachable through
      // the same Google account. Signing out and walking the provider's round
      // trip again is what proves that second half; the first sign-in alone
      // leaves it unverified.
      await demo.accounts.signOut()
      await demo.accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()

      await googleButton(page).click()
      await approveSocialSignIn(page)
      // The binding the first sign-in wrote is what resolves this login, so the
      // flow lands on the existing account instead of parking at the username
      // step a second time.
      await expect(homeHeading(page)).toBeVisible()

      // The same account: the username it chose and the binding are still
      // there, and the provider's address is still a contact rather than a
      // sign-in way.
      await openAccountSettings(page)
      await expect(
        identifiersSection(page)
          .getByRole('listitem')
          .filter({ hasText: FIRST_SIGN_IN_USERNAME }),
      ).toBeVisible()
      await expect(
        bindingsSection(page)
          .getByRole('listitem')
          .filter({ hasText: copy('accountUi', 'bindings.provider.google') }),
      ).toBeVisible()
      const identifiers = identifiersSection(page)
      await expect(
        identifiers
          .getByRole('region', {
            name: copy('accountUi', 'identifiers.groups.contact'),
            exact: true,
          })
          .getByText(identityProviderUser.email, { exact: true }),
      ).toBeVisible()
      await expect(
        identifiers
          .getByRole('region', {
            name: copy('accountUi', 'identifiers.groups.signIn'),
            exact: true,
          })
          .getByText(identityProviderUser.email, { exact: true }),
      ).toHaveCount(0)
    })
  })
})

test.describe('the provider address already belongs to an account', () => {
  test.use({ seedSocialAccount: true })

  test('social-sign-in: a Google email already on an account is refused with link-required', async ({
    demo,
  }) => {
    const { page } = demo

    await test.step('the sign-in surface offers the Google channel', async () => {
      await demo.accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()
      await expect(googleButton(page)).toBeVisible()
    })

    await test.step('the provider authorizes the seeded account address', async () => {
      await googleButton(page).click()
      await approveSocialSignIn(page)
    })

    await test.step('the product lands back on sign-in with the binding refusal', async () => {
      // Automatic linking no longer exists, so an address an account already
      // holds is refused rather than taking that account over. The refusal and
      // the colliding address are one alert: the sentence names what happened,
      // the address names which of the visitor's addresses the account already
      // holds, masked by the server to its first character and the domain as
      // written. One region, one announcement.
      const [local = '', domain = ''] = identityProviderUser.email.split('@')
      const refusal = page
        .getByRole('alert')
        .filter({ hasText: copy('authUi', 'errors.authn.link_required') })
      await expect(refusal).toBeVisible()
      await expect(refusal).toContainText(
        copy('authUi', 'social.linkRequiredEmail', {
          email: `${local.charAt(0)}***@${domain}`,
        }),
      )
      // The visitor is not signed in: the sign-in surface is still the
      // document, not the tenant area, so nothing was taken over.
      await expect(signInHeading(page)).toBeVisible()
      await expect(homeHeading(page)).toHaveCount(0)
    })

    await test.step('the existing account keeps its password sign-in', async () => {
      // The refusal touched neither the account nor its credential: the
      // username and the password the seed wrote still open the same account.
      await demo.accounts.signIn({
        identifier: reviewSocialAccount.username,
        password: reviewSocialAccount.password,
      })
      await expect(homeHeading(page)).toBeVisible()
    })
  })
})
