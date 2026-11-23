/**
 * The tenant administrator's organization administration surface.
 *
 * A business tenant's administrator -- a member holding the tenant's governed
 * permissions and no platform permission -- reaches the organization admin area
 * from the tenant home. This spec drives that surface the way the person
 * does: the account menu's organization group and the start card, the members
 * page's search, role filter and management panel, the invitations page's
 * issue, resend, revoke and status filter, the invitation handed to a
 * colleague in a second browser context, the tenant roles and grants, the
 * tenant settings layer, the tenant switch, the enterprise providers, the
 * `system` tenant and an impersonated session.
 *
 * The administrator signs in as the review fixture's proved-email account
 * (`e2eTenantAdmin`). The seed makes it acme's only tenant administrator,
 * which is the journey's precondition for the self-binding refusal: the
 * tenant has exactly one `tenant-admin` binding. The colleague comes through
 * a real invitation accepted in a second context; that acceptance is also
 * what makes the colleague an ordinary member for the "no entry" branch.
 *
 * The refusals are driven through the rendered controls and asserted on the
 * console's resolved failure copy. Two refusals the document names cannot be
 * reached through a rendered control and are therefore not claimed:
 *
 *  - The platform-domain permission grant. The organization grant picker
 *    offers only the governed catalog (REQ-tenant-admin-026), so a
 *    platform-domain code cannot be selected. Reaching the refusal would
 *    require the direct HTTP call the honesty gate forbids.
 *  - The `platform-admin` grant. The same governed catalog is the only grant
 *    source, so the page cannot name `platform-admin` as a grant target. Its
 *    role creation is driven, and the catalog is asserted not to offer the
 *    platform permission.
 *
 * The remaining narrow claims:
 *
 *  - REQ-tenant-admin-003 is driven as the switch retract/restore pair (to the
 *    personal tenant and back); a business-to-business switch is not driven.
 *  - REQ-tenant-admin-011/012/014 cover the current tenant's provider list;
 *    the cross-tenant absence is the fixture's single-tenant shape and is not
 *    driven through a second organization.
 *  - REQ-tenant-admin-033 (the last-administrator floor) and REQ-tenant-admin-
 *    047 are not claimed: the seed's one `tenant-admin` binding belongs to the
 *    signed-in administrator, and the panel disables that own binding before
 *    any request (REQ-tenant-admin-046), so the server's last-administrator
 *    path is not reachable through a rendered control. The Go tier asserts the
 *    server's refusals.
 *  - REQ-tenant-admin-034 covers the `platform-admin` role creation refusal.
 *    REQ-tenant-admin-048's binding half is not reachable through a rendered
 *    control any more: role binding moved into the member management panel,
 *    whose role list is the tenant's own roles, and `platform-admin` is not
 *    one of them. The refusal is the roles endpoint's, asserted by the unit
 *    tier.
 *  - REQ-tenant-admin-054 (cursor paging) is not driven: the review tenant
 *    holds one page of members and invitations, so neither pager renders.
 *  - REQ-tenancy-063 (an owner's account cannot be deleted or deactivated) is
 *    not driven: this spec never deletes or deactivates an account, and the
 *    PRD marks that requirement's end-to-end case unimplemented.
 *  - The personal-tenant direct visit is driven as an in-app history visit
 *    (`goBack`), because a full document load re-settles the current tenant
 *    to the account's non-personal membership; the history visit keeps the
 *    personal tenant current, which is the state the requirement describes.
 *
 * The settings surface's three refusals have no rendered control to drive.
 * A platform-held policy needs a multi-factor administrator, because the
 * admission layer would challenge every tenant-scoped request of a
 * single-factor one; that server names the review build's operator, whose
 * seasoned authenticator its seed provides, as the tenant's administrator,
 * and the branch signs in with that factor before reading the policy as an
 * enforced, disabled switch. A tenant the review build protects is read-only
 * as a whole, so its page renders no switch at all. A platform-level setting
 * is neither rendered nor addressable from the tenant surface. In all three
 * cases the review server fixture makes the same request through the
 * product's API beside the rendered assertion, as the same administrator
 * account: the protected write answers 403, the enforced policy's disable
 * answers 409, and the tenant surface answers the platform keys' writes and
 * deletes with 404. Those API answers are sibling evidence; the rendered
 * state above each one carries the journey.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts. The only non-UI reads are the email verification code and
 * the impersonation step-up SMS code from the review process log, and the
 * responses the pages themselves receive: the organization profile read, whose
 * owner
 * account identifier never appears on the page, and the tenant's member and
 * invitation reads, whose account identifiers the identity surfaces must
 * likewise never render, and the target's inbox read, whose expiry parameter
 * the transfer notice's body must render as a time. Those tests learn the
 * values from the response the page itself received: the identity tests assert
 * the rendered surface carries neither identifier, and the notification test
 * asserts the body carries the resolved time, not the delivery marker or the
 * raw instant.
 */
// @journey journey-tenant-admin.md
// @covers REQ-tenant-admin-001, REQ-tenant-admin-002, REQ-tenant-admin-003, REQ-tenant-admin-004, REQ-tenant-admin-006, REQ-tenant-admin-008, REQ-tenant-admin-009, REQ-tenant-admin-010, REQ-tenant-admin-011, REQ-tenant-admin-012, REQ-tenant-admin-013, REQ-tenant-admin-014, REQ-tenant-admin-015, REQ-tenant-admin-016, REQ-tenant-admin-017, REQ-tenant-admin-018, REQ-tenant-admin-022, REQ-tenant-admin-023, REQ-tenant-admin-025, REQ-tenant-admin-026, REQ-tenant-admin-027, REQ-tenant-admin-028, REQ-tenant-admin-029, REQ-tenant-admin-030, REQ-tenant-admin-031, REQ-tenant-admin-034, REQ-tenant-admin-041, REQ-tenant-admin-042, REQ-tenant-admin-043, REQ-tenant-admin-046, REQ-tenant-admin-048, REQ-tenant-admin-050, REQ-tenant-admin-051, REQ-tenant-admin-052, REQ-tenant-admin-053, REQ-tenant-admin-055, REQ-tenant-admin-056, REQ-tenant-admin-057, REQ-tenant-admin-058, REQ-tenant-admin-059, REQ-tenant-admin-060, REQ-tenant-admin-061, REQ-tenant-admin-062, REQ-tenant-admin-063, REQ-tenant-admin-064, REQ-tenant-admin-065, REQ-tenant-admin-066, REQ-tenant-admin-068, REQ-tenancy-008, REQ-tenancy-013, REQ-tenancy-015, REQ-tenancy-016, REQ-tenancy-021, REQ-tenancy-060, REQ-tenancy-061, REQ-tenancy-062, REQ-tenancy-064, REQ-tenancy-080, REQ-tenancy-081
import type { BrowserContext, Locator, Page } from '@playwright/test'

import {
  e2eInvitee,
  e2eOrdinaryMember,
  e2eProtectedTenant,
  e2eSystemTenantAdmin,
  e2eTenantAdmin,
  e2eTenantPolicies,
  e2eUnprovedTenantAdmin,
} from '../fixtures/config'
import {
  enterpriseIdentityProviderClaims,
  enterpriseIdentityProviderClient,
  enterpriseIdentityProviderDomain,
  enterpriseIdentityProviderRecordName,
  enterpriseIdentityProviderUser,
} from '../fixtures/identity-provider'
import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import type { E2EServer } from '../fixtures/server'
import { scaledTimeout } from '../fixtures/time-scale'
import { createAccounts } from '../helpers/accounts'
import type { Accounts } from '../helpers/accounts'
import {
  adminEntry,
  closeAccountMenu,
  openAccountMenu,
  organizationRow,
} from '../helpers/account-menu'
import { copy, copyKey, copyPattern } from '../helpers/i18n'
import { totpCode } from '../helpers/totp'
import {
  cell,
  collectTenantAccountIds,
  columnIndex,
  domainsSection,
  expectNoAccountIds,
  escapeRegExp,
  expectOrganizationEntryAbsent,
  expectOrganizationEntryPresent,
  expandCatalogSection,
  grantsSection,
  homeHeading,
  invitationRow,
  invitationStatusFilter,
  invitationsSection,
  mainNavigation,
  manageMemberButton,
  memberPanel,
  memberRoleFilter,
  memberRow,
  memberSearchField,
  membersSection,
  openOrganizationPage,
  providerFormSection,
  rolesSection,
  usageSection,
  verificationBanner,
  verificationSection,
  organizationHomeCard,
  organizationPath,
} from '../helpers/organization-admin'
import { chooseSelectOption } from '../helpers/select-option'

/** The seeded business tenant the organization surface acts on. Its id is its name. */
const TENANT = 'acme'

/**
 * The template's ordinary member's login username. The review seed writes the
 * account as a username with no separate email identifier, so the member read
 * answers this value.
 */
const ORDINARY_MEMBER_USERNAME = 'member'

/** The non-builtin role the administrator creates. */
const ROLE = 'Support'

/** The role's free-text description. */
const ROLE_DESCRIPTION = 'Reviews support requests and answers audit questions.'

/** The governed permission granted to the role. */
const PERMISSION = 'authn.membership.manage'

/** The eight governed permissions, in the order the catalog sorts them. */
const GOVERNED_PERMISSIONS = [
  'authn.idp.manage',
  'authn.membership.manage',
  'authz.tenant.role.manage',
  'authz.tenant.role.read',
  'settings.tenant.read',
  'settings.tenant.write',
  'tenant.profile.read',
  'tenant.profile.write',
] as const

/** A platform-domain permission the governed catalog must not offer. */
const PLATFORM_PERMISSION = 'authn.account.create'

/** The SSO provider the organization registers. */
const PROVIDER_NAME = 'Acme Organization OIDC'

/** The printed error the reserved-role and self-binding refusals render. */
function protectedText(): string {
  return copy('adminUi', 'errors.authz.protected')
}

/** The readable name the governed catalog renders for a permission code:
 * the declaring module's own description key, with the code's separators
 * turned into dashes. */
function permissionDescription(permission: string): string {
  return copyKey(
    'authzPermissions',
    `authz.permission.${permission.replace(/[.:]/g, '-')}`,
  )
}

/** The address of an invitation used to exercise resend and revoke. */
const RESENT_INVITEE = 'resent-invitee@demo.test'

/** The address and account a role-bearing invitation is accepted with: the
 * journey registers it through the link, because no seeded account is a
 * non-member of the organization. */
const ROLE_INVITEE = 'role-invitee@demo.test'
const ROLE_INVITEE_IDENTITY = {
  email: ROLE_INVITEE,
  username: 'role-invitee',
  displayName: 'Role Invitee',
  password: 'role-invitee-secret',
} as const

/** The status tag's text of one invitation row. A terminal row stacks the
 * tag over a note naming the instant it settled, so the cell's first line is
 * the tag's own label; the lookup reads the cell's text instead of a
 * presentation class name. */
async function invitationStatus(page: Page, email: string): Promise<string> {
  const table = invitationsSection(page).getByRole('table')
  const index = await columnIndex(table, copy('adminUi', 'invitations.column.status'))
  const text = await cell(invitationRow(page, email), index).innerText()
  return text.split('\n')[0]!.trim()
}

/** The one-time panel that hands out the issued invitation's acceptance link. */
function invitationLinkPanel(page: Page, email: string): Locator {
  return page.getByRole('status').filter({
    hasText: copy('adminUi', 'invitations.issued.link', { email }),
  })
}

/** Open one member's management panel and leave it open. */
async function openMemberPanel(page: Page, displayName: string): Promise<Locator> {
  await manageMemberButton(page, displayName).click()
  const panel = memberPanel(page)
  await expect(panel).toBeVisible()
  return panel
}

/**
 * Close one member's management panel through its close control. The control
 * is named by the member's composed identity label, which this helper leaves
 * open; the panel itself is matched by its own title.
 */
async function closeMemberPanel(page: Page): Promise<void> {
  const panel = memberPanel(page)
  await panel
    .getByRole('button', {
      name: copyPattern('adminUi', 'members.panel.closeAria', {
        name: '[^"]*',
      }),
    })
    .click()
  await expect(panel).toHaveCount(0)
}

/** The bindings list item for one role inside an open member panel. */
function bindingRow(panel: Locator, role: string): Locator {
  return panel.locator('li').filter({ hasText: role })
}

/** Select one value of the invitations section's status filter. */
async function chooseInvitationStatus(
  page: Page,
  key: string,
): Promise<void> {
  await chooseSelectOption(
    invitationStatusFilter(page),
    page.getByRole('option', { name: copy('adminUi', key), exact: true }),
  )
}

/** The impersonation page's start controls. */
function targetField(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'impersonations.start.accountLabel'),
    exact: true,
  })
}

/**
 * Choose the impersonation target through the account picker. The operator
 * identifies an account by a login name it can read, never by the internal id:
 * the field searches the account roster and hands the chosen row's id to the
 * request. The caller re-runs this after a status read remounts the form,
 * because a remount drops the selection with the draft.
 */
async function chooseImpersonationTarget(page: Page): Promise<void> {
  const field = targetField(page)
  await field.click()
  await field.fill(e2eTenantAdmin.username)
  await page
    .getByRole('option', {
      name: new RegExp(escapeRegExp(e2eTenantAdmin.displayName)),
    })
    .click()
}
function reasonField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('adminUi', 'impersonations.start.reasonLabel'),
    exact: true,
  })
}
function startButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'impersonations.start.submit'),
    exact: true,
  })
}
function stepUpDialog(page: Page): Locator {
  return page.getByRole('dialog', {
    name: copy('adminUi', 'stepUp.title'),
    exact: true,
  })
}
function impersonationBanner(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('authUi', 'impersonation.regionLabel'),
    exact: true,
  })
}

