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
 * A SECOND PATH COVERS A FACTOR REGISTERED DURING THE SESSION. A password
 * account signs in, enrols its first passkey in that same session, and starts
 * binding Google straight away. The binding route asks for a fresh local
 * multi-factor authentication, and the account's only factor -- the passkey --
 * was registered after the session authenticated, so it may not strengthen the
 * session on its own. The step-up therefore re-proves the password first and
 * only then offers the passkey as the second factor; the binding continues
 * where it started and returns to the security pane it was started from. The
 * server behaviour this exercises is that a factor registered during the
 * session becomes usable after the password is proved again. Without it the
 * dialog reports that the session cannot be confirmed and ends it with a
 * re-login, which is what the second test's first assertions rule out.
 *
 * THE PASSKEY IS A BROWSER-DEVICE FIXTURE, as in the passkey
 * user-verification journey: Chromium's virtual authenticator answers the
 * product's own WebAuthn ceremony (the page still calls
 * `navigator.credentials.create`/`.get`) over the one CDP family the honesty
 * gate allows. Nothing else is reached around the browser.
 *
 * Expected product copy is read from the shipped en-US bundles through
 * helpers/i18n.ts. The provider's own page copy belongs to the provider, so it
 * is the fixture's constant and helper/social.ts asserts it. The browser is
 * pinned to en-US and UTC by the Playwright config.
 */
// @journey journey-social-account-linking.md
// @covers REQ-account-027, REQ-account-028, REQ-account-053, REQ-account-067, REQ-signin-006, REQ-signin-015, REQ-signin-017, REQ-signin-027, REQ-signin-028, REQ-signup-049
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { identityProviderUser } from '../fixtures/identity-provider'
import {
  expandAccountRows,
  openAccountSection,
} from '../helpers/account-sections'
import { copy } from '../helpers/i18n'
import { submitFactorNameForm } from '../helpers/mfa'
import { approveSocialSignIn } from '../helpers/social'
import {
  addVirtualAuthenticator,
  type VirtualAuthenticator,
} from '../helpers/virtual-authenticator'

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

/**
 * The account of the second path: a password account opened with the address
 * the provider serves and proved through the review mail carrier, so the link
 * purpose is not refused for an unproved contact. It holds no factor when the
 * test starts; the passkey it enrols is the first one, registered after the
 * session that enrols it authenticated.
 */
const passkeyLinkerAccount = {
  username: 'passkey-linker',
  email: identityProviderUser.email,
  password: 'e2e-Password-123',
} as const

/**
 * The name the second path gives its passkey. Enrolment stores a name, and the
 * step-up surface offers the account's factors by that name rather than by
 * kind, so every selection below addresses the same fixed string.
 */
const PASSKEY_FACTOR_NAME = 'Backup passkey'

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

/** The security pane's social accounts card. */
function bindingsSection(page: Page) {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.social'),
    exact: true,
  })
}

/** The bound Google row: the provider's label. */
function googleRow(page: Page) {
  return bindingsSection(page)
    .getByRole('listitem')
    .filter({ hasText: copy('accountUi', 'bindings.provider.google') })
}

/** The action that starts linking one provider. */
function bindButton(page: Page, provider: string) {
  return bindingsSection(page).getByRole('button', {
    name: copy('accountUi', 'bindings.bindAriaWithProvider', { provider }),
    exact: true,
  })
}

/** The security pane's own heading, the page's one h1. */
function securityPaneHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'account.sections.security'),
    exact: true,
  })
}

/** One account section card, named by its own h2 title. */
function section(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

/** The security pane's two-step-verification card. */
function mfaSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.mfa'))
}

/** The factor kinds the review assembly serves, each its own group. */
type MfaKind = 'totp' | 'webauthn' | 'sms'

/**
 * One method group inside the two-step-verification card, named by its own
 * heading. While an enrollment is open the heading names the enrollment, so a
 * group is addressed by its kind only while it lists that kind's entries.
 */
function mfaGroup(page: Page, kind: MfaKind): Locator {
  return mfaSection(page).getByRole('region', {
    name: copy('accountUi', `mfa.factor.kind.${kind}`),
    exact: true,
  })
}

/**
 * The add action on a method group's heading. A passkey group that already has
 * an enabled row words it "add another device"; every other group, and a
 * passkey with no enabled row, words it "add".
 */
