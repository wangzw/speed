/**
 * The last-sign-in-way journey: an account opened through a social provider
 * holds exactly one way to sign in that is not a passkey -- its external
 * identity. A passkey is enrolled from the account page, which the journey
 * proves is not one of the three ways the rule counts (a password credential,
 * a proved phone number, an external identity); an authenticator app is
 * enrolled beside it for the same reason, and to complete a later login's
 * second factor without proving the passkey. Unlinking the external identity
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
 * THE STEP-UP IS PART OF THE WALK. An unlink on an account that holds an
 * active factor asks for a fresh local multi-factor authentication. Two server
 * rules shape how the walk reaches that challenge: a factor activated during
 * the session that registered it may not strengthen that same session, and a
 * factor whose method the session already proved is not offered again.
 *
 * The passkey is the first factor enrolled, in the provider-opened session.
 * Enrolling a second factor then asks for the same fresh local multi-factor
 * requirement as an unlink, so the walk signs in with the passkey and enrols
 * an authenticator app there. A further sign-in through the provider is
 * completed with the authenticator app: that session proves a local
 * multi-factor login, both factors predate it, and the passkey remains
 * unproven. The freshness window is lowered to its floor and waited out, so
 * the unlink asks for fresh proof: the bindings surface opens the shared
 * step-up dialog, which offers the passkey, and the walk answers with it. The
 * retried unlink then reaches the account module's last-way rule instead of
 * stopping at the gate.
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
// @covers REQ-security-040
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { identityProviderUser } from '../fixtures/identity-provider'
import { copy } from '../helpers/i18n'
import { approveSocialSignIn } from '../helpers/social'
import { totpCode } from '../helpers/totp'
import {
  addVirtualAuthenticator,
  type VirtualAuthenticator,
} from '../helpers/virtual-authenticator'

// The provider round trip, the passkey ceremonies, the two sign-ins and the
// freshness wait run inside this file's per-test bound; fixture assembly
// included.
test.describe.configure({ timeout: 300_000 })

/** The lowered `authn.mfa.recent-window`, in milliseconds. */
const RECENT_WINDOW_MS = 60_000

/** The margin added on top of the window before the unlink must ask again. */
const WINDOW_MARGIN_MS = 3_000

/** The password the account sets after the refused unlink. */
const NEW_PASSWORD = 'e2e-Password-789'

/** The last authenticator window this spec spent a code in, so two
 * submissions never reuse one code. */
let lastCodeStep = -1

test.beforeEach(() => {
  lastCodeStep = -1
})

/** A code for the next window this spec has not used (see the authenticator
 * app's own thirty-second window). */
async function nextTotpCode(secret: string): Promise<string> {
  const stepMs = 30_000
  let step = Math.floor(Date.now() / stepMs)
  if (step <= lastCodeStep) {
    const resumeAt = (lastCodeStep + 1) * stepMs + 500
    const wait = resumeAt - Date.now()
    if (wait > 0) {
      await new Promise<void>((resolve) => setTimeout(resolve, wait))
    }
    step = Math.floor(Date.now() / stepMs)
  }
  lastCodeStep = step
  return totpCode(secret, new Date(step * stepMs))
}

/** The shared secret the rendered authenticator setup URI carries. */
function secretOf(setupUri: string): string {
  const secret = new URL(setupUri).searchParams.get('secret')
  if (secret === null || secret === '') {
    throw new Error(
      'the authenticator setup URI the page rendered carries no secret',
    )
  }
  return secret
}

/** Wait until the given instant has passed, for a backend window. */
async function waitUntil(instant: number): Promise<void> {
  const wait = instant - Date.now()
  if (wait > 0) {
    await new Promise<void>((resolve) => setTimeout(resolve, wait))
  }
}

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

/** The sign-in surface's factor choice for one kind's wording key. */
function signInFactorButton(page: Page, kindKey: string): Locator {
  return page.getByRole('button', {
    name: copy('authUi', kindKey),
    exact: true,
  })
}

/** The sign-in flow's one-time-code field. */
function signInCodeField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'smsSignIn.codeLabel'),
    exact: true,
  })
}

/** The sign-in flow's submit control. */
function signInSubmit(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'signInFlow.submit'),
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