/** The admin's navigation landmark (the platform console's own shell). */
function adminNavigation(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

/**
 * The account menu row naming the current organization. The row is a menuitem
 * while the account has another organization to switch to and a plain row (no
 * role) when it has none, so the row's text is what both shapes share.
 */
function currentOrganizationRow(page: Page, name: string): Locator {
  return page.getByRole('menu').getByText(name, { exact: true })
}

/**
 * Move the tenant area onto `target` through the account menu's organization
 * row. The row names the current organization and expands to the others; a
 * pick closes the menu and announces the switch in the menu's live region.
 */
async function switchOrganization(
  page: Page,
  current: string,
  target: string,
): Promise<void> {
  await openAccountMenu(page)
  const currentRow = organizationRow(page, current)
  await expect(currentRow).toBeVisible()
  await currentRow.click()
  const row = page
    .getByRole('menu')
    .getByRole('menuitem', { name: target, exact: true })
  await expect(row).toBeVisible()
  await row.click()
  await expect(page.getByRole('menu')).toHaveCount(0)
  await expect(
    page.getByRole('status').filter({
      hasText: copy('tenancyUi', 'tenantSwitcher.switchedTo', {
        tenant: target,
      }),
    }),
  ).toBeVisible()
}

/** The seeded platform operator the impersonation branch signs in as. */
const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

/** The invited address as the server masks it on the public read. */
const MASKED_INVITEE = 'i***@demo.test'

/** The operator's authenticator seed, fixed by the review fixture. */
const TOTP_SEED = 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP'

/** The phone the review fixture proves and binds the operator's SMS factor to. */
const OPERATOR_PHONE = '+15551234567'

/** The reason recorded on the impersonation the branch starts. */
const IMPERSONATION_REASON =
  'Support ticket 4712: the organization admin surface during impersonation.'

/**
 * Sign the operator in with the password and the seeded authenticator factor.
 * The password step parks the login on its second factor; the operator holds
 * the authenticator and the SMS methods, and the authenticator one is the
 * seasoned local factor the impersonation gate accepts.
 */
async function signInOperatorWithTotp(page: Page, accounts: {
  signIn(input: { readonly identifier: string; readonly password: string }): Promise<void>
}): Promise<void> {
  await accounts.signIn({
    identifier: OPERATOR.username,
    password: OPERATOR.password,
  })
  const passwordPanel = page.getByRole('tabpanel', {
    name: copy('authUi', 'passwordSignIn.title'),
    exact: true,
  })
  await passwordPanel
    .getByRole('button', {
      name: copy('authUi', 'signInFlow.factorKind.totp'),
      exact: true,
    })
    .click()
  const codeField = passwordPanel.getByRole('textbox', {
    name: copy('authUi', 'smsSignIn.codeLabel'),
    exact: true,
  })
  await expect(codeField).toBeVisible()
  await codeField.fill(totpCode(TOTP_SEED))
  await passwordPanel
    .getByRole('button', {
      name: copy('authUi', 'signInFlow.submit'),
      exact: true,
    })
    .click()
  await expect(homeHeading(page)).toBeVisible()
}

test.describe('the tenant administrator manages the organization', () => {
  test.use({
    seedTenantAdmin: true,
    seedInvitee: true,
    // The journey's steps, and the platform-item probe inside step 17, make
    // more than the declared hundred requests a minute from this one account.
    // The review server raises the per-identity quota so the journey reaches
    // the product instead of the counting layer; the layer stays assembled.
    identityQuota: '10000',
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: the administrator invites, governs and switches the organization', async ({
    demo,
    browser,
  }) => {
    // The journey drives an invitation through a second context, the member
    // panel's five writes, the invitation resend/revoke cycle, the role
    // surface, the settings write and two tenant switches; the tier's default
    // bound covers one server and one journey, not this many settle points.
    test.setTimeout(scaledTimeout(600_000))

    const { page, server, accounts } = demo
    // The account ids the organization surface reads must never reach the
    // rendered page; this collects them from the product's own reads so the
    // assertions below can look for them.
    const accountIds = collectTenantAccountIds(page, TENANT)
    let inviteeContext: BrowserContext | undefined
    let inviteePage: Page | undefined
    let acceptanceLink = ''
    let inviteeAccount = ''

    try {
      await test.step('step 1: the tenant administrator signs in and the entry and card appear', async () => {
        await accounts.signIn({
          identifier: e2eTenantAdmin.username,
          password: e2eTenantAdmin.password,
        })
        await expect(homeHeading(page)).toBeVisible()

        // REQ-tenant-admin-001 / REQ-tenant-admin-002: a tenant administrator
        // holding no platform permission still sees the account menu's
        // organization group -- on both account-menu surfaces -- and the start
        // card, with one real link per openable page. The helper asserts the
        // group's accessible name and every entry and closes each menu, so the
        // caller's next click lands on the page.
        await expectOrganizationEntryPresent(page)
      })

      await test.step('step 2: the card and the entry land on the same first page', async () => {
        await organizationHomeCard(page).click()
        await expect(
          page.getByRole('heading', {
            level: 1,
            name: copy('adminUi', 'organization.nav.profile'),
            exact: true,
          }),
        ).toBeVisible()
        expect(new URL(page.url()).pathname).toBe(organizationPath('/profile'))

        await mainNavigation(page)
          .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
          .click()
        await expect(homeHeading(page)).toBeVisible()
        await openOrganizationPage(page, 'profile')
        expect(new URL(page.url()).pathname).toBe(organizationPath('/profile'))

        // The next step reads the member table, so leave the first page for
        // the invitations page through its account-menu entry.
        await openOrganizationPage(page, 'invitations')
      })

      await test.step('step 3: the member table lists this tenant with names, roles, status, source and joined-at', async () => {
        const table = membersSection(page).getByRole('table')
        const nameColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.displayName'),
        )
        const usernameColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.username'),
        )
        const rolesColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.roles'),
        )
        const statusColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.status'),
        )
        const sourceColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.source'),
        )
        const joinedColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.joinedAt'),
        )

        // REQ-tenant-admin-008 / REQ-tenant-admin-051: the current
        // administrator is in its own list, with the name, account, roles,
        // status, source and join time the record carries.
        const admin = memberRow(page, e2eTenantAdmin.displayName)
        await expect(admin).toHaveCount(1)
        await expect(cell(admin, nameColumn)).toHaveText(
          e2eTenantAdmin.displayName,
        )
        // The username column shows the member record's login username; the
        // name column beside it is the display name, and the internal account
        // id is not what either shows.
        await expect(cell(admin, usernameColumn)).toHaveText(
          e2eTenantAdmin.username,
        )
        await expect(cell(admin, rolesColumn)).toContainText('tenant-admin')
        await expect(cell(admin, statusColumn)).toHaveText(
          copy('adminUi', 'members.status.active'),
        )
        await expect(cell(admin, sourceColumn)).toHaveText(
          copy('adminUi', 'members.source.admin'),
        )
        await expect(cell(admin, joinedColumn)).not.toBeEmpty()

        // The template's ordinary member is here too, carrying no role;
        // nothing from another tenant is.
        const ordinary = memberRow(page, e2eOrdinaryMember.displayName)
        await expect(ordinary).toHaveCount(1)
        await expect(cell(ordinary, usernameColumn)).toHaveText(
          ORDINARY_MEMBER_USERNAME,
        )
        await expect(cell(ordinary, rolesColumn)).toHaveText(
          copy('adminUi', 'members.roles.none'),
        )
        await expect(cell(ordinary, statusColumn)).toHaveText(
          copy('adminUi', 'members.status.active'),
        )
        await expect(cell(ordinary, sourceColumn)).toHaveText(
          copy('adminUi', 'members.source.admin'),
        )

        // The organization surface resolves every identity from its own member
        // read; neither section renders the account ids that read carries, in
        // visible text or in any attribute.
        const ids = await accountIds.settled()
        await expectNoAccountIds(invitationsSection(page), ids)
        await expectNoAccountIds(membersSection(page), ids)
      })

      await test.step('step 4: the member search narrows the list and distinguishes an empty search', async () => {
        // REQ-tenant-admin-052: a prefix narrows the server-side read to the
        // matching members; a query with no match says no member matches it
        // rather than reporting an empty organization.
        await memberSearchField(page).fill('Demo Member')
        await expect
          .poll(async () => memberRow(page, e2eOrdinaryMember.displayName).count())
          .toBe(1)
        await expect(memberRow(page, e2eTenantAdmin.displayName)).toHaveCount(0)

        await memberSearchField(page).fill('no-such-member')
        await expect(
          membersSection(page).getByRole('heading', {
            name: copy('adminUi', 'members.empty.searchTitle'),
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          membersSection(page).getByText(
            copy('adminUi', 'members.empty.searchDescription', {
              query: 'no-such-member',
            }),
            { exact: true },
          ),
        ).toBeVisible()

        // Clearing restores the full list.
        await page
          .getByRole('button', {
            name: copy('adminUi', 'members.search.clear'),
            exact: true,
          })
          .click()
        await expect(memberRow(page, e2eTenantAdmin.displayName)).toHaveCount(1)
        await expect(memberRow(page, e2eOrdinaryMember.displayName)).toHaveCount(1)
      })

      await test.step('step 5: the role filter narrows the list and a ?role= address opens filtered', async () => {
        // REQ-tenant-admin-053: the role filter is server-side, writes the
        // role into the address, and a direct visit with that address opens
        // the filtered view with the filter applied.
        await chooseSelectOption(
          memberRoleFilter(page),
          page.getByRole('option', { name: 'tenant-admin', exact: true }),
        )
        await expect
          .poll(async () => memberRow(page, e2eTenantAdmin.displayName).count())
          .toBe(1)
        await expect(memberRow(page, e2eOrdinaryMember.displayName)).toHaveCount(0)
        expect(new URL(page.url()).search).toBe(
          `?role=${encodeURIComponent('tenant-admin')}`,
        )

        // A full load with the parameter opens the filtered view directly.
        await page.goto(
          `${server.url}${organizationPath('/invitations')}?role=${encodeURIComponent(
            'tenant-admin',
          )}`,
        )
        await expect(
          page.getByRole('heading', {
            level: 1,
            name: copy('adminUi', 'invitations.title'),
            exact: true,
          }),
        ).toBeVisible()
        await expect
          .poll(async () => memberRow(page, e2eTenantAdmin.displayName).count())
          .toBe(1)
        await expect(memberRow(page, e2eOrdinaryMember.displayName)).toHaveCount(0)

        // Clearing the filter removes the parameter and restores both rows.
        // A cleared role filter keeps the Select's empty value, which the
        // closed control renders as the zero-width placeholder rather than
        // the option's "Any role" text; the address and rows below carry the
        // cleared state.
        await chooseSelectOption(
          memberRoleFilter(page),
          page.getByRole('option', {
            name: copy('adminUi', 'members.filter.anyRole'),
            exact: true,
          }),
          null,
        )
        await expect
          .poll(async () => memberRow(page, e2eOrdinaryMember.displayName).count())
          .toBe(1)
        expect(new URL(page.url()).search).toBe('')
      })

      await test.step('step 6: the administrator issues an invitation, the panel shows its delivery and close is confirmed', async () => {
        await page
          .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
            exact: true,
          })
          .fill(e2eInvitee.identifier)
        await page
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issue.submit'),
            exact: true,
          })
          .click()
        await expect(
          page.getByText(
            copy('adminUi', 'invitations.issued.confirm', {
              email: e2eInvitee.identifier,
            }),
            { exact: true },
          ),
        ).toBeVisible()

        // REQ-tenant-admin-009 / REQ-tenant-admin-059: the acceptance value is
        // handed out once, with its validity, the bearer risk, the copy
        // control and the delivery result.
        const panel = invitationLinkPanel(page, e2eInvitee.identifier)
        await expect(panel).toBeVisible()
        await expect(
          panel.getByRole('button', {
            name: copy('adminUi', 'invitations.issued.copyLink'),
            exact: true,
          }),
        ).toBeVisible()
        const value = panel.getByText(
          new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
        )
        await expect(value).toBeVisible()
        acceptanceLink = ((await value.textContent()) ?? '').trim()
        expect(acceptanceLink).toMatch(
          new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
        )
        await expect(
          panel.getByText(
            copyPattern('adminUi', 'invitations.issued.expiresAt', {
              time: '.+',
            }),
          ),
        ).toBeVisible()
        await expect(
          panel.getByText(copy('adminUi', 'invitations.issued.risk'), {
            exact: true,
          }),
        ).toBeVisible()
        // REQ-tenant-admin-061: this deployment's mail carrier is the log
        // carrier, which reaches no recipient, so the invitation channel
        // reports not_configured and the panel hands out the link instead of
        // reporting an email as sent.
        await expect(
          panel.getByText(
            copy('adminUi', 'invitations.issued.delivery.not_configured'),
            { exact: true },
          ),
        ).toBeVisible()
        expect(acceptanceLink.startsWith(`${server.url}/invitations/`)).toBe(
          true,
        )

        // Closing is the only moment the sole copy is dropped, so it asks
        // first; keeping it leaves the panel in place.
        const close = panel.getByRole('button', {
          name: copy('adminUi', 'invitations.issued.closeAria', {
            tenant: TENANT,
            email: e2eInvitee.identifier,
          }),
          exact: true,
        })
        await close.click()
        const closeDialog = page.getByRole('dialog', {
          name: copy('adminUi', 'invitations.issued.closeTitle'),
          exact: true,
        })
        await expect(closeDialog).toBeVisible()
        await closeDialog
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issued.keepOpen'),
            exact: true,
          })
          .click()
        await expect(closeDialog).toHaveCount(0)
        await expect(panel).toBeVisible()
        await expect(panel.getByText(acceptanceLink, { exact: true })).toBeVisible()

        await close.click()
        await expect(closeDialog).toBeVisible()
        await closeDialog
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issued.close'),
            exact: true,
          })
          .click()
        await expect(panel).toHaveCount(0)
        await expect(page.getByText(acceptanceLink, { exact: true })).toHaveCount(
          0,
        )

        // REQ-tenant-admin-010 / REQ-tenancy-015: the list carries the pending
        // invitation with its address, status, inviter and expiry.
        const row = invitationRow(page, e2eInvitee.identifier)
        await expect(row).toHaveCount(1)
        await expect(
          row.getByText(copy('adminUi', 'invitations.status.pending'), {
            exact: true,
          }),
        ).toBeVisible()
        // The inviter column shows the display name; the username is the
        // hover title, not a second visible value.
        await expect(
          row.getByText(e2eTenantAdmin.displayName, { exact: true }),
        ).toBeVisible()
        const expiryColumn = await columnIndex(
          invitationsSection(page).getByRole('table'),
          copy('adminUi', 'invitations.column.expires'),
        )
        await expect(cell(row, expiryColumn)).not.toBeEmpty()
        // Once the one-time value is dropped, the now-historical row offers no
        // copy-link action.
        await expect(
          row.getByRole('button', {
            name: copy('adminUi', 'invitations.copyLink'),
            exact: true,
          }),
        ).toHaveCount(0)
      })

      await test.step('step 7: a colleague opens the link, signs in and accepts', async () => {
        inviteeContext = await browser.newContext({
          locale: 'en-US',
          timezoneId: 'UTC',
          viewport: page.viewportSize(),
        })
        inviteePage = await inviteeContext.newPage()
        const invitee = inviteePage
        const invitationPath = new URL(acceptanceLink).pathname

        // REQ-tenancy-016: an anonymous viewer meets the invitation landing,
        // with the summary and the two account actions, not a generic sign-in
        // page.
        await invitee.goto(acceptanceLink)
        await expect(
          invitee.getByRole('heading', {
            level: 1,
            name: copy('authUi', 'acceptInvitation.title'),
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          invitee.getByText(
            copy('authUi', 'acceptInvitation.invitedBy', {
              inviter: e2eTenantAdmin.displayName,
              tenant: TENANT,
            }),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(
          invitee.getByRole('button', {
            name: copy('authUi', 'acceptInvitation.signIn'),
            exact: true,
          }),
        ).toBeVisible()
        expect(new URL(invitee.url()).pathname).toBe(invitationPath)

        // The sign-in action opens the host's surface in place.
        await invitee
          .getByRole('button', {
            name: copy('authUi', 'acceptInvitation.signIn'),
            exact: true,
          })
          .click()
        await expect(
          invitee.getByRole('heading', {
            level: 1,
            name: copy('demo', 'signIn.heading'),
            exact: true,
          }),
        ).toBeVisible()

        await invitee
          .getByRole('textbox', {
            name: copy('authUi', 'passwordSignIn.identifierLabel'),
            exact: true,
          })
          .fill(e2eInvitee.username)
        await invitee
          .getByRole('textbox', {
            name: copy('authUi', 'passwordSignIn.passwordLabel'),
            exact: true,
          })
          .fill(e2eInvitee.password)
        await invitee
          .getByRole('button', {
            name: copy('authUi', 'passwordSignIn.submit'),
            exact: true,
          })
          .click()

        // Signing in through the landing keeps the address; the summary names
        // the inviter, the tenant, the masked invited address and the expiry.
        await expect(
          invitee.getByRole('heading', {
            name: copy('authUi', 'acceptInvitation.title'),
            exact: true,
          }),
        ).toBeVisible()
        expect(new URL(invitee.url()).pathname).toBe(invitationPath)
        await expect(
          invitee.getByText(
            copy('authUi', 'acceptInvitation.invitedBy', {
              inviter: e2eTenantAdmin.displayName,
              tenant: TENANT,
            }),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(
          invitee.getByText(
            copy('authUi', 'acceptInvitation.email', {
              email: MASKED_INVITEE,
            }),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(
          invitee.getByText(
            copyPattern('authUi', 'acceptInvitation.expires', {
              expires: '.+',
            }),
          ),
        ).toBeVisible()

        // REQ-tenancy-021: the acceptance lands on the tenant home with the
        // invited tenant as the current one.
        await invitee
          .getByRole('button', {
            name: copy('authUi', 'acceptInvitation.accept'),
            exact: true,
          })
          .click()
        await expect(homeHeading(invitee)).toBeVisible()
        expect(new URL(invitee.url()).pathname).toBe('/')
        await expect(
          invitee.getByRole('button', {
            name: copy('demo', 'home.notice.dismiss'),
            exact: true,
          }),
        ).toBeVisible()
        // The account menu's organization row names the invited tenant as the
        // current one.
        await openAccountMenu(invitee)
        await expect(currentOrganizationRow(invitee, TENANT)).toBeVisible()
        await closeAccountMenu(invitee)
      })

      await test.step('step 8: the colleague is an ordinary member and sees no organization entry', async () => {
        if (inviteePage === undefined) {
          throw new Error('the colleague page was never opened')
        }
        const invitee = inviteePage

        // REQ-tenant-admin-001's ordinary-member facet: no permission covers a
        // organization page, so neither entry exists.
        await expectOrganizationEntryAbsent(invitee)

        // REQ-tenant-admin-004: a direct visit to the area root or a subpath
        // falls back to the tenant home, with no organization content and no
        // retryable failure page.
        for (const path of [organizationPath(''), organizationPath('/invitations')]) {
          await invitee.goto(`${server.url}${path}`)
          await expect(homeHeading(invitee)).toBeVisible()
          expect(new URL(invitee.url()).pathname).toBe('/')
          await expect(
            invitee.getByRole('heading', {
              level: 1,
              name: copy('adminUi', 'invitations.title'),
              exact: true,
            }),
          ).toHaveCount(0)
          await expect(
            invitee.getByRole('heading', {
              level: 1,
              name: copy('demo', 'organization.accessFailed.title'),
              exact: true,
            }),
          ).toHaveCount(0)
        }

        // REQ-tenancy-013: an accepted link cannot be redeemed again. The
        // current session is already a member of the invited tenant, so the
        // terminal state says so and offers the way into the organization.
        await invitee.goto(acceptanceLink)
        await expect(
          invitee.getByRole('heading', {
            name: copy('authUi', 'acceptInvitation.member.title', {
              tenant: TENANT,
            }),
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          invitee.getByRole('button', {
            name: copy('authUi', 'acceptInvitation.member.open'),
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          invitee.getByRole('button', {
            name: copy('authUi', 'acceptInvitation.accept'),
            exact: true,
          }),
        ).toHaveCount(0)
      })

      await test.step('step 9: the colleague is a member', async () => {
        // The page carries no refresh control: the browser reload is the
        // reader's own re-read, and the accepted state lands with it.
        await page.reload()
        // The page opens on pending invitations. The acceptance left no
        // pending invitation, so the list renders its filtered empty state
        // until the accepted filter is chosen below.
        await expect(
          invitationsSection(page).getByRole('heading', {
            name: copy('adminUi', 'invitations.empty.filteredTitle'),
            exact: true,
          }),
        ).toBeVisible()

        // The page opens on pending invitations; the accepted row is history,
        // reached through its filter.
        await chooseInvitationStatus(page, 'invitations.status.accepted')
        // The invitation reached its accepted terminal state.
        await expect
          .poll(() => invitationStatus(page, e2eInvitee.identifier))
          .toBe(copy('adminUi', 'invitations.status.accepted'))

        // The member table gained the colleague, active, role-less and
        // invitation-sourced.
        const table = membersSection(page).getByRole('table')
        const usernameColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.username'),
        )
        const rolesColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.roles'),
        )
        const statusColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.status'),
        )
        const sourceColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.source'),
        )
        const joined = memberRow(page, e2eInvitee.displayName)
        await expect(joined).toHaveCount(1)
        // The username column shows the record's login username. The member
        // panel's own notices name the account by display name and username,
        // which is where the composed reference comes from.
        await expect(cell(joined, usernameColumn)).toHaveText(
          e2eInvitee.username,
        )
        inviteeAccount = `${e2eInvitee.displayName} (${e2eInvitee.username})`
        await expect(cell(joined, sourceColumn)).toHaveText(
          copy('adminUi', 'members.source.invite'),
        )
        await expect(cell(joined, statusColumn)).toHaveText(
          copy('adminUi', 'members.status.active'),
        )
        await expect(cell(joined, rolesColumn)).toHaveText(
          copy('adminUi', 'members.roles.none'),
        )

        // The refreshed page read the new member's account id with the rest of
        // the roster; no section renders it.
        const ids = await accountIds.settled()
        await expectNoAccountIds(invitationsSection(page), ids)
        await expectNoAccountIds(membersSection(page), ids)
      })

      await test.step('step 10: the administrator creates a described role, grants and revokes a governed permission, and reads the usage', async () => {
        await openOrganizationPage(page, 'roles')
        const roles = rolesSection(page)
        const rolesTable = roles.getByRole('table')

        // REQ-tenant-admin-022: the tenant's own roles, with the builtin one
        // marked; the platform domain's role is absent.
        const builtinColumn = await columnIndex(
          rolesTable,
          copy('adminUi', 'permissions.roles.column.builtin'),
        )
        const descriptionColumn = await columnIndex(
          rolesTable,
          copy('adminUi', 'permissions.roles.column.description'),
        )
        const permissionsColumn = await columnIndex(
          rolesTable,
          copy('adminUi', 'permissions.roles.column.permissions'),
        )
        const membersColumn = await columnIndex(
          rolesTable,
          copy('adminUi', 'permissions.roles.column.members'),
        )
        const builtin = rolesTable.locator('tbody tr').filter({
          hasText: 'tenant-admin',
        })
        await expect(builtin).toHaveCount(1)
        await expect(cell(builtin, builtinColumn)).toHaveText(
          copy('adminUi', 'permissions.roles.builtinYes'),
        )
        await expect(
          rolesTable.getByText('platform-admin', { exact: true }),
        ).toHaveCount(0)

        // REQ-tenant-admin-026: the catalog reference starts collapsed; its
        // own action opens it, and only the governed permission set is
        // offered. The full governed set still renders, in groups, once the
        // reference is open.
        const catalog = await expandCatalogSection(page)
        for (const permission of GOVERNED_PERMISSIONS) {
          await expect(
            catalog.getByText(permission, { exact: true }),
          ).toBeVisible()
        }
        await expect(
          catalog.getByText(PLATFORM_PERMISSION, { exact: true }),
        ).toHaveCount(0)

        // REQ-tenant-admin-023: a non-builtin role with its free-text
        // description and no grants or members yet.
        await roles
          .getByLabel(copy('adminUi', 'permissions.roles.create.roleLabel'), {
            exact: true,
          })
          .fill(ROLE)
        await roles
          .getByLabel(
            copy('adminUi', 'permissions.roles.create.descriptionLabel'),
            { exact: true },
          )
          .fill(ROLE_DESCRIPTION)
        await roles
          .getByRole('button', {
            name: copy('adminUi', 'permissions.roles.create.submit'),
            exact: true,
          })
          .click()
        await expect(
          page.getByText(
            copy('adminUi', 'permissions.roles.saved', { role: ROLE }),
            { exact: true },
          ),
        ).toBeVisible()
        const roleRow = rolesTable.locator('tbody tr').filter({ hasText: ROLE })
        await expect(roleRow).toHaveCount(1)
        await expect(cell(roleRow, builtinColumn)).toHaveText(
          copy('adminUi', 'permissions.roles.builtinNo'),
        )
        // The row carries what the operator wrote and the two counts the
        // server reports: no grants, no active member yet.
        await expect(cell(roleRow, descriptionColumn)).toHaveText(
          ROLE_DESCRIPTION,
        )
        await expect(cell(roleRow, permissionsColumn)).toHaveText('0')
        await expect(cell(roleRow, membersColumn)).toHaveText('0')

        // REQ-tenant-admin-027: the administrator holds every governed
        // permission, so the grant lands. `authn.membership.manage` is
        // high-risk, so the page asks for the deliberate second click.
        await roleRow
          .getByRole('button', {
            name: copy('adminUi', 'permissions.roles.actionsAria', {
              role: ROLE,
            }),
            exact: true,
          })
          .click()
        await page
          .getByRole('menuitem', {
            name: copy('adminUi', 'permissions.roles.manageGrants'),
            exact: true,
          })
          .click()
        const grants = grantsSection(page)
        await expect(
          grants.getByText(
            copy('adminUi', 'permissions.grants.forRole', { role: ROLE }),
            { exact: true },
          ),
        ).toBeVisible()
        await chooseSelectOption(
          grants.getByRole('combobox', {
            name: copy('adminUi', 'permissions.grants.permissionLabel'),
            exact: true,
          }),
          page.getByRole('option', {
            name: new RegExp(
              `^${escapeRegExp(permissionDescription(PERMISSION))}`,
            ),
          }),
          PERMISSION,
        )
        await grants
          .getByRole('button', {
            name: copy('adminUi', 'permissions.grants.add'),
            exact: true,
          })
          .click()
        const dangerousDialog = page.getByRole('dialog', {
          name: copy('adminUi', 'permissions.grants.dangerousTitle'),
          exact: true,
        })
        await expect(dangerousDialog).toBeVisible()
        await dangerousDialog
          .getByRole('button', {
            name: copy('adminUi', 'permissions.grants.dangerousConfirm'),
            exact: true,
          })
          .click()
        const armAgain = dangerousDialog.getByRole('button', {
          name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
          exact: true,
        })
        await expect(armAgain).toBeEnabled()
        await armAgain.click()
        await expect(
          page.getByText(
            copy('adminUi', 'permissions.grants.granted', {
              permission: PERMISSION,
              role: ROLE,
            }),
            { exact: true },
          ),
        ).toBeVisible()

        // The grant row leads with the catalog's readable name, keeps the
        // code as secondary text, marks the risk and offers revoke by the
        // server-reported permission code.
        const grantRow = grants
          .getByRole('table')
          .locator('tbody tr')
          .filter({ hasText: PERMISSION })
        await expect(grantRow).toHaveCount(1)
        await expect(
          grantRow.getByText(permissionDescription(PERMISSION), {
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          grantRow.getByText(PERMISSION, { exact: true }),
        ).toBeVisible()
        await expect(
          grantRow.getByText(copy('adminUi', 'permissions.catalog.dangerous'), {
            exact: true,
          }),
        ).toBeVisible()
        const revoke = grantRow.getByRole('button', {
          name: copy('adminUi', 'permissions.grants.revokeAria', {
            permission: PERMISSION,
          }),
          exact: true,
        })
        await expect(revoke).toBeEnabled()
        await revoke.click()
        await expect(
          page.getByText(
            copy('adminUi', 'permissions.grants.revoked', {
              permission: PERMISSION,
              role: ROLE,
            }),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(
          grants.getByRole('table').getByText(PERMISSION, { exact: true }),
        ).toHaveCount(0)

        // REQ-tenant-admin-041: the builtin administrator's usage is
        // read-only. The page reports the active member count and the holder,
        // and renders no binding or unbinding control of any kind.
        await builtin
          .getByRole('button', {
            name: copy('adminUi', 'permissions.roles.viewGrantsAria', {
              role: 'tenant-admin',
            }),
            exact: true,
          })
          .click()
        const usage = usageSection(page)
        await expect(
          usage.getByText(
            copy('adminUi', 'permissions.usage.memberCount', { count: 1 }),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(
          usage.getByText(
            `${e2eTenantAdmin.displayName} (${e2eTenantAdmin.username})`,
            { exact: true },
          ),
        ).toBeVisible()
        await expect(usage.getByRole('button', { name: /bind/i })).toHaveCount(
          0,
        )
        await expect(usage.getByRole('combobox')).toHaveCount(0)
      })

      await test.step('step 11: the member panel binds, unbinds and rebinds the role', async () => {
        // The binding belongs to the member, so the journey drives it from the
        // member's management panel rather than the roles page.
        await openOrganizationPage(page, 'invitations')

        const chooseRole = async (panel: Locator): Promise<void> => {
          await chooseSelectOption(
            panel.getByRole('combobox', {
              name: copy('adminUi', 'members.panel.roleLabel'),
              exact: true,
            }),
            page.getByRole('option', { name: ROLE, exact: true }),
          )
        }
        const bindRole = async (panel: Locator): Promise<void> => {
          await panel
            .getByRole('button', {
              name: copy('adminUi', 'members.panel.bind'),
              exact: true,
            })
            .click()
        }

        const panel = await openMemberPanel(page, e2eInvitee.displayName)
        // REQ-tenant-admin-051: the panel lists the member's current bindings;
        // the joined member holds none yet.
        await expect(
          panel.getByText(copy('adminUi', 'members.panel.rolesEmpty'), {
            exact: true,
          }),
        ).toBeVisible()

        // The panel names the member by display name and username; the account
        // id its row record carries appears nowhere in the dialog, text or
        // attributes.
        const ids = await accountIds.settled()
        await expectNoAccountIds(panel, ids)

        // REQ-tenant-admin-030 / REQ-tenant-admin-051: bind the role.
        await chooseRole(panel)
        await bindRole(panel)
        await expect(
          panel.getByText(
            copy('adminUi', 'members.panel.bindSaved', {
              account: e2eInvitee.displayName,
              role: ROLE,
            }),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(bindingRow(panel, ROLE)).toHaveCount(1)

        // REQ-tenant-admin-031: unbind asks first, then removes it from the
        // list. The confirmation names the role whose permissions stop
        // applying at once and the two server-side refusals it cannot show.
        await bindingRow(panel, ROLE)
          .getByRole('button', {
            name: copy('adminUi', 'members.panel.unbind'),
            exact: true,
          })
          .click()
        const unbindDialog = page.getByRole('dialog', {
          name: copy('adminUi', 'members.panel.unbindTitle'),
          exact: true,
        })
        await expect(unbindDialog).toBeVisible()
        await expect(
          unbindDialog.getByText(
            copy('adminUi', 'members.panel.unbindMessage', { role: ROLE }),
            { exact: true },
          ),
        ).toBeVisible()
        await unbindDialog
          .getByRole('button', {
            name: copy('adminUi', 'common.confirm'),
            exact: true,
          })
          .click()
        await expect(
          panel.getByText(
            copy('adminUi', 'members.panel.bindRemoved', {
              account: e2eInvitee.displayName,
              role: ROLE,
            }),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(bindingRow(panel, ROLE)).toHaveCount(0)
        await expect(
          panel.getByText(copy('adminUi', 'members.panel.rolesEmpty'), {
            exact: true,
          }),
        ).toBeVisible()

        // The rebind restores it, and the closed list reflects the same read.
        await chooseRole(panel)
        await bindRole(panel)
        await expect(bindingRow(panel, ROLE)).toHaveCount(1)
        await closeMemberPanel(page)

        const table = membersSection(page).getByRole('table')
        const rolesColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.roles'),
        )
        await expect
          .poll(
            async () =>
              (
                await cell(
                  memberRow(page, e2eInvitee.displayName),
                  rolesColumn,
                ).innerText()
              ).trim(),
          )
          .toBe(ROLE)
      })

      await test.step('step 12: the administrator suspends and resumes the member, keeping the binding', async () => {
        const table = membersSection(page).getByRole('table')
        const statusColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.status'),
        )

        const panel = await openMemberPanel(page, e2eInvitee.displayName)
        // REQ-tenant-admin-055: suspend asks first and the message says the
        // access is paused while the bindings are kept and resumable.
        await panel
          .getByRole('button', {
            name: copy('adminUi', 'members.panel.suspend'),
            exact: true,
          })
          .click()
        const suspendDialog = page.getByRole('dialog', {
          name: copy('adminUi', 'members.suspendTitle'),
          exact: true,
        })
        await expect(suspendDialog).toBeVisible()
        await expect(
          suspendDialog.getByText(
            copy('adminUi', 'members.suspendMessage', {
              name: e2eInvitee.displayName,
            }),
            { exact: true },
          ),
        ).toBeVisible()
        await suspendDialog
          .getByRole('button', {
            name: copy('adminUi', 'common.confirm'),
            exact: true,
          })
          .click()

        await expect(
          panel.getByText(
            copy('adminUi', 'members.suspended', { account: inviteeAccount }),
            { exact: true },
          ),
        ).toBeVisible()
        // The open panel follows the list read: suspended, bindings kept, and
        // no new binding until the member is resumed.
        await expect(
          panel.getByText(copy('adminUi', 'members.status.suspended'), {
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          panel.getByText(copy('adminUi', 'members.panel.suspendedBindNote'), {
            exact: true,
          }),
        ).toBeVisible()
        await expect(bindingRow(panel, ROLE)).toHaveCount(1)
        await expect(
          panel.getByRole('button', {
            name: copy('adminUi', 'members.panel.bind'),
            exact: true,
          }),
        ).toBeDisabled()

        await closeMemberPanel(page)
        await expect(
          cell(memberRow(page, e2eInvitee.displayName), statusColumn),
        ).toHaveText(copy('adminUi', 'members.status.suspended'))

        // Resume restores the membership and therefore the role's effect.
        const resumed = await openMemberPanel(page, e2eInvitee.displayName)
        await resumed
          .getByRole('button', {
            name: copy('adminUi', 'members.panel.resume'),
            exact: true,
          })
          .click()
        const resumeDialog = page.getByRole('dialog', {
          name: copy('adminUi', 'members.resumeTitle'),
          exact: true,
        })
        await expect(resumeDialog).toBeVisible()
        await resumeDialog
          .getByRole('button', {
            name: copy('adminUi', 'common.confirm'),
            exact: true,
          })
          .click()
        await expect(
          resumed.getByText(
            copy('adminUi', 'members.resumed', { account: inviteeAccount }),
            { exact: true },
          ),
        ).toBeVisible()
        await closeMemberPanel(page)
        await expect(
          cell(memberRow(page, e2eInvitee.displayName), statusColumn),
        ).toHaveText(copy('adminUi', 'members.status.active'))
      })

      await test.step('step 13: the administrator cannot suspend itself and cannot unbind its own tenant-admin', async () => {
        const panel = await openMemberPanel(page, e2eTenantAdmin.displayName)

        // REQ-tenant-admin-056: the control is disabled before any request,
        // and the panel says why.
        await expect(
          panel.getByRole('button', {
            name: copy('adminUi', 'members.panel.suspend'),
            exact: true,
          }),
        ).toBeDisabled()
        await expect(
          panel.getByText(copy('adminUi', 'members.panel.cannotSuspendSelf'), {
            exact: true,
          }),
        ).toBeVisible()

        // REQ-tenant-admin-046: the one `tenant-admin` binding is the
        // administrator's own; the panel disables it before any request and
        // says why. The server refusal behind it is the Go tier's claim.
        const own = bindingRow(panel, 'tenant-admin')
        await expect(own).toHaveCount(1)
        await expect(
          own.getByRole('button', {
            name: copy('adminUi', 'members.panel.unbind'),
            exact: true,
          }),
        ).toBeDisabled()
        await expect(
          own.getByText(copy('adminUi', 'members.panel.cannotUnbindOwnAdmin'), {
            exact: true,
          }),
        ).toBeVisible()

        await closeMemberPanel(page)
      })

      await test.step('step 14: removing the member clears them from the list after the double confirmation', async () => {
        const panel = await openMemberPanel(page, e2eInvitee.displayName)
        // REQ-tenant-admin-057: the message names the role bindings the
        // removal clears and the reversible alternative.
        await panel
          .getByRole('button', {
            name: copy('adminUi', 'members.remove'),
            exact: true,
          })
          .click()
        const dialog = page.getByRole('dialog', {
          name: copy('adminUi', 'members.removeTitle'),
          exact: true,
        })
        await expect(dialog).toBeVisible()
        await expect(
          dialog.getByText(
            copy('adminUi', 'members.removeMessage', {
              name: e2eInvitee.displayName,
            }),
            { exact: true },
          ),
        ).toBeVisible()

        await dialog
          .getByRole('button', {
            name: copy('adminUi', 'common.confirm'),
            exact: true,
          })
          .click()
        const again = dialog.getByRole('button', {
          name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
          exact: true,
        })
        await expect(again).toBeEnabled()
        await again.click()

        await expect(panel).toHaveCount(0)
        await expect(
          page.getByText(
            copy('adminUi', 'members.removed', { account: inviteeAccount }),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(memberRow(page, e2eInvitee.displayName)).toHaveCount(0)
      })

      await test.step('step 15: the platform-admin role write is refused and the catalog never offers it', async () => {
        await openOrganizationPage(page, 'roles')
        const roles = rolesSection(page)
        const rolesTable = roles.getByRole('table')

        // REQ-tenant-admin-034, role creation: the reserved name is refused
        // and the catalog does not gain a row. The binding half of
        // REQ-tenant-admin-048 is not reachable here any more: the member
        // panel's role list is the tenant's own roles, which never include
        // `platform-admin`.
        await roles
          .getByLabel(copy('adminUi', 'permissions.roles.create.roleLabel'), {
            exact: true,
          })
          .fill('platform-admin')
        await roles
          .getByRole('button', {
            name: copy('adminUi', 'permissions.roles.create.submit'),
            exact: true,
          })
          .click()
        await expect(
          roles.getByRole('alert').getByText(protectedText(), { exact: true }),
        ).toBeVisible()
        await expect(
          rolesTable.locator('tbody tr').filter({ hasText: 'platform-admin' }),
        ).toHaveCount(0)

        // The governed catalog is the grant source, so the page cannot even
        // name the platform role as a grant target. The grants section renders
        // for a chosen role, so select the role the journey created.
        await rolesTable
          .locator('tbody tr')
          .filter({ hasText: ROLE })
          .getByRole('button', {
            name: copy('adminUi', 'permissions.roles.actionsAria', {
              role: ROLE,
            }),
            exact: true,
          })
          .click()
        await page
          .getByRole('menuitem', {
            name: copy('adminUi', 'permissions.roles.manageGrants'),
            exact: true,
          })
          .click()
        await expect(grantsSection(page)).toBeVisible()
        await grantsSection(page)
          .getByRole('combobox', {
            name: copy('adminUi', 'permissions.grants.permissionLabel'),
            exact: true,
          })
          .click()
        await expect(
          page.getByRole('option', { name: /platform-admin/ }),
        ).toHaveCount(0)
        await page.keyboard.press('Escape')
      })

      await test.step('step 16: the administrator issues a role-bearing invitation, resends it, and the status filter narrows the list', async () => {
        await openOrganizationPage(page, 'invitations')

        // REQ-tenant-admin-060: issue a second invitation the journey can
        // resend and revoke without disturbing the accepted one.
        await page
          .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
            exact: true,
          })
          .fill(RESENT_INVITEE)
        // REQ-tenant-admin-009: the caller holds the tenant's role-management
        // permission, so the issue form offers the tenant's own roles. The
        // role created in step 10 is the one chosen; the builtin tenant-admin
        // role is left alone, so the tenant keeps its single administrator.
        await chooseSelectOption(
          page.getByRole('combobox', {
            name: copy('adminUi', 'invitations.issue.roleLabel'),
            exact: true,
          }),
          page.getByRole('option', { name: ROLE, exact: true }),
        )
        await page
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issue.submit'),
            exact: true,
          })
          .click()
        const firstPanel = invitationLinkPanel(page, RESENT_INVITEE)
        await expect(firstPanel).toBeVisible()
        const firstLink = (
          (await firstPanel
            .getByText(new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`))
            .textContent()) ?? ''
        ).trim()
        expect(firstLink).toMatch(
          new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
        )
        await firstPanel
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issued.closeAria', {
              tenant: TENANT,
              email: RESENT_INVITEE,
            }),
            exact: true,
          })
          .click()
        await page
          .getByRole('dialog', {
            name: copy('adminUi', 'invitations.issued.closeTitle'),
            exact: true,
          })
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issued.close'),
            exact: true,
          })
          .click()
        await expect(firstPanel).toHaveCount(0)

        // REQ-tenant-admin-009: the issued row carries the role the form
        // handed it in its role column. The table is re-read when the panel
        // closes, so the row is awaited before its header is located.
        const issuedRow = invitationRow(page, RESENT_INVITEE)
        await expect(issuedRow).toHaveCount(1)
        const roleColumn = await columnIndex(
          invitationsSection(page).getByRole('table'),
          copy('adminUi', 'invitations.column.role'),
        )
        await expect(cell(issuedRow, roleColumn)).toHaveText(ROLE)

        // Resend asks first, states the unchanged deadline, and replaces the
        // one-time link; the log carrier delivers nothing, so the panel is
        // the only place the new link appears.
        const resentRow = invitationRow(page, RESENT_INVITEE)
        await resentRow
          .getByRole('button', {
            name: copy('adminUi', 'invitations.actionsAria', {
              email: RESENT_INVITEE,
            }),
            exact: true,
          })
          .click()
        await page
          .getByRole('menuitem', {
            name: copy('adminUi', 'invitations.resend'),
            exact: true,
          })
          .click()
        const resendDialog = page.getByRole('dialog', {
          name: copy('adminUi', 'invitations.resendTitle'),
          exact: true,
        })
        await expect(resendDialog).toBeVisible()
        // The confirmation names the deadline that will not change, not a
        // validity the resend would grant.
        await expect(
          resendDialog.getByText(
            copyPattern('adminUi', 'invitations.resendMessage', {
              email: escapeRegExp(RESENT_INVITEE),
              time: '.+',
            }),
          ),
        ).toBeVisible()
        await resendDialog
          .getByRole('button', {
            name: copy('adminUi', 'common.confirm'),
            exact: true,
          })
          .click()
        await expect(
          page.getByText(
            copy('adminUi', 'invitations.resent', { email: RESENT_INVITEE }),
            { exact: true },
          ),
        ).toBeVisible()
        const secondPanel = invitationLinkPanel(page, RESENT_INVITEE)
        await expect(secondPanel).toBeVisible()
        const secondLink = (
          (await secondPanel
            .getByText(new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`))
            .textContent()) ?? ''
        ).trim()
        expect(secondLink).not.toBe(firstLink)
        expect(secondLink.startsWith(`${server.url}/invitations/`)).toBe(true)

        await secondPanel
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issued.closeAria', {
              tenant: TENANT,
              email: RESENT_INVITEE,
            }),
            exact: true,
          })
          .click()
        await page
          .getByRole('dialog', {
            name: copy('adminUi', 'invitations.issued.closeTitle'),
            exact: true,
          })
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issued.close'),
            exact: true,
          })
          .click()
        await expect(secondPanel).toHaveCount(0)

        // Resend replaced the token on the same invitation: no second row
        // appeared, and the row now says it was resent once with the latest
        // send's instant.
        await expect(invitationRow(page, RESENT_INVITEE)).toHaveCount(1)
        await expect(
          invitationRow(page, RESENT_INVITEE).getByText(
            copyPattern('adminUi', 'invitations.created.resentOnce', {
              time: '.+',
            }),
          ),
        ).toBeVisible()
        // A resend keeps the invitation's role unchanged.
        await expect(
          cell(invitationRow(page, RESENT_INVITEE), roleColumn),
        ).toHaveText(ROLE)

        // REQ-tenancy-081: the invitation grants its role on acceptance. A new
        // address registers through the link, proves the address, accepts, and
        // the member table then names the role beside the new member.
        await page
          .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
            exact: true,
          })
          .fill(ROLE_INVITEE)
        await chooseSelectOption(
          page.getByRole('combobox', {
            name: copy('adminUi', 'invitations.issue.roleLabel'),
            exact: true,
          }),
          page.getByRole('option', { name: ROLE, exact: true }),
        )
        await page
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issue.submit'),
            exact: true,
          })
          .click()
        const rolePanel = invitationLinkPanel(page, ROLE_INVITEE)
        await expect(rolePanel).toBeVisible()
        const roleLink = (
          (await rolePanel
            .getByText(new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`))
            .textContent()) ?? ''
        ).trim()
        await rolePanel
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issued.closeAria', {
              tenant: TENANT,
              email: ROLE_INVITEE,
            }),
            exact: true,
          })
          .click()
        await page
          .getByRole('dialog', {
            name: copy('adminUi', 'invitations.issued.closeTitle'),
            exact: true,
          })
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issued.close'),
            exact: true,
          })
          .click()
        await expect(rolePanel).toHaveCount(0)

        const roleContext = await browser.newContext({
          locale: 'en-US',
          timezoneId: 'UTC',
          viewport: page.viewportSize(),
        })
        try {
          const roleInvitee = await roleContext.newPage()
          await registerAndAcceptThroughLink(roleInvitee, roleLink, server)
        } finally {
          await roleContext.close()
        }

        // The acceptance wrote the role beside the new membership, read back
        // after a reload because the roster is the admin page's own read.
        await page.reload()
        const memberTable = membersSection(page).getByRole('table')
        const roleMember = memberTable
          .locator('tbody tr')
          .filter({ hasText: ROLE_INVITEE_IDENTITY.username })
        await expect(roleMember).toHaveCount(1)
        const memberRolesColumn = await columnIndex(
          memberTable,
          copy('adminUi', 'members.column.roles'),
        )
        await expect(cell(roleMember, memberRolesColumn)).toHaveText(ROLE)
        await chooseInvitationStatus(page, 'invitations.filter.all')

        // REQ-tenant-admin-058: each filter narrows the server read.
        await chooseInvitationStatus(page, 'invitations.status.accepted')
        await expect(invitationRow(page, e2eInvitee.identifier)).toHaveCount(1)
        await expect(invitationRow(page, RESENT_INVITEE)).toHaveCount(0)

        await chooseInvitationStatus(page, 'invitations.status.pending')
        await expect(invitationRow(page, RESENT_INVITEE)).toHaveCount(1)
        await expect(invitationRow(page, e2eInvitee.identifier)).toHaveCount(0)

        // Nothing has been revoked or expired yet, so each of those filters
        // names its own empty result rather than reporting an empty
        // organization.
        for (const status of [
          'invitations.status.revoked',
          'invitations.status.expired',
        ] as const) {
          await chooseInvitationStatus(page, status)
          await expect(
            invitationsSection(page).getByText(
              copy('adminUi', 'invitations.empty.filteredDescription', {
                status: copy('adminUi', status),
              }),
              { exact: true },
            ),
          ).toBeVisible()
        }

        // REQ-tenant-admin-010: revoking the pending invitation is a terminal
        // state the revoked filter then lists, with no actions left on it.
        await chooseInvitationStatus(page, 'invitations.status.pending')
        await invitationRow(page, RESENT_INVITEE)
          .filter({
            hasText: copy('adminUi', 'invitations.status.pending'),
          })
          .getByRole('button', {
            name: copy('adminUi', 'invitations.actionsAria', {
              email: RESENT_INVITEE,
            }),
            exact: true,
          })
          .click()
        await page
          .getByRole('menuitem', {
            name: copy('adminUi', 'invitations.revoke'),
            exact: true,
          })
          .click()
        const revokeDialog = page.getByRole('dialog', {
          name: copy('adminUi', 'invitations.revokeTitle'),
          exact: true,
        })
        await expect(revokeDialog).toBeVisible()
        await revokeDialog
          .getByRole('button', {
            name: copy('adminUi', 'common.confirm'),
            exact: true,
          })
          .click()
        await expect(
          page.getByText(
            copy('adminUi', 'invitations.revoked', { email: RESENT_INVITEE }),
            { exact: true },
          ),
        ).toBeVisible()

        await chooseInvitationStatus(page, 'invitations.status.revoked')
        const revokedRow = invitationRow(page, RESENT_INVITEE)
        await expect(revokedRow).toHaveCount(1)
        await expect(
          revokedRow.getByText(copy('adminUi', 'invitations.status.revoked'), {
            exact: true,
          }),
        ).toBeVisible()
        // A terminal invitation has nothing left to act on.
        await expect(
          revokedRow.getByRole('button', {
            name: copy('adminUi', 'invitations.actionsAria', {
              email: RESENT_INVITEE,
            }),
            exact: true,
          }),
        ).toHaveCount(0)
        await chooseInvitationStatus(page, 'invitations.filter.all')
      })

      await test.step('step 17: the settings page is the two sign-in policies and nothing else', async () => {
        await openOrganizationPage(page, 'settings')

        // REQ-tenant-admin-016: the organization surface manages exactly the
        // two declared admission policies. It is not the key/value register:
        // there is no table, no namespace navigation, no search and no batch
        // save bar, and no platform-level setting name is rendered.
        await expect(
          page.getByRole('heading', {
            level: 1,
            name: copy('adminUi', 'organization.nav.settings'),
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          page.getByRole('heading', {
            level: 2,
            name: copy('adminUi', 'organization.security.section.title'),
            exact: true,
          }),
        ).toBeVisible()

        const policies = [
          copy('adminUi', 'organization.security.mfa.title'),
          copy('adminUi', 'organization.security.sso.title'),
        ]
        for (const title of policies) {
          await expect(
            page.getByRole('heading', {
              level: 3,
              name: title,
              exact: true,
            }),
          ).toBeVisible()
        }

        await expect(page.getByRole('switch')).toHaveCount(2)
        for (const title of policies) {
          const control = page.getByRole('switch', {
            name: copy('adminUi', 'organization.security.switchAria', {
              title,
            }),
            exact: true,
          })
          await expect(control).toBeVisible()
          await expect(control).not.toBeChecked()
        }
        await expect(
          page.getByText(
            copy('adminUi', 'organization.security.status.disabled'),
            { exact: true },
          ),
        ).toHaveCount(2)

        await expect(page.getByRole('table')).toHaveCount(0)
        await expect(
          page.getByRole('navigation', {
            name: copy('adminUi', 'settings.nav.label'),
            exact: true,
          }),
        ).toHaveCount(0)
        await expect(
          page.getByRole('button', {
            name: copy('adminUi', 'settings.saveBar.save'),
            exact: true,
          }),
        ).toHaveCount(0)
        for (const namespace of e2eTenantPolicies.platformOnlyNamespaces) {
          await expect(page.getByText(namespace, { exact: false })).toHaveCount(0)
        }

        // REQ-tenant-admin-018 / REQ-tenant-admin-019 / REQ-tenant-admin-062:
        // the platform settings are invisible from this surface. A platform
        // operator lists every declared platform setting; the plan's fifteen
        // platform keys must all be in that dynamic listing, so an empty
        // listing (or one that lost a declaration) fails here. The tenant
        // administrator's own session then reads the full listing and
        // addresses each platform item: the read returns exactly the two
        // tenant keys, and every write and delete answers the bare 404 an
        // undeclared key gets. A namespace read has no tenant route at all,
        // so the router answers 404 or 405.
        //
        // This server does not run the impersonation fixture, so the seed
        // writes the operator's address as an administrator's assertion it
        // does not prove: the address is a contact, not a login way. The
        // username beside it is the login name, which is what the API sign-in
        // takes here.
        const platformSettings = await server.readPlatformSettings({
          identifier: OPERATOR.username,
          password: OPERATOR.password,
        })
        expect(platformSettings.status).toBe(200)
        const platformItems = platformSettings.items.filter(
          (item) =>
            !(e2eTenantPolicies.keys as readonly string[]).includes(
              item.key,
            ),
        )
        const platformKeys = new Set(platformItems.map((item) => item.key))
        for (const key of e2eTenantPolicies.platformKeys) {
          expect(
            platformKeys.has(key),
            `the platform listing is missing ${key}`,
          ).toBe(true)
        }

        const probe = await server.probeTenantSettings({
          tenant: TENANT,
          identifier: e2eTenantAdmin.identifier,
          password: e2eTenantAdmin.password,
          items: platformItems,
          namespaces: [...new Set(platformItems.map((item) => item.namespace))],
        })
        expect(probe.read.status).toBe(200)
        expect(probe.read.items.map((item) => item.key).sort()).toEqual(
          [...e2eTenantPolicies.keys].sort(),
        )
        for (const item of platformItems) {
          expect(probe.writes[item.key]?.status, `PUT ${item.key}`).toBe(404)
          expect(probe.deletes[item.key]?.status, `DELETE ${item.key}`).toBe(
            404,
          )
        }
        for (const [namespace, status] of Object.entries(
          probe.namespaceReads,
        )) {
          expect([404, 405], `GET namespace ${namespace}`).toContain(status)
        }

        // This journey leaves both policies off. Turning one on would require
        // it of the single-factor review administrator from the next request
        // onward -- the write itself passes, the read after it is challenged --
        // and every later step would meet that challenge. The toggle itself is
        // driven by `tenant-admin: an administrator turns a policy on, reads
        // it back, and turns it off`, which signs in the multi-factor operator;
        // the optimistic save, the confirmation and the rollback are also
        // driven in the page's unit tests.
      })

      await test.step('step 18: a personal-tenant switch retracts the entry and a direct visit returns home', async () => {
        // A personal organization is named after the account it belongs to, so
        // the switcher row carries the administrator's own display name.
        const personal = e2eTenantAdmin.displayName

        // REQ-tenant-admin-003/006: the switch retracts the entry, the account
        // menu's organization group and the card at once.
        await switchOrganization(page, TENANT, personal)
        await expect(homeHeading(page)).toBeVisible()
        await expectOrganizationEntryAbsent(page)

        // A direct visit while the personal tenant is current: history
        // navigation leaves the module's current tenant in place, so the
        // organization URL is reached with the personal tenant still current.
        // The full-document load a fresh address would perform re-settles the
        // account's non-personal membership, so it cannot exercise this
        // branch; the history visit reaches the same guard.
        await page.goBack()
        await expect(homeHeading(page)).toBeVisible()
        expect(new URL(page.url()).pathname).toBe('/')
        await expectOrganizationEntryAbsent(page)
        await expect(
          page.getByRole('heading', {
            level: 1,
            name: copy('adminUi', 'organization.nav.settings'),
            exact: true,
          }),
        ).toHaveCount(0)

        // Switching back restores the entry once the new permission set
        // settles.
        await switchOrganization(page, personal, TENANT)
        await expect(homeHeading(page)).toBeVisible()
        await expectOrganizationEntryPresent(page)
      })
    } finally {
      // The colleague's context holds its own cookie session; closing it keeps
      // a failed run from leaving a second context open while the server
      // drains.
      await inviteeContext?.close().catch(() => undefined)
    }
  })
})

test.describe('the organization SSO page', () => {
  test.use({
    seedTenantAdmin: true,
    enterpriseSSO: true,
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: the organization registers, verifies, disables and removes an SSO provider', async ({
    demo,
  }) => {
    // The branch signs in and out several times to observe discovery after
    // each provider state, so it needs more than the tier's default bound.
    test.setTimeout(scaledTimeout(300_000))

    const { page, server, accounts } = demo
    const enterprise = server.enterpriseIdentityProvider
    const stub = server.dnsStub
    if (enterprise === undefined) {
      throw new Error(
        'the enterprise fixture did not start the identity provider this spec configures',
      )
    }
    if (stub === undefined) {
      throw new Error(
        'the enterprise fixture did not start the DNS stub this spec publishes into',
      )
    }

    /** Publish the token the panel hands out, the operator's act at the DNS. */
    const publishAndVerify = async (): Promise<void> => {
      const domains = domainsSection(page)
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
      await expect(
        panel.getByText(enterpriseIdentityProviderRecordName, {
          exact: true,
        }),
      ).toBeVisible()
      const recordValue = panel.getByText(/^speed-authn=[0-9a-f]{64}$/)
      await expect(recordValue).toBeVisible()
      const published = ((await recordValue.textContent()) ?? '').trim()
      stub.setTXT([published])

      const domainRow = domains
        .getByRole('listitem')
        .filter({ hasText: enterpriseIdentityProviderDomain })
      await expect(domainRow).toHaveCount(1)
      await expect(
        domainRow.getByText(copy('adminUi', 'sso.domains.state.unverified'), {
          exact: true,
        }),
      ).toBeVisible()
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

    /** Set the provider's enabled flag through its edit form. */
    const setProviderEnabled = async (enabled: boolean): Promise<void> => {
      const form = providerFormSection(page)
      await chooseSelectOption(
        form.getByRole('combobox', {
          name: copy('adminUi', 'sso.provider.editLabel'),
          exact: true,
        }),
        page.getByRole('option', { name: PROVIDER_NAME, exact: true }),
      )
      await expect(
        form.getByLabel(copy('adminUi', 'sso.provider.nameLabel'), {
          exact: true,
        }),
      ).toHaveValue(PROVIDER_NAME)
      // The provider read does not carry the write-only configuration, and
      // the write validates it, so the operator restates it when saving a
      // change.
      await form
        .getByLabel(copy('adminUi', 'sso.provider.configLabel'), {
          exact: true,
        })
        .fill(
          JSON.stringify({
            issuer: enterprise.issuer,
            client_id: enterprise.clientID,
            claims: enterpriseIdentityProviderClaims,
          }),
        )
      await chooseSelectOption(
        form.getByRole('combobox', {
          name: copy('adminUi', 'sso.provider.enabledLabel'),
          exact: true,
        }),
        page.getByRole('option', {
          name: copy(
            'adminUi',
            enabled ? 'sso.provider.tri.true' : 'sso.provider.tri.false',
          ),
          exact: true,
        }),
      )
      await form
        .getByRole('button', {
          name: copy('adminUi', 'sso.provider.updateSubmit'),
          exact: true,
        })
        .click()
      await expect(
        page.getByText(
          copy('adminUi', 'sso.provider.updated', { name: PROVIDER_NAME }),
          { exact: true },
        ),
      ).toBeVisible()
      const row = page
        .getByRole('table')
        .locator('tbody tr')
        .filter({ hasText: PROVIDER_NAME })
      await expect(
        row.getByText(
          copy(
            'adminUi',
            enabled ? 'sso.enabledYes' : 'sso.enabledNo',
          ),
          { exact: true },
        ),
      ).toBeVisible()
    }

    /** Look the provider up by the fixture user's address on the sign-in page. */
    const discover = async (expected: boolean): Promise<void> => {
      await accounts.signOut()
      await accounts.openSignIn()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('demo', 'signIn.heading'),
          exact: true,
        }),
      ).toBeVisible()
      await page
        .getByRole('textbox', {
          name: copy('authUi', 'enterprise.emailLabel'),
          exact: true,
        })
        .fill(enterpriseIdentityProviderUser.email)
      await page
        .getByRole('button', {
          name: copy('authUi', 'enterprise.continue'),
          exact: true,
        })
        .click()
      if (expected) {
        await expect(
          page.getByRole('button', { name: PROVIDER_NAME, exact: true }),
        ).toBeVisible()
      } else {
        await expect(
          page.getByText(copy('authUi', 'enterprise.noProviders'), {
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          page.getByRole('button', { name: PROVIDER_NAME, exact: true }),
        ).toHaveCount(0)
      }
    }

    const signInAdmin = async (): Promise<void> => {
      await accounts.signIn({
        identifier: e2eTenantAdmin.username,
        password: e2eTenantAdmin.password,
      })
      await expect(homeHeading(page)).toBeVisible()
    }

    await test.step('step 1: the organization SSO page starts empty for this tenant', async () => {
      await signInAdmin()
      await openOrganizationPage(page, 'sso')
      // REQ-tenant-admin-011: the list is the current tenant's and is empty.
      await expect(
        page.getByRole('heading', {
          name: copy('adminUi', 'sso.empty.title'),
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 2: the administrator registers an enabled OIDC provider', async () => {
      const form = providerFormSection(page)
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
        .fill('jit')
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
        .fill(
          JSON.stringify({
            issuer: enterprise.issuer,
            client_id: enterprise.clientID,
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

      // REQ-tenant-admin-012: the provider appears in this tenant's list with
      // its protocol and enabled state.
      const row = page
        .getByRole('table')
        .locator('tbody tr')
        .filter({ hasText: PROVIDER_NAME })
      await expect(row).toHaveCount(1)
      await expect(row.getByText('oidc', { exact: true })).toBeVisible()
      await expect(
        row.getByText(copy('adminUi', 'sso.enabledYes'), { exact: true }),
      ).toBeVisible()
    })

    await test.step('step 3: the administrator claims the email domain and verifies its DNS record', async () => {
      // REQ-tenant-admin-015: the claim hands out the token and the DNS
      // record; publishing it and verifying flips the row.
      const domains = domainsSection(page)
      await chooseSelectOption(
        domains.getByRole('combobox', {
          name: copy('adminUi', 'sso.domains.providerLabel'),
          exact: true,
        }),
        page.getByRole('option', { name: PROVIDER_NAME, exact: true }),
      )
      await domains
        .getByLabel(copy('adminUi', 'sso.domains.domainLabel'), {
          exact: true,
        })
        .fill(enterpriseIdentityProviderDomain)
      await domains
        .getByRole('button', {
          name: copy('adminUi', 'sso.domains.claim'),
          exact: true,
        })
        .click()
      await publishAndVerify()
    })

    await test.step('step 4: a disabled provider leaves sign-in discovery', async () => {
      // REQ-tenant-admin-013: the verified domain no longer resolves to the
      // provider while it is disabled.
      await setProviderEnabled(false)
      await discover(false)
    })

    await test.step('step 5: re-enabling restores discovery', async () => {
      await signInAdmin()
      await openOrganizationPage(page, 'sso')
      await setProviderEnabled(true)
      await discover(true)
    })

    await test.step('step 6: deleting the provider removes it and its domain from discovery', async () => {
      await signInAdmin()
      await openOrganizationPage(page, 'sso')
      await page
        .getByRole('table')
        .locator('tbody tr')
        .filter({ hasText: PROVIDER_NAME })
        .getByRole('button', {
          name: copy('adminUi', 'sso.actionsAria', { name: PROVIDER_NAME }),
          exact: true,
        })
        .click()
      await page
        .getByRole('menuitem', {
          name: copy('adminUi', 'sso.delete'),
          exact: true,
        })
        .click()
      await page
        .getByRole('dialog', {
          name: copy('adminUi', 'sso.deleteTitle'),
          exact: true,
        })
        .getByRole('button', {
          name: copy('adminUi', 'common.confirm'),
          exact: true,
        })
        .click()
      await expect(
        page.getByText(
          copy('adminUi', 'sso.deleted', { name: PROVIDER_NAME }),
          { exact: true },
        ),
      ).toBeVisible()
      // REQ-tenant-admin-014: it is gone from the list.
      await expect(
        page.getByRole('heading', {
          name: copy('adminUi', 'sso.empty.title'),
          exact: true,
        }),
      ).toBeVisible()
      // ... and its verified domain no longer participates in discovery.
      await discover(false)
    })
  })
})

test.describe('the system tenant is not manageable', () => {
  test.use({ seedSystemTenantAdmin: true, viewport: { width: 1920, height: 1080 } })

  test('tenant-admin: the system tenant shows no entry and every direct visit returns home', async ({
    demo,
  }) => {
    const { page, server, accounts } = demo

    // The account holds the system tenant's tenant-admin role and no platform
    // permission.
    await accounts.signIn({
      identifier: e2eSystemTenantAdmin.identifier,
      password: e2eSystemTenantAdmin.password,
    })
    await expect(homeHeading(page)).toBeVisible()

    // REQ-tenant-admin-006: the system tenant offers neither the entry nor the
    // card.
    await expectOrganizationEntryAbsent(page)

    for (const path of [
      organizationPath(''),
      organizationPath('/invitations'),
      organizationPath('/settings'),
    ]) {
      await page.goto(`${server.url}${path}`)
      await expect(homeHeading(page)).toBeVisible()
      expect(new URL(page.url()).pathname).toBe('/')
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'invitations.title'),
          exact: true,
        }),
      ).toHaveCount(0)
      await expectOrganizationEntryAbsent(page)
    }
  })
})

test.describe('an impersonated session hides the organization', () => {
  test.use({
    seedTenantAdmin: true,
    seedImpersonation: true,
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: impersonating the administrator renders no organization surface', async ({
    demo,
  }) => {
    test.setTimeout(scaledTimeout(300_000))

    const { page, server, accounts } = demo

    await test.step('precondition: the operator signs in and opens the impersonation page', async () => {
      await signInOperatorWithTotp(page, accounts)
      // The operator's current organization is the platform's own, so the
      // admin entry is offered from the account menu.
      await openAccountMenu(page)
      await adminEntry(page).click()
      await expect(adminNavigation(page)).toBeVisible()

      await adminNavigation(page)
        .getByRole('link', {
          name: copy('adminUi', 'impersonations.title'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'impersonations.title'),
          exact: true,
        }),
      ).toBeVisible()

      await chooseSelectOption(
        page.getByRole('combobox', {
          name: copy('adminUi', 'common.tenant'),
          exact: true,
        }),
        page.getByRole('option', { name: TENANT, exact: true }),
      )
      await chooseImpersonationTarget(page)
      await reasonField(page).fill(IMPERSONATION_REASON)
      await expect(startButton(page)).toBeEnabled()

      // The start is fresh enough to land here; a slow setup may pass the
      // freshness window, in which case the admin raises its step-up dialog.
      // The branch classifies the answer rather than presuming either.
      const dialog = stepUpDialog(page)
      await expect(async () => {
        if (await dialog.isVisible()) return
        if (await homeHeading(page).isVisible()) return
        await chooseImpersonationTarget(page)
        await reasonField(page).fill(IMPERSONATION_REASON)
        await startButton(page).click({ timeout: scaledTimeout(3_000) })
        throw new Error('the impersonation start has not answered yet')
      }).toPass({ timeout: scaledTimeout(60_000), intervals: [2_000] })

      if (await dialog.isVisible()) {
        // The sign-in used the authenticator factor, so the SMS factor is the
        // one the step-up offers. Its code is the review carrier's log line.
        const before = server.log.mark()
        await dialog
          .getByRole('button', {
            name: copy('adminUi', 'stepUp.factorKind.sms'),
            exact: true,
          })
          .click()
        const codeField = dialog.getByRole('textbox', {
          name: 'code',
          exact: true,
        })
        await expect(codeField).toBeVisible()
        const { code } = await waitForVerificationCode(
          server.log,
          'sms',
          OPERATOR_PHONE,
          { after: before },
        )
        await codeField.fill(code)
        await dialog
          .getByRole('button', {
            name: copy('adminUi', 'stepUp.submit'),
            exact: true,
          })
          .click()
        await expect(dialog).toHaveCount(0)
      }

      await expect(homeHeading(page)).toBeVisible()
      expect(new URL(page.url()).pathname).toBe('/')
      await expect(impersonationBanner(page)).toBeVisible()
    })

    await test.step('step 1: the entry and card are withheld while impersonating', async () => {
      // REQ-tenant-admin-042: the impersonated session is the target account,
      // but the surface is withheld regardless of its permissions.
      await expectOrganizationEntryAbsent(page)
    })

    await test.step('step 2: a direct visit comes back to the tenant home', async () => {
      for (const path of [
        organizationPath('/invitations'),
        organizationPath('/settings'),
      ]) {
        await page.goto(`${server.url}${path}`)
        await expect(homeHeading(page)).toBeVisible()
        expect(new URL(page.url()).pathname).toBe('/')
        await expect(
          page.getByRole('heading', {
            level: 1,
            name: copy('adminUi', 'organization.nav.settings'),
            exact: true,
          }),
        ).toHaveCount(0)
        await expect(impersonationBanner(page)).toBeVisible()
      }
    })

    await test.step('step 3: ending the impersonation drops the banner', async () => {
      await impersonationBanner(page)
        .getByRole('button', {
          name: copy('authUi', 'impersonation.end'),
          exact: true,
        })
        .click()
      await expect(impersonationBanner(page)).toHaveCount(0)
      await expect(
        page.getByText(copy('authUi', 'impersonation.ended'), { exact: true }),
      ).toBeVisible()
    })
  })
})

test.describe('an unproved email is refused until it is proved', () => {
  test.use({
    seedUnprovedTenantAdmin: true,
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: the unproved administrator is refused, proves the address and retries', async ({
    demo,
  }) => {
    const { page, server, accounts } = demo
    const roleName = 'Proof Pending'

    await accounts.signIn({
      identifier: e2eUnprovedTenantAdmin.username,
      password: e2eUnprovedTenantAdmin.password,
    })
    await expect(homeHeading(page)).toBeVisible()
    // The unproved address does not block admission; the entry still appears.
    await expectOrganizationEntryPresent(page)

    await openOrganizationPage(page, 'roles')

    const createRole = async (): Promise<void> => {
      const roles = rolesSection(page)
      await roles
        .getByLabel(copy('adminUi', 'permissions.roles.create.roleLabel'), {
          exact: true,
        })
        .fill(roleName)
      await roles
        .getByRole('button', {
          name: copy('adminUi', 'permissions.roles.create.submit'),
          exact: true,
        })
        .click()
    }

    await test.step('step 1: the organization write is refused with the contact-required text', async () => {
      // REQ-tenant-admin-043/050: the write is refused before it lands and the
      // page says to prove the address first.
      await createRole()
      await expect(
        rolesSection(page)
          .getByRole('alert')
          .getByText(copy('adminUi', 'errors.authn.contact_required'), {
            exact: true,
          }),
      ).toBeVisible()
      await expect(
        rolesSection(page)
          .getByRole('table')
          .locator('tbody tr')
          .filter({ hasText: roleName }),
      ).toHaveCount(0)
    })

    await test.step('step 2: the administrator proves the address from the standing notice', async () => {
      const banner = verificationBanner(
        page,
        e2eUnprovedTenantAdmin.identifier,
      )
      await expect(banner).toBeVisible()

      // The cursor is taken before the entry is followed: the verification
      // section sends its one code after the arrival, and only that code may
      // be used.
      const beforeCode = server.log.mark()
      await banner
        .getByRole('link', {
          name: copy('demo', 'verificationBanner.verify'),
          exact: true,
        })
        .click()

      const section = verificationSection(page)
      await expect(section).toBeVisible()
      const codeField = section.getByLabel(
        copy('accountUi', 'verification.codeLabel'),
        { exact: true },
      )
      const { code } = await waitForVerificationCode(
        server.log,
        'mail',
        e2eUnprovedTenantAdmin.identifier,
        { after: beforeCode },
      )
      await codeField.fill(code)
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

    await test.step('step 3: the same write lands once the address is proved', async () => {
      await mainNavigation(page)
        .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
        .click()
      await expect(homeHeading(page)).toBeVisible()
      await openOrganizationPage(page, 'roles')

      await createRole()
      await expect(
        page.getByText(
          copy('adminUi', 'permissions.roles.saved', { role: roleName }),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        rolesSection(page)
          .getByRole('table')
          .locator('tbody tr')
          .filter({ hasText: roleName }),
      ).toHaveCount(1)
    })
  })
})

test.describe('a policy the platform holds on', () => {
  test.use({
    // A platform requirement binds every tenant, so the tenant-scoped read
    // that opens the settings page needs a multi-factor session. The review
    // build seeds its operator's seasoned authenticator, so this branch names
    // the operator the tenant's administrator. The page under test is the
    // tenant settings surface; the account's platform role changes nothing on
    // it, and the tenant-admin binding is what admits the read.
    seedImpersonation: true,
    tenantAdmins: { acme: [OPERATOR.identifier] },
    platformRequireMFA: true,
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: a platform-enforced policy is read-only and cannot be turned off', async ({
    demo,
  }) => {
    test.setTimeout(scaledTimeout(300_000))

    const { page, server, accounts } = demo

    await signInOperatorWithTotp(page, accounts)
    await openOrganizationPage(page, 'settings')

    // The platform layer holds the MFA policy on, so the item reads as
    // enforced and its switch is an enabled, disabled control with no way to
    // turn it off. The SSO policy, which the platform does not hold, keeps its
    // ordinary switch.
    const mfaTitle = copy('adminUi', 'organization.security.mfa.title')
    const ssoTitle = copy('adminUi', 'organization.security.sso.title')
    for (const title of [mfaTitle, ssoTitle]) {
      await expect(
        page.getByRole('heading', { level: 3, name: title, exact: true }),
      ).toBeVisible()
    }
    await expect(page.getByRole('switch')).toHaveCount(2)

    const mfaSwitch = page.getByRole('switch', {
      name: copy('adminUi', 'organization.security.switchAria', {
        title: mfaTitle,
      }),
      exact: true,
    })
    await expect(mfaSwitch).toBeChecked()
    await expect(mfaSwitch).toBeDisabled()
    await expect(
      page.getByText(
        copy('adminUi', 'organization.security.status.platformEnforced'),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(
      page.getByText(copy('adminUi', 'organization.security.platformLocked'), {
        exact: true,
      }),
    ).toBeVisible()

    const ssoSwitch = page.getByRole('switch', {
      name: copy('adminUi', 'organization.security.switchAria', {
        title: ssoTitle,
      }),
      exact: true,
    })
    // The organization has registered no SSO provider, so the switch is held
    // off with the reason named: turning the requirement on without a
    // provider would lock the organization out of every request.
    await expect(ssoSwitch).not.toBeChecked()
    await expect(ssoSwitch).toBeDisabled()
    await expect(
      page.getByText(copy('adminUi', 'organization.security.sso.noProvider'), {
        exact: true,
      }),
    ).toBeVisible()

    // REQ-tenant-admin-018: the rendered state above promises that turning
    // the enforced policy off cannot land, and the disabled switch offers no
    // control to drive. The same administrator account makes that write
    // through the product's API, in its own authenticated session, and the
    // server refuses it as a conflict; the answer is the sibling evidence
    // for the read-only rendering, never a replacement for it.
    const refusal = await server.writeTenantSetting({
      tenant: 'acme',
      identifier: OPERATOR.identifier,
      password: OPERATOR.password,
      totpSecret: TOTP_SEED,
      namespace: 'tenant.admission',
      key: 'require-mfa',
      value: 'false',
    })
    expect(refusal.status).toBe(409)
    expect(refusal.problemType).toBe(
      'urn:speed:settings:tenant-admission-enforced',
    )
  })
})

test.describe('a policy the platform does not hold', () => {
  test.use({
    // A tenant policy applies at members' next sign-in, not to sessions that
    // already exist, so the session that carries the write is exempt from the
    // rule it switches on. The review build seeds the operator's seasoned
    // authenticator, and this branch names the operator acme's administrator;
    // the seed writes the first configured administrator as the tenant's
    // owner, so the organization belongs to that operator. The global layer
    // stays off, so the tenant is free to choose the policy.
    seedImpersonation: true,
    tenantAdmins: { acme: [OPERATOR.identifier] },
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: an administrator turns a policy on, reads it back, and turns it off', async ({
    demo,
  }) => {
    test.setTimeout(scaledTimeout(300_000))

    const { page, accounts } = demo
    const mfaTitle = copy('adminUi', 'organization.security.mfa.title')
    const ssoTitle = copy('adminUi', 'organization.security.sso.title')
    const mfaSwitch = (): Locator =>
      page.getByRole('switch', {
        name: copy('adminUi', 'organization.security.switchAria', {
          title: mfaTitle,
        }),
        exact: true,
      })
    const ssoSwitch = (): Locator =>
      page.getByRole('switch', {
        name: copy('adminUi', 'organization.security.switchAria', {
          title: ssoTitle,
        }),
        exact: true,
      })
    await signInOperatorWithTotp(page, accounts)
    await openOrganizationPage(page, 'settings')

    // The platform does not enforce either policy, so both are ordinary
    // switches the tenant may move.
    await expect(mfaSwitch()).not.toBeChecked()
    await expect(mfaSwitch()).toBeEnabled()
    await expect(ssoSwitch()).not.toBeChecked()
    await expect(
      page.getByText(copy('adminUi', 'organization.security.status.disabled'), {
        exact: true,
      }),
    ).toHaveCount(2)

    await test.step('turning the policy on saves it immediately', async () => {
      // REQ-tenant-admin-017: enabling needs no dialog, saves on its own and
      // leaves the row saying so.
      await mfaSwitch().click()
      await expect(mfaSwitch()).toBeChecked()
      await expect(
        page.getByText(copy('adminUi', 'organization.security.saved'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(mfaSwitch()).toBeEnabled()
      await expect(
        page.getByText(copy('adminUi', 'organization.security.status.enabled'), {
          exact: true,
        }),
      ).toHaveCount(1)
      await expect(ssoSwitch()).not.toBeChecked()
    })

    await test.step('the row still reports the policy on', async () => {
      await expect(mfaSwitch()).toBeChecked()
      await expect(
        page.getByText(copy('adminUi', 'organization.security.status.enabled'), {
          exact: true,
        }),
      ).toHaveCount(1)
      await expect(ssoSwitch()).not.toBeChecked()
    })

    await test.step('turning it off asks first and then saves the disabled state', async () => {
      // REQ-tenant-admin-017: a close names the impact and waits for the
      // two-step guard before anything is written.
      await mfaSwitch().click()
      const dialog = page.getByRole('dialog', {
        name: copy('adminUi', 'organization.security.confirm.title', {
          title: mfaTitle,
        }),
        exact: true,
      })
      await expect(dialog).toBeVisible()
      await expect(
        dialog.getByText(
          copy('adminUi', 'organization.security.confirm.message', {
            title: mfaTitle,
            impact: copy('adminUi', 'organization.security.mfa.impact'),
          }),
          { exact: true },
        ),
      ).toBeVisible()
      // The guard has changed nothing yet: the dialog is still the only
      // interactive surface, and the page behind it is inert (the modal marks
      // the background aria-hidden, so the switch is not a role query while
      // the dialog is open). The write is observed from the row after the
      // second confirmation closes it.
      await expect(dialog).toBeVisible()

      await dialog
        .getByRole('button', {
          name: copy('adminUi', 'common.confirm'),
          exact: true,
        })
        .click()
      const again = dialog.getByRole('button', {
        name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
        exact: true,
      })
      await expect(again).toBeEnabled()
      await again.click()
      await expect(dialog).toHaveCount(0)

      await expect(mfaSwitch()).not.toBeChecked()
      await expect(mfaSwitch()).toBeEnabled()
      await expect(
        page.getByText(copy('adminUi', 'organization.security.saved'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        page.getByText(copy('adminUi', 'organization.security.status.disabled'), {
          exact: true,
        }),
      ).toHaveCount(2)
      await expect(ssoSwitch()).not.toBeChecked()
    })

    await test.step('the row still reports the policy off', async () => {
      await expect(mfaSwitch()).not.toBeChecked()
      await expect(
        page.getByText(copy('adminUi', 'organization.security.status.disabled'), {
          exact: true,
        }),
      ).toHaveCount(2)
      await expect(ssoSwitch()).not.toBeChecked()
    })
  })
})

test.describe('a protected business tenant is read-only', () => {
  test.use({
    seedProtectedTenant: true,
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: the protected organization renders no switch and its write is refused', async ({
    demo,
  }) => {
    const { page, server, accounts } = demo

    await accounts.signIn({
      identifier: e2eProtectedTenant.username,
      password: e2eProtectedTenant.password,
    })
    await expect(homeHeading(page)).toBeVisible()

    // The protected prefix is a settings write policy, not an admission rule:
    // the tenant's administrator still reaches organization administration.
    await openOrganizationPage(page, 'settings')

    // The surface says why it cannot be changed and renders no switch at all;
    // the two policies are still listed with their titles.
    await expect(
      page.getByText(
        copy('adminUi', 'organization.security.readOnly.protected.title'),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(
      page.getByText(
        copy(
          'adminUi',
          'organization.security.readOnly.protected.description',
        ),
        { exact: true },
      ),
    ).toBeVisible()
    for (const title of [
      copy('adminUi', 'organization.security.mfa.title'),
      copy('adminUi', 'organization.security.sso.title'),
    ]) {
      await expect(
        page.getByRole('heading', { level: 3, name: title, exact: true }),
      ).toBeVisible()
    }
    await expect(page.getByRole('switch')).toHaveCount(0)

    // The refused write has no rendered control on a read-only page. The
    // review server fixture makes the same write the page would, as the same
    // administrator, and the answer is the host seam's refusal. The rendered
    // read-only state above is asserted first; this is the write that state
    // promises cannot land.
    const refusal = await server.writeTenantSetting({
      tenant: e2eProtectedTenant.tenant,
      identifier: e2eProtectedTenant.identifier,
      password: e2eProtectedTenant.password,
      namespace: 'tenant.admission',
      key: 'require-mfa',
      value: 'true',
    })
    expect(refusal.status).toBe(403)
    expect(refusal.problemType).toBe('urn:speed:settings:protected')
  })
})

/**
 * The organization profile page's ownership surface.
 *
 * The review seed registers its business tenants as roster rows, not as
 * organizations: a seeded tenant carries no owner (`owner_account` stays
 * empty), so the owner-only controls never render on acme. These branches
 * therefore create an organization through the self-service page, whose
 * creator becomes the owner and the first tenant-admin in one product write,
 * and whose success lands on the organization profile page -- the fifth page.
 * That is the state the ownership controls describe, reached the way a person
 * reaches it.
 *
 * The ownership requirements (REQ-tenancy-060 through REQ-tenancy-064 and
 * REQ-tenant-admin-068) are registered in the PRD's feature documents but not
 * in this journey document, so they stay out of `@covers`: the honesty gate
 * accepts only ids the journey document lists, and the journey document is not
 * this task's to edit. The gap is reported rather than claimed.
 */
const OWNED_NAME = 'acme-lab'
const OWNED_DISPLAY_NAME = 'Acme Lab'
const RENAMED_ORG = 'Acme Lab Renamed'
const RENAMED_DESCRIPTION =
  'The renamed lab organization the profile save writes back.'
const OWNED_DESCRIPTION = 'The lab organization the ownership branches create.'
const OWNED_CONTACT_EMAIL = 'lab@demo.test'
const OWNED_WEBSITE = 'https://lab.demo.test'

/** One definition-row value on a detail-style list, by its term. */
function definitionValue(page: Page, label: string): Locator {
  return page
    .locator('dt')
    .filter({ hasText: new RegExp(`^${escapeRegExp(label)}$`) })
    .locator('xpath=following-sibling::dd[1]')
}

/** The owner fields one organization-profile read answers with. */
interface ProfileOwner {
  readonly owner_account: string
  readonly owner_display_name: string
  readonly owner_username: string
}

/**
 * Wait for the product's own organization-profile read of `name` and return
 * the owner fields it answered. The page shows the owner's display name and
 * username but never the internal account identifier, so this is where a test
 * that must prove the identifier's absence learns the value to look for.
 */
function waitForProfileOwner(
  page: Page,
  name: string,
): Promise<ProfileOwner> {
  return page
    .waitForResponse(
      (response) =>
        response.request().method() === 'GET' &&
        new URL(response.url()).pathname ===
          `/api/v1/tenants/${name}/profile`,
    )
    .then(async (response) => {
      expect(response.ok()).toBe(true)
      const body = (await response.json()) as { profile: ProfileOwner }
      return body.profile
    })
}

/** The profile page's owner-only card. */
function ownershipSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'tenantProfile.ownership.title'),
    exact: true,
  })
}

function transferOwnerButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'tenantProfile.ownership.transfer.button'),
    exact: true,
  })
}

function deleteOrganizationButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'tenantProfile.ownership.delete.button'),
    exact: true,
  })
}

