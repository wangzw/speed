// @journey journey-deploy-and-operate.md
// @covers REQ-deployment-002, REQ-deployment-003, REQ-deployment-004, REQ-deployment-005, REQ-deployment-006, REQ-deployment-007, REQ-deployment-008, REQ-deployment-012, REQ-deployment-017, REQ-deployment-018, REQ-deployment-019, REQ-signin-001, REQ-signin-006, REQ-signin-030, REQ-signup-027, REQ-platform-admin-001, REQ-platform-admin-006, REQ-platform-admin-007, REQ-platform-admin-017
import type { Locator, Page } from '@playwright/test'

import { deployOperator } from '../fixtures/config'
import { expect, test } from '../fixtures/deploy'
import {
  expandAccountRows,
  selectAccountSection,
} from '../helpers/account-sections'
import {
  adminEntry,
  backToApp,
  openAccountMenu,
} from '../helpers/account-menu'
import { copy } from '../helpers/i18n'

// The settings table renders its type column only from `lg`; keep the viewport wide enough for the item the refusal step names.
test.use({ viewport: { width: 1920, height: 1080 } })

const OPERATOR = deployOperator

const ENTRY_DOCUMENT_MARKER = '<div id="root"></div>'

const SETTING_KEY = 'ratelimit.limit'
const SETTING_TYPE = 'int'

const NEW_VALUE = '150'

const UNRUN_MODULE_PATH = '/api/v1/system/sms/templates'
const UNRUN_MODULE_NAME = 'sms.admin'

interface DiscoveryMethod {
  readonly name: string
  readonly kind: string
}

const DISCOVERY_METHODS = [
  { name: 'github', kind: 'redirect' },
  { name: 'google', kind: 'redirect' },
  { name: 'passkey', kind: 'webauthn' },
  { name: 'password', kind: 'form' },
  { name: 'sms', kind: 'form' },
] as const

function profileHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'account.sections.profile'),
    exact: true,
  })
}

function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.email'),
    exact: true,
  })
}

function verificationBanner(page: Page, email: string): Locator {
  return page.getByRole('region', {
    name: `${copy('demo', 'verificationBanner.region')}: ${copy(
      'demo',
      'verificationBanner.pending',
      { email },
    )}`,
    exact: true,
  })
}

function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

function adminCard(page: Page): Locator {
  return page
    .getByRole('heading', {
      level: 2,
      name: copy('demo', 'home.cards.admin.title'),
      exact: true,
    })
    .locator('xpath=ancestor::a[1]')
}

function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

function namespaceNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'settings.nav.label'),
    exact: true,
  })
}

function settingsTable(page: Page): Locator {
  return page.getByRole('table')
}

function settingRow(page: Page): Locator {
  return settingsTable(page)
    .getByRole('row')
    .filter({
      has: page.getByRole('textbox', { name: SETTING_KEY, exact: true }),
    })
}

function valueField(page: Page): Locator {
  return page.getByRole('textbox', { name: SETTING_KEY, exact: true })
}

function kpiValue(page: Page, label: string): Locator {
  return page
    .getByRole('main')
    .getByText(label, { exact: true })
    .locator('xpath=following-sibling::p[1]')
}

function jsonBody(answer: { readonly body: string }): Record<string, unknown> {
  try {
    return JSON.parse(answer.body) as Record<string, unknown>
  } catch (error) {
    throw new Error(
      `the answer was not JSON (${String(error)}); body:\n${answer.body}`,
    )
  }
}

