/**
 * The workspace administration surface's rendered landmarks.
 *
 * The workspace pages are the shared admin-ui pages mounted inside the tenant
 * frame at `/workspace/admin/*`. The host's entry is a navigation parent (a
 * disclosure button) plus a start card; there is no separate header entry.
 * These helpers address that frame and the sections the pages render, so a
 * journey names a landmark once instead of repeating its role query.
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

/**
 * The workspace admin entry: the navigation parent, a disclosure button. Its
 * presence and its `aria-expanded` state are the entry's own signals; the
 * children are real links under it.
 */
export function workspaceEntry(page: Page): Locator {
  return mainNavigation(page).getByRole('button', {
    name: copy('demo', 'nav.workspaceAdmin'),
    exact: true,
  })
}

/** The home page's workspace admin card, as the anchor it is. */
export function workspaceHomeCard(page: Page): Locator {
  return page
    .getByRole('heading', {
      level: 2,
      name: copy('demo', 'home.cards.workspaceAdmin.title'),
      exact: true,
    })
    .locator('xpath=ancestor::a[1]')
}

/** The URL of one workspace page, relative to the deployment root. */
export function workspacePath(path: string): string {
  return `/workspace/admin${path}`
}

/** The workspace pages, each by its navigation label and page title. */
export type WorkspacePageKey = 'invitations' | 'sso' | 'roles' | 'settings'

const WORKSPACE_PAGES: Record<
  WorkspacePageKey,
  { readonly nav: string; readonly title: string }
> = {
  invitations: {
    nav: 'workspace.nav.invitations',
    title: 'invitations.title',
  },
  sso: { nav: 'workspace.nav.sso', title: 'sso.title' },
  roles: { nav: 'workspace.nav.roles', title: 'permissions.title' },
  settings: { nav: 'workspace.nav.settings', title: 'settings.title' },
}

/**
 * Open one workspace page through the navigation parent's child link,
 * expanding the parent first if it is collapsed, and wait for the page's h1.
 */
export async function openWorkspacePage(
  page: Page,
  key: WorkspacePageKey,
): Promise<void> {
  const entry = workspaceEntry(page)
  await expect(entry).toBeVisible()
  if ((await entry.getAttribute('aria-expanded')) === 'false') {
    await entry.click()
  }
  const target = WORKSPACE_PAGES[key]
  await mainNavigation(page)
    .getByRole('link', { name: copy('adminUi', target.nav), exact: true })
    .click()
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
 * The entry and the home card are both gone. The navigation children are
 * asserted too: the parent is their only container, so a parent that a
 * regression hides while leaving a stray link behind still fails.
 */
export async function expectWorkspaceEntryAbsent(page: Page): Promise<void> {
  await expect(workspaceEntry(page)).toHaveCount(0)
  await expect(workspaceHomeCard(page)).toHaveCount(0)
  await expect(
    mainNavigation(page).getByRole('link', {
      name: copy('adminUi', 'workspace.nav.invitations'),
      exact: true,
    }),
  ).toHaveCount(0)
}

/** The entry and the home card are both offered. */
export async function expectWorkspaceEntryPresent(page: Page): Promise<void> {
  await expect(workspaceEntry(page)).toBeVisible()
  await expect(workspaceHomeCard(page)).toBeVisible()
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
