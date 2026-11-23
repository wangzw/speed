/**
 * The shared steps of the enterprise sign-in journeys.
 *
 * The OIDC onboarding journey and the SAML sign-in journey share one
 * precondition and several surfaces: the operator proves the account's own
 * address, opens the admin's SSO page, selects the tenant, registers a provider
 * and publishes the domain's record through the DNS stub, and the employee walks
 * the enterprise entry on the sign-in surface. Each step is driven through the
 * rendered product, and the copy it expects is read from the shipped en-US
 * bundles. The only non-UI reads are the two the journeys already name: the
 * operator's verification code from the process log, and the DNS stub's
 * `setTXT`, the operator's own act at the domain's DNS.
 */
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

/** The seeded platform administrator the e2e seed writes. */
export const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
} as const

/** The organization the employee already belongs to (the template's seed). */
export const EXISTING_TENANT = 'acme'

/** The tenant the provider is registered in (the enterprise fixtures' seed). */
export const TARGET_TENANT = 'globex'

/** The platform's own organization. The account menu offers the admin entry
 * only while this is the current organization; platform administration is not
 * a tenant member's surface. */
export const PLATFORM_TENANT = 'system'

/** The product's sign-in surface heading. */
export function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

/** The tenant area's home heading, the rendered proof of a landed session. */
export function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

/** The standing notice that asks the signed-in account to prove its address. */
export function verificationBanner(page: Page): Locator {
  // The notice's accessible name is its region title, a colon and the same
  // sentence the paragraph renders.
  return page.getByRole('region', {
    name: `${copy('demo', 'verificationBanner.region')}: ${copy(
      'demo',
      'verificationBanner.pending',
      { email: OPERATOR.identifier },
    )}`,
    exact: true,
  })
}

/** The profile pane's email card, which carries the verification surface. */
export function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.email'),
    exact: true,
  })
}

/** The admin's navigation landmark. */
export function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

/** One admin navigation link by its page title. */
export function adminLink(page: Page, label: string): Locator {
  return adminNav(page).getByRole('link', { name: label, exact: true })
}

/** The tenant selector of a tenant-addressed admin page. */
export function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'common.tenant'),
    exact: true,
  })
}

/** The SSO page's provider write section. */
export function providerSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.provider.title'),
    exact: true,
  })
}

/** The SSO page's domain section. */
export function domainSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.domains.title'),
    exact: true,
  })
}

/** The SSO page's provider list row for one registered provider. */
export function providerRow(page: Page, name: string): Locator {
  return page
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: name })
}

/**
 * Open the admin from the tenant area. The way in is the account menu's admin
 * entry, offered only while the platform's own organization is current, so
 * the journey moves there first.
 */
export async function openConsole(page: Page): Promise<void> {
  await switchToTenant(page, PLATFORM_TENANT)
  await openAccountMenu(page)
  await expect(adminEntry(page)).toBeVisible()
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
}

/** Leave the admin for the tenant area. The way back is the admin account
 * menu's destination entry. */
export async function leaveConsole(page: Page): Promise<void> {
  await openAccountMenu(page)
  await backToApp(page).click()
  await expect(homeHeading(page)).toBeVisible()
}

/** Select one organization through a tenant-addressed page's selector. */
export async function selectTenant(page: Page, tenant: string): Promise<void> {
  await chooseSelectOption(
    tenantSelector(page),
    page.getByRole('option', { name: tenant, exact: true }),
  )
}

/** The sign-in surface's work-address entry. */
export function workEmailField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'enterprise.emailLabel'),
    exact: true,
  })
}

/**
 * Walk the enterprise entry on the sign-in surface: enter the work address,
 * continue, and choose the provider the domain discovery named. The caller
 * approves at the provider.
 */
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

/**
 * The shared precondition of the onboarding journeys: sign the operator in
 * and prove the account's own address, which the admin's human writes are
 * gated on.
 */
