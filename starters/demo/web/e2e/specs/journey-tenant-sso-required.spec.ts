// @journey journey-tenant-sso-required.md
// @covers REQ-security-038, REQ-signin-020, REQ-signin-021, REQ-tenancy-008, REQ-tenancy-011, REQ-tenancy-015
import type { Locator, Page } from '@playwright/test'

import { tenantSSOAdmin, tenantSSOTenant } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import type { DNSStub } from '../fixtures/dns-stub'
import {
  enterpriseIdentityProviderClaims,
  enterpriseIdentityProviderClient,
  enterpriseIdentityProviderDomain,
  type EnterpriseIdentityProvider,
} from '../fixtures/identity-provider'
import { waitForVerificationCode } from '../fixtures/log'
import { expectAccountMenuName } from '../helpers/account-menu'
import type { E2EServer } from '../fixtures/server'
import {
  adminEntry,
  backToApp,
  openAccountMenu,
} from '../helpers/account-menu'
import { copy } from '../helpers/i18n'
import { chooseSelectOption } from '../helpers/select-option'
import { approveEnterpriseSignIn } from '../helpers/social'
import { switchToTenant } from '../helpers/switch-tenant'
import { expectClippedAnnouncement } from '../helpers/tenant-switch'

const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
} as const

const TENANT = tenantSSOTenant

const PROVIDER_NAME = 'Globex Workforce'

const INVITEE_EMAIL = 'colleague@demo.test'

function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
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

function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
    exact: true,
  })
}

function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

function adminLink(page: Page, label: string): Locator {
  return adminNav(page).getByRole('link', { name: label, exact: true })
}

function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'common.tenant'),
    exact: true,
  })
}

async function selectTenant(page: Page, tenant: string): Promise<void> {
  await chooseSelectOption(
    tenantSelector(page),
    page.getByRole('option', { name: tenant, exact: true }),
  )
}

const PLATFORM_TENANT = 'system'

const BUSINESS_TENANT = 'acme'

async function openConsole(page: Page): Promise<void> {
  await switchToTenant(page, PLATFORM_TENANT)
  await openAccountMenu(page)
  await expect(adminEntry(page)).toBeVisible()
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
}

async function enableTenantSSORequirement(page: Page): Promise<void> {
  await adminLink(page, copy('adminUi', 'settings.title')).click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('adminUi', 'settings.title'),
      exact: true,
    }),
  ).toBeVisible()

  const namespaceNav = page.getByRole('navigation', {
    name: copy('adminUi', 'settings.nav.label'),
    exact: true,
  })
  await namespaceNav
    .getByRole('button', { name: 'tenant.admission', exact: true })
    .click()

  const field = page.getByRole('textbox', {
    name: REQUIRE_SSO_KEY,
    exact: true,
  })
  await expect(field).toBeVisible()
  await field.fill('true')
  await page
    .getByRole('button', {
      name: copy('adminUi', 'settings.saveBar.save'),
      exact: true,
    })
    .click()
  await expect(
    page.getByText(copy('adminUi', 'settings.saved'), { exact: true }),
  ).toBeVisible()
}

const REQUIRE_SSO_KEY = 'tenant.admission.require-sso'

function providerSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.provider.title'),
    exact: true,
  })
}

function providerRow(page: Page): Locator {
  return page
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: PROVIDER_NAME })
}

function domainSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.domains.title'),
    exact: true,
  })
}

function invitationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'invitations.section.title'),
    exact: true,
  })
}

function memberSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'members.title'),
    exact: true,
  })
}

async function proveEmail(
  page: Page,
  server: E2EServer,
  email: string,
): Promise<void> {
  const banner = verificationBanner(page, email)
  await expect(banner).toBeVisible()
  const beforeCode = server.log.mark()
  await banner
    .getByRole('link', {
      name: copy('demo', 'verificationBanner.verify'),
      exact: true,
    })
    .click()

  const section = verificationSection(page)
  await expect(section).toBeVisible()
  const { code } = await waitForVerificationCode(server.log, 'mail', email, {
    after: beforeCode,
  })
  await section
    .getByLabel(copy('accountUi', 'verification.codeLabel'), { exact: true })
    .fill(code)
  await section
    .getByRole('button', {
      name: copy('accountUi', 'verification.verify'),
      exact: true,
    })
    .click()
  await expect(
    page.getByRole('region', {
      name: copy('accountUi', 'verification.verifiedTitle'),
      exact: true,
    }),
  ).toBeVisible()
  await expect(banner).toHaveCount(0)
}

