/**
 * The tenant administrator's workspace administration surface.
 *
 * A business tenant's administrator -- a member holding the tenant's governed
 * permissions and no platform permission -- reaches the workspace admin area
 * from the tenant home. This spec drives that surface the way the person
 * does: the account menu's workspace group and the start card, the members and
 * invitations pages, the invitation handed to a colleague in a second browser
 * context, the tenant roles and grants, the bindings, the tenant settings
 * layer, the tenant switch, the enterprise providers, the `system` tenant and
 * an impersonated session.
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
 *  - The platform-domain permission grant (step 9). The workspace grant
 *    picker offers only the governed catalog (REQ-tenant-admin-026), so a
 *    platform-domain code cannot be selected. Reaching the refusal would
 *    require the direct HTTP call the honesty gate forbids.
 *  - The `platform-admin` grant (part of step 10). The same governed catalog
 *    is the only grant source, so the page cannot name `platform-admin` as a
 *    grant target. Its role creation and binding are driven, and the catalog
 *    is asserted not to offer the platform permission.
 *
 * The remaining narrow claims:
 *
 *  - REQ-tenant-admin-003 is driven as the switch retract/restore pair (to the
 *    personal tenant and back); a business-to-business switch is not driven.
 *  - REQ-tenant-admin-010 covers the invitation list's rendered columns; no
 *    invitation is revoked.
 *  - REQ-tenant-admin-011/012/014 cover the current tenant's provider list;
 *    the cross-tenant absence is the fixture's single-tenant shape and is not
 *    driven through a second workspace.
 *  - REQ-tenant-admin-033 (the last-administrator floor) and REQ-tenant-admin-
 *    047 are not claimed: clicking the one existing binding is the self-
 *    unbind refusal (REQ-tenant-admin-046), which the server answers before
 *    the last-administrator rule.
 *  - REQ-tenant-admin-034/048 cover the `platform-admin` role creation and
 *    binding refusals; the grant half is not offered by the page (above).
 *  - The personal-tenant direct visit is driven as an in-app history visit
 *    (`goBack`), because a full document load re-settles the current tenant
 *    to the account's non-personal membership; the history visit keeps the
 *    personal tenant current, which is the state the requirement describes.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts. The only non-UI reads are the email verification code and
 * the impersonation step-up SMS code from the review process log.
 */
// @journey journey-tenant-admin.md
// @covers REQ-tenant-admin-001, REQ-tenant-admin-002, REQ-tenant-admin-003, REQ-tenant-admin-004, REQ-tenant-admin-006, REQ-tenant-admin-008, REQ-tenant-admin-009, REQ-tenant-admin-010, REQ-tenant-admin-011, REQ-tenant-admin-012, REQ-tenant-admin-013, REQ-tenant-admin-014, REQ-tenant-admin-015, REQ-tenant-admin-016, REQ-tenant-admin-017, REQ-tenant-admin-022, REQ-tenant-admin-023, REQ-tenant-admin-026, REQ-tenant-admin-027, REQ-tenant-admin-029, REQ-tenant-admin-030, REQ-tenant-admin-031, REQ-tenant-admin-034, REQ-tenant-admin-041, REQ-tenant-admin-042, REQ-tenant-admin-043, REQ-tenant-admin-046, REQ-tenant-admin-048, REQ-tenant-admin-050, REQ-tenancy-008, REQ-tenancy-013, REQ-tenancy-015, REQ-tenancy-016, REQ-tenancy-021
import type { BrowserContext, Locator, Page } from '@playwright/test'

import {
  reviewInvitee,
  reviewOrdinaryMember,
  reviewSystemTenantAdmin,
  reviewTenantAdmin,
  reviewTenantSetting,
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
import { copy, copyPattern } from '../helpers/i18n'
import { totpCode } from '../helpers/totp'
import {
  bindingsSection,
  catalogSection,
  cell,
  columnIndex,
  domainsSection,
  escapeRegExp,
  expectWorkspaceEntryAbsent,
  expectWorkspaceEntryPresent,
  grantsSection,
  homeHeading,
  invitationsSection,
  lookupSection,
  mainNavigation,
  membersSection,
  openWorkspacePage,
  providerFormSection,
  rolesSection,
  settingsNamespaces,
  switchTenant,
  tenantSwitcher,
  verificationBanner,
  verificationSection,
  workspaceHomeCard,
  workspacePath,
} from '../helpers/workspace-admin'

/** The seeded business tenant the workspace surface acts on. Its id is its alias. */
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

/** The SSO provider the workspace registers. */
const PROVIDER_NAME = 'Acme Workspace OIDC'

/** The printed error the reserved-role and self-binding refusals render. */
function protectedText(): string {
  return copy('adminUi', 'errors.authz.protected')
}

/** The invitation list's row for one address. */
function invitationRow(page: Page, email: string): Locator {
  return invitationsSection(page)
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: email })
}

