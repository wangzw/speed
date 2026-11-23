/**
 * J-enterprise-sso -- an operator connects an enterprise OIDC provider to a
 * tenant and an employee signs in with their work address.
 *
 * The file drives the journey three times, once per provisioning mode the
 * provider can declare. The first test is the pre-provisioned journey below.
 * The second has the provider open accounts itself and ask the person for a
 * username before anything exists. The third also opens accounts itself but
 * reads the username from the assertion's `preferred_username`, so the first
 * login lands directly in the account; it drives the two refusal branches
 * (a value the shared username form refuses, and one a business tenant already
 * holds as its name) before the legal attempt. Everything after the sign-in
 * surface -- the domain claim, the discovery by address, the switcher -- is
 * common to all three.
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
 *   7. The account menu's first entry names the current organization, that row
 *      expands the others rather than being a choice, and choosing the target
 *      tenant switches the interface to it.
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
 * fail on the requirement's whole claim. The operator-side steps drive each
 * requirement's positive branch only: the spec enables a provider that was
 * never disabled (so it cannot prove that a disabled provider leaves
 * discovery), verifies a domain that no other provider holds (so it cannot
 * prove the conflict refusal), and signs in once with a domain that is
 * verified (so it cannot prove the refusal a foreign domain or an unverified
 * domain produces). Those alternative paths stay with their own requirements
 * and are not claimed here. The just-in-time tests never meet an address an
 * existing account holds, so the link-required branch of REQ-signin-022 stays
 * with its own case. The claim-strategy test drives the invalid and taken-name
 * refusal branches; its missing-claim branch still needs its own case.
 */
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

/**
 * The display name a just-in-time account carries: the provider's own, because
 * no administrator prepared one.
 */
const CLAIM_DISPLAY_NAME = enterpriseIdentityProviderUser.name

/** The provider the operator registers for the claim-strategy onboarding. */
const CLAIM_PROVIDER_NAME = 'Globex Workforce Claim'

/**
 * The legal `preferred_username` the provider asserts for the first login. It
 * is already in the shared username form, so normalization leaves it alone and
 * the personal tenant's name must read exactly this.
 */
const CLAIM_USERNAME = 'annsmith'

/** A claim value the shared username form refuses: a dot is not in it. */
const INVALID_CLAIM_USERNAME = 'ann.smith'

/**
 * A claim value a business tenant already holds as its name. The account
 * lookup cannot see the tenant namespace, so this collision surfaces when the
 * personal tenant is written and the whole creation rolls back.
 */
const COLLIDING_CLAIM_USERNAME = EXISTING_TENANT

/** The invitations page's member section. */
function memberSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'members.title'),
    exact: true,
  })
}

/** The account picker's option for the employee's account. */
function employeeOption(page: Page): Locator {
  return page.getByRole('option', {
    name: new RegExp(`^${EMPLOYEE_DISPLAY_NAME}`),
  })
}

/** The username step a just-in-time first login may park at. */
function usernameStep(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'signInFlow.usernameLabel'),
    exact: true,
  })
}

/**
 * One claim-strategy first login: set the assertion's `preferred_username`,
 * start from the sign-in surface, and approve at the provider. What follows
 * the callback is the caller's to assert.
 */
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

/**
 * Assert the callback refused the first login through the surface's own
 * vocabulary. The login-failed sentence is what a claim refusal's 401 renders;
 * the unavailable sentence is what a 503 renders, so its absence is the
 * surface's evidence the answer was the refusal rather than an outage. No
 * username step and no landed session follow from the account never opening.
 */
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

/** Open one admin page from the console's navigation and wait for its title. */
async function openAdminPage(page: Page, titleKey: string): Promise<void> {
  const title = copy('adminUi', titleKey)
  await adminLink(page, title).click()
  await expect(
    page.getByRole('heading', { level: 1, name: title, exact: true }),
  ).toBeVisible()
}

/** The rows the admin page's one table lists, once it has rendered them. */
async function tableRows(page: Page): Promise<Locator> {
  const table = page.getByRole('table')
  await expect(table.locator('tbody tr').first()).toBeVisible()
  return table.locator('tbody tr')
}

/** The Accounts page's row for a display name, or none. */
function accountRow(page: Page, displayName: string): Locator {
  return page
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: displayName })
}

/** The Organizations page's row whose name is the given value. */
function organizationNameRow(page: Page, name: string): Locator {
  return page
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: name })
}

/** The globex member rows, on the invitations page's member section. */
function memberRows(page: Page): Locator {
  return memberSection(page).getByRole('table').locator('tbody tr')
}

