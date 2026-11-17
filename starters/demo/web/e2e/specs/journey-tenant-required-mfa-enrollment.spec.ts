/**
 * The tenant-required multi-factor enrollment journey: a member of an
 * organization whose `tenant.admission.require-mfa` is on has no second factor,
 * signs in with the password, and is forced to enroll an authenticator app in
 * the same login flow. Completing the registration does not complete the
 * login: the flow re-proves the password, offers the factor just registered as
 * the second factor, and only then establishes a multi-factor session and lands
 * on the organization home.
 *
 * The journey is driven through the rendered password channel. Every step is a
 * control a person presses; the one non-UI computation is the six-digit code,
 * which the enrollment step displays a setup key for and an authenticator app
 * would generate. The helper at `helpers/totp.ts` computes it from that key,
 * exactly as a person imports the key into their app.
 *
 * NO SESSION IS ISSUED MID-FLOW. While the flow is parked at the registration,
 * a second tab in the same browser context opens the deployment root and finds
 * the sign-in surface: the login has not authenticated anything yet, so the
 * rest of the application is unreachable. This is the observable form of "the
 * login cannot complete without the factor".
 *
 * CODES ARE SINGLE USE PER WINDOW. The code submitted to confirm the
 * registration is spent; REQ-security-012 refuses a code accepted in the same
 * thirty-second window. The second-factor code is therefore generated for the
 * next window, never reusing the one the enrollment confirmed with.
 *
 * `@covers` is the requirement set the assertions can fail on: the login stops
 * at a required second factor (REQ-security-008), the factor registered during
 * this login is used only after the password is proved again
 * (REQ-security-010), and the tenant's own require-mfa is what made enrollment
 * compulsory rather than a single-factor completion (REQ-security-046).
 */
// @journey journey-tenant-required-mfa-enrollment.md
// @covers REQ-security-008, REQ-security-010, REQ-security-046
import type { Locator, Page } from '@playwright/test'

import { reviewMember } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import { scaledTimeout } from '../fixtures/time-scale'
import { switchToTenant } from '../helpers/switch-tenant'
import { copy } from '../helpers/i18n'
import { homeHeading } from '../helpers/organization-admin'
import { totpCode } from '../helpers/totp'

/** The seeded member: no second factor, an active member of the tenant. */
const MEMBER = reviewMember

/** The organization whose own layer requires multi-factor. */
const TENANT = reviewMember.tenant

/** The six-digit code's own window; the register's parameters. */
const STEP_MS = 30_000

/** The window the spec last presented a code from. */
let lastCodeStep = -1

// The tenant's own require-mfa is seeded into its layer, and the member has no
// factor. Every other spec keeps the template's own seed.
test.use({ seedMember: true, tenantRequireMFA: true })

/** The password channel's panel: the step renderer replaces the form in it. */
function passwordPanel(page: Page): Locator {
  return page.getByRole('tabpanel', {
    name: copy('authUi', 'passwordSignIn.title'),
    exact: true,
  })
}

/**
 * A code for the next window this spec has not used, computed at the instant it
 * is typed. The enrollment confirms with one code; the later second factor must
 * use another window, because a code accepted once is refused in the same
 * window.
 */
async function nextCode(secret: string): Promise<string> {
  let step = Math.floor(Date.now() / STEP_MS)
  if (step <= lastCodeStep) {
    const resumeAt = (lastCodeStep + 1) * STEP_MS + 500
    const wait = resumeAt - Date.now()
    if (wait > 0) {
      await new Promise<void>((resolve) => setTimeout(resolve, wait))
    }
    step = Math.floor(Date.now() / STEP_MS)
  }
  lastCodeStep = step
  return totpCode(secret, new Date(step * STEP_MS))
}

/**
 * Read the base32 setup key the enrollment step's manual-entry disclosure
 * shows, the value a person types into an authenticator app. The panel groups
 * it into four-character blocks; the authenticator takes it without the spaces.
 */
async function revealedTotpSecret(panel: Locator): Promise<string> {
  await panel
    .getByRole('button', {
      name: copy('authUi', 'signInFlow.totpManualToggle'),
      exact: true,
    })
    .click()
  const secret = panel.locator('code')
  await expect(secret).toHaveCount(1)
  await expect(secret).toBeVisible()
  return (await secret.innerText()).replace(/\s+/gu, '')
}

