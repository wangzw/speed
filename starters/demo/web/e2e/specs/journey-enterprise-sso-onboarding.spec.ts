// @journey journey-enterprise-sso-onboarding.md
// @covers REQ-platform-admin-013, REQ-platform-config-017, REQ-platform-config-018, REQ-platform-config-020, REQ-platform-config-038, REQ-presentation-027, REQ-signin-001, REQ-signin-003, REQ-signin-004, REQ-signin-020, REQ-signin-021, REQ-signin-022, REQ-signin-027, REQ-signup-018, REQ-signup-047, REQ-signup-048, REQ-signup-053, REQ-signup-054, REQ-tenancy-009, REQ-tenancy-022, REQ-tenancy-023, REQ-tenancy-035, REQ-tenancy-036
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import type { DemoTarget } from '../fixtures/demo'
import {
  enterpriseIdentityProviderClaims,
  enterpriseIdentityProviderClient,
  enterpriseIdentityProviderUser,
} from '../fixtures/identity-provider'
import type { EnterpriseIdentityProvider } from '../fixtures/identity-provider'
import { expectAccountMenuName } from '../helpers/account-menu'
import {
  closeAccountMenu,
  openAccountMenu,
  organizationRow,
} from '../helpers/account-menu'
import {
  expandAccountRows,
  selectAccountSection,
} from '../helpers/account-sections'
import {
  EXISTING_TENANT,
  OPERATOR,
  TARGET_TENANT,
  adminLink,
  claimAndVerifyDomain,
  homeHeading,
  leaveConsole,
  openConsole,
  proveOperatorAddress,
  registerProvider,
  selectTenant,
  signInHeading,
  startEnterpriseSignIn,
} from '../helpers/enterprise-sso'
import { copy } from '../helpers/i18n'
import { chooseSelectOption } from '../helpers/select-option'
import { approveEnterpriseSignIn } from '../helpers/social'
import { switchToTenant } from '../helpers/switch-tenant'
import { expectClippedAnnouncement } from '../helpers/tenant-switch'

const PROVIDER_NAME = 'Globex Workforce'

const JIT_PROVIDER_NAME = 'Globex Workforce Self-Service'

const JIT_USERNAME = 'globex-newcomer'

const EMPLOYEE_DISPLAY_NAME = 'Globex Employee'

const EMPLOYEE_EMAIL = enterpriseIdentityProviderUser.email
const EMPLOYEE_USERNAME = 'globex-employee'

const CLAIM_DISPLAY_NAME = enterpriseIdentityProviderUser.name

const CLAIM_PROVIDER_NAME = 'Globex Workforce Claim'

const CLAIM_USERNAME = 'annsmith'

const INVALID_CLAIM_USERNAME = 'ann.smith'

const COLLIDING_CLAIM_USERNAME = EXISTING_TENANT

function memberSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'members.title'),
    exact: true,
  })
}

function employeeOption(page: Page): Locator {
  return page.getByRole('option', {
    name: new RegExp(`^${EMPLOYEE_DISPLAY_NAME}`),
  })
}

function usernameStep(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'signInFlow.usernameLabel'),
    exact: true,
  })
}

async function attemptClaimSignIn(
  demo: DemoTarget,
  claimValue: string,
): Promise<void> {
  const { page, server, accounts } = demo
  const provider = server.enterpriseIdentityProvider
  if (provider === undefined) {
    throw new Error(
      'the enterprise fixture did not start the identity provider this spec drives',
    )
  }
  provider.setPreferredUsername(claimValue)
  await accounts.openSignIn()
  await expect(signInHeading(page)).toBeVisible()
  await startEnterpriseSignIn(page, CLAIM_PROVIDER_NAME)
  await approveEnterpriseSignIn(page)
}

async function expectClaimRefused(page: Page): Promise<void> {
  await expect(
    page.getByText(copy('authUi', 'errors.authn.login_failed'), {
      exact: true,
    }),
  ).toBeVisible()
  await expect(
    page.getByText(copy('authUi', 'errors.authn.unavailable'), {
      exact: true,
    }),
  ).toHaveCount(0)
  await expect(usernameStep(page)).toHaveCount(0)
  await expect(homeHeading(page)).toHaveCount(0)
}

async function openAdminPage(page: Page, titleKey: string): Promise<void> {
  const title = copy('adminUi', titleKey)
  await adminLink(page, title).click()
  await expect(
    page.getByRole('heading', { level: 1, name: title, exact: true }),
  ).toBeVisible()
}

async function tableRows(page: Page): Promise<Locator> {
  const table = page.getByRole('table')
  await expect(table.locator('tbody tr').first()).toBeVisible()
  return table.locator('tbody tr')
}

