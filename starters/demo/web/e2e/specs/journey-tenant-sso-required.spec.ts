/**
 * J-tenant-sso-required -- a tenant administrator whose session was built from
 * a local first factor re-authenticates through the tenant's enterprise
 * provider before the tenant accepts an invitation write.
 *
 * The journey's five steps, all through the rendered product:
 *
 *   1. The tenant administrator opens the invitations-and-members page and
 *      submits an invitation.
 *   2. The tenant admission layer refuses the write and the page names the
 *      enterprise sign-in: the sentence comes from the product's own
 *      `authn.sso_required` copy and a re-login action sits under it. No local
 *      step-up dialog opens and no local factor is listed, because no local
 *      factor can satisfy the tenant's requirement.
 *   3. The administrator takes the re-login action; the admin's session gate
 *      returns to the sign-in surface.
 *   4. The administrator signs in through the tenant's enterprise entry: the
 *      work address, the organization the verified domain discovers, and the
 *      provider's approval. The callback lands on the tenant home.
 *   5. Back on the invitations page, the same invitation is submitted again and
 *      the page confirms it exactly as a direct submit does; the one-time panel
 *      hands over the acceptance link and the invitation list carries the
 *      pending row.
 *
 * Two things are non-UI, and only these:
 *
 *   - The platform operator's own e-mail address, and the administrator's, are
 *     proved through the review mail carrier's verification code, which
 *     fixtures/log.ts reads from the process log (the sanctioned
 *     verification-code read). Both admin writes and the invitation write are
 *     gated on a proved address, so the journey starts from that state.
 *   - The DNS publication is a FIXTURE ACTION. The product renders the record
 *     value to publish; the spec copies it into the running DNS stub with the
 *     stub's own `setTXT`, standing in for the operator's act at the domain's
 *     DNS, exactly as the enterprise onboarding journey does.
 *
 * The journey's precondition -- the tenant has an enabled, pre-provisioned
 * enterprise provider with a verified e-mail domain -- is reached through the
 * rendered admin the way the operator reaches it: the platform operator
 * registers the provider and claims and verifies the domain. The provider and
 * invitation routes are tenant-resolved on the API endpoint, and the admission
 * layer judges a request in the organization the request surface announces, not
 * the one a admin page's own selector names. A platform administrator's
 * tenant area settles the roster's staff default, so the operator's setup writes
 * are judged there while the page addresses the tenant the provider belongs to;
 * the journey then moves the tenant area to that tenant.
 *
 * The requirement itself is switched on through the admin's settings surface
 * after the administrator's session and admin area are established, rather
 * than seeded before the server starts. The admin's own permission read is a
 * request the admission layer judges, so a requirement already on would refuse
 * that read and keep the admin area from being entered at all; switching it
 * on while the page is open is also the operational sequence in which a
 * signed-in member first meets the requirement. The setting's global row is the
 * value the tenant falls back to, and the settings page writes that layer.
 *
 * The administrator is also a platform administrator because this host's
 * admin is platform-staff only, and the invitations-and-members page lives in
 * the admin. The tenant administrator binding -- not the platform role -- is
 * what the invitation writes exercise: the tenant's own permission domain
 * decides them.
 *
 * `@covers` is narrower than the journey document where an assertion cannot
 * fail on the requirement's whole claim. The spec drives the refusal and the
 * re-login for REQ-security-038, the positive pre-provisioned enterprise match
 * for REQ-signin-021, the verified-domain discovery for REQ-signin-020, the
 * issue and its one-time acceptance link for REQ-tenancy-011, the invitation
 * list for REQ-tenancy-015 and the member list for REQ-tenancy-008. The
 * document's acceptance-path, JIT, merge and domain-mismatch branches are not
 * driven here and are not claimed.
 */
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
import type { ReviewServer } from '../fixtures/server'
import { expectAccountMenuName } from '../helpers/account-menu'
import { copy } from '../helpers/i18n'
import { approveEnterpriseSignIn } from '../helpers/social'
import { expectClippedAnnouncement } from '../helpers/tenant-switch'

/** The seeded platform administrator the review assembly writes. */
const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
} as const

/** The tenant the provider is registered in and the requirement is on. */
const TENANT = tenantSSOTenant

/** The provider the operator registers for the tenant. */
const PROVIDER_NAME = 'Globex Workforce'

