import type { Locator, Page } from '@playwright/test'

import { expect } from '../fixtures/demo'
import type { DemoTarget } from '../fixtures/demo'
import type { DNSStub } from '../fixtures/dns-stub'
import {
  enterpriseIdentityProviderDomain,
  enterpriseIdentityProviderRecordName,
  enterpriseIdentityProviderUser,
} from '../fixtures/identity-provider'
import { waitForVerificationCode } from '../fixtures/log'
import { adminEntry, backToApp, openAccountMenu } from './account-menu'
import { copy } from './i18n'
import { chooseSelectOption } from './select-option'
import { switchToTenant } from './switch-tenant'

export const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
} as const

export const EXISTING_TENANT = 'acme'

export const TARGET_TENANT = 'globex'

// The admin entry is offered only while the platform organization is current.
export const PLATFORM_TENANT = 'system'

export function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

export function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

export function verificationBanner(page: Page): Locator {
  // The notice's accessible name is its region title, a colon, and the same sentence the paragraph renders.
  return page.getByRole('region', {
    name: `${copy('demo', 'verificationBanner.region')}: ${copy(
      'demo',
      'verificationBanner.pending',
      { email: OPERATOR.identifier },
    )}`,
    exact: true,
  })
}

export function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.email'),
    exact: true,
  })
}

export function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

export function adminLink(page: Page, label: string): Locator {
  return adminNav(page).getByRole('link', { name: label, exact: true })
}

export function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'common.tenant'),
    exact: true,
  })
}

export function providerSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.provider.title'),
    exact: true,
  })
}

export function domainSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.domains.title'),
    exact: true,
  })
}

export function providerRow(page: Page, name: string): Locator {
  return page
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: name })
}

export async function openConsole(page: Page): Promise<void> {
  await switchToTenant(page, PLATFORM_TENANT)
  await openAccountMenu(page)
  await expect(adminEntry(page)).toBeVisible()
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
}

export async function leaveConsole(page: Page): Promise<void> {
  await openAccountMenu(page)
  await backToApp(page).click()
  await expect(homeHeading(page)).toBeVisible()
}

export async function selectTenant(page: Page, tenant: string): Promise<void> {
  await chooseSelectOption(
    tenantSelector(page),
    page.getByRole('option', { name: tenant, exact: true }),
  )
}

export function workEmailField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'enterprise.emailLabel'),
    exact: true,
  })
}

export async function startEnterpriseSignIn(
  page: Page,
  providerName: string,
  address: string = enterpriseIdentityProviderUser.email,
): Promise<void> {
  const email = workEmailField(page)
  await expect(email).toBeVisible()
  await email.fill(address)
  await page
    .getByRole('button', {
      name: copy('authUi', 'enterprise.continue'),
      exact: true,
    })
    .click()
  const provider = page.getByRole('button', {
    name: providerName,
    exact: true,
  })
  await expect(provider).toBeVisible()
  await provider.click()
}

export async function proveOperatorAddress({
  page,
  server,
  accounts,
}: DemoTarget): Promise<void> {
  await accounts.signIn({
    identifier: OPERATOR.username,
    password: OPERATOR.password,
  })
  const banner = verificationBanner(page)
  await expect(banner).toBeVisible()

  // Take the cursor before following the entry so only the code this arrival sends can be accepted.
  const beforeCode = server.log.mark()
  await banner
    .getByRole('link', {
      name: copy('demo', 'verificationBanner.verify'),
      exact: true,
    })
    .click()

  const section = verificationSection(page)
  await expect(section).toBeVisible()
  const { code } = await waitForVerificationCode(
    server.log,
    'mail',
    OPERATOR.identifier,
    { after: beforeCode },
  )
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
    section.getByText(
      copy('accountUi', 'verification.verifiedDescription', {
        email: OPERATOR.identifier,
      }),
      { exact: true },
    ),
  ).toBeVisible()
  await expect(banner).toHaveCount(0)
}

