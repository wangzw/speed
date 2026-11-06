/**
 * Moving the tenant area to one organization through the account menu.
 *
 * The menu's organization row names the organization the account is acting in
 * and carries `aria-expanded`, which opens the roster of the others; a pick
 * closes the menu, switches, and announces the confirmation. The roster lists
 * only the organizations that are NOT current, so a target absent from it is
 * the current one.
 *
 * "Already current" is therefore decided from the organization row itself,
 * never from a text scan of the menu: a menu-wide text match would take a
 * merely listed tenant for the current one and close the menu without
 * switching. An account with a single organization renders that row as plain
 * context without the expansion control, so the row's position -- the menu's
 * first entry -- identifies it, and the target has to be the name it carries.
 */

import { expect, type Locator, type Page } from '@playwright/test'

import { copy } from './i18n'
import { closeAccountMenu, openAccountMenu } from './organization-admin.js'
import { expectClippedAnnouncement } from './tenant-switch'

/**
 * The account menu's current organization row: the only entry that carries
 * `aria-expanded`. Empty when the account may enter one organization, whose
 * row is context rather than a control.
 */
function currentOrganizationRow(menu: Locator): Locator {
  return menu.locator('[role="menuitem"][aria-expanded]')
}

/**
 * Move the tenant area to one organization through the account menu.
 *
 * The caller continues from the switched surface when the target was not
 * current. A target the account cannot enter fails here rather than passing as
 * "already current": the row's accessible name proves it is the target when the
 * roster does not list it.
 */
export async function switchToTenant(
  page: Page,
  tenant: string,
): Promise<void> {
  const menu = await openAccountMenu(page)
  const currentRow = currentOrganizationRow(menu)
  if ((await currentRow.count()) === 0) {
    // A lone organization has no expansion control: its row is context, not a
    // pick. Locate it by its position rather than by a text scan of the menu,
    // so the decision never depends on where a tenant's name happens to appear.
    const loneRow = menu.locator(':scope > div').first()
    await expect(loneRow).toHaveCount(1)
    await expect(loneRow).toContainText(tenant)
    await closeAccountMenu(page)
    return
  }
  await expect(currentRow).toHaveCount(1)
  if ((await currentRow.getAttribute('aria-expanded')) !== 'true') {
    await currentRow.click()
  }
  const others = menu.getByRole('group', {
    name: copy('demo', 'userMenu.organizations'),
    exact: true,
  })
  await expect(others).toBeVisible()
  const row = others.getByRole('menuitem', { name: tenant, exact: true })
  if ((await row.count()) === 0) {
    // The roster lists only the other organizations, so a target absent from it
    // is the current one. Prove that from the row's accessible name rather than
    // assuming it: a target the account cannot enter fails here instead of
    // passing as "already current".
    await expect(currentRow).toHaveAccessibleName(tenant)
    await closeAccountMenu(page)
    return
  }
  await row.click()
  await expectClippedAnnouncement(
    page,
    copy('tenancyUi', 'tenantSwitcher.switchedTo', { tenant }),
  )
  await expect(page.getByRole('menu')).toHaveCount(0)
}