/**
 * Ask the organization's owner to hand ownership to one of its active
 * members, from the profile page. The dialog's member selector offers the
 * organization's active members and the request takes the deliberate second
 * confirmation. A request changes nothing: the caller stays the owner until
 * the target confirms on the account-level page.
 */
async function requestOwnerTransfer(
  page: Page,
  target: { readonly displayName: string },
): Promise<void> {
  await transferOwnerButton(page).click()
  const dialog = page.getByRole('dialog', {
    name: copy('adminUi', 'tenantProfile.ownership.transfer.title', {
      name: OWNED_DISPLAY_NAME,
    }),
    exact: true,
  })
  await expect(dialog).toBeVisible()
  await chooseSelectOption(
    dialog.getByRole('combobox', {
      name: copy('adminUi', 'tenantProfile.ownership.transfer.targetLabel'),
      exact: true,
    }),
    // Only the organization's active members are offered, minus the current
    // owner; the target is one.
    page.getByRole('option', {
      name: new RegExp(`^${escapeRegExp(target.displayName)} \\(.+\\)$`),
    }),
  )

  // The request asks for the deliberate second confirmation.
  await dialog
    .getByRole('button', {
      name: copy('adminUi', 'tenantProfile.ownership.transfer.confirmLabel'),
      exact: true,
    })
    .click()
  const again = dialog.getByRole('button', {
    name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
    exact: true,
  })
  await expect(again).toBeEnabled()
  await again.click()

  // The confirmation says the request was sent and that ownership has not
  // moved, and the card now holds the pending request.
  await expect(
    ownershipSection(page).getByText(
      copyPattern('adminUi', 'tenantProfile.ownership.transfer.requested', {
        account: `${escapeRegExp(target.displayName)} \\(.+\\)`,
      }),
    ),
  ).toBeVisible()
  await expect(
    ownershipSection(page).getByRole('region', {
      name: copy('adminUi', 'tenantProfile.ownership.pending.title'),
      exact: true,
    }),
  ).toBeVisible()
}