export async function claimAndVerifyDomain(
  page: Page,
  stub: DNSStub,
  providerName: string,
): Promise<void> {
  const domains = domainSection(page)
  await chooseSelectOption(
    domains.getByRole('combobox', {
      name: copy('adminUi', 'sso.domains.providerLabel'),
      exact: true,
    }),
    page.getByRole('option', { name: providerName, exact: true }),
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
        name: providerName,
      }),
      { exact: true },
    ),
  ).toBeVisible()

  const panel = page.getByRole('status').filter({
    hasText: copy('adminUi', 'sso.domains.claimToken'),
  })
  await expect(panel).toBeVisible()
  for (const detailLabel of [
    'sso.domains.claimRecordName',
    'sso.domains.claimRecordValue',
  ]) {
    await expect(
      panel.getByText(copy('adminUi', detailLabel), { exact: true }),
    ).toBeVisible()
  }
  for (const copyLabel of [
    'sso.domains.claimTokenCopy',
    'sso.domains.claimRecordNameCopy',
    'sso.domains.claimRecordValueCopy',
  ]) {
    await expect(
      panel.getByRole('button', {
        name: copy('adminUi', copyLabel),
        exact: true,
      }),
    ).toBeVisible()
  }

  // The token is the one hex run of the module's length, on its own line.
  const value = panel.getByText(/^[0-9a-f]{64}$/)
  await expect(value).toBeVisible()
  const token = ((await value.textContent()) ?? '').trim()
  expect(token, 'the claim panel rendered no verification token').not.toBe('')

  await expect(
    panel.getByText(enterpriseIdentityProviderRecordName, {
      exact: true,
    }),
  ).toBeVisible()
  const recordValue = panel.getByText(/^speed-authn=[0-9a-f]{64}$/)
  await expect(recordValue).toBeVisible()
  const publishedValue = ((await recordValue.textContent()) ?? '').trim()

  stub.setTXT([publishedValue])

  const domainRow = domains
    .getByRole('listitem')
    .filter({ hasText: enterpriseIdentityProviderDomain })
  await expect(domainRow).toHaveCount(1)
  await expect(
    domainRow.getByText(enterpriseIdentityProviderDomain, { exact: true }),
  ).toBeVisible()
  await expect(
    domainRow.getByText(copy('adminUi', 'sso.domains.state.unverified'), {
      exact: true,
    }),
  ).toBeVisible()
  const domainActions = domainRow.getByRole('button', {
    name: copy('adminUi', 'sso.domains.actionsAria', {
      domain: enterpriseIdentityProviderDomain,
    }),
    exact: true,
  })
  await expect(domainActions).toBeVisible()
  await domainActions.click()
  const verifyItem = page.getByRole('menuitem', {
    name: copy('adminUi', 'sso.domains.verify'),
    exact: true,
  })
  await expect(verifyItem).toBeVisible()
  await verifyItem.click()

  await expect(
    page.getByText(
      copy('adminUi', 'sso.domains.verified', {
        domain: enterpriseIdentityProviderDomain,
        name: providerName,
      }),
      { exact: true },
    ),
  ).toBeVisible()
  await expect(
    domainRow.getByText(copy('adminUi', 'sso.domains.state.verified'), {
      exact: true,
    }),
  ).toBeVisible()
  await expect(
    domainRow.getByText(copy('adminUi', 'sso.domains.state.unverified'), {
      exact: true,
    }),
  ).toHaveCount(0)
  await domainActions.click()
  await expect(
    page.getByRole('menuitem', {
      name: copy('adminUi', 'sso.domains.remove'),
      exact: true,
    }),
  ).toBeVisible()
  await expect(verifyItem).toHaveCount(0)
  await page.keyboard.press('Escape')

  await panel
    .getByRole('button', {
      name: copy('adminUi', 'sso.domains.claimTokenCloseAria', {
        tenant: TARGET_TENANT,
        name: providerName,
        domain: enterpriseIdentityProviderDomain,
      }),
      exact: true,
    })
    .click()
  await expect(panel).toHaveCount(0)
}

export interface ProviderRegistration {
  readonly protocol: 'oidc' | 'saml'
  readonly name: string
  readonly provisioning: 'preprovisioned' | 'jit'
  readonly claim?: 'preferred_username' | 'email'
  readonly config: Readonly<Record<string, unknown>>
  readonly clientSecret?: string
}

export async function registerProvider(
  page: Page,
  registration: ProviderRegistration,
): Promise<void> {
  const { name, provisioning, claim } = registration
  const form = providerSection(page)
  const protocolField = form.getByLabel(
    copy('adminUi', 'sso.provider.protocolLabel'),
    { exact: true },
  )
  const nameField = form.getByLabel(
    copy('adminUi', 'sso.provider.nameLabel'),
    { exact: true },
  )
  const provisioningField = form.getByLabel(
    copy('adminUi', 'sso.provider.provisioningLabel'),
    { exact: true },
  )
  const create = form.getByRole('button', {
    name: copy('adminUi', 'sso.provider.createSubmit'),
    exact: true,
  })

  await protocolField.fill(registration.protocol)
  await nameField.fill(name)
  await provisioningField.fill(provisioning)

  if (claim !== undefined) {
    await chooseSelectOption(
      form.getByRole('combobox', {
        name: copy('adminUi', 'sso.provider.usernameStrategyLabel'),
        exact: true,
      }),
      page.getByRole('option', {
        name: copy('adminUi', 'sso.provider.usernameStrategy.claim'),
        exact: true,
      }),
    )
    await chooseSelectOption(
      form.getByRole('combobox', {
        name: copy('adminUi', 'sso.provider.usernameClaimLabel'),
        exact: true,
      }),
      page.getByRole('option', {
        name:
          claim === 'email'
            ? copy('adminUi', 'sso.provider.usernameClaim.email')
            : copy('adminUi', 'sso.provider.usernameClaim.preferredUsername'),
        exact: true,
      }),
    )
  }

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
    .getByLabel(copy('adminUi', 'sso.provider.configLabel'), {
      exact: true,
    })
    .fill(JSON.stringify(registration.config))
  if (registration.clientSecret !== undefined) {
    await form
      .getByLabel(copy('adminUi', 'sso.provider.clientSecretLabel'), {
        exact: true,
      })
      .fill(registration.clientSecret)
  }
  await create.click()

  await expect(
    page.getByText(copy('adminUi', 'sso.provider.created', { name }), {
      exact: true,
    }),
  ).toBeVisible()
  await expect(nameField).toHaveValue('')

  const row = providerRow(page, name)
  await expect(row).toHaveCount(1)
  await expect(row.getByText(name, { exact: true })).toBeVisible()
  await expect(
    row.getByText(registration.protocol, { exact: true }),
  ).toBeVisible()
  await expect(
    row.getByText(copy('adminUi', 'sso.enabledYes'), { exact: true }),
  ).toBeVisible()

  await selectTenant(page, EXISTING_TENANT)
  await expect(
    page.getByRole('heading', {
      name: copy('adminUi', 'sso.empty.title'),
      exact: true,
    }),
  ).toBeVisible()
  await expect(providerRow(page, name)).toHaveCount(0)
  await selectTenant(page, TARGET_TENANT)
  await expect(providerRow(page, name)).toHaveCount(1)
}
