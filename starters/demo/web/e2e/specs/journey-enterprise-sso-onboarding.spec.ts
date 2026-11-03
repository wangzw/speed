/**
 * J-enterprise-sso -- an operator connects an enterprise OIDC provider to a
 * tenant and an employee signs in with their work address.
 *
 * The file drives the journey twice, once per provisioning mode the provider
 * can declare. The first test is the pre-provisioned journey below. The second
 * has the provider open accounts itself: the first login parks at the username
 * step before any account exists, the answer opens the account with that
 * username, and the account joins the provider's tenant. Everything after the
 * sign-in surface -- the domain claim, the discovery by address, the switcher
 * -- is common to both.
 *
 * The pre-provisioned journey's seven steps, all through the rendered product:
 *
 *   1. The operator opens the admin's SSO page and selects the target tenant.
 *   2. The operator registers a provider: protocol OIDC, a name, pre-provisioned
 *      provisioning, enabled, the provider's issuer/client/claims configuration
 *      and its client secret. The list shows the provider under that tenant, the
 *      write confirms it by name, and the form resets.
 *   3. The operator claims the enterprise email domain. The page hands out a
 *      one-time DNS token plus the record name and full record value to
 *      publish, each with a copy control. The claimed domain is listed as
 *      unverified, turns verified after its record is published, and then
 *      takes part in discovery.
 *   4. The operator creates the employee's account in the admin (a display
 *      name, the username the account is created with, and the work address as
 *      its login identifier, asserted by the administrator) and adds it to the
 *      target tenant as a member.
 *   5. The employee opens the sign-in surface and uses the enterprise entry: the
 *      method listing carries the entry but no enterprise provider, and only the
 *      work address's discovery lists the organization.
 *   6. The employee approves at the provider and comes back signed in to the
 *      account the operator prepared -- the account's display name is the one the
 *      operator typed, not the provider's claim, so no second account was made.
 *   7. The tenant switcher names the current organization, that item is not
 *      selectable, and choosing the target tenant switches the interface to it.
 *
 * Two things are non-UI, and only these:
 *
 *   - The operator's own email address is proved through the review mail
 *     carrier's verification code, which fixtures/log.ts reads from the process
 *     log (the sanctioned verification-code read). The journey's precondition is
 *     an operator whose address is proved, and the admin's human writes are
 *     gated on it.
 *   - The DNS publication is a FIXTURE ACTION. The product renders the
 *     record name and the value to publish; the spec copies that value at
 *     `_speed-authn.<domain>` into the running DNS stub with the stub's own
 *     `setTXT`, standing in for the operator's act at the domain's DNS. The
 *     stub answers only that record name, so nothing else becomes reachable.
 *
 * No route is intercepted, no request is faked, and no API is called from the
 * spec: the provider is a real OpenID Connect server the fixtures start, and the
 * browser walks its authorization page with a click.
 *
 * The employee already belongs to the template's seeded organization (`acme`);
 * the target tenant is the second organization the enterprise fixtures seed
 * (`globex`). Both memberships are written through the admin's member page in
 * step 4. Without a second membership the switcher would hold only the target
 * tenant, and step 7's switch could not be driven at all.
 *
 * Expected product copy is read from the shipped en-US bundles through
 * helpers/i18n.ts. The provider's own authorization-page copy belongs to the
 * provider, so it is the fixture's constant and the helper asserts it.
 *
 * `@covers` is narrower than the journey document where an assertion cannot
 * fail on the requirement's whole claim: the spec drives each requirement's
 * positive branch only. It enables a provider that was never disabled (so it
 * cannot prove that a disabled provider leaves discovery), verifies a domain
 * that no other provider holds (so it cannot prove the conflict refusal), and
 * signs in once with a domain that is verified (so it cannot prove the refusal
 * a foreign domain or an unverified domain produces). Those alternative paths
 * stay with their own requirements and are not claimed here. The just-in-time
 * test likewise never meets an address an existing account holds, so the
 * link-required branch of REQ-signin-022 stays with its own case.
 */