test('tenant-required-mfa-enrollment: a required factor is enrolled, the password proved again, and the new factor used before the session exists', async ({
  demo,
}) => {
  test.setTimeout(scaledTimeout(180_000))
  const { page, server, accounts } = demo
  const panel = passwordPanel(page)

  await test.step('the member signs in with the password and the flow stops at a required registration', async () => {
    await accounts.signIn({
      identifier: MEMBER.username,
      password: MEMBER.password,
    })

    // The proved account has no factor and the tenant requires one: the flow
    // parks on the registration instead of completing. The sign-in form is gone
    // and the enrollment choice is what renders in its place.
    const totpKind = panel.getByRole('button', {
      name: copy('authUi', 'signInFlow.factorKind.totp'),
      exact: true,
    })
    await expect(totpKind).toBeVisible()
    await expect(
      panel.getByText(copy('authUi', 'signInFlow.enrollTitle'), {
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('no session exists while the login is parked at enrollment', async () => {
    // A second tab shares this context's cookies. If the login had issued a
    // session, it would find the application; it finds the sign-in surface,
    // which is the rest of the application being unreachable before the factor
    // is proven.
    const other = await page.context().newPage()
    try {
      await other.goto(`${server.url}/`)
      await expect(
        other.getByRole('heading', {
          name: copy('demo', 'signIn.heading'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(homeHeading(other)).toHaveCount(0)
    } finally {
      await other.close()
    }
  })

  let secret = ''

  await test.step('the member registers an authenticator app', async () => {
    await panel
      .getByRole('button', {
        name: copy('authUi', 'signInFlow.factorKind.totp'),
        exact: true,
      })
      .click()

    // The setup panel draws the QR symbol and withholds neither the guidance
    // nor the manual-entry key; the key is what this spec reads.
    const guidance = panel.getByText(copy('authUi', 'signInFlow.enrollTotpHelp'), {
      exact: true,
    })
    await expect(guidance).toBeVisible()
    secret = await revealedTotpSecret(panel)

    await panel
      .getByRole('textbox', {
        name: copy('authUi', 'signInFlow.codeLabel'),
        exact: true,
      })
      .fill(await nextCode(secret))
    await panel
      .getByRole('button', {
        name: copy('authUi', 'signInFlow.submit'),
        exact: true,
      })
      .click()
  })

  await test.step('the registration does not complete the login: the password is proved again', async () => {
    const identifier = panel.getByRole('textbox', {
      name: copy('authUi', 'passwordSignIn.identifierLabel'),
      exact: true,
    })
    await expect(identifier).toBeVisible()
    await identifier.fill(MEMBER.username)
    await panel
      .getByRole('textbox', {
        name: copy('authUi', 'passwordSignIn.passwordLabel'),
        exact: true,
      })
      .fill(MEMBER.password)
    await panel
      .getByRole('button', {
        name: copy('authUi', 'signInFlow.submit'),
        exact: true,
      })
      .click()
  })

  await test.step('the factor just registered is the second factor, and the login completes with it', async () => {
    const totpChoice = panel.getByRole('button', {
      name: copy('authUi', 'signInFlow.factorKind.totp'),
      exact: true,
    })
    await expect(totpChoice).toBeVisible()
    await totpChoice.click()

    const code = panel.getByRole('textbox', {
      name: copy('authUi', 'smsSignIn.codeLabel'),
      exact: true,
    })
    await expect(code).toBeVisible()
    await code.fill(await nextCode(secret))
    await panel
      .getByRole('button', {
        name: copy('authUi', 'signInFlow.submit'),
        exact: true,
      })
      .click()
  })

  await test.step('the member reaches the organization home', async () => {
    await expect(homeHeading(page)).toBeVisible()
    // The member's own organization is the personal one the host settles
    // first; moving to the tenant that requires the factor exercises the
    // tenant-scoped reads with the session the login just established.
    await switchToTenant(page, TENANT)
    await expect(homeHeading(page)).toBeVisible()
  })
})
