// @journey journey-tenant-required-mfa-enrollment.md
// @covers REQ-security-008, REQ-security-010, REQ-security-046
import type { Locator, Page } from '@playwright/test'

import { e2eMember } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import { scaledTimeout } from '../fixtures/time-scale'
import { switchToTenant } from '../helpers/switch-tenant'
import { copy } from '../helpers/i18n'
import { homeHeading } from '../helpers/organization-admin'
import { totpCode } from '../helpers/totp'

const MEMBER = e2eMember

const TENANT = e2eMember.tenant

const STEP_MS = 30_000

let lastCodeStep = -1

test.use({ seedMember: true, tenantRequireMFA: true })

function passwordPanel(page: Page): Locator {
  return page.getByRole('tabpanel', {
    name: copy('authUi', 'passwordSignIn.title'),
    exact: true,
  })
}

// A code for the next window this spec has not used: a code accepted once is refused in the same window.
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
    await switchToTenant(page, TENANT)
    await expect(homeHeading(page)).toBeVisible()
  })
})
