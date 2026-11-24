// @journey journey-operator-onboards-tenant.md
// @covers REQ-platform-admin-001, REQ-platform-admin-006, REQ-platform-admin-010, REQ-platform-admin-011, REQ-platform-admin-032, REQ-platform-admin-033, REQ-platform-admin-050, REQ-platform-admin-051, REQ-platform-admin-055, REQ-platform-admin-118, REQ-platform-admin-119, REQ-platform-admin-120, REQ-tenancy-001, REQ-tenancy-002, REQ-tenancy-005, REQ-tenancy-008, REQ-tenancy-011, REQ-tenancy-013, REQ-tenancy-016, REQ-tenancy-020, REQ-tenancy-021, REQ-tenancy-022, REQ-tenancy-058, REQ-tenancy-061, REQ-tenancy-062, REQ-tenancy-064
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { e2eInvitee, e2eOrdinaryMember } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import type { E2EServer } from '../fixtures/server'
import { scaledTimeout } from '../fixtures/time-scale'
import {
  adminEntry,
  backToApp,
  closeAccountMenu,
  openAccountMenu,
  organizationRow,
  switchOrganization,
} from '../helpers/account-menu'
import type { Accounts } from '../helpers/accounts'
import { copy, copyPattern } from '../helpers/i18n'
import { expiryChoices, expectOneOfText } from '../helpers/invitation-expiry'
import { columnIndex } from '../helpers/organization-admin'
import { chooseSelectOption } from '../helpers/select-option'

// The admin tables render their low-priority columns only at desktop widths, and the sidebar takes part of the width; keep the viewport wide enough for every column this spec reads.
test.use({ seedInvitee: true, viewport: { width: 1920, height: 1080 } })

const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

const MASKED_INVITEE = 'i***@demo.test'

const SEEDED_TENANT = 'acme'

const TENANT_NAME = 'globex'

const TENANT_DISPLAY_NAME = 'Globex Labs'

const TENANT_DESCRIPTION = 'A partner organization onboarded by the operator.'
const TENANT_CONTACT_EMAIL = 'hello@globex.test'
const TENANT_WEBSITE = 'https://globex.test'

const ROLE = 'tenant-inviter'
const PERMISSION = 'authn.membership.manage'

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function boundNotice(): string {
  return copy('adminUi', 'permissions.bindings.bound', {
    role: ROLE,
    account: copy('adminUi', 'common.accountReference.nameWithIdentifier', {
      name: OPERATOR.displayName,
      identifier: OPERATOR.username,
    }),
  })
}

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

function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
    exact: true,
  })
}

function adminCard(page: Page): Locator {
  return page
    .getByRole('heading', {
      level: 2,
      name: copy('demo', 'home.cards.admin.title'),
      exact: true,
    })
    .locator('xpath=ancestor::a[1]')
}

function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

function adminLink(page: Page, label: string): Locator {
  return adminNav(page).getByRole('link', { name: label, exact: true })
}

function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'common.tenant'),
    exact: true,
  })
}

function createSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'tenants.create.title'),
    exact: true,
  })
}

function rosterTable(page: Page): Locator {
  return page.getByRole('table', {
    name: copy('uiKit', 'dataTable.ariaLabel'),
    exact: true,
  })
}

function rosterColumns(): readonly string[] {
  return [
    copy('adminUi', 'tenantProfile.field.displayName'),
    copy('adminUi', 'tenants.column.name'),
    copy('adminUi', 'tenants.column.status'),
    copy('adminUi', 'tenants.column.actions'),
  ]
}

function summaryFields(): readonly string[] {
  return [
    copy('adminUi', 'tenantDetail.field.name'),
    copy('adminUi', 'tenantProfile.field.displayName'),
    copy('adminUi', 'tenantDetail.field.status'),
    copy('adminUi', 'tenantProfile.field.description'),
    copy('adminUi', 'tenantProfile.field.contactEmail'),
    copy('adminUi', 'tenantProfile.field.website'),
    copy('adminUi', 'tenantProfile.field.createdAt'),
  ]
}

function rosterRow(page: Page, name: string): Locator {
  return rosterTable(page)
    .locator('tbody tr')
    .filter({ hasText: name })
}

function rosterDisplayName(row: Locator, name: string): Locator {
  return row.getByRole('link', { name, exact: true })
}

function rosterName(row: Locator, name: string): Locator {
  return row.locator(`p[title="${name}"]`)
}

function summarySection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'tenantDetail.summary'),
    exact: true,
  })
}

function summaryValue(page: Page, index: number): Locator {
  return summarySection(page).locator('dd').nth(index)
}

function permissionsSection(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

function invitationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'invitations.section.title'),
    exact: true,
  })
}

function memberSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'members.title'),
    exact: true,
  })
}

function invitationRow(page: Page): Locator {
  return invitationSection(page)
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: e2eInvitee.identifier })
}