/** Clear the unlink's step-up challenge with the enrolled passkey. */
async function proveWithPasskey(page: Page): Promise<void> {
  const dialog = page.getByRole('dialog', {
    name: copy('accountUi', 'mfa.stepUp.title'),
    exact: true,
  })
  await expect(dialog).toBeVisible({ timeout: 15_000 })
  await dialog
    .getByRole('button', {
      name: copy('accountUi', 'mfa.factor.kind.webauthn'),
      exact: true,
    })
    .click()

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
// local identity provider. The freshness window is lowered to the setting's
// domain floor so the unlink's step-up is reachable in a browser run.
test.use({ socialIdentityProvider: true, recentWindow: '1m' })

test('last-login-way: removing the last way that is not a passkey is refused', async ({
  demo,
}) => {
  const { page } = demo
  const googleLabel = copy('accountUi', 'bindings.provider.google')
  let authenticator: VirtualAuthenticator | undefined
  // The authenticator app's shared secret, captured from its setup URI.
  let totpSecret = ''
  // When the re-established session authenticated, for the freshness window.
  let authenticatedAt = 0

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

    await test.step('the owner signs in with the passkey and enrols an authenticator app', async () => {
      // Enrolling a second factor asks for the same fresh local multi-factor
      // requirement as an unlink. The passkey sign-in supplies it, and the
      // authenticator app enrolled here is the factor a later login will use
      // so that the passkey stays unproven.
      await signOut(page)
      await demo.accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()
      await signInFactorButton(page, 'signInFlow.factorKind.webauthn').click()
      await expect(homeHeading(page)).toBeVisible({ timeout: 30_000 })

      await accountLink(page).click()
      await expect(accountHeading(page)).toBeVisible()
      const mfa = section(page, copy('accountUi', 'mfa.title'))
      await mfa
        .getByRole('button', {
          name: copy('accountUi', 'mfa.enroll.totp'),
          exact: true,
        })
        .click()
      const uriField = mfa.getByRole('textbox', {
        name: copy('accountUi', 'flowStep.enrollTotpUriLabel'),
        exact: true,
      })
      await expect(uriField).toBeVisible()
      totpSecret = secretOf(await uriField.inputValue())
      const codeField = mfa.getByRole('textbox', {
        name: copy('accountUi', 'flowStep.codeLabel'),
        exact: true,
      })
      await expect(codeField).toBeVisible()
      await codeField.fill(await nextTotpCode(totpSecret))
      await mfa
        .getByRole('button', {
          name: copy('accountUi', 'mfa.enroll.confirmLabel'),
          exact: true,
        })
        .click()
      // The accepted code leaves the code form; a settled flow then parks its
      // ready step as one further confirmation of the same control.
      await expect(codeField).toHaveCount(0, { timeout: 15_000 })
      const totpParked = mfa.getByRole('button', {
        name: copy('accountUi', 'mfa.enroll.confirmLabel'),
        exact: true,
      })
      if ((await totpParked.count()) > 0 && (await totpParked.isVisible())) {
        await totpParked.click()
      }
      await expect(
        mfa.getByText(
          copy('accountUi', 'mfa.enroll.enrolled', {
            factor: copy('accountUi', 'mfa.factor.kind.totp'),
          }),
          { exact: true },
        ),
      ).toBeVisible({ timeout: 15_000 })
    })

    await test.step('the owner signs back in through the provider', async () => {
      // Two server rules shape this login: a factor activated during the
      // session that registered it may not strengthen that same session, and
      // a factor whose method the session already proved is not offered again.
      // The provider sign-in is completed with the authenticator app, so the
      // session is a local multi-factor login, the passkey predates it, and
      // the passkey remains unproven for the unlink's step-up.
      await signOut(page)
      await demo.accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()
      await googleButton(page).click()
      await approveSocialSignIn(page)
      const totpChoice = signInFactorButton(
        page,
        'signInFlow.factorKind.totp',
      )
      await expect(totpChoice).toBeVisible({ timeout: 15_000 })
      await totpChoice.click()
      await expect(signInCodeField(page)).toBeVisible()
      await signInCodeField(page).fill(await nextTotpCode(totpSecret))
      await signInSubmit(page).click()
      await expect(homeHeading(page)).toBeVisible({ timeout: 15_000 })
      authenticatedAt = Date.now()

      await accountLink(page).click()
      await expect(accountHeading(page)).toBeVisible()
      // The same account: the provider's address and the Google binding are
      // both here, and so are both factors.
      await expect(
        section(page, copy('accountUi', 'identifiers.title'))
          .getByRole('listitem')
          .filter({ hasText: identityProviderUser.email }),
      ).toBeVisible()
      await expect(googleRow(page)).toBeVisible()
    })

    await test.step('unlinking the external identity asks for fresh proof and is then refused', async () => {
      // The session's multi-factor proof has to be older than the lowered
      // freshness window. This wait spans that backend window; it is not a
      // reaction to a render state.
      await waitUntil(authenticatedAt + RECENT_WINDOW_MS + WINDOW_MARGIN_MS)

      await googleUnlink(page).click()
      await confirmDialog(
        page,
        copy('accountUi', 'bindings.confirmTitle'),
        copy('accountUi', 'bindings.confirmLabel'),
      )

      // The unlink asks for fresh local multi-factor proof. The bindings
      // surface opens the shared step-up dialog, which offers the passkey the
      // account holds and this session has not proved.
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