function addFactorAction(
  page: Page,
  kind: MfaKind,
  another: boolean,
): Locator {
  const method = copy('accountUi', `mfa.factor.kind.${kind}`)
  return mfaGroup(page, kind).getByRole('button', {
    name: copy('accountUi', another ? 'mfa.manageAria' : 'mfa.addAria', {
      method,
    }),
    exact: true,
  })
}

/**
 * Click the enrolment wizard's confirmation when a settled answer parks the
 * flow's ready step as one further confirmation. The wizard closes itself when
 * the answer settles `enrolled`, so the call is a no-op then.
 */
async function confirmSettledEnrolment(mfa: Locator): Promise<void> {
  const confirm = mfa.getByRole('button', {
    name: copy('accountUi', 'mfa.enroll.confirmLabel'),
    exact: true,
  })
  if ((await confirm.count()) > 0 && (await confirm.isVisible())) {
    await confirm.click()
  }
}

/**
 * Enrol a passkey on the open account page. The wizard's confirmation carries
 * the user activation the creation ceremony needs, and the virtual
 * authenticator answers it.
 */
async function enrolPasskey(page: Page): Promise<void> {
  const mfa = mfaSection(page)
  await addFactorAction(page, 'webauthn', false).click()
  await submitFactorNameForm(mfa, PASSKEY_FACTOR_NAME)
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
  // The ceremony settles and the step leaves; a parked ready step is one
  // further confirmation of the same control.
  await expect(
    mfa.getByText(copy('accountUi', 'flowStep.passkeyRegisterPrompt'), {
      exact: true,
    }),
  ).toHaveCount(0, { timeout: 20_000 })
  await confirmSettledEnrolment(mfa)
  await expect(
    mfa.getByText(
      copy('accountUi', 'mfa.enroll.enrolled', {
        factor: copy('accountUi', 'mfa.factor.kind.webauthn'),
      }),
      { exact: true },
    ),
  ).toBeVisible({ timeout: 20_000 })
  // The settled enrolment lists one enabled passkey in the passkey group.
  const group = mfaGroup(page, 'webauthn')
  await expect(group.getByRole('listitem')).toHaveCount(1)
  await expect(
    group.getByText(PASSKEY_FACTOR_NAME, { exact: true }),
  ).toBeVisible()
}

