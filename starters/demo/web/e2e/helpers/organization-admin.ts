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

import { copy } from './i18n'

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

/** The two account-menu surfaces. The header's avatar is the deployment's
 * primary; the sidebar's row at the bottom of the navigation column opens the
 * same shared menu. */
export type AccountMenuSurface = 'header' | 'sidebar'

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
  surface: AccountMenuSurface = 'header',
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
  surface: AccountMenuSurface = 'header',
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
export type OrganizationPageKey = 'invitations' | 'sso' | 'roles' | 'settings'

/** The organization pages in the order the account menu lists them. */
export const ORGANIZATION_PAGE_KEYS: readonly OrganizationPageKey[] = [
  'invitations',
  'sso',
  'roles',
  'settings',
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
  roles: { nav: 'organization.nav.roles', title: 'permissions.title' },
  settings: { nav: 'organization.nav.settings', title: 'settings.title' },
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
  surface: AccountMenuSurface = 'header',
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

export function rolesSection(page: Page): Locator {
  return section(page, 'permissions.roles.title')
}

export function grantsSection(page: Page): Locator {
  return section(page, 'permissions.grants.title')
}

export function catalogSection(page: Page): Locator {
  return section(page, 'permissions.catalog.title')
}

export function bindingsSection(page: Page): Locator {
  return section(page, 'permissions.bindings.title')
}

export function lookupSection(page: Page): Locator {
  return section(page, 'permissions.lookup.title')
}

export function providerFormSection(page: Page): Locator {
  return section(page, 'sso.provider.title')
}

export function domainsSection(page: Page): Locator {
  return section(page, 'sso.domains.title')
}

/** The settings page's namespace navigation. */
export function settingsNamespaces(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'settings.nav.label'),
    exact: true,
  })
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

/** The tenant switcher's trigger, named by the current organization. */
export function tenantSwitcher(page: Page, name: string): Locator {
  return page
    .getByRole('banner')
    .getByRole('button', { name, exact: true })
}

/**
 * Move the tenant area to `target` through the switcher. `current` names the
 * trigger as it stands; the target row is waited for, because a personal
 * tenant may be backfilled after the frame settles.
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

  for (const surface of ['header', 'sidebar'] as const) {
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
 * both account-menu surfaces: the group itself (by its accessible name), one
 * entry per page, and the start card. Each surface is opened, asserted and
 * closed in turn, because the two share one list and an entry present in only
 * one of them is a live regression. The menus are closed before returning so
 * the caller's next click lands on the page rather than the menu's backdrop.
 */
export async function expectOrganizationEntryPresent(page: Page): Promise<void> {
  await expect(organizationHomeCard(page)).toBeVisible()
  for (const surface of ['header', 'sidebar'] as const) {
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