// @journey journey-enterprise-sso-onboarding.md
// @covers REQ-platform-admin-013, REQ-platform-config-017, REQ-platform-config-018, REQ-platform-config-020, REQ-platform-config-038, REQ-presentation-027, REQ-signin-001, REQ-signin-003, REQ-signin-004, REQ-signin-020, REQ-signin-021, REQ-signin-022, REQ-signin-027, REQ-signup-018, REQ-signup-047, REQ-signup-048, REQ-tenancy-009, REQ-tenancy-022, REQ-tenancy-023, REQ-tenancy-035, REQ-tenancy-036
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import type { DemoTarget } from '../fixtures/demo'
import type { DNSStub } from '../fixtures/dns-stub'
import {
  enterpriseIdentityProviderClaims,
  enterpriseIdentityProviderClient,
  enterpriseIdentityProviderDomain,
  enterpriseIdentityProviderRecordName,
  enterpriseIdentityProviderUser,
} from '../fixtures/identity-provider'
import type { EnterpriseIdentityProvider } from '../fixtures/identity-provider'
import { waitForVerificationCode } from '../fixtures/log'
import { expectAccountMenuName } from '../helpers/account-menu'
import {
  expandAccountRows,
  selectAccountSection,
} from '../helpers/account-sections'
import { copy } from '../helpers/i18n'
import { approveEnterpriseSignIn } from '../helpers/social'
import { expectClippedAnnouncement } from '../helpers/tenant-switch'

/** The seeded platform administrator the review assembly writes. */
const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
} as const

/** The organization the employee already belongs to (the template's seed). */
const EXISTING_TENANT = 'acme'

/** The tenant the provider is registered in (the enterprise fixtures' seed). */
const TARGET_TENANT = 'globex'

/** The provider the operator registers for the pre-provisioned onboarding. */
const PROVIDER_NAME = 'Globex Workforce'

/** The provider the operator registers for the just-in-time onboarding. */
const JIT_PROVIDER_NAME = 'Globex Workforce Self-Service'

/** The username the employee chooses when a just-in-time login opens the
 * account. Nothing seeds it: the step collects it before any account exists. */
const JIT_USERNAME = 'globex-newcomer'

/**
 * The account's display name. It deliberately differs from the provider's
 * `name` claim (`Enterprise Employee`): the account page naming this value is
 * what proves the login took the account the operator prepared rather than
 * opening a new one from the provider's claims.
 */
const EMPLOYEE_DISPLAY_NAME = 'Globex Employee'

const EMPLOYEE_EMAIL = enterpriseIdentityProviderUser.email
/** The login name the operator gives the employee's pre-provisioned account. */
const EMPLOYEE_USERNAME = 'globex-employee'

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

/** The standing notice that asks the signed-in account to prove its address. */
function verificationBanner(page: Page): Locator {
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
function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.email'),
    exact: true,
  })
}

/** The header's admin entry (a link, while the operator may reach it). */
function adminEntry(page: Page): Locator {
  return page
    .getByRole('banner')
    .getByRole('link', { name: copy('demo', 'nav.admin'), exact: true })
}

/** The admin's navigation landmark. */
function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

/** One admin navigation link by its page title. */
function adminLink(page: Page, label: string): Locator {
  return adminNav(page).getByRole('link', { name: label, exact: true })
}

/** The tenant selector of a tenant-addressed admin page. */
function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'common.tenant'),
    exact: true,
  })
}

/** The SSO page's provider write section. */
function providerSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.provider.title'),
    exact: true,
  })
}

/** The SSO page's domain section. */
function domainSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.domains.title'),
    exact: true,
  })
}

/** The SSO page's provider list row for one registered provider. */
function providerRow(page: Page, name: string): Locator {
  return page
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: name })
}

/** The invitations page's member section. */
function memberSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'members.title'),
    exact: true,
  })
}

