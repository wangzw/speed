// @journey journey-social-account-linking.md
// @covers REQ-account-027, REQ-account-028, REQ-account-053, REQ-account-067, REQ-signin-006, REQ-signin-015, REQ-signin-017, REQ-signin-027, REQ-signin-028, REQ-signup-049
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { identityProviderUser } from '../fixtures/identity-provider'
import { scaledTimeout } from '../fixtures/time-scale'
import {
  expandAccountRows,
  openAccountSection,
} from '../helpers/account-sections'
import { copy } from '../helpers/i18n'
import { fillFactorName } from '../helpers/mfa'
import { approveSocialSignIn } from '../helpers/social'
import {
  addVirtualAuthenticator,
  type VirtualAuthenticator,
} from '../helpers/virtual-authenticator'

const linkerAccount = {
  username: 'social-linker',
  email: identityProviderUser.email,
  password: 'e2e-Password-123',
} as const

const passkeyLinkerAccount = {
  username: 'passkey-linker',
  email: identityProviderUser.email,
  password: 'e2e-Password-123',
} as const

const PASSKEY_FACTOR_NAME = 'Backup passkey'

function signInGoogleButton(page: Page) {
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

function bindingsSection(page: Page) {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.social'),
    exact: true,
  })
}

function googleRow(page: Page) {
  return bindingsSection(page)
    .getByRole('listitem')
    .filter({ hasText: copy('accountUi', 'bindings.provider.google') })
}

function bindButton(page: Page, provider: string) {
  return bindingsSection(page).getByRole('button', {
    name: copy('accountUi', 'bindings.bindAriaWithProvider', { provider }),
    exact: true,
  })
}

function securityPaneHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'account.sections.security'),
    exact: true,
  })
}

function section(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

function mfaSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.mfa'))
}

type MfaKind = 'totp' | 'webauthn' | 'sms'

function mfaGroup(page: Page, kind: MfaKind): Locator {
  return mfaSection(page).getByRole('region', {
    name: copy('accountUi', `mfa.factor.kind.${kind}`),
    exact: true,
  })
}

function addFactorAction(page: Page, kind: MfaKind): Locator {
  const method = copy('accountUi', `mfa.factor.kind.${kind}`)
  return mfaGroup(page, kind).getByRole('button', {
    name: copy('accountUi', 'mfa.addAria', { method }),
    exact: true,
  })
}

async function enrolPasskey(page: Page): Promise<void> {
  const mfa = mfaSection(page)
  await addFactorAction(page, 'webauthn').click()
  await fillFactorName(mfa, PASSKEY_FACTOR_NAME)
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
  await expect(
    mfa.getByText(copy('accountUi', 'flowStep.passkeyRegisterPrompt'), {
      exact: true,
    }),
  ).toHaveCount(0)
  await expect(
    mfa.getByText(
      copy('accountUi', 'mfa.enroll.enrolled', {
        factor: copy('accountUi', 'mfa.factor.kind.webauthn'),
      }),
      { exact: true },
    ),
  ).toBeVisible()
  const group = mfaGroup(page, 'webauthn')
  await expect(group.getByRole('listitem')).toHaveCount(1)
  await expect(
    group.getByText(PASSKEY_FACTOR_NAME, { exact: true }),
  ).toBeVisible()
}

function stepUpDialog(page: Page): Locator {
  return page.getByRole('dialog', {
    name: copy('accountUi', 'mfa.stepUp.title'),
    exact: true,
  })
}

test.use({ socialIdentityProvider: true })

