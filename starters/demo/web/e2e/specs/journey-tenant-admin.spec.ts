// @journey journey-tenant-admin.md
// @covers REQ-tenant-admin-001, REQ-tenant-admin-002, REQ-tenant-admin-003, REQ-tenant-admin-004, REQ-tenant-admin-006, REQ-tenant-admin-008, REQ-tenant-admin-009, REQ-tenant-admin-010, REQ-tenant-admin-011, REQ-tenant-admin-012, REQ-tenant-admin-013, REQ-tenant-admin-014, REQ-tenant-admin-015, REQ-tenant-admin-016, REQ-tenant-admin-017, REQ-tenant-admin-018, REQ-tenant-admin-022, REQ-tenant-admin-023, REQ-tenant-admin-025, REQ-tenant-admin-026, REQ-tenant-admin-027, REQ-tenant-admin-028, REQ-tenant-admin-029, REQ-tenant-admin-030, REQ-tenant-admin-031, REQ-tenant-admin-034, REQ-tenant-admin-041, REQ-tenant-admin-042, REQ-tenant-admin-043, REQ-tenant-admin-046, REQ-tenant-admin-048, REQ-tenant-admin-050, REQ-tenant-admin-051, REQ-tenant-admin-052, REQ-tenant-admin-053, REQ-tenant-admin-055, REQ-tenant-admin-056, REQ-tenant-admin-057, REQ-tenant-admin-058, REQ-tenant-admin-059, REQ-tenant-admin-060, REQ-tenant-admin-061, REQ-tenant-admin-062, REQ-tenant-admin-063, REQ-tenant-admin-064, REQ-tenant-admin-065, REQ-tenant-admin-066, REQ-tenant-admin-068, REQ-tenancy-008, REQ-tenancy-013, REQ-tenancy-015, REQ-tenancy-016, REQ-tenancy-021, REQ-tenancy-060, REQ-tenancy-061, REQ-tenancy-062, REQ-tenancy-064, REQ-tenancy-080, REQ-tenancy-081
import type { BrowserContext, Locator, Page } from '@playwright/test'

import {
  e2eImpersonationFactors,
  e2eInvitee,
  e2eOrdinaryMember,
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
  expandAccountRows,
  selectAccountSection,
} from '../helpers/account-sections'
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

const TENANT = 'acme'

const ORDINARY_MEMBER_USERNAME = 'member'

const ROLE = 'Support'

const ROLE_DESCRIPTION = 'Reviews support requests and answers audit questions.'

const PERMISSION = 'authn.membership.manage'

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

const PLATFORM_PERMISSION = 'authn.account.create'

const PROVIDER_NAME = 'Acme Organization OIDC'

function protectedText(): string {
  return copy('adminUi', 'errors.authz.protected')
}

function permissionDescription(permission: string): string {
  return copyKey(
    'authzPermissions',
    `authz.permission.${permission.replace(/[.:]/g, '-')}`,
  )
}

const RESENT_INVITEE = 'resent-invitee@demo.test'

const ROLE_INVITEE = 'role-invitee@demo.test'
const ROLE_INVITEE_IDENTITY = {
  email: ROLE_INVITEE,
  username: 'role-invitee',
  displayName: 'Role Invitee',
  password: 'role-invitee-secret',
} as const

async function invitationStatus(page: Page, email: string): Promise<string> {
  const table = invitationsSection(page).getByRole('table')
  const index = await columnIndex(table, copy('adminUi', 'invitations.column.status'))
  const text = await cell(invitationRow(page, email), index).innerText()
  return text.split('\n')[0]!.trim()
}

function invitationLinkPanel(page: Page, email: string): Locator {
  return page.getByRole('status').filter({
    hasText: copy('adminUi', 'invitations.issued.link', { email }),
  })
}

async function openMemberPanel(page: Page, displayName: string): Promise<Locator> {
  await manageMemberButton(page, displayName).click()
  const panel = memberPanel(page)
  await expect(panel).toBeVisible()
  return panel
}

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

function bindingRow(panel: Locator, role: string): Locator {
  return panel.locator('li').filter({ hasText: role })
}