/**
 * The target's side: open the account-level transfer requests page from the
 * account menu, open the request naming the organization, and confirm it
 * through the page's own two-step confirmation. The caller has to be a
 * signed-in active member whose address the invitation proved, because the
 * confirmation is registration-gated.
 */
async function confirmOwnerTransfer(
  page: Page,
  organizationName: string,
): Promise<void> {
  const menu = await openAccountMenu(page)
  await menu
    .getByRole('menuitem', {
      name: copy('demo', 'userMenu.transferRequests'),
      exact: true,
    })
    .click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('accountUi', 'transferRequests.title'),
      exact: true,
    }),
  ).toBeVisible()

  // The list addresses a request by the organization's own name; the row's
  // Review action opens the detail view.
  const row = page
    .getByRole('listitem')
    .filter({ hasText: organizationName })
  await expect(row).toHaveCount(1)
  await row
    .getByRole('link', {
      name: copy('accountUi', 'transferRequests.open'),
      exact: true,
    })
    .click()

  const detail = page.getByRole('region', {
    name: organizationName,
    exact: true,
  })
  await expect(detail).toBeVisible()
  await detail
    .getByRole('button', {
      name: copy('accountUi', 'transferRequests.confirm'),
      exact: true,
    })
    .click()

  // Confirming changes an organization's owner, so it takes the deliberate
  // second confirmation the shared dialog asks for.
  const confirm = page.getByRole('dialog', {
    name: copy('accountUi', 'transferRequests.confirmTitle', {
      tenant: organizationName,
    }),
    exact: true,
  })
  await expect(confirm).toBeVisible()
  await confirm
    .getByRole('button', {
      name: copy('accountUi', 'transferRequests.confirm'),
      exact: true,
    })
    .click()
  const again = confirm.getByRole('button', {
    name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
    exact: true,
  })
  await expect(again).toBeEnabled()
  await again.click()

  await expect(
    page.getByText(
      copy('accountUi', 'transferRequests.confirmed', {
        tenant: organizationName,
      }),
      { exact: true },
    ),
  ).toBeVisible()
}

