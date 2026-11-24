import { expect, type Locator, type Page } from '@playwright/test'

import { copy } from './i18n'
import { openAccountMenu } from './organization-admin.js'
import type { AccountMenuSurface } from './organization-admin.js'

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&')
}

export async function expectAccountMenuName(
  trigger: Locator,
  identity: string,
): Promise<void> {
  // An unread badge's count joins the trigger text between glyph and identity; strip it before reading the glyph.
  const text = (await trigger.textContent())?.trim() ?? ''
  expect(text.endsWith(identity), 'the trigger renders the identity').toBe(
    true,
  )
  const glyph = text
    .slice(0, text.length - identity.length)
    .trim()
    .replace(/\s*\d+$/, '')
  expect(glyph, 'the trigger renders the initials glyph').not.toBe('')
  await expect(trigger).toHaveAccessibleName(
    new RegExp(
      `^${escapeRegExp(glyph)} ${escapeRegExp(identity)}(?:[,，] .+)?$`,
    ),
  )
}

export { closeAccountMenu, openAccountMenu } from './organization-admin.js'
export type { AccountMenuSurface } from './organization-admin.js'

export function adminEntry(page: Page): Locator {
  return page
    .getByRole('menu')
    .getByRole('menuitem', { name: copy('demo', 'nav.admin'), exact: true })
}

export function backToApp(page: Page): Locator {
  return page
    .getByRole('menu')
    .getByRole('menuitem', {
      name: copy('demo', 'admin.backToApp'),
      exact: true,
    })
}

export function organizationRow(page: Page, name: string): Locator {
  return page
    .getByRole('menu')
    .getByRole('menuitem', { name, exact: true })
}

export async function switchOrganization(
  page: Page,
  name: string,
  surface: AccountMenuSurface = 'sidebar',
): Promise<void> {
  const menu = await openAccountMenu(page, surface)
  await menu.getByRole('menuitem').first().click()
  await menu.getByRole('menuitem', { name, exact: true }).click()
  await expect(page.getByRole('menu')).toHaveCount(0)
}
