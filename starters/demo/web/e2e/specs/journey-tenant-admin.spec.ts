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
 * (`reviewTenantAdmin`). The seed makes it acme's only tenant administrator,
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
 *    047 are not claimed: clicking the one existing `tenant-admin` binding is
 *    the self-unbind refusal (REQ-tenant-admin-046), which the server answers
 *    before the last-administrator rule.
 *  - REQ-tenant-admin-034 covers the `platform-admin` role creation refusal.
 *    REQ-tenant-admin-048's binding half is not reachable through a rendered
 *    control any more: role binding moved into the member management panel,
 *    whose role list is the tenant's own roles, and `platform-admin` is not
 *    one of them. The refusal is the roles endpoint's, asserted by the unit
 *    tier.
 *  - REQ-tenant-admin-054 (cursor paging) is not driven: the review tenant
 *    holds one page of members and invitations, so neither pager renders.
 *  - The personal-tenant direct visit is driven as an in-app history visit
 *    (`goBack`), because a full document load re-settles the current tenant
 *    to the account's non-personal membership; the history visit keeps the
 *    personal tenant current, which is the state the requirement describes.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts; the invitation notice's subject is read from the backend
 * module's own catalog, because that is the string the mail carrier renders.
 * The only non-UI reads are the email verification code and the impersonation
 * step-up SMS code from the review process log, and the invitation mail line
 * the review carrier writes when a notice is delivered.
 */
// @journey journey-tenant-admin.md
// @covers REQ-tenant-admin-001, REQ-tenant-admin-002, REQ-tenant-admin-003, REQ-tenant-admin-004, REQ-tenant-admin-006, REQ-tenant-admin-008, REQ-tenant-admin-009, REQ-tenant-admin-010, REQ-tenant-admin-011, REQ-tenant-admin-012, REQ-tenant-admin-013, REQ-tenant-admin-014, REQ-tenant-admin-015, REQ-tenant-admin-016, REQ-tenant-admin-017, REQ-tenant-admin-022, REQ-tenant-admin-023, REQ-tenant-admin-026, REQ-tenant-admin-027, REQ-tenant-admin-029, REQ-tenant-admin-030, REQ-tenant-admin-031, REQ-tenant-admin-034, REQ-tenant-admin-041, REQ-tenant-admin-042, REQ-tenant-admin-043, REQ-tenant-admin-046, REQ-tenant-admin-048, REQ-tenant-admin-050, REQ-tenant-admin-051, REQ-tenant-admin-052, REQ-tenant-admin-053, REQ-tenant-admin-055, REQ-tenant-admin-056, REQ-tenant-admin-057, REQ-tenant-admin-058, REQ-tenant-admin-059, REQ-tenant-admin-060, REQ-tenant-admin-061, REQ-tenancy-008, REQ-tenancy-013, REQ-tenancy-015, REQ-tenancy-016, REQ-tenancy-021
import type { BrowserContext, Locator, Page } from '@playwright/test'