/** The tenant switcher's trigger, naming the current organization by alias. */
function tenantSwitcher(page: Page, alias: string): Locator {
  return page
    .getByRole('banner')
    .getByRole('button', { name: alias, exact: true })
}

/** The tenant switcher's row for one organization. */
function tenantMenuItem(page: Page, alias: string): Locator {
  return page.getByRole('menuitem', { name: alias, exact: true })
}

/**
 * Move the tenant area to one organization. The employee's account owns a
 * personal workspace, and the host settles that row as the current
 * organization for an ordinary member, so the trigger's current name is not
 * known up front. The menu's row is disabled when the organization is already
 * current, which is the only case this helper leaves the menu without a click.
 */
async function switchToTenant(page: Page, tenant: string): Promise<void> {
  const trigger = page
    .getByRole('banner')
    .getByRole('button', { name: tenant, exact: true })
    .or(
      page
        .getByRole('banner')
        .getByRole('button', {
          name: copy('demo', 'workspace.personal'),
          exact: true,
        }),
    )
  await expect(trigger).toBeVisible()
  await trigger.click()
  const row = tenantMenuItem(page, tenant)
  await expect(row).toBeVisible()
  if (await row.isEnabled()) {
    await row.click()
    await expectClippedAnnouncement(
      page,
      copy('tenancyUi', 'tenantSwitcher.switchedTo', { tenant }),
    )
  } else {
    await page.keyboard.press('Escape')
  }
  await expect(tenantSwitcher(page, tenant)).toBeVisible()
}

/** Select one organization through a tenant-addressed page's selector. */
async function selectTenant(page: Page, tenant: string): Promise<void> {
  await tenantSelector(page).click()
  await page.getByRole('option', { name: tenant, exact: true }).click()
}

/** The account picker's option for the employee's account. */
function employeeOption(page: Page): Locator {
  return page.getByRole('option', {
    name: new RegExp(`^${EMPLOYEE_DISPLAY_NAME}`),
  })
}

/**
 * The shared precondition of both onboarding journeys: sign the operator in
 * and prove the account's own address, which the admin's human writes are
 * gated on.
 */