/**
 * Register one enabled OIDC provider through the admin's provider form,
 * naming the local fixture's issuer, client and claim mapping. The provider
 * belongs to the tenant the page is switched to.
 *
 * A just-in-time provider must state how a first login determines its
 * username. `claim` selects "From claim" and the assertion claim the account's
 * username is read from; omitted, the form keeps its default, which is the
 * before-opening confirmation the file's second test drives.
 */
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
    // the start page is where the account menu is read.
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
    // The identity cell names the account by display name and login username
    // together, which is the username the create named; the address remains on
    // record beside it and the internal id is never rendered.
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
    await leaveConsole(page)
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
    await expect(homeHeading(page)).toBeVisible()

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
    // organization, which the host may settle first; move to the seeded
    // organization through the same control this step drives so the switch
    // below starts from it.
    await switchToTenant(page, EXISTING_TENANT)

    // REQ-tenancy-022/035: the menu's first entry names the current
    // organization, and expanding it lists the other organizations the
    // employee belongs to.
    const menu = await openAccountMenu(page)
    await expect(menu.getByRole('menuitem').first()).toHaveAccessibleName(
      EXISTING_TENANT,
    )
    await organizationRow(page, EXISTING_TENANT).click()
    const choices = menu.getByRole('group', {
      name: copy('demo', 'userMenu.organizations'),
      exact: true,
    })
    // REQ-tenancy-036: the current organization's row is the expansion
    // control, never one of the choices, so the group does not list it again.
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

    // REQ-tenancy-023: choosing another organization moves the interface to it.
    await organizationRow(page, TARGET_TENANT).click()
    await expectClippedAnnouncement(
      page,
      copy('tenancyUi', 'tenantSwitcher.switchedTo', {
        tenant: TARGET_TENANT,
      }),
    )

    // The menu closed on the pick; opening it again names the target as the
    // organization being acted in.
    await expect(page.getByRole('menu')).toHaveCount(0)
    const switched = await openAccountMenu(page)
    await expect(switched.getByRole('menuitem').first()).toHaveAccessibleName(
      TARGET_TENANT,
    )
    await closeAccountMenu(page)
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
    // The operator leaves the admin for the tenant area first: signing out
    // from the admin lands on the plain sign-in surface.
    await leaveConsole(page)
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
    // just-in-time login wrote. The account owns a personal organization as
    // well, which the host may settle first, so the helper moves from
    // whichever row is current.
    await switchToTenant(page, TARGET_TENANT)
    const menu = await openAccountMenu(page)
    await expect(menu.getByText(TARGET_TENANT, { exact: true })).toBeVisible()
    await closeAccountMenu(page)
  })
})

/**
 * The journey's third onboarding mode: the provider states how a first login
 * picks the username. A claim-strategy provider reads the account's username
 * from the assertion, so the callback opens the account and no username step
 * appears. The provider's fixed user asserts one `preferred_username` per
 * attempt; the fixture's handle swaps it between sign-ins, which is the only
 * thing that changes.
 *
 * The refusal branches come first, so the administrator's own pages can prove
 * they wrote nothing before the legal attempt opens the account: a value the
 * shared username form refuses, and a value a business tenant already holds as
 * its name. The second is the creation-time collision the account lookup
 * cannot see; it surfaces when the personal tenant, whose name is the
 * username, is written and rolls the whole creation back.
 */
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

    // The provider's username strategy is the claim the assertion carries.
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
    // The two refusals left the browser on the sign-in surface; the operator
    // returns to the console the same way a person does.
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
    // Neither refused value added a tenant. The collision value is already the
    // business tenant's name, so the roster keeps exactly its own row.
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

    // No administrator prepared a display name, so the account carries the
    // provider's.
    const userMenu = page.getByTestId('account-entry-trigger')
    await expectAccountMenuName(userMenu, CLAIM_DISPLAY_NAME)

    // The account opens into the provider's organization, not its personal
    // one: the menu's current row names the provider's tenant.
    const menu = await openAccountMenu(page)
    await expect(menu.getByRole('menuitem').first()).toHaveAccessibleName(
      TARGET_TENANT,
    )
    // The personal organization's menu row is named by the account's display
    // name (REQ-tenancy-050), so the name is not the menu's to show; the
    // roster below is where it is read.
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
    // The tenant area names the personal organization after the account; the
    // name the request surface would use is on the platform roster, so the
    // operator returns there.
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
    // The row is the account's personal tenant, not a business one: its name
    // is the account's username, and a personal organization carries no owner
    // transfer or delete control. The platform operator holds tenant.manage,
    // so those controls would render on a business tenant's row.
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
    // The identity cell names the account by the display name and the username
    // the claim supplied, which is also the personal tenant's name asserted
    // above; the internal id is never rendered.
    await expect(
      opened.getByText(`${CLAIM_DISPLAY_NAME} (${CLAIM_USERNAME})`, {
        exact: true,
      }),
    ).toBeVisible()
  })
})