async function chooseInvitationStatus(
  page: Page,
  key: string,
): Promise<void> {
  await chooseSelectOption(
    invitationStatusFilter(page),
    page.getByRole('option', { name: copy('adminUi', key), exact: true }),
  )
}

function targetField(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'impersonations.start.accountLabel'),
    exact: true,
  })
}

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

function adminNavigation(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

function currentOrganizationRow(page: Page, name: string): Locator {
  return page.getByRole('menu').getByText(name, { exact: true })
}

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

const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

const MASKED_INVITEE = 'i***@demo.test'

const TOTP_SEED = e2eImpersonationFactors.totpSeed

const OPERATOR_PHONE = '+8613900139000'

const IMPERSONATION_REASON =
  'Support ticket 4712: the organization admin surface during impersonation.'

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

function phoneSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.phone'),
    exact: true,
  })
}

function emailSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.email'),
    exact: true,
  })
}

async function proveOperatorEmail(
  page: Page,
  server: E2EServer,
): Promise<void> {
  await selectAccountSection(page, 'profile')
  await expandAccountRows(page)
  const email = emailSection(page)
  const before = server.log.mark()
  await email
    .getByRole('button', {
      name: copy('accountUi', 'verification.send'),
      exact: true,
    })
    .click()
  const { code } = await waitForVerificationCode(
    server.log,
    'mail',
    OPERATOR.identifier,
    { after: before },
  )
  await email
    .getByRole('textbox', {
      name: copy('accountUi', 'verification.codeLabel'),
      exact: true,
    })
    .fill(code)
  await email
    .getByRole('button', {
      name: copy('accountUi', 'verification.verify'),
      exact: true,
    })
    .click()
  await expect(
    email.getByText(copy('accountUi', 'identifiers.verified'), {
      exact: true,
    }),
  ).toBeVisible()
}

async function proveOperatorPhone(
  page: Page,
  server: E2EServer,
): Promise<void> {
  await selectAccountSection(page, 'profile')
  await expandAccountRows(page)
  const phone = phoneSection(page)
  await phone
    .getByRole('button', {
      name: copy('demo', 'account.action.add'),
      exact: true,
    })
    .click()
  await phone
    .getByRole('combobox', {
      name: copy('accountUi', 'phone.country.label'),
      exact: true,
    })
    .fill('86')
  await page.getByRole('option', { name: /\+86$/ }).click()
  await phone
    .getByRole('textbox', {
      name: copy('accountUi', 'phone.phone.label'),
      exact: true,
    })
    .fill('13900139000')
  const before = server.log.mark()
  await phone
    .getByRole('button', {
      name: copy('accountUi', 'phone.send'),
      exact: true,
    })
    .click()
  const { code } = await waitForVerificationCode(
    server.log,
    'sms',
    OPERATOR_PHONE,
    { after: before },
  )
  await phone
    .getByRole('textbox', {
      name: copy('accountUi', 'phone.code.label'),
      exact: true,
    })
    .fill(code)
  await phone
    .getByRole('button', {
      name: copy('accountUi', 'phone.verify'),
      exact: true,
    })
    .click()
  await expect(
    page.getByText(copy('accountUi', 'phone.added', { phone: OPERATOR_PHONE }), {
      exact: true,
    }),
  ).toBeVisible()
}

