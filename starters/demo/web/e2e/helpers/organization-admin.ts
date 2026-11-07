/**
 * The organization administration surface's rendered landmarks.
 *
 * The organization pages are the shared admin-ui pages mounted inside the tenant
 * frame at `/organization/admin/*`. The host's entry is a group of links inside
 * the account menu, plus a start card on the tenant home; the sidebar carries
 * no organization navigation. These helpers open the account menu, address the
 * group and the sections the pages render, so a journey names a landmark once
 * instead of repeating its role query.
 *
 * Every expected string comes from the shipped bundles through `copy`; a
 * renamed label or section title fails in the helper rather than silently
 * matching stale copy.
 */
import { expect, type Locator, type Page } from '@playwright/test'

import { copy, copyPattern } from './i18n'

/** The tenant home's heading. */
export function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

/** The tenant area's main navigation landmark. */
export function mainNavigation(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('layoutKit', 'appShell.navLabel'),
    exact: true,
  })
}

/** The two account-menu surfaces. The sidebar's row at the bottom of the
 * navigation column is the deployment's default -- it is the one surface both
 * shell shapes render -- and the header's avatar opens the same shared menu
 * in the top-bar shape. */
export type AccountMenuSurface = 'header' | 'sidebar'

/** The account-menu surfaces the running shell renders: the row is in every
 * shape, the header avatar only in the top-bar shape. */
async function renderedSurfaces(
  page: Page,
): Promise<readonly AccountMenuSurface[]> {
  return (await page.getByTestId('user-menu-trigger').count()) > 0
    ? ['header', 'sidebar']
    : ['sidebar']
}

/** The account menu's trigger on one surface. */
function accountMenuTrigger(
  page: Page,
  surface: AccountMenuSurface,
): Locator {
  return page.getByTestId(
    surface === 'header' ? 'user-menu-trigger' : 'account-entry-trigger',
  )
}

/**
 * Open one surface's account menu and leave it open for the caller's
 * assertions. The trigger's own `aria-expanded` says whether it is already
 * open, so opening an already-open menu (which would toggle it closed) cannot
 * happen.
 */
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

/** Close one surface's account menu if it is open, leaving the page as it was.
 * MUI's Grow transition keeps the `role="menu"` element in the DOM after the
 * trigger reports `aria-expanded="false"`, so the close also waits for the
 * menu to leave: the next open must match exactly one menu under strict mode. */
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

/** The home page's organization admin card, as the anchor it is. */
export function organizationHomeCard(page: Page): Locator {
  return page
    .getByRole('heading', {
      level: 2,
      name: copy('demo', 'home.cards.organizationAdmin.title'),
      exact: true,
    })
    .locator('xpath=ancestor::a[1]')
}

/** The URL of one organization page, relative to the deployment root. */
export function organizationPath(path: string): string {
  return `/organization/admin${path}`
}

/** The organization pages, each by its navigation label and page title. */
export type OrganizationPageKey =
  | 'invitations'
  | 'sso'
  | 'roles'
  | 'settings'
  | 'profile'

/** The organization pages in the order the account menu lists them. */
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

/**
 * The organization admin group inside the account menu, addressed by the name it
 * carries on `aria-label`. The group paints no caption, so this is a role
 * query rather than a text query; scoping it to the menu keeps it off any
 * other group the page may render.
 */
export function organizationGroup(page: Page): Locator {
  return page.getByRole('menu').getByRole('group', {
    name: copy('demo', 'nav.organizationAdmin'),
    exact: true,
  })
}

/** One organization page's entry in the open account menu, as a menuitem link. */
export function organizationMenuItem(
  page: Page,
  key: OrganizationPageKey,
): Locator {
  return page.getByRole('menu').getByRole('menuitem', {
    name: copy('adminUi', ORGANIZATION_PAGES[key].nav),
    exact: true,
  })
}

/**
 * Open one account-menu surface (the header's by default) and return its
 * organization admin entries: one menuitem per page the caller's admission
 * allows, in admission order. The menu stays open for the caller's
 * assertions.
 */
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

/**
 * Open one organization page through its account-menu entry, opening the menu if
 * it is closed, and wait for the page's h1.
 */
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

/** One titled section of a page, as the region SectionCard renders. */
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

/** The members section's search field. */
export function memberSearchField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('adminUi', 'members.search.label'),
    exact: true,
  })
}

/** The members section's role filter. */
export function memberRoleFilter(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'members.filter.roleLabel'),
    exact: true,
  })
}

/**
 * One member table row, found by the display name it renders. The match is an
 * exact text cell, not a substring of the row: a name that another row's cell
 * happens to contain must not answer for it.
 */
export function memberRow(page: Page, displayName: string): Locator {
  return membersSection(page)
    .getByRole('table')
    .locator('tbody tr')
    .filter({ has: page.getByText(displayName, { exact: true }) })
}

/**
 * One member row's Manage control. The row is found by the display name it
 * shows, and the control's accessible name is matched against the bundle's
 * sentence with its variable left open: the product names the member by the
 * composed display-name and username label, which this helper does not
 * reproduce. A control whose sentence moved still fails here.
 */
