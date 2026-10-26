/**
 * The participant's step at the external identity provider.
 *
 * A social sign-in leaves the product for the provider's authorization page.
 * The provider is the review harness's own OpenID Connect server
 * (fixtures/identity-provider.ts), but its page is a real document in the
 * browser and the participant approves it the way they would approve any
 * provider: by reading whose account it names and pressing the control. This
 * helper performs that step and nothing else -- it never answers a product
 * request, never intercepts a route and never reads anything the browser does
 * not render.
 */
import { expect } from '@playwright/test'
import type { Page } from '@playwright/test'

import {
  enterpriseIdentityProviderCopy,
  enterpriseIdentityProviderUser,
  identityProviderCopy,
  identityProviderUser,
} from '../fixtures/identity-provider'

/**
 * Read the provider's page, confirm it names the fixed user, and approve the
 * authorization. The click navigates the browser back to the product.
 */
export async function approveSocialSignIn(page: Page): Promise<void> {
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: identityProviderCopy.heading,
      exact: true,
    }),
  ).toBeVisible()
  await expect(
    page.getByText(
      identityProviderCopy.accountSentence(identityProviderUser.email),
      { exact: true },
    ),
  ).toBeVisible()
  await page
    .getByRole('button', {
      name: identityProviderCopy.approve,
      exact: true,
    })
    .click()
}

/**
 * The enterprise participant's step at their provider's authorization page.
 *
 * An enterprise sign-in reaches the same kind of provider page as a social
 * one, started from the enterprise fixtures instead. The page belongs to the
 * provider, so its copy is the fixture's constant; the participant reads whose
 * account it names and approves it.
 */
export async function approveEnterpriseSignIn(page: Page): Promise<void> {
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: enterpriseIdentityProviderCopy.heading,
      exact: true,
    }),
  ).toBeVisible()
  await expect(
    page.getByText(
      enterpriseIdentityProviderCopy.accountSentence(
        enterpriseIdentityProviderUser.email,
      ),
      { exact: true },
    ),
  ).toBeVisible()
  await page
    .getByRole('button', {
      name: enterpriseIdentityProviderCopy.approve,
      exact: true,
    })
    .click()
}