export async function proveOperatorAddress({
  page,
  server,
  accounts,
}: DemoTarget): Promise<void> {
  // The journey starts with an operator whose address is proved. The e2e
  // seed writes the address as an administrator's assertion, so the spec
  // reaches the precondition the way a person does -- the standing notice's
  // verify entry, then the code the e2e mail carrier writes to the process
  // log.
  await accounts.signIn({
    identifier: OPERATOR.username,
    password: OPERATOR.password,
  })
  const banner = verificationBanner(page)
  await expect(banner).toBeVisible()

  // The cursor is taken before the entry is followed: the verification
  // section sends its one code after the arrival, and only that code may be
  // used.
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

/**
 * Claim the fixture's email domain for one provider, publish the record the
 * one-time panel hands out through the DNS stub, and verify it.
 */
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

  // The one-time panel carries the DNS token and, beside it, the record name
  // and full record value the operator publishes -- each with its own copy
  // control. The panel is the page's own state, rendered outside the write
  // scope.
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

  // The token is the monospace value the panel hands out: one hex run of the
  // module's own token length, on a line of its own.
  const value = panel.getByText(/^[0-9a-f]{64}$/)
  await expect(value).toBeVisible()
  const token = ((await value.textContent()) ?? '').trim()
  expect(token, 'the claim panel rendered no verification token').not.toBe('')

  // The record name and value are handed over complete, so the operator
  // publishes without deriving either.
  await expect(
    panel.getByText(enterpriseIdentityProviderRecordName, {
      exact: true,
    }),
  ).toBeVisible()
  const recordValue = panel.getByText(/^speed-authn=[0-9a-f]{64}$/)
  await expect(recordValue).toBeVisible()
  const publishedValue = ((await recordValue.textContent()) ?? '').trim()

  // FIXTURE ACTION: the operator's own act at the domain's DNS is to publish
  // the record value the panel shows. The stub stands in for that DNS zone and
  // answers only this record name; the spec writes the record through the
  // stub's handle.
  stub.setTXT([publishedValue])

  // The declared domain is listed for the provider, named in the row and
  // marked unverified until its record is checked; that row is where the
  // verification action lives.
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

  // The write names the domain and the row flips in place: verified, no
  // longer unverified, and its verification action is gone.
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
  // Reopening the verified row's menu proves the verification action is gone;
  // removal, the only remaining write, is still offered there.
  await domainActions.click()
  await expect(
    page.getByRole('menuitem', {
      name: copy('adminUi', 'sso.domains.remove'),
      exact: true,
    }),
  ).toBeVisible()
  await expect(verifyItem).toHaveCount(0)
  await page.keyboard.press('Escape')

  // Closing the one-time panel drops the only copy of the token and the record
  // it handed over.
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

/** What one provider registration writes into the admin's provider form. */
export interface ProviderRegistration {
  readonly protocol: 'oidc' | 'saml'
  readonly name: string
  readonly provisioning: 'preprovisioned' | 'jit'
  /**
   * A just-in-time provider must state how a first login determines its
   * username. `claim` selects "From claim" and the assertion claim the
   * account's username is read from; omitted, the form keeps its default,
   * which is the before-opening confirmation.
   */
  readonly claim?: 'preferred_username' | 'email'
  /** The protocol's configuration, written as the form's JSON. */
  readonly config: Readonly<Record<string, unknown>>
  /** The write-only client secret, for a protocol that has one. */
  readonly clientSecret?: string
}

/**
 * Register one enabled provider through the admin's provider form. The
 * provider belongs to the tenant the page is switched to; the write confirms
 * it by name, the form returns to its initial state and the list carries the
 * provider under that tenant and under no other.
 */
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
    // The strategy field belongs to a just-in-time provider, and the claim
    // field appears only once "From claim" is chosen.
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

  // The enabled flag is a three-state field whose untouched value leaves the
  // module's default; the journey's provider is enabled, so the operator
  // states it.
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

  // The provider configuration is the protocol's own JSON, and the client
  // secret is write-only and belongs to the protocols that have one.
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

  // The write confirms the created provider by name, the form returns to its
  // initial state, and the list carries the provider under this tenant.
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

  // REQ-platform-config-017: the provider belongs to the selected tenant.
  // Another tenant's list does not carry it, and the target tenant's does
  // again after switching back.
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
