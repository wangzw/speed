/**
 * The social first-sign-in journey: a visitor picks Google on the sign-in
 * surface, leaves for the provider's own authorization page, approves it, and
 * comes back to the product. The journey has two branches, and each is driven
 * on a server of its own because the provider's fixed address is what decides
 * which branch the product takes.
 *
 *   - The address belongs to no account. The first social sign-in opens an
 *     account, writes the provider-declared verified address as the account's
 *     email identifier, and signs the visitor in. The account page's
 *     verification section reads as complete and names that address, and the
 *     sign-in identifiers list carries it as verified -- the visitor is never
 *     asked to prove the mail again.
 *   - The address already belongs to an account, and automatic linking is off
 *     (the review assembly's switch keeps its declared default). The product
 *     refuses the login with the link-required refusal and lands the browser
 *     back on the sign-in surface, which renders the refusal's own sentence.
 *     Nothing is signed in and no second account is created; the existing
 *     account's sign-in ways are untouched.
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
// @covers REQ-account-015, REQ-account-016, REQ-signin-015, REQ-signin-017, REQ-signin-027, REQ-signin-028, REQ-signup-017, REQ-signup-019
import type { Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { reviewSocialAccount } from '../fixtures/config'
import { identityProviderUser } from '../fixtures/identity-provider'
import { copy } from '../helpers/i18n'
import { approveSocialSignIn } from '../helpers/social'

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

// This journey drives the provider's real round trip, so its server starts the
// local identity provider; no other spec pays for openssl or its two ports.
test.use({ socialIdentityProvider: true })

test.describe('the provider address belongs to no account', () => {
  test('social-sign-in: a first Google sign-in opens an account with the provider-verified email', async ({
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

    await test.step('the browser comes back signed in to the tenant area', async () => {
      await expect(homeHeading(page)).toBeVisible()
    })

    await test.step('the account reads as verified for the provider address', async () => {
      await openAccountSettings(page)

      // The completion state names the address the provider proved; the
      // visitor is not asked for a code anywhere.
      await expect(
        page.getByRole('region', {
          name: copy('accountUi', 'verification.verifiedTitle'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        page.getByText(
          copy('accountUi', 'verification.verifiedDescription', {
            email: identityProviderUser.email,
          }),
          { exact: true },
        ),
      ).toBeVisible()

      // The address is the account's email identifier and reads as verified.
      const identifiers = page.getByRole('region', {
        name: copy('accountUi', 'identifiers.title'),
        exact: true,
      })
      const row = identifiers
        .getByRole('listitem')
        .filter({ hasText: identityProviderUser.email })
      await expect(row).toBeVisible()
      await expect(
        row.getByText(copy('accountUi', 'identifiers.verified'), {
          exact: true,
        }),
      ).toBeVisible()
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
      await expect(
        page.getByText(copy('authUi', 'errors.authn.link_required'), {
          exact: true,
        }),
      ).toBeVisible()
      // The visitor is not signed in: the sign-in surface is still the
      // document, not the tenant area, so nothing was taken over.
      await expect(signInHeading(page)).toBeVisible()
      await expect(homeHeading(page)).toHaveCount(0)
    })

    await test.step('the existing account keeps its password sign-in', async () => {
      // The refusal touched neither the account nor its credential: the
      // address and the password the seed wrote still open the same account.
      await demo.accounts.signIn({
        identifier: reviewSocialAccount.identifier,
        password: reviewSocialAccount.password,
      })
      await expect(homeHeading(page)).toBeVisible()
    })
  })
})