function accountRow(page: Page, displayName: string): Locator {
  return page
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: displayName })
}

function organizationNameRow(page: Page, name: string): Locator {
  return page
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: name })
}

function memberRows(page: Page): Locator {
  return memberSection(page).getByRole('table').locator('tbody tr')
}

function registerOIDCProvider(
  page: Page,
  provider: EnterpriseIdentityProvider,
  name: string,
  provisioning: 'preprovisioned' | 'jit',
  claim?: 'preferred_username' | 'email',
): Promise<void> {
  return registerProvider(page, {
    protocol: 'oidc',
    name,
    provisioning,
    claim,
    config: {
      issuer: provider.issuer,
      client_id: provider.clientID,
      claims: enterpriseIdentityProviderClaims,
    },
    clientSecret: enterpriseIdentityProviderClient.secret,
  })
}

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
    await chooseSelectOption(
      dialog.getByRole('combobox', {
        name: copy('adminUi', 'accounts.create.emailViaLabel'),
        exact: true,
      }),
      page.getByRole('option', {
        name: copy('adminUi', 'accounts.create.via.admin'),
        exact: true,
      }),
    )
    await expect(submit).toBeEnabled()
    await submit.click()

    await expect(dialog).toHaveCount(0)
    const accountRow = page
      .getByRole('table')
      .locator('tbody tr')
      .filter({ hasText: EMPLOYEE_DISPLAY_NAME })
    await expect(accountRow).toHaveCount(1)
    await expect(
      accountRow.getByText(
        `${EMPLOYEE_DISPLAY_NAME} (${EMPLOYEE_USERNAME})`,
        { exact: true },
      ),
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

    await writeMembership(TARGET_TENANT)

    await writeMembership(EXISTING_TENANT)
  })

  await test.step('step 5: the employee uses the enterprise entry and sees the organization for the address', async () => {
    await leaveConsole(page)
    await accounts.signOut()
    await accounts.openSignIn()
    await expect(signInHeading(page)).toBeVisible()

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

    await expect(homeHeading(page)).toBeVisible()

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
    await switchToTenant(page, EXISTING_TENANT)

    // The menu's first entry names the current organization; expanding it lists the others.
    const menu = await openAccountMenu(page)
    await expect(menu.getByRole('menuitem').first()).toHaveAccessibleName(
      EXISTING_TENANT,
    )
    await organizationRow(page, EXISTING_TENANT).click()
    const choices = menu.getByRole('group', {
      name: copy('demo', 'userMenu.organizations'),
      exact: true,
    })
    await expect(
      choices.getByRole('menuitem', {
        name: EXISTING_TENANT,
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(
      choices.getByRole('menuitem', {
        name: TARGET_TENANT,
        exact: true,
      }),
    ).toBeVisible()

    await organizationRow(page, TARGET_TENANT).click()
    await expectClippedAnnouncement(
      page,
      copy('tenancyUi', 'tenantSwitcher.switchedTo', {
        tenant: TARGET_TENANT,
      }),
    )

    await expect(page.getByRole('menu')).toHaveCount(0)
    const switched = await openAccountMenu(page)
    await expect(switched.getByRole('menuitem').first()).toHaveAccessibleName(
      TARGET_TENANT,
    )
    await closeAccountMenu(page)
  })
})

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

    await openConsole(page)
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
    await leaveConsole(page)
    await accounts.signOut()
    await accounts.openSignIn()
    await expect(signInHeading(page)).toBeVisible()

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

    const username = page.getByRole('textbox', {
      name: copy('authUi', 'signInFlow.usernameLabel'),
      exact: true,
    })
    await expect(username).toBeVisible()
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
    await expect(homeHeading(page)).toBeVisible()

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
    await switchToTenant(page, TARGET_TENANT)
    const menu = await openAccountMenu(page)
    await expect(menu.getByText(TARGET_TENANT, { exact: true })).toBeVisible()
    await closeAccountMenu(page)
  })
})

