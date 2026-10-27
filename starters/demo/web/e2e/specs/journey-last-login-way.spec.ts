/**
 * The last-sign-in-way journey: an account opened through a social provider
 * holds exactly one way to sign in that is not a passkey -- its external
 * identity. A passkey is enrolled from the account page, which the journey
 * proves is not one of the three ways the rule counts (a password credential,
 * a proved phone number, an external identity). Unlinking the external identity
 * while it is the only such way is refused and changes nothing: the refusal is
 * rendered, and the binding stays listed. The owner then sets a password from
 * the same account page; the external identity is no longer the only
 * non-passkey way, and the same unlink now succeeds.
 *
 * The social round trip is the harness's real OpenID Connect provider: the
 * browser leaves for its authorization page and clicks its control, so no route
 * is intercepted and no response is faked. The passkey is Chromium's virtual
 * authenticator (helpers/virtual-authenticator.ts): the page still calls
 * `navigator.credentials.create`/`.get`, and the fixture answers as a platform
 * authenticator would.
 *
 * THE PASSKEY SIGN-IN IS PART OF THE WALK. An unlink on an account that holds
 * an active factor asks for a fresh local multi-factor authentication, and the
 * social session is a single non-local factor. The bindings surface has no
 * step-up dialog of its own, so the walk reaches that freshness the way the
 * account itself can: it signs out and signs back in with the passkey it just
 * enrolled, which proves the same account locally with a passkey and starts a
 * fresh local multi-factor session. The unlink then reaches the account
 * module's last-way rule instead of stopping at the step-up gate.
 *
 * WHAT IS NOT ASSERTED. The shipped refusal sentence is the account surface's
 * generic conflict text; it does not itself say "add another way first", so
 * this spec does not claim the requirement's wording clause. It asserts the
 * rendered refusal and that the binding survives it.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts.
 */
// @journey journey-last-login-way.md
// @covers REQ-security-040
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { identityProviderUser } from '../fixtures/identity-provider'
import { copy } from '../helpers/i18n'
import { approveSocialSignIn } from '../helpers/social'
import {
  addVirtualAuthenticator,
  type VirtualAuthenticator,
} from '../helpers/virtual-authenticator'

// The provider round trip, the passkey ceremonies and the sign-out/sign-in
// pair run inside this file's per-test bound; fixture assembly included.
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

/** The sign-in surface's passkey channel. */
function passkeyButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'signInFlow.factorKind.webauthn'),
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

/** Sign a landed session out through the account menu. */
async function signOut(page: Page): Promise<void> {
  await page.getByRole('banner').getByTestId('user-menu-trigger').click()
  await page
    .getByRole('menuitem', {
      name: copy('demo', 'userMenu.signOut'),
      exact: true,
    })
    .click()
}

// The journey drives the provider's real round trip, so its server starts the
// local identity provider.
test.use({ socialIdentityProvider: true })

test('last-login-way: removing the last way that is not a passkey is refused', async ({
  demo,
}) => {
  const { page } = demo
  const googleLabel = copy('accountUi', 'bindings.provider.google')
  let authenticator: VirtualAuthenticator | undefined

  try {
    await test.step('a social sign-in opens an account with no password and no phone', async () => {
      await demo.accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()
      await googleButton(page).click()
      await approveSocialSignIn(page)
      await expect(homeHeading(page)).toBeVisible()

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
    })

    await test.step('a passkey is enrolled from the account page', async () => {
      authenticator = await addVirtualAuthenticator(page.context(), page)
      const mfa = section(page, copy('accountUi', 'mfa.title'))
      await mfa
        .getByRole('button', {
          name: copy('accountUi', 'mfa.enroll.webauthn'),
          exact: true,
        })
        .click()
      const prompt = mfa.getByText(
        copy('accountUi', 'flowStep.passkeyRegisterPrompt'),
        { exact: true },
      )
      await expect(prompt).toBeVisible()
      await mfa
        .getByRole('button', {
          name: copy('accountUi', 'mfa.enroll.confirmLabel'),
          exact: true,
        })
        .click()
      await expect(prompt).toHaveCount(0, { timeout: 20_000 })

      // The accepted credential may leave a parked ready step whose
      // confirmation carries the same label; the machine is present, so one
      // more click settles it.
      const parked = mfa.getByRole('button', {
        name: copy('accountUi', 'mfa.enroll.confirmLabel'),
        exact: true,
      })
      if ((await parked.count()) > 0 && (await parked.isVisible())) {
        await parked.click()
      }
      await expect(
        mfa.getByText(
          copy('accountUi', 'mfa.enroll.enrolled', {
            factor: copy('accountUi', 'mfa.factor.kind.webauthn'),
          }),
          { exact: true },
        ),
      ).toBeVisible({ timeout: 20_000 })
    })

    await test.step('the owner signs back in with the passkey it just enrolled', async () => {
      await signOut(page)
      await demo.accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()
      await passkeyButton(page).click()
      await expect(homeHeading(page)).toBeVisible({ timeout: 30_000 })

      await accountLink(page).click()
      await expect(accountHeading(page)).toBeVisible()
      // The same account: the provider's address and the Google binding are
      // both here.
      await expect(
        section(page, copy('accountUi', 'identifiers.title'))
          .getByRole('listitem')
          .filter({ hasText: identityProviderUser.email }),
      ).toBeVisible()
      await expect(googleRow(page)).toBeVisible()
    })

    await test.step('unlinking the external identity is refused and the binding stays', async () => {
      await googleUnlink(page).click()
      await confirmDialog(
        page,
        copy('accountUi', 'bindings.confirmTitle'),
        copy('accountUi', 'bindings.confirmLabel'),
      )

      // The account module refuses to remove the last non-passkey way: the
      // refusal renders in the bindings section's result region, and the row
      // it would have removed is still listed.
      await expect(
        bindingsSection(page)
          .getByRole('alert')
          .filter({ hasText: copy('accountUi', 'errors.authn.conflict') }),
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
