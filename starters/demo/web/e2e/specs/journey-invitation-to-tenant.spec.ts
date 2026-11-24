// @journey journey-invitation-to-tenant.md
// @covers REQ-platform-admin-001, REQ-platform-admin-032, REQ-platform-admin-033, REQ-signin-030, REQ-tenancy-008, REQ-tenancy-011, REQ-tenancy-013, REQ-tenancy-015, REQ-tenancy-016, REQ-tenancy-017, REQ-tenancy-018, REQ-tenancy-019, REQ-tenancy-020, REQ-tenancy-021, REQ-tenancy-035, REQ-tenancy-043, REQ-tenancy-057, REQ-tenancy-078
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { e2eInvitee } from '../fixtures/config'
import { expect, test, type DemoTarget } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import {
  adminEntry,
  closeAccountMenu,
  openAccountMenu,
  organizationRow,
} from '../helpers/account-menu'
import { copy } from '../helpers/i18n'
import {
  expiryChoices,
  expectOneOfText,
} from '../helpers/invitation-expiry'
import {
  collectTenantAccountIds,
  expectNoAccountIds,
} from '../helpers/organization-admin'
import { chooseSelectOption } from '../helpers/select-option'
import { switchToTenant } from '../helpers/switch-tenant'

// The admin invitation table renders its expiry column only at desktop widths, and the sidebar takes part of the width; keep the viewport wide enough for every column this spec reads.
test.use({
  seedInvitee: false,
  invitationMail: false,
  viewport: { width: 1920, height: 1080 },
})

type DemoServer = DemoTarget['server']

const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

const TENANT = 'acme'

const MASKED_INVITEE = 'i***@demo.test'

const EXISTING_MEMBER = 'Demo Member'

const EXISTING_MEMBER_USERNAME = 'member'

const OUTSIDER = {
  username: 'outsider-e2e',
  email: 'outsider@demo.test',
  password: 'outsider-e2e-secret',
  displayName: 'Invitation Outsider',
} as const

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
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

const PLATFORM_TENANT = 'system'

async function openConsole(page: Page): Promise<void> {
  await switchToTenant(page, PLATFORM_TENANT)
  await openAccountMenu(page)
  await expect(adminEntry(page)).toBeVisible()
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
}

function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'common.tenant'),
    exact: true,
  })
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

async function columnIndex(table: Locator, header: string): Promise<number> {
  const headers = table.locator('thead th')
  const count = await headers.count()
  for (let index = 0; index < count; index += 1) {
    if ((await headers.nth(index).innerText()).trim() === header) {
      return index
    }
  }
  throw new Error(`the table has no column headed ${header}`)
}

function invitationRow(page: Page): Locator {
  return invitationSection(page)
    .getByRole('table')
    .locator('tbody tr')
    .filter({ has: page.getByText(e2eInvitee.identifier, { exact: true }) })
}

function invitationLinkPanel(page: Page, email: string): Locator {
  return page.getByRole('status').filter({
    hasText: copy('adminUi', 'invitations.issued.link', { email }),
  })
}

async function proveOperatorEmail(demo: DemoTarget): Promise<void> {
  const { page, server, accounts } = demo
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
}

async function openTenantInvitations(page: Page): Promise<void> {
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

  await switchToTenant(page, PLATFORM_TENANT)
  await expect(adminCard(page)).toBeVisible()
  await openConsole(page)

  await adminNav(page)
    .getByRole('link', {
      name: copy('adminUi', 'invitations.title'),
      exact: true,
    })
    .click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('adminUi', 'invitations.title'),
      exact: true,
    }),
  ).toBeVisible()

  await chooseSelectOption(
    tenantSelector(page),
    page.getByRole('option', { name: TENANT, exact: true }),
  )
}

async function submitInvitation(page: Page, email: string): Promise<void> {
  await page
    .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
      exact: true,
    })
    .fill(email)
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
  await page
    .getByRole('button', {
      name: copy('adminUi', 'invitations.issue.submit'),
      exact: true,
    })
    .click()
}

async function issueInvitation(page: Page, email: string): Promise<void> {
  await submitInvitation(page, email)
  await expect(
    page.getByText(
      copy('adminUi', 'invitations.issued.confirm', { email }),
      { exact: true },
    ),
  ).toBeVisible()
}