/** The notification surface's inbox list. */
function inboxList(page: Page): Locator {
  return page.getByRole('list', {
    name: copy('notificationUi', 'inbox.title'),
  })
}

/** One in-app message as the notification surface's own read answers it. */
interface InboxMessage {
  readonly title: string
  readonly body: string
  readonly params: Record<string, unknown> | null
}

/**
 * Open the notification centre from the account menu and return the inbox read
 * that carries the notice titled `title`.
 *
 * The delivery worker writes an in-app message on the queue's own poll, so the
 * first read after the page opens can land before the notice does. The live
 * stream re-reads the list on each pushed frame; the predicate skips the
 * answers that do not carry the notice rather than pinning the test to the
 * first fetch.
 */
async function openNotificationCentre(
  page: Page,
  title: string,
): Promise<InboxMessage> {
  const answered = page.waitForResponse(
    async (response) => {
      if (response.request().method() !== 'GET') {
        return false
      }
      if (
        new URL(response.url()).pathname !== '/api/v1/notifications/messages'
      ) {
        return false
      }
      if (!response.ok()) {
        return false
      }
      const body = (await response.json()) as {
        messages?: readonly InboxMessage[]
      }
      return (body.messages ?? []).some((message) => message.title === title)
    },
    { timeout: scaledTimeout(45_000) },
  )
  const menu = await openAccountMenu(page)
  // The row's accessible name appends the unread count ("Messages, 1 unread")
  // when one is known, so the lookup matches the name's opening words.
  await menu
    .getByRole('menuitem', { name: copy('demo', 'userMenu.messages') })
    .click()
  const body = (await (await answered).json()) as {
    messages?: readonly InboxMessage[]
  }
  const message = (body.messages ?? []).find(
    (candidate) => candidate.title === title,
  )
  if (message === undefined) {
    throw new Error(`the inbox read carried no notice titled ${title}`)
  }
  return message
}

