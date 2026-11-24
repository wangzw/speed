// @journey journey-social-sign-in-and-linking.md
// @covers REQ-account-015, REQ-account-016, REQ-account-060, REQ-account-068, REQ-signin-015, REQ-signin-017, REQ-signin-027, REQ-signin-028, REQ-signin-038, REQ-signup-047, REQ-signup-048, REQ-signup-049, REQ-signup-053
import type { Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { e2eSocialAccount } from '../fixtures/config'
import { identityProviderUser } from '../fixtures/identity-provider'
import {
  expandAccountRows,
  openAccountSection,
  selectAccountSection,
} from '../helpers/account-sections'
import { copy } from '../helpers/i18n'
import { approveSocialSignIn } from '../helpers/social'

const FIRST_SIGN_IN_USERNAME = 'social-first-signin'

const FIRST_SIGN_IN_PASSWORD = 'e2e-Social-Password-123'

function googleButton(page: Page) {
  return page.getByRole('button', {
    name: copy('authUi', 'social.provider.google'),
    exact: true,
  })
}

function signInHeading(page: Page) {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

function homeHeading(page: Page) {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

function identifiersSection(page: Page) {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.loginWays'),
    exact: true,
  })
}

function emailCard(page: Page) {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.email'),
    exact: true,
  })
}

function usernameSignInSentence(page: Page) {
  return identifiersSection(page).getByText(
    copy('accountUi', 'identifiers.usage.loginAs', {
      kind: copy('accountUi', 'identifiers.kind.username'),
      value: FIRST_SIGN_IN_USERNAME,
    }),
    { exact: true },
  )
}

function bindingsSection(page: Page) {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.social'),
    exact: true,
  })
}

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
      await expect(
        page.getByText(
          copy('authUi', 'signInFlow.signupAccountInstruction'),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        page.getByText(
          copy('authUi', 'signInFlow.signupIdentity', {
            provider: copy('authUi', 'social.provider.google'),
            name: identityProviderUser.name,
          }).split(identityProviderUser.name)[0] ?? '',
          { exact: false },
        ),
      ).toBeVisible()
      await expect(
        page.getByText(identityProviderUser.name, { exact: true }),
      ).toBeVisible()
      await expect(
        page.getByRole('textbox', {
          name: copy('authUi', 'register.emailLabel'),
          exact: true,
        }),
      ).toContainText(identityProviderUser.email)
      await demo.accounts.answerSocialSignup({
        username: FIRST_SIGN_IN_USERNAME,
        password: FIRST_SIGN_IN_PASSWORD,
      })
    })

    await test.step('the browser comes back signed in to the tenant area', async () => {
      await expect(homeHeading(page)).toBeVisible()
    })

    await test.step('the account holds the username and the display name, and the provider address as a contact', async () => {
      await openAccountSection(page, 'profile')
      await expandAccountRows(page)

      await expect(
        page.getByLabel(copy('accountUi', 'profile.displayName.label'), {
          exact: true,
        }),
      ).toHaveValue(identityProviderUser.name)

      const email = emailCard(page)
      await expect(
        email.getByText(identityProviderUser.email, { exact: true }),
      ).toBeVisible()
      await expect(
        email.getByText(copy('accountUi', 'identifiers.verified'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        email.getByText(copy('accountUi', 'identifiers.axes.defaultContact'), {
          exact: true,
        }),
      ).toBeVisible()

      await selectAccountSection(page, 'security')
      await expandAccountRows(page)
      const signInWays = identifiersSection(page)
      await expect(usernameSignInSentence(page)).toBeVisible()
      await expect(
        signInWays.getByRole('combobox', {
          name: copy('accountUi', 'identifiers.kind.email'),
          exact: true,
        }),
      ).toContainText(identityProviderUser.email)
    })

    await test.step('the password set at signup signs in to the account', async () => {
      await demo.accounts.signOut()
      await demo.accounts.signIn({
        identifier: FIRST_SIGN_IN_USERNAME,
        password: FIRST_SIGN_IN_PASSWORD,
      })
      await expect(homeHeading(page)).toBeVisible()

      await openAccountSection(page, 'security')
      await expandAccountRows(page)
      await expect(usernameSignInSentence(page)).toBeVisible()
    })

    await test.step('the same Google account signs in again to the same account', async () => {
      await demo.accounts.signOut()
      await demo.accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()

      await googleButton(page).click()
      await approveSocialSignIn(page)
      await expect(homeHeading(page)).toBeVisible()

      await openAccountSection(page, 'security')
      await expandAccountRows(page)
      const signInWays = identifiersSection(page)
      await expect(usernameSignInSentence(page)).toBeVisible()
      await expect(
        bindingsSection(page)
          .getByRole('listitem')
          .filter({ hasText: copy('accountUi', 'bindings.provider.google') }),
      ).toBeVisible()
      await expect(
        signInWays.getByRole('combobox', {
          name: copy('accountUi', 'identifiers.kind.email'),
          exact: true,
        }),
      ).toContainText(identityProviderUser.email)
      await openAccountSection(page, 'profile')
      await expandAccountRows(page)
      await expect(
        emailCard(page).getByText(identityProviderUser.email, { exact: true }),
      ).toBeVisible()
    })
  })

  test('social-sign-in: the account-opening password signs in under the provider address', async ({
    demo,
  }) => {
    const { page } = demo

    await test.step('the first Google sign-in opens the account with the chosen username and password', async () => {
      await demo.accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()
      await googleButton(page).click()
      await approveSocialSignIn(page)
      await demo.accounts.answerSocialSignup({
        username: FIRST_SIGN_IN_USERNAME,
        password: FIRST_SIGN_IN_PASSWORD,
      })
      await expect(homeHeading(page)).toBeVisible()
    })

    await test.step('the provider address and the account-opening password sign in to the account', async () => {
      await demo.accounts.signOut()
      await demo.accounts.signIn({
        identifier: identityProviderUser.email,
        password: FIRST_SIGN_IN_PASSWORD,
      })
      await expect(homeHeading(page)).toBeVisible()

      await openAccountSection(page, 'security')
      await expandAccountRows(page)
      const signInWays = identifiersSection(page)
      await expect(usernameSignInSentence(page)).toBeVisible()
      await expect(
        signInWays.getByRole('combobox', {
          name: copy('accountUi', 'identifiers.kind.email'),
          exact: true,
        }),
      ).toContainText(identityProviderUser.email)
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
      await expect(signInHeading(page)).toBeVisible()
      await expect(homeHeading(page)).toHaveCount(0)
    })

    await test.step('the existing account keeps its password sign-in', async () => {
      await demo.accounts.signIn({
        identifier: e2eSocialAccount.username,
        password: e2eSocialAccount.password,
      })
      await expect(homeHeading(page)).toBeVisible()
    })
  })
})
