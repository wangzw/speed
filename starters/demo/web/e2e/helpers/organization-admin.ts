import { expect, type Locator, type Page } from '@playwright/test'

import { copy, copyPattern } from './i18n'

export function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

export function mainNavigation(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('layoutKit', 'appShell.navLabel'),
    exact: true,
  })
}

export type AccountMenuSurface = 'header' | 'sidebar'

async function renderedSurfaces(
  page: Page,
): Promise<readonly AccountMenuSurface[]> {
  return (await page.getByTestId('user-menu-trigger').count()) > 0
    ? ['header', 'sidebar']
    : ['sidebar']
}

function accountMenuTrigger(
  page: Page,
  surface: AccountMenuSurface,
): Locator {
  return page.getByTestId(
    surface === 'header' ? 'user-menu-trigger' : 'account-entry-trigger',
  )
}

// aria-expanded decides whether to click, so an already-open menu is not toggled closed.
export async function openAccountMenu(
  page: Page,
  surface: AccountMenuSurface = 'sidebar',
): Promise<Locator> {
  const trigger = accountMenuTrigger(page, surface)
  await expect(trigger).toBeVisible()
  if ((await trigger.getAttribute('aria-expanded')) !== 'true') {
    await trigger.click()
  }
  const menu = page.getByRole('menu')
  await expect(menu).toBeVisible()
  return menu
}

// MUI's Grow keeps role=menu in the DOM after aria-expanded=false; wait for it to leave so the next open matches exactly one menu.
export async function closeAccountMenu(
  page: Page,
  surface: AccountMenuSurface = 'sidebar',
): Promise<void> {
  const trigger = accountMenuTrigger(page, surface)
  if ((await trigger.getAttribute('aria-expanded')) === 'true') {
    await page.keyboard.press('Escape')
    await expect(trigger).toHaveAttribute('aria-expanded', 'false')
  }
  await expect(page.getByRole('menu')).toHaveCount(0)
}

export function organizationHomeCard(page: Page): Locator {
  return page
    .getByRole('heading', {
      level: 2,
      name: copy('demo', 'home.cards.organizationAdmin.title'),
      exact: true,
    })
    .locator('xpath=ancestor::a[1]')
}

export function organizationPath(path: string): string {
  return `/organization/admin${path}`
}

export type OrganizationPageKey =
  | 'invitations'
  | 'sso'
  | 'roles'
  | 'settings'
  | 'profile'

export const ORGANIZATION_PAGE_KEYS: readonly OrganizationPageKey[] = [
  'invitations',
  'sso',
  'roles',
  'settings',
  'profile',
]

const ORGANIZATION_PAGES: Record<
  OrganizationPageKey,
  { readonly nav: string; readonly title: string }
> = {
  invitations: {
    nav: 'organization.nav.invitations',
    title: 'invitations.title',
  },
  sso: { nav: 'organization.nav.sso', title: 'sso.title' },
  roles: { nav: 'organization.nav.roles', title: 'organization.nav.roles' },
  settings: { nav: 'organization.nav.settings', title: 'organization.nav.settings' },
  profile: { nav: 'organization.nav.profile', title: 'organization.nav.profile' },
}

// The group paints no caption; address it by its aria-label through a scoped role query.
export function organizationGroup(page: Page): Locator {
  return page.getByRole('menu').getByRole('group', {
    name: copy('demo', 'nav.organizationAdmin'),
    exact: true,
  })
}

export function organizationMenuItem(
  page: Page,
  key: OrganizationPageKey,
): Locator {
  return page.getByRole('menu').getByRole('menuitem', {
    name: copy('adminUi', ORGANIZATION_PAGES[key].nav),
    exact: true,
  })
}

export async function organizationEntry(
  page: Page,
  surface: AccountMenuSurface = 'sidebar',
): Promise<Locator> {
  const menu = await openAccountMenu(page, surface)
  return menu
    .getByRole('group', {
      name: copy('demo', 'nav.organizationAdmin'),
      exact: true,
    })
    .getByRole('menuitem')
}

export async function openOrganizationPage(
  page: Page,
  key: OrganizationPageKey,
): Promise<void> {
  await openAccountMenu(page)
  const target = ORGANIZATION_PAGES[key]
  await organizationMenuItem(page, key).click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('adminUi', target.title),
      exact: true,
    }),
  ).toBeVisible()
}