async function takeAcceptanceLink(
  page: Page,
  server: DemoServer,
  email: string,
): Promise<string> {
  const panel = invitationLinkPanel(page, email)
  await expect(panel).toBeVisible()
  // The delivery outcome sits beside the hand-out line, so match the panel's text as a substring, not an exact node.
  await expect(
    panel.getByText(
      copy('adminUi', 'invitations.issued.link', { email }),
    ),
  ).toBeVisible()
  await expect(
    panel.getByText(
      copy('adminUi', 'invitations.issued.delivery.not_configured'),
      { exact: true },
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
  const link = ((await value.textContent()) ?? '').trim()
  expect(link).toMatch(
    new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
  )

  await panel
    .getByRole('button', {
      name: copy('adminUi', 'invitations.issued.closeAria', {
        tenant: TENANT,
        email,
      }),
      exact: true,
    })
    .click()
  const closeDialog = page.getByRole('dialog', {
    name: copy('adminUi', 'invitations.issued.closeTitle'),
    exact: true,
  })
  await expect(closeDialog).toBeVisible()
  await closeDialog
    .getByRole('button', {
      name: copy('adminUi', 'invitations.issued.close'),
      exact: true,
    })
    .click()
  await expect(panel).toHaveCount(0)
  await expect(page.getByText(link, { exact: true })).toHaveCount(0)
  return link
}

async function registerAndProve(
  context: BrowserContext,
  link: string,
  identity: {
    readonly username: string
    readonly email: string
    readonly password: string
    readonly displayName: string
  },
  server: DemoServer,
): Promise<Page> {
  const page = await context.newPage()
  const invitationPath = new URL(link).pathname

  await page.goto(link)
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('authUi', 'acceptInvitation.title'),
      exact: true,
    }),
  ).toBeVisible()
  await expect(
    page.getByText(
      copy('authUi', 'acceptInvitation.email', { email: MASKED_INVITEE }),
      { exact: true },
    ),
  ).toBeVisible()
  expect(new URL(page.url()).pathname).toBe(invitationPath)

  await page
    .getByRole('button', {
      name: copy('authUi', 'acceptInvitation.createAccount'),
      exact: true,
    })
    .click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('demo', 'signIn.registerHeading'),
      exact: true,
    }),
  ).toBeVisible()

  await page
    .getByLabel(copy('authUi', 'register.usernameLabel'), { exact: true })
    .fill(identity.username)
  await page
    .getByLabel(copy('authUi', 'register.displayNameLabel'), { exact: true })
    .fill(identity.displayName)
  await page
    .getByLabel(copy('authUi', 'register.emailLabel'), { exact: true })
    .fill(identity.email)
  await page
    .getByLabel(copy('authUi', 'register.passwordLabel'), { exact: true })
    .fill(identity.password)
  await page
    .getByLabel(copy('authUi', 'register.confirmPasswordLabel'), {
      exact: true,
    })
    .fill(identity.password)

  const beforeCode = server.log.mark()
  await page
    .getByRole('button', { name: copy('authUi', 'register.submit'), exact: true })
    .click()

  await expect(
    page.getByRole('region', {
      name: copy('accountUi', 'verification.title'),
      exact: true,
    }),
  ).toBeVisible()
  expect(new URL(page.url()).pathname).toBe('/account/profile')

  const section = verificationSection(page)
  const { code } = await waitForVerificationCode(
    server.log,
    'mail',
    identity.email,
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
    .poll(() => new URL(page.url()).pathname)
    .toBe(invitationPath)
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('authUi', 'acceptInvitation.title'),
      exact: true,
    }),
  ).toBeVisible()
  return page
}

function invitationRowInStatus(page: Page, statusKey: string): Locator {
  return invitationRow(page).filter({
    hasText: copy('adminUi', statusKey),
  })
}

const EXPIRY_PREFIX = copy('authUi', 'acceptInvitation.expires', { expires: '' })