export function manageMemberButton(page: Page, displayName: string): Locator {
  return memberRow(page, displayName).getByRole('button', {
    name: copyPattern('adminUi', 'members.manageAria', { name: '[^"]*' }),
  })
}

/** The member management panel. */
export function memberPanel(page: Page): Locator {
  return page.getByRole('dialog', {
    name: copy('adminUi', 'members.panel.title'),
    exact: true,
  })
}

/** The invitations section's status filter. */
export function invitationStatusFilter(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'invitations.filter.label'),
    exact: true,
  })
}

/**
 * One invitation table row, found by the address it renders. The match is an
 * exact address cell: `invitee@demo.test` must not answer for
 * `resent-invitee@demo.test`.
 */
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

/** The selected role's read-only usage section. */
export function usageSection(page: Page): Locator {
  return section(page, 'permissions.usage.title')
}

/**
 * Expand the permission catalog reference and return its section. The
 * reference is collapsed by default; its toggle lives in the section's own
 * actions, and `aria-expanded` is what proves the content region opened.
 */
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

/**
 * The index of the column headed `header` in a table. The header copy comes
 * from the shipped bundle, so a renamed column fails here rather than matching
 * a stale literal.
 */
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

/** The cell at `index` in one table row. */
export function cell(row: Locator, index: number): Locator {
  return row.locator('td').nth(index)
}

/**
 * The account identifiers the page's own tenant reads answer, for the leak
 * assertions below. The console resolves a member's or an inviter's identity
 * from the read it already makes, so those responses are where the internal
 * account ids are learned: the pages must never render them. The collection is
 * passive -- it listens to the product's own reads and issues none.
 */
export interface TenantAccountIdReads {
  /** Wait for every read seen so far to finish parsing and answer its ids. */
  readonly settled: () => Promise<readonly string[]>
}

/** One tenant member row's identity fields, as the read answers them. */
interface MemberReadRow {
  readonly account: string
}

/** One invitation row's issuer, as the read answers it. */
interface InvitationReadRow {
  readonly invited_by: string
}

/**
 * Collect the account ids from the member and invitation reads the page makes
 * for `tenant`. The invitation issuer is empty for a host command, which names
 * no account and therefore contributes nothing.
 */
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
      // A read may answer while the previous batch is still parsing, so wait
      // until no new one arrived during a pass.
      for (;;) {
        const seen = pending.length
        await Promise.all(pending)
        if (seen === pending.length) break
      }
      return [...ids]
    },
  }
}

/**
 * Assert that a rendered surface carries none of `accountIds` in its text or
 * any attribute value. `outerHTML` is the whole of what the browser renders for
 * the element: text nodes, attribute values and every descendant. An empty id
 * list is a harness error rather than a vacuous pass -- a page whose reads
 * answered no account would otherwise satisfy the assertion without evidence.
 */
export async function expectNoAccountIds(
  scope: Locator,
  accountIds: readonly string[],
): Promise<void> {
  if (accountIds.length === 0) {
    throw new Error(
      'no account identifiers were read, so the leak assertion has nothing to look for',
    )
  }
  const rendered = await scope.evaluate((element) => element.outerHTML)
  for (const id of accountIds) {
    expect(
      rendered,
      `the surface rendered the account identifier ${id}`,
    ).not.toContain(id)
  }
}

/** The tenant switcher's trigger, named by the current organization. */
export function tenantSwitcher(page: Page, name: string): Locator {
  return page
    .getByRole('banner')
    .getByRole('button', { name, exact: true })
}

/**
 * Move the tenant area to `target` through the switcher. `current` names the
 * trigger as it stands; the target row is waited for, because the caller
 * tenant list may still be settling when the switcher opens.
 */
export async function switchTenant(
  page: Page,
  current: string,
  target: string,
): Promise<void> {
  const trigger = tenantSwitcher(page, current)
  await expect(trigger).toBeVisible()
  await trigger.click()
  const row = page.getByRole('menuitem', { name: target, exact: true })
  await expect(row).toBeVisible({ timeout: 15_000 })
  await row.click()
}

/**
 * The organization entry and the home card are both gone. The sidebar is asserted
 * to carry no organization control either: the entry moved into the account menu,
 * so a regression that brings the old navigation-column control back must
 * fail. Both
 * account menus are then opened in turn -- the header's and the sidebar's --
 * because they share one list and an absence in only one of them is a live
 * regression. The sidebar and card checks run before a menu opens, because an
 * open menu's backdrop hides the rest of the app from role queries and would
 * make the absence assertions pass vacuously.
 */
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

/**
 * The account menu's organization group and the home card are both offered on
 * every account-menu surface the shell renders: the group itself (by its
 * accessible name), one entry per page, and the start card. Each surface is
 * opened, asserted and closed in turn, because the surfaces share one list and
 * an entry present in only one of them is a live regression. The menus are closed before returning so
 * the caller's next click lands on the page rather than the menu's backdrop.
 */
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

/** The email-verification notice under the app bar. */
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

/** The account page's email-verification section. */
export function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
    exact: true,
  })
}

/** Escape the characters a regular expression reads as syntax. */
export function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