test.describe('the tenant administrator manages the organization', () => {
  test.use({
    seedTenantAdmin: true,
    seedInvitee: true,
    identityQuota: '10000',
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: the administrator invites, governs and switches the organization', async ({
    demo,
    browser,
  }) => {
    test.setTimeout(scaledTimeout(600_000))

    const { page, server, accounts } = demo
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

        const admin = memberRow(page, e2eTenantAdmin.displayName)
        await expect(admin).toHaveCount(1)
        await expect(cell(admin, nameColumn)).toHaveText(
          e2eTenantAdmin.displayName,
        )
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

        const ids = await accountIds.settled()
        await expectNoAccountIds(invitationsSection(page), ids)
        await expectNoAccountIds(membersSection(page), ids)
      })

      await test.step('step 4: the member search narrows the list and distinguishes an empty search', async () => {
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
        await expect(
          panel.getByText(
            copy('adminUi', 'invitations.issued.delivery.not_configured'),
            { exact: true },
          ),
        ).toBeVisible()
        expect(acceptanceLink.startsWith(`${server.url}/invitations/`)).toBe(
          true,
        )

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

        const row = invitationRow(page, e2eInvitee.identifier)
        await expect(row).toHaveCount(1)
        await expect(
          row.getByText(copy('adminUi', 'invitations.status.pending'), {
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          row.getByText(e2eTenantAdmin.displayName, { exact: true }),
        ).toBeVisible()
        const expiryColumn = await columnIndex(
          invitationsSection(page).getByRole('table'),
          copy('adminUi', 'invitations.column.expires'),
        )
        await expect(cell(row, expiryColumn)).not.toBeEmpty()
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
        await openAccountMenu(invitee)
        await expect(currentOrganizationRow(invitee, TENANT)).toBeVisible()
        await closeAccountMenu(invitee)
      })

      await test.step('step 8: the colleague is an ordinary member and sees no organization entry', async () => {
        if (inviteePage === undefined) {
          throw new Error('the colleague page was never opened')
        }
        const invitee = inviteePage

        await expectOrganizationEntryAbsent(invitee)

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
        await page.reload()
        await expect(
          invitationsSection(page).getByRole('heading', {
            name: copy('adminUi', 'invitations.empty.filteredTitle'),
            exact: true,
          }),
        ).toBeVisible()

        await chooseInvitationStatus(page, 'invitations.status.accepted')
        await expect
          .poll(() => invitationStatus(page, e2eInvitee.identifier))
          .toBe(copy('adminUi', 'invitations.status.accepted'))

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

        const ids = await accountIds.settled()
        await expectNoAccountIds(invitationsSection(page), ids)
        await expectNoAccountIds(membersSection(page), ids)
      })

      await test.step('step 10: the administrator creates a described role, grants and revokes a governed permission, and reads the usage', async () => {
        await openOrganizationPage(page, 'roles')
        const roles = rolesSection(page)
        const rolesTable = roles.getByRole('table')

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

        const catalog = await expandCatalogSection(page)
        for (const permission of GOVERNED_PERMISSIONS) {
          await expect(
            catalog.getByText(permission, { exact: true }),
          ).toBeVisible()
        }
        await expect(
          catalog.getByText(PLATFORM_PERMISSION, { exact: true }),
        ).toHaveCount(0)

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
        await expect(cell(roleRow, descriptionColumn)).toHaveText(
          ROLE_DESCRIPTION,
        )
        await expect(cell(roleRow, permissionsColumn)).toHaveText('0')
        await expect(cell(roleRow, membersColumn)).toHaveText('0')

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
        await expect(
          panel.getByText(copy('adminUi', 'members.panel.rolesEmpty'), {
            exact: true,
          }),
        ).toBeVisible()

        const ids = await accountIds.settled()
        await expectNoAccountIds(panel, ids)

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

        await page
          .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
            exact: true,
          })
          .fill(RESENT_INVITEE)
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

        const issuedRow = invitationRow(page, RESENT_INVITEE)
        await expect(issuedRow).toHaveCount(1)
        const roleColumn = await columnIndex(
          invitationsSection(page).getByRole('table'),
          copy('adminUi', 'invitations.column.role'),
        )
        await expect(cell(issuedRow, roleColumn)).toHaveText(ROLE)

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

        await expect(invitationRow(page, RESENT_INVITEE)).toHaveCount(1)
        await expect(
          invitationRow(page, RESENT_INVITEE).getByText(
            copyPattern('adminUi', 'invitations.created.resentOnce', {
              time: '.+',
            }),
          ),
        ).toBeVisible()
        await expect(
          cell(invitationRow(page, RESENT_INVITEE), roleColumn),
        ).toHaveText(ROLE)

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

        await chooseInvitationStatus(page, 'invitations.status.accepted')
        await expect(invitationRow(page, e2eInvitee.identifier)).toHaveCount(1)
        await expect(invitationRow(page, RESENT_INVITEE)).toHaveCount(0)

        await chooseInvitationStatus(page, 'invitations.status.pending')
        await expect(invitationRow(page, RESENT_INVITEE)).toHaveCount(1)
        await expect(invitationRow(page, e2eInvitee.identifier)).toHaveCount(0)

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

      })

      await test.step('step 18: a personal-tenant switch retracts the entry and a direct visit returns home', async () => {
        const personal = e2eTenantAdmin.displayName

        await switchOrganization(page, TENANT, personal)
        await expect(homeHeading(page)).toBeVisible()
        await expectOrganizationEntryAbsent(page)

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

        await switchOrganization(page, personal, TENANT)
        await expect(homeHeading(page)).toBeVisible()
        await expectOrganizationEntryPresent(page)
      })
    } finally {
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
      await expect(
        page.getByRole('heading', {
          name: copy('adminUi', 'sso.empty.title'),
          exact: true,
        }),
      ).toBeVisible()
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

    await accounts.signIn({
      identifier: e2eSystemTenantAdmin.identifier,
      password: e2eSystemTenantAdmin.password,
    })
    await expect(homeHeading(page)).toBeVisible()

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
      await proveOperatorEmail(page, server)
      await proveOperatorPhone(page, server)
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
    await proveOperatorEmail(page, server)
    await openOrganizationPage(page, 'settings')

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
    await expect(ssoSwitch).not.toBeChecked()
    await expect(ssoSwitch).toBeDisabled()
    await expect(
      page.getByText(copy('adminUi', 'organization.security.sso.noProvider'), {
        exact: true,
      }),
    ).toBeVisible()

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
    seedImpersonation: true,
    tenantAdmins: { acme: [OPERATOR.identifier] },
    viewport: { width: 1920, height: 1080 },
  })

  test('tenant-admin: an administrator turns a policy on, reads it back, and turns it off', async ({
    demo,
  }) => {
    test.setTimeout(scaledTimeout(300_000))

    const { page, server, accounts } = demo
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
    await proveOperatorEmail(page, server)
    await openOrganizationPage(page, 'settings')

    await expect(mfaSwitch()).not.toBeChecked()
    await expect(mfaSwitch()).toBeEnabled()
    await expect(ssoSwitch()).not.toBeChecked()
    await expect(
      page.getByText(copy('adminUi', 'organization.security.status.disabled'), {
        exact: true,
      }),
    ).toHaveCount(2)

    await test.step('turning the policy on saves it immediately', async () => {
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

const OWNED_NAME = 'acme-lab'
const OWNED_DISPLAY_NAME = 'Acme Lab'
const RENAMED_ORG = 'Acme Lab Renamed'
const RENAMED_DESCRIPTION =
  'The renamed lab organization the profile save writes back.'
const OWNED_DESCRIPTION = 'The lab organization the ownership branches create.'
const OWNED_CONTACT_EMAIL = 'lab@demo.test'
const OWNED_WEBSITE = 'https://lab.demo.test'

function definitionValue(page: Page, label: string): Locator {
  return page
    .locator('dt')
    .filter({ hasText: new RegExp(`^${escapeRegExp(label)}$`) })
    .locator('xpath=following-sibling::dd[1]')
}

interface ProfileOwner {
  readonly owner_account: string
  readonly owner_display_name: string
  readonly owner_username: string
}

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
    page.getByRole('option', {
      name: new RegExp(`^${escapeRegExp(target.displayName)} \\(.+\\)$`),
    }),
  )

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

function inboxList(page: Page): Locator {
  return page.getByRole('list', {
    name: copy('notificationUi', 'inbox.title'),
  })
}

interface InboxMessage {
  readonly title: string
  readonly body: string
  readonly params: Record<string, unknown> | null
}

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
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('adminUi', 'organization.nav.profile'),
      exact: true,
    }),
  ).toBeVisible()
  expect(new URL(page.url()).pathname).toBe(organizationPath('/profile'))
}

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
  await mainNavigation(page)
    .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
    .click()
  await expect(homeHeading(page)).toBeVisible()
  return link
}