test('deploy-and-operate: a deployment serves, seeds, and refuses an unproved operator write', async ({
  deploy,
}) => {
  const { page, server } = deploy

  await test.step('steps 3-4: the deploy assembly seeds a fresh volume and mints the signing key', async () => {
    expect(server.seedOutput).toContain(
      `seeded: account ${OPERATOR.identifier}`,
    )
    expect(server.seedOutput).toContain('seeded: tenant system')
    expect(server.seedOutput).not.toContain('seeded: tenant acme')
    expect(server.seedOutput).toContain('seeded: role system/platform-admin')

    expect(server.signingKeyMode).toBe(0o600)
  })

  await test.step('step 5: one origin serves the entry document, its page fallback and 404 for named assets', async () => {
    const root = await server.probe('/')
    expect(root.status).toBe(200)
    expect(root.body).toContain(ENTRY_DOCUMENT_MARKER)

    const unknownPage = await server.probe('/deploy/unknown/page')
    expect(unknownPage.status).toBe(200)
    expect(unknownPage.body).toBe(root.body)

    const unknownAsset = await server.probe('/assets/not-a-built-asset.js')
    expect(unknownAsset.status).toBe(404)
    expect(unknownAsset.body).not.toBe(root.body)

    const methods = await server.probe('/api/v1/auth/methods')
    expect(methods.status).toBe(200)
    expect(methods.contentType).toContain('application/json')

    const discovery = jsonBody(methods)
    const entries = (discovery.methods as readonly DiscoveryMethod[]).map(
      ({ name, kind }) => ({ name, kind }),
    )
    expect(entries).toEqual(DISCOVERY_METHODS)
  })

  await test.step('step 6: the protected account path meets an anonymous viewer with the sign-in surface', async () => {
    await page.goto(`${server.url}/account`)
    await expect(signInHeading(page)).toBeVisible()
    expect(new URL(page.url()).pathname).toBe('/account')

    await expect(
      page.getByRole('textbox', {
        name: copy('authUi', 'passwordSignIn.identifierLabel'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByRole('textbox', {
        name: copy('authUi', 'passwordSignIn.passwordLabel'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByRole('button', {
        name: copy('authUi', 'passwordSignIn.submit'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByRole('button', {
        name: copy('authUi', 'social.provider.google'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByRole('button', {
        name: copy('authUi', 'social.provider.github'),
        exact: true,
      }),
    ).toBeVisible()

    await expect(adminNav(page)).toHaveCount(0)
  })

  await test.step('step 7: the seeded operator signs in, the path resumes, and the admin entries appear', async () => {
    const identifier = page.getByRole('textbox', {
      name: copy('authUi', 'passwordSignIn.identifierLabel'),
      exact: true,
    })
    await identifier.fill(OPERATOR.username)
    await page
      .getByRole('textbox', {
        name: copy('authUi', 'passwordSignIn.passwordLabel'),
        exact: true,
      })
      .fill(OPERATOR.password)
    await page
      .getByRole('button', {
        name: copy('authUi', 'passwordSignIn.submit'),
        exact: true,
      })
      .click()
    await identifier.waitFor({ state: 'detached' })

    expect(new URL(page.url()).pathname).toBe('/account/profile')
    await expect(signInHeading(page)).toHaveCount(0)
    await expandAccountRows(page)
    await expect(profileHeading(page)).toBeVisible()

    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()
    const card = adminCard(page)
    await expect(card).toBeVisible()
    const cardHref = await card.getAttribute('href')
    expect(cardHref).not.toBeNull()

    await openAccountMenu(page)
    const entry = adminEntry(page)
    await expect(entry).toBeVisible()
    const entryHref = await entry.getAttribute('href')
    expect(entryHref).not.toBeNull()
    expect(entryHref).toBe(cardHref)
  })

  await test.step('step 8: the account page shows the profile and identifiers, and an unproved write is refused', async () => {
    await page.goto(`${server.url}/account`)
    await expandAccountRows(page)
    await expect(profileHeading(page)).toBeVisible()
    await expect(
      page.getByLabel(copy('accountUi', 'profile.displayName.label'), {
        exact: true,
      }),
    ).toHaveValue(OPERATOR.displayName)

    const emailCard = verificationSection(page)
    await expect(
      emailCard.getByText(OPERATOR.identifier, { exact: true }),
    ).toBeVisible()
    await expect(
      emailCard.getByText(copy('accountUi', 'identifiers.unverified'), {
        exact: true,
      }),
    ).toBeVisible()

    const banner = verificationBanner(page, OPERATOR.identifier)
    await expect(banner).toBeVisible()
    await expect(
      banner.getByRole('link', {
        name: copy('demo', 'verificationBanner.verify'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      banner.getByRole('link', {
        name: copy('demo', 'verificationBanner.change'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(banner.getByRole('button')).toHaveCount(0)

    await selectAccountSection(page, 'profile')
    await expandAccountRows(page)
    const section = verificationSection(page)
    await expect(section).toBeVisible()
    await expect(
      section.getByRole('button', {
        name: copy('accountUi', 'verification.send'),
        exact: true,
      }),
    ).toBeEnabled()
    await expect(
      section
        .getByRole('status')
        .filter({
          hasText: copy('accountUi', 'verification.sent', {
            email: OPERATOR.identifier,
          }),
        }),
    ).toHaveCount(0)

    await openAccountMenu(page)
    await adminEntry(page).click()
    await expect(adminNav(page)).toBeVisible()
    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'settings.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'settings.title'),
        exact: true,
      }),
    ).toBeVisible()
    await namespaceNav(page)
      .getByRole('button', { name: 'ratelimit', exact: true })
      .click()
    await expect(valueField(page)).toBeVisible()

    const row = settingRow(page)
    await expect(row.getByText(SETTING_TYPE, { exact: true })).toBeVisible()
    await expect(
      row.getByText(copy('adminUi', 'settings.state.default'), {
        exact: true,
      }),
    ).toBeVisible()

    await valueField(page).fill(NEW_VALUE)
    await page
      .getByRole('button', {
        name: copy('adminUi', 'settings.saveBar.save'),
        exact: true,
      })
      .click()

    const refusal = copy('adminUi', 'errors.authn.contact_required')
    await expect(page.getByRole('alert').filter({ hasText: refusal }))
      .toBeVisible()
    await expect(valueField(page)).toHaveAccessibleDescription(refusal)
    await expect(valueField(page)).toHaveAttribute('aria-invalid', 'true')
    await expect(valueField(page)).toHaveValue(NEW_VALUE)
    await expect(
      row.getByText(copy('adminUi', 'settings.state.default'), {
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('step 9: the admin shows its grouped navigation and the roster-derived overview', async () => {
    await openAccountMenu(page)
    await backToApp(page).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()
    await openAccountMenu(page)
    await adminEntry(page).click()

    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'dashboard.title'),
        exact: true,
      }),
    ).toBeVisible()

    const nav = adminNav(page)
    await expect(
      nav.getByRole('button', {
        name: copy('adminUi', 'nav.groups.identity'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      nav.getByRole('link', {
        name: copy('adminUi', 'tenants.title'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      nav.getByRole('link', { name: copy('adminUi', 'sso.title'), exact: true }),
    ).toHaveCount(0)

    await expect(
      kpiValue(page, copy('adminUi', 'dashboard.kpi.tenants')),
    ).toHaveText('2')
    await expect(
      kpiValue(page, copy('adminUi', 'dashboard.kpi.active')),
    ).toHaveText('2')
    await expect(
      kpiValue(page, copy('adminUi', 'dashboard.kpi.disabled')),
    ).toHaveText('0')
    await expect(
      kpiValue(page, copy('adminUi', 'dashboard.kpi.deleted')),
    ).toHaveText('0')
  })

  await test.step('step 10: the operations listener answers the liveness and readiness probes', async () => {
    const healthOne = await server.probeOps('/health')
    expect(healthOne.status).toBe(200)
    expect(jsonBody(healthOne)).toMatchObject({ status: 'ok' })
    const healthTwo = await server.probeOps('/health')
    expect(healthTwo.status).toBe(200)
    expect(jsonBody(healthTwo)).toMatchObject({ status: 'ok' })

    const ready = await server.probeOps('/ready')
    expect(ready.status).toBe(200)
    expect(jsonBody(ready)).toMatchObject({ status: 'ready' })
  })

  await test.step('step 11: the merged document covers an operation no command runs', async () => {
    const answer = await server.probeOps('/openapi.json')
    expect(answer.status).toBe(200)
    const document = jsonBody(answer)
    const paths = document.paths as Record<
      string,
      Record<string, { tags?: string[] }>
    >
    const operation = paths[UNRUN_MODULE_PATH]?.get
    expect(operation).toBeDefined()
    expect(operation?.tags ?? []).toContain(UNRUN_MODULE_NAME)
  })
})