/**
 * Create an organization through the self-service page: the account menu's
 * organization row expands to the create entry, the form is filled, and the
 * hand-over lands on the new organization's profile page. The creator is its
 * owner and first tenant-admin.
 */
async function createOrganization(
  page: Page,
  input: {
    readonly name: string
    readonly displayName: string
    readonly description?: string
  },
): Promise<void> {
  const menu = await openAccountMenu(page)
  await menu.getByRole('menuitem').first().click()
  await menu
    .getByRole('menuitem', {
      name: copy('demo', 'organization.create'),
      exact: true,
    })
    .click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('orgCreateUi', 'page.title'),
      exact: true,
    }),
  ).toBeVisible()
  await page
    .getByLabel(copy('orgCreateUi', 'fields.name'), { exact: true })
    .fill(input.name)
  await page
    .getByLabel(copy('orgCreateUi', 'fields.displayName'), { exact: true })
    .fill(input.displayName)
  if (input.description !== undefined) {
    await page
      .getByLabel(copy('orgCreateUi', 'fields.description'), { exact: true })
      .fill(input.description)
  }
  await page
    .getByRole('button', { name: copy('orgCreateUi', 'submit'), exact: true })
    .click()
  // The create-then-enter sequence switches the current organization and
  // navigates to its profile page.
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('adminUi', 'organization.nav.profile'),
      exact: true,
    }),
  ).toBeVisible()
  expect(new URL(page.url()).pathname).toBe(organizationPath('/profile'))
}

/**
 * Issue an invitation to `email` on the organization's invitations page and
 * return the acceptance link the one-time panel hands out. The page is left
 * (which dismisses the panel) once the link is read.
 */
async function issueInvitation(
  page: Page,
  server: { readonly url: string },
  email: string,
): Promise<string> {
  await openOrganizationPage(page, 'invitations')
  await page
    .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
      exact: true,
    })
    .fill(email)
  await page
    .getByRole('button', {
      name: copy('adminUi', 'invitations.issue.submit'),
      exact: true,
    })
    .click()
  const panel = invitationLinkPanel(page, email)
  await expect(panel).toBeVisible()
  const value = panel.getByText(
    new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
  )
  await expect(value).toBeVisible()
  const link = ((await value.textContent()) ?? '').trim()
  expect(link).toMatch(
    new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
  )
  // Leaving the page drops the one-time panel; the link is already captured.
  await mainNavigation(page)
    .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
    .click()
  await expect(homeHeading(page)).toBeVisible()
  return link
}

/**
 * Open an invitation link in a second context, sign the invited account in and
 * accept. The invitation fixture's invitee is the default; a caller passes an
 * account when a membership is restored to another account.
 */