async function acceptInvitationAsInvitee(
  invitee: Page,
  link: string,
  account: {
    readonly username: string
    readonly password: string
  } = e2eInvitee,
): Promise<void> {
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

    const profileRead = waitForProfileOwner(page, OWNED_NAME)
    await createOrganization(page, {
      name: OWNED_NAME,
      displayName: OWNED_DISPLAY_NAME,
      description: OWNED_DESCRIPTION,
    })
    const profileOwner = await profileRead

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
    await expect(page.getByText(profileOwner.owner_account)).toHaveCount(0)
    expect(await page.content()).not.toContain(profileOwner.owner_account)
    await expect(
      definitionValue(page, copy('adminUi', 'tenantProfile.field.name')),
    ).toContainText(OWNED_NAME)
    await expect(ownershipSection(page)).toBeVisible()
    await expect(transferOwnerButton(page)).toBeVisible()
    await expect(deleteOrganizationButton(page)).toBeVisible()

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

    await openAccountMenu(page)
    await expect(organizationRow(page, RENAMED_ORG)).toBeVisible()
    await closeAccountMenu(page)

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

      const link = await issueInvitation(page, server, e2eInvitee.identifier)
      inviteeContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      const invitee = await inviteeContext.newPage()
      await acceptInvitationAsInvitee(invitee, link)
      await bindMemberRole(page, e2eInvitee.displayName, 'tenant-admin')

      const inviteeAccounts = createAccounts({ page: invitee, server })
      await inviteeAccounts.signOut()
      await inviteeAccounts.signIn({
        identifier: e2eInvitee.username,
        password: e2eInvitee.password,
      })
      await expect(homeHeading(invitee)).toBeVisible()

      await openOrganizationPage(invitee, 'profile')

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
      await expect(noticeRow).not.toContainText(String.fromCharCode(0xe000))
      await expect(noticeRow).not.toContainText(String.fromCharCode(0xe001))
      await expect(noticeRow).not.toContainText(expiresAt)

      expect(new URL(page.url()).pathname).toBe(organizationPath('/profile'))

      await confirmOwnerTransfer(invitee, OWNED_NAME)

      await openOrganizationPage(page, 'invitations')
      await openOrganizationPage(page, 'profile')

      const owner = definitionValue(
        page,
        copy('adminUi', 'tenantProfile.field.owner'),
      )
      await expect(owner).toHaveText(
        `${e2eInvitee.displayName} (${e2eInvitee.username})`,
      )
      await expect(transferOwnerButton(page)).toHaveCount(0)
      await expect(deleteOrganizationButton(page)).toHaveCount(0)

      await openOrganizationPage(page, 'invitations')
      const callerMembers = membersSection(page).getByRole('table')
      const callerRoles = await columnIndex(
        callerMembers,
        copy('adminUi', 'members.column.roles'),
      )
      const retained = memberRow(page, e2eTenantAdmin.displayName)
      await expect(retained).toHaveCount(1)
      await expect(cell(retained, callerRoles)).toContainText('tenant-admin')

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
      const previous = memberRow(invitee, e2eTenantAdmin.displayName)
      await expect(previous).toHaveCount(1)
      await expect(cell(previous, rolesColumn)).toContainText('tenant-admin')

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

      const link = await issueInvitation(page, server, e2eInvitee.identifier)
      inviteeContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      const invitee = await inviteeContext.newPage()
      await acceptInvitationAsInvitee(invitee, link)

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

      expect(new URL(other.url()).pathname).toBe(organizationPath('/profile'))
      await confirmOwnerTransfer(invitee, OWNED_NAME)

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

    const menu = await openAccountMenu(page)
    await expect(organizationRow(page, OWNED_NAME)).toBeVisible()
    await organizationRow(page, OWNED_NAME).click()
    await menu.getByRole('menuitem', { name: 'acme', exact: true }).click()
    await expect(homeHeading(page)).toBeVisible()

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
