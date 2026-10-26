/**
 * J-enterprise-sso -- an operator connects an enterprise OIDC provider to a
 * tenant and an employee signs in with their work address.
 *
 * The journey's seven steps, all through the rendered product:
 *
 *   1. The operator opens the console's SSO page and selects the target tenant.
 *   2. The operator registers a provider: protocol OIDC, a name, pre-provisioned
 *      provisioning, enabled, the provider's issuer/client/claims configuration
 *      and its client secret. The list shows the provider under that tenant, the
 *      write confirms it by name, and the form resets.
 *   3. The operator claims the enterprise email domain. The page hands out a
 *      one-time DNS token with a copy control; the domain is verified after the
 *      token's record is published, and the domain then takes part in discovery.
 *   4. The operator creates the employee's account in the console (display name
 *      plus the work address as its login identifier, asserted by the
 *      administrator) and adds it to the target tenant as a member.
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
 *     an operator whose address is proved, and the console's human writes are
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
 * (`globex`). Both memberships are written through the console's member page in
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
 * that no other provider holds (so it cannot prove the conflict refusal), uses
 * pre-provisioned onboarding against an account the operator created (so it
 * cannot prove the just-in-time branch and never exercises "no prepared
 * account"), and signs in once with a domain that is verified (so it cannot
 * prove the refusal a foreign domain or an unverified domain produces). Those
 * alternative paths stay with their own requirements and are not claimed here.
 */
// @journey journey-enterprise-sso-onboarding.md
// @covers REQ-platform-admin-013, REQ-platform-config-017, REQ-platform-config-018, REQ-platform-config-020, REQ-platform-config-038, REQ-presentation-027, REQ-signin-001, REQ-signin-003, REQ-signin-004, REQ-signin-020, REQ-signin-021, REQ-signin-027, REQ-signup-018, REQ-tenancy-009, REQ-tenancy-022, REQ-tenancy-023, REQ-tenancy-035, REQ-tenancy-036
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import {
  enterpriseIdentityProviderClaims,
  enterpriseIdentityProviderClient,
  enterpriseIdentityProviderDomain,
  enterpriseIdentityProviderRecordName,
  enterpriseIdentityProviderUser,
} from '../fixtures/identity-provider'
import { waitForVerificationCode } from '../fixtures/log'
import { copy } from '../helpers/i18n'
import { approveEnterpriseSignIn } from '../helpers/social'

/** The seeded platform administrator the review assembly writes. */
const OPERATOR = {
  identifier: 'operator@demo.test',
  password: 'operator-dev-secret',
} as const

/** The organization the employee already belongs to (the template's seed). */
const EXISTING_TENANT = 'acme'

/** The tenant the provider is registered in (the enterprise fixtures' seed). */
const TARGET_TENANT = 'globex'

/** The provider the operator registers. */
const PROVIDER_NAME = 'Globex Workforce'

/**
 * The account's display name. It deliberately differs from the provider's
 * `name` claim (`Enterprise Employee`): the account page naming this value is
 * what proves the login took the account the operator prepared rather than
 * opening a new one from the provider's claims.
 */
const EMPLOYEE_DISPLAY_NAME = 'Globex Employee'

const EMPLOYEE_EMAIL = enterpriseIdentityProviderUser.email

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

/** The account page's email-verification section. */
function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
    exact: true,
  })
}

/** The header's console entry (a link, while the operator may reach it). */
function consoleEntry(page: Page): Locator {
  return page
    .getByRole('banner')
    .getByRole('link', { name: copy('demo', 'nav.console'), exact: true })
}

/** The console's navigation landmark. */
function consoleNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('consoleUi', 'shell.navLabel'),
    exact: true,
  })
}

/** One console navigation link by its page title. */
function consoleLink(page: Page, label: string): Locator {
  return consoleNav(page).getByRole('link', { name: label, exact: true })
}