export function section(page: Page, titleKey: string): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', titleKey),
    exact: true,
  })
}

export function invitationsSection(page: Page): Locator {
  return section(page, 'invitations.section.title')
}

export function membersSection(page: Page): Locator {
  return section(page, 'members.title')
}

export function memberSearchField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('adminUi', 'members.search.label'),
    exact: true,
  })
}

export function memberRoleFilter(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'members.filter.roleLabel'),
    exact: true,
  })
}

// Match an exact text cell, not a substring of the row: a name another row contains must not answer.
export function memberRow(page: Page, displayName: string): Locator {
  return membersSection(page)
    .getByRole('table')
    .locator('tbody tr')
    .filter({ has: page.getByText(displayName, { exact: true }) })
}

// The control's name embeds a composed label this helper does not reproduce, so match the bundle's sentence with its variable left open.
export function manageMemberButton(page: Page, displayName: string): Locator {
  return memberRow(page, displayName).getByRole('button', {
    name: copyPattern('adminUi', 'members.manageAria', { name: '[^"]*' }),
  })
}

export function memberPanel(page: Page): Locator {
  return page.getByRole('dialog', {
    name: copy('adminUi', 'members.panel.title'),
    exact: true,
  })
}

export function invitationStatusFilter(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'invitations.filter.label'),
    exact: true,
  })
}

// Match an exact address cell: invitee@demo.test must not answer for resent-invitee@demo.test.
export function invitationRow(page: Page, email: string): Locator {
  return invitationsSection(page)
    .getByRole('table')
    .locator('tbody tr')
    .filter({ has: page.getByText(email, { exact: true }) })
}

export function rolesSection(page: Page): Locator {
  return section(page, 'permissions.roles.title')
}

export function grantsSection(page: Page): Locator {
  return section(page, 'permissions.grants.title')
}

export function catalogSection(page: Page): Locator {
  return section(page, 'permissions.catalog.title')
}

export function usageSection(page: Page): Locator {
  return section(page, 'permissions.usage.title')
}

// The reference is collapsed by default; aria-expanded proves the content region opened.
export async function expandCatalogSection(page: Page): Promise<Locator> {
  const catalog = catalogSection(page)
  const toggle = catalog.getByRole('button', {
    name: copy('adminUi', 'permissions.catalog.expand'),
    exact: true,
  })
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await toggle.click()
  await expect(
    catalog.getByRole('button', {
      name: copy('adminUi', 'permissions.catalog.collapse'),
      exact: true,
    }),
  ).toHaveAttribute('aria-expanded', 'true')
  return catalog
}

export function providerFormSection(page: Page): Locator {
  return section(page, 'sso.provider.title')
}

export function domainsSection(page: Page): Locator {
  return section(page, 'sso.domains.title')
}

export async function columnIndex(
  table: Locator,
  header: string,
): Promise<number> {
  const headers = table.locator('thead th')
  const count = await headers.count()
  for (let index = 0; index < count; index += 1) {
    if ((await headers.nth(index).innerText()).trim() === header) {
      return index
    }
  }
  throw new Error(`the table has no column headed ${header}`)
}

export function cell(row: Locator, index: number): Locator {
  return row.locator('td').nth(index)
}

// The console learns internal account ids from the reads it already makes; the pages must never render them. This collection listens to the product's own reads and issues none.
export interface TenantAccountIdReads {
  readonly settled: () => Promise<readonly string[]>
}

interface MemberReadRow {
  readonly account: string
}

interface InvitationReadRow {
  readonly invited_by: string
}

export function collectTenantAccountIds(
  page: Page,
  tenant: string,
): TenantAccountIdReads {
  const ids = new Set<string>()
  const pending: Promise<unknown>[] = []
  const membersPath = `/api/v1/tenants/${tenant}/members`
  const invitationsPath = `/api/v1/tenants/${tenant}/invitations`
  page.on('response', (response) => {
    if (response.request().method() !== 'GET' || !response.ok()) {
      return
    }
    const path = new URL(response.url()).pathname
    if (path === membersPath) {
      pending.push(
        response
          .json()
          .then((body: { readonly members: readonly MemberReadRow[] }) => {
            for (const member of body.members) ids.add(member.account)
          })
          .catch(() => undefined),
      )
      return
    }
    if (path === invitationsPath) {
      pending.push(
        response
          .json()
          .then((body: { readonly invitations: readonly InvitationReadRow[] }) => {
            for (const invitation of body.invitations) {
              if (invitation.invited_by !== '') ids.add(invitation.invited_by)
            }
          })
          .catch(() => undefined),
      )
    }
  })
  return {
    settled: async () => {
      // A read may answer while the previous batch is still parsing; wait until no new one arrived during a pass.
      for (;;) {
        const seen = pending.length
        await Promise.all(pending)
        if (seen === pending.length) break
      }
      return [...ids]
    },
  }
}