test('invitation-to-tenant: an operator invites an address and the invitee joins the tenant', async ({
  demo,
  browser,
}) => {
  const { page, server } = demo
  const accountIds = collectTenantAccountIds(page, TENANT)
  let inviteeContext: BrowserContext | undefined
  let inviteePage: Page | undefined
  let acceptanceLink = ''
  let issuedAt = 0
  let issuedAtConfirmed = 0
  let expectedExpiry: string[] = []

  try {
    await test.step('precondition: the operator signs in and proves the account email', async () => {
      await proveOperatorEmail(demo)
    })

    await test.step('step 1: the operator opens the invitation-and-members page and selects the tenant', async () => {
      await openTenantInvitations(page)

      await expect(
        invitationSection(page).getByRole('heading', {
          name: copy('adminUi', 'invitations.empty.filteredTitle'),
          exact: true,
        }),
      ).toBeVisible()
      const members = memberSection(page).getByRole('table')
      const nameColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.displayName'),
      )
      const usernameColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.username'),
      )
      const sourceColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.source'),
      )
      const existing = members
        .locator('tbody tr')
        .filter({ hasText: EXISTING_MEMBER })
      await expect(existing).toHaveCount(1)
      await expect(existing.locator('td').nth(nameColumn)).toHaveText(
        EXISTING_MEMBER,
      )
      await expect(existing.locator('td').nth(usernameColumn)).toHaveText(
        EXISTING_MEMBER_USERNAME,
      )
      await expect(existing.locator('td').nth(sourceColumn)).toHaveText(
        copy('adminUi', 'members.source.admin'),
      )

      const ids = await accountIds.settled()
      await expectNoAccountIds(invitationSection(page), ids)
      await expectNoAccountIds(memberSection(page), ids)
    })

    await test.step('step 2: the operator invites the address with a chosen lifetime and sees the confirmation', async () => {
      issuedAt = Date.now()
      await issueInvitation(page, e2eInvitee.identifier)
      issuedAtConfirmed = Date.now()
    })

    await test.step('step 3: the one-time panel hands out the acceptance link until it is closed', async () => {
      acceptanceLink = await takeAcceptanceLink(
        page,
        server,
        e2eInvitee.identifier,
      )
    })

    await test.step('step 4: issuing to the same address again is refused as invitation-pending', async () => {
      const refused = page.waitForResponse(
        (response) =>
          response.request().method() === 'POST' &&
          new URL(response.url()).pathname ===
            `/api/v1/tenants/${TENANT}/invitations`,
      )
      await submitInvitation(page, e2eInvitee.identifier)
      const answer = await refused
      expect(answer.status()).toBe(409)
      const problem = (await answer.json()) as { type?: string }
      expect(problem.type).toBe('urn:speed:authn:invitation-pending')
      await expect(invitationSection(page).getByRole('alert')).toBeVisible()
      await expect(
        invitationRowInStatus(page, 'invitations.status.pending'),
      ).toHaveCount(1)
    })

    await test.step('step 5: the list shows the one pending invitation', async () => {
      const table = invitationSection(page).getByRole('table')
      await expect(table).toBeVisible()

      for (const header of [
        copy('adminUi', 'invitations.column.email'),
        copy('adminUi', 'invitations.column.status'),
        copy('adminUi', 'invitations.column.invitedBy'),
        copy('adminUi', 'invitations.column.expires'),
      ]) {
        await expect(
          table.getByRole('columnheader', { name: header, exact: true }),
        ).toBeVisible()
      }

      const pending = invitationRowInStatus(page, 'invitations.status.pending')
      await expect(pending).toHaveCount(1)
      await expect(
        pending.getByText(e2eInvitee.identifier, { exact: true }),
      ).toBeVisible()
      const invitedByColumn = await columnIndex(
        table,
        copy('adminUi', 'invitations.column.invitedBy'),
      )
      const invitedByCell = pending.getByRole('cell').nth(invitedByColumn)
      await expect(invitedByCell).toHaveText(OPERATOR.displayName)
      await expect(
        invitedByCell.getByTitle(
          `${OPERATOR.displayName} (${OPERATOR.username})`,
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        pending.getByText(`${OPERATOR.displayName} (${OPERATOR.username})`, {
          exact: true,
        }),
      ).toHaveCount(0)

      expectedExpiry = expiryChoices(issuedAt, issuedAtConfirmed)
      const expiryColumn = await columnIndex(
        table,
        copy('adminUi', 'invitations.column.expires'),
      )
      await expectOneOfText(
        pending.getByRole('cell').nth(expiryColumn),
        expectedExpiry,
      )
      await expect(
        invitationRowInStatus(page, 'invitations.status.revoked'),
      ).toHaveCount(0)
    })

    await test.step('step 6: the invitee registers through the link and proves the invited address', async () => {
      inviteeContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      inviteePage = await registerAndProve(
        inviteeContext,
        acceptanceLink,
        {
          username: e2eInvitee.username,
          email: e2eInvitee.identifier,
          password: e2eInvitee.password,
          displayName: e2eInvitee.displayName,
        },
        server,
      )
    })

    await test.step('step 7: the accept screen shows the tenant, the masked invited address and the expiry', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.invitedBy', {
            inviter: OPERATOR.displayName,
            tenant: TENANT,
          }),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.email', { email: MASKED_INVITEE }),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        invitee.getByText(e2eInvitee.identifier, { exact: true }),
      ).toHaveCount(0)
      const expiryLine = invitee.getByText(
        new RegExp(`^${escapeRegExp(EXPIRY_PREFIX)}`),
      )
      await expectOneOfText(
        expiryLine,
        expectedExpiry.map((text) => `${EXPIRY_PREFIX}${text}`),
      )
    })

    await test.step('step 8: the invitee accepts, lands on the tenant home and sees the confirmation', async () => {
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

      const menu = await openAccountMenu(invitee)
      await expect(menu.getByText(TENANT, { exact: true })).toBeVisible()
      if ((await organizationRow(invitee, TENANT).count()) > 0) {
        await organizationRow(invitee, TENANT).click()
        await expect(
          menu
            .getByRole('group', {
              name: copy('demo', 'userMenu.organizations'),
              exact: true,
            })
            .getByRole('menuitem', { name: TENANT, exact: true }),
        ).toHaveCount(0)
      }
      await closeAccountMenu(invitee)
      await expect(organizationRow(invitee, TENANT)).toHaveCount(0)
    })

    await test.step('step 9: a member has no admin entry, and a full-page admin load turns back home', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage

      await expect(
        invitee
          .getByRole('banner')
          .getByRole('link', { name: copy('demo', 'nav.admin'), exact: true }),
      ).toHaveCount(0)
      await openAccountMenu(invitee)
      await expect(adminEntry(invitee)).toHaveCount(0)
      await closeAccountMenu(invitee)
      await expect(
        invitee.getByRole('heading', {
          level: 2,
          name: copy('demo', 'home.cards.admin.title'),
          exact: true,
        }),
      ).toHaveCount(0)

      for (const path of ['/admin', '/admin/settings']) {
        await invitee.goto(`${server.url}${path}`)
        await expect(
          invitee.getByRole('heading', {
            level: 1,
            name: copy('demo', 'home.heading'),
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          invitee.getByRole('navigation', {
            name: copy('adminUi', 'shell.navLabel'),
            exact: true,
          }),
        ).toHaveCount(0)
        await expect(
          invitee.getByRole('progressbar', {
            name: copy('demo', 'admin.accessPending'),
            exact: true,
          }),
        ).toHaveCount(0)
        expect(new URL(invitee.url()).pathname).toBe('/')
      }
    })

    await test.step('step 10: the invitee re-opens the accepted link and meets the member state', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage

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
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.email', { email: MASKED_INVITEE }),
          { exact: true },
        ),
      ).toHaveCount(0)
    })

    await test.step('step 11: the operator sees the invitation accepted and the invitee in the member table', async () => {
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
        invitationRowInStatus(page, 'invitations.status.accepted'),
      ).toHaveCount(1)
      await expect(
        invitationRowInStatus(page, 'invitations.status.revoked'),
      ).toHaveCount(0)

      const members = memberSection(page).getByRole('table')
      await expect(members.locator('tbody tr')).toHaveCount(3)
      const nameColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.displayName'),
      )
      const usernameColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.username'),
      )
      const sourceColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.source'),
      )
      const activeColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.active'),
      )
      const invited = members
        .locator('tbody tr')
        .filter({ hasText: e2eInvitee.displayName })
      await expect(invited).toHaveCount(1)
      await expect(invited.locator('td').nth(nameColumn)).toHaveText(
        e2eInvitee.displayName,
      )
      await expect(invited.locator('td').nth(usernameColumn)).toHaveText(
        e2eInvitee.username,
      )
      await expect(invited.locator('td').nth(sourceColumn)).toHaveText(
        copy('adminUi', 'members.source.invite'),
      )
      await expect(invited.locator('td').nth(activeColumn)).toHaveText(
        copy('adminUi', 'members.activeYes'),
      )

      const ids = await accountIds.settled()
      await expectNoAccountIds(invitationSection(page), ids)
      await expectNoAccountIds(memberSection(page), ids)
    })
  } finally {
    await inviteeContext?.close().catch(() => undefined)
  }
})