async function proveOperatorAddress({
  page,
  server,
  accounts,
}: DemoTarget): Promise<void> {
  // The journey starts with an operator whose address is proved. The review
  // seed writes the address as an administrator's assertion, so the spec
  // reaches the precondition the way a person does -- the standing notice's
  // verify entry, then the code the review mail carrier writes to the process
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
 * Register one enabled OIDC provider through the admin's provider form,
 * naming the local fixture's issuer, client and claim mapping. The provider
 * belongs to the tenant the page is switched to.
 */
async function registerOIDCProvider(
  page: Page,
  provider: EnterpriseIdentityProvider,
  name: string,
  provisioning: 'preprovisioned' | 'jit',
): Promise<void> {
  const form = providerSection(page)
  const protocol = form.getByLabel(
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

  await protocol.fill('oidc')
  await nameField.fill(name)
  await provisioningField.fill(provisioning)

  // The enabled flag is a three-state field whose untouched value leaves the
  // module's default; the journey's provider is enabled, so the operator
  // states it.
  await form
    .getByRole('combobox', {
      name: copy('adminUi', 'sso.provider.enabledLabel'),
      exact: true,
    })
    .click()
  await page
    .getByRole('option', {
      name: copy('adminUi', 'sso.provider.tri.true'),
      exact: true,
    })
    .click()

  // The provider configuration names the local issuer, its client identifier
  // and the claim mapping that finds the user's address and display name.
  await form
    .getByLabel(copy('adminUi', 'sso.provider.configLabel'), {
      exact: true,
    })
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
  await expect(row.getByText('oidc', { exact: true })).toBeVisible()
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

/**
 * Claim the fixture's email domain for one provider, publish the record the
 * one-time panel hands out through the DNS stub, and verify it.
 */
async function claimAndVerifyDomain(
  page: Page,
  stub: DNSStub,
  providerName: string,
): Promise<void> {
  const domains = domainSection(page)
  await domains
    .getByRole('combobox', {
      name: copy('adminUi', 'sso.domains.providerLabel'),
      exact: true,
    })
    .click()
  await page.getByRole('option', { name: providerName, exact: true }).click()
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
  const verifyButton = domainRow.getByRole('button', {
    name: copy('adminUi', 'sso.domains.verifyAria', {
      domain: enterpriseIdentityProviderDomain,
    }),
    exact: true,
  })
  await expect(verifyButton).toBeVisible()
  await verifyButton.click()

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
  await expect(verifyButton).toHaveCount(0)

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

// This journey drives the provider's real round trip and the domain's TXT
// lookup, so its server starts the enterprise provider, the DNS stub and the
// enterprise SSO seams; no other spec pays for them.
test.use({ enterpriseSSO: true })

test('enterprise-sso-onboarding: an operator connects an OIDC provider and an employee signs in with the work email', async ({
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

  await test.step("precondition: the operator signs in and proves the account's own address", () =>
    proveOperatorAddress(demo))

  await test.step('step 1: the operator opens the admin SSO page and selects the target tenant', async () => {
    // The account page the verification landed on is inside the tenant shell;
    // the start page is where the admin entry is read.
    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(homeHeading(page)).toBeVisible()

    await expect(adminEntry(page)).toBeVisible()
    await adminEntry(page).click()
    await expect(adminNav(page)).toBeVisible()
    await adminLink(page, copy('adminUi', 'sso.title')).click()

    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'sso.title'),
        exact: true,
      }),
    ).toBeVisible()

    // No tenant is chosen yet, so the page prompts for one and renders no
    // provider list.
    await expect(
      page.getByRole('heading', {
        name: copy('adminUi', 'sso.needTenant.title'),
        exact: true,
      }),
    ).toBeVisible()

    await selectTenant(page, TARGET_TENANT)
    await expect(
      page.getByRole('heading', {
        name: copy('adminUi', 'sso.empty.title'),
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('step 2: the operator registers an enabled, pre-provisioned OIDC provider', () =>
    registerOIDCProvider(page, provider, PROVIDER_NAME, 'preprovisioned'))

  await test.step('step 3: the operator claims the email domain, publishes its token, and verifies it', () =>
    claimAndVerifyDomain(page, stub, PROVIDER_NAME))

  await test.step('step 4: the operator creates the employee account and adds it to the tenant', async () => {
    await adminLink(page, copy('adminUi', 'accounts.title')).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'accounts.title'),
        exact: true,
      }),
    ).toBeVisible()

    await page
      .getByRole('button', {
        name: copy('adminUi', 'accounts.create.title'),
        exact: true,
      })
      .click()
    const dialog = page.getByRole('dialog', {
      name: copy('adminUi', 'accounts.create.title'),
      exact: true,
    })
    await expect(dialog).toBeVisible()

    const submit = dialog.getByRole('button', {
      name: copy('adminUi', 'accounts.create.submit'),
      exact: true,
    })
    // REQ-platform-admin-013: a user account needs a display name and a
    // username before it can be created; the work address is its passed
    // identifier, not a substitute for either.
    await dialog
      .getByLabel(copy('adminUi', 'accounts.create.displayNameLabel'), {
        exact: true,
      })
      .fill(EMPLOYEE_DISPLAY_NAME)
    await expect(submit).toBeDisabled()

    await dialog
      .getByLabel(copy('adminUi', 'accounts.create.usernameLabel'), {
        exact: true,
      })
      .fill(EMPLOYEE_USERNAME)
    await expect(submit).toBeEnabled()

    await dialog
      .getByLabel(copy('adminUi', 'accounts.create.emailLabel'), {
        exact: true,
      })
      .fill(EMPLOYEE_EMAIL)
    // The administrator's assertion is the verification channel a
    // pre-provisioned enterprise login needs on the address; it is not proof
    // of ownership.
    await dialog
      .getByRole('combobox', {
        name: copy('adminUi', 'accounts.create.emailViaLabel'),
        exact: true,
      })
      .click()
    await page
      .getByRole('option', {
        name: copy('adminUi', 'accounts.create.via.admin'),
        exact: true,
      })
      .click()
    await expect(submit).toBeEnabled()
    await submit.click()

    await expect(dialog).toHaveCount(0)
    const accountRow = page
      .getByRole('table')
      .locator('tbody tr')
      .filter({ hasText: EMPLOYEE_DISPLAY_NAME })
    await expect(accountRow).toHaveCount(1)
    // The row's secondary line is the account's first identifier, which is the
    // username the create named; the address remains on record beside it.
    await expect(
      accountRow.getByText(EMPLOYEE_USERNAME, { exact: true }),
    ).toBeVisible()

    const writeMembership = async (tenant: string): Promise<void> => {
      await adminLink(page, copy('adminUi', 'invitations.title')).click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'invitations.title'),
          exact: true,
        }),
      ).toBeVisible()
      await selectTenant(page, tenant)

      const members = memberSection(page)
      const account = members.getByRole('combobox', {
        name: copy('adminUi', 'members.write.accountLabel'),
        exact: true,
      })
      await account.fill(EMPLOYEE_EMAIL)
      await expect(employeeOption(page)).toBeVisible()
      await employeeOption(page).click()
      await members
        .getByRole('button', {
          name: copy('adminUi', 'members.write.submit'),
          exact: true,
        })
        .click()

      const row = members
        .getByRole('table')
        .locator('tbody tr')
        .filter({ hasText: EMPLOYEE_DISPLAY_NAME })
      await expect(row).toHaveCount(1)
      await expect(
        row.getByText(copy('adminUi', 'members.activeYes'), {
          exact: true,
        }),
      ).toBeVisible()
    }

    // The journey's step: the employee becomes a member of the tenant the
    // provider serves.
    await writeMembership(TARGET_TENANT)

    // The precondition step 7 needs: the employee already belongs to the
    // template's organization, so the switcher has a current organization to
    // move away from.
    await writeMembership(EXISTING_TENANT)
  })

  await test.step('step 5: the employee uses the enterprise entry and sees the organization for the address', async () => {
    // The operator leaves the admin for the tenant area first: signing out
    // from the admin lands on the plain sign-in surface, while the tenant
    // frame's session-ended surface is what the account helper waits for.
    await page
      .getByRole('banner')
      .getByRole('link', {
        name: copy('demo', 'admin.backToApp'),
        exact: true,
      })
      .click()
    await expect(homeHeading(page)).toBeVisible()
    await accounts.signOut()
    await accounts.openSignIn()
    await expect(signInHeading(page)).toBeVisible()

    // REQ-signin-003: the method listing carries the enterprise entry but no
    // enterprise provider. The provider appears only after the address's
    // domain is discovered.
    const email = page.getByRole('textbox', {
      name: copy('authUi', 'enterprise.emailLabel'),
      exact: true,
    })
    await expect(email).toBeVisible()
    await expect(
      page.getByRole('button', { name: PROVIDER_NAME, exact: true }),
    ).toHaveCount(0)

    await email.fill(EMPLOYEE_EMAIL)
    await page
      .getByRole('button', {
        name: copy('authUi', 'enterprise.continue'),
        exact: true,
      })
      .click()

    // The domain discovery names the organization the verified domain maps to,
    // and only that.
    await expect(
      page.getByText(copy('authUi', 'enterprise.providersTitle'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByRole('button', { name: PROVIDER_NAME, exact: true }),
    ).toBeVisible()
  })

  await test.step('step 6: the employee approves at the provider and lands in the prepared account', async () => {
    await page
      .getByRole('button', { name: PROVIDER_NAME, exact: true })
      .click()
    await approveEnterpriseSignIn(page)

    // The callback establishes the session and returns the employee to the
    // initiating page, the tenant home.
    await expect(homeHeading(page)).toBeVisible({ timeout: 15_000 })

    // The account is the one the operator prepared: the user menu names the
    // display name the operator typed, which differs from the provider's name
    // claim. A just-in-time account would carry the provider's claim instead.
    const userMenu = page.getByTestId('account-entry-trigger')
    await expectAccountMenuName(userMenu, EMPLOYEE_DISPLAY_NAME)

    await userMenu.click()
    await page
      .getByRole('menuitem', {
        name: copy('demo', 'account.sections.profile'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'account.sections.profile'),
        exact: true,
      }),
    ).toBeVisible()

    // The work address is on that account (the email card's contact row,
    // still unproved) and the profile carries the operator's display name.
    await expandAccountRows(page)
    const emailCard = page.getByRole('region', {
      name: copy('demo', 'account.rows.label.email'),
      exact: true,
    })
    await expect(
      emailCard.getByText(EMPLOYEE_EMAIL, { exact: true }),
    ).toBeVisible()
    await expect(
      emailCard.getByText(copy('accountUi', 'identifiers.unverified'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByLabel(copy('accountUi', 'profile.displayName.label'), {
        exact: true,
      }),
    ).toHaveValue(EMPLOYEE_DISPLAY_NAME)

    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(homeHeading(page)).toBeVisible()
  })

  await test.step('step 7: the employee switches from the current organization into the tenant', async () => {
    // The journey's start state is the seeded organization the employee
    // already belongs to. An ordinary member's account also owns a personal
    // workspace, which the host settles first; move to the seeded organization
    // through the same control this step drives so the switch below starts
    // from it.
    await switchToTenant(page, EXISTING_TENANT)

    // REQ-tenancy-022/035: the trigger names the current organization, and the
    // list carries every organization the employee belongs to.
    const switcher = tenantSwitcher(page, EXISTING_TENANT)
    await expect(switcher).toBeVisible()
    await expect(switcher).toBeEnabled()
    await switcher.click()

    // REQ-tenancy-036: the current organization's own row cannot be selected.
    await expect(tenantMenuItem(page, EXISTING_TENANT)).toBeDisabled()
    await expect(tenantMenuItem(page, TARGET_TENANT)).toBeEnabled()

    // REQ-tenancy-023: choosing another organization moves the interface to it.
    await tenantMenuItem(page, TARGET_TENANT).click()
    await expect(tenantSwitcher(page, TARGET_TENANT)).toBeVisible()
    await expectClippedAnnouncement(
      page,
      copy('tenancyUi', 'tenantSwitcher.switchedTo', {
        tenant: TARGET_TENANT,
      }),
    )
  })
})

/**
 * The journey's second onboarding mode: the provider opens accounts itself.
 *
 * The employee has no prepared account, so the first login cannot match one.
 * The flow parks at the username step before any account exists, the answer
 * opens the account with that username, and the account joins the tenant the
 * provider serves. Nothing seeds the account or its membership; the admin
 * writes only the provider and its domain, and the sign-in is the only thing
 * that creates the account.
 */
test('enterprise-sso-onboarding: a just-in-time provider asks for a username before it opens an account', async ({
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

  await test.step("precondition: the operator signs in and proves the account's own address", () =>
    proveOperatorAddress(demo))

  await test.step('step 1: the operator opens the admin SSO page and selects the target tenant', async () => {
    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(homeHeading(page)).toBeVisible()

    await expect(adminEntry(page)).toBeVisible()
    await adminEntry(page).click()
    await expect(adminNav(page)).toBeVisible()
    await adminLink(page, copy('adminUi', 'sso.title')).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'sso.title'),
        exact: true,
      }),
    ).toBeVisible()

    await selectTenant(page, TARGET_TENANT)
    await expect(
      page.getByRole('heading', {
        name: copy('adminUi', 'sso.empty.title'),
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('step 2: the operator registers an enabled, just-in-time OIDC provider', () =>
    registerOIDCProvider(page, provider, JIT_PROVIDER_NAME, 'jit'))

  await test.step('step 3: the operator claims the email domain, publishes its token, and verifies it', () =>
    claimAndVerifyDomain(page, stub, JIT_PROVIDER_NAME))

  await test.step('step 4: the employee signs in with the work email and is asked for a username', async () => {
    // The operator leaves the admin for the tenant area first: signing out
    // from the admin lands on the plain sign-in surface.
    await page
      .getByRole('banner')
      .getByRole('link', {
        name: copy('demo', 'admin.backToApp'),
        exact: true,
      })
      .click()
    await expect(homeHeading(page)).toBeVisible()
    await accounts.signOut()
    await accounts.openSignIn()
    await expect(signInHeading(page)).toBeVisible()

    // REQ-signin-003: the provider appears only after the address's domain is
    // discovered.
    const email = page.getByRole('textbox', {
      name: copy('authUi', 'enterprise.emailLabel'),
      exact: true,
    })
    await expect(email).toBeVisible()
    await expect(
      page.getByRole('button', { name: JIT_PROVIDER_NAME, exact: true }),
    ).toHaveCount(0)

    await email.fill(enterpriseIdentityProviderUser.email)
    await page
      .getByRole('button', {
        name: copy('authUi', 'enterprise.continue'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('button', { name: JIT_PROVIDER_NAME, exact: true }),
    ).toBeVisible()

    await page
      .getByRole('button', { name: JIT_PROVIDER_NAME, exact: true })
      .click()
    await approveEnterpriseSignIn(page)

    // The callback parks the first login at the username step: the account is
    // opened by the answer, not before it. The input carries the product's
    // own label and the rule the username must satisfy.
    const username = page.getByRole('textbox', {
      name: copy('authUi', 'signInFlow.usernameLabel'),
      exact: true,
    })
    await expect(username).toBeVisible({ timeout: 15_000 })
    await expect(
      page.getByText(copy('authUi', 'signInFlow.usernameHelper'), {
        exact: true,
      }),
    ).toBeVisible()

    await username.fill(JIT_USERNAME)
    await page
      .getByRole('button', {
        name: copy('authUi', 'signInFlow.submit'),
        exact: true,
      })
      .click()
  })

  await test.step('step 5: the account opens with the chosen username and the provider display name', async () => {
    await expect(homeHeading(page)).toBeVisible({ timeout: 15_000 })

    // The account carries the provider's display name, because no
    // administrator prepared one. The operator typed nothing for this
    // employee.
    const userMenu = page.getByTestId('account-entry-trigger')
    await expectAccountMenuName(
      userMenu,
      enterpriseIdentityProviderUser.name,
    )

    await userMenu.click()
    await page
      .getByRole('menuitem', {
        name: copy('demo', 'account.sections.profile'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'account.sections.profile'),
        exact: true,
      }),
    ).toBeVisible()

    // The username answered at the step is the account's login name. The
    // provider's address is not written onto a just-in-time account.
    await selectAccountSection(page, 'security')
    await expandAccountRows(page)
    const identifiers = page.getByRole('region', {
      name: copy('demo', 'account.rows.label.loginWays'),
      exact: true,
    })
    await expect(
      identifiers.getByText(
        copy('accountUi', 'identifiers.usage.loginAs', {
          kind: copy('accountUi', 'identifiers.kind.username'),
          value: JIT_USERNAME,
        }),
        { exact: true },
      ),
    ).toBeVisible()

    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(homeHeading(page)).toBeVisible()
  })

  await test.step('step 6: the account belongs to the tenant the provider serves', async () => {
    // The switcher lists only the organizations the account is an active
    // member of, so reaching the target tenant there is the membership the
    // just-in-time login wrote. The account owns a personal workspace as
    // well, which the host may settle first, so the helper moves from
    // whichever row is current.
    await switchToTenant(page, TARGET_TENANT)
    await expect(tenantSwitcher(page, TARGET_TENANT)).toBeVisible()
  })
})