import {
  reviewInvitee,
  reviewOrdinaryMember,
  reviewSystemTenantAdmin,
  reviewTenantAdmin,
  reviewTenantPolicies,
  reviewUnprovedTenantAdmin,
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
import {
  adminEntry,
  closeAccountMenu,
  openAccountMenu,
  organizationRow,
} from '../helpers/account-menu'
import { copy, copyKey, copyPattern } from '../helpers/i18n'
import { totpCode } from '../helpers/totp'
import {
  catalogSection,
  cell,
  columnIndex,
  domainsSection,
  escapeRegExp,
  expectOrganizationEntryAbsent,
  expectOrganizationEntryPresent,
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
  verificationBanner,
  verificationSection,
  organizationHomeCard,
  organizationPath,
} from '../helpers/organization-admin'

/** The seeded business tenant the organization surface acts on. Its id is its alias. */
const TENANT = 'acme'

/** The non-builtin role the administrator creates. */
const ROLE = 'Support'

/** The governed permission granted to the role. */
const PERMISSION = 'authn.membership.manage'

/** The six governed permissions, in the order the catalog sorts them. */
const GOVERNED_PERMISSIONS = [
  'authn.idp.manage',
  'authn.membership.manage',
  'authz.tenant.role.manage',
  'authz.tenant.role.read',
  'settings.tenant.read',
  'settings.tenant.write',
] as const

/** A platform-domain permission the governed catalog must not offer. */
const PLATFORM_PERMISSION = 'authn.account.create'

/** The SSO provider the organization registers. */
const PROVIDER_NAME = 'Acme Organization OIDC'

/** The printed error the reserved-role and self-binding refusals render. */
function protectedText(): string {
  return copy('adminUi', 'errors.authz.protected')
}

/** The address of an invitation used to exercise resend and revoke. */
const RESENT_INVITEE = 'resent-invitee@demo.test'

/** The status cell's text of one invitation row. */
async function invitationStatus(page: Page, email: string): Promise<string> {
  const table = invitationsSection(page).getByRole('table')
  const index = await columnIndex(table, copy('adminUi', 'invitations.column.status'))
  return (await cell(invitationRow(page, email), index).innerText()).trim()
}

/** The one-time panel that hands out the issued invitation's acceptance link. */
function invitationLinkPanel(page: Page, email: string): Locator {
  return page.getByRole('status').filter({
    hasText: copy('adminUi', 'invitations.issued.link', { email }),
  })
}

/**
 * The subject the invitation notice carries in en-US: the mail template as the
 * request's language rendered it. The mail carrier writes no template key or
 * language, so this is what selects that one notice among the process log's
 * mail lines.
 */
function invitationSubject(): string {
  return copyKey('invitemail', 'authn.invitemail.subject', 'en-US', {
    tenant: TENANT,
  })
}

/** Open one member's management panel and leave it open. */
async function openMemberPanel(page: Page, displayName: string): Promise<Locator> {
  await manageMemberButton(page, displayName).click()
  const panel = memberPanel(page)
  await expect(panel).toBeVisible()
  return panel
}

/** Close one member's management panel through its close control. */
async function closeMemberPanel(
  page: Page,
  displayName: string,
): Promise<void> {
  const panel = memberPanel(page)
  await panel
    .getByRole('button', {
      name: copy('adminUi', 'members.panel.closeAria', { name: displayName }),
      exact: true,
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
  await invitationStatusFilter(page).click()
  await page
    .getByRole('option', { name: copy('adminUi', key), exact: true })
    .click()
}

/** The account's display name as the admin's accounts row carries it. */
function accountManagePrefix(): string {
  return (
    copy('adminUi', 'accounts.manageAria', { id: '__ACCOUNT_ID__' }).split(
      '__ACCOUNT_ID__',
    )[0] ?? ''
  )
}

/** Read one account's full identifier off its admin accounts row. */
async function accountIdOf(page: Page, displayName: string): Promise<string> {
  const prefix = accountManagePrefix()
  const row = page.getByRole('row').filter({ hasText: displayName })
  await expect(row).toHaveCount(1)
  const label = await row
    .getByRole('button', { name: prefix })
    .getAttribute('aria-label')
  if (label === null || !label.startsWith(prefix)) {
    throw new Error(
      `the accounts row for ${displayName} carries no identifier in its manage control`,
    )
  }
  const id = label.slice(prefix.length).trim()
  if (id === '') {
    throw new Error(`the accounts row for ${displayName} carries an empty identifier`)
  }
  return id
}

/** The impersonation page's start controls. */
function targetField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('adminUi', 'impersonations.start.accountLabel'),
    exact: true,
  })
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
  await expect(currentRow).toBeVisible({ timeout: 15_000 })
  await currentRow.click()
  const row = page
    .getByRole('menu')
    .getByRole('menuitem', { name: target, exact: true })
  await expect(row).toBeVisible({ timeout: 15_000 })
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
    test.setTimeout(600_000)

    const { page, server, accounts } = demo
    let inviteeContext: BrowserContext | undefined
    let inviteePage: Page | undefined
    let acceptanceLink = ''
    let tenantAdminAccount = ''
    let inviteeAccount = ''

    try {
      await test.step('step 1: the tenant administrator signs in and the entry and card appear', async () => {
        await accounts.signIn({
          identifier: reviewTenantAdmin.username,
          password: reviewTenantAdmin.password,
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
            name: copy('adminUi', 'invitations.title'),
            exact: true,
          }),
        ).toBeVisible()
        expect(new URL(page.url()).pathname).toBe(organizationPath('/invitations'))

        await mainNavigation(page)
          .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
          .click()
        await expect(homeHeading(page)).toBeVisible()
        await openOrganizationPage(page, 'invitations')
        expect(new URL(page.url()).pathname).toBe(organizationPath('/invitations'))
      })

      await test.step('step 3: the member table lists this tenant with names, roles, status, source and joined-at', async () => {
        const table = membersSection(page).getByRole('table')
        const nameColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.displayName'),
        )
        const accountColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.account'),
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
        const admin = memberRow(page, reviewTenantAdmin.displayName)
        await expect(admin).toHaveCount(1)
        await expect(cell(admin, nameColumn)).toHaveText(
          reviewTenantAdmin.displayName,
        )
        tenantAdminAccount = (await cell(admin, accountColumn).innerText()).trim()
        expect(
          tenantAdminAccount,
          'the member row carried no account identifier',
        ).not.toBe('')
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
        const ordinary = memberRow(page, reviewOrdinaryMember.displayName)
        await expect(ordinary).toHaveCount(1)
        await expect(cell(ordinary, rolesColumn)).toHaveText(
          copy('adminUi', 'members.roles.none'),
        )
        await expect(cell(ordinary, statusColumn)).toHaveText(
          copy('adminUi', 'members.status.active'),
        )
        await expect(cell(ordinary, sourceColumn)).toHaveText(
          copy('adminUi', 'members.source.admin'),
        )
      })

      await test.step('step 4: the member search narrows the list and distinguishes an empty search', async () => {
        // REQ-tenant-admin-052: a prefix narrows the server-side read to the
        // matching members; a query with no match says no member matches it
        // rather than reporting an empty organization.
        await memberSearchField(page).fill('Demo Member')
        await expect
          .poll(async () => memberRow(page, reviewOrdinaryMember.displayName).count(), {
            timeout: 15_000,
          })
          .toBe(1)
        await expect(memberRow(page, reviewTenantAdmin.displayName)).toHaveCount(0)

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
        await expect(memberRow(page, reviewTenantAdmin.displayName)).toHaveCount(1)
        await expect(memberRow(page, reviewOrdinaryMember.displayName)).toHaveCount(1)
      })

      await test.step('step 5: the role filter narrows the list and a ?role= address opens filtered', async () => {
        // REQ-tenant-admin-053: the role filter is server-side, writes the
        // role into the address, and a direct visit with that address opens
        // the filtered view with the filter applied.
        await memberRoleFilter(page).click()
        await page
          .getByRole('option', { name: 'tenant-admin', exact: true })
          .click()
        await expect
          .poll(async () => memberRow(page, reviewTenantAdmin.displayName).count(), {
            timeout: 15_000,
          })
          .toBe(1)
        await expect(memberRow(page, reviewOrdinaryMember.displayName)).toHaveCount(0)
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
        ).toBeVisible({ timeout: 15_000 })
        await expect
          .poll(async () => memberRow(page, reviewTenantAdmin.displayName).count(), {
            timeout: 15_000,
          })
          .toBe(1)
        await expect(memberRow(page, reviewOrdinaryMember.displayName)).toHaveCount(0)

        // Clearing the filter removes the parameter and restores both rows.
        await memberRoleFilter(page).click()
        await page
          .getByRole('option', {
            name: copy('adminUi', 'members.filter.anyRole'),
            exact: true,
          })
          .click()
        await expect
          .poll(async () => memberRow(page, reviewOrdinaryMember.displayName).count(), {
            timeout: 15_000,
          })
          .toBe(1)
        expect(new URL(page.url()).search).toBe('')
      })

      await test.step('step 6: the administrator issues an invitation, the panel shows its delivery and close is confirmed', async () => {
        // The delivery line is written before the issue answers, so the cursor
        // is taken before the submit and only this notice may answer the wait.
        const mailMark = server.log.mark()
        await page
          .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
            exact: true,
          })
          .fill(reviewInvitee.identifier)
        // The form carries the default validity beside its choice.
        await expect(
          page.getByText(
            copy('adminUi', 'invitations.issue.defaultExpiry', { days: 7 }),
            { exact: true },
          ),
        ).toBeVisible()
        await page
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issue.submit'),
            exact: true,
          })
          .click()
        await expect(
          page.getByText(
            copy('adminUi', 'invitations.issued.confirm', {
              email: reviewInvitee.identifier,
            }),
            { exact: true },
          ),
        ).toBeVisible()

        // REQ-tenant-admin-009 / REQ-tenant-admin-059: the acceptance value is
        // handed out once, with its validity, the bearer risk, the copy
        // control and the delivery result.
        const panel = invitationLinkPanel(page, reviewInvitee.identifier)
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
        // REQ-tenant-admin-061: this deployment configures a mail carrier, so
        // the panel reports the notice as sent.
        await expect(
          panel.getByText(copy('adminUi', 'invitations.issued.delivery.sent'), {
            exact: true,
          }),
        ).toBeVisible()

        // The review mail carrier wrote the notice to the process log: the
        // destination, the en-US subject the module's catalog renders, and the
        // acceptance landing the panel's link also names. The logging layer
        // masks the `spd_inv_` token wherever it appears (the token prefix is
        // a registered sensitive key name), so the body names the landing with
        // the token redacted; the panel's link is the unredacted form of the
        // same address.
        const mail = await server.waitForCarrierMessage({
          to: reviewInvitee.identifier,
          subject: invitationSubject(),
          after: mailMark,
        })
        const landing = `${server.url}/invitations/`
        expect(mail.text).toContain(landing)
        expect(acceptanceLink.startsWith(landing)).toBe(true)
        expect(mail.text).toContain(TENANT)

        // Closing is the only moment the sole copy is dropped, so it asks
        // first; keeping it leaves the panel in place.
        const close = panel.getByRole('button', {
          name: copy('adminUi', 'invitations.issued.closeAria', {
            tenant: TENANT,
            email: reviewInvitee.identifier,
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
        const row = invitationRow(page, reviewInvitee.identifier)
        await expect(row).toHaveCount(1)
        await expect(
          row.getByText(copy('adminUi', 'invitations.status.pending'), {
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          row.getByText(reviewTenantAdmin.displayName, { exact: true }),
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

        // REQ-tenancy-016: an anonymous viewer meets the sign-in surface, not
        // the invitation summary.
        await invitee.goto(acceptanceLink)
        await expect(
          invitee.getByRole('heading', {
            level: 1,
            name: copy('demo', 'signIn.heading'),
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          invitee.getByRole('heading', {
            name: copy('authUi', 'acceptInvitation.title'),
            exact: true,
          }),
        ).toHaveCount(0)
        expect(new URL(invitee.url()).pathname).toBe(invitationPath)

        await invitee
          .getByRole('textbox', {
            name: copy('authUi', 'passwordSignIn.identifierLabel'),
            exact: true,
          })
          .fill(reviewInvitee.username)
        await invitee
          .getByRole('textbox', {
            name: copy('authUi', 'passwordSignIn.passwordLabel'),
            exact: true,
          })
          .fill(reviewInvitee.password)
        await invitee
          .getByRole('button', {
            name: copy('authUi', 'passwordSignIn.submit'),
            exact: true,
          })
          .click()

        // Signing in through the landing keeps the address; the summary names
        // the tenant, the invited address and the expiry.
        await expect(
          invitee.getByRole('heading', {
            name: copy('authUi', 'acceptInvitation.title'),
            exact: true,
          }),
        ).toBeVisible({ timeout: 15_000 })
        expect(new URL(invitee.url()).pathname).toBe(invitationPath)
        await expect(
          invitee.getByText(
            copy('authUi', 'acceptInvitation.invited', { tenant: TENANT }),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(
          invitee.getByText(
            copy('authUi', 'acceptInvitation.email', {
              email: reviewInvitee.identifier,
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
        await expect(homeHeading(invitee)).toBeVisible({ timeout: 15_000 })
        expect(new URL(invitee.url()).pathname).toBe('/')
        await expect(
          invitee.getByRole('button', {
            name: copy('demo', 'home.membershipNotice.dismiss'),
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
          await expect(homeHeading(invitee)).toBeVisible({ timeout: 15_000 })
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

        // REQ-tenancy-013: an accepted link cannot be redeemed again; the
        // terminal state is the shared "no longer available" one.
        await invitee.goto(acceptanceLink)
        await expect(
          invitee.getByRole('heading', {
            name: copy('authUi', 'acceptInvitation.unavailable.title'),
            exact: true,
          }),
        ).toBeVisible({ timeout: 15_000 })
        await expect(
          invitee.getByRole('button', {
            name: copy('authUi', 'acceptInvitation.accept'),
            exact: true,
          }),
        ).toHaveCount(0)
      })

      await test.step('step 9: the administrator refreshes and the colleague is a member', async () => {
        await page
          .getByRole('button', {
            name: copy('adminUi', 'common.refresh'),
            exact: true,
          })
          .click()

        // The invitation reached its accepted terminal state.
        await expect
          .poll(() => invitationStatus(page, reviewInvitee.identifier), {
            timeout: 15_000,
          })
          .toBe(copy('adminUi', 'invitations.status.accepted'))

        // The member table gained the colleague, active, role-less and
        // invitation-sourced.
        const table = membersSection(page).getByRole('table')
        const accountColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.account'),
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
        const joined = memberRow(page, reviewInvitee.displayName)
        await expect(joined).toHaveCount(1)
        inviteeAccount = (await cell(joined, accountColumn).innerText()).trim()
        expect(inviteeAccount, 'the joined member carried no account').not.toBe(
          '',
        )
        await expect(cell(joined, sourceColumn)).toHaveText(
          copy('adminUi', 'members.source.invite'),
        )
        await expect(cell(joined, statusColumn)).toHaveText(
          copy('adminUi', 'members.status.active'),
        )
        await expect(cell(joined, rolesColumn)).toHaveText(
          copy('adminUi', 'members.roles.none'),
        )
      })

      await test.step('step 10: the administrator creates a role and grants a governed permission', async () => {
        await openOrganizationPage(page, 'roles')
        const roles = rolesSection(page)
        const rolesTable = roles.getByRole('table')

        // REQ-tenant-admin-022: the tenant's own roles, with the builtin one
        // marked; the platform domain's role is absent.
        const builtinColumn = await columnIndex(
          rolesTable,
          copy('adminUi', 'permissions.roles.column.builtin'),
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

        // REQ-tenant-admin-026: only the governed permission set is offered.
        const catalog = catalogSection(page).getByRole('table')
        for (const permission of GOVERNED_PERMISSIONS) {
          await expect(
            catalog.getByText(permission, { exact: true }),
          ).toBeVisible()
        }
        await expect(
          catalog.getByText(PLATFORM_PERMISSION, { exact: true }),
        ).toHaveCount(0)

        // REQ-tenant-admin-023: a non-builtin role with no grants yet.
        await roles
          .getByLabel(copy('adminUi', 'permissions.roles.create.roleLabel'), {
            exact: true,
          })
          .fill(ROLE)
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

        // REQ-tenant-admin-027: the administrator holds every governed
        // permission, so the grant lands and the role's table shows it.
        await roleRow
          .getByRole('button', {
            name: copy('adminUi', 'permissions.roles.manageGrantsAria', {
              role: ROLE,
            }),
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
        await grants
          .getByRole('combobox', {
            name: copy('adminUi', 'permissions.grants.permissionLabel'),
            exact: true,
          })
          .click()
        await page
          .getByRole('option', {
            name: new RegExp(`^${escapeRegExp(PERMISSION)}`),
          })
          .click()
        await grants
          .getByRole('button', {
            name: copy('adminUi', 'permissions.grants.add'),
            exact: true,
          })
          .click()
        await expect(
          page.getByText(
            copy('adminUi', 'permissions.grants.granted', {
              permission: PERMISSION,
              role: ROLE,
            }),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(
          grants.getByRole('table').getByText(PERMISSION, { exact: true }),
        ).toBeVisible()
      })

      await test.step('step 11: the member panel binds, unbinds and rebinds the role', async () => {
        // The binding belongs to the member, so the journey drives it from the
        // member's management panel rather than the roles page.
        await openOrganizationPage(page, 'invitations')

        const chooseRole = async (panel: Locator): Promise<void> => {
          await panel
            .getByRole('combobox', {
              name: copy('adminUi', 'members.panel.roleLabel'),
              exact: true,
            })
            .click()
          await page.getByRole('option', { name: ROLE, exact: true }).click()
        }
        const bindRole = async (panel: Locator): Promise<void> => {
          await panel
            .getByRole('button', {
              name: copy('adminUi', 'members.panel.bind'),
              exact: true,
            })
            .click()
        }

        const panel = await openMemberPanel(page, reviewInvitee.displayName)
        // REQ-tenant-admin-051: the panel lists the member's current bindings;
        // the joined member holds none yet.
        await expect(
          panel.getByText(copy('adminUi', 'members.panel.rolesEmpty'), {
            exact: true,
          }),
        ).toBeVisible()

        // REQ-tenant-admin-030 / REQ-tenant-admin-051: bind the role.
        await chooseRole(panel)
        await bindRole(panel)
        await expect(
          panel.getByText(
            copy('adminUi', 'members.panel.bindSaved', {
              account: reviewInvitee.displayName,
              role: ROLE,
            }),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(bindingRow(panel, ROLE)).toHaveCount(1)

        // REQ-tenant-admin-031: unbind removes it from the list.
        await bindingRow(panel, ROLE)
          .getByRole('button', {
            name: copy('adminUi', 'members.panel.unbind'),
            exact: true,
          })
          .click()
        await expect(
          panel.getByText(
            copy('adminUi', 'members.panel.bindRemoved', {
              account: reviewInvitee.displayName,
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
        await closeMemberPanel(page, reviewInvitee.displayName)

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
                  memberRow(page, reviewInvitee.displayName),
                  rolesColumn,
                ).innerText()
              ).trim(),
            { timeout: 15_000 },
          )
          .toBe(ROLE)
      })

      await test.step('step 12: the administrator suspends and resumes the member, keeping the binding', async () => {
        const table = membersSection(page).getByRole('table')
        const statusColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.status'),
        )

        const panel = await openMemberPanel(page, reviewInvitee.displayName)
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
              name: reviewInvitee.displayName,
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

        await closeMemberPanel(page, reviewInvitee.displayName)
        await expect(
          cell(memberRow(page, reviewInvitee.displayName), statusColumn),
        ).toHaveText(copy('adminUi', 'members.status.suspended'))

        // Resume restores the membership and therefore the role's effect.
        const resumed = await openMemberPanel(page, reviewInvitee.displayName)
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
        await closeMemberPanel(page, reviewInvitee.displayName)
        await expect(
          cell(memberRow(page, reviewInvitee.displayName), statusColumn),
        ).toHaveText(copy('adminUi', 'members.status.active'))
      })

      await test.step('step 13: the administrator cannot suspend itself and cannot unbind its own tenant-admin', async () => {
        const panel = await openMemberPanel(page, reviewTenantAdmin.displayName)

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
        // administrator's own; the server refuses the unbind and the binding
        // stays.
        const own = bindingRow(panel, 'tenant-admin')
        await expect(own).toHaveCount(1)
        await own
          .getByRole('button', {
            name: copy('adminUi', 'members.panel.unbind'),
            exact: true,
          })
          .click()
        await expect(own.getByText(protectedText(), { exact: true })).toBeVisible()
        await expect(bindingRow(panel, 'tenant-admin')).toHaveCount(1)

        await closeMemberPanel(page, reviewTenantAdmin.displayName)
      })

      await test.step('step 14: removing the member clears them from the list after the double confirmation', async () => {
        const panel = await openMemberPanel(page, reviewInvitee.displayName)
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
              name: reviewInvitee.displayName,
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
        await expect(memberRow(page, reviewInvitee.displayName)).toHaveCount(0)
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
            name: copy('adminUi', 'permissions.roles.manageGrantsAria', {
              role: ROLE,
            }),
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

      await test.step('step 16: the invitation resend replaces the link and the status filter narrows the list', async () => {
        await openOrganizationPage(page, 'invitations')

        // REQ-tenant-admin-060: issue a second invitation the journey can
        // resend and revoke without disturbing the accepted one.
        await page
          .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
            exact: true,
          })
          .fill(RESENT_INVITEE)
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

        // Resend asks first, states the new validity, and replaces the
        // one-time link; the mail carrier writes the new link too.
        const mailMark = server.log.mark()
        const resentRow = invitationRow(page, RESENT_INVITEE)
        await resentRow
          .getByRole('button', {
            name: copy('adminUi', 'invitations.resend'),
            exact: true,
          })
          .click()
        const resendDialog = page.getByRole('dialog', {
          name: copy('adminUi', 'invitations.resendTitle'),
          exact: true,
        })
        await expect(resendDialog).toBeVisible()
        await expect(
          resendDialog.getByText(
            copy('adminUi', 'invitations.resendMessage', {
              email: RESENT_INVITEE,
              days: 7,
            }),
            { exact: true },
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
        const resentMail = await server.waitForCarrierMessage({
          to: RESENT_INVITEE,
          subject: invitationSubject(),
          after: mailMark,
        })
        // The token is redacted in the process log; the landing and tenant are
        // the observable parts of the same notice the panel hands out.
        expect(resentMail.text).toContain(`${server.url}/invitations/`)
        expect(secondLink.startsWith(`${server.url}/invitations/`)).toBe(true)
        expect(resentMail.text).toContain(TENANT)

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

        // REQ-tenant-admin-058: each filter narrows the server read. The
        // superseded invitation is already a revoked row, so the revoked
        // filter lists it while the replacement stays pending.
        await chooseInvitationStatus(page, 'invitations.status.accepted')
        await expect(invitationRow(page, reviewInvitee.identifier)).toHaveCount(1)
        await expect(invitationRow(page, RESENT_INVITEE)).toHaveCount(0)

        await chooseInvitationStatus(page, 'invitations.status.pending')
        await expect(invitationRow(page, RESENT_INVITEE)).toHaveCount(1)
        await expect(invitationRow(page, reviewInvitee.identifier)).toHaveCount(0)

        await chooseInvitationStatus(page, 'invitations.status.revoked')
        await expect(invitationRow(page, RESENT_INVITEE)).toHaveCount(1)
        await expect(
          invitationRow(page, RESENT_INVITEE).getByText(
            copy('adminUi', 'invitations.status.revoked'),
            { exact: true },
          ),
        ).toBeVisible()

        // No invitation has expired, so that filter's empty result names the
        // filter rather than reporting an empty organization.
        await chooseInvitationStatus(page, 'invitations.status.expired')
        await expect(
          invitationsSection(page).getByText(
            copy('adminUi', 'invitations.empty.filteredDescription', {
              status: copy('adminUi', 'invitations.status.expired'),
            }),
            { exact: true },
          ),
        ).toBeVisible()

        // REQ-tenant-admin-010: revoking the pending invitation is a terminal
        // state the revoked filter then lists beside the superseded row.
        await chooseInvitationStatus(page, 'invitations.status.pending')
        await invitationRow(page, RESENT_INVITEE)
          .filter({
            hasText: copy('adminUi', 'invitations.status.pending'),
          })
          .getByRole('button', {
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
        await expect(invitationRow(page, RESENT_INVITEE)).toHaveCount(2)
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
        for (const namespace of reviewTenantPolicies.platformOnlyNamespaces) {
          await expect(page.getByText(namespace, { exact: false })).toHaveCount(0)
        }

        // This journey leaves both policies off. Turning one on would require
        // it of the single-factor review administrator from the next request
        // onward -- the write itself passes, the read after it is challenged --
        // and every later step would meet that challenge. The optimistic save,
        // the confirmation and the rollback are driven in the page's unit
        // tests; the tenant surface's write path is driven in the settings
        // module's own tests.
      })

      await test.step('step 18: a personal-tenant switch retracts the entry and a direct visit returns home', async () => {
        // A personal organization is named after the account it belongs to, so
        // the switcher row carries the administrator's own display name.
        const personal = reviewTenantAdmin.displayName

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
        await expect(homeHeading(page)).toBeVisible({ timeout: 15_000 })
        expect(new URL(page.url()).pathname).toBe('/')
        await expectOrganizationEntryAbsent(page)
        await expect(
          page.getByRole('heading', {
            level: 1,
            name: copy('adminUi', 'settings.title'),
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
    test.setTimeout(300_000)

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

    /** Set the provider's enabled flag through its edit form. */
    const setProviderEnabled = async (enabled: boolean): Promise<void> => {
      const form = providerFormSection(page)
      await form
        .getByRole('combobox', {
          name: copy('adminUi', 'sso.provider.editLabel'),
          exact: true,
        })
        .click()
      await page
        .getByRole('option', { name: PROVIDER_NAME, exact: true })
        .click()
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
      await form
        .getByRole('combobox', {
          name: copy('adminUi', 'sso.provider.enabledLabel'),
          exact: true,
        })
        .click()
      await page
        .getByRole('option', {
          name: copy(
            'adminUi',
            enabled ? 'sso.provider.tri.true' : 'sso.provider.tri.false',
          ),
          exact: true,
        })
        .click()
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
        identifier: reviewTenantAdmin.username,
        password: reviewTenantAdmin.password,
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
      await domains
        .getByRole('combobox', {
          name: copy('adminUi', 'sso.domains.providerLabel'),
          exact: true,
        })
        .click()
      await page
        .getByRole('option', { name: PROVIDER_NAME, exact: true })
        .click()
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
          name: copy('adminUi', 'sso.deleteAria', { name: PROVIDER_NAME }),
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
      identifier: reviewSystemTenantAdmin.identifier,
      password: reviewSystemTenantAdmin.password,
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
      await expect(homeHeading(page)).toBeVisible({ timeout: 15_000 })
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
    test.setTimeout(300_000)

    const { page, server, accounts } = demo

    await test.step('precondition: the operator signs in and opens the impersonation page', async () => {
      await signInOperatorWithTotp(page, accounts)
      // The operator's current organization is the platform's own, so the
      // admin entry is offered from the account menu.
      await openAccountMenu(page)
      await adminEntry(page).click()
      await expect(adminNavigation(page)).toBeVisible()

      // The target's identifier is read off the admin's own accounts table.
      await adminNavigation(page)
        .getByRole('link', {
          name: copy('adminUi', 'accounts.title'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'accounts.title'),
          exact: true,
        }),
      ).toBeVisible()
      const targetAccountId = await accountIdOf(
        page,
        reviewTenantAdmin.displayName,
      )

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

      await page
        .getByRole('combobox', {
          name: copy('adminUi', 'common.tenant'),
          exact: true,
        })
        .click()
      await page.getByRole('option', { name: TENANT, exact: true }).click()
      await targetField(page).fill(targetAccountId)
      await reasonField(page).fill(IMPERSONATION_REASON)
      await expect(startButton(page)).toBeEnabled()

      // The start is fresh enough to land here; a slow setup may pass the
      // freshness window, in which case the admin raises its step-up dialog.
      // The branch classifies the answer rather than presuming either.
      const dialog = stepUpDialog(page)
      await expect(async () => {
        if (await dialog.isVisible()) return
        if (await homeHeading(page).isVisible()) return
        await targetField(page).fill(targetAccountId)
        await reasonField(page).fill(IMPERSONATION_REASON)
        await startButton(page).click({ timeout: 3_000 })
        throw new Error('the impersonation start has not answered yet')
      }).toPass({ timeout: 60_000, intervals: [2_000] })

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

      await expect(homeHeading(page)).toBeVisible({ timeout: 15_000 })
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
        await expect(homeHeading(page)).toBeVisible({ timeout: 15_000 })
        expect(new URL(page.url()).pathname).toBe('/')
        await expect(
          page.getByRole('heading', {
            level: 1,
            name: copy('adminUi', 'settings.title'),
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
      identifier: reviewUnprovedTenantAdmin.username,
      password: reviewUnprovedTenantAdmin.password,
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
        reviewUnprovedTenantAdmin.identifier,
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
        reviewUnprovedTenantAdmin.identifier,
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