async function acceptInvitationAsInvitee(
  invitee: Page,
  link: string,
  account: {
    readonly username: string
    readonly password: string
  } = e2eInvitee,
): Promise<void> {
  await invitee.goto(link)
  // The landing shows the invitation summary first; signing in is the
  // landing's own action, and it opens the host's surface in place.
  await expect(
    invitee.getByRole('heading', {
      level: 1,
      name: copy('authUi', 'acceptInvitation.title'),
      exact: true,
    }),
  ).toBeVisible()
  await invitee
    .getByRole('button', {
      name: copy('authUi', 'acceptInvitation.signIn'),
      exact: true,
    })
    .click()
  await expect(
    invitee.getByRole('heading', {
      level: 1,
      name: copy('demo', 'signIn.heading'),
      exact: true,
    }),
  ).toBeVisible()
  await invitee
    .getByRole('textbox', {
      name: copy('authUi', 'passwordSignIn.identifierLabel'),
      exact: true,
    })
    .fill(account.username)
  await invitee
    .getByRole('textbox', {
      name: copy('authUi', 'passwordSignIn.passwordLabel'),
      exact: true,
    })
    .fill(account.password)
  await invitee
    .getByRole('button', {
      name: copy('authUi', 'passwordSignIn.submit'),
      exact: true,
    })
    .click()
  await expect(
    invitee.getByRole('heading', {
      name: copy('authUi', 'acceptInvitation.title'),
      exact: true,
    }),
  ).toBeVisible()
  await invitee
    .getByRole('button', {
      name: copy('authUi', 'acceptInvitation.accept'),
      exact: true,
    })
    .click()
  await expect(homeHeading(invitee)).toBeVisible()
}

/**
 * Register a new account through an invitation link, prove its address with the
 * code the account page sends, and accept the invitation. The registration's
 * product landing is the account page's verification section, and the
 * remembered landing returns the viewer to the link once the address is
 * proved.
 */
