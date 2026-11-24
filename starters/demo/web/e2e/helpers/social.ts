import { expect } from '@playwright/test'
import type { Page } from '@playwright/test'

import {
  enterpriseIdentityProviderCopy,
  enterpriseIdentityProviderUser,
  identityProviderCopy,
  identityProviderUser,
} from '../fixtures/identity-provider'

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