/** The colleague the administrator invites. */
const INVITEE_EMAIL = 'colleague@demo.test'

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

/** The account page's email-verification section. */
function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
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

/** Select one organization through a tenant-addressed page's selector. */
async function selectTenant(page: Page, tenant: string): Promise<void> {
  await tenantSelector(page).click()
  await page.getByRole('option', { name: tenant, exact: true }).click()
}

/**
 * The organization the tenant area announces for a platform administrator
 * before the journey switches it: the roster's staff default, the first
 * active business tenant. The administrator the journey signs in as is
 * platform staff because this host's admin is platform-staff only, so the
 * tenant area settles this organization rather than one of their own
 * memberships.
 */
const STAFF_DEFAULT_TENANT = 'acme'

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
 * Move the tenant area to one organization.
 *
 * The requests the admin's tenant-addressed pages issue carry the tenant
 * area's current organization on the request surface, and the admission layer
 * decides the request in that organization's domain, not in the one the page's
 * own selector names. The journey therefore has to switch the tenant area
 * before an invitation write is judged in the tenant that requires SSO. The
 * trigger names the organization the host settled, which is the roster's staff
 * default until a switch; the menu's row is disabled when the organization is
 * already current, the only case this helper leaves the menu with Escape.
 */
async function switchToTenant(page: Page, tenant: string): Promise<void> {
  const trigger = tenantSwitcher(page, tenant).or(
    tenantSwitcher(page, STAFF_DEFAULT_TENANT),
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

/** Open the admin from the tenant area. */
async function openConsole(page: Page): Promise<void> {
  await expect(adminEntry(page)).toBeVisible()
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
}

/**
 * Switch the tenant's SSO requirement on through the admin's settings
 * surface.
 *
 * The setting's global row is the value the tenant falls back to, and the
 * settings page writes the global layer. The requirement is switched on here,
 * after the administrator's admin session is established, because the
 * admin's own permission read is a request the admission layer judges: with
 * the requirement already on, the read is refused and the admin area cannot
 * be entered at all. Switching it on while the page is open is also the
 * operational sequence in which a signed-in member first meets the requirement.
 */
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

/** The setting the tenant's SSO requirement lives in. */
const REQUIRE_SSO_KEY = 'tenant.admission.require-sso'

/** The SSO page's provider write section. */
function providerSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.provider.title'),
    exact: true,
  })
}

/** The SSO page's provider list row for the registered provider. */
function providerRow(page: Page): Locator {
  return page
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: PROVIDER_NAME })
}

/** The SSO page's domain section. */
function domainSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.domains.title'),
    exact: true,
  })
}

/** The invitations page's invitation section. */
function invitationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'invitations.section.title'),
    exact: true,
  })
}

/** The invitations page's member section. */
function memberSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'members.title'),
    exact: true,
  })
}

/**
 * Prove the signed-in account's own e-mail address through the rendered
 * notice, the review process log's code and the verification action. The
 * cursor is taken before the entry is followed, so only the code this arrival
 * sends may be used.
 */
async function proveEmail(
  page: Page,
  server: ReviewServer,
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

/** Leave the admin for the tenant area, where the sign-out menu lives. */
async function leaveConsole(page: Page): Promise<void> {
  await page
    .getByRole('banner')
    .getByRole('link', {
      name: copy('demo', 'admin.backToApp'),
      exact: true,
    })
    .click()
  await expect(homeHeading(page)).toBeVisible()
}

/** Open the admin's invitations-and-members page from the tenant area. */
async function openInvitations(page: Page): Promise<void> {
  await page
    .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
    .click()
  await expect(homeHeading(page)).toBeVisible()
  await expect(adminEntry(page)).toBeVisible()
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
  await adminLink(page, copy('adminUi', 'invitations.title')).click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('adminUi', 'invitations.title'),
      exact: true,
    }),
  ).toBeVisible()
}

/** Register the tenant's enabled, pre-provisioned OIDC provider. */
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

