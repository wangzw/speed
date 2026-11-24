// The roster lists only the organizations that are NOT current, so a target absent from it is the current one. Decide that from the organization row's accessible name, never from a menu-wide text scan.

import { expect, type Locator, type Page } from '@playwright/test'

import { copy } from './i18n'
import { closeAccountMenu, openAccountMenu } from './organization-admin.js'
import { expectClippedAnnouncement } from './tenant-switch'

function currentOrganizationRow(menu: Locator): Locator {
  return menu.locator('[role="menuitem"][aria-expanded]')
}

export async function switchToTenant(
  page: Page,
  tenant: string,
): Promise<void> {
  const menu = await openAccountMenu(page)
  const currentRow = currentOrganizationRow(menu)
  if ((await currentRow.count()) === 0) {
    // A lone organization renders a named group, not a pick; locate its row by position and prove the target by accessible name.
    const loneRow = menu.locator(':scope > div').first()
    await expect(loneRow).toHaveCount(1)
    await expect(loneRow).toHaveAccessibleName(tenant)
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
    // Absent from the roster means current; prove it by the row's accessible name so an unenterable target fails.
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