/** The tenant selector of a tenant-addressed console page. */
function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('consoleUi', 'common.tenant'),
    exact: true,
  })
}

/** The SSO page's provider write section. */
function providerSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('consoleUi', 'sso.provider.title'),
    exact: true,
  })
}

/** The SSO page's domain section. */
function domainSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('consoleUi', 'sso.domains.title'),
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

/** The invitations page's member section. */
function memberSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('consoleUi', 'members.title'),
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

  await test.step("precondition: the operator signs in and proves the account's own address", async () => {
    // The journey starts with an operator whose address is proved: the
    // console's human writes are gated on it. The review seed writes the
    // address as an administrator's assertion, so the spec reaches the
    // precondition the way a person does -- the standing notice's verify
    // entry, then the code the review mail carrier writes to the process log.
    await accounts.signIn({
      identifier: OPERATOR.identifier,
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
      page.getByRole('region', {
        name: copy('accountUi', 'verification.verifiedTitle'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(banner).toHaveCount(0)
  })

  await test.step('step 1: the operator opens the console SSO page and selects the target tenant', async () => {
    // The account page the verification landed on is inside the tenant shell;
    // the start page is where the console entry is read.
    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(homeHeading(page)).toBeVisible()

    await expect(consoleEntry(page)).toBeVisible()
    await consoleEntry(page).click()
    await expect(consoleNav(page)).toBeVisible()
    await consoleLink(page, copy('consoleUi', 'sso.title')).click()

    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('consoleUi', 'sso.title'),
        exact: true,
      }),
    ).toBeVisible()

    // No tenant is chosen yet, so the page prompts for one and renders no
    // provider list.
    await expect(
      page.getByRole('heading', {
        name: copy('consoleUi', 'sso.needTenant.title'),
        exact: true,
      }),
    ).toBeVisible()

    await selectTenant(page, TARGET_TENANT)
    await expect(
      page.getByRole('heading', {
        name: copy('consoleUi', 'sso.empty.title'),
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('step 2: the operator registers an enabled, pre-provisioned OIDC provider', async () => {
    const form = providerSection(page)
    const protocol = form.getByLabel(
      copy('consoleUi', 'sso.provider.protocolLabel'),
      { exact: true },
    )
    const name = form.getByLabel(copy('consoleUi', 'sso.provider.nameLabel'), {
      exact: true,
    })
    const provisioning = form.getByLabel(
      copy('consoleUi', 'sso.provider.provisioningLabel'),
      { exact: true },
    )
    const create = form.getByRole('button', {
      name: copy('consoleUi', 'sso.provider.createSubmit'),
      exact: true,
    })

    await protocol.fill('oidc')
    await name.fill(PROVIDER_NAME)
    await provisioning.fill('preprovisioned')

    // The enabled flag is a three-state field whose untouched value leaves the
    // module's default; the journey's provider is enabled, so the operator
    // states it.
    await form
      .getByRole('combobox', {
        name: copy('consoleUi', 'sso.provider.enabledLabel'),
        exact: true,
      })
      .click()
    await page
      .getByRole('option', {
        name: copy('consoleUi', 'sso.provider.tri.true'),
        exact: true,
      })
      .click()

    // The provider configuration names the local issuer, its client
    // identifier and the claim mapping that finds the user's address and
    // display name.
    await form
      .getByLabel(copy('consoleUi', 'sso.provider.configLabel'), {
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
      .getByLabel(copy('consoleUi', 'sso.provider.clientSecretLabel'), {
        exact: true,
      })
      .fill(enterpriseIdentityProviderClient.secret)
    await create.click()

    // The write confirms the created provider by name, the form returns to its
    // initial state, and the list carries the provider under this tenant.
    await expect(
      page.getByText(
        copy('consoleUi', 'sso.provider.created', { name: PROVIDER_NAME }),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(name).toHaveValue('')

    const row = providerRow(page)
    await expect(row).toHaveCount(1)
    await expect(
      row.getByText(PROVIDER_NAME, { exact: true }),
    ).toBeVisible()
    await expect(row.getByText('oidc', { exact: true })).toBeVisible()
    await expect(
      row.getByText(copy('consoleUi', 'sso.enabledYes'), { exact: true }),
    ).toBeVisible()

    // REQ-platform-config-017: the provider belongs to the selected tenant.
    // Another tenant's list does not carry it, and the target tenant's does
    // again after switching back.
    await selectTenant(page, EXISTING_TENANT)
    await expect(
      page.getByRole('heading', {
        name: copy('consoleUi', 'sso.empty.title'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(providerRow(page)).toHaveCount(0)
    await selectTenant(page, TARGET_TENANT)
    await expect(providerRow(page)).toHaveCount(1)
  })

  await test.step('step 3: the operator claims the email domain, publishes its token, and verifies it', async () => {
    const domains = domainSection(page)
    await domains
      .getByRole('combobox', {
        name: copy('consoleUi', 'sso.domains.providerLabel'),
        exact: true,
      })
      .click()
    await page
      .getByRole('option', { name: PROVIDER_NAME, exact: true })
      .click()
    await domains
      .getByLabel(copy('consoleUi', 'sso.domains.domainLabel'), { exact: true })
      .fill(enterpriseIdentityProviderDomain)
    await domains
      .getByRole('button', {
        name: copy('consoleUi', 'sso.domains.claim'),
        exact: true,
      })
      .click()

    await expect(
      page.getByText(
        copy('consoleUi', 'sso.domains.claimed', {
          domain: enterpriseIdentityProviderDomain,
          name: PROVIDER_NAME,
        }),
        { exact: true },
      ),
    ).toBeVisible()

    // The one-time panel carries the DNS token and, beside it, the record
    // name and full record value the operator publishes -- each with its own
    // copy control. The panel is the page's own state, rendered outside the
    // write scope.
    const panel = page.getByRole('status').filter({
      hasText: copy('consoleUi', 'sso.domains.claimToken'),
    })
    await expect(panel).toBeVisible()
    for (const copyLabel of [
      'sso.domains.claimTokenCopy',
      'sso.domains.claimRecordNameCopy',
      'sso.domains.claimRecordValueCopy',
    ]) {
      await expect(
        panel.getByRole('button', {
          name: copy('consoleUi', copyLabel),
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
    // the record value the panel shows. The stub stands in for that DNS zone
    // and answers only this record name; the spec writes the record through
    // the stub's handle.
    stub.setTXT([publishedValue])

    // The declared domain is listed for the provider; its row carries the
    // verification action and turns verified in place.
    const domainRow = domains
      .getByRole('listitem')
      .filter({ hasText: enterpriseIdentityProviderDomain })
    await expect(domainRow).toHaveCount(1)
    await domainRow
      .getByRole('button', {
        name: copy('consoleUi', 'sso.domains.verifyAria', {
          domain: enterpriseIdentityProviderDomain,
        }),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(
        copy('consoleUi', 'sso.domains.verified', {
          domain: enterpriseIdentityProviderDomain,
          name: PROVIDER_NAME,
        }),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(
      domainRow.getByText(copy('consoleUi', 'sso.domains.state.verified'), {
        exact: true,
      }),
    ).toBeVisible()

    // Closing the one-time panel drops the only copy of the token and the
    // record it handed over.
    await panel
      .getByRole('button', {
        name: copy('consoleUi', 'sso.domains.claimTokenCloseAria', {
          tenant: TARGET_TENANT,
          name: PROVIDER_NAME,
          domain: enterpriseIdentityProviderDomain,
        }),
        exact: true,
      })
      .click()
    await expect(panel).toHaveCount(0)
  })

  await test.step('step 4: the operator creates the employee account and adds it to the tenant', async () => {
    await consoleLink(page, copy('consoleUi', 'accounts.title')).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('consoleUi', 'accounts.title'),
        exact: true,
      }),
    ).toBeVisible()

    await page
      .getByRole('button', {
        name: copy('consoleUi', 'accounts.create.title'),
        exact: true,
      })
      .click()
    const dialog = page.getByRole('dialog', {
      name: copy('consoleUi', 'accounts.create.title'),
      exact: true,
    })
    await expect(dialog).toBeVisible()

    const submit = dialog.getByRole('button', {
      name: copy('consoleUi', 'accounts.create.submit'),
      exact: true,
    })
    // REQ-platform-admin-013: a user account needs a display name AND a login
    // identifier before it can be created.
    await dialog
      .getByLabel(copy('consoleUi', 'accounts.create.displayNameLabel'), {
        exact: true,
      })
      .fill(EMPLOYEE_DISPLAY_NAME)
    await expect(submit).toBeDisabled()

    await dialog
      .getByLabel(copy('consoleUi', 'accounts.create.emailLabel'), {
        exact: true,
      })
      .fill(EMPLOYEE_EMAIL)
    // The administrator's assertion is the verification channel a
    // pre-provisioned enterprise login needs on the address; it is not proof
    // of ownership.
    await dialog
      .getByRole('combobox', {
        name: copy('consoleUi', 'accounts.create.emailViaLabel'),
        exact: true,
      })
      .click()
    await page
      .getByRole('option', {
        name: copy('consoleUi', 'accounts.create.via.admin'),
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
    await expect(
      accountRow.getByText(EMPLOYEE_EMAIL, { exact: true }),
    ).toBeVisible()

    const writeMembership = async (tenant: string): Promise<void> => {
      await consoleLink(page, copy('consoleUi', 'invitations.title')).click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('consoleUi', 'invitations.title'),
          exact: true,
        }),
      ).toBeVisible()
      await selectTenant(page, tenant)

      const members = memberSection(page)
      const account = members.getByRole('combobox', {
        name: copy('consoleUi', 'members.write.accountLabel'),
        exact: true,
      })
      await account.fill(EMPLOYEE_EMAIL)
      await expect(employeeOption(page)).toBeVisible()
      await employeeOption(page).click()
      await members
        .getByRole('button', {
          name: copy('consoleUi', 'members.write.submit'),
          exact: true,
        })
        .click()

      const row = members
        .getByRole('table')
        .locator('tbody tr')
        .filter({ hasText: EMPLOYEE_DISPLAY_NAME })
      await expect(row).toHaveCount(1)
      await expect(
        row.getByText(copy('consoleUi', 'members.activeYes'), {
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
    // The operator leaves the console for the tenant area first: signing out
    // from the console lands on the plain sign-in surface, while the tenant
    // frame's session-ended surface is what the account helper waits for.
    await page
      .getByRole('banner')
      .getByRole('link', {
        name: copy('demo', 'console.backToApp'),
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
    const userMenu = page.getByRole('banner').getByTestId('user-menu-trigger')
    await expect(userMenu).toHaveAccessibleName(EMPLOYEE_DISPLAY_NAME)

    await userMenu.click()
    await page
      .getByRole('menuitem', {
        name: copy('demo', 'userMenu.accountSettings'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'account.title'),
        exact: true,
      }),
    ).toBeVisible()

    // The work address is on that account as a sign-in identifier and the
    // profile carries the operator's display name.
    await expect(
      page
        .getByRole('region', {
          name: copy('accountUi', 'identifiers.title'),
          exact: true,
        })
        .getByRole('listitem')
        .filter({ hasText: EMPLOYEE_EMAIL }),
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
    await expect(
      page.getByText(
        copy('tenancyUi', 'tenantSwitcher.switchedTo', {
          tenant: TARGET_TENANT,
        }),
        { exact: true },
      ),
    ).toBeVisible()
  })
})
