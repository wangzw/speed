/**
 * The account menu: what its trigger is named, and the entries inside it.
 *
 * The trigger shows the signed-in principal's initials and carries an
 * accessible name that must contain what it shows (WCAG 2.5.3 Label in Name)
 * together with the identity it stands for. The glyph is read off the trigger
 * rather than re-derived here: the assertion states the rule, so it keeps
 * holding if the initials derivation changes.
 *
 * The bar carries no controls of its own any more: the organization switcher,
 * the platform admin entry, the admin area's way back to the tenant area and
 * the notification count all live inside the account menu that both surfaces
 * (the header avatar and the sidebar row) open. Each entry helper names one
 * row; a caller opens the menu first -- `openAccountMenu` in
 * organization-admin.ts, re-exported below -- and dismisses it with
 * `closeAccountMenu`. Every expected string comes from the shipped bundles
 * through `copy`, so a renamed label fails here rather than silently matching
 * stale copy.
 */

import { expect, type Locator, type Page } from '@playwright/test'

import { copy } from './i18n'
import { openAccountMenu } from './organization-admin.js'
import type { AccountMenuSurface } from './organization-admin.js'

/** Escapes a literal for use inside a regular expression. */
function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&')
}

/** Asserts the trigger is named "«glyph» «identity»" for the identity shown. */
export async function expectAccountMenuName(
  trigger: Locator,
  identity: string,
): Promise<void> {
  // The entry renders the initials and the identity side by side: the text
  // strips the identity off the end to read the glyph, and the accessible
  // name is that glyph, a space, and the identity.
  const text = (await trigger.textContent())?.trim() ?? ''
  expect(text.endsWith(identity), 'the trigger renders the identity').toBe(
    true,
  )
  const glyph = text
    .slice(0, text.length - identity.length)
    .trim()
    // The unread badge renders inside the same trigger, between the glyph and
    // the identity, so its count lands in the text too. It is not part of the
    // glyph: the name carries it through the notification phrase instead.
    .replace(/\s*\d+$/, '')
  expect(glyph, 'the trigger renders the initials glyph').not.toBe('')
  // An unread count appends its phrase to the same name (the badge is visible
  // text, so it belongs there). The assertion therefore pins the glyph and
  // the identity as the name's opening and allows that phrase after them.
  await expect(trigger).toHaveAccessibleName(
    new RegExp(
      `^${escapeRegExp(glyph)} ${escapeRegExp(identity)}(?:[,，] .+)?$`,
    ),
  )
}

export { closeAccountMenu, openAccountMenu } from './organization-admin.js'
export type { AccountMenuSurface } from './organization-admin.js'

/** The platform admin entry, inside the tenant area's account menu. */
export function adminEntry(page: Page): Locator {
  return page
    .getByRole('menu')
    .getByRole('menuitem', { name: copy('demo', 'nav.admin'), exact: true })
}

/** The admin area's way back to the tenant area, inside its account menu. */
export function backToApp(page: Page): Locator {
  return page
    .getByRole('menu')
    .getByRole('menuitem', {
      name: copy('demo', 'admin.backToApp'),
      exact: true,
    })
}

/** The account menu's organization row: the switcher's current value, named by
 * the organization the account is acting in. */
export function organizationRow(page: Page, name: string): Locator {
  return page
    .getByRole('menu')
    .getByRole('menuitem', { name, exact: true })
}

/**
 * Move the current organization to another one the account may enter.
 *
 * The organization row is the account menu's first entry and names the
 * organization the account is acting in; expanding it lists the others. The
 * pick closes the menu, so the caller continues from the switched surface.
 */
export async function switchOrganization(
  page: Page,
  name: string,
  surface: AccountMenuSurface = 'header',
): Promise<void> {
  const menu = await openAccountMenu(page, surface)
  await menu.getByRole('menuitem').first().click()
  await menu.getByRole('menuitem', { name, exact: true }).click()
  await expect(page.getByRole('menu')).toHaveCount(0)
}