const IDENTIFIER_ATTRIBUTES = [
  'title',
  'aria-label',
  'aria-description',
  'aria-valuetext',
  'href',
  'value',
  'placeholder',
  'alt',
] as const

// An empty id list is a harness error, not a vacuous pass.
export async function expectNoAccountIds(
  scope: Locator,
  accountIds: readonly string[],
): Promise<void> {
  if (accountIds.length === 0) {
    throw new Error(
      'no account identifiers were read, so the leak assertion has nothing to look for',
    )
  }
  for (const id of accountIds) {
    await expect(
      scope,
      `the surface rendered the account identifier ${id}`,
    ).not.toContainText(id)
    for (const attribute of IDENTIFIER_ATTRIBUTES) {
      await expect(
        scope.locator(`[${attribute}*="${id}"]`),
        `an element's ${attribute} carried the account identifier ${id}`,
      ).toHaveCount(0)
    }
  }
}

export function tenantSwitcher(page: Page, name: string): Locator {
  return page
    .getByRole('banner')
    .getByRole('button', { name, exact: true })
}

// Wait for the target row: the caller tenant list may still be settling when the switcher opens.
export async function switchTenant(
  page: Page,
  current: string,
  target: string,
): Promise<void> {
  const trigger = tenantSwitcher(page, current)
  await expect(trigger).toBeVisible()
  await trigger.click()
  const row = page.getByRole('menuitem', { name: target, exact: true })
  await expect(row).toBeVisible()
  await row.click()
}

// The sidebar and card checks run before a menu opens: an open menu's backdrop hides the rest of the app from role queries. Both account-menu surfaces are checked because they share one list.
export async function expectOrganizationEntryAbsent(page: Page): Promise<void> {
  await expect(organizationHomeCard(page)).toHaveCount(0)
  const navigation = mainNavigation(page)
  for (const role of ['button', 'link'] as const) {
    await expect(
      navigation.getByRole(role, {
        name: copy('demo', 'nav.organizationAdmin'),
        exact: true,
      }),
    ).toHaveCount(0)
  }
  for (const key of ORGANIZATION_PAGE_KEYS) {
    await expect(
      navigation.getByRole('link', {
        name: copy('adminUi', ORGANIZATION_PAGES[key].nav),
        exact: true,
      }),
    ).toHaveCount(0)
  }

  for (const surface of await renderedSurfaces(page)) {
    const entries = await organizationEntry(page, surface)
    await expect(entries).toHaveCount(0)
    await expect(organizationGroup(page)).toHaveCount(0)
    for (const key of ORGANIZATION_PAGE_KEYS) {
      await expect(organizationMenuItem(page, key)).toHaveCount(0)
    }
    await closeAccountMenu(page, surface)
  }
}

// Both surfaces are opened and closed in turn; leaving one open would make the next click hit its backdrop.
export async function expectOrganizationEntryPresent(page: Page): Promise<void> {
  await expect(organizationHomeCard(page)).toBeVisible()
  for (const surface of await renderedSurfaces(page)) {
    const entries = await organizationEntry(page, surface)
    await expect(organizationGroup(page)).toBeVisible()
    await expect(entries).toHaveCount(ORGANIZATION_PAGE_KEYS.length)
    for (const key of ORGANIZATION_PAGE_KEYS) {
      await expect(organizationMenuItem(page, key)).toBeVisible()
    }
    await closeAccountMenu(page, surface)
  }
}

export function verificationBanner(page: Page, email: string): Locator {
  return page.getByRole('region', {
    name: `${copy('demo', 'verificationBanner.region')}: ${copy(
      'demo',
      'verificationBanner.pending',
      { email },
    )}`,
    exact: true,
  })
}

export function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
    exact: true,
  })
}

export function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