async function registerAndAcceptThroughLink(
  invitee: Page,
  link: string,
  server: E2EServer,
): Promise<void> {
  const invitationPath = new URL(link).pathname
  await invitee.goto(link)
  await expect(
    invitee.getByRole('heading', {
      level: 1,
      name: copy('authUi', 'acceptInvitation.title'),
      exact: true,
    }),
  ).toBeVisible()
  await invitee
    .getByRole('button', {
      name: copy('authUi', 'acceptInvitation.createAccount'),
      exact: true,
    })
    .click()
  await expect(
    invitee.getByRole('heading', {
      level: 1,
      name: copy('demo', 'signIn.registerHeading'),
      exact: true,
    }),
  ).toBeVisible()

  await invitee
    .getByLabel(copy('authUi', 'register.usernameLabel'), { exact: true })
    .fill(ROLE_INVITEE_IDENTITY.username)
  await invitee
    .getByLabel(copy('authUi', 'register.displayNameLabel'), { exact: true })
    .fill(ROLE_INVITEE_IDENTITY.displayName)
  await invitee
    .getByLabel(copy('authUi', 'register.emailLabel'), { exact: true })
    .fill(ROLE_INVITEE_IDENTITY.email)
  await invitee
    .getByLabel(copy('authUi', 'register.passwordLabel'), { exact: true })
    .fill(ROLE_INVITEE_IDENTITY.password)
  await invitee
    .getByLabel(copy('authUi', 'register.confirmPasswordLabel'), {
      exact: true,
    })
    .fill(ROLE_INVITEE_IDENTITY.password)

  // Registration sends no mail; the one code that follows is the account
  // page's verification send, so the cursor before the submit is enough.
  const beforeCode = server.log.mark()
  await invitee
    .getByRole('button', {
      name: copy('authUi', 'register.submit'),
      exact: true,
    })
    .click()

  await expect(
    invitee.getByRole('region', {
      name: copy('accountUi', 'verification.title'),
      exact: true,
    }),
  ).toBeVisible()
  const section = verificationSection(invitee)
  const { code } = await waitForVerificationCode(
    server.log,
    'mail',
    ROLE_INVITEE_IDENTITY.email,
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

  // The remembered landing returns the proved viewer to the link, which now
  // offers the acceptance; the arrival itself proves the settled proof.
  await expect
    .poll(() => new URL(invitee.url()).pathname)
    .toBe(invitationPath)
  await expect(
    invitee.getByRole('heading', {
      level: 1,
      name: copy('authUi', 'acceptInvitation.title'),
      exact: true,
    }),
  ).toBeVisible()
  await invitee
    .getByRole('button', {
      name: copy('authUi', 'acceptInvitation.accept'),
      exact: true,
    })
    .click()
  await expect(homeHeading(invitee)).toBeVisible()
}

/** Bind a role to one member through the members page's management panel. */
async function bindMemberRole(
  page: Page,
  displayName: string,
  role: string,
): Promise<void> {
  await openOrganizationPage(page, 'invitations')
  await manageMemberButton(page, displayName).click()
  const panel = memberPanel(page)
  await expect(panel).toBeVisible()
  await chooseSelectOption(
    panel.getByRole('combobox', {
      name: copy('adminUi', 'members.panel.roleLabel'),
      exact: true,
    }),
    page.getByRole('option', { name: role, exact: true }),
  )
  await panel
    .getByRole('button', {
      name: copy('adminUi', 'members.panel.bind'),
      exact: true,
    })
    .click()
  await expect(
    panel.getByText(
      copy('adminUi', 'members.panel.bindSaved', { account: displayName, role }),
      { exact: true },
    ),
  ).toBeVisible()
  await panel
    .getByRole('button', {
      name: copyPattern('adminUi', 'members.panel.closeAria', {
        name: '[^"]*',
      }),
    })
    .click()
  await expect(panel).toHaveCount(0)
}

/**
 * Sign the review template's platform operator in and prove its address. The
 * platform management writes refuse an account whose email is unproved, and
 * the seed writes the operator's address as an assertion, not a proof.
 */
async function signInAndVerifyOperator(
  page: Page,
  server: E2EServer,
  accounts: Accounts,
): Promise<void> {
  await accounts.signIn({
    identifier: OPERATOR.username,
    password: OPERATOR.password,
  })
  const banner = verificationBanner(page, OPERATOR.identifier)
  await expect(banner).toBeVisible()
  const before = server.log.mark()
  await banner
    .getByRole('link', {
      name: copy('demo', 'verificationBanner.verify'),
      exact: true,
    })
    .click()
  const section = verificationSection(page)
  await expect(section).toBeVisible()
  const codeField = section.getByLabel(
    copy('accountUi', 'verification.codeLabel'),
    { exact: true },
  )
  const { code } = await waitForVerificationCode(
    server.log,
    'mail',
    OPERATOR.identifier,
    { after: before },
  )
  await codeField.fill(code)
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
}

test.describe('the organization profile page belongs to its owner', () => {
  test.use({
    seedTenantAdmin: true,
    seedInvitee: true,
    identityQuota: '10000',
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: the owner reads the profile and saves its four descriptive fields', async ({
    demo,
  }) => {
    test.setTimeout(scaledTimeout(180_000))

    const { page, accounts } = demo

    await accounts.signIn({
      identifier: e2eTenantAdmin.username,
      password: e2eTenantAdmin.password,
    })
    await expect(homeHeading(page)).toBeVisible()

    // The create-then-enter sequence lands on the profile page; the read it
    // issues is awaited so the owner's internal identifier is known. The page
    // must never render that identifier.
    const profileRead = waitForProfileOwner(page, OWNED_NAME)
    await createOrganization(page, {
      name: OWNED_NAME,
      displayName: OWNED_DISPLAY_NAME,
      description: OWNED_DESCRIPTION,
    })
    const profileOwner = await profileRead

    // The identity card names the owner by display name and username
    // together, and the caller is that owner: the two controls render only
    // for the account the profile's owner column carries.
    const owner = definitionValue(
      page,
      copy('adminUi', 'tenantProfile.field.owner'),
    )
    await expect(owner).toBeVisible()
    await expect(owner).toHaveText(
      `${e2eTenantAdmin.displayName} (${e2eTenantAdmin.username})`,
    )
    expect(profileOwner.owner_display_name).toBe(e2eTenantAdmin.displayName)
    expect(profileOwner.owner_username).toBe(e2eTenantAdmin.username)
    expect(
      profileOwner.owner_account,
      'the profile names no owner account',
    ).not.toBe('')
    // The internal account identifier appears nowhere on the page: not as
    // text and not in an attribute. The username above is the only identity
    // the owner row exposes beside the display name.
    await expect(page.getByText(profileOwner.owner_account)).toHaveCount(0)
    expect(await page.content()).not.toContain(profileOwner.owner_account)
    // The status chip shares the value cell with the name, so the name is
    // asserted as the cell's leading text rather than the whole cell.
    await expect(
      definitionValue(page, copy('adminUi', 'tenantProfile.field.name')),
    ).toContainText(OWNED_NAME)
    await expect(ownershipSection(page)).toBeVisible()
    await expect(transferOwnerButton(page)).toBeVisible()
    await expect(deleteOrganizationButton(page)).toBeVisible()

    // The four descriptive fields are the page's to edit.
    await page
      .getByLabel(copy('adminUi', 'tenantProfile.field.displayName'), {
        exact: true,
      })
      .fill(RENAMED_ORG)
    await page
      .getByLabel(copy('adminUi', 'tenantProfile.field.description'), {
        exact: true,
      })
      .fill(RENAMED_DESCRIPTION)
    await page
      .getByLabel(copy('adminUi', 'tenantProfile.field.contactEmail'), {
        exact: true,
      })
      .fill(OWNED_CONTACT_EMAIL)
    await page
      .getByLabel(copy('adminUi', 'tenantProfile.field.website'), {
        exact: true,
      })
      .fill(OWNED_WEBSITE)
    await page
      .getByRole('button', {
        name: copy('adminUi', 'tenantProfile.form.save'),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(
        copy('adminUi', 'tenantProfile.form.savedNamed', { name: RENAMED_ORG }),
        { exact: true },
      ),
    ).toBeVisible()

    // The landed save reaches the host's tenant-list reload, so the account
    // menu's organization row shows the new name at once.
    await openAccountMenu(page)
    await expect(organizationRow(page, RENAMED_ORG)).toBeVisible()
    await closeAccountMenu(page)

    // A full document load re-settles the current tenant from the caller's
    // memberships, so the reloaded page addresses the seeded acme first. The
    // read-back therefore returns to the organization the save wrote.
    await page.reload()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'organization.nav.profile'),
        exact: true,
      }),
    ).toBeVisible()
    await switchOrganization(page, 'acme', RENAMED_ORG)
    await openOrganizationPage(page, 'profile')

    // Every one of the four saved values comes back from the server. Each was
    // written to a value distinct from the organization's creation state, so a
    // payload that dropped any of them leaves that field reading the old value
    // and fails here.
    // The status chip shares the value cell with the name, so the name is
    // asserted as the cell's leading text rather than the whole cell.
    await expect(
      definitionValue(page, copy('adminUi', 'tenantProfile.field.name')),
    ).toContainText(OWNED_NAME)
    const savedFields = [
      ['tenantProfile.field.displayName', RENAMED_ORG],
      ['tenantProfile.field.description', RENAMED_DESCRIPTION],
      ['tenantProfile.field.contactEmail', OWNED_CONTACT_EMAIL],
      ['tenantProfile.field.website', OWNED_WEBSITE],
    ] as const
    for (const [labelKey, value] of savedFields) {
      await expect(
        page.getByLabel(copy('adminUi', labelKey), { exact: true }),
      ).toHaveValue(value)
    }
  })

  test('tenant-admin: a non-owner administrator reads the profile but sees no ownership controls', async ({
    demo,
    browser,
  }) => {
    test.setTimeout(scaledTimeout(300_000))

    const { page, server, accounts } = demo
    let inviteeContext: BrowserContext | undefined

    try {
      await accounts.signIn({
        identifier: e2eTenantAdmin.username,
        password: e2eTenantAdmin.password,
      })
      await expect(homeHeading(page)).toBeVisible()

      await createOrganization(page, {
        name: OWNED_NAME,
        displayName: OWNED_DISPLAY_NAME,
      })

      // A second tenant administrator who is not the owner: invite the
      // colleague, accept, then bind the tenant-admin role through the member
      // panel.
      const link = await issueInvitation(page, server, e2eInvitee.identifier)
      inviteeContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      const invitee = await inviteeContext.newPage()
      await acceptInvitationAsInvitee(invitee, link)
      await bindMemberRole(page, e2eInvitee.displayName, 'tenant-admin')

      // The binding changes the invitee's tenant permission set, which the
      // session read at sign-in. A fresh sign-in re-reads it; the signed-in
      // account and the organization stay the same.
      const inviteeAccounts = createAccounts({ page: invitee, server })
      await inviteeAccounts.signOut()
      await inviteeAccounts.signIn({
        identifier: e2eInvitee.username,
        password: e2eInvitee.password,
      })
      await expect(homeHeading(invitee)).toBeVisible()

      await openOrganizationPage(invitee, 'profile')

      // The page is admitted by the tenant-admin grant and names the owner by
      // display name and username, but the caller is not that account:
      // neither control renders, and the ownership card is absent altogether.
      const owner = definitionValue(
        invitee,
        copy('adminUi', 'tenantProfile.field.owner'),
      )
      await expect(owner).toBeVisible()
      await expect(owner).toHaveText(
        `${e2eTenantAdmin.displayName} (${e2eTenantAdmin.username})`,
      )
      await expect(ownershipSection(invitee)).toHaveCount(0)
      await expect(transferOwnerButton(invitee)).toHaveCount(0)
      await expect(deleteOrganizationButton(invitee)).toHaveCount(0)
    } finally {
      await inviteeContext?.close().catch(() => undefined)
    }
  })

  test('tenant-admin: the owner hands the organization to an active member and stays an administrator', async ({
    demo,
    browser,
  }) => {
    test.setTimeout(scaledTimeout(300_000))

    const { page, server, accounts } = demo
    let inviteeContext: BrowserContext | undefined

    try {
      await accounts.signIn({
        identifier: e2eTenantAdmin.username,
        password: e2eTenantAdmin.password,
      })
      await expect(homeHeading(page)).toBeVisible()

      await createOrganization(page, {
        name: OWNED_NAME,
        displayName: OWNED_DISPLAY_NAME,
      })

      // The target is an active member of the organization, reached through
      // a real invitation accepted in a second context.
      const link = await issueInvitation(page, server, e2eInvitee.identifier)
      inviteeContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      const invitee = await inviteeContext.newPage()
      await acceptInvitationAsInvitee(invitee, link)

      await openOrganizationPage(page, 'profile')
      await requestOwnerTransfer(page, e2eInvitee)

      // The target's own notification surface carries the request. Its body
      // renders the request's expiry as a time in the viewer's display zone:
      // the account stores no zone and this context pins UTC, so the expected
      // rendering is the instant the inbox read answered, formatted for UTC.
      // The row must carry that time -- not the marker the server writes in
      // its place, and not the raw instant.
      const transferTitle = copyKey(
        'ownershipNotice',
        'tenant.owner.transfer_requested.in_app.title',
      )
      const notice = await openNotificationCentre(invitee, transferTitle)
      const expiresAt = notice.params?.['expires_at']
      if (typeof expiresAt !== 'string') {
        throw new Error('the transfer notice carries no expiry parameter')
      }
      const expiresText = new Intl.DateTimeFormat('en-US', {
        dateStyle: 'medium',
        timeStyle: 'short',
        timeZone: 'UTC',
      }).format(new Date(expiresAt))
      const noticeRow = inboxList(invitee)
        .getByRole('listitem')
        .filter({ hasText: transferTitle })
      await expect(noticeRow).toHaveCount(1)
      await expect(noticeRow).toContainText(expiresText)
      // The delivery marker's delimiters frame the parameter name the client
      // resolves; neither may reach the rendered row.
      await expect(noticeRow).not.toContainText(String.fromCharCode(0xe000))
      await expect(noticeRow).not.toContainText(String.fromCharCode(0xe001))
      await expect(noticeRow).not.toContainText(expiresAt)

      // A request changes nothing yet: the caller stays on the
      // organization's own page and remains its owner.
      expect(new URL(page.url()).pathname).toBe(organizationPath('/profile'))

      // The target confirms on the account-level page opened from its own
      // account menu. The confirmation is the write that moves the owner,
      // and it happens in the target's context, so this page's own read is
      // re-issued rather than updated through a shared cache.
      await confirmOwnerTransfer(invitee, OWNED_NAME)

      // Re-open the profile: this page's own read predates the confirmation,
      // and the target's context shares no cache with it.
      await openOrganizationPage(page, 'invitations')
      await openOrganizationPage(page, 'profile')

      // The re-read profile names the target as the owner, and the controls
      // withdraw from the previous owner because it no longer owns the
      // organization -- not because it left it.
      const owner = definitionValue(
        page,
        copy('adminUi', 'tenantProfile.field.owner'),
      )
      await expect(owner).toHaveText(
        `${e2eInvitee.displayName} (${e2eInvitee.username})`,
      )
      await expect(transferOwnerButton(page)).toHaveCount(0)
      await expect(deleteOrganizationButton(page)).toHaveCount(0)

      // The previous owner is still a member and keeps its tenant-admin
      // binding on the organization's own member list.
      await openOrganizationPage(page, 'invitations')
      const callerMembers = membersSection(page).getByRole('table')
      const callerRoles = await columnIndex(
        callerMembers,
        copy('adminUi', 'members.column.roles'),
      )
      const retained = memberRow(page, e2eTenantAdmin.displayName)
      await expect(retained).toHaveCount(1)
      await expect(cell(retained, callerRoles)).toContainText('tenant-admin')

      // The target now holds the organization: its owner controls render for
      // that account, and its member row carries the tenant-admin binding.
      const inviteeAccounts = createAccounts({ page: invitee, server })
      await inviteeAccounts.signOut()
      await inviteeAccounts.signIn({
        identifier: e2eInvitee.username,
        password: e2eInvitee.password,
      })
      await expect(homeHeading(invitee)).toBeVisible()
      await expect(organizationHomeCard(invitee)).toBeVisible()
      await openOrganizationPage(invitee, 'profile')
      const promotedOwner = definitionValue(
        invitee,
        copy('adminUi', 'tenantProfile.field.owner'),
      )
      await expect(promotedOwner).toBeVisible()
      // The transfer's new owner is named by its display name and username,
      // read fresh from the account store rather than carried over from the
      // previous owner.
      await expect(promotedOwner).toHaveText(
        `${e2eInvitee.displayName} (${e2eInvitee.username})`,
      )
      await expect(transferOwnerButton(invitee)).toBeVisible()
      await expect(deleteOrganizationButton(invitee)).toBeVisible()

      await openOrganizationPage(invitee, 'invitations')
      const members = membersSection(invitee).getByRole('table')
      const rolesColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.roles'),
      )
      const promoted = memberRow(invitee, e2eInvitee.displayName)
      await expect(promoted).toHaveCount(1)
      await expect(cell(promoted, rolesColumn)).toContainText('tenant-admin')
      // The previous owner is still a member of the organization and still
      // carries tenant-admin.
      const previous = memberRow(invitee, e2eTenantAdmin.displayName)
      await expect(previous).toHaveCount(1)
      await expect(cell(previous, rolesColumn)).toContainText('tenant-admin')

      // The organization is still among the previous owner's memberships: the
      // switcher offers it from the other organization, and moving back lands
      // on the organization home with its administration entry intact.
      const menu = await openAccountMenu(page)
      await menu.getByRole('menuitem').first().click()
      await expect(
        menu.getByRole('menuitem', { name: 'acme', exact: true }),
      ).toBeVisible()
      await menu.getByRole('menuitem', { name: 'acme', exact: true }).click()
      await expect(homeHeading(page)).toBeVisible()
      await expectOrganizationEntryPresent(page)

      const backMenu = await openAccountMenu(page)
      await backMenu.getByRole('menuitem').first().click()
      const destination = backMenu.getByRole('menuitem', {
        name: OWNED_DISPLAY_NAME,
        exact: true,
      })
      await expect(destination).toBeVisible()
      await destination.click()
      await expect(homeHeading(page)).toBeVisible()
      await expectOrganizationEntryPresent(page)
    } finally {
      await inviteeContext?.close().catch(() => undefined)
    }
  })

  test('tenant-admin: an owner handed the organization on is refused the write its stale page still offers', async ({
    demo,
    browser,
  }) => {
    test.setTimeout(scaledTimeout(300_000))

    const { page, server, accounts } = demo
    let inviteeContext: BrowserContext | undefined
    let otherContext: BrowserContext | undefined

    try {
      await accounts.signIn({
        identifier: e2eTenantAdmin.username,
        password: e2eTenantAdmin.password,
      })
      await expect(homeHeading(page)).toBeVisible()

      await createOrganization(page, {
        name: OWNED_NAME,
        displayName: OWNED_DISPLAY_NAME,
      })

      // The transfer's target is an active member, reached through a real
      // invitation accepted in a second context.
      const link = await issueInvitation(page, server, e2eInvitee.identifier)
      inviteeContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      const invitee = await inviteeContext.newPage()
      await acceptInvitationAsInvitee(invitee, link)

      // The owner's page opens the deletion and clears the name guard before
      // anything changes, so the confirmation is armed and the write needs one
      // more click. The page keeps the profile it read -- this account is the
      // owner in it -- so the control stays on screen while the state
      // underneath moves.
      await openOrganizationPage(page, 'profile')
      await deleteOrganizationButton(page).click()
      const dialog = page.getByRole('dialog', {
        name: copy('adminUi', 'tenantProfile.ownership.delete.title', {
          name: OWNED_DISPLAY_NAME,
        }),
        exact: true,
      })
      await expect(dialog).toBeVisible()
      await dialog
        .getByLabel(
          copy('adminUi', 'tenantProfile.ownership.delete.nameLabel'),
          { exact: true },
        )
        .fill(OWNED_NAME)
      await dialog
        .getByRole('button', {
          name: copy('adminUi', 'tenantProfile.ownership.delete.confirmLabel'),
          exact: true,
        })
        .click()
      const again = dialog.getByRole('button', {
        name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
        exact: true,
      })
      await expect(again).toBeEnabled()

      // A second session of the same account requests the organization for
      // the colleague. The request changes nothing by itself: the target's
      // confirmation is the write that moves the owner, and it leaves the
      // previous owner's membership and bindings in place.
      otherContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      const other = await otherContext.newPage()
      const otherAccounts = createAccounts({ page: other, server })
      await otherAccounts.signIn({
        identifier: e2eTenantAdmin.username,
        password: e2eTenantAdmin.password,
      })
      await expect(homeHeading(other)).toBeVisible()
      await switchOrganization(other, 'acme', OWNED_DISPLAY_NAME)
      await openOrganizationPage(other, 'profile')
      await requestOwnerTransfer(other, e2eInvitee)

      // The second session stays on the organization's profile: a request
      // does not move the owner. The target then confirms on its own
      // account-level page, in a session of its own, and that confirmation
      // is what hands the organization on while this first page keeps the
      // owner profile it read.
      expect(new URL(other.url()).pathname).toBe(organizationPath('/profile'))
      await confirmOwnerTransfer(invitee, OWNED_NAME)

      // The stale page's confirmation is still armed. The caller is still a
      // member -- the transfer kept its membership and its bindings -- but the
      // organization's owner is the colleague, so the owner decision refuses
      // the write, which the card states as the forbidden answer.
      await expect(dialog).toBeVisible()
      await expect(again).toBeEnabled()
      await again.click()
      await expect(
        ownershipSection(page).getByText(
          copy('adminUi', 'tenantProfile.ownership.error.forbidden'),
          { exact: true },
        ),
      ).toBeVisible()
    } finally {
      await inviteeContext?.close().catch(() => undefined)
      await otherContext?.close().catch(() => undefined)
    }
  })

  test('tenant-admin: the owner types the name, deletes the organization, and the name stays taken', async ({
    demo,
  }) => {
    test.setTimeout(scaledTimeout(180_000))

    const { page, accounts } = demo

    await accounts.signIn({
      identifier: e2eTenantAdmin.username,
      password: e2eTenantAdmin.password,
    })
    await expect(homeHeading(page)).toBeVisible()

    await createOrganization(page, {
      name: OWNED_NAME,
      displayName: OWNED_DISPLAY_NAME,
    })

    await openOrganizationPage(page, 'profile')
    await deleteOrganizationButton(page).click()

    // Deletion is terminal, so the dialog asks for the name and then for the
    // deliberate second confirmation.
    const dialog = page.getByRole('dialog', {
      name: copy('adminUi', 'tenantProfile.ownership.delete.title', {
        name: OWNED_DISPLAY_NAME,
      }),
      exact: true,
    })
    await expect(dialog).toBeVisible()
    const nameField = dialog.getByLabel(
      copy('adminUi', 'tenantProfile.ownership.delete.nameLabel'),
      { exact: true },
    )
    const confirm = dialog.getByRole('button', {
      name: copy('adminUi', 'tenantProfile.ownership.delete.confirmLabel'),
      exact: true,
    })
    // The typed name is the guard, not decoration: the confirmation is inert
    // while the field is empty or names anything but this organization's own
    // name, and only the matching value enables it.
    await expect(confirm).toBeDisabled()
    await nameField.fill(`${OWNED_NAME}-not-the-name`)
    await expect(confirm).toBeDisabled()
    await nameField.fill(OWNED_NAME)
    await expect(confirm).toBeEnabled()
    await confirm.click()
    const again = dialog.getByRole('button', {
      name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
      exact: true,
    })
    await expect(again).toBeEnabled()
    await again.click()

    // The caller is no longer a member: the host leaves the organization and
    // the confirmation survives the move to the home page.
    await expect(homeHeading(page)).toBeVisible()
    expect(new URL(page.url()).pathname).toBe('/')
    const confirmation = page
      .getByRole('button', {
        name: copy('demo', 'home.notice.dismiss'),
        exact: true,
      })
      .locator('xpath=ancestor::div[contains(@class, "MuiAlert-root")][1]')
    await expect(
      confirmation.getByText(
        copy('adminUi', 'tenantProfile.ownership.delete.saved', {
          name: OWNED_DISPLAY_NAME,
        }),
        { exact: true },
      ),
    ).toBeVisible()

    // Switch back to the account's remaining organization: the deleted one is
    // still the current tenant only through its recorded name, and a
    // tenant-scoped request that announced it would be refused before the
    // create handler runs.
    const menu = await openAccountMenu(page)
    await expect(organizationRow(page, OWNED_NAME)).toBeVisible()
    await organizationRow(page, OWNED_NAME).click()
    await menu.getByRole('menuitem', { name: 'acme', exact: true }).click()
    await expect(homeHeading(page)).toBeVisible()

    // The tombstone keeps the name: a second creation with the same name is
    // refused as an identifier conflict, and the form keeps the values.
    const createMenu = await openAccountMenu(page)
    await createMenu.getByRole('menuitem').first().click()
    await createMenu
      .getByRole('menuitem', {
        name: copy('demo', 'organization.create'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('orgCreateUi', 'page.title'),
        exact: true,
      }),
    ).toBeVisible()
    await page
      .getByLabel(copy('orgCreateUi', 'fields.name'), { exact: true })
      .fill(OWNED_NAME)
    await page
      .getByLabel(copy('orgCreateUi', 'fields.displayName'), { exact: true })
      .fill(OWNED_DISPLAY_NAME)
    await page
      .getByRole('button', { name: copy('orgCreateUi', 'submit'), exact: true })
      .click()
    await expect(
      page.getByText(copy('orgCreateUi', 'errors.org.identifier_unavailable'), {
        exact: true,
      }),
    ).toBeVisible()
    expect(new URL(page.url()).pathname).toBe('/organizations/new')
  })

  test('tenant-admin: a transfer whose target stopped being an active member is refused with the conflict copy', async ({
    demo,
    browser,
  }) => {
    test.setTimeout(scaledTimeout(300_000))

    const { page, server, accounts } = demo
    let otherContext: BrowserContext | undefined
    let inviteeContext: BrowserContext | undefined

    try {
      await accounts.signIn({
        identifier: e2eTenantAdmin.username,
        password: e2eTenantAdmin.password,
      })
      await expect(homeHeading(page)).toBeVisible()

      await createOrganization(page, {
        name: OWNED_NAME,
        displayName: OWNED_DISPLAY_NAME,
      })

      // The target is an active member when the dialog lists it.
      const link = await issueInvitation(page, server, e2eInvitee.identifier)
      inviteeContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      await acceptInvitationAsInvitee(await inviteeContext.newPage(), link)

      await openOrganizationPage(page, 'profile')
      await transferOwnerButton(page).click()
      const dialog = page.getByRole('dialog', {
        name: copy('adminUi', 'tenantProfile.ownership.transfer.title', {
          name: OWNED_DISPLAY_NAME,
        }),
        exact: true,
      })
      await expect(dialog).toBeVisible()
      await chooseSelectOption(
        dialog.getByRole('combobox', {
          name: copy('adminUi', 'tenantProfile.ownership.transfer.targetLabel'),
          exact: true,
        }),
        page.getByRole('option', {
          name: new RegExp(
            `^${escapeRegExp(e2eInvitee.displayName)} \\(.+\\)$`,
          ),
        }),
      )

      // A second administrator session suspends the target after the dialog
      // read listed it. The write that follows therefore meets a membership
      // that is no longer active, which the tenant admin answers as a
      // conflict.
      otherContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      const other = await otherContext.newPage()
      const otherAccounts = createAccounts({ page: other, server })
      await otherAccounts.signIn({
        identifier: e2eTenantAdmin.username,
        password: e2eTenantAdmin.password,
      })
      await expect(homeHeading(other)).toBeVisible()
      await switchOrganization(other, 'acme', OWNED_DISPLAY_NAME)
      await openOrganizationPage(other, 'invitations')
      await manageMemberButton(other, e2eInvitee.displayName).click()
      const panel = memberPanel(other)
      await expect(panel).toBeVisible()
      await panel
        .getByRole('button', {
          name: copy('adminUi', 'members.panel.suspend'),
          exact: true,
        })
        .click()
      await other
        .getByRole('dialog', {
          name: copy('adminUi', 'members.suspendTitle'),
          exact: true,
        })
        .getByRole('button', {
          name: copy('adminUi', 'common.confirm'),
          exact: true,
        })
        .click()
      await expect(
        panel.getByText(copy('adminUi', 'members.status.suspended'), {
          exact: true,
        }),
      ).toBeVisible()

      // The stale confirmation is answered 409; the dialog stays open and
      // states the conflict beside the chosen target.
      await dialog
        .getByRole('button', {
          name: copy('adminUi', 'tenantProfile.ownership.transfer.confirmLabel'),
          exact: true,
        })
        .click()
      const again = dialog.getByRole('button', {
        name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
        exact: true,
      })
      await expect(again).toBeEnabled()
      await again.click()
      // The refusal closes the dialog and reports beside the controls, which
      // the refused write leaves in place.
      await expect(
        ownershipSection(page).getByText(
          copy('adminUi', 'tenantProfile.ownership.error.conflict'),
          { exact: true },
        ),
      ).toBeVisible()
    } finally {
      await otherContext?.close().catch(() => undefined)
      await inviteeContext?.close().catch(() => undefined)
    }
  })

  test('tenant-admin: a stale owner whose organization was already ended is told it no longer exists', async ({
    demo,
    browser,
  }) => {
    test.setTimeout(scaledTimeout(300_000))

    const { page, server, accounts } = demo
    let operatorContext: BrowserContext | undefined

    try {
      await accounts.signIn({
        identifier: e2eTenantAdmin.username,
        password: e2eTenantAdmin.password,
      })
      await expect(homeHeading(page)).toBeVisible()

      await createOrganization(page, {
        name: OWNED_NAME,
        displayName: OWNED_DISPLAY_NAME,
      })
      // The identity card names the organization by its name, the value the
      // request surface uses; it carries no internal identifier.
      // The value cell carries the status chip beside the name, so the name
      // is read from the cell's leading text.
      const organizationName = (
        await definitionValue(
          page,
          copy('adminUi', 'tenantProfile.field.name'),
        )
          .locator('p')
          .first()
          .innerText()
      ).trim()
      expect(organizationName).toBe(OWNED_NAME)

      // Another session ends the organization through the platform surface.
      // The page under test keeps its cached profile, so its owner controls
      // are still rendered when the write below is attempted.
      operatorContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      const operator = await operatorContext.newPage()
      const operatorAccounts = createAccounts({ page: operator, server })
      await signInAndVerifyOperator(operator, server, operatorAccounts)
      await operator
        .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
        .click()
      await expect(
        operator.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toBeVisible()
      await openAccountMenu(operator)
      await adminEntry(operator).click()
      await operator
        .getByRole('link', {
          name: copy('adminUi', 'tenants.title'),
          exact: true,
        })
        .click()
      const roster = operator.getByRole('table', {
        name: copy('uiKit', 'dataTable.ariaLabel'),
        exact: true,
      })
      const created = roster
        .locator('tbody tr')
        .filter({ hasText: OWNED_NAME })
      await expect(created).toHaveCount(1)
      await created
        .getByRole('button', {
          name: new RegExp(
            `^${escapeRegExp(
              copy('adminUi', 'tenantLifecycle.deleteLabel'),
            )} .+$`,
          ),
        })
        .click()
      const platformDelete = operator.getByRole('dialog', {
        name: copy('adminUi', 'tenantLifecycle.deleteConfirm.title', {
          name: OWNED_DISPLAY_NAME,
        }),
        exact: true,
      })
      await expect(platformDelete).toBeVisible()
      // Deletion asks for the organization's name before the two-step
      // confirmation can arm.
      await platformDelete
        .getByLabel(
          copy('adminUi', 'tenantLifecycle.deleteConfirm.nameLabel'),
          { exact: true },
        )
        .fill(organizationName)
      await platformDelete
        .getByRole('button', {
          name: copy('adminUi', 'tenantLifecycle.deleteConfirm.confirmLabel'),
          exact: true,
        })
        .click()
      const platformAgain = platformDelete.getByRole('button', {
        name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
        exact: true,
      })
      await expect(platformAgain).toBeEnabled()
      await platformAgain.click()
      await expect(
        operator.getByText(
          copy('adminUi', 'tenantLifecycle.deleteConfirm.success', {
            name: organizationName,
          }),
          { exact: true },
        ),
      ).toBeVisible()

      // The stale owner's page still offers the delete control. The write
      // reaches the tombstone the other session wrote and is answered as a
      // conflict, which the ownership card states.
      await deleteOrganizationButton(page).click()
      const dialog = page.getByRole('dialog', {
        name: copy('adminUi', 'tenantProfile.ownership.delete.title', {
          name: OWNED_DISPLAY_NAME,
        }),
        exact: true,
      })
      await expect(dialog).toBeVisible()
      await dialog
        .getByLabel(
          copy('adminUi', 'tenantProfile.ownership.delete.nameLabel'),
          { exact: true },
        )
        .fill(OWNED_NAME)
      await dialog
        .getByRole('button', {
          name: copy('adminUi', 'tenantProfile.ownership.delete.confirmLabel'),
          exact: true,
        })
        .click()
      const again = dialog.getByRole('button', {
        name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
        exact: true,
      })
      await expect(again).toBeEnabled()
      await again.click()
      // The organization is gone, so the answer is the surface's not-found
      // copy. The conflict copy belongs to the member-state refusal the
      // `stopped being an active member` branch drives; a tombstone is
      // refused before the write, at the tenant resolution.
      await expect(
        ownershipSection(page).getByText(
          copy('adminUi', 'tenantProfile.ownership.error.notFound'),
          { exact: true },
        ),
      ).toBeVisible()
    } finally {
      await operatorContext?.close().catch(() => undefined)
    }
  })
})