test('social-account-linking: a password account links Google, then signs in with it', async ({
  demo,
}) => {
  const { page } = demo
  const google = copy('accountUi', 'bindings.provider.google')
  const github = copy('accountUi', 'bindings.provider.github')

  await test.step('an existing password account proves the provider address', async () => {
    await demo.accounts.registerAndVerify(linkerAccount)
    await demo.accounts.signOut()
  })

  await test.step('before linking, the same Google identity is refused', async () => {
    await demo.accounts.openSignIn()
    await expect(signInHeading(page)).toBeVisible()

    await signInGoogleButton(page).click()
    await approveSocialSignIn(page)

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
    await openAccountSection(page, 'security')
    await expandAccountRows(page)
  })

  await test.step('the section lists no Google row and offers Google to add', async () => {
    const bindings = bindingsSection(page)
    await expect(bindings).toBeVisible()

    await expect(
      bindings.getByText(copy('accountUi', 'bindings.status.bound'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(bindButton(page, google)).toBeVisible()
    await expect(bindButton(page, github)).toBeVisible()
  })

  await test.step('choosing Google binds it and returns to the page it started from', async () => {
    await bindButton(page, google).click()
    await approveSocialSignIn(page)

    await expect(securityPaneHeading(page)).toBeVisible()
    await expandAccountRows(page)
    await expect(googleRow(page)).toBeVisible()
    await expect(
      googleRow(page).getByText(identityProviderUser.email, { exact: true }),
    ).toBeVisible()
  })

  await test.step('the bound channel leaves the add area and the session stays', async () => {
    await expect(bindButton(page, google)).toHaveCount(0)
    await expect(bindButton(page, github)).toBeVisible()
    await expect(
      page.getByTestId('account-entry-trigger'),
    ).toBeVisible()
    await expect(signInHeading(page)).toHaveCount(0)
  })

  await test.step('a later Google sign-in enters the same account directly', async () => {
    await demo.accounts.signOut()
    await demo.accounts.openSignIn()

    await signInGoogleButton(page).click()
    await approveSocialSignIn(page)

    await expect(homeHeading(page)).toBeVisible()
    await expect(
      page
        .getByRole('alert')
        .filter({ hasText: copy('authUi', 'errors.authn.link_required') }),
    ).toHaveCount(0)

    await openAccountSection(page, 'security')
    await expandAccountRows(page)
    await expect(googleRow(page)).toBeVisible()
    const identifiers = page.getByRole('region', {
      name: copy('demo', 'account.rows.label.loginWays'),
      exact: true,
    })
    await expect(
      identifiers.getByRole('combobox', {
        name: copy('accountUi', 'identifiers.kind.email'),
        exact: true,
      }),
    ).toContainText(identityProviderUser.email)
  })

  await test.step('the password sign-in still enters that same account', async () => {
    await demo.accounts.signOut()
    await demo.accounts.signIn({
      identifier: linkerAccount.username,
      password: linkerAccount.password,
    })
    await expect(homeHeading(page)).toBeVisible()

    await openAccountSection(page, 'security')
    await expandAccountRows(page)
    await expect(googleRow(page)).toBeVisible()
  })
})

test.describe('a passkey enrolled in this session', () => {
  // Registration, the mail code, a password sign-in, the passkey ceremonies with their step-up, and the provider round trip, plus fixture assembly, exceed the tier's default; keep this bound above it.
  test.describe.configure({ timeout: scaledTimeout(300_000) })

  test('social-account-linking: a passkey enrolled in this session carries the binding step-up', async ({
    demo,
  }) => {
    const { page, server } = demo
    const google = copy('accountUi', 'bindings.provider.google')
    let authenticator: VirtualAuthenticator | undefined

    try {
      await test.step('precondition: an account is registered with the provider address proved, then signs in with its password', async () => {
        await demo.accounts.registerAndVerify(passkeyLinkerAccount)
        await demo.accounts.signOut()
        await demo.accounts.signIn({
          identifier: passkeyLinkerAccount.username,
          password: passkeyLinkerAccount.password,
        })
        await expect(homeHeading(page)).toBeVisible()
        authenticator = await addVirtualAuthenticator(page.context(), page)
      })

      await test.step('step 1: the account enrols its first passkey in the same session', async () => {
        await openAccountSection(page, 'security')
        await expandAccountRows(page)
        await enrolPasskey(page)
        await expect(
          mfaGroup(page, 'webauthn').getByText(PASSKEY_FACTOR_NAME, {
            exact: true,
          }),
        ).toBeVisible()
      })

      await test.step('step 2: starting the Google binding asks for the password first, not for a re-login', async () => {
        await bindButton(page, google).click()

        const dialog = stepUpDialog(page)
        await expect(dialog).toBeVisible()
        await expect(
          dialog.getByText(copy('accountUi', 'mfa.stepUp.description'), {
            exact: true,
          }),
        ).toBeVisible()
        const identifierField = dialog.getByRole('textbox', {
          name: copy('accountUi', 'flowStep.field.identifier'),
          exact: true,
        })
        const passwordField = dialog.getByRole('textbox', {
          name: copy('accountUi', 'flowStep.field.password'),
          exact: true,
        })
        await expect(identifierField).toBeVisible()
        await expect(passwordField).toBeVisible()
        await expect(
          dialog.getByText(copy('accountUi', 'mfa.stepUp.cannotElevate'), {
            exact: true,
          }),
        ).toHaveCount(0)
        await expect(
          dialog.getByRole('button', {
            name: copy('accountUi', 'mfa.stepUp.relogin'),
            exact: true,
          }),
        ).toHaveCount(0)

        await expect(identifierField).toHaveValue(passkeyLinkerAccount.username)
        await expect(identifierField).toHaveAttribute('readonly', '')
        await passwordField.fill(passkeyLinkerAccount.password)
        await dialog
          .getByRole('button', {
            name: copy('accountUi', 'mfa.stepUp.confirmLabel'),
            exact: true,
          })
          .click()
      })

      await test.step('step 3: the proved password unlocks the passkey as the second factor, and its ceremony completes the step-up', async () => {
        const dialog = stepUpDialog(page)
        const factorChoice = dialog.getByRole('button', {
          name: copy('accountUi', 'mfa.factor.kind.webauthn'),
          exact: true,
        })
        await expect(factorChoice).toBeVisible()
        await factorChoice.click()

        await expect(
          dialog.getByText(copy('accountUi', 'flowStep.passkeyPrompt'), {
            exact: true,
          }),
        ).toHaveCount(0)
        await expect(dialog).toHaveCount(0)
      })

      await test.step('step 4: the replayed binding completes at the provider and returns to the security pane it started from', async () => {
        await approveSocialSignIn(page)

        await expect(page).toHaveURL(
          new URL('/account/security', server.url).toString(),
        )
        await expect(securityPaneHeading(page)).toBeVisible()
        await expandAccountRows(page)
        await expect(googleRow(page)).toBeVisible()
        await expect(
          googleRow(page).getByText(identityProviderUser.email, {
            exact: true,
          }),
        ).toBeVisible()
        await expect(bindButton(page, google)).toHaveCount(0)
        await expect(
          page.getByTestId('account-entry-trigger'),
        ).toBeVisible()
        await expect(signInHeading(page)).toHaveCount(0)
      })
    } finally {
      await authenticator?.close().catch(() => undefined)
    }
  })
})