/** The step-up dialog the account surfaces open on a refused write. */
function stepUpDialog(page: Page): Locator {
  return page.getByRole('dialog', {
    name: copy('accountUi', 'mfa.stepUp.title'),
    exact: true,
  })
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
    await openAccountSection(page, 'security')
    await expandAccountRows(page)
  })

  await test.step('the section lists no Google row and offers Google to add', async () => {
    const bindings = bindingsSection(page)
    await expect(bindings).toBeVisible()

    // Nothing is bound yet; no row carries the bound state, and the add area
    // is what keeps the section open.
    await expect(
      bindings.getByText(copy('accountUi', 'bindings.status.bound'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(bindButton(page, google)).toBeVisible()
    // A second enabled redirect method is offered too, so the action is the
    // deployment's channel list rather than a single hard-wired one.
    await expect(bindButton(page, github)).toBeVisible()
  })

  await test.step('choosing Google binds it and returns to the page it started from', async () => {
    await bindButton(page, google).click()
    await approveSocialSignIn(page)

    // The callback completes the link and the host returns to the page the
    // binding started from -- the security pane, not the profile pane the
    // account group opens at.
    await expect(securityPaneHeading(page)).toBeVisible()
    await expandAccountRows(page)
    await expect(googleRow(page)).toBeVisible()
    // The row names the provider's address, so the binding is the identity the
    // provider proved, not a second account.
    await expect(
      googleRow(page).getByText(identityProviderUser.email, { exact: true }),
    ).toBeVisible()
  })

  await test.step('the bound channel leaves the add area and the session stays', async () => {
    // An already bound channel is no longer offered; the other one still is.
    await expect(bindButton(page, google)).toHaveCount(0)
    await expect(bindButton(page, github)).toBeVisible()
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
    await openAccountSection(page, 'security')
    await expandAccountRows(page)
    await expect(googleRow(page)).toBeVisible()
    const identifiers = page.getByRole('region', {
      name: copy('demo', 'account.rows.label.loginWays'),
      exact: true,
    })
    // The account's only address is also its sign-in way, so the email group
    // lists it as the chosen choice.
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

// The second path: the account enrols its first passkey in the session that
// then binds Google. Registration, the mail code, a password sign-in, the
// passkey's creation ceremony, the step-up's password and passkey ceremonies,
// and the provider round trip; the bound covers fixture assembly as well as
// the journey, so a slow assembly reports the harness's own diagnostics.
test.describe('a passkey enrolled in this session', () => {
  test.describe.configure({ timeout: 300_000 })

  test('social-account-linking: a passkey enrolled in this session carries the binding step-up', async ({
    demo,
  }) => {
    const { page, server } = demo
    const google = copy('accountUi', 'bindings.provider.google')
    let authenticator: VirtualAuthenticator | undefined

    try {
      await test.step('precondition: an account is registered with the provider address proved, then signs in with its password', async () => {
        // The registration signs the new account in; the mail carrier's code
        // proves the address, so the link purpose is not refused for an
        // unproved contact. Signing out and back in makes the passkey's
        // registration land after the session's own authentication, which is
        // the precondition this path exists for.
        await demo.accounts.registerAndVerify(passkeyLinkerAccount)
        await demo.accounts.signOut()
        await demo.accounts.signIn({
          identifier: passkeyLinkerAccount.username,
          password: passkeyLinkerAccount.password,
        })
        await expect(homeHeading(page)).toBeVisible()
        // The WebAuthn ceremonies are answered by Chromium's virtual
        // authenticator on this page's target.
        authenticator = await addVirtualAuthenticator(page.context(), page)
      })

      await test.step('step 1: the account enrols its first passkey in the same session', async () => {
        await openAccountSection(page, 'security')
        await expandAccountRows(page)
        await enrolPasskey(page)
        // The passkey is the account's only factor and its group lists it.
        await expect(
          mfaGroup(page, 'webauthn').getByText(PASSKEY_FACTOR_NAME, {
            exact: true,
          }),
        ).toBeVisible()
      })

      await test.step('step 2: starting the Google binding asks for the password first, not for a re-login', async () => {
        await bindButton(page, google).click()

        // The account has a factor, so the challenge does not report missing
        // two-step verification; the only factor post-dates the sign-in, so
        // the server starts the flow at the password and offers the passkey
        // afterwards. The dialog therefore shows the credential form.
        const dialog = stepUpDialog(page)
        await expect(dialog).toBeVisible({ timeout: 15_000 })
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
        await expect(identifierField).toBeVisible({ timeout: 15_000 })
        await expect(passwordField).toBeVisible()
        // The pre-fix outcome is the refusal this path rules out: a session
        // that cannot be confirmed at all, ending in a re-login.
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

        await identifierField.fill(passkeyLinkerAccount.username)
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
          name: PASSKEY_FACTOR_NAME,
          exact: true,
        })
        await expect(factorChoice).toBeVisible({ timeout: 15_000 })
        await factorChoice.click()

        // Choosing the factor is the whole interaction: the assertion step
        // starts the browser ceremony as soon as it arrives. The dialog
        // closes only after the ready state is taken, which is the elevation
        // the refused binding is waiting for.
        await expect(
          dialog.getByText(copy('accountUi', 'flowStep.passkeyPrompt'), {
            exact: true,
          }),
        ).toHaveCount(0, { timeout: 30_000 })
        await expect(dialog).toHaveCount(0, { timeout: 30_000 })
      })

      await test.step('step 4: the replayed binding completes at the provider and returns to the security pane it started from', async () => {
        await approveSocialSignIn(page)

        // The round trip carries its source page in the flow's return target,
        // so the callback lands back on the security pane rather than the
        // profile the account group opens at.
        await expect(page).toHaveURL(
          new URL('/account/security', server.url).toString(),
          { timeout: 30_000 },
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
        // The step-up elevated the existing session rather than replacing it:
        // the shell is still signed in and the sign-in surface was never
        // visited for the binding.
        await expect(
          page.getByRole('banner').getByTestId('user-menu-trigger'),
        ).toBeVisible()
        await expect(signInHeading(page)).toHaveCount(0)
      })
    } finally {
      // The passkey device is the spec's own; closing it keeps a failed run
      // from holding the notification stream open.
      await authenticator?.close().catch(() => undefined)
    }
  })
})