test('enterprise-sso-onboarding: a claim-strategy provider opens the account with the asserted username', async ({
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

  await test.step('step 1: the operator registers a claim-strategy provider and claims the domain', async () => {
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

    await selectTenant(page, TARGET_TENANT)
    await expect(
      page.getByRole('heading', {
        name: copy('adminUi', 'sso.empty.title'),
        exact: true,
      }),
    ).toBeVisible()

    await registerOIDCProvider(
      page,
      provider,
      CLAIM_PROVIDER_NAME,
      'jit',
      'preferred_username',
    )
    await claimAndVerifyDomain(page, stub, CLAIM_PROVIDER_NAME)
  })

  const baseline = await test.step('step 2: the operator records the rows the roster holds', async () => {
    await openAdminPage(page, 'accounts.title')
    const accountsCount = await (await tableRows(page)).count()

    await openAdminPage(page, 'tenants.title')
    const organizationsCount = await (await tableRows(page)).count()

    await openAdminPage(page, 'invitations.title')
    await selectTenant(page, TARGET_TENANT)
    const members = memberRows(page)
    await expect(members.first()).toBeVisible()
    const membersCount = await members.count()

    return { accountsCount, organizationsCount, membersCount }
  })

  await test.step('step 3: the operator leaves the console and signs out', async () => {
    await leaveConsole(page)
    await accounts.signOut()
  })

  await test.step('step 4: a value the username form refuses refuses the login', async () => {
    await attemptClaimSignIn(demo, INVALID_CLAIM_USERNAME)
    await expectClaimRefused(page)
  })

  await test.step('step 5: a value a business tenant holds as its name refuses the login', async () => {
    await attemptClaimSignIn(demo, COLLIDING_CLAIM_USERNAME)
    await expectClaimRefused(page)
  })

  await test.step('step 6: the administrator pages show neither refusal wrote a row', async () => {
    await accounts.signIn({
      identifier: OPERATOR.username,
      password: OPERATOR.password,
    })
    await expect(homeHeading(page)).toBeVisible()
    await openConsole(page)

    await openAdminPage(page, 'accounts.title')
    expect(await (await tableRows(page)).count()).toBe(baseline.accountsCount)
    await expect(accountRow(page, CLAIM_DISPLAY_NAME)).toHaveCount(0)

    await openAdminPage(page, 'tenants.title')
    expect(await (await tableRows(page)).count()).toBe(
      baseline.organizationsCount,
    )
    await expect(organizationNameRow(page, CLAIM_USERNAME)).toHaveCount(0)
    await expect(
      organizationNameRow(page, INVALID_CLAIM_USERNAME),
    ).toHaveCount(0)
    await expect(
      organizationNameRow(page, COLLIDING_CLAIM_USERNAME),
    ).toHaveCount(1)

    await openAdminPage(page, 'invitations.title')
    await selectTenant(page, TARGET_TENANT)
    await expect(memberRows(page).first()).toBeVisible()
    expect(await memberRows(page).count()).toBe(baseline.membersCount)
    await expect(
      memberRows(page).filter({ hasText: CLAIM_DISPLAY_NAME }),
    ).toHaveCount(0)

    await leaveConsole(page)
    await accounts.signOut()
  })

  await test.step('step 7: a legal claim opens the account directly, with no username step', async () => {
    await attemptClaimSignIn(demo, CLAIM_USERNAME)
    await expect(homeHeading(page)).toBeVisible()
    await expect(usernameStep(page)).toHaveCount(0)

    const userMenu = page.getByTestId('account-entry-trigger')
    await expectAccountMenuName(userMenu, CLAIM_DISPLAY_NAME)

    const menu = await openAccountMenu(page)
    await expect(menu.getByRole('menuitem').first()).toHaveAccessibleName(
      TARGET_TENANT,
    )
    await menu.getByRole('menuitem').first().click()
    const choices = menu.getByRole('group', {
      name: copy('demo', 'userMenu.organizations'),
      exact: true,
    })
    await expect(
      choices.getByRole('menuitem', {
        name: CLAIM_DISPLAY_NAME,
        exact: true,
      }),
    ).toBeVisible()
    await closeAccountMenu(page)
  })

  await test.step('step 8: the account and its personal tenant carry the asserted username', async () => {
    await accounts.signOut()
    await accounts.signIn({
      identifier: OPERATOR.username,
      password: OPERATOR.password,
    })
    await expect(homeHeading(page)).toBeVisible()
    await openConsole(page)

    await openAdminPage(page, 'tenants.title')
    const personal = organizationNameRow(page, CLAIM_USERNAME)
    await expect(personal).toHaveCount(1)
    await expect(
      personal.getByRole('button', {
        name: copy('adminUi', 'tenantLifecycle.transferAria', {
          name: CLAIM_USERNAME,
        }),
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(
      personal.getByRole('button', {
        name: copy('adminUi', 'tenantLifecycle.deleteAria', {
          name: CLAIM_USERNAME,
        }),
        exact: true,
      }),
    ).toHaveCount(0)

    await openAdminPage(page, 'accounts.title')
    const opened = accountRow(page, CLAIM_DISPLAY_NAME)
    await expect(opened).toHaveCount(1)
    await expect(
      opened.getByText(`${CLAIM_DISPLAY_NAME} (${CLAIM_USERNAME})`, {
        exact: true,
      }),
    ).toBeVisible()
  })
})