test('invitation-to-tenant: an account that proves a different address cannot accept the invitation', async ({
  demo,
  browser,
}) => {
  const { page, server } = demo
  let outsiderContext: BrowserContext | undefined
  let acceptanceLink = ''

  try {
    await test.step('precondition: the operator signs in, proves the account email and opens the invitations page', async () => {
      await proveOperatorEmail(demo)
      await openTenantInvitations(page)
    })

    await test.step('step 1: the operator issues an invitation and the panel hands out the link', async () => {
      await issueInvitation(page, e2eInvitee.identifier)
      acceptanceLink = await takeAcceptanceLink(
        page,
        server,
        e2eInvitee.identifier,
      )
      outsiderContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      expect(acceptanceLink).toMatch(
        new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
      )
    })

    await test.step('step 2: the outsider registers, proves its own address and is refused', async () => {
      if (outsiderContext === undefined) {
        throw new Error('the outsider context was never opened')
      }
      const outsider = await registerAndProve(
        outsiderContext,
        acceptanceLink,
        OUTSIDER,
        server,
      )

      await expect(
        outsider.getByRole('heading', {
          name: copy('authUi', 'acceptInvitation.unproved.title'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        outsider.getByText(
          copy('authUi', 'acceptInvitation.unproved.description', {
            email: MASKED_INVITEE,
          }),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        outsider.getByRole('button', {
          name: copy('authUi', 'acceptInvitation.accept'),
          exact: true,
        }),
      ).toHaveCount(0)
      const inviteToken = new URL(acceptanceLink).pathname.split('/').pop()
      const refused = outsider.waitForResponse(
        (response) =>
          response.request().method() === 'POST' &&
          new URL(response.url()).pathname ===
            `/api/v1/auth/invitations/${inviteToken ?? ''}`,
      )
      await outsider
        .getByRole('button', {
          name: copy('authUi', 'acceptInvitation.unproved.retry'),
          exact: true,
        })
        .click()
      const answer = await refused
      expect(answer.status()).toBe(403)
      const problem = (await answer.json()) as { type?: string }
      expect(problem.type).toBe('urn:speed:authn:invitation-email-unproved')
      await expect(
        outsider.getByRole('heading', {
          name: copy('authUi', 'acceptInvitation.unproved.title'),
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 3: the invitation stays pending and no membership is written', async () => {
      await page
        .getByRole('button', {
          name: copy('adminUi', 'common.refresh'),
          exact: true,
        })
        .click()
      await expect(
        invitationRowInStatus(page, 'invitations.status.pending'),
      ).toHaveCount(1)
      await expect(
        invitationRowInStatus(page, 'invitations.status.accepted'),
      ).toHaveCount(0)

      const members = memberSection(page).getByRole('table')
      await expect(members.locator('tbody tr')).toHaveCount(2)
      await expect(
        members
          .locator('tbody tr')
          .filter({ hasText: OUTSIDER.displayName }),
      ).toHaveCount(0)
      await expect(
        members
          .locator('tbody tr')
          .filter({ hasText: OUTSIDER.username }),
      ).toHaveCount(0)
    })
  } finally {
    await outsiderContext?.close().catch(() => undefined)
  }
})