/** One member table's row, found by the display name it renders. */
function memberRow(page: Page, displayName: string): Locator {
  return membersSection(page)
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: displayName })
}

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

/** The header's admin entry. */
function adminEntry(page: Page): Locator {
  return page.getByRole('banner').getByRole('link', {
    name: copy('demo', 'nav.admin'),
    exact: true,
  })
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
  'Support ticket 4712: the workspace admin surface during impersonation.'

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

test.describe('the tenant administrator manages the workspace', () => {
  test.use({
    seedTenantAdmin: true,
    seedInvitee: true,
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: the administrator invites, governs and switches the workspace', async ({
    demo,
    browser,
  }) => {
    // The journey drives an invitation through a second context, the role
    // surface, the settings write and two tenant switches; the tier's default
    // bound covers one server and one journey, not this many settle points.
    test.setTimeout(300_000)

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
        // workspace group -- on both account-menu surfaces -- and the start
        // card, with one real link per openable page. The helper asserts the
        // group label and every entry and closes each menu, so the caller's
        // next click lands on the page.
        await expectWorkspaceEntryPresent(page)
      })

      await test.step('step 2: the card and the entry land on the same first page', async () => {
        await workspaceHomeCard(page).click()
        await expect(
          page.getByRole('heading', {
            level: 1,
            name: copy('adminUi', 'invitations.title'),
            exact: true,
          }),
        ).toBeVisible()
        expect(new URL(page.url()).pathname).toBe(workspacePath('/invitations'))

        await mainNavigation(page)
          .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
          .click()
        await expect(homeHeading(page)).toBeVisible()
        await openWorkspacePage(page, 'invitations')
        expect(new URL(page.url()).pathname).toBe(workspacePath('/invitations'))
      })

      await test.step('step 3: the member table lists this tenant with names and status', async () => {
        const table = membersSection(page).getByRole('table')
        const nameColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.displayName'),
        )
        const accountColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.account'),
        )
        const activeColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.active'),
        )
        const sourceColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.source'),
        )

        // REQ-tenant-admin-008: the current administrator is in its own list,
        // with the name, account, active state and source the record carries.
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
        await expect(cell(admin, activeColumn)).toHaveText(
          copy('adminUi', 'members.activeYes'),
        )
        await expect(cell(admin, sourceColumn)).toHaveText(
          copy('adminUi', 'members.source.admin'),
        )

        // The template's ordinary member is here too; nothing from another
        // tenant is.
        const ordinary = memberRow(page, reviewOrdinaryMember.displayName)
        await expect(ordinary).toHaveCount(1)
        await expect(cell(ordinary, sourceColumn)).toHaveText(
          copy('adminUi', 'members.source.admin'),
        )
      })

      await test.step('step 4: the administrator issues an invitation and the one-time link is shown', async () => {
        await page
          .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
            exact: true,
          })
          .fill(reviewInvitee.identifier)
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

        // REQ-tenant-admin-009: the acceptance value is handed out once, as
        // the host's absolute link, with a copy control.
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

        // The panel stays until the operator closes it; after the close the
        // link is no longer on the page.
        await panel
          .getByRole('button', {
            name: copy('adminUi', 'invitations.issued.closeAria', {
              tenant: TENANT,
              email: reviewInvitee.identifier,
            }),
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
      })

      await test.step('step 5: a colleague opens the link, signs in and accepts', async () => {
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
        await expect(tenantSwitcher(invitee, TENANT)).toBeVisible()
      })

      await test.step('step 6: the colleague is an ordinary member and sees no workspace entry', async () => {
        if (inviteePage === undefined) {
          throw new Error('the colleague page was never opened')
        }
        const invitee = inviteePage

        // REQ-tenant-admin-001's ordinary-member facet: no permission covers a
        // workspace page, so neither entry exists.
        await expectWorkspaceEntryAbsent(invitee)

        // REQ-tenant-admin-004: a direct visit to the area root or a subpath
        // falls back to the tenant home, with no workspace content and no
        // retryable failure page.
        for (const path of [workspacePath(''), workspacePath('/invitations')]) {
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
              name: copy('demo', 'workspace.accessFailed.title'),
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

      await test.step('step 7: the administrator refreshes and the colleague is a member', async () => {
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

        // The member table gained the colleague, active and invitation-sourced.
        const table = membersSection(page).getByRole('table')
        const accountColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.account'),
        )
        const sourceColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.source'),
        )
        const activeColumn = await columnIndex(
          table,
          copy('adminUi', 'members.column.active'),
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
        await expect(cell(joined, activeColumn)).toHaveText(
          copy('adminUi', 'members.activeYes'),
        )
      })

      await test.step('step 8: the administrator creates a role and grants a governed permission', async () => {
        await openWorkspacePage(page, 'roles')
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

      await test.step('step 9: the administrator binds, unbinds and rebinds the role', async () => {
        const bindings = bindingsSection(page)
        const bindingsTable = bindings.getByRole('table')
        const accountField = bindings.getByRole('combobox', {
          name: copy('adminUi', 'permissions.bindings.accountLabel'),
          exact: true,
        })

        const chooseAccount = async (): Promise<void> => {
          await accountField.click()
          await accountField.fill(reviewInvitee.displayName)
          await page
            .getByRole('option', {
              name: new RegExp(`^${escapeRegExp(reviewInvitee.displayName)}`),
            })
            .click()
        }
        const chooseRole = async (role: string): Promise<void> => {
          await bindings
            .getByLabel(copy('adminUi', 'permissions.bindings.roleLabel'), {
              exact: true,
            })
            .fill(role)
        }
        const bind = async (): Promise<void> => {
          await bindings
            .getByRole('button', {
              name: copy('adminUi', 'permissions.bindings.bind'),
              exact: true,
            })
            .click()
        }

        // REQ-tenant-admin-030: bind the role to the joined member.
        await chooseAccount()
        await chooseRole(ROLE)
        await bind()
        await expect(
          page.getByText(
            copy('adminUi', 'permissions.bindings.bound', {
              account: copy('adminUi', 'common.accountReference.name', {
                name: reviewInvitee.displayName,
              }),
              role: ROLE,
            }),
            { exact: true },
          ),
        ).toBeVisible()
        const supportBinding = bindingsTable
          .locator('tbody tr')
          .filter({ hasText: ROLE })
        await expect(supportBinding).toHaveCount(1)
        await expect(
          supportBinding.getByText(reviewInvitee.displayName, { exact: true }),
        ).toBeVisible()

        // REQ-tenant-admin-029: the account lookup lists the same binding.
        const lookup = lookupSection(page)
        await lookup
          .getByRole('combobox', {
            name: copy('adminUi', 'permissions.lookup.accountLabel'),
            exact: true,
          })
          .click()
        await lookup
          .getByRole('combobox', {
            name: copy('adminUi', 'permissions.lookup.accountLabel'),
            exact: true,
          })
          .fill(reviewInvitee.displayName)
        await page
          .getByRole('option', {
            name: new RegExp(`^${escapeRegExp(reviewInvitee.displayName)}`),
          })
          .click()
        await lookup
          .getByRole('button', {
            name: copy('adminUi', 'permissions.lookup.apply'),
            exact: true,
          })
          .click()
        await expect(
          lookup.getByRole('table').locator('tbody tr').filter({ hasText: ROLE }),
        ).toHaveCount(1)

        // REQ-tenant-admin-031: the unbind removes it from both reads.
        await supportBinding
          .getByRole('button', {
            name: copy('adminUi', 'permissions.bindings.unbindAria', {
              account: reviewInvitee.displayName,
              role: ROLE,
            }),
            exact: true,
          })
          .click()
        await expect(
          page.getByText(
            copy('adminUi', 'permissions.bindings.unbound', {
              account: copy('adminUi', 'common.accountReference.name', {
                name: reviewInvitee.displayName,
              }),
              role: ROLE,
            }),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(
          bindingsTable.locator('tbody tr').filter({ hasText: ROLE }),
        ).toHaveCount(0)
        await expect(
          lookup.getByRole('heading', {
            name: copy('adminUi', 'permissions.lookup.empty.title'),
            exact: true,
          }),
        ).toBeVisible()

        // The rebind restores it.
        await chooseAccount()
        await chooseRole(ROLE)
        await bind()
        await expect(supportBinding).toHaveCount(1)
        await expect(
          lookup.getByRole('table').locator('tbody tr').filter({ hasText: ROLE }),
        ).toHaveCount(1)
      })

      await test.step('step 10: the platform-admin writes are refused and change nothing', async () => {
        const roles = rolesSection(page)
        const rolesTable = roles.getByRole('table')
        const bindings = bindingsSection(page)
        const bindingsTable = bindings.getByRole('table')

        // REQ-tenant-admin-034/048, role creation: the reserved name is
        // refused and the catalog does not gain a row.
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

        // The binding of the reserved role is refused the same way.
        const accountField = bindings.getByRole('combobox', {
          name: copy('adminUi', 'permissions.bindings.accountLabel'),
          exact: true,
        })
        await accountField.click()
        await accountField.fill(reviewInvitee.displayName)
        await page
          .getByRole('option', {
            name: new RegExp(`^${escapeRegExp(reviewInvitee.displayName)}`),
          })
          .click()
        await bindings
          .getByLabel(copy('adminUi', 'permissions.bindings.roleLabel'), {
            exact: true,
          })
          .fill('platform-admin')
        await bindings
          .getByRole('button', {
            name: copy('adminUi', 'permissions.bindings.bind'),
            exact: true,
          })
          .click()
        await expect(
          bindings.getByRole('alert').getByText(protectedText(), {
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          bindingsTable.locator('tbody tr').filter({ hasText: 'platform-admin' }),
        ).toHaveCount(0)

        // The governed catalog is the grant source, so the page cannot even
        // name the platform role as a grant target.
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

      await test.step('step 11: the administrator cannot unbind its own tenant-admin binding', async () => {
        const bindings = bindingsSection(page)
        const bindingsTable = bindings.getByRole('table')

        // REQ-tenant-admin-046: the one tenant-admin binding is the
        // administrator's own; the server refuses the unbind and the row
        // stays.
        const own = bindingsTable
          .locator('tbody tr')
          .filter({ hasText: 'tenant-admin' })
        await expect(own).toHaveCount(1)
        await expect(
          own.getByText(reviewTenantAdmin.displayName, { exact: true }),
        ).toBeVisible()
        await own.getByRole('button', {
          name: copy('adminUi', 'permissions.bindings.unbindAria', {
            account: reviewTenantAdmin.displayName,
            role: 'tenant-admin',
          }),
          exact: true,
        }).click()
        await expect(
          bindings.getByRole('alert').getByText(protectedText(), {
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          bindingsTable.locator('tbody tr').filter({ hasText: 'tenant-admin' }),
        ).toHaveCount(1)
      })

      await test.step('step 12: the settings page reports the source layer and a tenant write lands', async () => {
        await openWorkspacePage(page, 'settings')

        // REQ-tenant-admin-016: the source column is rendered, and the
        // platform-only namespaces are not offered at all.
        await expect(
          page.getByRole('columnheader', {
            name: copy('adminUi', 'settings.column.source'),
            exact: true,
          }),
        ).toBeVisible()
        const namespaces = settingsNamespaces(page)
        await expect(
          namespaces.getByRole('button', {
            name: /^(authn|ratelimit|tenant\.admission)\./,
          }),
        ).toHaveCount(0)
        const delivery = namespaces.getByRole('button', {
          name: reviewTenantSetting.namespace,
          exact: true,
        })
        await expect(delivery).toBeVisible()
        await delivery.click()

        const table = page.getByRole('table')
        const row = table
          .locator('tbody tr')
          .filter({ hasText: reviewTenantSetting.key })
        // The namespace change re-reads the register; wait for the row before
        // reading the columns, because the pending read renders a skeleton
        // with no table at all.
        await expect(row).toHaveCount(1)
        const sourceColumn = await columnIndex(
          table,
          copy('adminUi', 'settings.column.source'),
        )
        const stateColumn = await columnIndex(
          table,
          copy('adminUi', 'settings.column.state'),
        )
        await expect(cell(row, sourceColumn)).toHaveText(
          copy('adminUi', 'settings.source.default'),
        )
        await expect(cell(row, stateColumn)).toHaveText(
          copy('adminUi', 'settings.state.default'),
        )
        const field = page.getByRole('textbox', {
          name: reviewTenantSetting.key,
          exact: true,
        })
        await expect(field).toHaveValue(reviewTenantSetting.defaultValue)

        // REQ-tenant-admin-017: the write lands on the tenant layer, and the
        // row reports the new value, the overridden state and the tenant
        // source.
        await field.fill(reviewTenantSetting.value)
        await page
          .getByRole('button', {
            name: copy('adminUi', 'settings.saveBar.save'),
            exact: true,
          })
          .click()
        await expect(
          page.getByText(copy('adminUi', 'settings.saved'), { exact: true }),
        ).toBeVisible()
        await expect(field).toHaveValue(reviewTenantSetting.value)
        await expect(cell(row, sourceColumn)).toHaveText(
          copy('adminUi', 'settings.source.tenant'),
        )
        await expect(cell(row, stateColumn)).toHaveText(
          copy('adminUi', 'settings.state.overridden'),
        )
      })

      await test.step('step 13: a personal-tenant switch retracts the entry and a direct visit returns home', async () => {
        const personal = copy('demo', 'workspace.personal')

        // REQ-tenant-admin-003/006: the switch retracts the entry, the account
        // menu's workspace group and the card at once.
        await switchTenant(page, TENANT, personal)
        await expect(homeHeading(page)).toBeVisible()
        await expectWorkspaceEntryAbsent(page)

        // A direct visit while the personal tenant is current: history
        // navigation leaves the module's current tenant in place, so the
        // workspace URL is reached with the personal tenant still current.
        // The full-document load a fresh address would perform re-settles the
        // account's non-personal membership, so it cannot exercise this
        // branch; the history visit reaches the same guard.
        await page.goBack()
        await expect(homeHeading(page)).toBeVisible({ timeout: 15_000 })
        expect(new URL(page.url()).pathname).toBe('/')
        await expectWorkspaceEntryAbsent(page)
        await expect(
          page.getByRole('heading', {
            level: 1,
            name: copy('adminUi', 'settings.title'),
            exact: true,
          }),
        ).toHaveCount(0)

        // Switching back restores the entry once the new permission set
        // settles.
        await switchTenant(page, personal, TENANT)
        await expect(homeHeading(page)).toBeVisible()
        await expectWorkspaceEntryPresent(page)
      })
    } finally {
      // The colleague's context holds its own cookie session; closing it keeps
      // a failed run from leaving a second context open while the server
      // drains.
      await inviteeContext?.close().catch(() => undefined)
    }
  })
})

test.describe('the workspace SSO page', () => {
  test.use({
    seedTenantAdmin: true,
    enterpriseSSO: true,
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: the workspace registers, verifies, disables and removes an SSO provider', async ({
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

    await test.step('step 1: the workspace SSO page starts empty for this tenant', async () => {
      await signInAdmin()
      await openWorkspacePage(page, 'sso')
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
      await openWorkspacePage(page, 'sso')
      await setProviderEnabled(true)
      await discover(true)
    })

    await test.step('step 6: deleting the provider removes it and its domain from discovery', async () => {
      await signInAdmin()
      await openWorkspacePage(page, 'sso')
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
    await expectWorkspaceEntryAbsent(page)

    for (const path of [
      workspacePath(''),
      workspacePath('/invitations'),
      workspacePath('/settings'),
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
      await expectWorkspaceEntryAbsent(page)
    }
  })
})

test.describe('an impersonated session hides the workspace', () => {
  test.use({
    seedTenantAdmin: true,
    seedImpersonation: true,
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: impersonating the administrator renders no workspace surface', async ({
    demo,
  }) => {
    test.setTimeout(300_000)

    const { page, server, accounts } = demo

    await test.step('precondition: the operator signs in and opens the impersonation page', async () => {
      await signInOperatorWithTotp(page, accounts)
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
      await expectWorkspaceEntryAbsent(page)
    })

    await test.step('step 2: a direct visit comes back to the tenant home', async () => {
      for (const path of [
        workspacePath('/invitations'),
        workspacePath('/settings'),
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
    await expectWorkspaceEntryPresent(page)

    await openWorkspacePage(page, 'roles')

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

    await test.step('step 1: the workspace write is refused with the contact-required text', async () => {
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
      await openWorkspacePage(page, 'roles')

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