/** Claim the provider's e-mail domain, publish the record and verify it. */
async function claimAndVerifyDomain(page: Page, stub: DNSStub): Promise<void> {
  const domains = domainSection(page)
  await domains
    .getByRole('combobox', {
      name: copy('adminUi', 'sso.domains.providerLabel'),
      exact: true,
    })
    .click()
  await page.getByRole('option', { name: PROVIDER_NAME, exact: true }).click()
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

  // The one-time panel hands the record value over complete; publishing it is
  // the operator's act at the domain's DNS, performed through the stub.
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
      name: copy('adminUi', 'sso.domains.verifyAria', {
        domain: enterpriseIdentityProviderDomain,
      }),
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

// The admin's invitation table and the SSO page carry low-priority columns;
// the sidebar takes part of the width, so the viewport is wide enough for the
// container to render every column this journey reads.
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
    // The operator's own address must be proved before the admin's human
    // writes answer; it is the review seed's administrator assertion until
    // then.
    await accounts.signIn({
      identifier: OPERATOR.username,
      password: OPERATOR.password,
    })
    await proveEmail(page, server, OPERATOR.identifier)

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
    // The tenant frame's sign-out surface is what the account helper waits
    // for; the admin's own session gate would show the sign-in form instead.
    await leaveConsole(page)
    await accounts.signOut()

    await accounts.signIn({
      identifier: tenantSSOAdmin.username,
      password: tenantSSOAdmin.password,
    })
    await proveEmail(page, server, tenantSSOAdmin.identifier)

    // The tenant's requirement is judged on the organization the request
    // surface announces, and the tenant area settles the roster's staff
    // default for a platform administrator. The journey's administrator
    // exercises the tenant's own domain, so the tenant area moves to it
    // before any admin page is opened.
    await switchToTenant(page, TENANT)
  })

  await test.step('precondition: the tenant requirement is switched on after the administrator is signed in', async () => {
    // The operator and the administrator are one account in this host: the
    // admin that carries the invitations page is platform-staff only. The
    // requirement is switched on through that admin's settings surface once
    // the administrator's session and admin area are established, which is
    // the sequence in which a signed-in member first meets the requirement.
    await openConsole(page)
    await enableTenantSSORequirement(page)
  })

  await test.step('step 1: the tenant administrator submits an invitation', async () => {
    await adminLink(page, copy('adminUi', 'invitations.title')).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'invitations.title'),
        exact: true,
      }),
    ).toBeVisible()
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
  })

  await test.step('step 2: the page names the enterprise sign-in and offers no local factor', async () => {
    const section = invitationSection(page)
    // The refused write and the refused list read carry the same sentence;
    // the assertion is that the page renders it, not how many surfaces do.
    await expect(
      section
        .getByText(copy('adminUi', 'errors.authn.sso_required'), {
          exact: true,
        })
        .first(),
    ).toBeVisible()

    // The refusal's only exit is the re-login action; no local step-up
    // dialog opens and no local factor is listed, because none can satisfy
    // the tenant's requirement.
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
    // Both the refused list read and the refused write offer the same exit:
    // ending the session is the only way to a sign-in through the tenant's
    // provider. Either control is the documented action.
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
    // The enterprise entry asks for the work address, then lists the
    // organization the verified domain discovers, then leaves for the
    // provider's approval page.
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

    // The callback lands on the tenant home. The user menu names the display
    // name the seed wrote, which differs from the provider's `name` claim: a
    // just-in-time account opened from the claims would carry the claim.
    await expect(homeHeading(page)).toBeVisible({ timeout: 15_000 })
    const userMenu = page
      .getByTestId('account-entry-trigger')
    await expectAccountMenuName(userMenu, tenantSSOAdmin.displayName)
  })

  await test.step('step 5: the same invitation is accepted with the direct-submit confirmation', async () => {
    // The provider's callback settles the tenant area again; a platform
    // administrator starts from the roster's staff default, so the journey
    // moves it back to the tenant whose provider signed the session in before
    // the write is judged. The helper leaves an already-current organization
    // alone.
    await switchToTenant(page, TENANT)
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

    // The write is now accepted and confirms it with the page's own sentence,
    // the same one a direct submit renders.
    await expect(
      section.getByText(
        copy('adminUi', 'invitations.issued.confirm', {
          email: INVITEE_EMAIL,
        }),
        { exact: true },
      ),
    ).toBeVisible()

    // The invited address receives the invitation: the one-time panel hands
    // over its acceptance link, and the list carries the pending row.
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

    // The tenant administrator is still a member holding the tenant-admin
    // binding: the member list reads under the re-authenticated session and
    // names the seeded account.
    await expect(
      memberSection(page)
        .getByRole('table')
        .locator('tbody tr')
        .filter({ hasText: tenantSSOAdmin.displayName }),
    ).toHaveCount(1)
  })
})