async function leaveConsole(page: Page): Promise<void> {
  await openAccountMenu(page)
  await backToApp(page).click()
  await expect(homeHeading(page)).toBeVisible()
}

async function openInvitations(page: Page): Promise<void> {
  await page
    .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
    .click()
  await expect(homeHeading(page)).toBeVisible()
  await openConsole(page)
  await adminLink(page, copy('adminUi', 'invitations.title')).click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('adminUi', 'invitations.title'),
      exact: true,
    }),
  ).toBeVisible()
}

async function registerProvider(
  page: Page,
  provider: EnterpriseIdentityProvider,
): Promise<void> {
  const form = providerSection(page)
  await form
    .getByLabel(copy('adminUi', 'sso.provider.protocolLabel'), {
      exact: true,
    })
    .fill('oidc')
  await form
    .getByLabel(copy('adminUi', 'sso.provider.nameLabel'), { exact: true })
    .fill(PROVIDER_NAME)
  await form
    .getByLabel(copy('adminUi', 'sso.provider.provisioningLabel'), {
      exact: true,
    })
    .fill('preprovisioned')

  await chooseSelectOption(
    form.getByRole('combobox', {
      name: copy('adminUi', 'sso.provider.enabledLabel'),
      exact: true,
    }),
    page.getByRole('option', {
      name: copy('adminUi', 'sso.provider.tri.true'),
      exact: true,
    }),
  )

  await form
    .getByLabel(copy('adminUi', 'sso.provider.configLabel'), { exact: true })
    .fill(
      JSON.stringify({
        issuer: provider.issuer,
        client_id: provider.clientID,
        claims: enterpriseIdentityProviderClaims,
      }),
    )
  await form
    .getByLabel(copy('adminUi', 'sso.provider.clientSecretLabel'), {
      exact: true,
    })
    .fill(enterpriseIdentityProviderClient.secret)
  await form
    .getByRole('button', {
      name: copy('adminUi', 'sso.provider.createSubmit'),
      exact: true,
    })
    .click()

  await expect(
    page.getByText(
      copy('adminUi', 'sso.provider.created', { name: PROVIDER_NAME }),
      { exact: true },
    ),
  ).toBeVisible()
  await expect(providerRow(page)).toHaveCount(1)
}

async function claimAndVerifyDomain(page: Page, stub: DNSStub): Promise<void> {
  const domains = domainSection(page)
  await chooseSelectOption(
    domains.getByRole('combobox', {
      name: copy('adminUi', 'sso.domains.providerLabel'),
      exact: true,
    }),
    page.getByRole('option', { name: PROVIDER_NAME, exact: true }),
  )
  await domains
    .getByLabel(copy('adminUi', 'sso.domains.domainLabel'), { exact: true })
    .fill(enterpriseIdentityProviderDomain)
  await domains
    .getByRole('button', {
      name: copy('adminUi', 'sso.domains.claim'),
      exact: true,
    })
    .click()

  await expect(
    page.getByText(
      copy('adminUi', 'sso.domains.claimed', {
        domain: enterpriseIdentityProviderDomain,
        name: PROVIDER_NAME,
      }),
      { exact: true },
    ),
  ).toBeVisible()

  const panel = page.getByRole('status').filter({
    hasText: copy('adminUi', 'sso.domains.claimToken'),
  })
  await expect(panel).toBeVisible()
  const recordValue = panel.getByText(/^speed-authn=[0-9a-f]{64}$/)
  await expect(recordValue).toBeVisible()
  const publishedValue = ((await recordValue.textContent()) ?? '').trim()
  stub.setTXT([publishedValue])

  const domainRow = domains
    .getByRole('listitem')
    .filter({ hasText: enterpriseIdentityProviderDomain })
  await expect(domainRow).toHaveCount(1)
  await domainRow
    .getByRole('button', {
      name: copy('adminUi', 'sso.domains.actionsAria', {
        domain: enterpriseIdentityProviderDomain,
      }),
      exact: true,
    })
    .click()
  await page
    .getByRole('menuitem', {
      name: copy('adminUi', 'sso.domains.verify'),
      exact: true,
    })
    .click()
  await expect(
    page.getByText(
      copy('adminUi', 'sso.domains.verified', {
        domain: enterpriseIdentityProviderDomain,
        name: PROVIDER_NAME,
      }),
      { exact: true },
    ),
  ).toBeVisible()
  await expect(
    domainRow.getByText(copy('adminUi', 'sso.domains.state.verified'), {
      exact: true,
    }),
  ).toBeVisible()

  await panel
    .getByRole('button', {
      name: copy('adminUi', 'sso.domains.claimTokenCloseAria', {
        tenant: TENANT,
        name: PROVIDER_NAME,
        domain: enterpriseIdentityProviderDomain,
      }),
      exact: true,
    })
    .click()
  await expect(panel).toHaveCount(0)
}