function invitationLinkPanel(page: Page, email: string): Locator {
  return page.getByRole('status').filter({
    hasText: copy('adminUi', 'invitations.issued.link', { email }),
  })
}

const EXPIRY_PREFIX = copy('authUi', 'acceptInvitation.expires', { expires: '' })

test('operator-onboards-tenant: an operator registers a tenant, invites its first member, and the member joins', async ({
  demo,
  browser,
}) => {
  const { page, server, accounts } = demo
  let inviteeContext: BrowserContext | undefined
  let inviteePage: Page | undefined
  let acceptanceLink = ''
  let issuedAt = 0
  let issuedAtConfirmed = 0
  let expectedExpiry: string[] = []

  try {
    await test.step('precondition: the operator signs in and proves the account email', async () => {
      await accounts.signIn({
        identifier: OPERATOR.username,
        password: OPERATOR.password,
      })
      const banner = verificationBanner(page, OPERATOR.identifier)
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
        OPERATOR.identifier,
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

    await test.step('step 1: the operator enters the admin from the header entry and sees the grouped navigation', async () => {
      await page
        .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toBeVisible()

      await switchOrganization(page, 'system')
      await expect(adminCard(page)).toBeVisible()
      await openAccountMenu(page)
      await expect(adminEntry(page)).toBeVisible()
      await adminEntry(page).click()
      const nav = adminNav(page)
      await expect(nav).toBeVisible()

      await expect(
        nav.getByRole('button', {
          name: copy('adminUi', 'nav.groups.identity'),
          exact: true,
        }),
      ).toBeVisible()
      for (const label of [
        copy('adminUi', 'dashboard.title'),
        copy('adminUi', 'tenants.title'),
        copy('adminUi', 'settings.title'),
        copy('adminUi', 'audit.title'),
      ]) {
        await expect(adminLink(page, label)).toBeVisible()
      }
    })

    await test.step('step 2: the operator opens the tenant roster and reads the seeded tenant row', async () => {
      await adminLink(page, copy('adminUi', 'tenants.title')).click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'tenants.title'),
          exact: true,
        }),
      ).toBeVisible()

      const table = rosterTable(page)
      await expect(table.getByRole('columnheader')).toHaveText([
        ...rosterColumns(),
      ])
      const rows = table.locator('tbody tr')
      await expect(rows).toHaveCount(5)
      for (const row of await rows.all()) {
        await expect(row.getByRole('cell')).toHaveCount(
          rosterColumns().length,
        )
      }
      const seeded = rosterRow(page, SEEDED_TENANT)
      await expect(seeded).toHaveCount(1)
      await expect(rosterDisplayName(seeded, SEEDED_TENANT)).toBeVisible()
      await expect(rosterName(seeded, SEEDED_TENANT)).toBeVisible()
      await expect(
        seeded.getByText(copy('adminUi', 'tenants.status.active'), {
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 3: the operator registers a new tenant from its own page, and the roster grows by one active row', async () => {
      const rows = rosterTable(page).locator('tbody tr')
      const before = await rows.count()

      await page
        .getByRole('link', {
          name: copy('adminUi', 'tenants.register'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'tenants.create.title'),
          exact: true,
        }),
      ).toBeVisible()

      const form = createSection(page)
      const nameField = form.getByLabel(
        copy('orgCreateUi', 'fields.name'),
        { exact: true },
      )
      const displayNameField = form.getByLabel(
        copy('orgCreateUi', 'fields.displayName'),
        { exact: true },
      )
      const descriptionField = form.getByLabel(
        copy('orgCreateUi', 'fields.description'),
        { exact: true },
      )
      const contactEmailField = form.getByLabel(
        copy('orgCreateUi', 'fields.contactEmail'),
        { exact: true },
      )
      const websiteField = form.getByLabel(
        copy('orgCreateUi', 'fields.website'),
        { exact: true },
      )
      const submit = form.getByRole('button', {
        name: copy('adminUi', 'tenants.create.submit'),
        exact: true,
      })
      const conflict = copy('adminUi', 'errors.tenant.conflict')

      await expect(form.getByLabel(/identifier/i)).toHaveCount(0)
      for (const field of [
        nameField,
        displayNameField,
        descriptionField,
        contactEmailField,
        websiteField,
      ]) {
        await expect(field).toBeVisible()
      }

      await nameField.fill(SEEDED_TENANT)
      await displayNameField.fill('Acme Name Clash')
      await submit.click()
      await expect(page.getByText(conflict, { exact: true })).toBeVisible()

      await nameField.fill(TENANT_NAME)
      await displayNameField.fill(TENANT_DISPLAY_NAME)
      await descriptionField.fill(TENANT_DESCRIPTION)
      await contactEmailField.fill(TENANT_CONTACT_EMAIL)
      await websiteField.fill(TENANT_WEBSITE)
      await submit.click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'tenantDetail.title', { name: TENANT_DISPLAY_NAME }),
          exact: true,
        }),
      ).toBeVisible()
      expect(new URL(page.url()).pathname.endsWith(`/tenants/${TENANT_NAME}`)).toBe(
        true,
      )

      await page
        .getByRole('link', {
          name: copy('adminUi', 'tenants.title'),
          exact: true,
        })
        .first()
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'tenants.title'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(rows).toHaveCount(before + 1)
      const created = rosterRow(page, TENANT_NAME)
      await expect(created).toHaveCount(1)
      await expect(rosterDisplayName(created, TENANT_DISPLAY_NAME)).toBeVisible()
      await expect(rosterName(created, TENANT_NAME)).toBeVisible()
      await expect(
        created.getByText(copy('adminUi', 'tenants.status.active'), {
          exact: true,
        }),
      ).toBeVisible()

      await expect(rows.nth(0)).toContainText(SEEDED_TENANT)
      await expect(rows.nth(1)).toContainText(TENANT_NAME)
    })

    await test.step("step 4: the detail page shows the tenant's name, display name, profile and status", async () => {
      await rosterRow(page, TENANT_NAME)
        .getByRole('link', { name: TENANT_DISPLAY_NAME, exact: true })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'tenantDetail.title', { name: TENANT_DISPLAY_NAME }),
          exact: true,
        }),
      ).toBeVisible()
      expect(new URL(page.url()).pathname.endsWith(`/tenants/${TENANT_NAME}`)).toBe(
        true,
      )

      await expect(summarySection(page).locator('dt')).toHaveText([
        ...summaryFields(),
      ])
      await expect(summaryValue(page, 0)).toHaveText(TENANT_NAME)
      await expect(summaryValue(page, 1)).toHaveText(TENANT_DISPLAY_NAME)
      await expect(summaryValue(page, 2)).toHaveText(
        copy('adminUi', 'tenants.status.active'),
      )
      await expect(summaryValue(page, 3)).toHaveText(TENANT_DESCRIPTION)
      await expect(summaryValue(page, 4)).toHaveText(TENANT_CONTACT_EMAIL)
      await expect(summaryValue(page, 5)).toHaveText(TENANT_WEBSITE)
      await expect(
        page.getByLabel(
          copy('adminUi', 'tenantDetail.actions.renameLabel'),
          { exact: true },
        ),
      ).toHaveCount(0)

      await page
        .getByRole('link', {
          name: copy('adminUi', 'tenantDetail.back'),
          exact: true,
        })
        .click()
      await expect(rosterRow(page, TENANT_NAME)).toHaveCount(1)
      await expect(
        rosterName(rosterRow(page, TENANT_NAME), TENANT_NAME),
      ).toBeVisible()
    })

    await test.step('the operator manages the new tenant through the permissions page', async () => {
      await adminLink(page, copy('adminUi', 'permissions.title')).click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'permissions.title'),
          exact: true,
        }),
      ).toBeVisible()

      await chooseSelectOption(
        tenantSelector(page),
        page.getByRole('option', { name: TENANT_DISPLAY_NAME, exact: true }),
      )

      const roles = permissionsSection(
        page,
        copy('adminUi', 'permissions.roles.title'),
      )
      const roleField = roles.getByLabel(
        copy('adminUi', 'permissions.roles.create.roleLabel'),
        { exact: true },
      )
      await roleField.fill(ROLE)
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
      const roleRow = roles
        .getByRole('table')
        .locator('tbody tr')
        .filter({ hasText: ROLE })
      await expect(roleRow).toHaveCount(1)

      await roleRow
        .getByRole('button', {
          name: copy('adminUi', 'permissions.roles.manageGrantsAria', {
            role: ROLE,
          }),
          exact: true,
        })
        .click()
      const grants = permissionsSection(
        page,
        copy('adminUi', 'permissions.grants.title'),
      )
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
          name: new RegExp(escapeRegExp(PERMISSION)),
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
      await expect(
        grants.getByRole('table').getByText(PERMISSION, { exact: true }),
      ).toBeVisible()

      const bindings = permissionsSection(
        page,
        copy('adminUi', 'permissions.bindings.title'),
      )
      const accountField = bindings.getByRole('combobox', {
        name: copy('adminUi', 'permissions.bindings.accountLabel'),
        exact: true,
      })
      await accountField.click()
      await accountField.fill(OPERATOR.identifier)
      await page
        .getByRole('option', { name: new RegExp(OPERATOR.displayName) })
        .click()
      await bindings
        .getByLabel(copy('adminUi', 'permissions.bindings.roleLabel'), {
          exact: true,
        })
        .fill(ROLE)
      await bindings
        .getByRole('button', {
          name: copy('adminUi', 'permissions.bindings.bind'),
          exact: true,
        })
        .click()
      await expect(
        page.getByText(boundNotice(), { exact: true }),
      ).toBeVisible()
      const bindingsTable = bindings.getByRole('table')
      const bound = bindingsTable
        .locator('tbody tr')
        .filter({ hasText: ROLE })
      await expect(bound).toHaveCount(1)
      await expect(
        bound.getByText(`${OPERATOR.displayName} (${OPERATOR.username})`, {
          exact: true,
        }),
      ).toBeVisible()
      await expect(bound.getByText(ROLE, { exact: true })).toBeVisible()
    })

    await test.step('step 5: the operator opens invitations-and-members and selects the new tenant', async () => {
      await adminLink(page, copy('adminUi', 'invitations.title')).click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'invitations.title'),
          exact: true,
        }),
      ).toBeVisible()

      await expect(
        page.getByRole('heading', {
          name: copy('adminUi', 'invitations.needTenant.title'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(invitationSection(page)).toHaveCount(0)
      await expect(memberSection(page)).toHaveCount(0)

      await chooseSelectOption(
        tenantSelector(page),
        page.getByRole('option', { name: TENANT_DISPLAY_NAME, exact: true }),
      )
      await expect(
        invitationSection(page)
          .getByRole('heading', {
            name: copy('adminUi', 'invitations.empty.filteredTitle'),
            exact: true,
          }),
      ).toBeVisible()
      const members = memberSection(page).getByRole('table')
      const creator = members
        .locator('tbody tr')
        .filter({ hasText: OPERATOR.displayName })
      await expect(creator).toHaveCount(1)
      await expect(
        creator.getByText(copy('adminUi', 'members.source.initial'), {
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 6: the operator issues an invitation and the one-time panel hands out the link', async () => {
      await page
        .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
          exact: true,
        })
        .fill(e2eInvitee.identifier)
      await chooseSelectOption(
        page.getByRole('combobox', {
          name: copy('adminUi', 'invitations.issue.quickLabel'),
          exact: true,
        }),
        page.getByRole('option', {
          name: copy('adminUi', 'invitations.issue.thirtyDays'),
          exact: true,
        }),
      )
      issuedAt = Date.now()
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
      issuedAtConfirmed = Date.now()

      const table = invitationSection(page).getByRole('table')
      for (const header of [
        copy('adminUi', 'invitations.column.email'),
        copy('adminUi', 'invitations.column.status'),
        copy('adminUi', 'invitations.column.role'),
        copy('adminUi', 'invitations.column.invitedBy'),
        copy('adminUi', 'invitations.column.expires'),
        copy('adminUi', 'invitations.column.created'),
      ]) {
        await expect(
          table.getByRole('columnheader', { name: header, exact: true }),
        ).toBeVisible()
      }
      const row = invitationRow(page)
      await expect(row).toBeVisible()
      await expect(
        row.getByText(e2eInvitee.identifier, { exact: true }),
      ).toBeVisible()
      await expect(
        row.getByText(copy('adminUi', 'invitations.status.pending'), {
          exact: true,
        }),
      ).toBeVisible()
      const roleColumn = await columnIndex(
        table,
        copy('adminUi', 'invitations.column.role'),
      )
      const invitedByColumn = await columnIndex(
        table,
        copy('adminUi', 'invitations.column.invitedBy'),
      )
      await expect(row.getByRole('cell').nth(roleColumn)).toHaveText('')
      const invitedByCell = row.getByRole('cell').nth(invitedByColumn)
      await expect(invitedByCell).toHaveText(OPERATOR.displayName)
      await expect(invitedByCell.locator('p[title]')).toHaveAttribute(
        'title',
        `${OPERATOR.displayName} (${OPERATOR.username})`,
      )
      await expect(
        row.getByText(`${OPERATOR.displayName} (${OPERATOR.username})`, {
          exact: true,
        }),
      ).toHaveCount(0)

      await chooseSelectOption(
        page.getByRole('combobox', {
          name: copy('adminUi', 'invitations.filter.label'),
          exact: true,
        }),
        page.getByRole('option', {
          name: copy('adminUi', 'invitations.status.accepted'),
          exact: true,
        }),
      )
      await expect(
        invitationSection(page).getByRole('heading', {
          name: copy('adminUi', 'invitations.empty.filteredTitle'),
          exact: true,
        }),
      ).toBeVisible()
      await chooseSelectOption(
        page.getByRole('combobox', {
          name: copy('adminUi', 'invitations.filter.label'),
          exact: true,
        }),
        page.getByRole('option', {
          name: copy('adminUi', 'invitations.status.pending'),
          exact: true,
        }),
      )
      await expect(invitationRow(page)).toBeVisible()

      const panel = invitationLinkPanel(page, e2eInvitee.identifier)
      await expect(panel).toBeVisible()
      // The delivery outcome sits beside the hand-out line, so match the panel's text as a substring, not an exact node.
      await expect(
        panel.getByText(
          copy('adminUi', 'invitations.issued.link', {
            email: e2eInvitee.identifier,
          }),
        ),
      ).toBeVisible()
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
      const firstLink = ((await value.textContent()) ?? '').trim()

      await panel
        .getByRole('button', {
          name: copy('adminUi', 'invitations.issued.closeAria', {
            tenant: TENANT_NAME,
            email: e2eInvitee.identifier,
          }),
          exact: true,
        })
        .click()
      const closeDialog = page.getByRole('dialog')
      await expect(closeDialog).toBeVisible()
      await closeDialog
        .getByRole('button', {
          name: copy('adminUi', 'invitations.issued.close'),
          exact: true,
        })
        .click()
      await expect(panel).toHaveCount(0)
      await expect(
        page.getByText(firstLink, { exact: true }),
      ).toHaveCount(0)

      await invitationSection(page)
        .getByRole('button', {
          name: copy('adminUi', 'invitations.actionsAria', {
            email: e2eInvitee.identifier,
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
      const resendDialog = page.getByRole('dialog')
      await expect(resendDialog).toBeVisible()
      await resendDialog
        .getByRole('button', {
          name: copy('adminUi', 'common.confirm'),
          exact: true,
        })
        .click()

      const resentPanel = invitationLinkPanel(page, e2eInvitee.identifier)
      await expect(resentPanel).toBeVisible()
      const resentValue = resentPanel.getByText(
        new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
      )
      await expect(resentValue).toBeVisible()
      acceptanceLink = ((await resentValue.textContent()) ?? '').trim()
      expect(acceptanceLink).not.toBe(firstLink)
      await expect(invitationSection(page).getByRole('table').locator('tbody tr')).toHaveCount(1)
      expectedExpiry = expiryChoices(issuedAt, issuedAtConfirmed)
      const expiryColumn = await columnIndex(
        table,
        copy('adminUi', 'invitations.column.expires'),
      )
      await expectOneOfText(
        invitationRow(page).getByRole('cell').nth(expiryColumn),
        expectedExpiry,
      )

      await resentPanel
        .getByRole('button', {
          name: copy('adminUi', 'invitations.issued.closeAria', {
            tenant: TENANT_NAME,
            email: e2eInvitee.identifier,
          }),
          exact: true,
        })
        .click()
      await page
        .getByRole('dialog')
        .getByRole('button', {
          name: copy('adminUi', 'invitations.issued.close'),
          exact: true,
        })
        .click()
      await expect(resentPanel).toHaveCount(0)

      const staleContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      try {
        const stalePage = await staleContext.newPage()
        await stalePage.goto(firstLink)
        await expect(
          stalePage.getByRole('heading', {
            level: 1,
            name: copy('authUi', 'acceptInvitation.missing.title'),
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          stalePage.getByText(
            copy('authUi', 'acceptInvitation.missing.description'),
            { exact: true },
          ),
        ).toBeVisible()
        await expect(
          stalePage.getByRole('button', {
            name: copy('authUi', 'acceptInvitation.accept'),
            exact: true,
          }),
        ).toHaveCount(0)
      } finally {
        await staleContext.close().catch(() => undefined)
      }

    })

    await test.step('step 7: the operator hands the acceptance link to the invitee', async () => {
      expect(acceptanceLink).toMatch(
        new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
      )
      inviteeContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      inviteePage = await inviteeContext.newPage()
    })

    await test.step('step 8: the invitee opens the link without a session, signs in, and sees the invitation summary', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
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
            inviter: OPERATOR.displayName,
            tenant: TENANT_DISPLAY_NAME,
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
        invitee.getByRole('button', {
          name: copy('authUi', 'acceptInvitation.signIn'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        invitee.getByRole('button', {
          name: copy('authUi', 'acceptInvitation.createAccount'),
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
            inviter: OPERATOR.displayName,
            tenant: TENANT_DISPLAY_NAME,
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
      const expiryLine = invitee.getByText(
        new RegExp(`^${escapeRegExp(EXPIRY_PREFIX)}`),
      )
      await expectOneOfText(
        expiryLine,
        expectedExpiry.map((text) => `${EXPIRY_PREFIX}${text}`),
      )
    })

    await test.step('step 9: the invitee accepts, lands on the tenant home and sees the confirmation', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage

      await invitee
        .getByRole('button', {
          name: copy('authUi', 'acceptInvitation.accept'),
          exact: true,
        })
        .click()

      await expect(
        invitee.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toBeVisible()
      expect(new URL(invitee.url()).pathname).toBe('/')

      const dismiss = invitee.getByRole('button', {
        name: copy('demo', 'home.notice.dismiss'),
        exact: true,
      })
      await expect(dismiss).toBeVisible()
      const confirmation = dismiss.locator(
        'xpath=ancestor::div[contains(@class, "MuiAlert-root")][1]',
      )
      await expect(
        confirmation.getByText(copy('demo', 'home.notice.membershipTitle'), {
          exact: true,
        }),
      ).toBeVisible()

      await openAccountMenu(invitee)
      await expect(organizationRow(invitee, TENANT_DISPLAY_NAME)).toBeVisible()
      await expect(organizationRow(invitee, TENANT_DISPLAY_NAME)).toBeEnabled()
      await closeAccountMenu(invitee)
    })

    await test.step('step 10: the organization row names the current organization and its list offers only the others', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage

      const menu = await openAccountMenu(invitee)
      const row = organizationRow(invitee, TENANT_DISPLAY_NAME)
      await expect(row).toBeVisible()
      await row.click()
      const others = menu.getByRole('group', {
        name: copy('demo', 'userMenu.organizations'),
        exact: true,
      })
      await expect(others).toBeVisible()
      await expect(
        others.getByRole('menuitem', {
          name: TENANT_DISPLAY_NAME,
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(
        others.getByRole('menuitem', {
          name: e2eInvitee.displayName,
          exact: true,
        }),
      ).toBeVisible()
      await closeAccountMenu(invitee)

      await invitee.goto(acceptanceLink)
      await expect(
        invitee.getByRole('heading', {
          name: copy('authUi', 'acceptInvitation.member.title', {
            tenant: TENANT_DISPLAY_NAME,
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
      await invitee
        .getByRole('button', {
          name: copy('authUi', 'acceptInvitation.member.open'),
          exact: true,
        })
        .click()
      await expect(
        invitee.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toBeVisible()

      await expect(
        invitee.getByRole('heading', {
          level: 2,
          name: copy('demo', 'home.cards.admin.title'),
          exact: true,
        }),
      ).toHaveCount(0)
      await openAccountMenu(invitee)
      await expect(adminEntry(invitee)).toHaveCount(0)
      await closeAccountMenu(invitee)
    })

    await test.step('step 11: the operator refreshes and sees the invitation accepted and the member listed', async () => {
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'invitations.title'),
          exact: true,
        }),
      ).toBeVisible()
      await chooseSelectOption(
        page.getByRole('combobox', {
          name: copy('adminUi', 'invitations.filter.label'),
          exact: true,
        }),
        page.getByRole('option', {
          name: copy('adminUi', 'invitations.status.accepted'),
          exact: true,
        }),
      )
      await page
        .getByRole('button', {
          name: copy('adminUi', 'common.refresh'),
          exact: true,
        })
        .click()

      await expect(
        invitationRow(page).getByText(
          copy('adminUi', 'invitations.status.accepted'),
          { exact: true },
        ),
      ).toBeVisible()

      const members = memberSection(page).getByRole('table')
      await expect(members.locator('tbody tr')).toHaveCount(2)
      const invited = members
        .locator('tbody tr')
        .filter({ hasText: e2eInvitee.displayName })
      await expect(invited).toHaveCount(1)
      await expect(
        invited.getByText(copy('adminUi', 'members.source.invite'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        invited.getByText(copy('adminUi', 'members.activeYes'), {
          exact: true,
        }),
      ).toBeVisible()
    })
  } finally {
    await inviteeContext?.close().catch(() => undefined)
  }
})


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
    OPERATOR.identifier,
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
}

async function enterAdminFromHome(page: Page): Promise<void> {
  await page
    .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
    .click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('demo', 'home.heading'),
      exact: true,
    }),
  ).toBeVisible()
  await switchOrganization(page, 'system')
  await openAccountMenu(page)
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
}

function transferTenantButton(page: Page, name: string): Locator {
  return rosterRow(page, name).getByRole('button', {
    name: copy('adminUi', 'tenantLifecycle.transferAria', { name }),
    exact: true,
  })
}

function deleteTenantButton(page: Page, name: string): Locator {
  return rosterRow(page, name).getByRole('button', {
    name: copy('adminUi', 'tenantLifecycle.deleteAria', { name }),
    exact: true,
  })
}

function transferDialog(page: Page, name: string): Locator {
  return page.getByRole('dialog', {
    name: copy('adminUi', 'tenantLifecycle.transfer.title', { name }),
    exact: true,
  })
}

function deleteDialog(page: Page, name: string): Locator {
  return page.getByRole('dialog', {
    name: copy('adminUi', 'tenantLifecycle.deleteConfirm.title', { name }),
    exact: true,
  })
}

async function confirmTwice(dialog: Locator, confirmLabel: string): Promise<void> {
  await dialog
    .getByRole('button', { name: confirmLabel, exact: true })
    .click()
  const again = dialog.getByRole('button', {
    name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
    exact: true,
  })
  await expect(again).toBeEnabled()
  await again.click()
}

async function confirmDelete(dialog: Locator, name: string): Promise<void> {
  const field = dialog.getByLabel(
    copy('adminUi', 'tenantLifecycle.deleteConfirm.nameLabel'),
    { exact: true },
  )
  await expect(
    dialog.getByText(
      copy('adminUi', 'tenantLifecycle.deleteConfirm.nameHint', { name }),
      { exact: true },
    ),
  ).toBeVisible()
  const confirm = dialog.getByRole('button', {
    name: copy('adminUi', 'tenantLifecycle.deleteConfirm.confirmLabel'),
    exact: true,
  })
  await expect(confirm).toBeDisabled()
  await field.fill(name.slice(0, -1))
  await expect(confirm).toBeDisabled()
  await field.fill(name)
  await expect(confirm).toBeEnabled()
  await confirmTwice(
    dialog,
    copy('adminUi', 'tenantLifecycle.deleteConfirm.confirmLabel'),
  )
}

test.describe('the platform lifecycle controls', () => {
  test.use({
    seedInvitee: true,
    tenantAdmins: { acme: [OPERATOR.identifier] },
    viewport: { width: 1920, height: 1080 },
  })

  test('operator-onboards-tenant: the operator transfers a tenant owner through the member selector', async ({
    demo,
  }) => {
    test.setTimeout(scaledTimeout(180_000))

    const { page, server, accounts } = demo

    await signInAndVerifyOperator(page, server, accounts)
    await enterAdminFromHome(page)
    await adminLink(page, copy('adminUi', 'tenants.title')).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenants.title'),
        exact: true,
      }),
    ).toBeVisible()

    await transferTenantButton(page, SEEDED_TENANT).click()
    const dialog = transferDialog(page, SEEDED_TENANT)
    await expect(dialog).toBeVisible()
    const member = dialog.getByRole('combobox', {
      name: copy('adminUi', 'tenantLifecycle.transfer.memberLabel'),
      exact: true,
    })
    await expect(member).toBeVisible()
    await member.click()
    await expect(
      page.getByRole('option', {
        name: new RegExp(
          `^${escapeRegExp(e2eOrdinaryMember.displayName)} \\(.+\\)$`,
        ),
      }),
    ).toBeVisible()
    await page
      .getByRole('option', {
        name: new RegExp(
          `^${escapeRegExp(e2eOrdinaryMember.displayName)} \\(.+\\)$`,
        ),
      })
      .click()
    await dialog
      .getByRole('button', {
        name: copy('adminUi', 'tenantLifecycle.transfer.submit'),
        exact: true,
      })
      .click()

    const confirm = page.getByRole('dialog', {
      name: copy('adminUi', 'tenantLifecycle.transfer.confirmTitle', {
        name: SEEDED_TENANT,
      }),
      exact: true,
    })
    await expect(confirm).toBeVisible()
    await confirmTwice(
      confirm,
      copy('adminUi', 'tenantLifecycle.transfer.confirmLabel'),
    )

    await expect(
      page.getByText(
        copyPattern('adminUi', 'tenantLifecycle.transfer.success', {
          name: escapeRegExp(SEEDED_TENANT),
          target: '.+',
        }),
      ),
    ).toBeVisible()
  })

  test('operator-onboards-tenant: with only tenant.manage the operator uses the account selector and a non-member is refused', async ({
    demo,
  }) => {
    test.setTimeout(scaledTimeout(180_000))

    const { page, server, accounts } = demo

    await signInAndVerifyOperator(page, server, accounts)
    await enterAdminFromHome(page)

    await openAccountMenu(page)
    await backToApp(page).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()
    await switchOrganization(page, OPERATOR.displayName)
    await page.goBack()
    await expect(adminNav(page)).toBeVisible()

    await adminLink(page, copy('adminUi', 'tenants.title')).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenants.title'),
        exact: true,
      }),
    ).toBeVisible()

    await transferTenantButton(page, SEEDED_TENANT).click()
    const dialog = transferDialog(page, SEEDED_TENANT)
    await expect(dialog).toBeVisible()

    const accountField = dialog.getByRole('combobox', {
      name: copy('adminUi', 'tenantLifecycle.transfer.accountLabel'),
      exact: true,
    })
    await expect(accountField).toBeVisible()
    await accountField.fill(e2eInvitee.displayName)
    await expect(
      page.getByRole('option', {
        name: new RegExp(
          `^${escapeRegExp(e2eInvitee.displayName)} \\(.+\\)$`,
        ),
      }),
    ).toBeVisible()
    await page
      .getByRole('option', {
        name: new RegExp(
          `^${escapeRegExp(e2eInvitee.displayName)} \\(.+\\)$`,
        ),
      })
      .click()
    await dialog
      .getByRole('button', {
        name: copy('adminUi', 'tenantLifecycle.transfer.submit'),
        exact: true,
      })
      .click()

    const confirm = page.getByRole('dialog', {
      name: copy('adminUi', 'tenantLifecycle.transfer.confirmTitle', {
        name: SEEDED_TENANT,
      }),
      exact: true,
    })
    await expect(confirm).toBeVisible()
    await confirmTwice(
      confirm,
      copy('adminUi', 'tenantLifecycle.transfer.confirmLabel'),
    )

    await expect(
      dialog.getByText(
        copy('adminUi', 'tenantLifecycle.transfer.failedConflict'),
        { exact: true },
      ),
    ).toBeVisible()
  })
})

const LIFECYCLE_TENANT_NAME = 'initrode'
const LIFECYCLE_TENANT_DISPLAY_NAME = 'Initrode Labs'
const LIFECYCLE_TENANT_DESCRIPTION =
  'A disposable organization for the delete branch.'

test.describe('the platform lifecycle delete', () => {
  test.use({ viewport: { width: 1920, height: 1080 } })

  test('operator-onboards-tenant: the operator deletes a tenant and its name cannot be reused', async ({
    demo,
  }) => {
    test.setTimeout(scaledTimeout(180_000))

    const { page, server, accounts } = demo

    await signInAndVerifyOperator(page, server, accounts)
    await enterAdminFromHome(page)
    await adminLink(page, copy('adminUi', 'tenants.title')).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenants.title'),
        exact: true,
      }),
    ).toBeVisible()

    await page
      .getByRole('link', { name: copy('adminUi', 'tenants.register'), exact: true })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenants.create.title'),
        exact: true,
      }),
    ).toBeVisible()
    const form = createSection(page)
    await expect(form.getByLabel(/identifier/i)).toHaveCount(0)
    await form
      .getByLabel(copy('orgCreateUi', 'fields.name'), { exact: true })
      .fill(LIFECYCLE_TENANT_NAME)
    await form
      .getByLabel(copy('orgCreateUi', 'fields.displayName'), { exact: true })
      .fill(LIFECYCLE_TENANT_DISPLAY_NAME)
    await form
      .getByLabel(copy('orgCreateUi', 'fields.description'), { exact: true })
      .fill(LIFECYCLE_TENANT_DESCRIPTION)
    await form
      .getByRole('button', {
        name: copy('adminUi', 'tenants.create.submit'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenantDetail.title', {
          name: LIFECYCLE_TENANT_DISPLAY_NAME,
        }),
        exact: true,
      }),
    ).toBeVisible()
    expect(
      new URL(page.url()).pathname.endsWith(`/tenants/${LIFECYCLE_TENANT_NAME}`),
    ).toBe(true)
    await expect(summarySection(page).locator('dt')).toHaveText([
      ...summaryFields(),
    ])
    await expect(summaryValue(page, 3)).toHaveText(LIFECYCLE_TENANT_DESCRIPTION)
    await page
      .getByRole('link', { name: copy('adminUi', 'tenants.title'), exact: true })
      .first()
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenants.title'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(rosterTable(page).getByRole('columnheader')).toHaveText([
      ...rosterColumns(),
    ])

    await deleteTenantButton(page, LIFECYCLE_TENANT_NAME).click()
    const dialog = deleteDialog(page, LIFECYCLE_TENANT_DISPLAY_NAME)
    await expect(dialog).toBeVisible()
    await confirmDelete(dialog, LIFECYCLE_TENANT_NAME)
    await expect(
      page.getByText(
        copy('adminUi', 'tenantLifecycle.deleteConfirm.success', {
          name: LIFECYCLE_TENANT_NAME,
        }),
        { exact: true },
      ),
    ).toBeVisible()

    const tombstone = rosterRow(page, LIFECYCLE_TENANT_NAME)
    await expect(
      tombstone.getByText(copy('adminUi', 'tenants.status.deleted'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(tombstone.getByRole('cell')).toHaveCount(
      rosterColumns().length,
    )
    await expect(
      transferTenantButton(page, LIFECYCLE_TENANT_NAME),
    ).toHaveCount(0)
    await expect(deleteTenantButton(page, LIFECYCLE_TENANT_NAME)).toHaveCount(0)

    await page
      .getByRole('link', { name: copy('adminUi', 'tenants.register'), exact: true })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenants.create.title'),
        exact: true,
      }),
    ).toBeVisible()
    const again = createSection(page)
    const nameField = again.getByLabel(copy('orgCreateUi', 'fields.name'), {
      exact: true,
    })
    const displayNameField = again.getByLabel(
      copy('orgCreateUi', 'fields.displayName'),
      { exact: true },
    )
    const submit = again.getByRole('button', {
      name: copy('adminUi', 'tenants.create.submit'),
      exact: true,
    })
    const conflict = copy('adminUi', 'errors.tenant.conflict')

    await nameField.fill(LIFECYCLE_TENANT_NAME)
    await displayNameField.fill('Initrode Second')
    await submit.click()
    await expect(page.getByText(conflict, { exact: true })).toBeVisible()
  })
})