// The invitation table and the SSO page render their low-priority columns only at desktop widths, and the sidebar takes part of the width; keep the viewport wide enough for every column this spec reads.
test.use({ tenantSSO: true, viewport: { width: 1920, height: 1080 } })

test("tenant-sso-required: a member re-invites a colleague through the tenant's enterprise SSO after the tenant demands it", async ({
  demo,
}) => {
  const { page, server, accounts } = demo
  const provider = server.enterpriseIdentityProvider
  if (provider === undefined) {
    throw new Error(
      'the enterprise fixture did not start the identity provider this spec drives',
    )
  }
  const stub = server.dnsStub
  if (stub === undefined) {
    throw new Error(
      'the enterprise fixture did not start the DNS stub this spec publishes into',
    )
  }

  await test.step('precondition: the operator registers the tenant provider and verifies its e-mail domain', async () => {
    await accounts.signIn({
      identifier: OPERATOR.username,
      password: OPERATOR.password,
    })
    await proveEmail(page, server, OPERATOR.identifier)

    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(homeHeading(page)).toBeVisible()
    await openConsole(page)
    await adminLink(page, copy('adminUi', 'sso.title')).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'sso.title'),
        exact: true,
      }),
    ).toBeVisible()

    await selectTenant(page, TENANT)
    await expect(
      page.getByRole('heading', {
        name: copy('adminUi', 'sso.empty.title'),
        exact: true,
      }),
    ).toBeVisible()

    await registerProvider(page, provider)
    await claimAndVerifyDomain(page, stub)
  })

  await test.step('precondition: the tenant administrator signs in with the local first factor and proves the work address', async () => {
    await leaveConsole(page)
    await accounts.signOut()

    await accounts.signIn({
      identifier: tenantSSOAdmin.username,
      password: tenantSSOAdmin.password,
    })
    await proveEmail(page, server, tenantSSOAdmin.identifier)

  })

  await test.step('precondition: the tenant requirement is switched on while the administrator session predates it', async () => {
    await openConsole(page)
    await enableTenantSSORequirement(page)
  })

  await test.step('step 1: the administrator ends the exempt session and signs in again with the local first factor', async () => {
    await leaveConsole(page)
    await accounts.signOut()
    await accounts.signIn({
      identifier: tenantSSOAdmin.username,
      password: tenantSSOAdmin.password,
    })
    await expect(homeHeading(page)).toBeVisible()
  })

  await test.step('step 2: the invitations surface is refused and the page names the enterprise sign-in', async () => {
    await openInvitations(page)
    await selectTenant(page, TENANT)

    const section = invitationSection(page)
    await expect(
      section
        .getByText(copy('adminUi', 'errors.authn.sso_required'), {
          exact: true,
        })
        .first(),
    ).toBeVisible()

    await expect(
      section
        .getByRole('button', {
          name: copy('adminUi', 'stepUp.relogin'),
          exact: true,
        })
        .first(),
    ).toBeVisible()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(
      page.getByRole('button', {
        name: copy('adminUi', 'stepUp.submit'),
        exact: true,
      }),
    ).toHaveCount(0)
    for (const factor of [
      'stepUp.factorKind.totp',
      'stepUp.factorKind.webauthn',
      'stepUp.factorKind.sms',
    ]) {
      await expect(page.getByText(copy('adminUi', factor), { exact: true }))
        .toHaveCount(0)
    }
  })

  await test.step('step 3: the re-login action ends the session and the sign-in surface appears', async () => {
    await invitationSection(page)
      .getByRole('button', {
        name: copy('adminUi', 'stepUp.relogin'),
        exact: true,
      })
      .first()
      .click()
    await expect(signInHeading(page)).toBeVisible()
  })

  await test.step('step 4: the administrator signs in through the tenant provider and keeps the account', async () => {
    await page
      .getByRole('textbox', {
        name: copy('authUi', 'enterprise.emailLabel'),
        exact: true,
      })
      .fill(tenantSSOAdmin.identifier)
    await page
      .getByRole('button', {
        name: copy('authUi', 'enterprise.continue'),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(copy('authUi', 'enterprise.providersTitle'), {
        exact: true,
      }),
    ).toBeVisible()
    await page
      .getByRole('button', { name: PROVIDER_NAME, exact: true })
      .click()
    await approveEnterpriseSignIn(page)

    await expect(homeHeading(page)).toBeVisible()
    const userMenu = page
      .getByTestId('account-entry-trigger')
    await expectAccountMenuName(userMenu, tenantSSOAdmin.displayName)
  })

  await test.step('step 5: the invitation is accepted with the direct-submit confirmation', async () => {
    await openInvitations(page)
    await selectTenant(page, TENANT)

    const section = invitationSection(page)
    await section
      .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
        exact: true,
      })
      .fill(INVITEE_EMAIL)
    await section
      .getByRole('button', {
        name: copy('adminUi', 'invitations.issue.submit'),
        exact: true,
      })
      .click()

    await expect(
      section.getByText(
        copy('adminUi', 'invitations.issued.confirm', {
          email: INVITEE_EMAIL,
        }),
        { exact: true },
      ),
    ).toBeVisible()

    await expect(
      page.getByRole('status').filter({
        hasText: copy('adminUi', 'invitations.issued.link', {
          email: INVITEE_EMAIL,
        }),
      }),
    ).toBeVisible()
    const row = section
      .getByRole('table')
      .locator('tbody tr')
      .filter({ hasText: INVITEE_EMAIL })
    await expect(row).toHaveCount(1)
    await expect(
      row.getByText(copy('adminUi', 'invitations.status.pending'), {
        exact: true,
      }),
    ).toBeVisible()

    await expect(
      memberSection(page)
        .getByRole('table')
        .locator('tbody tr')
        .filter({ hasText: tenantSSOAdmin.displayName }),
    ).toHaveCount(1)
  })
})

test('tenant-sso-required: the account menu switches from a business organization to the platform one', async ({
  demo,
}) => {
  const { page, accounts } = demo
  await accounts.signIn({
    identifier: OPERATOR.username,
    password: OPERATOR.password,
  })
  await page
    .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
    .click()
  await expect(homeHeading(page)).toBeVisible()

  await switchToTenant(page, BUSINESS_TENANT)

  const menu = await openAccountMenu(page)
  await menu.locator('[role="menuitem"][aria-expanded]').click()
  await expect(
    menu.getByRole('group', {
      name: copy('demo', 'userMenu.organizations'),
      exact: true,
    }),
  ).toBeVisible()

  await switchToTenant(page, PLATFORM_TENANT)
  await expectClippedAnnouncement(
    page,
    copy('tenancyUi', 'tenantSwitcher.switchedTo', { tenant: PLATFORM_TENANT }),
  )
})
